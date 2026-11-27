package workflowreminders

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Windows 按办理环节保存配置的工作日天数，零值表示不计算该环节的配置期限。
type Windows struct {
	FilingInitial, FilingFinal, Report, Implementation, Remediation int
}

type dueTask struct{ Resource, ID string }

const deadlineDates = `
	WITH business_day AS (
	    SELECT COALESCE(NULLIF($1,'')::date, timezone('Asia/Shanghai',CURRENT_TIMESTAMP)::date) AS today
	), calendar AS MATERIALIZED (
	    SELECT today, workflow_due(today,1) AS next_workday FROM business_day
	), timed AS MATERIALIZED (
	    SELECT t.*, c.today, c.next_workday,
	        COALESCE(t.explicit_deadline, CASE WHEN w.days > 0 THEN
	            workflow_due(timezone('Asia/Shanghai',t.stage_at)::date,w.days) END) AS deadline
	    FROM workflow_tasks t CROSS JOIN calendar c
	    CROSS JOIN LATERAL (SELECT CASE t.window_kind
	        WHEN 'filing_initial'  THEN $2::int
	        WHEN 'filing_final'    THEN $3::int
	        WHEN 'report'         THEN $4::int
	        WHEN 'implementation' THEN $5::int
	        WHEN 'remediation'    THEN $6::int
	        ELSE 0 END AS days) w
    WHERE (t.explicit_deadline IS NOT NULL OR w.days > 0)`

const deadlineRecipients = `
), addressed AS (
	    SELECT *, CASE WHEN audience='regulator' THEN applicant_audience ELSE audience END AS target,
	        CASE WHEN audience<>'regulator' THEN recipient
	             WHEN applicant_audience='institution' THEN institution ELSE organization END AS target_recipient,
	        CASE WHEN deadline < today THEN 'deadline_overdue'
	             WHEN deadline = today THEN 'deadline_due'
	             WHEN deadline <= next_workday THEN 'deadline_due_soon' END AS reason
	    FROM timed
	), unsent AS (
	    SELECT a.* FROM addressed a
	    WHERE a.reason IS NOT NULL AND a.target IN ('institution','operator')
	      AND length(a.target_recipient) BETWEEN 1 AND 200 AND btrim(a.target_recipient)<>''
	      AND NOT EXISTS (
	        SELECT 1 FROM review_notices n
	        WHERE n.resource=a.resource AND n.resource_id=a.id AND n.event_version=a.stage_version
	          AND n.audience=a.target AND n.recipient=a.target_recipient AND n.state=a.reason
	          AND n.workflow_day=a.deadline
	          AND n.state IN ('deadline_due_soon','deadline_due','deadline_overdue')
	      )
	)`

const selectDueTasks = deadlineDates + deadlineRecipients +
	` SELECT resource,id::text FROM unsent ORDER BY resource,id`

const insertDueNotices = deadlineDates + ` AND t.id=$7 AND t.resource=$8` + deadlineRecipients + `
	INSERT INTO review_notices
	    (audience,resource,resource_id,event_version,state,recipient,reference,subject,comment,workflow_day)
	SELECT target,resource,id,stage_version,reason,target_recipient,number,system_name,
	    label || '的办理期限为 ' || to_char(deadline,'YYYY-MM-DD') || '。' ||
	        CASE WHEN audience='regulator' THEN '请关注监管办理进度。' ELSE '请及时处理。' END,
	    deadline
	FROM unsent ON CONFLICT DO NOTHING`

