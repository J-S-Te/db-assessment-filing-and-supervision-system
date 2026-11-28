package implementations

import (
	"strings"
	"time"
	_ "time/tzdata"
)

var shanghai = shanghaiLocation()

// shanghaiLocation 读取嵌入时区数据中的中国上海时区。
// 参数 无。
// 返回值 业务报备所采用的时区。
func shanghaiLocation() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic(err)
	}
	return location
}

// quotaYear 计算变更提交所属的上海自然年。
// 参数 now 为服务器接受提交的时刻。
// 返回值 中国上海自然年。
func quotaYear(now time.Time) int { return now.In(shanghai).Year() }

// planEditDeadline 获取原计划测评开始日期的上海零点。
// 参数 record 为已保存的报备。
// 返回值 计划修订的截止时刻，存量日期无法解析时为空。
func planEditDeadline(record storedRecord) *time.Time {
	start, err := time.ParseInLocation(time.DateOnly, record.PlannedStart, shanghai)
	if err != nil {
		return nil
	}
	return &start
}

// planStarted 判断当前时刻是否达到已保存计划的测评开始日零点。
// 参数 record 为已保存报备，now 为当前业务时刻。
// 返回值 已到开始日时为真。
func planStarted(record storedRecord, now time.Time) bool {
	deadline := planEditDeadline(record)
	return deadline != nil && !now.Before(*deadline)
}

// canAmendSubmittedPlan 检查发起人是否可以修订从未受理的已提交计划。
// 参数 record 为当前记录，operator 为经校验的经办人。
// 返回值 当前状态和发起人满足修订前置条件时为真。
func canAmendSubmittedPlan(record storedRecord, operator string) bool {
	return record.Status == submitted && record.AcceptedAt == nil && record.CreatedBy == operator
}

// canWithdraw 核对发起人、报备状态和测评开始前后的撤销时限。
// 参数 record 为数据库当前记录，operator 为当前操作者，now 为操作时刻。
// 返回值 符合撤销条件时为真。
func canWithdraw(record storedRecord, operator string, now time.Time) bool {
	if operator != record.CreatedBy || record.SubmittedAt == nil || now.Before(*record.SubmittedAt) {
		return false
	}
	if planStarted(record, now) {
		switch record.Status {
		case submitted, initial, final, supervision, returned:
			return true
		}
		return false
	}
	return record.Status == submitted && record.AcceptedAt == nil && now.Before(record.SubmittedAt.Add(withdrawWindow))
}

// canInstitutionEdit 判断当前机构可否编辑自建草稿或已退回报备。
// 参数 record 为已通过归属校验的报备，operator 为服务端确定的机构经办人标记。
// 返回值 报备可编辑并提交时为真。
func canInstitutionEdit(record storedRecord, operator string) bool {
	return (record.Status == returned && !operatorInitiated(record)) ||
		(record.Status == draft && record.CreatedBy == operator)
}

// institutionActions 返回当前机构可执行的动作。
// 参数 record 为已通过归属校验的报备，operator 为机构经办人标记，now 为当前业务时刻。
// 返回值 可在该报备上执行的操作代码。
func institutionActions(record storedRecord, operator string, now time.Time) []string {
	return applicantActions(record, operator, now, canInstitutionEdit(record, operator))
}

// applicantActions 汇总报备发起及补正阶段的编辑、提交和撤销动作。
// 参数 record、operator、now 为报备和经办时刻，editable 表示允许编辑草稿或补正。
// 返回值 与当前状态、时限和身份一致的可执行操作。
func applicantActions(record storedRecord, operator string, now time.Time, editable bool) []string {
	actions := []string{}
	if record.Status == draft || !planStarted(record, now) {
		if editable {
			actions = append(actions, "edit", "submit")
		} else if canAmendSubmittedPlan(record, operator) {
			actions = append(actions, "edit")
		}
	}
	if canWithdraw(record, operator, now) {
		actions = append(actions, "withdraw")
	}
	return actions
}

// canArchive 判断实施报备是否已完成监管复核且尚未归档。
// 参数 record 为当前实施报备记录。
// 返回值 允许归档时为真。
func canArchive(record storedRecord) bool {
	return record.Status == approved && record.ArchivedAt == nil
}

// nextStatus 按当前审核节点计算操作后的状态，归档沿用已有审核结论。
// 参数 record 为数据库当前记录，action、comment 为命令，operator 和 now 为操作人和时刻。
// 返回值 新状态或领域拒绝原因。
func nextStatus(record storedRecord, action, comment, operator string, now time.Time) (string, error) {
	switch action {
	case "submit":
		if record.Status == returned && planStarted(record, now) {
			return "", errPlanClosed
		}
		if record.Status == draft || record.Status == returned {
			return submitted, nil
		}
	case "accept":
		if record.Status == submitted {
			return initial, nil
		}
	case "approve":
		switch record.Status {
		case initial:
			return final, nil
		case final:
			return supervision, nil
		case supervision:
			return approved, nil
		}
	case "return_for_correction":
		if record.Status == initial || record.Status == final || record.Status == supervision {
			if strings.TrimSpace(comment) == "" {
				return "", errComment
			}
			return returned, nil
		}
	case "restart_initial":
		if record.Status == final || record.Status == supervision {
			if strings.TrimSpace(comment) == "" {
				return "", errRestartComment
			}
			return initial, nil
		}
	case "withdraw":
		if !canWithdraw(record, operator, now) {
			return "", errWithdrawal
		}
		return withdrawn, nil
	case "archive":
		if canArchive(record) {
			return approved, nil
		}
	default:
		return "", errInvalid
	}
	return "", errConflict
}

// allowedActions 根据记录和服务端时刻告知页面可操作项。
// 参数 record 为数据库当前记录，operator 和 now 为操作人和时刻。
// 返回值 页面操作代码，空结果为非空数组。
func allowedActions(record storedRecord, operator string, now time.Time) []string {
	switch record.Status {
	case draft, returned:
		return applicantActions(record, operator, now, true)
	case submitted:
		return append([]string{"accept"}, applicantActions(record, operator, now, false)...)
	case initial, final, supervision:
		return append([]string{"approve", "return_for_correction"}, applicantActions(record, operator, now, false)...)
	case approved:
		if canArchive(record) {
			return []string{"archive"}
		}
	}
	return []string{}
}
