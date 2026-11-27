package cases

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

const operatorApplicationURL = "/api/v1/operator/filing-applications"

// TestOperatorApplicationScopeMaterialsAndReceipt 贯通运营者材料、归属与有效收件回执。
// 参数 t 为真实隔离测试上下文。
// 返回值 无。
func TestOperatorApplicationScopeMaterialsAndReceipt(t *testing.T) {
	service, handler := fixtureService(t)
	service.RegisterOperatorApplicationRoutes(handler.(*http.ServeMux))
	filing := approvedFiling(t, handler, "BA-OP-APP")
	owner := "?organization=" + url.QueryEscape("测试单位")
	other := "?organization=" + url.QueryEscape("其他单位")
	assertError(t, requestJSON(handler, "POST", operatorApplicationURL+owner, map[string]any{
		"filingId": filing.ID, "type": "change", "reason": "自助调整所有权",
		"changes": map[string]string{"organizationName": "其他单位"},
	}), 400, "invalid_input")
	input := map[string]any{"filingId": filing.ID, "type": "cancel", "reason": "已完成业务下线并保留所需数据"}
	assertError(t, requestJSON(handler, "POST", operatorApplicationURL+other, input), 404, "not_found")
	created := readResponse[mutation](t, requestJSON(handler, "POST", operatorApplicationURL+owner, input), 201)
	path := operatorApplicationURL + "/" + created.ID
	assertError(t, requestJSON(handler, "GET", path+other, nil), 404, "not_found")
	assertError(t, requestJSON(handler, "GET", path+"/receipt"+owner, nil), 409, "receipt_unavailable")
	assertError(t, requestJSON(handler, "POST", applicationURL+"/"+created.ID+"/submit", versionInput{created.Version}), 400, "application_materials_required")
	assertError(t, uploadApplicationFile(t, handler, path, owner, "change_statement"), 400, "invalid_input")
	file := readResponse[Attachment](t, uploadApplicationFile(t, handler, path, owner, "cancellation_statement"), 201)
	assertError(t, requestJSON(handler, "GET", path+"/attachments/"+file.ID+other, nil), 404, "not_found")
	download := requestJSON(handler, "GET", path+"/attachments/"+file.ID+owner, nil)
	if download.Code != 200 || !strings.HasPrefix(download.Body.String(), "%PDF-") {
		t.Fatalf("incorrect application attachment: %d %s", download.Code, download.Body.String())
	}
	assertApplicationMaterialReaders(t, handler, created.ID, file.ID)
	submitted := readResponse[mutation](t, requestJSON(handler, "POST", path+"/submit"+owner, versionInput{created.Version + 1}), 200)
	assertOperatorApplicationReceipt(t, handler, path, owner, other)
	assertOperatorApplicationReview(t, service, handler, path, owner, created.ID, submitted.Version)
}

// assertApplicationMaterialReaders 核对监管和原发起机构可审阅同一份运营者申请材料。
// 参数 t 为测试上下文，handler 为业务路由，applicationID 和 fileID 为申请和附件标识。
// 返回值 无。
func assertApplicationMaterialReaders(t *testing.T, handler http.Handler, applicationID, fileID string) {
	t.Helper()
	admin := applicationURL + "/" + applicationID + "/attachments"
	files := readResponse[itemResponse[Attachment]](t, requestJSON(handler, "GET", admin, nil), 200)
	if len(files.Items) != 1 || files.Items[0].ID != fileID {
		t.Fatalf("regulator material list: %+v", files)
	}
	institution := "/api/v1/institution/filing-applications/" + applicationID + "/attachments/" + fileID
	original := requestJSON(handler, "GET", institution+"?institution="+url.QueryEscape("测评中心"), nil)
	if original.Code != 200 || !strings.HasPrefix(original.Body.String(), "%PDF-") {
		t.Fatalf("institution cannot retrieve shared material: %d", original.Code)
	}
	assertError(t, requestJSON(handler, "GET", institution+"?institution="+url.QueryEscape("其他机构"), nil), 404, "not_found")
}

