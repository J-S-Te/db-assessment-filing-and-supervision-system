package cases

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
)

// TestInstitutionInitialReviewOfOperatorFiling 验证机构初审分权、材料复核和并发留痕。
// 参数 t 为隔离数据库和文件目录的测试上下文。
// 返回值 无。
func TestInstitutionInitialReviewOfOperatorFiling(t *testing.T) {
	service, handler := operatorFilingFixture(t)
	owner := operatorFilingQuery("本地备案单位")
	created := createOperatorFiling(t, handler, owner)
	local := operatorFilingsURL + "/" + created.ID
	admin := filingsURL + "/" + created.ID
	collection := institutionBase + "/filings"
	institution := collection + "/" + created.ID
	scope, foreign := firstSubmissionScope("隔离测评机构"), firstSubmissionScope("其他机构")
	sent := submitOperatorFiling(t, handler, local, owner, created.Version)
	assertError(t, reviewRequest(handler, admin, approve, "", sent.Version), 409, "conflict")
	assertError(t, scopedReview(handler, institution, foreign, approve, sent.Version), 404, "not_found")
	assertError(t, requestJSON(handler, "GET", institution+"/review-history"+foreign, nil), 404, "not_found")
	assertInstitutionReviewDetail(t, handler, admin, institution+scope, local+owner)
	page := readResponse[listResponse](t, requestJSON(handler, "GET", collection+scope+"&review=assigned", nil), 200)
	if len(page.Items) != 1 || page.Items[0].ID != created.ID {
		t.Fatalf("待初审列表未匹配已提交备案: %+v", page)
	}
	listed := readResponse[struct{ Items []map[string]any }](t,
		requestJSON(handler, "GET", filingsURL+"?status=pending_initial", nil), 200)
	if len(listed.Items) != 1 || listed.Items[0]["initialReviewAudience"] != "institution" {
		t.Fatalf("监管列表未展示当前初审归属: %+v", listed)
	}
	assertAssignedNotice(t, service, "filings", created.ID, sent.Version, pendingInitial, "institution", "隔离测评机构")
	checkInitialApprovalMaterials(t, service, handler, created.ID, institution, scope, sent.Version)

	var reviews sync.WaitGroup
	results := make(chan int, 3)
	for range 2 {
		reviews.Go(func() { results <- scopedReview(handler, institution, scope, approve, sent.Version).Code })
	}
	reviews.Go(func() { results <- reviewRequest(handler, admin, approve, "", sent.Version).Code })
	reviews.Wait()
	close(results)
	var statuses []int
	for status := range results {
		statuses = append(statuses, status)
	}
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{200, 409, 409}) {
		t.Fatalf("并发初审应只推进一次: %v", statuses)
	}
	assertError(t, scopedReview(handler, institution, scope, approve, sent.Version+1), 409, "conflict")
	assertAssignedNotice(t, service, "filings", created.ID, sent.Version+1, pendingFinal, "operator", "本地备案单位")
	readResponse[mutation](t, reviewRequest(handler, admin, approve, "", sent.Version+1), 200)
	assertInstitutionApprovalAudit(t, handler, institution+"/review-history"+scope, "隔离测评机构")
	page = readResponse[listResponse](t, requestJSON(handler, "GET", collection+scope+"&review=assigned", nil), 200)
	if len(page.Items) != 0 {
		t.Fatalf("已办理记录仍在机构初审列表: %+v", page)
	}
}

