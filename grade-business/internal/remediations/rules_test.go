package remediations

import (
	"errors"
	"testing"
)

// TestValidPlanRequiresRiskDatesAndOwners 验证整改条目必须有责任、措施及一致期限。
// 参数 t 为业务规则测试上下文。
// 返回值 无。
func TestValidPlanRequiresRiskDatesAndOwners(t *testing.T) {
	base := planInput{
		ReportID: "980e2773-7631-4bee-9355-65cd5b7e5cc1", Deadline: "2028-02-29",
		Risks: []riskItem{{Risk: "high", Description: "风险项", Measure: " 修订权限 ",
			Responsibility: "运维组", Deadline: "2028-02-15"}},
	}
	if err := validatePlan(&base); err != nil || base.Risks[0].Measure != "修订权限" {
		t.Fatalf("well-formed plan rejected or unnormalized: %+v, %v", base, err)
	}
	base.Risks[0].Deadline = "2028-03-01"
	if !errors.Is(validatePlan(&base), errInvalid) {
		t.Fatal("risk deadline later than plan deadline accepted")
	}
	base.Risks[0].Deadline = "2028-02-15"
	base.Risks[0].Responsibility = "  "
	if !errors.Is(validatePlan(&base), errInvalid) {
		t.Fatal("risk without an owner accepted")
	}
	base.Risks = nil
	if !errors.Is(validatePlan(&base), errInvalid) {
		t.Fatal("plan without risks accepted")
	}
}

// TestReviewRequiresReasonAndValidDates 验证审批返回字段和日期规则。
// 参数 t 为业务规则测试上下文。
// 返回值 无。
func TestReviewRequiresReasonAndValidDates(t *testing.T) {
	input := reviewInput{Decision: "return_for_correction", Deadline: "2028-02-29",
		Comment: "意见", ExpectedVersion: 2}
	if !errors.Is(validateReview(&input), errInvalid) {
		t.Fatal("omitted return reason accepted")
	}
	input.Reason = " 责任分工不完整 "
	if err := validateReview(&input); err != nil || input.Reason != "责任分工不完整" {
		t.Fatalf("valid return rejected: %v", err)
	}
	input.Decision, input.Reason, input.Deadline = "approve", "", "2027-02-29"
	if !errors.Is(validateReview(&input), errInvalid) {
		t.Fatal("invalid calendar date accepted")
	}
}

// TestCursorRoundTrip 验证可重复的翻页定位及非法游标拒绝。
// 参数 t 为业务规则测试上下文。
// 返回值 无。
func TestCursorRoundTrip(t *testing.T) {
	id := "980e2773-7631-4bee-9355-65cd5b7e5cc1"
	at := "2026-12-30T08:11:12.387Z"
	key, err := parseCursor(makeCursor(at, id))
	if err != nil || key.ID != id || key.At != at {
		t.Fatalf("invalid cursor round trip: %+v, %v", key, err)
	}
	if _, err := parseCursor(makeCursor(at, "broken")); !errors.Is(err, errInvalid) {
		t.Fatalf("invalid cursor not rejected: %v", err)
	}
}
