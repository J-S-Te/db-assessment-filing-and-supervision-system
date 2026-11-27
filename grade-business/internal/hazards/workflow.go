package hazards

import (
	"context"
	"errors"
	"strings"

	"grade-business/internal/reviewinbox"

	"github.com/jackc/pgx/v5"
)

type lockedHazard struct {
	Status               string
	Version              int64
	Ledger               string
	RegistrationFilingID string
}

// editable 判断当前台账是否可登记整改证据并提交。
// 参数 status 为隐患的当前状态，使用持锁时读取的状态进行写入校验。
// 返回值 可以继续整改时为真。
func editable(status string) bool {
	return status == statusOpen || status == statusReturned
}

// allowedActions 返回指定状态可发起的台账办理操作。
// 参数 status 为隐患的当前状态。
// 返回值 当前允许的处理操作，已销号时为空列表。
func allowedActions(status string) []string {
	switch status {
	case statusDispatch:
		return []string{actionDispatch}
	case statusOpen, statusReturned:
		return []string{actionSubmit}
	case statusPending:
		return []string{actionReturn, actionClose}
	default:
		return []string{}
	}
}

// nextStatus 检查操作所处业务阶段并确定目标状态。
// 参数 status 为当前隐患状态，action 为已验证的办理动作。
// 返回值 可达状态，或阶段不符错误。
func nextStatus(status, action string) (string, error) {
	if status == statusDispatch && action == actionDispatch {
		return statusOpen, nil
	}
	if action == actionSupervise && canSupervise(status) {
		return status, nil
	}
	if editable(status) && action == actionSubmit {
		return statusRetest, nil
	}
	if status == statusRetest {
		switch action {
		case actionRetestPass:
			return statusPending, nil
		case actionRetestReturn:
			return statusReturned, nil
		}
	}
	if status == statusPending {
		switch action {
		case actionReturn:
			return statusReturned, nil
		case actionClose:
			return statusClosed, nil
		}
	}
	return "", errConflict
}

// lockHazard 锁定行以序列化证据上传和台账操作。
// 参数 ctx 含可选运营单位或测评机构，tx 为持久化事务，id 为隐患标识。
// 返回值 当前状态和版本，或记录不存在及数据库错误。
func lockHazard(ctx context.Context, tx pgx.Tx, id string) (lockedHazard, error) {
	if !validID(id) {
		return lockedHazard{}, errNotFound
	}
	var item lockedHazard
	err := tx.QueryRow(ctx, `SELECT status,version,ledger,
		COALESCE(registration_filing_id::text,'') FROM hazard_items WHERE id=$1 FOR UPDATE`, id).
		Scan(&item.Status, &item.Version, &item.Ledger, &item.RegistrationFilingID)
	if err == nil && (operatorOrganization(ctx) != "" || hazardInstitution(ctx) != "") {
		err = lockHazardScope(ctx, tx, id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return lockedHazard{}, errNotFound
	}
	return item, err
}

// lockHazardScope 在持锁办理期间校验隐患当前备案单位与关联报告机构。
// 参数 ctx 含已核验端的范围，tx 为已锁隐患的事务，id 为隐患标识。
// 返回值 匹配时为 nil，未找到记录或数据库失败时返回错误。
func lockHazardScope(ctx context.Context, tx pgx.Tx, id string) error {
	var matched string
	return tx.QueryRow(ctx, `SELECT h.id::text`+hazardLockedJoin+`
		WHERE h.id=$1 AND `+lockedHazardScope+` FOR SHARE OF anchor,filing`,
		id, operatorOrganization(ctx), hazardInstitution(ctx)).Scan(&matched)
}

// requireEvidence 核验整改人已提交本轮材料，退回或交接后必须登记新的证据。
// 参数 ctx 为请求上下文，tx 为已锁目标行的事务，id 为隐患标识。
// 返回值 可提交时为 nil，缺失本轮材料或查询失败时返回错误。
func requireEvidence(ctx context.Context, tx pgx.Tx, id string) error {
	var ready bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM hazard_attachments a JOIN hazard_items h ON h.id=a.hazard_id
		WHERE a.hazard_id=$1 AND a.kind='evidence'
		AND a.item_version>GREATEST(h.handoff_version,COALESCE((SELECT max(version) FROM hazard_events
		    WHERE hazard_id=$1 AND action IN ('return_for_correction','retest_return')),0))
	)`, id).Scan(&ready)
	if err == nil && !ready {
		return errEvidence
	}
	return err
}

const retestReportQuery = `EXISTS (SELECT 1 FROM hazard_attachments a
	WHERE a.hazard_id=h.id AND a.kind='retest_report' AND a.item_version>(
		SELECT max(e.version) FROM hazard_events e WHERE e.hazard_id=h.id AND e.action='submit'))`

// requireRetestReport 核对机构已上传本轮最新提交之后的复测报告。
// 参数 ctx 为请求上下文，tx 已锁定隐患，id 为记录标识。
// 返回值 报告已齐备时为 nil，否则为材料缺失或查询错误。
func requireRetestReport(ctx context.Context, tx pgx.Tx, id string) error {
	var ready bool
	err := tx.QueryRow(ctx, "SELECT "+retestReportQuery+" FROM hazard_items h WHERE h.id=$1", id).Scan(&ready)
	if err == nil && !ready {
		return errRetestReport
	}
	return err
}

// actionWithinScope 限定实际办理端能够执行的工作流动作。
// 参数 ctx 含当前已验证端，action 为输入的动作名称。
// 返回值 此端有权执行该动作时为真。
func actionWithinScope(ctx context.Context, action string) bool {
	if hazardInstitution(ctx) != "" {
		return action == actionRetestPass || action == actionRetestReturn
	}
	if operatorOrganization(ctx) != "" {
		return action == actionSubmit
	}
	return action == actionSubmit || action == actionReturn || action == actionClose ||
		action == actionDispatch || action == actionSupervise
}

// act 校验最新台账版本并按当前状态完成整改办理。
// 参数 ctx 含企业端可选范围，id 为隐患标识，input 为操作、版本和说明。
// 返回值 新状态和版本，或状态、材料及持久化错误。
func (s *Service) act(ctx context.Context, id string, input actionInput) (hazardMutation, error) {
	var result hazardMutation
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		current, err := lockHazard(ctx, tx, id)
		if err != nil {
			return err
		}
		if current.Version != input.ExpectedVersion || !actionWithinScope(ctx, input.Action) {
			return errConflict
		}
		next, err := nextStatus(current.Status, input.Action)
		if err != nil {
			return err
		}
		institution, err := prepareHazardAction(ctx, tx, id, current, input.Action)
		if err != nil {
			return err
		}
		result, err = s.persistAction(ctx, tx, id, current.Status, next, input, institution)
		return err
	})
	return result, err
}

// prepareHazardAction 检查当前办理动作的材料和备案条件。
// 参数 ctx 控制查询，tx 持有条目锁，id、current 和 action 为目标及操作。
// 返回值 派单时取得的复测机构，或业务及查询错误。
func prepareHazardAction(ctx context.Context, tx pgx.Tx, id string, current lockedHazard, action string) (string, error) {
	switch action {
	case actionSubmit:
		return "", requireEvidence(ctx, tx, id)
	case actionRetestPass:
		return "", requireRetestReport(ctx, tx, id)
	case actionDispatch:
		institution, _, err := lockRegistrationFiling(ctx, tx, current.RegistrationFilingID)
		return institution, err
	case actionSupervise:
		if current.Ledger != ledgerMajorRisk {
			return "", errConflict
		}
	}
	return "", nil
}

// persistAction 在同一事务保存台账状态、销号时间及工作人员的历史说明。
// 参数 ctx 含企业端可选范围，tx 为持锁事务，id 为隐患，from 和 next 为前后状态，input 为操作内容，institution 为派单时已锁定的登记机构。
// 返回值 写入后的标识、状态和版本，或持久化错误。
func (s *Service) persistAction(ctx context.Context, tx pgx.Tx, id, from, next string,
	input actionInput, institution string) (hazardMutation, error) {
	var result hazardMutation
	err := tx.QueryRow(ctx, `UPDATE hazard_items SET status=$2,version=version+1,
		closed_at=CASE WHEN $2='closed' THEN clock_timestamp() ELSE NULL END,
		dispatched_at=CASE WHEN $3='dispatch' THEN clock_timestamp() ELSE dispatched_at END,
		retest_institution=CASE WHEN $3='dispatch' THEN $4 ELSE retest_institution END
		WHERE id=$1 RETURNING id::text,status,version`, id, next, input.Action, institution).
		Scan(&result.ID, &result.Status, &result.Version)
	if err != nil {
		return hazardMutation{}, err
	}
	operator := s.operator
	if organization := operatorOrganization(ctx); organization != "" {
		operator = organization
	}
	if scope := hazardInstitution(ctx); scope != "" {
		operator = scope
	}
	_, err = tx.Exec(ctx, `INSERT INTO hazard_events
		(hazard_id,action,from_status,to_status,operator,comment,version)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, id, input.Action, from, next, operator, input.Comment, result.Version)
	if err != nil {
		return result, err
	}
	return result, deliverAction(ctx, tx, result, input)
}

