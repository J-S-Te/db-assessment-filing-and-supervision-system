package supervision

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"grade-business/internal/access"
)

// create 建立有效备案的监管草稿，以请求标识吸收重复提交。
// 参数 ctx 控制写入，input 为已验证的草稿及可选线索编号。
// 返回值 草稿任务、同请求的已登记任务，或业务和数据库错误。
func (s *Service) create(ctx context.Context, input createInput) (taskSummary, error) {
	var result taskSummary
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		existing, err := findDraftRequest(ctx, tx, input)
		if !errors.Is(err, pgx.ErrNoRows) {
			result = existing
			return err
		}
		if err := checkDraftSources(ctx, tx, input); err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `INSERT INTO supervision_tasks(request_id,filing_id,lead_alert_id,
			filing_number,system_name,organization_name,title,due_on,cycle_year)
			SELECT $1,f.id,NULLIF($4,'')::uuid,f.number,f.system_name,f.organization_name,$2,$3::date,
				extract(year FROM $3::date)::int FROM business_cases f WHERE f.id=$5
			ON CONFLICT (request_id) DO NOTHING RETURNING id::text`,
			input.RequestID, input.Title, input.DueOn, input.LeadAlertID, input.FilingID).Scan(&result.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			result, err = findDraftRequest(ctx, tx, input)
			return err
		}
		if err != nil {
			return classifyCreateError(err)
		}
		created, err := loadTask(ctx, tx, result.ID, "", false)
		if err != nil {
			return err
		}
		result = created.taskSummary
		name, actor := supervisionAuditActor(ctx, s.actor, "")
		_, err = tx.Exec(ctx, `INSERT INTO supervision_events(task_id,version,action,to_status,actor,actor_id,actor_role)
			VALUES($1,1,'create','draft',$2,NULLIF($3,'')::uuid,NULLIF($4,''))`,
			result.ID, name, actor.ID, actor.Role)
		return err
	})
	return result, err
}

// findDraftRequest 复用同一草稿请求，同时拒绝更改请求标识对应条件。
// 参数 ctx、db 为数据环境，input 指用户重试时提供的原始条件。
// 返回值 原任务摘要或重复冲突、未匹配、数据库错误。
func findDraftRequest(ctx context.Context, db reader, input createInput) (taskSummary, error) {
	var existing taskRecord
	err := db.QueryRow(ctx, `SELECT `+taskColumns+` FROM supervision_tasks t WHERE t.request_id=$1 AND `+
		access.FilingDistrictScope("t.filing_id", "$2", false), input.RequestID, access.Districts(ctx)).
		Scan(taskTargets(&existing)...)
	if err != nil {
		return taskSummary{}, err
	}
	var lead string
	if existing.LeadAlertID != nil {
		lead = *existing.LeadAlertID
	}
	if existing.FilingID != input.FilingID || existing.Title != input.Title ||
		existing.DueOn != input.DueOn || lead != input.LeadAlertID {
		return taskSummary{}, errDuplicate
	}
	return existing.taskSummary, nil
}

// checkDraftSources 锁定有效备案并核对期限、可选预警线索和真实收件单位。
// 参数 ctx、tx 为新建事务，input 为请求的备案及截止日期。
// 返回值 业务上允许创建时为空，否则为可识别冲突或数据库错误。
func checkDraftSources(ctx context.Context, tx pgx.Tx, input createInput) error {
	var owner string
	var future bool
	err := tx.QueryRow(ctx, `SELECT f.organization_name,$2::date>(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date
		FROM business_cases f WHERE f.id=$1 AND `+eligibleFiling+" AND "+
		access.FilingDistrictScope("f.id", "$3", true)+" FOR SHARE OF f",
		input.FilingID, input.DueOn, access.Districts(ctx)).
		Scan(&owner, &future)
	if errors.Is(err, pgx.ErrNoRows) {
		return errScope
	}
	if err != nil {
		return err
	}
	if !future {
		return errInput
	}
	return lockLeadSource(ctx, tx, input.LeadAlertID, input.FilingID, owner)
}

// lockLeadSource 确认只能以同一备案当前仍超期的预警登记监督检查。
// 参数 ctx、tx 为监管业务事务，lead、filing、owner 为关联线索及归属。
// 返回值 未选择线索或归属符合时为空，否则为状态或数据库错误。
func lockLeadSource(ctx context.Context, tx pgx.Tx, lead, filing, owner string) error {
	if lead == "" {
		return nil
	}
	var found bool
	err := tx.QueryRow(ctx, `SELECT true FROM alert_supervision_leads l
		JOIN local_alerts a ON a.id=l.alert_id JOIN hazard_items h ON h.id=a.hazard_id
		JOIN hazard_context hc ON hc.id=h.id WHERE l.alert_id=$1 AND hc.filing_id=$2
		AND l.organization_name COLLATE "C"=$3 AND `+leadStillOpen+` FOR SHARE OF a,h`,
		lead, filing, owner).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return errScope
	}
	return err
}

// classifyCreateError 仅映射年度和线索的明确业务唯一约束。
// 参数 err 为插入监督任务时的数据库错误。
// 返回值 可向用户解释的重复错误，或原始数据库错误。
func classifyCreateError(err error) error {
	var dbError *pgconn.PgError
	if errors.As(err, &dbError) &&
		(dbError.ConstraintName == "supervision_annual_once" || dbError.ConstraintName == "supervision_lead_once") {
		return errDuplicate
	}
	return err
}
