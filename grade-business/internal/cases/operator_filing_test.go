package cases

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
)

const operatorFilingsURL = "/api/v1/operator/filings"

// TestOperatorFilingGetsNumberAtFinalApproval 验证首报编号由管理端终审分配。
// 参数 t 为隔离 PostgreSQL 和真实 HTTP 路由的测试上下文。
// 返回值 无。
func TestOperatorFilingGetsNumberAtFinalApproval(t *testing.T) {
	_, handler := operatorFilingFixture(t)
	owner := operatorFilingQuery("本地备案单位")
	form := map[string]string{
		"systemName": "无编号首报", "assessmentOrg": "隔离测评机构", "level": "2",
	}
	item := readResponse[mutation](t, requestJSON(handler, "POST", operatorFilingsURL+owner, form), 201)
	form["number"] = "BA-UNAUTHORIZED"
	other := readResponse[mutation](t, requestJSON(handler, "POST", operatorFilingsURL+owner, form), 201)
	local := operatorFilingsURL + "/" + item.ID
	admin := filingsURL + "/" + item.ID
	for _, id := range []string{item.ID, other.ID} {
		detail := readResponse[recordDetail](t, requestJSON(handler, "GET", operatorFilingsURL+"/"+id+owner, nil), 200)
		if detail.Number != "" {
			t.Fatalf("运营者新建时已有正式编号: %q", detail.Number)
		}
	}
	sent := submitOperatorFiling(t, handler, local, owner, item.Version)
	partner := institutionBase + "/filings/" + item.ID
	initial := readResponse[mutation](t, scopedReview(handler, partner, firstSubmissionScope("隔离测评机构"),
		approve, sent.Version), 200)
	assertError(t, reviewRequest(handler, admin, approve, "", initial.Version), 400, "filing_number_required")
	createFiling(t, handler, "BA-ASSIGNED-001")
	decision := map[string]any{"decision": approve, "expectedVersion": initial.Version, "filingNumber": "ba-assigned-001"}
	assertError(t, requestJSON(handler, "POST", admin+"/review-actions", decision), 409, "duplicate_number")
	decision["filingNumber"] = "BA-ASSIGNED-002"
	readResponse[mutation](t, requestJSON(handler, "POST", admin+"/review-actions", decision), 200)
	final := readResponse[recordDetail](t, requestJSON(handler, "GET", local+owner, nil), 200)
	if final.Number != "BA-ASSIGNED-002" || final.Status != approved {
		t.Fatalf("管理端分配的编号未与终审状态同步: %+v", final)
	}
}

