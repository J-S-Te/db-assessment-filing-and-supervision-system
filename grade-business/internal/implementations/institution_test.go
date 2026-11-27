package implementations

import (
	"net/http"
	"net/url"
	"slices"
	"testing"
)

// TestInstitutionImplementationScope 验证实施详情及写操作只对报备登记机构开放。
// 参数 t 为隔离数据库集成测试上下文。
// 返回值 无。
func TestInstitutionImplementationScope(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(pool)
	filing := implementationTestFiling(t, pool, "测试备案-机构范围", approved)
	item := testInput(filing, routine)
	item.AssessmentOrg = "实际实施机构"
	record := returnedImplementation(t, mux, item)
	resource := "/api/v1/institution/implementations/" + record.ID
	own := "?institution=" + url.QueryEscape(item.AssessmentOrg)
	other := "?institution=" + url.QueryEscape("备案测评机构")
	result := decodeResponse[detail](t, callImplementation(mux, "GET", resource+own, nil), 200)
	if result.Version != record.Version || result.FilingID != filing || result.AssessmentOrg != item.AssessmentOrg ||
		!slices.Equal(result.AllowedActions, []string{"edit", "submit"}) {
		t.Fatalf("机构详情 = %+v", result)
	}
	patch := patchInput{input: item, ExpectedVersion: record.Version}
	submit := command{Action: "submit", ExpectedVersion: record.Version}
	decodeResponse[map[string]string](t, callImplementation(mux, "GET", resource+other, nil), 404)
	decodeResponse[map[string]string](t, callImplementation(mux, "PATCH", resource+other, patch), 404)
	decodeResponse[map[string]string](t, callImplementation(mux, "POST", resource+"/actions"+other, submit), 404)
	for _, query := range []string{"", own + "&institution=other", own + "&unexpected=1"} {
		decodeResponse[map[string]string](t, callImplementation(mux, "GET", resource+query, nil), 400)
	}
	decodeResponse[map[string]string](t, callImplementation(mux, "POST", resource+"/actions"+own,
		command{Action: "approve", ExpectedVersion: record.Version}), 400)
}

// TestInstitutionImplementationCorrection 验证机构补正锁住关键字段并沿用版本与变更额度。
// 参数 t 为隔离数据库集成测试上下文。
// 返回值 无。
func TestInstitutionImplementationCorrection(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(pool)
	filing := implementationTestFiling(t, pool, "测试备案-机构补正", approved)
	otherFiling := implementationTestFiling(t, pool, "测试备案-禁止重关联", approved)
	item := testInput(filing, change)
	item.AssessmentOrg = "实际实施机构"
	record := returnedImplementation(t, mux, item)
	resource := "/api/v1/institution/implementations/" + record.ID
	scope := "?institution=" + url.QueryEscape(item.AssessmentOrg)
	assertInstitutionImplementationImmutable(t, mux, resource+scope, item, otherFiling, record.Version)
	item.AssessmentOrg = ""
	item.Plan = "按监管意见补充割接和回退方案。"
	corrected := decodeResponse[mutation](t, callImplementation(mux, "PATCH", resource+scope,
		patchInput{input: item, ExpectedVersion: record.Version}), 200)
	if corrected.ID != record.ID || corrected.Status != returned || corrected.Version != record.Version+1 {
		t.Fatalf("补正结果 = %+v", corrected)
	}
	assertInstitutionResubmission(t, mux, resource, scope, record, corrected, item)
	assertQuota(t, mux, filing, 1)
}

