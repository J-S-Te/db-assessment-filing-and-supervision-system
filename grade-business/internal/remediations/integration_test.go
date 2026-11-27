package remediations

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

const rootURL = "/api/v1/remediations"
const samplePDF = "%PDF-1.7\n% local remediation fixture\n%%EOF"

// TestRemediationWorkflow 验证材料齐全方可提交，退回重提和期限审核真实留痕。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestRemediationWorkflow(t *testing.T) {
	db, router := remediationFixture(t)
	report, _ := insertReports(t, db)
	input := newPlanInput(report)
	created := response[mutation](t, call(router, "POST", rootURL, input), 201)
	path := rootURL + "/" + created.ID
	response[map[string]string](t, call(router, "POST", path+"/submit", submitInput{1}), 400)
	checkAttachments(t, router, path, input)
	submitted := response[mutation](t, call(router, "POST", path+"/submit", submitInput{4}), 200)
	if submitted.Status != pendingReview || submitted.Version != 5 {
		t.Fatalf("unexpected submission %+v", submitted)
	}
	returnedRecord := response[mutation](t, call(router, "POST", path+"/review-actions", reviewInput{
		Decision: "return_for_correction", Deadline: input.Deadline,
		Comment: "待完善责任人", Reason: "补充分工", ExpectedVersion: 5,
	}), 200)
	if returnedRecord.Status != returned {
		t.Fatalf("unexpected return %+v", returnedRecord)
	}
	response[mutation](t, call(router, "POST", path+"/submit", submitInput{6}), 200)
	assertConcurrentApproval(t, router, path, 7)
	checkReviewHistory(t, router, path)
}

// TestRemediationFiltersAndEligibility 验证有效报告、日期过滤、分页和空统计。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestRemediationFiltersAndEligibility(t *testing.T) {
	db, router := remediationFixture(t)
	report, unapproved := insertReports(t, db)
	empty := response[statistics](t, call(router, "GET", rootURL+"/statistics", nil), 200)
	if empty.FirstPassRate != nil || empty.Pending != 0 {
		t.Fatalf("unexpected empty statistics %+v", empty)
	}
	reports := response[collection[eligibleReport]](t,
		call(router, "GET", rootURL+"/eligible-reports?q=系统", nil), 200)
	if len(reports.Items) != 1 || reports.Items[0].ID != report {
		t.Fatalf("ineligible report was offered %+v", reports)
	}
	response[map[string]string](t, call(router, "POST", rootURL, newPlanInput(unapproved)), 400)
	first := response[mutation](t, call(router, "POST", rootURL, newPlanInput(report)), 201)
	second := response[mutation](t, call(router, "POST", rootURL, newPlanInput(report)), 201)
	testPages(t, router, first.ID, second.ID)
	if _, err := db.Exec(t.Context(), `UPDATE business_cases SET cancelled_at=now() WHERE kind='filing'`); err != nil {
		t.Fatal(err)
	}
	response[map[string]string](t, call(router, "POST", rootURL, newPlanInput(report)), 400)
	got := response[detail](t, call(router, "GET", rootURL+"/"+first.ID, nil), 200)
	if got.ReportID != report || got.OrganizationName != "测试单位" {
		t.Fatalf("historical detail not readable %+v", got)
	}
}

// TestRemediationEditAndAuditRollback 验证编辑并发保护以及审核状态和留痕共同回滚。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestRemediationEditAndAuditRollback(t *testing.T) {
	db, router := remediationFixture(t)
	report, _ := insertReports(t, db)
	input := newPlanInput(report)
	created := response[mutation](t, call(router, "POST", rootURL, input), 201)
	path := rootURL + "/" + created.ID
	input.Risks[0].Responsibility, input.ExpectedVersion = "合规组", 1
	updated := response[mutation](t, call(router, "PATCH", path, input), 200)
	if updated.Version != 2 {
		t.Fatalf("edit produced incorrect version %+v", updated)
	}
	input.Risks[0].Responsibility = "过期写入"
	response[map[string]string](t, call(router, "PATCH", path, input), 409)
	for _, kind := range []string{"plan", "responsibility", "schedule"} {
		response[cases.Attachment](t, upload(router, path, kind, samplePDF, report), 201)
	}
	response[mutation](t, call(router, "POST", path+"/submit", submitInput{5}), 200)
	if _, err := db.Exec(t.Context(), `ALTER TABLE remediation_review_events
		ADD CONSTRAINT test_reject_approve CHECK (action <> 'approve')`); err != nil {
		t.Fatal(err)
	}
	response[map[string]string](t, call(router, "POST", path+"/review-actions", reviewInput{
		Decision: "approve", Deadline: businessDate(9), Comment: "校验事务", ExpectedVersion: 6,
	}), 500)
	got := response[detail](t, call(router, "GET", path, nil), 200)
	events := response[collection[reviewEvent]](t, call(router, "GET", path+"/review-history", nil), 200)
	if got.Status != pendingReview || got.Version != 6 || got.Deadline != businessDate(7) ||
		got.Risks[0].Responsibility != "合规组" || len(events.Items) != 1 || events.Items[0].Action != "submit" {
		t.Fatalf("failed approval changed the persisted plan or its history: %+v, %+v", got, events)
	}
}

