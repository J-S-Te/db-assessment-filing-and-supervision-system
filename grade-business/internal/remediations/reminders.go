package remediations

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/access"
	"grade-business/internal/httpx"
	"grade-business/internal/reviewinbox"
)

const reminderEligible = `p.status='returned' AND p.deadline BETWEEN ` + today + ` AND ` + today + ` + 10`

type reminderInput struct {
	PlanIDs []string `json:"planIds"`
}

type reminderResult struct {
	PlanID           string `json:"planId"`
	OrganizationName string `json:"organizationName,omitempty"`
	NoticeID         string `json:"noticeId,omitempty"`
	Status           string `json:"status"`
}

// handleReminders 校验并处理运营者整改规划的批量催办请求。
// 参数 w 为响应，r 携带监管端明确选择的规划标识。
// 返回值 无。
func (s *Service) handleReminders(w http.ResponseWriter, r *http.Request) {
	var input reminderInput
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil || validateReminderIDs(input.PlanIDs) != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_input", "请选择 1 至 50 条不同的整改规划。")
		return
	}
	items, err := s.sendReminders(r.Context(), input.PlanIDs)
	respond(w, http.StatusOK, collection[reminderResult]{Items: items}, err)
}

// validateReminderIDs 约束单次催办规模并规范化规划标识。
// 参数 ids 为用户选择的规划标识切片，可在原位转换 UUID 字母大小写。
// 返回值 选择有效时为 nil，空选择、重复、非法或超量时返回校验错误。
func validateReminderIDs(ids []string) error {
	if len(ids) < 1 || len(ids) > 50 {
		return errInvalid
	}
	seen := make(map[string]bool, len(ids))
	for i, id := range ids {
		canonical := strings.ToLower(id)
		if !validID(canonical) || seen[canonical] {
			return errInvalid
		}
		seen[canonical] = true
		ids[i] = canonical
	}
	return nil
}

// sendReminders 在同一事务中锁定明确选择的规划并登记送达，保持响应和选择同序。
// 参数 ctx 为请求上下文，ids 为已验证的规划标识。
// 返回值 每条规划的处理结果，数据库异常时整批回滚并返回错误。
func (s *Service) sendReminders(ctx context.Context, ids []string) ([]reminderResult, error) {
	positions := make([]int, len(ids))
	items := make([]reminderResult, len(ids))
	for i := range positions {
		positions[i] = i
	}
	// 批次可能选择重叠规划，按 UUID 序获取行锁避免循环等待。
	slices.SortFunc(positions, func(i, j int) int { return strings.Compare(ids[i], ids[j]) })
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		for _, i := range positions {
			item, err := sendReminder(ctx, tx, ids[i])
			if err != nil {
				return err
			}
			items[i] = item
		}
		return nil
	})
	return items, err
}

// sendReminder 核对持锁时的状态、临期范围和备案归属并幂等登记催办。
// 参数 ctx 为请求上下文，tx 为催办事务，id 为已选择的规划标识。
// 返回值 单条催办结果，或查询及通知写入错误。
func sendReminder(ctx context.Context, tx pgx.Tx, id string) (reminderResult, error) {
	item := reminderResult{PlanID: id, Status: "ineligible"}
	notice := reviewinbox.Notice{
		Audience: "operator", Resource: "remediations", ResourceID: id, State: "reminder",
	}
	var deadline, day string
	err := tx.QueryRow(ctx, `SELECT p.version,f.organization_name,r.number,f.system_name,
		to_char(p.deadline,'YYYY-MM-DD'),to_char(`+today+`,'YYYY-MM-DD') `+joins+`
		WHERE p.id=$1 AND (`+reminderEligible+`)
		AND `+access.FilingDistrictScope("f.id", "$2", true)+` FOR UPDATE OF p FOR SHARE OF r,f`,
		id, access.Districts(ctx)).
		Scan(&notice.Version, &notice.Recipient, &notice.Reference, &notice.Subject, &deadline, &day)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, nil
	}
	if err != nil {
		return item, err
	}
	notice.Comment = "此次规划的整改期限为 " + deadline + "，请完成补正并重新提交。"
	noticeID, sent, err := reviewinbox.DeliverReminder(ctx, tx, notice, day)
	if err != nil {
		return item, err
	}
	item.OrganizationName, item.NoticeID = notice.Recipient, noticeID
	item.Status = "already_sent"
	if sent {
		item.Status = "sent"
	}
	return item, nil
}
