package alerts

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"grade-business/internal/hazards"
	"grade-business/internal/reviewinbox"
)

type lockedAlertSource struct {
	HazardID, Status, Deadline, Today, Organization, Institution, FilingNumber, SystemName string
}

type pendingDisposition struct {
	ID, AlertID, Action, RequesterAudience, RequestedBy string
	RequestedDeadline                                   *string
}

const lockedAlertInstitution = `COALESCE(h.retest_institution,
	CASE WHEN anchor.kind='report' AND anchor.status='approved' THEN anchor.assessment_org END,'')`

// lockAlertSource 锁定实际来源和归属，避免行锁等待后使用旧的视图值判断权限。
// 参数 ctx 控制查询并携带办理范围，tx 为处置事务，id 为预警公开标识。
// 返回值 当前来源、归属与期限，或访问及数据库错误。
func lockAlertSource(ctx context.Context, tx pgx.Tx, id pgtype.UUID) (lockedAlertSource, error) {
	var result lockedAlertSource
	scope := currentAlertScope(ctx)
	err := tx.QueryRow(ctx, `SELECT h.id::text,h.status,to_char(h.deadline,'YYYY-MM-DD'),
	  to_char(`+alertBusinessDateSQL+`,'YYYY-MM-DD'),f.organization_name,`+lockedAlertInstitution+`,f.number,f.system_name
	  FROM local_alerts a`+alertSourceJoins+`
	  JOIN business_cases anchor ON anchor.id=COALESCE(hc.report_id,hc.filing_id)
	  WHERE a.id=$1 AND ($2::text='' OR ($2='operator' AND f.organization_name COLLATE "C"=$3)
	    OR ($2='institution' AND (`+lockedAlertInstitution+`) COLLATE "C"=$3))
	  FOR UPDATE OF h FOR SHARE OF anchor,f`, id, scope.audience, scope.recipient).
		Scan(&result.HazardID, &result.Status, &result.Deadline, &result.Today, &result.Organization,
			&result.Institution, &result.FilingNumber, &result.SystemName)
	return result, err
}

// lockAlert 在持有来源锁后锁定下一个需要变更的预警。
// 参数 ctx 控制查询，tx 为处置事务，id、hazard 为预警及其实际来源。
// 返回值 当前预警状态或数据库错误。
func lockAlert(ctx context.Context, tx pgx.Tx, id pgtype.UUID, hazard string) (actionResult, error) {
	var result actionResult
	err := tx.QueryRow(ctx, `SELECT id::text,status,version,closed_at FROM local_alerts
	  WHERE id=$1 AND hazard_id=$2 FOR UPDATE`, id, hazard).
		Scan(&result.ID, &result.Status, &result.Version, &result.ClosedAt)
	return result, err
}

// findPendingDisposition 查找同一隐患任何来源的唯一待审请求。
// 参数 ctx 控制读取，tx 持有来源锁，hazard 为实际来源标识。
// 返回值 当前待审请求（没有时为空）或数据库错误。
func findPendingDisposition(ctx context.Context, tx pgx.Tx, hazard string) (*pendingDisposition, error) {
	var result pendingDisposition
	err := tx.QueryRow(ctx, `SELECT id::text,alert_id::text,action,requester_audience,requested_by,
	    to_char(requested_deadline,'YYYY-MM-DD')
	  FROM local_alert_dispositions WHERE hazard_id=$1 AND status='pending'`, hazard).
		Scan(&result.ID, &result.AlertID, &result.Action, &result.RequesterAudience,
			&result.RequestedBy, &result.RequestedDeadline)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &result, err
}

// createDisposition 保存一次实际转办或一个等待审批的申请，并链接已存在隐患。
// 参数 ctx 控制写入，tx 为处置事务，source、item 为来源与预警，scope、input 为办理方归属及请求。
// 返回值 实际预警状态与处置记录，或校验及写入错误。
func createDisposition(ctx context.Context, tx pgx.Tx, source lockedAlertSource, item actionResult,
	scope alertScope, input actionInput) (actionResult, error) {
	if source.Status == statusClosed && (input.Action == actionConvert || input.Action == actionExtension) {
		return actionResult{}, errConflict
	}
	if input.Action == actionExtension && (input.RequestedDeadline <= source.Deadline || input.RequestedDeadline <= source.Today) {
		return actionResult{}, errDeadline
	}
	target, state := item.Status, statusPending
	if input.Action == actionConvert {
		if err := ensureNotConverted(ctx, tx, source.HazardID); err != nil {
			return actionResult{}, err
		}
		target, state = statusProcessing, "completed"
	}
	actor := scope.recipient
	if scope.audience == audienceInstitution {
		actor = "本地机构：" + actor
	}
	result, err := saveAlertDecision(ctx, tx, item, target, actor, input)
	if err != nil {
		return actionResult{}, err
	}
	result.HazardID = source.HazardID
	err = tx.QueryRow(ctx, `WITH saved AS (
	  INSERT INTO local_alert_dispositions
	    (alert_id,hazard_id,action,status,comment,requested_deadline,requested_by,requested_version,requester_audience)
	  VALUES ($1,$2,$3,$4,$5,NULLIF($6,'')::date,$7,$8,$9) RETURNING *
	) SELECT `+dispositionJSON+` FROM saved d`, item.ID, source.HazardID, input.Action, state,
		input.Comment, input.RequestedDeadline, scope.recipient, result.Version, scope.audience).
		Scan(&result.Disposition)
	return result, err
}

