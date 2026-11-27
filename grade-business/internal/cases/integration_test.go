package cases

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/migrations"
)

const (
	filingsURL = "/api/v1/filings"
	reportsURL = "/api/v1/reports"
	testPDF    = "%PDF-1.7\n% local business fixture\n%%EOF"
)

// TestFilingHTTPWorkflow 验证备案的材料、补正、并发审核和落库留痕。
// 参数 t 报告测试中的业务结果。
// 返回值 无。
func TestFilingHTTPWorkflow(t *testing.T) {
	service, handler := fixtureService(t)
	f := createFiling(t, handler, " QA-BIZ-01 ")
	path := filingsURL + "/" + f.ID
	assertError(t, requestJSON(handler, "POST", path+"/submit", versionInput{f.Version}), 400, "materials_required")
	first := postMaterial(t, handler, path, "filing_form")
	postMaterial(t, handler, path, "classification_report")
	snapshot, err := service.FilingMaterials(t.Context(), f.ID)
	if err != nil || snapshot.Revision != 2 || len(snapshot.Attachments) != 2 || snapshot.Attachments[0].ID != first.ID {
		t.Fatalf("unexpected material snapshot: %+v, %v", snapshot, err)
	}
	submitted := readResponse[mutation](t, requestJSON(handler, "POST", path+"/submit", versionInput{f.Version + 2}), 200)
	if submitted.Status != pendingInitial || submitted.Version != 4 {
		t.Fatalf("unexpected submit result: %+v", submitted)
	}
	assertError(t, reviewRequest(handler, path, "return_for_correction", " ", 4), 400, "invalid_input")
	returnedRecord := readResponse[mutation](t, reviewRequest(handler, path, "return_for_correction", "补充意见", 4), 200)
	if returnedRecord.Status != returned || returnedRecord.Version != 5 {
		t.Fatalf("unexpected return result: %+v", returnedRecord)
	}
	postMaterial(t, handler, path, "supporting")
	readResponse[mutation](t, requestJSON(handler, "POST", path+"/submit", versionInput{6}), 200)
	assertOneReview(t, handler, path, 7)
	final := readResponse[mutation](t, reviewRequest(handler, path, "approve", "完成审核", 8), 200)
	if final.Status != approved || final.Version != 9 {
		t.Fatalf("unexpected final result: %+v", final)
	}
	assertHistory(t, handler, path)
}

// assertOneReview 确保两次并发提交只产生一次有效审核。
// 参数 t 为测试上下文，handler 为实际 HTTP 路由，path 为业务路径，version 为两个请求共用的预期版本。
// 返回值 无。
func assertOneReview(t *testing.T, handler http.Handler, path string, version int64) {
	t.Helper()
	var group sync.WaitGroup
	results := make(chan int, 2)
	for range 2 {
		group.Go(func() {
			results <- reviewRequest(handler, path, "approve", "", version).Code
		})
	}
	group.Wait()
	counts := map[int]int{<-results: 1}
	counts[<-results]++
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatalf("concurrent review HTTP statuses: %v", counts)
	}
}

// assertHistory 验证真正保存的每个审核节点和必要业务字段。
// 参数 t 为测试上下文，handler 为实际路由，path 为备案地址。
// 返回值 无。
func assertHistory(t *testing.T, handler http.Handler, path string) {
	t.Helper()
	history := readResponse[itemResponse[reviewEvent]](
		t, requestJSON(handler, "GET", path+"/review-history", nil), 200).Items
	if len(history) != 5 || history[0].ToStatus != approved || history[1].ToStatus != pendingFinal ||
		history[3].Comment != "补充意见" || history[0].Operator != "测试监管席位" {
		t.Fatalf("history does not match persisted transitions: %+v", history)
	}
}