// assertOperatorApplicationReceipt 核对回执来自实际提交留痕，并拒绝不同范围访问。
// 参数 t 为测试上下文，handler 为业务路由，path、owner 和 other 为申请地址及范围。
// 返回值 无。
func assertOperatorApplicationReceipt(t *testing.T, handler http.Handler, path, owner, other string) {
	t.Helper()
	item := readResponse[map[string]any](t, requestJSON(handler, "GET", path+owner, nil), 200)
	if item["receiptAvailable"] != true {
		t.Fatalf("missing receipt flag: %v", item)
	}
	assertError(t, requestJSON(handler, "GET", path+"/receipt"+other, nil), 404, "not_found")
	receipt := requestJSON(handler, "GET", path+"/receipt"+owner, nil)
	if receipt.Code != 200 || !strings.Contains(receipt.Body.String(), "BA-OP-APP") ||
		!strings.Contains(receipt.Header().Get("Content-Disposition"), "filing-application-receipt.txt") {
		t.Fatalf("receipt lacks audit provenance: %d %s", receipt.Code, receipt.Body.String())
	}
	filter := readResponse[applicationPage](t, requestJSON(handler, "GET", operatorApplicationURL+owner+"&type=cancel", nil), 200)
	if len(filter.Items) != 1 || !strings.HasSuffix(path, "/"+filter.Items[0].ID) {
		t.Fatalf("scoped listing incorrect: %+v", filter)
	}
}

// assertOperatorApplicationReview 核对反馈失败时审核回滚，并确认双方真实收件和留痕。
// 参数 t 为测试上下文，service 为隔离服务，handler、path、scope、id 和 version 为审核上下文。
// 返回值 无。
func assertOperatorApplicationReview(t *testing.T, service *Service, handler http.Handler, path, scope, id string, version int64) {
	t.Helper()
	if _, err := service.db.Exec(t.Context(), `ALTER TABLE review_notices ADD CONSTRAINT test_block_app_notice
		CHECK (audience<>'operator' OR resource<>'filing-applications')`); err != nil {
		t.Fatal(err)
	}
	admin := applicationURL + "/" + id
	partner := institutionBase + "/filing-applications/" + id + "/review-actions" + firstSubmissionScope("测评中心")
	decision := reviewInput{versionInput: versionInput{version}, Decision: returnForFix, Comment: "补充注销依据"}
	assertError(t, requestJSON(handler, "POST", partner, decision), 500, "internal_error")
	unchanged := readResponse[applicationDetail](t, requestJSON(handler, "GET", admin, nil), 200)
	if unchanged.Status != pendingInitial || unchanged.Version != version {
		t.Fatalf("partial review: %+v", unchanged)
	}
	if _, err := service.db.Exec(t.Context(), `ALTER TABLE review_notices DROP CONSTRAINT test_block_app_notice`); err != nil {
		t.Fatal(err)
	}
	readResponse[mutation](t, requestJSON(handler, "POST", partner, decision), 200)
	history := readResponse[itemResponse[reviewEvent]](t, requestJSON(handler, "GET", path+"/review-history"+scope, nil), 200)
	if len(history.Items) != 2 || history.Items[0].Comment != "补充注销依据" {
		t.Fatalf("operator history missing institution feedback: %+v", history)
	}
	var notices int
	err := service.db.QueryRow(t.Context(), `SELECT count(*) FROM review_notices WHERE resource='filing-applications'
		AND resource_id=$1 AND event_version=$2 AND state='returned' AND (
		(audience='operator' AND recipient='测试单位') OR (audience='institution' AND recipient='测评中心'))`,
		id, version+1).Scan(&notices)
	if err != nil || notices != 2 {
		t.Fatalf("incomplete notice deliveries: %d, %v", notices, err)
	}
}

