package app

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/httpx"
)

type queueScope struct {
	audience  string
	recipient string
}

type taskCounts struct {
	Pending int64 `json:"pending"`
	Near    int64 `json:"near"`
	Overdue int64 `json:"overdue"`
	Unread  int64 `json:"unread"`
}

type recentSystem struct {
	ID         string `json:"id"`
	Number     string `json:"number"`
	SystemName string `json:"systemName"`
	Status     string `json:"status"`
}

type rectificationCounts struct {
	Open          int64 `json:"open"`
	PendingRetest int64 `json:"pendingRetest"`
	PendingReview int64 `json:"pendingReview"`
	Closed        int64 `json:"closed"`
}

type scopedWorkbench struct {
	Tasks         []overviewTask      `json:"tasks"`
	Counts        taskCounts          `json:"counts"`
	RecentSystems []recentSystem      `json:"recentSystems"`
	Rectification rectificationCounts `json:"rectification"`
}

// registerScopedWorkbenches 挂载机构和运营者在各自办理范围内的工作台。
// 参数 mux 为回环同源路由，pool 为业务数据库，windows 为审核工作日设置。
// 返回值 无。
func registerScopedWorkbenches(mux *http.ServeMux, pool *pgxpool.Pool, windows ReviewWindows) {
	mux.HandleFunc("GET /api/v1/institution/workbench",
		handleScopedWorkbench(pool, windows, "institution", "institution"))
	mux.HandleFunc("GET /api/v1/operator/workbench",
		handleScopedWorkbench(pool, windows, "operator", "organization"))
}

// handleScopedWorkbench 返回当前所选机构或单位的待办、未读和系统摘要。
// 参数 pool 为业务库，windows 为各审核阶段时限，audience 和 key 为办理方与对应参数。
// 返回值 同源工作台 HTTP 处理函数。
func handleScopedWorkbench(pool *pgxpool.Pool, windows ReviewWindows, audience, key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		values, err := url.ParseQuery(r.URL.RawQuery)
		scope := strings.TrimSpace(values.Get(key))
		if err != nil || len(values) != 1 || len(values[key]) != 1 || scope == "" ||
			!utf8.ValidString(scope) || utf8.RuneCountInString(scope) > 200 || strings.ContainsRune(scope, 0) {
			httpx.Error(w, http.StatusBadRequest, "invalid_input", "请选择当前工作台的机构或运营单位。")
			return
		}
		out, err := readScopedWorkbench(r.Context(), pool, windows, queueScope{audience, scope})
		if err != nil {
			slog.Error("业务工作台读取失败", "role", audience)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "暂时无法读取业务工作台。")
			return
		}
		httpx.JSON(w, http.StatusOK, out)
	}
}

// readScopedWorkbench 在相同只读快照中读取当前归属的待办、系统和整改概览。
// 参数 ctx 控制读取，pool 为业务库，windows 为审核时限，scope 为业务办理方和归属。
// 返回值 最小工作台数据或数据库错误。
func readScopedWorkbench(ctx context.Context, pool *pgxpool.Pool,
	windows ReviewWindows, scope queueScope,
) (scopedWorkbench, error) {
	var out scopedWorkbench
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	err := pgx.BeginTxFunc(ctx, pool, options, func(tx pgx.Tx) error {
		var err error
		out.Tasks, out.Counts, _, err = readQueue(ctx, tx, windows, scope)
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, scopedOverviewSQL, scope.audience, scope.recipient).
			Scan(&out.RecentSystems, &out.Rectification.Open, &out.Rectification.PendingRetest,
				&out.Rectification.PendingReview, &out.Rectification.Closed)
	})
	return out, err
}

const taskClockSQL = `
WITH clock AS (
	SELECT (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date AS today
), scoped AS (`

const currentAssigneeSQL = `(w.audience=$1 AND w.recipient COLLATE "C" = $2)`

const retestObserverSQL = `($1='operator' AND w.resource='hazards' AND w.status='pending_retest'
   AND w.audience='institution' AND w.organization COLLATE "C" = $2)`

const taskMarksSQL = taskClockSQL + `
	SELECT w.*, w.audience<>$1 AS monitor
	FROM workflow_tasks w
	WHERE ($1 <> 'operator' OR w.resource <> 'implementations')
	AND (` + currentAssigneeSQL + ` OR ` + retestObserverSQL + `)` + taskDatesSQL