// TestReportOwnershipAndWorkflow 验证报告关联合规、材料隔离和终审通过。
// 参数 t 报告实际 API 响应与数据库结果。
// 返回值 无。
func TestReportOwnershipAndWorkflow(t *testing.T) {
	service, handler := fixtureService(t)
	first := createFiling(t, handler, "BA-1")
	path := filingsURL + "/" + first.ID
	attachment := postMaterial(t, handler, path, "filing_form")
	input := reportInput{Number: "BG-01", FilingID: first.ID, AssessmentOrg: "测评中心", Risk: "high"}
	reportRow := readResponse[mutation](t, requestJSON(handler, "POST", reportsURL, input), 201)
	reportPath := reportsURL + "/" + reportRow.ID

	input.Number, input.FilingID = "BG-02", reportRow.ID
	assertError(t, requestJSON(handler, "POST", reportsURL, input), 404, "not_found")
	assertError(t, requestJSON(handler, "GET", reportPath+"/attachments/"+attachment.ID, nil), 404, "not_found")
	assertError(t, requestJSON(handler, "GET", filingsURL+"/"+reportRow.ID, nil), 404, "not_found")
	if _, err := service.FilingMaterials(t.Context(), reportRow.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("report returned as a filing material snapshot: %v", err)
	}
	assertError(t, requestJSON(handler, "POST", reportPath+"/submit", versionInput{1}), 400, "materials_required")
	reportFile := postMaterial(t, handler, reportPath, "report_file")
	checkDownloadedMaterial(t, handler, reportPath+"/attachments/"+reportFile.ID)

	readResponse[mutation](t, requestJSON(handler, "POST", reportPath+"/submit", versionInput{2}), 200)
	readResponse[mutation](t, reviewRequest(handler, reportPath, "approve", "", 3), 200)
	readResponse[mutation](t, reviewRequest(handler, reportPath, "approve", "", 4), 200)
	got := readResponse[recordDetail](t, requestJSON(handler, "GET", reportPath, nil), 200)
	if got.Status != approved || got.Risk != "high" || got.FilingID != first.ID ||
		got.SystemName != "监管系统" || got.FilingNumber != "BA-1" || got.SubmittedAt == nil {
		t.Fatalf("unexpected persisted report: %+v", got)
	}
}

// TestReportRiskListWithPostgres 核对测评风险级别与关键词组合筛选。
// 参数 t 为测试上下文，数据库按现有 fixture 隔离。
// 返回值 无。
func TestReportRiskListWithPostgres(t *testing.T) {
	_, handler := fixtureService(t)
	filing := createFiling(t, handler, "BA-RISK")
	input := reportInput{Number: "风险筛选-高", FilingID: filing.ID, AssessmentOrg: "测评中心", Risk: "high"}
	high := readResponse[mutation](t, requestJSON(handler, "POST", reportsURL, input), 201)
	input.Number, input.Risk = "风险筛选-低", "low"
	readResponse[mutation](t, requestJSON(handler, "POST", reportsURL, input), 201)
	path := reportsURL + "?risk=high&status=draft&q=BA-RISK"
	found := readResponse[listResponse](t, requestJSON(handler, "GET", path, nil), 200)
	if len(found.Items) != 1 || found.Items[0].ID != high.ID {
		t.Fatalf("组合筛选结果错误: %+v", found)
	}
	assertError(t, requestJSON(handler, "GET", reportsURL+"?risk=unknown", nil), 400, "invalid_input")
	assertError(t, requestJSON(handler, "GET", reportsURL+"?risk=high&risk=low", nil), 400, "invalid_input")
	assertError(t, requestJSON(handler, "GET", filingsURL+"?risk=high", nil), 400, "invalid_input")
}

// checkDownloadedMaterial 验证公开材料响应仅使用附件名和实际内容。
// 参数 t 为测试上下文，handler 为实际路由，path 为材料下载地址。
// 返回值 无。
func checkDownloadedMaterial(t *testing.T, handler http.Handler, path string) {
	t.Helper()
	file := requestJSON(handler, "GET", path, nil)
	if file.Code != 200 || file.Body.String() != testPDF ||
		!strings.Contains(file.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("unexpected file download: %d", file.Code)
	}
}