// TestReportCannotChangeAfterMaterialUpload 验证已登记材料不会沿用到另一测评报告。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestReportCannotChangeAfterMaterialUpload(t *testing.T) {
	directory := t.TempDir()
	db, router := remediationFixtureAt(t, directory)
	original, _ := insertReports(t, db)
	var alternate string
	err := db.QueryRow(t.Context(), `INSERT INTO business_cases(kind,number,assessment_org,filing_id,risk,status)
		SELECT kind,'BG-ALT',assessment_org,filing_id,risk,status FROM business_cases WHERE id=$1 RETURNING id`,
		original).Scan(&alternate)
	if err != nil {
		t.Fatal(err)
	}
	created := response[mutation](t, call(router, "POST", rootURL, newPlanInput(original)), 201)
	path := rootURL + "/" + created.ID
	withoutFiles := response[detail](t, call(router, "GET", path, nil), 200)
	changed := newPlanInput(alternate)
	changed.ExpectedVersion = 1
	response[mutation](t, call(router, "PATCH", path, changed), 200)
	response[map[string]string](t, upload(router, path, "plan", samplePDF, ""), 400)
	stale := response[map[string]string](t, upload(router, path, "plan", samplePDF, original), 409)
	files := response[collection[cases.Attachment]](t, call(router, "GET", path+"/attachments", nil), 200)
	disk, err := os.ReadDir(directory)
	if stale["code"] != "report_changed" || len(files.Items) != 0 || err != nil || len(disk) != 0 {
		t.Fatalf("stale request kept material: %+v, %+v, %+v, %v", stale, files, disk, err)
	}
	response[cases.Attachment](t, upload(router, path, "plan", samplePDF, alternate), 201)
	blocked := newPlanInput(original)
	blocked.ExpectedVersion = 3
	failure := response[map[string]string](t, call(router, "PATCH", path, blocked), 409)
	if failure["code"] != "report_change_blocked" {
		t.Fatalf("unexpected error: %v", failure)
	}
	got := response[detail](t, call(router, "GET", path, nil), 200)
	if !slices.Contains(withoutFiles.AllowedActions, "change_report") ||
		slices.Contains(got.AllowedActions, "change_report") || got.ReportID != alternate || got.Version != 3 {
		t.Fatalf("incorrect report permissions or persisted data: %+v, %+v", withoutFiles, got)
	}
	changed.ExpectedVersion = 3
	changed.Risks[0].Responsibility = "改正后的负责组"
	response[mutation](t, call(router, "PATCH", path, changed), 200)
}

// testPages 核对筛选后的页序和稳定去重。
// 参数 t 为测试上下文，router 为实际路由，first 和 second 为两条测试记录标识。
// 返回值 无。
func testPages(t *testing.T, router http.Handler, first, second string) {
	t.Helper()
	query := rootURL + "?limit=1&q=BA-QA&status=draft&due=within_10_days"
	pageOne := response[page](t, call(router, "GET", query, nil), 200)
	if len(pageOne.Items) != 1 || pageOne.Items[0].ID != second || pageOne.NextCursor == "" {
		t.Fatalf("unexpected filtered first page %+v", pageOne)
	}
	pageTwo := response[page](t, call(router, "GET", query+"&cursor="+pageOne.NextCursor, nil), 200)
	if len(pageTwo.Items) != 1 || pageTwo.Items[0].ID != first || pageTwo.NextCursor != "" {
		t.Fatalf("unexpected filtered second page %+v", pageTwo)
	}
}

// checkAttachments 核对文件格式限制、三个材料类别及下载归属。
// 参数 t 为测试上下文，router 为实际路由，path 为计划路径，input 为关联对象。
// 返回值 无。
func checkAttachments(t *testing.T, router http.Handler, path string, input planInput) {
	t.Helper()
	response[map[string]string](t, upload(router, path, "plan", "伪造材料", input.ReportID), 400)
	first := response[cases.Attachment](t, upload(router, path, "plan", samplePDF, input.ReportID), 201)
	response[cases.Attachment](t, upload(router, path, "responsibility", samplePDF, input.ReportID), 201)
	response[cases.Attachment](t, upload(router, path, "schedule", samplePDF, input.ReportID), 201)
	other := response[mutation](t, call(router, "POST", rootURL, input), 201)
	response[map[string]string](t,
		call(router, "GET", rootURL+"/"+other.ID+"/attachments/"+first.ID, nil), 404)
	download := call(router, "GET", path+"/attachments/"+first.ID, nil)
	if download.Code != 200 || download.Body.String() != samplePDF ||
		!strings.HasPrefix(download.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("unexpected download %d; %q", download.Code, download.Header())
	}
}