// TestInstitutionInitialReviewOfOperatorApplication 验证变更申请限原机构初审并维持复测绑定。
// 参数 t 为隔离业务测试上下文。
// 返回值 无。
func TestInstitutionInitialReviewOfOperatorApplication(t *testing.T) {
	service, handler := fixtureService(t)
	service.RegisterOperatorApplicationRoutes(handler.(*http.ServeMux))
	filing := approvedFiling(t, handler, "BA-INSTITUTION-APP")
	owner := operatorFilingQuery("测试单位")
	scope, target := firstSubmissionScope("测评中心"), firstSubmissionScope("复测新机构")
	bound := insertApplicationImplementation(t, service, filing.ID, "复测新机构", "change")
	created := readResponse[mutation](t, requestJSON(handler, "POST", operatorApplicationURL+owner,
		assessmentTransferForm(filing.ID, "复测新机构")), 201)
	local := operatorApplicationURL + "/" + created.ID
	admin := applicationURL + "/" + created.ID
	collection := institutionBase + "/filing-applications"
	institution := collection + "/" + created.ID
	assertError(t, requestJSON(handler, "POST", institution+"/submit"+scope,
		versionInput{created.Version}), 409, "conflict")
	file := readResponse[Attachment](t, uploadApplicationFile(t, handler, local, owner, changeApplicationStatement), 201)
	sent := readResponse[mutation](t, requestJSON(handler, "POST", local+"/submit"+owner, versionInput{created.Version + 1}), 200)
	assertError(t, reviewRequest(handler, admin, approve, "", sent.Version), 409, "conflict")
	assertError(t, scopedReview(handler, institution, target, approve, sent.Version), 404, "not_found")
	assertError(t, requestJSON(handler, "GET", institution+"/review-history"+target, nil), 404, "not_found")
	assertInstitutionReviewDetail(t, handler, admin, institution+scope, local+owner)
	page := readResponse[applicationPage](t,
		requestJSON(handler, "GET", collection+scope+"&review=assigned&type=change", nil), 200)
	if len(page.Items) != 1 || page.Items[0].ID != created.ID {
		t.Fatalf("机构待初审列表未匹配原机构变更申请: %+v", page)
	}
	listed := readResponse[struct{ Items []map[string]any }](t,
		requestJSON(handler, "GET", applicationURL+"?status=pending_initial", nil), 200)
	if len(listed.Items) != 1 || listed.Items[0]["initialReviewAudience"] != "institution" {
		t.Fatalf("监管申请列表未展示当前初审归属: %+v", listed)
	}
	assertAssignedNotice(t, service, "filing-applications", created.ID, sent.Version, pendingInitial, "institution", "测评中心")
	checkApplicationApprovalEvidence(t, service, handler, created.ID, file.ID, bound, institution, scope, sent.Version)
	initial := readResponse[mutation](t, scopedReview(handler, institution, scope, approve, sent.Version), 200)
	assertError(t, scopedReview(handler, institution, scope, approve, initial.Version), 409, "conflict")
	assertAssignedNotice(t, service, "filing-applications", created.ID, initial.Version, pendingFinal, "operator", "测试单位")
	before := readResponse[recordDetail](t, requestJSON(handler, "GET", filingsURL+"/"+filing.ID, nil), 200)
	if before.AssessmentOrg != "测评中心" {
		t.Fatalf("机构初审提前改变主备案: %+v", before)
	}
	readResponse[mutation](t, reviewRequest(handler, admin, approve, "", initial.Version), 200)
	assertInstitutionApprovalAudit(t, handler, institution+"/review-history"+scope, "测评中心")
	after := readResponse[recordDetail](t, requestJSON(handler, "GET", filingsURL+"/"+filing.ID, nil), 200)
	if after.AssessmentOrg != "复测新机构" {
		t.Fatalf("监管终审未生效: %+v", after)
	}
}

// TestExistingOperatorFilingPendingFinalCanFinish 验证已经完成旧监管初审的自主备案继续终审。
// 参数 t 为真实隔离 PostgreSQL 及临时文件目录的测试上下文。
// 返回值 无。
func TestExistingOperatorFilingPendingFinalCanFinish(t *testing.T) {
	service, handler := operatorFilingFixture(t)
	owner := operatorFilingQuery("本地备案单位")
	created := createOperatorFiling(t, handler, owner)
	local := operatorFilingsURL + "/" + created.ID
	sent := submitOperatorFiling(t, handler, local, owner, created.Version)
	_, err := service.db.Exec(t.Context(), `WITH reviewed AS (
		UPDATE business_cases SET status='pending_final',version=version+1 WHERE id=$1
		RETURNING id,version
	)
	INSERT INTO case_review_events(case_id,action,from_status,to_status,operator,comment,version)
	SELECT id,'approve','pending_initial','pending_final','旧监管席位','原初审留痕',version FROM reviewed`, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	admin := filingsURL + "/" + created.ID
	result := readResponse[mutation](t, reviewRequest(handler, admin, approve, "", sent.Version+1), 200)
	if result.Status != approved {
		t.Fatalf("存量终审没有继续完成: %+v", result)
	}
	events := readResponse[itemResponse[reviewEvent]](t, requestJSON(handler, "GET", admin+"/review-history", nil), 200).Items
	if len(events) != 3 || events[1].Operator != "旧监管席位" || events[1].Comment != "原初审留痕" {
		t.Fatalf("存量初审留痕被覆盖: %+v", events)
	}
}

// scopedReview 发送带机构范围的审核决定。
// 参数 handler 为测试 HTTP 路由，path、scope 指定业务记录，decision 和 version 指定审核决定及版本。
// 返回值 机构审核的 HTTP 响应。
func scopedReview(handler http.Handler, path, scope, decision string, version int64) *httptest.ResponseRecorder {
	return requestJSON(handler, "POST", path+"/review-actions"+scope,
		reviewInput{versionInput: versionInput{version}, Decision: decision, Comment: "核对申请材料"})
}

// assertInstitutionReviewDetail 核对同一运营者来源在三端有一致来源说明和独立动作权限。
// 参数 t 和 handler 为隔离测试上下文，admin、institution、operator 为详情地址。
// 返回值 无。
func assertInstitutionReviewDetail(t *testing.T, handler http.Handler, admin, institution, operator string) {
	t.Helper()
	for _, path := range []string{admin, institution, operator} {
		detail := readResponse[struct {
			InitialReviewAudience string   `json:"initialReviewAudience"`
			AllowedActions        []string `json:"allowedActions"`
		}](t, requestJSON(handler, "GET", path, nil), 200)
		var want []string
		if path == institution {
			want = []string{approve, returnForFix}
		}
		if detail.InitialReviewAudience != "institution" || !slices.Equal(detail.AllowedActions, want) {
			t.Fatalf("详情初审角色不符 %s: %+v", path, detail)
		}
	}
}

// assertAssignedNotice 验证实际发送的一条通知和所用审核版本及接收人。
// 参数 t、service 为隔离服务，resource、id、version、state 指定事件，audience、recipient 指定接收方。
// 返回值 无。
func assertAssignedNotice(t *testing.T, service *Service, resource, id string, version int64, state, audience, recipient string) {
	t.Helper()
	var count int
	err := service.db.QueryRow(t.Context(), `SELECT count(*) FROM review_notices
		WHERE resource=$1 AND resource_id=$2 AND event_version=$3 AND state=$4 AND audience=$5 AND recipient=$6`,
		resource, id, version, state, audience, recipient).Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("通知未准确送达 %s %s: count=%d error=%v", audience, state, count, err)
	}
}

