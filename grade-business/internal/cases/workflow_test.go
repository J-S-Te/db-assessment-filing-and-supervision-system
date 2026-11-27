package cases

import (
	"errors"
	"testing"
)

// TestFilingTransitions 验证初审、终审、补正和重提的状态约束。
// 参数 t 负责报告测试状态。
// 返回值 无。
func TestFilingTransitions(t *testing.T) {
	cases := []struct {
		name, status, action, comment, want string
		ready                               bool
		wantError                           error
	}{
		{"草稿提交", "draft", "submit", "", "pending_initial", true, nil},
		{"缺少材料", "draft", "submit", "", "", false, ErrMaterials},
		{"草稿无法审核", "draft", "approve", "", "", true, ErrConflict},
		{"初审通过", "pending_initial", "approve", "", "pending_final", true, nil},
		{"终审通过", "pending_final", "approve", "", "approved", true, nil},
		{"初审退回", "pending_initial", "return_for_correction", "补充材料", "returned", true, nil},
		{"终审退回", "pending_final", "return_for_correction", "核对原文", "returned", true, nil},
		{"空白意见拦截", "pending_final", "return_for_correction", "  ", "", true, ErrInvalid},
		{"补正重提", "returned", "submit", "", "pending_initial", true, nil},
		{"终审后禁止覆盖", "approved", "return_for_correction", "内容", "", true, ErrConflict},
		{"未知动作拦截", "pending_initial", "delete", "", "", true, ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := transition(tc.status, tc.action, tc.comment, tc.ready)
			if got != tc.want || !errors.Is(err, tc.wantError) {
				t.Fatalf("transition = %q, %v; want %q, %v", got, err, tc.want, tc.wantError)
			}
		})
	}
}

// TestFileNameBoundary 验证材料文件名和允许格式的边界。
// 参数 t 负责报告测试状态。
// 返回值 无。
func TestFileNameBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, extension string
		valid           bool
	}{
		{"备案资料.PDF", ".pdf", true},
		{"定级报告.docx", ".docx", true},
		{"情况汇总.xlsx", ".xlsx", true},
		{"补充材料.zip", ".zip", true},
		{"..\\其他.zip", "", false},
		{"../其他.zip", "", false},
		{"无效.txt", "", false},
		{"bad\nname.pdf", "", false},
	} {
		extension, err := attachmentExtension(tc.name)
		if extension != tc.extension || (err == nil) != tc.valid {
			t.Errorf("attachmentExtension(%q) = %q, %v", tc.name, extension, err)
		}
	}
}
