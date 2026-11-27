package filingpolicy

import "testing"

// TestConfiguredMaterialsRequireExactStageAndMatchingName 验证同类不同文件及不同阶段不能相互替代。
// 参数 t 为单元测试上下文。
// 返回值 无。
func TestConfiguredMaterialsRequireExactStageAndMatchingName(t *testing.T) {
	rules := []Rule{
		{Stage: Initial, Kind: "supporting", Label: "专家评审意见", FilenameContains: "评审意见"},
		{Stage: Cancel, Kind: "supporting", Label: "销毁说明", FilenameContains: "销毁"},
	}
	if found := evidence(rules[0], []Material{{ID: "other", Kind: "supporting", FileName: "其他说明.pdf"}}); found != "" {
		t.Fatalf("把不相关文件作为证明：%s", found)
	}
	policy, err := normalize(rules)
	if err != nil || len(policy.For(Cancel)) != 1 || policy.For(Cancel)[0].Label != "销毁说明" {
		t.Fatalf("配置 %+v，错误 %v", policy, err)
	}
	if found := evidence(rules[0], []Material{{ID: "match", Kind: "supporting", FileName: "专家评审意见.pdf"}}); found != "match" {
		t.Fatalf("未找到匹配的材料：%s", found)
	}
	if _, err := normalize([]Rule{
		{Stage: Initial, Kind: "supporting", Label: "意见", FilenameContains: "意见"},
		{Stage: Initial, Kind: "supporting", Label: "意见", FilenameContains: "其他"},
	}); err == nil {
		t.Fatal("接受了同一阶段的重复规则名称")
	}
}