// ensureNotConverted 拒绝对任何已完成转办的同一隐患重复登记。
// 参数 ctx 控制读取，tx 持有来源锁，hazard 为真实隐患标识。
// 返回值 已登记转办或数据库查询错误。
func ensureNotConverted(ctx context.Context, tx pgx.Tx, hazard string) error {
	var converted bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM local_alert_dispositions
	  WHERE hazard_id=$1 AND action='convert')`, hazard).Scan(&converted); err != nil {
		return err
	}
	if converted {
		return errConflict
	}
	return nil
}

// reviewDisposition 办理预警申请复核并在同一事务保存决定和收件通知。
// 参数 ctx 控制写入，tx 为处置事务，source、item、pending 为已锁定业务记录，operator 与 input 为审批数据。
// 返回值 当前业务结果或审批及写入错误。
func reviewDisposition(ctx context.Context, tx pgx.Tx, source lockedAlertSource, item actionResult,
	pending pendingDisposition, operator string, input actionInput) (actionResult, error) {
	target, outcome, err := dispositionOutcome(ctx, tx, source, item, pending, operator, input)
	if err != nil {
		return actionResult{}, err
	}
	result, err := saveAlertDecision(ctx, tx, item, target, operator, input)
	if err != nil {
		return actionResult{}, err
	}
	result.HazardID = source.HazardID
	err = tx.QueryRow(ctx, `WITH saved AS (
	  UPDATE local_alert_dispositions SET status=$2,decision_comment=$3,decided_by=$4,
	    decided_at=clock_timestamp(),decision_version=$5 WHERE id=$1 AND status='pending' RETURNING *
	) SELECT `+dispositionJSON+` FROM saved d`, pending.ID, outcome, input.Comment, operator, result.Version).
		Scan(&result.Disposition)
	if err != nil {
		return actionResult{}, err
	}
	return result, deliverDispositionOutcome(ctx, tx, source, pending, result, input.Comment)
}

// deliverDispositionOutcome 向当前运营者及仍属该隐患的反馈提交机构送达审批结果。
// 参数 ctx、tx 控制事务，source、pending 指明锁定后的来源和申请，result、comment 为审批结果和说明。
// 返回值 两端通知均已保存或没有合格收件人时为 nil，否则返回写入错误。
func deliverDispositionOutcome(ctx context.Context, tx pgx.Tx, source lockedAlertSource,
	pending pendingDisposition, result actionResult, comment string) error {
	notice := reviewinbox.Notice{
		Resource: "alerts", ResourceID: result.ID, State: result.Disposition.Status, Version: result.Version,
		Reference: source.FilingNumber, Subject: source.SystemName, Comment: comment,
	}
	if source.Organization != "" {
		notice.Audience, notice.Recipient = audienceOperator, source.Organization
		if err := reviewinbox.Deliver(ctx, tx, notice); err != nil {
			return err
		}
	}
	if pending.RequesterAudience == audienceInstitution && source.Institution != "" &&
		source.Institution == pending.RequestedBy {
		notice.Audience, notice.Recipient = audienceInstitution, source.Institution
		return reviewinbox.Deliver(ctx, tx, notice)
	}
	return nil
}

// dispositionOutcome 根据审批结论推进预警或调用隐患模块延期。
// 参数 ctx 控制写入，tx 为审批事务，source、item、pending 为实际来源，operator 与 input 为审批数据。
// 返回值 预警目标状态、申请结果和领域或数据库错误。
func dispositionOutcome(ctx context.Context, tx pgx.Tx, source lockedAlertSource, item actionResult,
	pending pendingDisposition, operator string, input actionInput) (string, string, error) {
	if input.Action == actionReject {
		return item.Status, "rejected", nil
	}
	if pending.Action == actionFalseAlarm || pending.Action == actionFeedback {
		return statusClosed, "approved", nil
	}
	if pending.Action != actionExtension {
		return "", "", errConflict
	}
	if reason := extensionApprovalBlockedReason(source.Status, source.Deadline, source.Today, pending.RequestedDeadline); reason != "" {
		return "", "", approvalBlockedError(reason)
	}
	err := hazards.ExtendDeadline(ctx, tx, source.HazardID, *pending.RequestedDeadline, operator, input.Comment)
	if errors.Is(err, hazards.ErrDeadlineConflict) {
		return "", "", errConflict
	}
	return item.Status, "approved", err
}

// extensionApprovalBlockedReason 按隐患延期使用的状态与日期边界核对待审申请。
// 参数 status 为隐患状态，current、today 为真实期限和上海业务日，requested 为拟延期日期。
// 返回值 具体不可批准原因，符合延期边界时为空。
func extensionApprovalBlockedReason(status, current, today string, requested *string) string {
	switch {
	case status == statusClosed:
		return "关联隐患已销项，延期申请只能驳回。"
	case requested == nil:
		return "延期申请缺少拟延期日期，只能驳回。"
	case *requested <= today:
		return "申请延期日期已到或已过，延期申请只能驳回。"
	case *requested <= current:
		return "申请延期日期不晚于当前整改期限，延期申请只能驳回。"
	default:
		return ""
	}
}