// TestInstitutionImplementationDraftState 验证监管录入的实施草稿在机构侧保持只读。
// 参数 t 为隔离数据库集成测试上下文。
// 返回值 无。
func TestInstitutionImplementationDraftState(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(pool)
	filing := implementationTestFiling(t, pool, "测试备案-实施草稿", approved)
	item := testInput(filing, routine)
	record := decodeResponse[mutation](t, callImplementation(mux, "POST", path, item), 201)
	resource := "/api/v1/institution/implementations/" + record.ID
	scope := firstImplementationScope()
	detail := decodeResponse[detail](t, callImplementation(mux, "GET", resource+scope, nil), 200)
	if detail.Status != draft || len(detail.AllowedActions) != 0 {
		t.Fatalf("草稿详情 = %+v", detail)
	}
	decodeResponse[map[string]string](t, callImplementation(mux, "PATCH", resource+scope,
		patchInput{input: item, ExpectedVersion: record.Version}), 409)
	decodeResponse[map[string]string](t, callImplementation(mux, "POST", resource+"/actions"+scope,
		command{Action: "submit", ExpectedVersion: record.Version}), 409)
}

// returnedImplementation 通过真实监管接口生成一份退回补正的实施报备。
// 参数 t 为测试上下文，mux 为业务路由，item 为报备表单。
// 返回值 当前退回状态的记录版本。
func returnedImplementation(t *testing.T, mux *http.ServeMux, item input) mutation {
	t.Helper()
	record := decodeResponse[mutation](t, callImplementation(mux, "POST", path, item), 201)
	record = performAction(t, mux, record, "submit", "")
	record = performAction(t, mux, record, "accept", "")
	return performAction(t, mux, record, "return_for_correction", "补充割接和回退方案。")
}

// assertInstitutionImplementationImmutable 检查机构不能改变原备案、报备类型及机构。
// 参数 t 为测试上下文，mux 为业务路由，endpoint 为带机构的报备路径，
// item 为原表单，otherFiling 为替换备案标识，version 为退回版本。
// 返回值 无。
func assertInstitutionImplementationImmutable(t *testing.T, mux *http.ServeMux, endpoint string,
	item input, otherFiling string, version int64) {
	t.Helper()
	changedFiling, changedType, changedInstitution := item, item, item
	changedFiling.FilingID = otherFiling
	changedType.Type = routine
	changedInstitution.AssessmentOrg = "另一家测评机构"
	for _, value := range []input{changedFiling, changedType, changedInstitution} {
		response := callImplementation(mux, "PATCH", endpoint, patchInput{input: value, ExpectedVersion: version})
		if failure := decodeResponse[map[string]string](t, response, 409); failure["code"] != "invalid_state" {
			t.Fatalf("更改锁定字段返回 = %+v", failure)
		}
	}
}

// assertInstitutionResubmission 检查旧版本、补正重提和监管详情同步。
// 参数 t 为测试上下文，mux 为业务路由，resource、scope 为路径和机构条件，
// before、corrected 为补正前后版本，item 为补正表单。
// 返回值 无。
func assertInstitutionResubmission(t *testing.T, mux *http.ServeMux, resource, scope string,
	before, corrected mutation, item input) {
	t.Helper()
	actions := resource + "/actions" + scope
	stale := callImplementation(mux, "POST", actions, command{Action: "submit", ExpectedVersion: before.Version})
	if failure := decodeResponse[map[string]string](t, stale, 409); failure["code"] != "stale_version" {
		t.Fatalf("旧版重提返回 = %+v", failure)
	}
	submission := command{Action: "submit", ExpectedVersion: corrected.Version}
	result := decodeResponse[mutation](t, callImplementation(mux, "POST", actions, submission), 200)
	if result.Status != submitted || result.Version != corrected.Version+1 {
		t.Fatalf("补正重提 = %+v", result)
	}
	decodeResponse[map[string]string](t, callImplementation(mux, "POST", actions, submission), 409)
	current := decodeResponse[detail](t, callImplementation(mux, "GET", path+"/"+result.ID, nil), 200)
	if current.Version != result.Version || current.Plan != item.Plan || current.FilingID != item.FilingID ||
		current.AssessmentOrg != "实际实施机构" || current.Type != item.Type || current.Status != submitted {
		t.Fatalf("监管收到的补正版本 = %+v", current)
	}
}
