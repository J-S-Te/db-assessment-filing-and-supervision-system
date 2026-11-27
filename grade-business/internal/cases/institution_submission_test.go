package cases

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"testing"
)

const institutionBase = "/api/v1/institution"

// firstSubmissionScope 构造隔离测试中明确的机构查询范围。
// 参数 institution 为测试数据所登记的机构。
// 返回值 已编码的查询后缀。
func firstSubmissionScope(institution string) string {
	return "?institution=" + url.QueryEscape(institution)
}

// TestInstitutionCanFileFirstSubmission 核对机构按自己的范围创建、编辑草稿、传材料并送审。
// 参数 t 为只读写隔离 PostgreSQL 和临时材料目录的测试上下文。
// 返回值 无。
func TestInstitutionCanFileFirstSubmission(t *testing.T) {
	_, handler := fixtureService(t)
	base := institutionBase + "/filings"
	own, other := firstSubmissionScope("隔离提交机构"), firstSubmissionScope("其他机构")
	body := map[string]any{
		"number": "BA-FIRST", "systemName": "隔离系统", "organizationName": "隔离单位", "level": "2",
	}
	created := readResponse[mutation](t, requestJSON(handler, "POST", base+own, body), 201)
	path := base + "/" + created.ID
	body["assessmentOrg"] = "其他机构"
	assertError(t, requestJSON(handler, "POST", base+own, body), 400, "invalid_input")
	delete(body, "assessmentOrg")
	body["expectedVersion"] = created.Version
	body["systemName"] = "更新后的隔离系统"
	readResponse[mutation](t, requestJSON(handler, "PATCH", path+own, body), 200)
	assertScopedFilingList(t, handler, base, own, other, created.ID)
	assertError(t, requestJSON(handler, "GET", path+other, nil), 404, "not_found")
	assertError(t, requestJSON(handler, "POST", path+"/submit"+own, versionInput{2}), 400, "materials_required")
	postMaterialAt(t, handler, path+"/attachments"+own, "filing_form")
	postMaterialAt(t, handler, path+"/attachments"+own, "classification_report")
	sent := readResponse[mutation](t, requestJSON(handler, "POST", path+"/submit"+own, versionInput{4}), 200)
	if sent.Status != pendingInitial {
		t.Fatalf("首次送审未进入初审: %+v", sent)
	}
	assertError(t, scopedReview(handler, path, own, approve, sent.Version), 409, "conflict")
	assigned := readResponse[listResponse](t, requestJSON(handler, "GET", base+own+"&review=assigned", nil), 200)
	if len(assigned.Items) != 0 {
		t.Fatalf("机构自报进入了自己的初审清单: %+v", assigned)
	}
	admin := filingsURL + "/" + created.ID
	initial := readResponse[mutation](t, reviewRequest(handler, admin, approve, "", sent.Version), 200)
	readResponse[mutation](t, reviewRequest(handler, admin, approve, "", initial.Version), 200)
}

// assertScopedFilingList 核对列表及详情使用 URL 指定的机构名，并支持草稿办理。
// 参数 t 为测试上下文，handler 为实际 API，base、own、other、id 为业务地址和归属。
// 返回值 无。
func assertScopedFilingList(t *testing.T, handler http.Handler, base, own, other, id string) {
	t.Helper()
	detail := readResponse[recordDetail](t, requestJSON(handler, "GET", base+"/"+id+own, nil), 200)
	if detail.AssessmentOrg != "隔离提交机构" || detail.SystemName != "更新后的隔离系统" ||
		!slices.Equal(detail.AllowedActions, []string{"edit", "submit"}) {
		t.Fatalf("当前机构草稿详情不符: %+v", detail)
	}
	results := readResponse[listResponse](t, requestJSON(handler, "GET", base+own+"&status=draft&q=更新后", nil), 200)
	foreign := readResponse[listResponse](t, requestJSON(handler, "GET", base+other, nil), 200)
	if len(results.Items) != 1 || results.Items[0].ID != id || len(foreign.Items) != 0 {
		t.Fatalf("机构列表跨越了归属: %+v %+v", results, foreign)
	}
}

