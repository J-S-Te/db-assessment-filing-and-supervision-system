package alerts

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type syncResult struct {
	Created int64 `json:"created"`
}

const syncLocalAlerts = `
WITH business_day AS (SELECT ` + alertBusinessDateSQL + ` AS today),
candidates AS (
	SELECT h.id AS hazard_id,rule.kind,h.severity,f.system_name,f.organization_name,h.deadline,
	       f.number AS filing_number,` + lockedAlertInstitution + ` AS institution
	FROM hazard_items h
	JOIN hazard_context hc ON hc.id=h.id
	JOIN business_cases f ON f.id=hc.filing_id
	JOIN business_cases anchor ON anchor.id=COALESCE(hc.report_id,hc.filing_id)
	LEFT JOIN remediation_plans p ON p.id=h.plan_id
	CROSS JOIN business_day d
	CROSS JOIN (VALUES ('high_risk'),('overdue')) AS rule(kind)
	WHERE h.status<>'pending_dispatch' AND (h.plan_id IS NULL OR p.status='approved')
	AND ((rule.kind='high_risk' AND h.severity='high' AND h.status IN ('open','returned'))
	   OR (rule.kind='overdue' AND h.status<>'closed' AND h.deadline<d.today))
	AND NOT EXISTS (SELECT 1 FROM local_alerts a WHERE a.hazard_id=h.id AND a.kind=rule.kind)
	FOR SHARE OF h,anchor,f SKIP LOCKED
),
inserted AS (
	INSERT INTO local_alerts(hazard_id,kind,severity,system_name,organization_name,deadline)
	SELECT hazard_id,kind,severity,system_name,organization_name,deadline FROM candidates
	ORDER BY hazard_id,kind
	ON CONFLICT (hazard_id,kind) DO NOTHING
	RETURNING id,hazard_id,kind,version,triggered_at
),
recorded AS (
	INSERT INTO local_alert_events(alert_id,version,action,to_status,operator,occurred_at)
	SELECT id,version,'create','pending',$1,triggered_at FROM inserted
), notified AS (
	INSERT INTO review_notices(audience,resource,resource_id,event_version,state,recipient,reference,subject,comment)
	SELECT recipient.audience,'alerts',i.id,i.version,'pending',recipient.name,c.filing_number,c.system_name,''
	FROM inserted i JOIN candidates c USING (hazard_id,kind)
	CROSS JOIN LATERAL (VALUES ('operator',c.organization_name),('institution',c.institution)) recipient(audience,name)
	WHERE recipient.name<>'' AND (recipient.audience='operator' OR length(recipient.name)<=200)
	ON CONFLICT DO NOTHING
)
SELECT count(*) FROM inserted
`

// sync 略过正在更新的来源，同事务生成预警、收件通知及待跟踪的超期线索。
// 参数 ctx 控制同步事务。
// 返回值 实际新增条数及读取或写入错误。
func (s *Service) sync(ctx context.Context) (syncResult, error) {
	var result syncResult
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, syncLocalAlerts, s.operator).Scan(&result.Created); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, enqueueSupervisionLeads)
		return err
	})
	return result, err
}

// execute 先锁来源隐患再锁预警，在同一事务保存实际处置、留痕与需要送达的通知。
// 参数 ctx 控制写入，id 为预警标识，input 为已检查的动作、预期版本及备注。
// 返回值 更新后的预警摘要，或不存在、冲突及数据库错误。
func (s *Service) execute(ctx context.Context, id pgtype.UUID, input actionInput) (actionResult, error) {
	var outcome actionResult
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		source, err := lockAlertSource(ctx, tx, id)
		if err != nil {
			return err
		}
		item, err := lockAlert(ctx, tx, id, source.HazardID)
		if err != nil {
			return err
		}
		if item.Version != input.ExpectedVersion || item.Status == statusClosed {
			return errConflict
		}
		pending, err := findPendingDisposition(ctx, tx, source.HazardID)
		if err != nil {
			return err
		}
		outcome, err = s.executeLocked(ctx, tx, source, item, pending, input)
		return err
	})
	return outcome, err
}

// executeLocked 区分机构反馈、运营者处理和监管决定，并在待审时拒绝绕过。
// 参数 ctx 控制写入，tx 持有业务锁，source、item、pending 为当前来源和申请，input 为新动作。
// 返回值 更新后的业务对象或领域错误。
func (s *Service) executeLocked(ctx context.Context, tx pgx.Tx, source lockedAlertSource,
	item actionResult, pending *pendingDisposition, input actionInput) (actionResult, error) {
	if input.Action == actionApprove || input.Action == actionReject {
		if pending == nil || pending.AlertID != item.ID {
			return actionResult{}, errConflict
		}
		return reviewDisposition(ctx, tx, source, item, *pending, s.operator, input)
	}
	if pending != nil {
		return actionResult{}, errConflict
	}
	scope := currentAlertScope(ctx)
	switch scope.audience {
	case audienceOperator:
		return createDisposition(ctx, tx, source, item, scope, input)
	case audienceInstitution:
		return executeInstitutionAlert(ctx, tx, source, item, scope, input)
	}
	target := nextStatus(item.Status, input.Action)
	if target == "" {
		return actionResult{}, errConflict
	}
	return saveAlertDecision(ctx, tx, item, target, s.operator, input)
}

// executeInstitutionAlert 推进机构领取及提交反馈，保持原隐患记录。
// 参数 ctx、tx 为持锁操作，source、item 为当前来源和预警，scope、input 为办理方与反馈动作。
// 返回值 预警状态、申请或版本及数据库错误。
func executeInstitutionAlert(ctx context.Context, tx pgx.Tx, source lockedAlertSource,
	item actionResult, scope alertScope, input actionInput) (actionResult, error) {
	switch {
	case input.Action == actionProcess && item.Status == statusPending:
		return saveAlertDecision(ctx, tx, item, statusProcessing, "本地机构："+scope.recipient, input)
	case input.Action == actionFeedback && item.Status == statusProcessing:
		return createDisposition(ctx, tx, source, item, scope, input)
	default:
		return actionResult{}, errConflict
	}
}

// nextStatus 确定当前预警可执行动作的实际目标状态。
// 参数 status 为当前处置状态，action 为请求动作。
// 返回值 允许时返回目标状态，禁止时返回空字符串。
func nextStatus(status, action string) string {
	switch {
	case status == statusPending && action == actionProcess:
		return statusProcessing
	case (status == statusPending || status == statusProcessing) && action == actionClose:
		return statusClosed
	default:
		return ""
	}
}

// saveAlertDecision 保存处置结果并添加不可覆盖的同版本事件。
// 参数 ctx、tx 控制锁定写入，item、target 标识原预警和目标状态，operator、input 为经办人与备注。
// 返回值 由数据库确认的结果及写入错误。
func saveAlertDecision(ctx context.Context, tx pgx.Tx, item actionResult,
	target, operator string, input actionInput,
) (actionResult, error) {
	result := actionResult{ID: item.ID}
	err := tx.QueryRow(ctx, `UPDATE local_alerts SET status=$2,version=version+1,
		closed_at=CASE WHEN $2='closed' THEN clock_timestamp() END WHERE id=$1
		RETURNING status,version,closed_at`, item.ID, target).Scan(&result.Status, &result.Version, &result.ClosedAt)
	if err != nil {
		return actionResult{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO local_alert_events
		(alert_id,version,action,from_status,to_status,operator,comment) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		item.ID, result.Version, input.Action, item.Status, result.Status, operator, input.Comment)
	return result, err
}
