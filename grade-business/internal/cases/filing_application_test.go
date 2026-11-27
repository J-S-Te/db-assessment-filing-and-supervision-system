package cases

import (
	"net/http"
	"net/http/httptest"
	"slices"

	"sync"
	"testing"
)

const applicationURL = "/api/v1/filing-applications"

// approvedFiling 创建一条实际经过完整审核流程的备案。
// 参数 t 为测试上下文，handler 为实际路由，number 为备案编号。
// 返回值 终审通过的备案标识、状态和版本。
func approvedFiling(t *testing.T, handler http.Handler, number string) mutation {
	t.Helper()
	f := createFiling(t, handler, number)
	path := filingsURL + "/" + f.ID
	postMaterial(t, handler, path, "filing_form")
	postMaterial(t, handler, path, "classification_report")
	readResponse[mutation](t, requestJSON(handler, "POST", path+"/submit", versionInput{3}), 200)
	readResponse[mutation](t, reviewRequest(handler, path, "approve", "", 4), 200)
	return readResponse[mutation](t, reviewRequest(handler, path, "approve", "", 5), 200)
}

// TestFilingApplicationChange 验证主备案在申请终审生效并持久化前后差异。
// 参数 t 为测试上下文。
// 返回值 无。
func TestFilingApplicationChange(t *testing.T) {
	_, handler := fixtureService(t)
	f := approvedFiling(t, handler, "BA-CHANGE")
	input := map[string]any{"filingId": f.ID, "type": "change", "reason": "系统升级",
		"changes": map[string]string{"systemName": "新系统"}}
	app := readResponse[mutation](t, requestJSON(handler, "POST", applicationURL, input), 201)
	appPath := applicationURL + "/" + app.ID
	filingPath := filingsURL + "/" + f.ID
	readResponse[mutation](t, requestJSON(handler, "POST", appPath+"/submit", versionInput{app.Version}), 200)
	returned := readResponse[mutation](t, reviewRequest(handler, appPath, "return_for_correction", "补充依据", 2), 200)
	patch := map[string]any{"reason": "补充升级依据", "expectedVersion": returned.Version,
		"changes": map[string]string{"systemName": "新监管系统", "level": "4"}}
	readResponse[mutation](t, requestJSON(handler, "PATCH", appPath, patch), 200)
	assertError(t, requestJSON(handler, "PATCH", appPath, patch), 409, "conflict")
	readResponse[mutation](t, requestJSON(handler, "POST", appPath+"/submit", versionInput{4}), 200)
	readResponse[mutation](t, reviewRequest(handler, appPath, "approve", "", 5), 200)
	original := readResponse[recordDetail](t, requestJSON(handler, "GET", filingPath, nil), 200)
	if original.SystemName != "监管系统" || original.Version != f.Version {
		t.Fatalf("original filing changed before final review: %+v", original)
	}
	readResponse[mutation](t, reviewRequest(handler, appPath, "approve", "", 6), 200)
	assertApplicationChange(t, handler, appPath, filingPath, f.Version+1)
}

// assertApplicationChange 核对生效字段、变更前后值和审核留痕。
// 参数 t 为测试上下文，handler 为业务路由，appPath 与 filingPath 为两个记录地址，version 为主备案版本。
// 返回值 无。
func assertApplicationChange(t *testing.T, handler http.Handler, appPath, filingPath string, version int64) {
	t.Helper()
	changed := readResponse[recordDetail](t, requestJSON(handler, "GET", filingPath, nil), 200)
	if changed.SystemName != "新监管系统" || changed.Level != "4" || changed.Version != version {
		t.Fatalf("unexpected effective filing: %+v", changed)
	}
	result := readResponse[struct {
		AppliedAt *string
		Changes   []struct{ Field, Before, After string }
	}](t, requestJSON(handler, "GET", appPath, nil), 200)
	if len(result.Changes) != 2 || result.Changes[0].Field != "systemName" ||
		result.Changes[0].Before != "监管系统" || result.Changes[0].After != "新监管系统" || result.AppliedAt == nil {
		t.Fatalf("unexpected audit snapshot: %+v", result)
	}
	history := readResponse[itemResponse[reviewEvent]](
		t, requestJSON(handler, "GET", appPath+"/review-history", nil), 200).Items
	if len(history) != 5 || history[3].Comment != "补充依据" || history[0].ToStatus != approved {
		t.Fatalf("unexpected lifecycle history: %+v", history)
	}
}