const taskDatesSQL = `
), dated AS (
	SELECT w.*,
	  COALESCE(w.explicit_deadline,workflow_due((w.stage_at AT TIME ZONE 'Asia/Shanghai')::date,
	    CASE w.window_kind WHEN 'filing_initial' THEN $3::int WHEN 'filing_final' THEN $4::int
	         WHEN 'report' THEN $5::int WHEN 'implementation' THEN $6::int WHEN 'remediation' THEN $7::int END
	  )) AS deadline
	FROM scoped w
), marks AS MATERIALIZED (
	SELECT w.*, CASE WHEN deadline<today THEN 'overdue'
	            WHEN deadline<=workflow_due(today,1) THEN 'near'
	            WHEN deadline IS NOT NULL THEN 'upcoming' END AS urgency
	FROM dated w CROSS JOIN clock
)`

const taskQueueSQL = taskMarksSQL + `,
page AS (
	SELECT * FROM marks ORDER BY monitor,deadline NULLS LAST, stage_at,id LIMIT 12
)
SELECT COALESCE((
  SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
    'id',id,'resource',resource,'label',CASE WHEN monitor THEN '待机构复测' ELSE label END,
    'systemName',system_name,'number',number,'status',status,'deadline',deadline,
    'urgency',urgency,'canRemind',CASE WHEN monitor THEN true END
  )) ORDER BY monitor,deadline NULLS LAST, stage_at,id) FROM page
),'[]'::jsonb),
  count(*) FILTER (WHERE NOT monitor),
  count(*) FILTER (WHERE urgency='near'),
  count(*) FILTER (WHERE urgency='overdue'),
  (SELECT count(*) FROM review_notices WHERE audience=$1 AND recipient COLLATE "C" = $2 AND acknowledged_at IS NULL),
  count(*) FILTER (WHERE window_kind<>'' AND deadline<today),
  count(*) FILTER (WHERE window_kind<>'' AND deadline BETWEEN today AND today+10),
  count(*) FILTER (WHERE window_kind<>'' AND deadline BETWEEN today+11 AND today+30)
FROM marks CROSS JOIN clock
`

// readQueue 统计全量待办并返回优先的十二条，不复制业务状态。
// 参数 ctx 控制查询，tx 为工作台读取事务，windows 为审核时限，scope 为当前业务归属。
// 返回值 本页任务、实际待办计数、监管审核日期分档以及查询错误。
func readQueue(ctx context.Context, tx pgx.Tx, windows ReviewWindows, scope queueScope) (
	[]overviewTask, taskCounts, reviewDeadlineCounts, error,
) {
	var tasks []overviewTask
	var counts taskCounts
	var deadlines reviewDeadlineCounts
	err := tx.QueryRow(ctx, taskQueueSQL, scope.audience, scope.recipient, windows.FilingDays,
		windows.FilingFinalDays, windows.ReportDays, windows.ImplementationDays, windows.RemediationDays).
		Scan(&tasks, &counts.Pending, &counts.Near, &counts.Overdue, &counts.Unread,
			&deadlines.Overdue, &deadlines.Near, &deadlines.Upcoming)
	return tasks, counts, deadlines, err
}

const scopedOverviewSQL = `
WITH systems AS (
	SELECT id,number,system_name,
	       CASE WHEN cancelled_at IS NOT NULL THEN 'cancelled' ELSE status END AS status,
	       created_at
	FROM business_cases
	WHERE kind='filing' AND status<>'withdrawn' AND discarded_at IS NULL AND (
	    ($1='operator' AND organization_name COLLATE "C" = $2)
	    OR ($1='institution' AND assessment_org COLLATE "C" = $2)
	)
	ORDER BY created_at DESC,id LIMIT 6
), hazards AS (
	SELECT h.status
	FROM hazard_items h JOIN hazard_context hc ON hc.id=h.id
	WHERE h.status<>'pending_dispatch' AND (
	    ($1='operator' AND hc.organization_name COLLATE "C" = $2)
	    OR ($1='institution' AND hc.assessment_org COLLATE "C" = $2)
	)
)
SELECT COALESCE((SELECT jsonb_agg(jsonb_build_object(
           'id',id,'number',number,'systemName',system_name,'status',status
       ) ORDER BY created_at DESC,id) FROM systems),'[]'::jsonb),
       count(*) FILTER (WHERE status IN ('open','returned')),
       count(*) FILTER (WHERE status='pending_retest'),
       count(*) FILTER (WHERE status='pending_review'),
       count(*) FILTER (WHERE status='closed')
FROM hazards
`