// TestOperatorFilingTracksSharedReview 核对自主备案在三端共享提交、退回及通过进度。
// 参数 t 为隔离测试库与临时材料目录的测试上下文。
// 返回值 无。
func TestOperatorFilingTracksSharedReview(t *testing.T) {
	s, handler := operatorFilingFixture(t)
	own := operatorFilingQuery("本地备案单位")
	item := createOperatorFiling(t, handler, own)
	local := operatorFilingsURL + "/" + item.ID
	admin := filingsURL + "/" + item.ID
	partner := institutionBase + "/filings/" + item.ID
	partnerScope := firstSubmissionScope("隔离测评机构")
	receipt := local + "/receipt" + own
	detail := readResponse[recordDetail](t, requestJSON(handler, "GET", local+own, nil), 200)
	if detail.OrganizationName != "本地备案单位" || detail.ReceiptAvailable == nil || *detail.ReceiptAvailable ||
		!slices.Equal(detail.RequiredMaterials, []string{"filing_form", "classification_report"}) {
		t.Fatalf("备案归属或材料与回执状态错误: %+v", detail)
	}
	assertError(t, requestJSON(handler, "GET", receipt, nil), 409, "receipt_unavailable")
	sent := submitOperatorFiling(t, handler, local, own, item.Version)
	progress := readResponse[recordDetail](t, requestJSON(handler, "GET", local+own, nil), 200)
	if progress.SubmittedAt == nil || progress.ReceiptAvailable == nil || !*progress.ReceiptAvailable {
		t.Fatalf("待审详情缺少真实提交时间和回执入口: %+v", progress)
	}
	assertOperatorFilingReceipt(t, handler, receipt, "提交回执", "待分配")
	back := readResponse[mutation](t, requestJSON(handler, "POST", partner+"/review-actions"+partnerScope,
		reviewInput{versionInput: versionInput{sent.Version}, Decision: returnForFix, Comment: "请复核单位材料"}), 200)
	assertOperatorReviewNotice(t, s, item.ID, returned)
	assertOperatorFilingScopeStatus(t, handler, item.ID, own, returned)
	assertError(t, requestJSON(handler, "PATCH", local+own, map[string]any{
		"systemName": "自主备案系统", "level": "2",
		"assessmentOrg": "补正阶段不得自行改派", "expectedVersion": back.Version,
	}), 409, "conflict")
	sent = readResponse[mutation](t,
		requestJSON(handler, "POST", local+"/submit"+own, versionInput{back.Version}), 200)
	assertAssignedNotice(t, s, "filings", item.ID, sent.Version, pendingInitial, "institution", "隔离测评机构")
	initial := readResponse[mutation](t, scopedReview(handler, partner, partnerScope, approve, sent.Version), 200)
	assertOperatorFilingScopeStatus(t, handler, item.ID, own, pendingFinal)
	readResponse[mutation](t, requestJSON(handler, "POST", admin+"/review-actions", reviewInput{
		versionInput: versionInput{initial.Version}, Decision: approve, FilingNumber: "BA-SELF",
	}), 200)
	assertOperatorReviewNotice(t, s, item.ID, approved)
	assertOperatorFilingScopeStatus(t, handler, item.ID, own, approved)
	assertOperatorFilingReceipt(t, handler, receipt, "审核通过回执", "BA-SELF")
	events := readResponse[itemResponse[reviewEvent]](
		t, requestJSON(handler, "GET", local+"/review-history"+own, nil), 200).Items
	if len(events) != 5 || events[3].Comment != "请复核单位材料" ||
		events[4].Operator != "本地运营者：本地备案单位" {
		t.Fatalf("备案进度未反映落库的提交和退回事件: %+v", events)
	}
}

// TestOperatorFilingWriteAndReadScope 核对组织隔离、必传材料和并发版本防护。
// 参数 t 为隔离 PostgreSQL 与临时材料目录的测试上下文。
// 返回值 无。
func TestOperatorFilingWriteAndReadScope(t *testing.T) {
	_, handler := operatorFilingFixture(t)
	own, other := operatorFilingQuery("本地备案单位"), operatorFilingQuery("另一个单位")
	item := createOperatorFiling(t, handler, own)
	local := operatorFilingsURL + "/" + item.ID
	assertError(t, requestJSON(handler, "GET", operatorFilingsURL, nil), 400, "invalid_input")
	assertError(t, requestJSON(handler, "GET", operatorFilingsURL+own+"&organization=x", nil), 400, "invalid_input")
	assertError(t, requestJSON(handler, "POST", operatorFilingsURL+own, map[string]string{
		"number": "BA-FORGED", "systemName": "系统", "organizationName": "另一个单位",
		"assessmentOrg": "隔离候选机构", "level": "2",
	}), 400, "invalid_input")
	result := readResponse[listResponse](t, requestJSON(handler, "GET", operatorFilingsURL+other, nil), 200)
	if len(result.Items) != 0 {
		t.Fatalf("另一个运营单位看到备案: %+v", result)
	}
	search := "&q=" + url.QueryEscape("自主备案系统") + "&status=draft&limit=1"
	result = readResponse[listResponse](t,
		requestJSON(handler, "GET", operatorFilingsURL+own+search, nil), 200)
	if len(result.Items) != 1 || result.Items[0].ID != item.ID {
		t.Fatalf("本单位未获得筛选后的备案: %+v", result)
	}
	for _, resource := range []string{"", "/review-history", "/attachments", "/receipt"} {
		assertError(t, requestJSON(handler, "GET", local+resource+other, nil), 404, "not_found")
	}
	change := map[string]any{"number": "BA-SELF", "systemName": "修改系统",
		"assessmentOrg": "隔离候选机构", "level": "3", "expectedVersion": item.Version}
	assertError(t, requestJSON(handler, "PATCH", local+other, change), 404, "not_found")
	updated := readResponse[mutation](t, requestJSON(handler, "PATCH", local+own, change), 200)
	detail := readResponse[recordDetail](t, requestJSON(handler, "GET", local+own, nil), 200)
	if detail.Number != "" {
		t.Fatalf("运营者编辑时写入了正式编号: %q", detail.Number)
	}
	assertError(t, requestJSON(handler, "PATCH", local+own, change), 409, "conflict")
	assertError(t, requestJSON(handler, "POST", local+"/submit"+other, versionInput{updated.Version}), 404, "not_found")
	assertError(t, requestJSON(handler, "POST", local+"/attachments"+other, nil), 404, "not_found")
	postMaterialAt(t, handler, local+"/attachments"+own, "classification_report")
	assertError(t, requestJSON(handler, "POST", local+"/submit"+own, versionInput{updated.Version + 1}),
		400, "materials_required")
	file := postMaterialAt(t, handler, local+"/attachments"+own, "filing_form")
	checkDownloadedMaterial(t, handler, local+"/attachments/"+file.ID+own)
	assertError(t, requestJSON(handler, "GET", local+"/attachments/"+file.ID+other, nil), 404, "not_found")
	assertOneOperatorFilingSubmission(t, handler, [2]string{
		local + "/submit" + own, filingsURL + "/" + item.ID + "/submit",
	}, updated.Version+2)
	change["expectedVersion"] = updated.Version + 3
	assertError(t, requestJSON(handler, "PATCH", local+own, change), 409, "conflict")
}