// checkInitialApprovalMaterials 在提交后模拟必传文件失效并核对机构通过重新校验。
// 参数 t、service、handler 为隔离环境，id、institution、scope、version 为当前备案审核字段。
// 返回值 无，成功重置测试文件类别以便后续继续审批。
func checkInitialApprovalMaterials(t *testing.T, service *Service, handler http.Handler, id, institution, scope string, version int64) {
	t.Helper()
	if _, err := service.db.Exec(t.Context(), `UPDATE case_attachments SET kind='supporting'
		WHERE case_id=$1 AND kind='classification_report'`, id); err != nil {
		t.Fatal(err)
	}
	assertError(t, scopedReview(handler, institution, scope, approve, version), 400, "materials_required")
	if _, err := service.db.Exec(t.Context(), `UPDATE case_attachments SET kind='classification_report'
		WHERE case_id=$1 AND kind='supporting'`, id); err != nil {
		t.Fatal(err)
	}
}

// checkApplicationApprovalEvidence 验证待审申请的材料缺失与已绑定实施报备失效各自阻止通过。
// 参数 t、service、handler 为隔离环境，id、fileID、bound 为关联标识，institution、scope、version 为审核字段。
// 返回值 无，恢复两项审核依据以便继续审批。
func checkApplicationApprovalEvidence(t *testing.T, service *Service, handler http.Handler,
	id, fileID, bound, institution, scope string, version int64,
) {
	t.Helper()
	var fileKey, fileName string
	var size int64
	err := service.db.QueryRow(t.Context(), `DELETE FROM filing_application_attachments WHERE id=$1
		RETURNING file_key,file_name,size_bytes`, fileID).Scan(&fileKey, &fileName, &size)
	if err != nil {
		t.Fatal(err)
	}
	assertError(t, scopedReview(handler, institution, scope, approve, version), 400, "application_materials_required")
	if _, err := service.db.Exec(t.Context(), `INSERT INTO filing_application_attachments
		(id,application_id,kind,file_key,file_name,size_bytes) VALUES($1,$2,$3,$4,$5,$6)`,
		fileID, id, changeApplicationStatement, fileKey, fileName, size); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(t.Context(), `UPDATE implementation_reports SET status='returned' WHERE id=$1`, bound); err != nil {
		t.Fatal(err)
	}
	assertError(t, scopedReview(handler, institution, scope, approve, version), 409, "retest_required")
	if _, err := service.db.Exec(t.Context(), `UPDATE implementation_reports SET status='approved' WHERE id=$1`, bound); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(t.Context(), `UPDATE filing_applications SET implementation_id=NULL WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	assertError(t, scopedReview(handler, institution, scope, approve, version), 409, "retest_required")
	if _, err := service.db.Exec(t.Context(), `UPDATE filing_applications SET implementation_id=$2 WHERE id=$1`, id, bound); err != nil {
		t.Fatal(err)
	}
}

// assertInstitutionApprovalAudit 核对两审按真实的监管与机构席位留痕。
// 参数 t、handler 为隔离环境，history 为机构审核历史地址，institution 为预期初审机构名称。
// 返回值 无。
func assertInstitutionApprovalAudit(t *testing.T, handler http.Handler, history, institution string) {
	t.Helper()
	items := readResponse[itemResponse[reviewEvent]](t, requestJSON(handler, "GET", history, nil), 200).Items
	if len(items) != 3 || items[0].ToStatus != approved || items[1].ToStatus != pendingFinal ||
		items[0].Operator == items[1].Operator || items[1].Operator != "本地机构："+institution {
		t.Fatalf("双阶段审核留痕不符: %+v", items)
	}
}