var taskLocks = map[string]string{
	"filings": `SELECT 1 FROM business_cases WHERE id=$1 AND kind='filing' FOR SHARE SKIP LOCKED`,
	"filing-applications": `SELECT 1 FROM filing_applications a JOIN business_cases f ON f.id=a.filing_id
		WHERE a.id=$1 FOR SHARE OF a,f SKIP LOCKED`,
	"reports": `SELECT 1 FROM business_cases r JOIN business_cases f ON f.id=r.filing_id
		WHERE r.id=$1 AND r.kind='report' FOR SHARE OF r,f SKIP LOCKED`,
	"implementations": `SELECT 1 FROM implementation_reports i JOIN business_cases f ON f.id=i.filing_id
		WHERE i.id=$1 FOR SHARE OF i,f SKIP LOCKED`,
	"remediations": `SELECT 1 FROM remediation_plans p JOIN business_cases r ON r.id=p.report_id
		JOIN business_cases f ON f.id=r.filing_id WHERE p.id=$1 FOR SHARE OF p,r,f SKIP LOCKED`,
	"hazards": `SELECT 1 FROM hazard_items h JOIN hazard_context hc ON hc.id=h.id
		JOIN business_cases f ON f.id=hc.filing_id
		JOIN business_cases anchor ON anchor.id=COALESCE(hc.report_id,hc.filing_id)
		WHERE h.id=$1 FOR SHARE OF h,anchor,f SKIP LOCKED`,
	"supervisions": `SELECT 1 FROM supervision_tasks s JOIN business_cases f ON f.id=s.filing_id
		WHERE s.id=$1 FOR SHARE OF s,f SKIP LOCKED`,
	"supervision-site": `SELECT 1 FROM supervision_tasks t
		JOIN supervision_site_inspections s ON s.task_id=t.id JOIN business_cases f ON f.id=t.filing_id
		WHERE t.id=$1 FOR SHARE OF t,s,f SKIP LOCKED`,
}

// Run 每分钟读取在办事项并投递到期提醒，启动时立即扫描一次。
// 参数 ctx 控制后台任务生命周期，pool 为业务库，windows 为各环节的配置时限。
// 返回值 无，上下文取消后退出。
func Run(ctx context.Context, pool *pgxpool.Pool, windows Windows) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for ctx.Err() == nil {
		scan, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := Sweep(scan, pool, windows)
		cancel()
		if err != nil && ctx.Err() == nil {
			var databaseError *pgconn.PgError
			if errors.As(err, &databaseError) {
				slog.Warn("办理期限提醒扫描失败", "sqlstate", databaseError.Code)
			} else {
				slog.Warn("办理期限提醒扫描未完成", "timeout", errors.Is(err, context.DeadlineExceeded))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Sweep 扫描上海业务日内符合条件的事项并幂等投递到期提醒。
// 参数 ctx 控制本轮扫描，pool 为业务库，windows 为各环节的配置时限。
// 返回值 查询错误或单项投递错误，单项失败时继续投递其余事项。
func Sweep(ctx context.Context, pool *pgxpool.Pool, windows Windows) error {
	return sweepOn(ctx, pool, windows, "")
}

// sweepOn 先收集符合提醒条件的事项，再逐条持锁核对并投递。
// 参数 ctx 控制本轮扫描，pool 为业务库，windows 为工作日配置，day 可覆盖上海业务日期。
// 返回值 查询错误或本轮各次投递错误。
func sweepOn(ctx context.Context, pool *pgxpool.Pool, windows Windows, day string) error {
	args := [8]any{day, windows.FilingInitial, windows.FilingFinal, windows.Report,
		windows.Implementation, windows.Remediation}
	rows, err := pool.Query(ctx, selectDueTasks, args[:6]...)
	if err != nil {
		return err
	}
	tasks, err := pgx.CollectRows(rows, pgx.RowToStructByPos[dueTask])
	if err != nil {
		return err
	}
	var failures error
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return errors.Join(failures, err)
		}
		args[6], args[7] = task.ID, task.Resource
		if err := notifyDueTask(ctx, pool, task, args[:]); err != nil {
			failures = errors.Join(failures, err)
		}
	}
	return failures
}

// notifyDueTask 锁定来源及可变归属，按持锁后的业务状态重新计算并登记提醒。
// 参数 ctx 控制投递，pool 为业务库，task 为事项，args 含当前日期、时限和定位参数。
// 返回值 读取或写入错误，事项已移除、正在修改或已发送提醒时返回 nil。
func notifyDueTask(ctx context.Context, pool *pgxpool.Pool, task dueTask, args []any) error {
	query, found := taskLocks[task.Resource]
	if !found {
		return errors.New("deadline reminder resource unsupported")
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var locked int
		err := tx.QueryRow(ctx, query, task.ID).Scan(&locked)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, insertDueNotices, args...)
		return err
	})
}