// TestInstitutionFirstApplicationAndWithdrawal 核对本机构有效备案的变更注销创建、编辑与撤回规则。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestInstitutionFirstApplicationAndWithdrawal(t *testing.T) {
	_, handler := fixtureService(t)
	source := approvedFiling(t, handler, "BA-FIRST-APP")
	own, other := firstSubmissionScope("测评中心"), firstSubmissionScope("另一个机构")
	base := institutionBase + "/filing-applications"
	input := map[string]any{"filingId": source.ID, "type": "change", "reason": "首次提交申请",
		"changes": map[string]string{"systemName": "隔离变更系统"}}
	assertError(t, requestJSON(handler, "POST", base+other, input), 404, "not_found")
	app := readResponse[mutation](t, requestJSON(handler, "POST", base+own, input), 201)
	path := base + "/" + app.ID
	edited := readResponse[mutation](t, requestJSON(handler, "PATCH", path+own, map[string]any{
		"reason": "更新变更原因", "changes": input["changes"], "expectedVersion": app.Version,
	}), 200)
	assertError(t, requestJSON(handler, "POST", path+"/withdraw"+other, versionInput{edited.Version}), 404, "not_found")
	withdrawn := readResponse[mutation](t, requestJSON(handler, "POST", path+"/withdraw"+own, versionInput{edited.Version}), 200)
	if withdrawn.Status != applicationWithdrawn {
		t.Fatalf("机构草稿撤回未落库: %+v", withdrawn)
	}
	cancel := readResponse[mutation](t, requestJSON(handler, "POST", base+own,
		map[string]any{"filingId": source.ID, "type": "cancel", "reason": "隔离注销申请"}), 201)
	sent := readResponse[mutation](t,
		requestJSON(handler, "POST", base+"/"+cancel.ID+"/submit"+own, versionInput{cancel.Version}), 200)
	assertError(t, requestJSON(handler, "POST", base+"/"+cancel.ID+"/withdraw"+own, versionInput{sent.Version}), 409, "conflict")
	items := readResponse[applicationPage](t, requestJSON(handler, "GET", base+own+"&type=cancel&filingId="+source.ID, nil), 200)
	if len(items.Items) != 1 || items.Items[0].ID != cancel.ID || items.Items[0].Status != pendingInitial {
		t.Fatalf("申请列表未返回首次提交的真实记录: %+v", items)
	}
	assertError(t, scopedReview(handler, base+"/"+cancel.ID, own, approve, sent.Version), 409, "conflict")
	assigned := readResponse[applicationPage](t, requestJSON(handler, "GET", base+own+"&review=assigned", nil), 200)
	if len(assigned.Items) != 0 {
		t.Fatalf("机构自办申请进入了自己的初审清单: %+v", assigned)
	}
	admin := applicationURL + "/" + cancel.ID
	initial := readResponse[mutation](t, reviewRequest(handler, admin, approve, "", sent.Version), 200)
	readResponse[mutation](t, reviewRequest(handler, admin, approve, "", initial.Version), 200)
}

// TestInstitutionReportMaterialProfileChecksEverySubmit 验证九类模式要求的数据由报告固化，监管提交同样校验。
// 参数 t 为使用隔离 PostgreSQL 及临时材料目录的测试上下文。
// 返回值 无。
func TestInstitutionReportMaterialProfileChecksEverySubmit(t *testing.T) {
	_, handler := fixtureService(t)
	active := approvedFiling(t, handler, "BA-REPORT-FIRST")
	unapproved := createFiling(t, handler, "BA-REPORT-DRAFT")
	own, other := firstSubmissionScope("测评中心"), firstSubmissionScope("另一个机构")
	base := institutionBase + "/reports"
	body := map[string]any{"number": "BG-FIRST", "risk": "medium", "filingId": unapproved.ID}
	assertError(t, requestJSON(handler, "POST", base+own, body), 409, "filing_inactive")
	body["filingId"] = active.ID
	assertError(t, requestJSON(handler, "POST", base+other, body), 404, "not_found")
	created := readResponse[mutation](t, requestJSON(handler, "POST", base+own, body), 201)
	scoped := base + "/" + created.ID
	admin := reportsURL + "/" + created.ID
	body["expectedVersion"] = created.Version
	body["risk"] = "high"
	edited := readResponse[mutation](t, requestJSON(handler, "PATCH", scoped+own, body), 200)
	assertError(t, requestJSON(handler, "GET", scoped+other, nil), 404, "not_found")
	checkInstitutionReportList(t, handler, base, own, other, created.ID)
	primary := postMaterialAt(t, handler, scoped+"/attachments"+own, "report_file")
	assertError(t, requestJSON(handler, "GET", scoped+"/attachments/"+primary.ID+other, nil), 404, "not_found")
	assertError(t, requestJSON(handler, "POST", admin+"/submit", versionInput{edited.Version + 1}), 400, "materials_required")
	assertReportMaterialCompletion(t, handler, scoped, admin, own, active.ID)
}

