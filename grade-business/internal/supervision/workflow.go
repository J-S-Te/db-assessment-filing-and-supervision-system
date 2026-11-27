package supervision

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/reviewinbox"
)

type actionResult struct {
	mutation
	inspectionID string
	summaryID    string
	counts       *rectificationCounts
}

// editableResult 表明运营者是否仍可办理当轮自查材料。
// 参数 state 为任务当前状态。
// 返回值 下发或退回补正时为真。
func editableResult(state string) bool {
	return state == "open" || state == "returned"
}

// execute 在同一事务中核对收件范围、锁定阶段并保存状态、留痕和通知。
// 参数 ctx 为业务请求，id、owner 标记任务和席位，input 为受控审核动作。
// 返回值 已提交状态、已生效的重复请求结果或业务冲突。
func (s *Service) execute(ctx context.Context, id, owner string, input actionInput) (mutation, error) {
	var result mutation
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		record, err := loadTask(ctx, tx, id, owner, true)
		if err != nil {
			return err
		}
		if record.Version != input.ExpectedVersion {
			result, err = replayAction(ctx, tx, record, input)
			return err
		}
		next, err := checkAction(ctx, tx, record, owner, input)
		if err != nil {
			return err
		}
		result = next.mutation
		return s.saveAction(ctx, tx, record, owner, input, next)
	})
	return result, err
}

// replayAction 只重放同一刚提交动作，拒绝会改写历史或阶段已变化的请求。
// 参数 ctx、db 为查询环境，record 是锁定任务，input 是客户端原始版本及动作。
// 返回值 相同业务结果或状态冲突。
func replayAction(ctx context.Context, db reader, record taskRecord, input actionInput) (mutation, error) {
	if record.Version != input.ExpectedVersion+1 {
		return mutation{}, errState
	}
	var match bool
	err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM supervision_events
		WHERE task_id=$1 AND version=$2 AND action=$3 AND comment=$4
		  AND COALESCE(correction_kind,'')=$5)`, record.ID, record.Version,
		input.Action, input.Comment, input.CorrectionKind).Scan(&match)
	if err != nil {
		return mutation{}, err
	}
	if !match {
		return mutation{}, errState
	}
	return mutation{record.ID, record.Status, record.Version}, nil
}

// checkAction 按实际状态验证目标阶段及相应材料，准备落库结果。
// 参数 ctx、tx 为事务，record 为当前任务，owner、input 描述办理方和动作。
// 返回值 动作计划、缺材料或业务冲突。
func checkAction(ctx context.Context, tx pgx.Tx, record taskRecord,
	owner string, input actionInput,
) (actionResult, error) {
	status := actionStatus(record.Status, input.Action)
	if status == "" {
		return actionResult{}, errState
	}
	next := actionResult{mutation: mutation{record.ID, status, record.Version + 1}}
	if input.Action == "publish" || input.Action == "submit" {
		if err := lockWritableFiling(ctx, tx, record, input.Action == "publish"); err != nil {
			return actionResult{}, err
		}
	}
	switch input.Action {
	case "publish":
		if record.LeadAlertID != nil {
			if err := lockLeadSource(ctx, tx, *record.LeadAlertID, record.FilingID, record.OrganizationName); err != nil {
				return actionResult{}, err
			}
		}
		_, _, err := requiredMaterials(ctx, tx, record, inspectionTemplate, summaryTemplate)
		return next, err
	case "submit":
		return prepareSubmission(ctx, tx, record, next)
	case "cancel":
		var pending bool
		err := tx.QueryRow(ctx, `SELECT `+currentSitePending+`
			FROM supervision_tasks t WHERE t.id=$1`, record.ID).Scan(&pending)
		if err != nil {
			return actionResult{}, err
		}
		if pending {
			return actionResult{}, errSitePending
		}
		return next, nil
	default:
		return next, nil
	}
}

// actionStatus 定义自查任务的合法阶段流转。
// 参数 from 和 action 为锁定的当前状态及业务动作。
// 返回值 新阶段，动作不适用时为空。
func actionStatus(from, action string) string {
	switch {
	case from == "draft" && action == "publish":
		return "open"
	case editableResult(from) && action == "submit":
		return "submitted"
	case from == "submitted" && action == "return":
		return "returned"
	case from == "submitted" && action == "approve":
		return "approved"
	case (from == "draft" || editableResult(from)) && action == "cancel":
		return "cancelled"
	default:
		return ""
	}
}

// lockWritableFiling 在读锁下核对任务绑定的备案仍处于同一有效归属。
// 参数 ctx、tx 为业务事务，record 为任务原始归属，checkDue 在下发时检查截止日期。
// 返回值 当前归属和日期合格时为 nil，否则为业务冲突或数据库错误。
func lockWritableFiling(ctx context.Context, tx pgx.Tx, record taskRecord, checkDue bool) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT true FROM business_cases f WHERE f.id=$1 AND `+eligibleFiling+`
		AND f.organization_name COLLATE "C"=$2
		AND (NOT $3 OR $4::text::date>(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date)
		FOR SHARE OF f`, record.FilingID, record.OrganizationName, checkDue, record.DueOn).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return errScope
	}
	return err
}