// assertConcurrentApproval 确保竞争审核只推进一次业务版本。
// 参数 t 为测试上下文，router 为真实路由，path 为计划，version 为共同的预期版本。
// 返回值 无。
func assertConcurrentApproval(t *testing.T, router http.Handler, path string, version int64) {
	t.Helper()
	early := reviewInput{Decision: "approve", Deadline: "2024-01-01", Comment: "确认通过",
		ExpectedVersion: version}
	response[map[string]string](t, call(router, "POST", path+"/review-actions", early), 400)
	early.Deadline = businessDate(8)
	var runners sync.WaitGroup
	results := make(chan int, 2)
	for range 2 {
		runners.Go(func() {
			results <- call(router, "POST", path+"/review-actions", early).Code
		})
	}
	runners.Wait()
	total := map[int]int{}
	total[<-results]++
	total[<-results]++
	if total[200] != 1 || total[409] != 1 {
		t.Fatalf("unexpected competing review status %v", total)
	}
}

// checkReviewHistory 核对补正、重提、并发审核和即时统计.
// 参数 t 为测试上下文，router 为真实路由，path 为计划路径。
// 返回值 无。
func checkReviewHistory(t *testing.T, router http.Handler, path string) {
	t.Helper()
	got := response[detail](t, call(router, "GET", path, nil), 200)
	if got.Status != approved || got.Submission != 2 || got.Deadline != businessDate(8) ||
		got.RiskCounts.High != 1 || got.Version != 8 {
		t.Fatalf("unexpected approved detail %+v", got)
	}
	h := response[collection[reviewEvent]](t, call(router, "GET", path+"/review-history", nil), 200)
	if len(h.Items) != 4 || h.Items[0].Action != "approve" || h.Items[2].Reason != "补充分工" ||
		h.Items[2].Operator != "测试整改审核席位" || h.Items[0].Deadline != businessDate(8) {
		t.Fatalf("unexpected events %+v", h.Items)
	}
	stats := response[statistics](t, call(router, "GET", rootURL+"/statistics", nil), 200)
	if stats.FirstPassRate == nil || *stats.FirstPassRate != 0 || stats.DueWithin10Days != 1 ||
		stats.Pending != 0 || stats.Returned != 0 {
		t.Fatalf("unexpected reviewed statistics %+v", stats)
	}
}

// businessDate 生成与业务统计时区一致的测试日期。
// 参数 offset 为自今天起的偏移日数。
// 返回值 公历日期字符串。
func businessDate(offset int) string {
	businessZone := time.FixedZone("Asia/Shanghai", 8*60*60)
	return time.Now().In(businessZone).AddDate(0, 0, offset).Format(dateLayout)
}

// newPlanInput 生成带有完整风控措施和所有必要录入内容的测试表单。
// 参数 report 为该计划关联的测评报告标识。
// 返回值 测试计划输入。
func newPlanInput(report string) planInput {
	return planInput{ReportID: report, Deadline: businessDate(7), Risks: []riskItem{{
		Risk: "high", Description: "定期审核权限", Measure: "复核最小权限",
		Responsibility: "运维组", Deadline: businessDate(6),
	}}}
}

// call 通过真正的 HTTP 栈发出测试 JSON 请求。
// 参数 handler 为业务路由，method 和 path 为请求条件，body 为可选请求体。
// 返回值 已记录的 HTTP 响应。
func call(handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	var encoded []byte
	if body != nil {
		encoded, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, req)
	return out
}