// TestOperatorApplicationChecksCurrentFilingOwner 验证历史归属可读，但原单位不能修改已转属备案的申请。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestOperatorApplicationChecksCurrentFilingOwner(t *testing.T) {
	service, handler := fixtureService(t)
	service.RegisterOperatorApplicationRoutes(handler.(*http.ServeMux))
	filing := approvedFiling(t, handler, "BA-OWN-APP")
	owner := "?organization=" + url.QueryEscape("测试单位")
	created := readResponse[mutation](t, requestJSON(handler, "POST", operatorApplicationURL+owner,
		map[string]any{"filingId": filing.ID, "type": "cancel", "reason": "保留历史归属"}), 201)
	if _, err := service.db.Exec(t.Context(), `UPDATE business_cases SET organization_name='已转属单位' WHERE id=$1`, filing.ID); err != nil {
		t.Fatal(err)
	}
	path := operatorApplicationURL + "/" + created.ID
	historical := readResponse[applicationDetail](t, requestJSON(handler, "GET", path+owner, nil), 200)
	if len(historical.AllowedActions) != 0 {
		t.Fatalf("previous operator still offered write actions: %+v", historical)
	}
	assertError(t, uploadApplicationFile(t, handler, path, owner, "cancellation_statement"), 404, "not_found")
	assertError(t, requestJSON(handler, "PATCH", path+owner, map[string]any{"reason": "试图变更旧单", "expectedVersion": 1}), 404, "not_found")
	assertError(t, requestJSON(handler, "POST", path+"/withdraw"+owner, versionInput{1}), 404, "not_found")
}

// TestChangedAssessmentOrgRequiresBoundApprovedImplementation 验证换机构申请取得并持续核对已通过报备。
// 参数 t 为真实隔离数据库测试上下文。
// 返回值 无。
func TestChangedAssessmentOrgRequiresBoundApprovedImplementation(t *testing.T) {
	service, handler := fixtureService(t)
	filing := approvedFiling(t, handler, "BA-RETEST-APP")
	form := map[string]any{"filingId": filing.ID, "type": "change", "reason": "变更后进行复测",
		"changes": map[string]string{"assessmentOrg": "新的测评机构"}}
	created := readResponse[mutation](t, requestJSON(handler, "POST", applicationURL, form), 201)
	path := applicationURL + "/" + created.ID
	assertError(t, requestJSON(handler, "POST", path+"/submit", versionInput{1}), 409, "retest_required")
	insertApplicationImplementation(t, service, filing.ID, "新的测评机构", "routine")
	insertApplicationImplementation(t, service, filing.ID, "错误机构", "change")
	other := approvedFiling(t, handler, "BA-RETEST-OTHER")
	insertApplicationImplementation(t, service, other.ID, "新的测评机构", "change")
	assertError(t, requestJSON(handler, "POST", path+"/submit", versionInput{1}), 409, "retest_required")
	bound := insertApplicationImplementation(t, service, filing.ID, "新的测评机构", "change")
	readResponse[mutation](t, requestJSON(handler, "POST", path+"/submit", versionInput{1}), 200)
	item := readResponse[map[string]any](t, requestJSON(handler, "GET", path, nil), 200)
	if item["implementationId"] != bound {
		t.Fatalf("retest binding missing: %v", item)
	}
	insertApplicationImplementation(t, service, filing.ID, "新的测评机构", "change")
	for _, expected := range []int64{2, 3} {
		if _, err := service.db.Exec(t.Context(), `UPDATE implementation_reports SET status='returned' WHERE id=$1`, bound); err != nil {
			t.Fatal(err)
		}
		assertError(t, reviewRequest(handler, path, "approve", "", expected), 409, "retest_required")
		if _, err := service.db.Exec(t.Context(), `UPDATE implementation_reports SET status='approved' WHERE id=$1`, bound); err != nil {
			t.Fatal(err)
		}
		readResponse[mutation](t, reviewRequest(handler, path, "approve", "", expected), 200)
	}
	changed := readResponse[recordDetail](t, requestJSON(handler, "GET", filingsURL+"/"+filing.ID, nil), 200)
	if changed.AssessmentOrg != "新的测评机构" {
		t.Fatalf("filing retained previous institution: %+v", changed)
	}
}