// prepareSubmission 校验要求补正的类别具备新文件，锁定当轮材料与整改汇总。
// 参数 ctx、tx 为业务事务，record 和 next 为任务现状及待提交动作。
// 返回值 附带当次快照的结果或缺材料、数据库错误。
func prepareSubmission(ctx context.Context, tx pgx.Tx, record taskRecord, next actionResult) (actionResult, error) {
	inspection, summary, err := requiredMaterials(ctx, tx, record, inspectionForm, rectificationForm)
	if err != nil {
		return actionResult{}, err
	}
	if (record.CorrectionKind == "both" || record.CorrectionKind == inspectionForm) &&
		inspection.Version <= record.ReturnedVer {
		return actionResult{}, errMaterials
	}
	if (record.CorrectionKind == "both" || record.CorrectionKind == rectificationForm) &&
		summary.Version <= record.ReturnedVer {
		return actionResult{}, errMaterials
	}
	counts, err := countRectification(ctx, tx, record.FilingID)
	next.inspectionID, next.summaryID, next.counts = inspection.ID, summary.ID, &counts
	return next, err
}

// saveAction 落库业务阶段和本轮材料，并通知固定收件单位。
// 参数 ctx、tx 为提交事务，record、owner、input、next 提供原始归属、动作与结果。
// 返回值 任一状态、证据或通知写入错误。
func (s *Service) saveAction(ctx context.Context, tx pgx.Tx, record taskRecord,
	owner string, input actionInput, next actionResult,
) error {
	if err := storeActionState(ctx, tx, next, input); err != nil {
		return err
	}
	actor := s.actor
	if owner != "" {
		actor = owner
	}
	_, err := tx.Exec(ctx, `INSERT INTO supervision_events(task_id,version,action,from_status,to_status,
		actor,comment,correction_kind,inspection_file_id,summary_file_id,rectification)
		VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,'')::uuid,NULLIF($10,'')::uuid,$11)`,
		next.ID, next.Version, input.Action, record.Status, next.Status, actor, input.Comment,
		input.CorrectionKind, next.inspectionID, next.summaryID, next.counts)
	if err != nil {
		return err
	}
	return sendActionNotice(ctx, tx, record, input, next.mutation)
}

// storeActionState 维护下发时间、提交版本及当轮补正规则。
// 参数 ctx、tx 为更新事务，next 和 input 为已经检查的目标状态及动作。
// 返回值 状态修改错误。
func storeActionState(ctx context.Context, tx pgx.Tx, next actionResult, input actionInput) error {
	_, err := tx.Exec(ctx, `UPDATE supervision_tasks SET status=$2,version=$3,updated_at=clock_timestamp(),
		published_at=CASE WHEN $4='publish' THEN clock_timestamp() ELSE published_at END,
		submitted_at=CASE WHEN $4='submit' THEN clock_timestamp() ELSE submitted_at END,
		submitted_version=CASE WHEN $4='submit' THEN $3 ELSE submitted_version END,
		return_version=CASE WHEN $4='return' THEN $3 WHEN $4='submit' THEN NULL ELSE return_version END,
		correction_kind=CASE WHEN $4='return' THEN $5 WHEN $4='submit' THEN NULL ELSE correction_kind END
		WHERE id=$1`, next.ID, next.Status, next.Version, input.Action, input.CorrectionKind)
	return err
}

// sendActionNotice 只向任务正式下发时固定的运营单位投递站内处理结果。
// 参数 ctx、tx 为提交事务，record 为收件快照，input、next 为动作及新状态。
// 返回值 免通知时为空，否则为消息写入结果。
func sendActionNotice(ctx context.Context, tx pgx.Tx, record taskRecord, input actionInput, next mutation) error {
	if input.Action == "submit" || (input.Action == "cancel" && record.PublishedAt == nil) {
		return nil
	}
	comment := input.Comment
	if input.Action == "publish" {
		comment = "请于 " + record.DueOn + " 前提交自查表与整改情况汇总表。"
	}
	return reviewinbox.Deliver(ctx, tx, reviewinbox.Notice{
		Audience: "operator", Resource: "supervisions", ResourceID: next.ID,
		Version: next.Version, State: next.Status, Recipient: record.OrganizationName,
		Reference: record.FilingNumber, Subject: record.Title, Comment: comment,
	})
}