// TestFilingApplicationCancellation 验证并发发起、注销生效及历史关联保留。
// 参数 t 为测试上下文。
// 返回值 无。
func TestFilingApplicationCancellation(t *testing.T) {
	_, handler := fixtureService(t)
	f := approvedFiling(t, handler, "BA-CANCEL")
	reportForm := reportInput{Number: "BG-KEEP", FilingID: f.ID, AssessmentOrg: "测评中心", Risk: "low"}
	report := readResponse[mutation](t, requestJSON(handler, "POST", reportsURL, reportForm), 201)
	app := concurrentCancelDraft(t, handler, f.ID)
	path := applicationURL + "/" + app.ID
	readResponse[mutation](t, requestJSON(handler, "POST", path+"/submit", versionInput{1}), 200)
	readResponse[mutation](t, reviewRequest(handler, path, "approve", "", 2), 200)
	readResponse[mutation](t, reviewRequest(handler, path, "approve", "", 3), 200)
	filing := readResponse[map[string]any](t, requestJSON(handler, "GET", filingsURL+"/"+f.ID, nil), 200)
	if filing["cancelledAt"] == nil || filing["status"] != approved {
		t.Fatalf("unexpected cancelled filing: %+v", filing)
	}
	got := readResponse[listResponse](t, requestJSON(handler, "GET", filingsURL+"?lifecycle=cancelled", nil), 200)
	if len(got.Items) != 1 || got.Items[0].ID != f.ID {
		t.Fatalf("unexpected lifecycle filter result: %+v", got)
	}
	reportForm.Number = "BG-BLOCK"
	assertError(t, requestJSON(handler, "POST", reportsURL, reportForm), 409, "filing_inactive")
	old := readResponse[recordDetail](t, requestJSON(handler, "GET", reportsURL+"/"+report.ID, nil), 200)
	if old.FilingID != f.ID {
		t.Fatalf("old report lost its filing link: %+v", old)
	}
}

// concurrentCancelDraft 验证同一备案的两个并发请求只创建一个活动申请。
// 参数 t 为测试上下文，handler 为实际路由，filingID 为已通过备案标识。
// 返回值 获准创建的申请信息。
func concurrentCancelDraft(t *testing.T, handler http.Handler, filingID string) mutation {
	t.Helper()
	form := map[string]string{"filingId": filingID, "type": "cancel", "reason": "系统下线"}
	var group sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		group.Go(func() { results <- requestJSON(handler, "POST", applicationURL, form) })
	}
	group.Wait()
	first, second := <-results, <-results
	if first.Code == 409 {
		first, second = second, first
	}
	app := readResponse[mutation](t, first, 201)
	assertError(t, second, 409, "active_application")
	return app
}