// TestOperatorApplicationShowsReusableRetestProgress 验证同一目标的有效报备走势可见并优先显示通过结果。
// 参数 t 为隔离真实业务测试上下文。
// 返回值 无。
func TestOperatorApplicationShowsReusableRetestProgress(t *testing.T) {
	service, handler := fixtureService(t)
	service.RegisterOperatorApplicationRoutes(handler.(*http.ServeMux))
	filing := approvedFiling(t, handler, "BA-RETEST-PROGRESS")
	owner := "?organization=" + url.QueryEscape("测试单位")
	form := assessmentTransferForm(filing.ID, "目标测评机构")
	created := readResponse[mutation](t, requestJSON(handler, "POST", operatorApplicationURL+owner, form), 201)
	path := operatorApplicationURL + "/" + created.ID
	resumable := insertApplicationImplementation(t, service, filing.ID, "目标测评机构", "change")
	withdrawn := insertApplicationImplementation(t, service, filing.ID, "目标测评机构", "change")
	if _, err := service.db.Exec(t.Context(), `UPDATE implementation_reports SET status='returned' WHERE id=$1`, resumable); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(t.Context(), `UPDATE implementation_reports SET status='withdrawn',accepted_at=NULL WHERE id=$1`, withdrawn); err != nil {
		t.Fatal(err)
	}
	assertRetestSuggestion(t, handler, path+owner, resumable, "returned")
	passed := insertApplicationImplementation(t, service, filing.ID, "目标测评机构", "change")
	assertRetestSuggestion(t, handler, path+owner, passed, "approved")
	assertRetestSuggestion(t, handler, applicationURL+"/"+created.ID, "", "")
	readResponse[Attachment](t, uploadApplicationFile(t, handler, path, owner, "change_statement"), 201)
	readResponse[mutation](t, requestJSON(handler, "POST", path+"/submit"+owner, versionInput{2}), 200)
	assertRetestSuggestion(t, handler, path+owner, "", "")
}

// TestApprovedAssessmentTransfersConsumeImplementationOnce 验证换回并再换同一机构时必须使用新的报备依据。
// 参数 t 为真实隔离数据库测试上下文。
// 返回值 无。
func TestApprovedAssessmentTransfersConsumeImplementationOnce(t *testing.T) {
	service, handler := fixtureService(t)
	service.RegisterOperatorApplicationRoutes(handler.(*http.ServeMux))
	filing := approvedFiling(t, handler, "BA-RETEST-REUSE")
	consumed := insertApplicationImplementation(t, service, filing.ID, "第二家测评机构", "change")
	completeAssessmentTransfer(t, handler, filing.ID, "第二家测评机构")
	insertApplicationImplementation(t, service, filing.ID, "测评中心", "change")
	completeAssessmentTransfer(t, handler, filing.ID, "测评中心")
	created := readResponse[mutation](t, requestJSON(handler, "POST", applicationURL,
		assessmentTransferForm(filing.ID, "第二家测评机构")), 201)
	path := applicationURL + "/" + created.ID
	owner := "?organization=" + url.QueryEscape("测试单位")
	scoped := operatorApplicationURL + "/" + created.ID + owner
	assertRetestSuggestion(t, handler, scoped, "", "")
	assertError(t, requestJSON(handler, "POST", path+"/submit", versionInput{1}), 409, "retest_required")
	_, err := service.db.Exec(t.Context(), `UPDATE filing_applications SET status='approved',applied_at=now(),
		implementation_id=$2 WHERE id=$1`, created.ID, consumed)
	var conflict *pgconn.PgError
	if !errors.As(err, &conflict) || conflict.Code != "23505" || conflict.ConstraintName != "filing_applications_implementation_once" {
		t.Fatalf("database accepted a consumed retest as fresh evidence: %v", err)
	}
	replacement := insertApplicationImplementation(t, service, filing.ID, "第二家测评机构", "change")
	assertRetestSuggestion(t, handler, scoped, replacement, "approved")
	readResponse[mutation](t, requestJSON(handler, "POST", path+"/submit", versionInput{1}), 200)
	item := readResponse[map[string]any](t, requestJSON(handler, "GET", path, nil), 200)
	if item["implementationId"] != replacement {
		t.Fatalf("new approved evidence was not bound: %v", item)
	}
}

