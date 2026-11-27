package implementations

import (
	"errors"
	"testing"
	"time"
)

// TestImplementationProgression 验证受理、初审、终审和监管复核为不同步骤。
// 参数 t 为测试上下文。
// 返回值 无。
func TestImplementationProgression(t *testing.T) {
	now := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	cases := []struct{ before, action, after string }{
		{"draft", "submit", "submitted"},
		{"submitted", "accept", "pending_initial"},
		{"pending_initial", "approve", "pending_final"},
		{"pending_final", "approve", "pending_supervision"},
		{"pending_supervision", "approve", "approved"},
		{"pending_supervision", "return_for_correction", "returned"},
		{"returned", "submit", "submitted"},
	}
	for _, tc := range cases {
		after, err := nextStatus(storedRecord{detail: detail{summary: summary{Status: tc.before}}}, tc.action, "补充实施范围", "测试监管", now)
		if err != nil || after != tc.after {
			t.Errorf("%s + %s = %q, %v", tc.before, tc.action, after, err)
		}
	}
	for _, tc := range []struct{ before, action, comment string }{
		{"submitted", "approve", ""}, {"approved", "accept", ""},
		{"pending_initial", "return_for_correction", "  "}, {"withdrawn", "submit", ""},
	} {
		if _, err := nextStatus(storedRecord{detail: detail{summary: summary{Status: tc.before}}}, tc.action, tc.comment, "测试监管", now); err == nil {
			t.Errorf("invalid transition accepted: %s + %s", tc.before, tc.action)
		}
	}
}

// TestWithdrawalRequiresOriginalOperatorAndUnacceptedWindow 验证撤销的发起人、受理及时间条件。
// 参数 t 为测试上下文。
// 返回值 无。
func TestWithdrawalRequiresOriginalOperatorAndUnacceptedWindow(t *testing.T) {
	submitted := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	record := storedRecord{detail: detail{summary: summary{Status: "submitted", SubmittedAt: &submitted}}, CreatedBy: "报备人"}
	last := submitted.Add(24*time.Hour - time.Nanosecond)
	if after, err := nextStatus(record, "withdraw", "", "报备人", last); err != nil || after != "withdrawn" {
		t.Fatalf("valid withdrawal = %q %v", after, err)
	}
	if _, err := nextStatus(record, "withdraw", "", "报备人", submitted.Add(24*time.Hour)); !errors.Is(err, errWithdrawal) {
		t.Fatalf("deadline error = %v", err)
	}
	if _, err := nextStatus(record, "withdraw", "", "其他报备人", last); !errors.Is(err, errWithdrawal) {
		t.Fatalf("wrong operator error = %v", err)
	}
	record.AcceptedAt = &submitted
	if _, err := nextStatus(record, "withdraw", "", "报备人", last); !errors.Is(err, errWithdrawal) {
		t.Fatalf("already accepted error = %v", err)
	}
}

// TestQuotaUsesShanghaiYear 验证 UTC 日期仍为上年时上海已进入新配额年度。
// 参数 t 为测试上下文。
// 返回值 无。
func TestQuotaUsesShanghaiYear(t *testing.T) {
	boundary := time.Date(2026, 12, 31, 16, 0, 0, 0, time.UTC)
	if got := quotaYear(boundary.Add(-time.Nanosecond)); got != 2026 {
		t.Errorf("before Shanghai midnight: %d", got)
	}
	if got := quotaYear(boundary); got != 2027 {
		t.Errorf("at Shanghai midnight: %d", got)
	}
}

// TestImplementationValidation 检查关联、日期顺序及必填业务字段。
// 参数 t 为测试上下文。
// 返回值 无。
func TestImplementationValidation(t *testing.T) {
	item := input{FilingID: "00000000-0000-4000-8000-000000000010", Type: "change", Reporter: " 王工 ",
		PlannedStart: "2026-11-23", PlannedEnd: "2026-11-25", Plan: "评估实施范围。"}
	if clean, err := validateInput(item); err != nil || clean.Reporter != "王工" {
		t.Fatalf("unexpected validation: %+v, %v", clean, err)
	}
	item.PlannedEnd = "2026-11-22"
	if _, err := validateInput(item); err == nil {
		t.Error("逆序日期被接受")
	}
	item.PlannedEnd, item.FilingID = "2026-11-25", "invalid"
	if _, err := validateInput(item); err == nil {
		t.Error("无效备案标识被接受")
	}
}