// TestFilingInputAndPagination 验证字段白名单、编号唯一和无重复的分页。
// 参数 t 报告 HTTP 接口及相关数据库结果。
// 返回值 无。
func TestFilingInputAndPagination(t *testing.T) {
	_, handler := fixtureService(t)
	first := createFiling(t, handler, "BA-01")
	second := createFiling(t, handler, "BA-02")
	raw := json.RawMessage(`{"number":"BA-03","systemName":"监管系统","organizationName":"测试单位","assessmentOrg":"测试机构","level":"3","clientIp":"192.0.2.100"}`)
	assertError(t, requestJSON(handler, "POST", filingsURL, raw), 400, "invalid_input")
	assertError(t, requestJSON(handler, "POST", filingsURL, sampleFiling("ba-01")), 409, "duplicate_number")
	checkPagination(t, handler, first.ID, second.ID)
	update := struct {
		filingInput
		versionInput
	}{sampleFiling("BA-03"), versionInput{1}}
	path := filingsURL + "/" + second.ID
	result := readResponse[mutation](t, requestJSON(handler, "PATCH", path, update), 200)
	if result.Version != 2 {
		t.Fatalf("update did not advance version: %+v", result)
	}
	assertError(t, requestJSON(handler, "PATCH", path, update), 409, "conflict")
}

// checkPagination 检查返回列与游标后的稳定去重。
// 参数 t 为测试上下文，handler 为真实路由，first、second 为按创建顺序生成的标识。
// 返回值 无。
func checkPagination(t *testing.T, handler http.Handler, first, second string) {
	t.Helper()
	page := readResponse[listResponse](t, requestJSON(handler, "GET", filingsURL+"?limit=1&q=监管", nil), 200)
	if len(page.Items) != 1 || page.NextCursor == "" || page.Items[0].ID != second {
		t.Fatalf("first page: %+v", page)
	}
	next := readResponse[listResponse](t, requestJSON(handler, "GET", filingsURL+"?limit=1&q=监管&cursor="+page.NextCursor, nil), 200)
	if len(next.Items) != 1 || next.NextCursor != "" || next.Items[0].ID != first {
		t.Fatalf("second page: %+v", next)
	}
	keys := readResponse[map[string]json.RawMessage](t, requestJSON(handler, "GET", filingsURL+"/"+first, nil), 200)
	if len(keys) != 9 || keys["risk"] != nil || keys["clientIp"] != nil ||
		keys["version"] == nil || keys["allowedActions"] == nil {
		t.Fatalf("unexpected response field set: %v", keys)
	}
}

// sampleFiling 构造可复用的虚构备案输入。
// 参数 number 为测试备案编号。
// 返回值 合法的备案表单。
func sampleFiling(number string) filingInput {
	return filingInput{Number: number, SystemName: "监管系统", OrganizationName: "测试单位", AssessmentOrg: "测评中心", Level: "3"}
}

// createFiling 新建独立测试备案并解码公开响应。
// 参数 t 为测试上下文，handler 为真实路由，number 为示例备案编号。
// 返回值 新备案标识、状态和版本。
func createFiling(t *testing.T, handler http.Handler, number string) mutation {
	t.Helper()
	return readResponse[mutation](t, requestJSON(handler, "POST", filingsURL, sampleFiling(number)), 201)
}

// reviewRequest 构造并发送指定审核动作。
// 参数 handler 为实际路由，path 为记录路径，decision、comment、version 为审核输入。
// 返回值 审核接口响应。
func reviewRequest(handler http.Handler, path, decision, comment string, version int64) *httptest.ResponseRecorder {
	return requestJSON(handler, "POST", path+"/review-actions",
		reviewInput{versionInput: versionInput{version}, Decision: decision, Comment: comment})
}

// postMaterial 将测试 PDF 通过真实材料接口保存。
// 参数 t 为测试上下文，handler 为真实路由，path 为父记录地址，kind 为材料种类。
// 返回值 已落库的附件元数据。
func postMaterial(t *testing.T, handler http.Handler, path, kind string) Attachment {
	t.Helper()
	return postMaterialAt(t, handler, path+"/attachments", kind)
}