// assertRetestSuggestion 核对运营者详情提供的最小报备进度或明确省略该提示。
// 参数 t 为测试上下文，handler 和 path 为详情请求，id 和 status 为预期可用报备。
// 返回值 无。
func assertRetestSuggestion(t *testing.T, handler http.Handler, path, id, status string) {
	t.Helper()
	item := readResponse[struct{ RetestImplementation *struct{ ID, Status string } }](t, requestJSON(handler, "GET", path, nil), 200)
	if id == "" && item.RetestImplementation == nil {
		return
	}
	if item.RetestImplementation == nil || item.RetestImplementation.ID != id || item.RetestImplementation.Status != status {
		t.Fatalf("incorrect reusable retest opportunity: %+v", item.RetestImplementation)
	}
}

// assessmentTransferForm 生成同一备案更换测评机构的申请表单。
// 参数 id 和 target 为备案标识及拟变更的机构名称。
// 返回值 合规的换机构申请表单。
func assessmentTransferForm(id, target string) map[string]any {
	return map[string]any{"filingId": id, "type": "change", "reason": "变更测评机构并复测",
		"changes": map[string]string{"assessmentOrg": target}}
}

// completeAssessmentTransfer 运行变更申请的提交、初审及终审，并消费当时已通过的报备。
// 参数 t 为测试上下文，handler 为业务路由，filingID 和 target 为备案及拟变更机构。
// 返回值 无。
func completeAssessmentTransfer(t *testing.T, handler http.Handler, filingID, target string) {
	t.Helper()
	created := readResponse[mutation](t, requestJSON(handler, "POST", applicationURL, assessmentTransferForm(filingID, target)), 201)
	path := applicationURL + "/" + created.ID
	readResponse[mutation](t, requestJSON(handler, "POST", path+"/submit", versionInput{1}), 200)
	for _, version := range []int64{2, 3} {
		readResponse[mutation](t, reviewRequest(handler, path, "approve", "", version), 200)
	}
}

// insertApplicationImplementation 构造持久化的通过实施报备，并满足其业务状态和配额字段约束。
// 参数 t 为测试上下文，service 为隔离服务，id、organization 和 kind 指定备案、机构和报备类型。
// 返回值 创建的报备标识。
func insertApplicationImplementation(t *testing.T, service *Service, id, organization, kind string) string {
	t.Helper()
	var reportID string
	err := service.db.QueryRow(t.Context(), `INSERT INTO implementation_reports
		(filing_id,filing_number,system_name,organization_name,assessment_org,report_type,reporter,
		 planned_start,planned_end,plan,created_by,status,submitted_at,accepted_at,quota_year)
		SELECT id,number,system_name,organization_name,$2,$3,'测试报备人',CURRENT_DATE,CURRENT_DATE,
		       '隔离复测安排','测试席位','approved',now(),now(),CASE WHEN $3='change' THEN 2026 END
		FROM business_cases WHERE id=$1 RETURNING id::text`, id, organization, kind).Scan(&reportID)
	if err != nil {
		t.Fatal(err)
	}
	return reportID
}

// uploadApplicationFile 在真实运营者路由上传单个受控 PDF。
// 参数 t 为测试上下文，handler 为路由，path、scope 和 kind 为申请、单位及材料类别。
// 返回值 上传响应。
func uploadApplicationFile(t *testing.T, handler http.Handler, path, scope, kind string) *httptest.ResponseRecorder {
	t.Helper()
	var content bytes.Buffer
	form := multipart.NewWriter(&content)
	if err := form.WriteField("kind", kind); err != nil {
		t.Fatal(err)
	}
	file, err := form.CreateFormFile("file", "备案说明.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("%PDF-1.7\n% isolated example\n%%EOF")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", path+"/attachments"+scope, &content)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
