package classification

import (
	"errors"
	"testing"
)

// TestEvaluateTables 验证各类受侵害客体与损害程度的定级矩阵。
// 参数 t 为测试上下文。
// 返回值 无。
func TestEvaluateTables(t *testing.T) {
	for _, row := range []struct {
		subject, damage string
		level           int
	}{
		{"rights", "general", 1}, {"rights", "serious", 2}, {"rights", "severe", 2},
		{"public", "general", 2}, {"public", "serious", 3}, {"public", "severe", 4},
		{"national", "general", 3}, {"national", "serious", 4}, {"national", "severe", 5},
	} {
		t.Run(row.subject+"_"+row.damage, func(t *testing.T) {
			input := Input{
				Information: []Impact{{Subject: "rights", Damage: "general"}},
				Service:     []Impact{{Subject: row.subject, Damage: row.damage}},
				Basis:       "公共服务中断可能影响已登记的业务和用户。",
			}
			result, err := Evaluate(input)
			if err != nil || result.InformationLevel != 1 || result.ServiceLevel != row.level ||
				result.SuggestedLevel != row.level || result.ExpertReview != (row.level >= 2) ||
				result.Standard != "GB/T 22240-2020" {
				t.Fatalf("定级结果 %+v，错误 %v", result, err)
			}
		})
	}
}

// TestEvaluateRejectsMissingOrDuplicateImpact 确认不完整或重复的事实不产生定级结果。
// 参数 t 为测试上下文。
// 返回值 无。
func TestEvaluateRejectsMissingOrDuplicateImpact(t *testing.T) {
	valid := []Impact{{Subject: "rights", Damage: "general"}}
	for _, input := range []Input{
		{Information: valid, Basis: "缺少服务影响"},
		{Information: valid, Service: valid, Basis: "   "},
		{Information: valid, Service: []Impact{{Subject: "public", Damage: "unknown"}}, Basis: "未知损害"},
		{Information: valid, Service: append(append([]Impact{}, valid...), valid...), Basis: "重复客体"},
	} {
		if _, err := Evaluate(input); !errors.Is(err, ErrInvalid) {
			t.Errorf("应拒绝不完整或重复的定级事实，得到 %v", err)
		}
	}
}