// TestFilingApplicationAtomicApproval 验证终审留痕失败会回滚申请和主备案。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestFilingApplicationAtomicApproval(t *testing.T) {
	service, handler := fixtureService(t)
	f := approvedFiling(t, handler, "BA-ATOMIC-APPLICATION")
	form := map[string]string{"filingId": f.ID, "type": "cancel", "reason": "系统下线"}
	created := readResponse[mutation](t, requestJSON(handler, "POST", applicationURL, form), 201)
	path := applicationURL + "/" + created.ID
	readResponse[mutation](t, requestJSON(handler, "POST", path+"/submit", versionInput{1}), 200)
	readResponse[mutation](t, reviewRequest(handler, path, "approve", "", 2), 200)
	_, err := service.db.Exec(t.Context(), `ALTER TABLE filing_application_review_events
		ADD CONSTRAINT qa_block_final_review CHECK (to_status <> 'approved')`)
	if err != nil {
		t.Fatal(err)
	}
	assertError(t, reviewRequest(handler, path, "approve", "", 3), 500, "internal_error")
	actual := readResponse[applicationDetail](t, requestJSON(handler, "GET", path, nil), 200)
	filing := readResponse[recordDetail](t, requestJSON(handler, "GET", filingsURL+"/"+f.ID, nil), 200)
	if actual.Version != 3 || actual.Status != pendingFinal || actual.AppliedAt != nil ||
		filing.CancelledAt != nil || filing.Version != f.Version {
		t.Fatalf("failed audit changed application or linked filing: %+v %+v", actual, filing)
	}
	history := readResponse[itemResponse[reviewEvent]](
		t, requestJSON(handler, "GET", path+"/review-history", nil), 200)
	if len(history.Items) != 2 || history.Items[0].ToStatus != pendingFinal {
		t.Fatalf("unexpected persisted history: %+v", history)
	}
}

// TestFilingApplicationListAndValidation 验证可发起范围、稀疏字段及关联筛选和分页。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestFilingApplicationListAndValidation(t *testing.T) {
	_, handler := fixtureService(t)
	pending := createFiling(t, handler, "BA-PENDING-APPLICATION")
	assertError(t, requestJSON(handler, "POST", applicationURL,
		map[string]string{"filingId": pending.ID, "type": "cancel", "reason": "停用"}), 409, "filing_inactive")
	f := approvedFiling(t, handler, "BA-APP-LIST")
	g := approvedFiling(t, handler, "BA-APP-OTHER")
	input := map[string]any{"filingId": f.ID, "type": "change", "reason": "系统升级",
		"changes": map[string]string{"systemName": "监管系统"}}
	assertError(t, requestJSON(handler, "POST", applicationURL, input), 400, "invalid_input")
	input["changes"] = map[string]string{"level": "6"}
	assertError(t, requestJSON(handler, "POST", applicationURL, input), 400, "invalid_input")
	input["changes"] = map[string]string{"systemName": "新名称", "unsupported": "extra"}
	assertError(t, requestJSON(handler, "POST", applicationURL, input), 400, "invalid_input")
	input["changes"] = map[string]string{"systemName": "新名称"}
	first := readResponse[mutation](t, requestJSON(handler, "POST", applicationURL, input), 201)
	input["filingId"] = g.ID
	second := readResponse[mutation](t, requestJSON(handler, "POST", applicationURL, input), 201)
	assertApplicationList(t, handler, f.ID, first.ID, second.ID)
}

// assertApplicationList 核对搜索分页稳定性以及关联申请范围。
// 参数 t 为测试上下文，handler 为业务路由，filingID 为限定备案，firstID 和 secondID 为两条申请。
// 返回值 无。
func assertApplicationList(t *testing.T, handler http.Handler, filingID, firstID, secondID string) {
	t.Helper()
	query := applicationURL + "?q=BA-APP&type=change&status=draft&limit=1"
	page := readResponse[applicationPage](t, requestJSON(handler, "GET", query, nil), 200)
	if len(page.Items) != 1 || page.Items[0].ID != secondID || page.NextCursor == "" {
		t.Fatalf("unexpected first page: %+v", page)
	}
	page = readResponse[applicationPage](t, requestJSON(handler, "GET", query+"&cursor="+page.NextCursor, nil), 200)
	if len(page.Items) != 1 || page.Items[0].ID != firstID || page.NextCursor != "" {
		t.Fatalf("unexpected second page: %+v", page)
	}
	scoped := applicationURL + "?filingId=" + filingID
	page = readResponse[applicationPage](t, requestJSON(handler, "GET", scoped, nil), 200)
	if len(page.Items) != 1 || page.Items[0].ID != firstID {
		t.Fatalf("unexpected filing scope: %+v", page)
	}
	assertError(t, requestJSON(handler, "GET", scoped+"&type=cancel&type=change", nil), 400, "invalid_input")
}