// TestOperatorFilingInstitutionIsReadOnly 验证测评机构不能改派或代提运营者自主备案。
// 参数 t 为隔离 PostgreSQL 与本地材料测试上下文。
// 返回值 无。
func TestOperatorFilingInstitutionIsReadOnly(t *testing.T) {
	_, handler := operatorFilingFixture(t)
	own := operatorFilingQuery("本地备案单位")
	draft := createOperatorFiling(t, handler, own)
	partner := "/api/v1/institution/filings/" + draft.ID
	scope := "?" + url.Values{"institution": {"隔离测评机构"}}.Encode()
	item := readResponse[recordDetail](t, requestJSON(handler, "GET", partner+scope, nil), 200)
	if len(item.AllowedActions) != 0 {
		t.Fatalf("机构获得自主备案编辑权限: %v", item.AllowedActions)
	}
	update := map[string]any{
		"number": draft.ID, "systemName": "试图改派", "organizationName": "其他单位",
		"assessmentOrg": item.AssessmentOrg, "level": "3", "expectedVersion": item.Version,
	}
	assertError(t, requestJSON(handler, "PATCH", partner+scope, update), 409, "conflict")
	assertError(t, requestJSON(handler, "POST", partner+"/submit"+scope, versionInput{item.Version}), 409, "conflict")
	assertError(t, requestJSON(handler, "POST", partner+"/attachments"+scope, nil), 409, "conflict")
	local := operatorFilingsURL + "/" + draft.ID
	file := postMaterialAt(t, handler, local+"/attachments"+own, "filing_form")
	checkDownloadedMaterial(t, handler, partner+"/attachments/"+file.ID+scope)
}

// assertOneOperatorFilingSubmission 核对运营者和监管同时提交同一版本时只有一份进入初审。
// 参数 t 与 handler 为隔离环境，paths 为两端提交地址，version 为双方共同的已读版本。
// 返回值 无。
func assertOneOperatorFilingSubmission(t *testing.T, handler http.Handler, paths [2]string, version int64) {
	t.Helper()
	var requests sync.WaitGroup
	statuses := make(chan int, 2)
	for _, path := range paths {
		requests.Go(func() {
			statuses <- requestJSON(handler, "POST", path, versionInput{version}).Code
		})
	}
	requests.Wait()
	first, second := <-statuses, <-statuses
	if !((first == http.StatusOK && second == http.StatusConflict) ||
		(first == http.StatusConflict && second == http.StatusOK)) {
		t.Fatalf("重复提交产生意外响应: %d, %d", first, second)
	}
}

