package precheck

import (
	"context"
	"testing"

	"grade-business/internal/filingaudit"
)

// TestContentProcessingRecoversPerJob 验证材料读取异常后仍可处理下一项预审。
// 参数 t 为合成材料读取流程的测试上下文。
// 返回值 无。
func TestContentProcessingRecoversPerJob(t *testing.T) {
	calls := 0
	service := New(nil, func(context.Context, string) (Snapshot, error) {
		return Snapshot{Revision: 1}, nil
	}, func(context.Context, string, []Material) ([]filingaudit.Document, filingaudit.Unit, error) {
		calls++
		if calls == 1 {
			panic("测试读取器发生内部异常")
		}
		return nil, filingaudit.Unit{}, nil
	})
	progress := func(string, []filingaudit.MaterialReview, int) error { return nil }
	failed, err := service.checkContents(t.Context(), "fixture", 1, nil, nil, progress)
	if err != nil || failed.failure == "" || failed.checks == nil {
		t.Fatalf("异常任务没有返回可保存的失败状态：%+v, %v", failed, err)
	}
	next, err := service.checkContents(t.Context(), "next-fixture", 1, nil, nil, progress)
	if err != nil || next.failure != "" || next.source != "local_rules" {
		t.Fatalf("下一项任务未正常完成：%+v, %v", next, err)
	}
}