// TestFilingApplicationWithdraw 验证草稿和退回申请撤回后保留记录并释放活动名额。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestFilingApplicationWithdraw(t *testing.T) {
	_, handler := fixtureService(t)
	f := approvedFiling(t, handler, "BA-WITHDRAW-APPLICATION")
	form := map[string]string{"filingId": f.ID, "type": "cancel", "reason": "待核对注销条件"}
	app := readResponse[mutation](t, requestJSON(handler, "POST", applicationURL, form), 201)
	draftPath := applicationURL + "/" + app.ID
	detail := readResponse[applicationDetail](t, requestJSON(handler, "GET", draftPath, nil), 200)
	if !slices.Contains(detail.AllowedActions, "withdraw") {
		t.Fatalf("draft does not support withdrawal: %+v", detail)
	}
	assertError(t, reviewRequest(handler, draftPath, "withdraw", "", app.Version+1), 409, "conflict")
	assertWithdrawn(t, handler, draftPath, draft, app.Version)

	again := readResponse[mutation](t, requestJSON(handler, "POST", applicationURL, form), 201)
	path := applicationURL + "/" + again.ID
	readResponse[mutation](t, requestJSON(handler, "POST", path+"/submit", versionInput{1}), 200)
	assertError(t, reviewRequest(handler, path, "withdraw", "", 2), 409, "conflict")
	readResponse[mutation](t, reviewRequest(handler, path, "return_for_correction", "更正类型", 2), 200)
	assertWithdrawn(t, handler, path, returned, 3)
	form["type"] = "change"
	request := map[string]any{"filingId": f.ID, "type": form["type"], "reason": "修正申请类型",
		"changes": map[string]string{"systemName": "新系统"}}
	readResponse[mutation](t, requestJSON(handler, "POST", applicationURL, request), 201)
	page := readResponse[applicationPage](t,
		requestJSON(handler, "GET", applicationURL+"?status=withdrawn&filingId="+f.ID, nil), 200)
	if len(page.Items) != 2 || page.Items[0].ID != again.ID || page.Items[1].ID != app.ID {
		t.Fatalf("withdrawn history is missing from list: %+v", page)
	}
	filings := readResponse[listResponse](t,
		requestJSON(handler, "GET", filingsURL+"?status=withdrawn", nil), 200)
	if len(filings.Items) != 0 {
		t.Fatalf("撤回变更或注销申请不应把主备案归入已撤回: %+v", filings)
	}
}

// assertWithdrawn 核对撤回终态、原申请保留和新审核历史。
// 参数 t 为测试上下文，handler 为业务路由，path 为申请地址，from、version 为撤回前的状态和版本。
// 返回值 无。
func assertWithdrawn(t *testing.T, handler http.Handler, path, from string, version int64) {
	t.Helper()
	result := readResponse[mutation](t, reviewRequest(handler, path, "withdraw", "调整申请类型", version), 200)
	if result.Status != "withdrawn" || result.Version != version+1 {
		t.Fatalf("unexpected withdrawal result: %+v", result)
	}
	detail := readResponse[applicationDetail](t, requestJSON(handler, "GET", path, nil), 200)
	if detail.AppliedAt != nil || detail.Reason == "" || len(detail.AllowedActions) != 0 {
		t.Fatalf("unexpected withdrawn detail: %+v", detail)
	}
	history := readResponse[itemResponse[reviewEvent]](
		t, requestJSON(handler, "GET", path+"/review-history", nil), 200)
	if len(history.Items) == 0 {
		t.Fatal("missing withdrawal history")
	}
	last := history.Items[0]
	if last.Action != "withdraw" || last.FromStatus != from || last.ToStatus != "withdrawn" ||
		last.Comment != "调整申请类型" || last.Operator != "测试监管席位" {
		t.Fatalf("unexpected withdrawal event: %+v", last)
	}
}
