package httpx

import (
	"testing"
)

// TestCSVText 核对导出字段对电子表格公式前缀的转义。
// 参数 t 用于报告漏转义或误转义的导出值。
// 返回值 无。
func TestCSVText(t *testing.T) {
	for _, item := range []struct {
		value, want string
	}{
		{"=SUM(A1)", "'=SUM(A1)"},
		{"+1", "'+1"},
		{"-1", "'-1"},
		{"@cmd", "'@cmd"},
		{"[1] 工作表", "'[1] 工作表"},
		{"＝全角等号", "'＝全角等号"},
		{"［全角方括号", "'［全角方括号"},
		{"\t=制表符后公式", "'\t=制表符后公式"},
		{"普通文字", "普通文字"},
		{"备注: 2026-11-28", "备注: 2026-11-28"},
	} {
		if got := CSVText(item.value); got != item.want {
			t.Errorf("CSVText(%q)=%q，应为 %q", item.value, got, item.want)
		}
	}
}
