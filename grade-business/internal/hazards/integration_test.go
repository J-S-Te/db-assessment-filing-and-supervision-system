package hazards

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"grade-business/internal/cases"
	"grade-business/migrations"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testLedgerPath = "/api/v1/hazards"

// TestManualSyncCopiesRiskSnapshot 验证历史整改规划按风险生成唯一台账且不重复登记。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestManualSyncCopiesRiskSnapshot(t *testing.T) {
	service, router := ledgerFixture(t)
	plan, report, filing := seedPlan(t, service.db)
	syncPath := testLedgerPath + "/sync/" + plan
	for range 2 {
		result := ledgerReply[struct{ Count int }](t, ledgerCall(router, "POST", syncPath, nil), 200)
		if result.Count != 2 {
			t.Fatalf("expected two risk items after each sync, got %+v", result)
		}
	}
	path := testLedgerPath + "?planId=" + plan + "&q=系统甲&severity=high"
	page := ledgerReply[struct{ Items []map[string]any }](t, ledgerCall(router, "GET", path, nil), 200)
	if len(page.Items) != 1 || page.Items[0]["reportId"] != report || page.Items[0]["filingId"] != filing ||
		page.Items[0]["description"] != "风险一" || page.Items[0]["deadline"] != "2099-01-01" {
		t.Fatalf("unexpected stored risk projection: %+v", page)
	}
}

// ledgerFixture 在显式隔离库的唯一模式内启动真实 API 和材料存储。
// 参数 t 为测试上下文，管理临时模式和目录生命周期。
// 返回值 实际业务服务和 HTTP 路由。
func ledgerFixture(t *testing.T) (*Service, http.Handler) {
	t.Helper()
	return ledgerFixtureWithMaterials(t, t.TempDir())
}