// operatorFilingFixture 在独立测试环境挂载共享备案与运营者路由。
// 参数 t 为测试上下文。
// 返回值 备案服务及 HTTP 路由。
func operatorFilingFixture(t *testing.T) (*Service, http.Handler) {
	t.Helper()
	s, err := New(fixtureDB(t), t.TempDir(), "隔离监管席位")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	s.RegisterOperatorFilingRoutes(mux)
	return s, mux
}

// operatorFilingQuery 将运营单位编码进入本地 API 的查询参数。
// 参数 organization 为所选测试运营单位。
// 返回值 带问号的 URL 范围参数。
func operatorFilingQuery(organization string) string {
	return "?" + url.Values{"organization": {organization}}.Encode()
}

// createOperatorFiling 建立归属到指定运营者的真实备案草稿。
// 参数 t 和 handler 为测试环境，scope 为所选运营单位的查询参数。
// 返回值 已保存草稿的标识、版本和状态。
func createOperatorFiling(t *testing.T, handler http.Handler, scope string) mutation {
	t.Helper()
	res := requestJSON(handler, "POST", operatorFilingsURL+scope, map[string]string{
		"systemName": "自主备案系统", "assessmentOrg": "隔离测评机构", "level": "2",
	})
	return readResponse[mutation](t, res, 201)
}

// submitOperatorFiling 上传两项有效材料并提交自主备案。
// 参数 t 和 handler 为测试环境，local、own 为范围地址，version 为原始版本。
// 返回值 提交成功后的最小变更结果。
func submitOperatorFiling(t *testing.T, handler http.Handler, local, own string, version int64) mutation {
	t.Helper()
	postMaterialAt(t, handler, local+"/attachments"+own, "filing_form")
	postMaterialAt(t, handler, local+"/attachments"+own, "classification_report")
	return readResponse[mutation](t, requestJSON(handler, "POST", local+"/submit"+own, versionInput{version + 2}), 200)
}

// assertOperatorFilingScopeStatus 读取三端同一份备案状态。
// 参数 t 和 handler 为测试环境，id 为备案，scope 为运营单位，status 为预期状态。
// 返回值 无。
func assertOperatorFilingScopeStatus(t *testing.T, handler http.Handler, id, scope, status string) {
	t.Helper()
	for _, path := range []string{
		filingsURL + "/" + id,
		institutionBase + "/filings/" + id + firstSubmissionScope("隔离测评机构"),
		operatorFilingsURL + "/" + id + scope,
	} {
		item := readResponse[recordDetail](t, requestJSON(handler, "GET", path, nil), 200)
		if item.Status != status {
			t.Fatalf("备案状态未同步 %s: %+v", path, item)
		}
	}
}

// assertOperatorFilingReceipt 核对回执类型及附件仅包含必要业务字段。
// 参数 t 和 handler 为测试环境，path 为回执地址，title 和 number 为应展示的类型和编号。
// 返回值 无。
func assertOperatorFilingReceipt(t *testing.T, handler http.Handler, path, title, number string) {
	t.Helper()
	res := requestJSON(handler, "GET", path, nil)
	if res.Code != 200 || res.Header().Get("Content-Type") != "text/plain; charset=utf-8" ||
		res.Header().Get("Content-Disposition") != "attachment; filename=filing-receipt.txt" ||
		res.Header().Get("Cache-Control") != "no-store" || !strings.Contains(res.Body.String(), title) ||
		!strings.Contains(res.Body.String(), "当前备案编号："+number+"\n") {
		t.Fatalf("实际回执不符: %d %s", res.Code, res.Body.String())
	}
}

// assertOperatorReviewNotice 确认运营者与测评机构收到同一状态变更的各自通知。
// 参数 t 和 s 为隔离测试环境，id 为备案，status 为真实审核状态。
// 返回值 无。
func assertOperatorReviewNotice(t *testing.T, s *Service, id, status string) {
	t.Helper()
	var count int
	err := s.db.QueryRow(t.Context(), `SELECT count(*) FROM review_notices
		WHERE resource='filings' AND resource_id=$1 AND state=$2
		  AND ((audience='operator' AND recipient='本地备案单位')
		    OR (audience='institution' AND recipient='隔离测评机构'))`, id, status).Scan(&count)
	if err != nil || count != 2 {
		t.Fatalf("两端未收到同一状态通知: %d %v", count, err)
	}
}