// TestInstitutionCannotSubmitForeignLegacyReportDraft 核对历史监管草稿由机构首次送审也需拥有主备案。
// 参数 t 为只读写独立 PostgreSQL schema 与临时材料目录的测试上下文。
// 返回值 无。
func TestInstitutionCannotSubmitForeignLegacyReportDraft(t *testing.T) {
	_, handler := fixtureService(t)
	filing := approvedFiling(t, handler, "BA-LEGACY-SCOPE")
	report := readResponse[mutation](t, requestJSON(handler, "POST", reportsURL, reportInput{
		Number: "BG-LEGACY-SCOPE", FilingID: filing.ID, AssessmentOrg: "旧报告机构", Risk: "medium",
	}), 201)
	admin := reportsURL + "/" + report.ID
	owner := firstSubmissionScope("旧报告机构")
	scoped := institutionBase + "/reports/" + report.ID
	postMaterial(t, handler, admin, "report_file")
	assertError(t, requestJSON(handler, "POST", scoped+"/submit"+owner, versionInput{2}), 404, "not_found")
	sent := readResponse[mutation](t, requestJSON(handler, "POST", admin+"/submit", versionInput{2}), 200)
	if sent.Status != pendingInitial {
		t.Fatalf("监管旧草稿的原有单材料规则未生效: %+v", sent)
	}
}

// checkInstitutionReportList 核对机构范围与风险筛选共同限制测评列表。
// 参数 t 为测试上下文，handler 为实际 API，base、own、other、id 指定报告查询与归属。
// 返回值 无。
func checkInstitutionReportList(t *testing.T, handler http.Handler, base, own, other, id string) {
	t.Helper()
	owned := readResponse[listResponse](t, requestJSON(handler, "GET", base+own+"&risk=high", nil), 200)
	foreign := readResponse[listResponse](t, requestJSON(handler, "GET", base+other, nil), 200)
	if len(owned.Items) != 1 || owned.Items[0].ID != id || len(foreign.Items) != 0 {
		t.Fatalf("报告范围或风险筛选未生效: %+v %+v", owned, foreign)
	}
}

// assertReportMaterialCompletion 验证必传清单、附件校验、首次提交及后续补正重提。
// 参数 t 为测试上下文，handler 为实际 API，scoped、admin、own、filingID 确定路径与归属。
// 返回值 无。
func assertReportMaterialCompletion(t *testing.T, handler http.Handler, scoped, admin, own, filingID string) {
	t.Helper()
	required := []string{"report_file", "remediation_plan", "assessment_conclusion",
		"issues_recommendations", "cover_basic_statement"}
	initial := readResponse[map[string]json.RawMessage](t, requestJSON(handler, "GET", scoped+own, nil), 200)
	var kinds []string
	if err := json.Unmarshal(initial["requiredMaterials"], &kinds); err != nil || !slices.Equal(kinds, required) ||
		string(initial["filingId"]) != "\""+filingID+"\"" || string(initial["canCorrect"]) != "true" {
		t.Fatalf("机构报告详情未返回绑定的材料校验配置: %+v", initial)
	}
	for _, kind := range required[1:] {
		postMaterialAt(t, handler, scoped+"/attachments"+own, kind)
	}
	updated := readResponse[recordDetail](t, requestJSON(handler, "GET", admin, nil), 200)
	sent := readResponse[mutation](t, requestJSON(handler, "POST", admin+"/submit", versionInput{updated.Version}), 200)
	back := readResponse[mutation](t, reviewRequest(handler, admin, returnForFix, "隔离补正说明", sent.Version), 200)
	optional := []string{"cloud_extension", "big_data_extension", "major_risk_recommendations", "supporting"}
	for _, kind := range optional {
		postMaterialAt(t, handler, scoped+"/attachments"+own, kind)
	}
	files := readResponse[itemResponse[Attachment]](t, requestJSON(handler, "GET", scoped+"/attachments"+own, nil), 200)
	if len(files.Items) != 9 {
		t.Fatalf("九类材料未全部落库: %+v", files)
	}
	version := versionInput{back.Version + int64(len(optional))}
	retry := readResponse[mutation](t, requestJSON(handler, "POST", scoped+"/submit"+own, version), 200)
	if retry.Status != pendingInitial {
		t.Fatalf("机构补正后未重新送初审: %+v", retry)
	}
}