// ledgerFixtureWithMaterials 为断言证据清理保留对测试材料目录的读取能力。
// 参数 t 为测试上下文，directory 为该测试创建的独立受控目录。
// 返回值 真实台账服务和对应的 HTTP 路由。
func ledgerFixtureWithMaterials(t *testing.T, directory string) (*Service, http.Handler) {
	t.Helper()
	dsn := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if dsn == "" {
		t.Skip("GRADE_BUSINESS_TEST_DSN not set")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil || config.ConnConfig.Database != "grade_business_test" {
		t.Fatal("requires the explicitly isolated grade_business_test database")
	}
	pool := createLedgerSchema(t, config)
	if err := migrations.Apply(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	records, err := cases.New(pool, directory, "隐患测试席位")
	if err != nil {
		t.Fatal(err)
	}
	s := New(pool, records, "隐患测试席位")
	mux := http.NewServeMux()
	s.Register(mux)
	s.RegisterOperator(mux)
	s.RegisterInstitution(mux)
	return s, mux
}

// createLedgerSchema 分配测试专属模式并注册清理，避免共享业务表。
// 参数 t 为测试上下文，config 为已验证的隔离库配置。
// 返回值 仅查询当前测试模式的连接池。
func createLedgerSchema(t *testing.T, config *pgxpool.Config) *pgxpool.Pool {
	t.Helper()
	admin, err := pgxpool.NewWithConfig(t.Context(), config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("hazards_%d", time.Now().UnixNano())
	name := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(t.Context(), "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+name+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedPlan 写入一条有两个风险的已审核规划及其上游备案与报告。
// 参数 t 为测试上下文，db 为模式隔离后的测试连接池。
// 返回值 整改规划、测评报告和备案标识。
func seedPlan(t *testing.T, db *pgxpool.Pool) (plan, report, filing string) {
	t.Helper()
	err := db.QueryRow(t.Context(), `WITH filing AS (
	 INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status)
	 VALUES('filing','BA-001','系统甲','试点单位','测评中心','3','approved') RETURNING id
	), report AS (
	 INSERT INTO business_cases(kind,number,assessment_org,filing_id,risk,status)
	 SELECT 'report','BG-001','测评中心',id,'high','approved' FROM filing RETURNING id,filing_id
	), plan AS (
	 INSERT INTO remediation_plans(report_id,deadline,risks,status,submission,submitted_at)
	 SELECT id,'2099-02-01','[{"risk":"high","description":"风险一","measure":"措施一",
	 "responsibility":"责任人甲","deadline":"2099-01-01"},{"risk":"low","description":"风险二",
	 "measure":"措施二","responsibility":"责任人乙","deadline":"2099-01-02"}]',
	 'approved',1,clock_timestamp() FROM report RETURNING id
	) SELECT plan.id::text,report.id::text,report.filing_id::text FROM plan CROSS JOIN report`).
		Scan(&plan, &report, &filing)
	if err != nil {
		t.Fatal(err)
	}
	return plan, report, filing
}

// ledgerCall 通过真实 HTTP 路由发送结构化业务请求。
// 参数 handler 为测试路由，method 和 path 指定接口，body 为可选 JSON 请求对象。
// 返回值 捕获的 HTTP 响应。
func ledgerCall(handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	var data bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&data).Encode(body); err != nil {
			panic(err)
		}
	}
	request := httptest.NewRequest(method, path, &data)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	return result
}

// ledgerReply 核对 HTTP 状态并解码公开响应。
// 参数 t 为测试上下文，response 为捕获的响应，status 为预期状态码。
// 返回值 解码后的响应体。
func ledgerReply[T any](t *testing.T, response *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if response.Code != status {
		t.Fatalf("HTTP %d, want %d: %s", response.Code, status, response.Body.String())
	}
	var out T
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

const fixturePDF = "%PDF-1.7\n% hazard evidence fixture\n%%EOF"

// TestHazardReviewLifecycle 贯通整改证据、退回补正和销号，并核对历史留痕。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestHazardReviewLifecycle(t *testing.T) {
	materialDir := t.TempDir()
	service, router := ledgerFixtureWithMaterials(t, materialDir)
	path, other, plan := syncedPaths(t, service, router)
	original := ledgerReply[hazardDetail](t, ledgerCall(router, "GET", path, nil), 200)
	if original.Status != statusOpen || original.Version != 1 || original.Measure != "措施一" {
		t.Fatalf("initial hazard does not match plan: %+v", original)
	}
	actions := path + "/actions"
	submit := actionInput{Action: actionSubmit, ExpectedVersion: 1, Comment: "已完成整改"}
	assertLedgerError(t, ledgerCall(router, "POST", actions, submit), 400, "evidence_required")
	first := ledgerReply[cases.Attachment](t, ledgerUpload(t, router, path, kindEvidence, fixturePDF), 201)
	assertEvidenceOwnership(t, router, path, other, first.ID)
	assertLedgerError(t, ledgerCall(router, "POST", actions, submit), 409, "conflict")
	submit.ExpectedVersion = 2
	submitted := ledgerReply[hazardMutation](t, ledgerCall(router, "POST", actions, submit), 200)
	assertUploadRejected(t, router, path, materialDir)
	ready := passInstitutionRetest(t, router, original.ID, submitted.Version)
	finishHazardCorrection(t, router, path, ready.Version)
	assertClosedHazard(t, router, path, plan)
	assertHazardFeedback(t, service.db, original.ID)
}

// assertHazardFeedback 核对入账、复测、监管退回和销号按真实事件送达，重复同步不增加通知。
// 参数 t 为测试上下文，db 为隔离数据库，id 为已办结隐患标识。
// 返回值 无。
func assertHazardFeedback(t *testing.T, db *pgxpool.Pool, id string) {
	t.Helper()
	var operator, institution []string
	err := db.QueryRow(t.Context(), `SELECT
		array_agg(state ORDER BY event_version) FILTER(WHERE audience='operator' AND recipient='试点单位'),
		array_agg(state ORDER BY event_version) FILTER(WHERE audience='institution' AND recipient='测评中心')
		FROM review_notices WHERE resource='hazards' AND resource_id=$1
		AND reference='BG-001' AND subject='系统甲'`, id).Scan(&operator, &institution)
	if err != nil || !slices.Equal(operator, []string{"open", "pending_review", "returned", "pending_review", "closed"}) ||
		!slices.Equal(institution, []string{"pending_retest", "returned", "pending_retest", "closed"}) {
		t.Fatalf("incorrect hazard feedback: operator=%v institution=%v error=%v", operator, institution, err)
	}
}

// syncedPaths 预置批准规划并从持久化台账中取两项业务地址。
// 参数 t 为测试上下文，service 为隔离服务，router 为实际 API 路由。
// 返回值 高风险条目地址、低风险条目地址以及上游整改规划标识。
func syncedPaths(t *testing.T, service *Service, router http.Handler) (string, string, string) {
	t.Helper()
	plan, _, _ := seedPlan(t, service.db)
	ledgerReply[map[string]int](t, ledgerCall(router, "POST", testLedgerPath+"/sync/"+plan, nil), 200)
	base := testLedgerPath + "?planId=" + plan + "&severity="
	high := ledgerReply[hazardPage](t, ledgerCall(router, "GET", base+"high", nil), 200)
	low := ledgerReply[hazardPage](t, ledgerCall(router, "GET", base+"low", nil), 200)
	if len(high.Items) != 1 || len(low.Items) != 1 {
		t.Fatalf("missing source hazards: %+v %+v", high, low)
	}
	return testLedgerPath + "/" + high.Items[0].ID, testLedgerPath + "/" + low.Items[0].ID, plan
}

// finishHazardCorrection 校验退回须写明原因，重提必须说明情况并提交新证据。
// 参数 t 为测试上下文，router 为业务路由，path 为隐患地址，version 为等待审核版本。
// 返回值 无。
func finishHazardCorrection(t *testing.T, router http.Handler, path string, version int64) {
	t.Helper()
	actions := path + "/actions"
	ret := actionInput{Action: actionReturn, ExpectedVersion: version}
	assertLedgerError(t, ledgerCall(router, "POST", actions, ret), 400, "invalid_input")
	ret.Comment = "补充复测材料"
	returned := ledgerReply[hazardMutation](t, ledgerCall(router, "POST", actions, ret), 200)
	if returned.Status != statusReturned || returned.Version != version+1 {
		t.Fatalf("unexpected returned hazard: %+v", returned)
	}
	again := actionInput{Action: actionSubmit, ExpectedVersion: returned.Version, Comment: "已完成复测"}
	assertLedgerError(t, ledgerCall(router, "POST", actions, again), 400, "evidence_required")
	ledgerReply[cases.Attachment](t, ledgerUpload(t, router, path, kindEvidence, fixturePDF+"\n% retest"), 201)
	again.ExpectedVersion++
	submitted := ledgerReply[hazardMutation](t, ledgerCall(router, "POST", actions, again), 200)
	pending := passInstitutionRetest(t, router, submitted.ID, submitted.Version)
	closeInput := actionInput{Action: actionClose, ExpectedVersion: pending.Version, Comment: "复核销号"}
	ledgerReply[hazardMutation](t, ledgerCall(router, "POST", actions, closeInput), 200)
}

// assertClosedHazard 验证销号后留痕完整，重复同步仍保持既有台账与证据。
// 参数 t 为测试上下文，router 为实际 API 路由，path 为已销号条目，plan 为原规划标识。
// 返回值 无。
func assertClosedHazard(t *testing.T, router http.Handler, path, plan string) {
	t.Helper()
	count := ledgerReply[map[string]int](t,
		ledgerCall(router, "POST", testLedgerPath+"/sync/"+plan, nil), 200)
	closed := ledgerReply[hazardDetail](t, ledgerCall(router, "GET", path, nil), 200)
	if count["count"] != 2 || closed.Status != statusClosed || closed.ClosedAt == nil ||
		closed.Version != 11 || len(closed.AllowedActions) != 0 {
		t.Fatalf("sync changed closed hazard: %+v, count %+v", closed, count)
	}
	materials := ledgerReply[hazardCollection[cases.Attachment]](t,
		ledgerCall(router, "GET", path+"/attachments", nil), 200)
	history := ledgerReply[hazardCollection[hazardEvent]](t,
		ledgerCall(router, "GET", path+"/history", nil), 200)
	if len(materials.Items) != 4 || len(history.Items) != 6 || history.Items[0].Action != actionClose ||
		history.Items[1].Action != actionRetestPass || history.Items[2].Comment != "已完成复测" ||
		history.Items[3].Action != actionReturn || history.Items[3].ToStatus != statusReturned ||
		history.Items[5].Operator != "隐患测试席位" {
		t.Fatalf("incomplete evidence or history: %+v %+v", materials, history)
	}
}

// assertEvidenceOwnership 验证只能通过真实归属路径下载且下载响应保留原始内容。
// 参数 t 为测试上下文，router 为业务路由，path 和 other 为不同隐患地址，id 为证据标识。
// 返回值 无。
func assertEvidenceOwnership(t *testing.T, router http.Handler, path, other, id string) {
	t.Helper()
	download := ledgerCall(router, "GET", path+"/attachments/"+id, nil)
	if download.Code != 200 || download.Body.String() != fixturePDF ||
		download.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unexpected file download: %d %v", download.Code, download.Header())
	}
	assertLedgerError(t, ledgerCall(router, "GET", other+"/attachments/"+id, nil), 404, "not_found")
}

// assertUploadRejected 核对不能向待审核台账上传材料且拒绝后无未登记文件。
// 参数 t 为测试上下文，router 为业务路由，path 为待审核台账，directory 为本测试材料目录。
// 返回值 无。
func assertUploadRejected(t *testing.T, router http.Handler, path, directory string) {
	t.Helper()
	assertLedgerError(t, ledgerUpload(t, router, path, kindEvidence, fixturePDF), 409, "conflict")
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 1 {
		t.Fatalf("rejected attachment left unexpected files: %v (%v)", files, err)
	}
}

// ledgerUpload 构造与管理端一致的多段证据上传请求。
// 参数 t、router 为测试环境，path 为隐患详情地址，kind、data 和 version 为材料类别、内容及可选版本。
// 返回值 上传接口的实际响应。
func ledgerUpload(t *testing.T, router http.Handler, path, kind, data string,
	version ...int64) *httptest.ResponseRecorder {
	t.Helper()
	request := ledgerUploadRequest(t, path, kind, data)
	if len(version) > 0 {
		request.Header.Set("If-Match", fmt.Sprintf(`"%d"`, version[0]))
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

// ledgerUploadRequest 构造可以附加上传条件的真实多段表单请求。
// 参数 t 为测试上下文，path 为隐患详情地址，kind 和 data 为材料类别和内容。
// 返回值 已编码表单的上传请求。
func ledgerUploadRequest(t *testing.T, path, kind, data string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("kind", kind); err != nil {
		t.Fatal(err)
	}
	file, err := form.CreateFormFile("file", "整改证据.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	base, query, hasQuery := strings.Cut(path, "?")
	url := base + "/attachments"
	if hasQuery {
		url += "?" + query
	}
	request := httptest.NewRequest("POST", url, &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	return request
}

// assertLedgerError 核对 HTTP 错误的业务编码，验证服务端流程拒绝原因。
// 参数 t 为测试上下文，response 为实际接口响应，status 和 code 为期望错误。
// 返回值 无。
func assertLedgerError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	problem := ledgerReply[struct{ Code string }](t, response, status)
	if problem.Code != code {
		t.Fatalf("unexpected error code %q, want %q", problem.Code, code)
	}
}

// TestHazardReviewRollbackAndRace 验证既有待监管复核记录仍可销号，留痕失败时回滚且竞争仅成功一次。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestHazardReviewRollbackAndRace(t *testing.T) {
	service, router := ledgerFixture(t)
	path, _, _ := syncedPaths(t, service, router)
	actions := path + "/actions"
	initial := ledgerReply[hazardDetail](t, ledgerCall(router, "GET", path, nil), 200)
	if _, err := service.db.Exec(t.Context(), `WITH retained AS (
		UPDATE hazard_items SET status='pending_review',version=2 WHERE id=$1 RETURNING id,version
	) INSERT INTO hazard_events(hazard_id,action,from_status,to_status,operator,comment,version)
	SELECT id,'submit','open','pending_review','既有运营者','上线前直接提交',version FROM retained`, initial.ID); err != nil {
		t.Fatal(err)
	}
	input := actionInput{Action: actionClose, ExpectedVersion: 2}
	assertFailedCloseUnchanged(t, service.db, router, path, input,
		"hazard_events", "action <> 'close'")
	assertFailedCloseUnchanged(t, service.db, router, path, input,
		"review_notices", "state <> 'closed'")
	var work sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		work.Go(func() { statuses <- ledgerCall(router, "POST", actions, input).Code })
	}
	work.Wait()
	first, second := <-statuses, <-statuses
	if min(first, second) != 200 || max(first, second) != 409 {
		t.Fatalf("unexpected concurrent review pair: %d %d", first, second)
	}
	history := ledgerReply[hazardCollection[hazardEvent]](t, ledgerCall(router, "GET", path+"/history", nil), 200)
	if len(history.Items) != 2 || history.Items[0].Action != actionClose {
		t.Fatalf("expected a single persisted close event: %+v", history)
	}
}

// assertFailedCloseUnchanged 人为阻断同事务中的一项写入，核对隐患状态和留痕都回滚。
// 参数 t 为测试上下文，db 为隔离库，router 为业务路由，path 和 input 为销号请求，table 和 check 为测试约束。
// 返回值 无。
func assertFailedCloseUnchanged(t *testing.T, db *pgxpool.Pool, router http.Handler, path string,
	input actionInput, table, check string) {
	t.Helper()
	if _, err := db.Exec(t.Context(), "ALTER TABLE "+table+" ADD CONSTRAINT test_fail_close CHECK ("+check+")"); err != nil {
		t.Fatal(err)
	}
	assertLedgerError(t, ledgerCall(router, "POST", path+"/actions", input), 500, "internal_error")
	detail := ledgerReply[hazardDetail](t, ledgerCall(router, "GET", path, nil), 200)
	history := ledgerReply[hazardCollection[hazardEvent]](t, ledgerCall(router, "GET", path+"/history", nil), 200)
	if detail.Status != statusPending || detail.ClosedAt != nil ||
		detail.Version != input.ExpectedVersion || len(history.Items) != 1 {
		t.Fatalf("failed close persisted changes: %+v, %+v", detail, history)
	}
	if _, err := db.Exec(t.Context(), "ALTER TABLE "+table+" DROP CONSTRAINT test_fail_close"); err != nil {
		t.Fatal(err)
	}
}

// TestHazardFiltersAndSyncRestrictions 验证期限边界、稳定分页和历史同步资格。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestHazardFiltersAndSyncRestrictions(t *testing.T) {
	service, router := ledgerFixture(t)
	_, _, plan := syncedPaths(t, service, router)
	_, err := service.db.Exec(t.Context(), `UPDATE hazard_items SET deadline=
		(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date + CASE WHEN severity='high' THEN -1 ELSE 10 END`)
	if err != nil {
		t.Fatal(err)
	}
	for due, severity := range map[string]string{"overdue": "high", "within_10_days": "low"} {
		result := ledgerReply[hazardPage](t,
			ledgerCall(router, "GET", testLedgerPath+"?due="+due, nil), 200)
		if len(result.Items) != 1 || result.Items[0].Severity != severity {
			t.Fatalf("unexpected %s deadline boundary: %+v", due, result)
		}
	}
	assertHazardPagination(t, router, plan)
	assertSyncRestrictions(t, service, router, plan)
}

// assertHazardPagination 核对按创建顺序分页以及非法条件的响应。
// 参数 t 为测试上下文，router 为业务路由，plan 为限定来源规划。
// 返回值 无。
func assertHazardPagination(t *testing.T, router http.Handler, plan string) {
	t.Helper()
	query := testLedgerPath + "?planId=" + plan + "&limit=1"
	first := ledgerReply[hazardPage](t, ledgerCall(router, "GET", query, nil), 200)
	if len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("unexpected first page: %+v", first)
	}
	second := ledgerReply[hazardPage](t, ledgerCall(router, "GET", query+"&cursor="+first.NextCursor, nil), 200)
	if len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID || second.NextCursor != "" {
		t.Fatalf("unexpected second page: %+v", second)
	}
	assertLedgerError(t, ledgerCall(router, "GET", query+"&limit=2", nil), 400, "invalid_input")
	assertLedgerError(t, ledgerCall(router, "GET", query+"&date=unused", nil), 400, "invalid_input")
}

// assertSyncRestrictions 验证同步接口只处理已批准规划且拒绝额外表单。
// 参数 t 为测试上下文，service 为隔离服务，router 为业务路由，plan 为已批准规划标识。
// 返回值 无。
func assertSyncRestrictions(t *testing.T, service *Service, router http.Handler, plan string) {
	t.Helper()
	syncPath := testLedgerPath + "/sync/"
	assertLedgerError(t, ledgerCall(router, "POST", syncPath+plan, map[string]string{}), 400, "invalid_input")
	var draftID string
	err := service.db.QueryRow(t.Context(), `INSERT INTO remediation_plans(report_id,deadline,risks)
		SELECT report_id,deadline,risks FROM remediation_plans WHERE id=$1 RETURNING id::text`, plan).Scan(&draftID)
	if err != nil {
		t.Fatal(err)
	}
	assertLedgerError(t, ledgerCall(router, "POST", syncPath+draftID, nil), 409, "plan_not_approved")
}