// deliverAction 将复测待办、复测结论及监管决定送达对应的当前业务办理方。
// 参数 ctx 为请求上下文，tx 为办理事务，result 为新状态及版本，input 为动作和意见。
// 返回值 查询或登记错误，意见已登记时返回 nil。
func deliverAction(ctx context.Context, tx pgx.Tx, result hazardMutation, input actionInput) error {
	notice := reviewinbox.Notice{Audience: "operator", Resource: "hazards", ResourceID: result.ID,
		State: result.Status, Version: result.Version, Comment: input.Comment}
	var institution string
	var canRetest bool
	err := tx.QueryRow(ctx, `SELECT filing.organization_name,`+lockedRetestInstitution+`,`+lockedRetestAllowed+`,
		anchor.number,concat_ws(' · ',filing.system_name,h.title)`+hazardLockedJoin+`
		WHERE h.id=$1 FOR SHARE OF anchor,filing`, result.ID).
		Scan(&notice.Recipient, &institution, &canRetest, &notice.Reference, &notice.Subject)
	if err != nil {
		return err
	}
	if input.Action == actionSubmit {
		if !canRetest || strings.TrimSpace(institution) == "" {
			return errConflict
		}
		notice.Audience, notice.Recipient = "institution", institution
		return reviewinbox.Deliver(ctx, tx, notice)
	}
	if input.Action == actionSupervise {
		if result.Status == statusRetest {
			notice.Audience, notice.Recipient = "institution", institution
		}
		return deliverSupervision(ctx, tx, notice)
	}
	if input.Action == actionDispatch && notice.Comment == "" {
		notice.Comment = "监管已派发整改，请按期提交本轮证据。"
	}
	if err := reviewinbox.Deliver(ctx, tx, notice); err != nil {
		return err
	}
	if (input.Action == actionReturn || input.Action == actionClose) && strings.TrimSpace(institution) != "" {
		notice.Audience, notice.Recipient = "institution", institution
		return reviewinbox.Deliver(ctx, tx, notice)
	}
	return nil
}