// response 校验 HTTP 状态并读取约定结构。
// 参数 t 为测试上下文，record 为响应，status 为预期 HTTP 状态。
// 返回值 实际响应中可解析的业务数据。
func response[T any](t *testing.T, record *httptest.ResponseRecorder, status int) T {
	t.Helper()
	var result T
	if record.Code != status {
		t.Fatalf("HTTP %d, expected %d: %s", record.Code, status, record.Body.String())
	}
	if err := json.Unmarshal(record.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// upload 经真实 multipart 业务路由提交流程附件。
// 参数 router 为真实路由，path 可含企业查询，kind、body 为材料信息，reportID 为页面所见的报告。
// 返回值 已记录的 HTTP 响应。
func upload(router http.Handler, path, kind, body, reportID string) *httptest.ResponseRecorder {
	var encoded bytes.Buffer
	writer := multipart.NewWriter(&encoded)
	_ = writer.WriteField("kind", kind)
	if reportID != "" {
		_ = writer.WriteField("reportId", reportID)
	}
	part, _ := writer.CreateFormFile("file", "规划材料.pdf")
	_, _ = part.Write([]byte(body))
	_ = writer.Close()
	base, query, hasQuery := strings.Cut(path, "?")
	url := base + "/attachments"
	if hasQuery {
		url += "?" + query
	}
	req := httptest.NewRequest("POST", url, &encoded)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	out := httptest.NewRecorder()
	router.ServeHTTP(out, req)
	return out
}

// remediationFixture 创建单独的可清理 schema、材料目录和 HTTP 路由。
// 参数 t 为测试上下文，实体测试必须从显式 DSN 注入隔离数据库。
// 返回值 整改业务数据库连接和真实路由。
func remediationFixture(t *testing.T) (*pgxpool.Pool, http.Handler) {
	t.Helper()
	return remediationFixtureAt(t, t.TempDir())
}

// remediationFixtureAt 在隔离数据库和可检查的目录中启动真实业务路由。
// 参数 t 为测试上下文，directory 为本用例的独立材料目录。
// 返回值 业务数据库连接和实际 HTTP 处理器。
func remediationFixtureAt(t *testing.T, directory string) (*pgxpool.Pool, http.Handler) {
	t.Helper()
	db := isolatedDB(t, true)
	return db, remediationRouter(t, db, directory)
}

// remediationRouter 在隔离数据库和材料目录上建立同一套监管与运营者路由。
// 参数 t 为测试上下文，db 与 directory 为独立连接池和材料目录。
// 返回值 实际 HTTP 业务路由。
func remediationRouter(t *testing.T, db *pgxpool.Pool, directory string) http.Handler {
	t.Helper()
	records, err := cases.New(db, directory, "测试整改审核席位")
	if err != nil {
		t.Fatal(err)
	}
	router := http.NewServeMux()
	service := New(db, records, "测试整改审核席位")
	service.Register(router)
	service.RegisterOperator(router)
	return router
}

// isolatedDB 仅在明确的业务测试库中创建和迁移独立数据模式。
// 参数 t 为测试上下文，withSlots 控制是否已经应用在审占位迁移。
// 返回值 已迁移的测试连接池。
func isolatedDB(t *testing.T, withSlots bool) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if dsn == "" {
		t.Skip("GRADE_BUSINESS_TEST_DSN not set")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil || config.ConnConfig.Database != "grade_business_test" {
		t.Fatal("requires explicitly isolated grade_business_test database")
	}
	admin, err := pgxpool.NewWithConfig(t.Context(), config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("remediations_%d", time.Now().UnixNano())
	name := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+name+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	return migrationPool(t, config, schema, withSlots)
}

// migrationPool 应用关联备案和整改实际迁移文件。
// 参数 t 为测试上下文，config 为隔离配置，schema 为数据模式名，withSlots 为占位迁移开关。
// 返回值 迁移完成的连接池。
func migrationPool(t *testing.T, config *pgxpool.Config, schema string, withSlots bool) *pgxpool.Pool {
	t.Helper()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if !withSlots {
		// 升级专项验证先写入旧数据，再单独实际执行 021，其他结构按当前服务迁移。
		if _, err := pool.Exec(t.Context(), `CREATE TABLE schema_migrations (
			name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());
			INSERT INTO schema_migrations(name) VALUES('021_remediation_in_review.sql')`); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrations.Apply(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

// applyRemediationMigration 执行真实的 SQL 迁移以验证兼容行为。
// 参数 t 为测试上下文，pool 为隔离库，name 为迁移脚本名。
// 返回值 无。
func applyRemediationMigration(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	file, err := os.ReadFile("../../migrations/" + name + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(file)); err != nil {
		t.Fatal(err)
	}
}

// insertReports 准备已通过和未通过的关联报告。
// 参数 t 为测试上下文，db 为仅供测试的数据库连接。
// 返回值 已通过和未通过报告的标识。
func insertReports(t *testing.T, db *pgxpool.Pool) (string, string) {
	t.Helper()
	var filing, valid, invalid string
	err := db.QueryRow(t.Context(), `INSERT INTO business_cases(kind,number,system_name,
		organization_name,assessment_org,level,status)
		VALUES('filing','BA-QA','测试系统','测试单位','测试机构','3','approved') RETURNING id`).Scan(&filing)
	if err != nil {
		t.Fatal(err)
	}
	query := `INSERT INTO business_cases(kind,number,assessment_org,filing_id,risk,status)
		VALUES('report',$1,'测试机构',$2,'high',$3) RETURNING id`
	if err := db.QueryRow(t.Context(), query, "BG-QA", filing, "approved").Scan(&valid); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(t.Context(), query, "BG-DRAFT", filing, "draft").Scan(&invalid); err != nil {
		t.Fatal(err)
	}
	return valid, invalid
}
