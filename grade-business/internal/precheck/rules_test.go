package precheck

import "testing"

// TestRulesUseOnlyRecordedMaterials 检查规则结果及证据只来源于已保存材料。
// 参数 t 为单元测试上下文。
// 返回值 无。
func TestRulesUseOnlyRecordedMaterials(t *testing.T) {
	found := evaluate([]Material{{ID: "recorded-form", Kind: "filing_form"}, {ID: "unrelated", Kind: "supporting"}})
	if len(found) != 2 || found[0].RuleID != "filing_form.present" || found[0].AttachmentID != "recorded-form" ||
		found[1].RuleID != "classification_report.missing" || found[1].AttachmentID != "" {
		t.Fatalf("unexpected findings %+v", found)
	}
}