// postMaterialAt 验证指定上传地址可接收合成测试 PDF。
// 参数 t 为测试上下文，handler 为实际路由，path 为含查询参数的上传地址，kind 为材料类别。
// 返回值 已保存附件的公开字段。
func postMaterialAt(t *testing.T, handler http.Handler, path, kind string) Attachment {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("kind", kind); err != nil {
		t.Fatal(err)
	}
	file, err := form.CreateFormFile("file", "测试材料.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(testPDF)); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", path, &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return readResponse[Attachment](t, w, 201)
}

// requestJSON 经过 stdlib 路由发送测试 JSON 请求。
// 参数 handler 为实际路由，method 和 path 为 HTTP 路由条件，input 为可选请求体。
// 返回值 已记录的 HTTP 响应。
func requestJSON(handler http.Handler, method, path string, input any) *httptest.ResponseRecorder {
	var body []byte
	if input != nil {
		body, _ = json.Marshal(input)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if input != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

// readResponse 检查 HTTP 状态并解码为预期结构。
// 参数 t 为测试上下文，w 为响应，expected 为预期 HTTP 状态。
// 返回值 经过 JSON 解码的响应值。
func readResponse[T any](t *testing.T, w *httptest.ResponseRecorder, expected int) T {
	t.Helper()
	var result T
	if w.Code != expected {
		t.Fatalf("HTTP %d, expected %d: %s", w.Code, expected, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// assertError 检查 HTTP 错误码和最小错误字段。
// 参数 t 为测试上下文，w 为响应，expected 和 code 为预期 HTTP 及业务状态码。
// 返回值 无。
func assertError(t *testing.T, w *httptest.ResponseRecorder, expected int, code string) {
	t.Helper()
	item := readResponse[map[string]string](t, w, expected)
	if item["code"] != code || item["message"] == "" || len(item) != 2 {
		t.Fatalf("unexpected error fields: %v", item)
	}
}

// fixtureService 创建实际业务路由，并使用独立数据库模式和文件卷。
// 参数 t 为用于清理环境的测试上下文。
// 返回值 业务服务及其 HTTP 路由。
func fixtureService(t *testing.T) (*Service, http.Handler) {
	t.Helper()
	pool := fixtureDB(t)
	s, err := New(pool, t.TempDir(), "测试监管席位")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	return s, mux
}

// fixtureDB 连接显式提供的测试库，并为本用例创建独立数据模式。
// 参数 t 为测试上下文。
// 返回值 已迁移的专用数据库连接池。
func fixtureDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if dsn == "" {
		t.Skip("GRADE_BUSINESS_TEST_DSN not set")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid business test DSN")
	}
	if config.ConnConfig.Database != "grade_business_test" {
		t.Fatal("requires explicitly isolated grade_business_test database")
	}
	admin, err := pgxpool.NewWithConfig(t.Context(), config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("cases_%d", time.Now().UnixNano())
	name := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+name+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	return migrateFixture(t, config, schema)
}

// migrateFixture 在本用例数据模式中执行实际业务迁移。
// 参数 t 为测试上下文，config 为隔离连接配置，schema 为独立数据模式名称。
// 返回值 完成业务迁移的连接池。
func migrateFixture(t *testing.T, config *pgxpool.Config, schema string) *pgxpool.Pool {
	t.Helper()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrations.Apply(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

// TestReviewAndAuditRollback 验证留痕写入失败时审核状态不会单独推进。
// 参数 t 为测试上下文。
// 返回值 无。
func TestReviewAndAuditRollback(t *testing.T) {
	service, handler := fixtureService(t)
	record := createFiling(t, handler, "BA-ATOMIC")
	path := filingsURL + "/" + record.ID
	postMaterial(t, handler, path, "filing_form")
	postMaterial(t, handler, path, "classification_report")
	readResponse[mutation](t, requestJSON(handler, "POST", path+"/submit", versionInput{3}), 200)
	if _, err := service.db.Exec(t.Context(),
		`ALTER TABLE case_review_events ADD CONSTRAINT qa_reject_approval CHECK (action <> 'approve')`); err != nil {
		t.Fatal(err)
	}
	assertError(t, reviewRequest(handler, path, "approve", "", 4), 500, "internal_error")
	detail := readResponse[recordDetail](t, requestJSON(handler, "GET", path, nil), 200)
	history := readResponse[itemResponse[reviewEvent]](t, requestJSON(handler, "GET", path+"/review-history", nil), 200)
	if detail.Version != 4 || detail.Status != pendingInitial || len(history.Items) != 1 ||
		history.Items[0].Action != submit {
		t.Fatalf("failed audit changed persisted state: %+v, %+v", detail, history)
	}
}
