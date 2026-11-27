package cases

import (
	"encoding/json"
	"testing"

	"grade-business/internal/filingpolicy"
)

// TestFilingPolicyCanDisableAndRestoreRule 验证关闭后保留配置、解除缺件拦截，并可恢复。
// 参数 t 为隔离库 HTTP 测试上下文。
// 返回值 无。
func TestFilingPolicyCanDisableAndRestoreRule(t *testing.T) {
	_, router := operatorFilingFixture(t)
	const policyPath = "/api/v1/filing-material-policy"
	initial := readResponse[filingpolicy.Policy](t, requestJSON(router, "GET", policyPath, nil), 200)
	rule := map[string]any{
		"stage": "initial", "kind": "supporting", "label": "评审意见",
		"filenameContains": "评审意见", "disabled": true,
	}
	form := map[string]any{"expectedVersion": initial.Version, "rules": []map[string]any{rule}}
	readResponse[filingpolicy.Policy](t, requestJSON(router, "PUT", policyPath, form), 200)
	saved := readResponse[struct {
		Version int64                        `json:"version"`
		Rules   []map[string]json.RawMessage `json:"rules"`
	}](t, requestJSON(router, "GET", policyPath, nil), 200)
	if saved.Version != initial.Version+1 || len(saved.Rules) != 1 ||
		string(saved.Rules[0]["disabled"]) != "true" {
		t.Fatalf("关闭状态未持久化：%+v", saved)
	}

	own := operatorFilingQuery("本地备案单位")
	item := createOperatorFiling(t, router, own)
	path := operatorFilingsURL + "/" + item.ID
	postMaterialAt(t, router, path+"/attachments"+own, "filing_form")
	postMaterialAt(t, router, path+"/attachments"+own, "classification_report")
	current := readResponse[recordDetail](t, requestJSON(router, "GET", path+own, nil), 200)
	if len(current.PolicyMaterials) != 0 {
		t.Fatalf("关闭规则仍出现在详情：%+v", current.PolicyMaterials)
	}
	readResponse[mutation](t, requestJSON(router, "POST", path+"/submit"+own, versionInput{current.Version}), 200)

	rule["disabled"] = false
	form["expectedVersion"] = saved.Version
	readResponse[filingpolicy.Policy](t, requestJSON(router, "PUT", policyPath, form), 200)
	restored := createOperatorFiling(t, router, own)
	path = operatorFilingsURL + "/" + restored.ID
	postMaterialAt(t, router, path+"/attachments"+own, "filing_form")
	postMaterialAt(t, router, path+"/attachments"+own, "classification_report")
	current = readResponse[recordDetail](t, requestJSON(router, "GET", path+own, nil), 200)
	if len(current.PolicyMaterials) != 1 || current.PolicyMaterials[0].FilenameContains != "评审意见" {
		t.Fatalf("恢复后缺少原有规则：%+v", current.PolicyMaterials)
	}
	assertError(t, requestJSON(router, "POST", path+"/submit"+own, versionInput{current.Version}),
		400, "configured_material_required")
}

// TestFilingPolicyBlocksUnmatchedMaterial 验证配置实际影响提交并受版本限制。
// 参数 t 为隔离库 HTTP 测试上下文。
// 返回值 无。
func TestFilingPolicyBlocksUnmatchedMaterial(t *testing.T) {
	_, router := operatorFilingFixture(t)
	const policyPath = "/api/v1/filing-material-policy"
	initial := readResponse[filingpolicy.Policy](t, requestJSON(router, "GET", policyPath, nil), 200)
	if len(initial.Rules) != 0 {
		t.Fatal("新业务库默认启用了额外必传项")
	}
	form := struct {
		versionInput
		Rules []filingpolicy.Rule `json:"rules"`
	}{
		versionInput{ExpectedVersion: initial.Version},
		[]filingpolicy.Rule{{Stage: filingpolicy.Initial, Kind: "supporting", Label: "评审意见", FilenameContains: "测试材料"}},
	}
	updated := readResponse[filingpolicy.Policy](t, requestJSON(router, "PUT", policyPath, form), 200)
	if updated.Version != initial.Version+1 {
		t.Fatal("材料规则版本未前进")
	}
	assertError(t, requestJSON(router, "PUT", policyPath, form), 409, "conflict")
	own := operatorFilingQuery("本地备案单位")
	item := createOperatorFiling(t, router, own)
	path := operatorFilingsURL + "/" + item.ID
	postMaterialAt(t, router, path+"/attachments"+own, "filing_form")
	postMaterialAt(t, router, path+"/attachments"+own, "classification_report")
	current := readResponse[recordDetail](t, requestJSON(router, "GET", path+own, nil), 200)
	assertError(t, requestJSON(router, "POST", path+"/submit"+own, versionInput{current.Version}), 400, "configured_material_required")
	postMaterialAt(t, router, path+"/attachments"+own, "supporting")
	current = readResponse[recordDetail](t, requestJSON(router, "GET", path+own, nil), 200)
	if result := readResponse[mutation](t, requestJSON(router, "POST", path+"/submit"+own, versionInput{current.Version}), 200); result.Status != pendingInitial {
		t.Fatalf("补齐材料后未进入初审：%+v", result)
	}
}
