package cases

import (
	"testing"

	"grade-business/internal/classification"
)

// TestFilingClassificationEvidence 核对初步定级可预览、随草稿留痕且不覆盖正式申报等级。
// 参数 t 为带隔离 PostgreSQL 的测试上下文。
// 返回值 无。
func TestFilingClassificationEvidence(t *testing.T) {
	_, router := operatorFilingFixture(t)
	input := classification.Input{
		Information: []classification.Impact{{Subject: "public", Damage: "serious"}},
		Service:     []classification.Impact{{Subject: "rights", Damage: "general"}},
		Basis:       "中断将影响业务申报与公共服务的处理。",
	}
	const preview = "/api/v1/filings/classification-preview"
	result := readResponse[classification.Assessment](t, requestJSON(router, "POST", preview, input), 200)
	if result.SuggestedLevel != 3 || !result.ExpertReview {
		t.Fatalf("初步定级结果错误 %+v", result)
	}
	own := operatorFilingQuery("本地备案单位")
	form := map[string]any{
		"number": "CLASSIFICATION-01", "systemName": "定级自评系统",
		"assessmentOrg": "隔离测评机构", "level": "2",
		"classificationAssessment": input,
	}
	created := readResponse[mutation](t, requestJSON(router, "POST", operatorFilingsURL+own, form), 201)
	path := operatorFilingsURL + "/" + created.ID
	item := readResponse[map[string]any](t, requestJSON(router, "GET", path+own, nil), 200)
	assessment, ok := item["classificationAssessment"].(map[string]any)
	if !ok || assessment["suggestedLevel"] != float64(3) || item["level"] != "2" {
		t.Fatalf("自评依据或申报等级错误 %+v", item)
	}
	delete(form, "classificationAssessment")
	form["expectedVersion"] = created.Version
	readResponse[mutation](t, requestJSON(router, "PATCH", path+own, form), 200)
	item = readResponse[map[string]any](t, requestJSON(router, "GET", path+own, nil), 200)
	if item["classificationAssessment"] == nil {
		t.Fatal("旧版编辑请求丢失了已有自评依据")
	}
	assertError(t, requestJSON(router, "GET", path+operatorFilingQuery("另一单位"), nil), 404, "not_found")
}
