package implementations

import (
	"context"
	"slices"
	"time"

	"grade-business/internal/access"
)

// implementationReviewerSQL 查找当前审批轮次内一个节点的最近一次实名通过事件。
// 参数 state 是允许查询的固定报备状态条件，from、to 是固定审批节点。
// 返回值 获取审核账号标识的 SQL 表达式，未留有效身份时结果为空值。
func implementationReviewerSQL(state, from, to string) string {
	return `CASE WHEN ` + state + ` THEN (
    SELECT CASE WHEN e.operator_role IN ('ROLE_SYS_ADMIN','ROLE_REGULATOR') THEN e.operator_id::text END
    FROM implementation_events e
    WHERE e.report_id=i.id AND e.action='approve' AND e.version <= i.version
      AND e.from_status='` + from + `' AND e.to_status='` + to + `'
      AND e.version > COALESCE((SELECT max(boundary.version) FROM implementation_events boundary
          WHERE boundary.report_id=i.id AND boundary.version <= i.version
            AND boundary.action IN ('submit','restart_initial','return_for_correction')),0)
    ORDER BY e.version DESC LIMIT 1
) END`
}

// regulatorImplementationAction 判断一个命令是否属于辖区报备审核操作。
// 参数 action 为实施报备命令名。
// 返回值 监管员具有该命令的角色权限时为真。
func regulatorImplementationAction(action string) bool {
	switch action {
	case "accept", "approve", "return_for_correction", "restart_initial":
		return true
	}
	return false
}

// approvalReviewBlock 终审校验异人要求，复核确认该轮初终审的实名审批凭据。
// 参数 actor 为真实会话身份，record 包含已读取的本轮审核信息。
// 返回值 审核资格、身份不可核实或审核人重复错误，通过时为空。
func approvalReviewBlock(actor access.Principal, record storedRecord) error {
	if actor.ID == "" || (actor.Role != access.SystemAdmin && actor.Role != access.Regulator) {
		return errPermission
	}
	if err := reviewEvidenceBlock(record); err != nil {
		return err
	}
	if record.Status == final && actor.ID == *record.FirstReviewerID {
		return errSameReviewer
	}
	return nil
}

// reviewEvidenceBlock 检查有待终审或复核的历史阶段是否留下可核实的审批账号。
// 参数 record 包含当前状态及同一轮初审、终审的实名证据。
// 返回值 当前历史节点需从初审重新办理时返回业务错误。
func reviewEvidenceBlock(record storedRecord) error {
	if (record.Status == final || record.Status == supervision) && record.FirstReviewerID == nil {
		return errUnknownReviewer
	}
	if record.Status == supervision && (record.FinalReviewerID == nil ||
		*record.FirstReviewerID == *record.FinalReviewerID) {
		return errUnverifiedReviewers
	}
	return nil
}

// checkImplementationReview 对申请和审核共用的命令入口独立执行角色及审级约束。
// 参数 ctx 携带真实会话，record 为持锁读取的报备，action 为目标命令。
// 返回值 角色、身份或审批轮次拒绝错误，允许继续时为空。
func checkImplementationReview(ctx context.Context, record storedRecord, action string) error {
	actor, _ := access.Current(ctx)
	if actor.Role == access.Regulator && !regulatorImplementationAction(action) {
		return errPermission
	}
	switch action {
	case "approve":
		return approvalReviewBlock(actor, record)
	case "restart_initial":
		if actor.ID == "" || (actor.Role != access.SystemAdmin && actor.Role != access.Regulator) {
			return errPermission
		}
		if reviewEvidenceBlock(record) == nil {
			return errRestartUnavailable
		}
	}
	return nil
}

// setReviewActions 生成管理端详情的角色可操作项及终审受限原因。
// 参数 ctx 携带当前账号，record 为当前报备，operator 为历史发起人标记，now 为业务时钟。
// 返回值 无。
func setReviewActions(ctx context.Context, record *storedRecord, operator string, now time.Time) {
	actor, _ := access.Current(ctx)
	record.AllowedActions = allowedActions(*record, operator, now)
	if actor.Role == access.Regulator {
		record.AllowedActions = slices.DeleteFunc(record.AllowedActions, func(action string) bool {
			return !regulatorImplementationAction(action)
		})
	}
	if record.Status != final && record.Status != supervision {
		return
	}
	if err := approvalReviewBlock(actor, *record); err != nil {
		record.ReviewBlockReason = err.Error()
		record.AllowedActions = slices.DeleteFunc(record.AllowedActions, func(action string) bool {
			return action == "approve"
		})
		if err == errUnknownReviewer || err == errUnverifiedReviewers {
			record.AllowedActions = append(record.AllowedActions, "restart_initial")
		}
	}
}
