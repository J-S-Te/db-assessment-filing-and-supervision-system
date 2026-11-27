package reporthistory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/migrations"
)

const historyReportsPath = "/history-reports"
const historyComparisonPath = "/history-comparison"

// TestHistoryRejectsInvalidFilters 验证无效请求在访问数据库前被拒绝。
// 参数 t 为测试上下文。
// 返回值 无。
func TestHistoryRejectsInvalidFilters(t *testing.T) {
	mux := http.NewServeMux()
	New(nil).Register(mux)
	base := "/api/v1/reports/00000000-0000-4000-8000-000000000001"
	for _, suffix := range []string{historyReportsPath + "?limit=0", historyReportsPath + "?cursor=bad",
		historyComparisonPath + "?from=invalid", historyComparisonPath + "?organization=other"} {
		readHistoryTest[map[string]string](t, mux, base+suffix, 400)
	}
}

// TestHistoryListsOnlyApprovedSameFiling 检查从待审报告打开归档时的范围及排序。
// 参数 t 为测试上下文，仅使用隔离 PostgreSQL schema。
// 返回值 无。
func TestHistoryListsOnlyApprovedSameFiling(t *testing.T) {
	pool, mux := historyFixture(t)
	filing := insertHistoryFiling(t, pool, "BA-归档")
	foreign := insertHistoryFiling(t, pool, "BA-其他")
	anchor := insertHistoryReport(t, pool, filing, "BG-待审", "pending_final", "medium", "2026-05-03")
	newer := insertHistoryReport(t, pool, filing, "BG-新", "approved", "medium", "2026-05-02")
	older := insertHistoryReport(t, pool, filing, "BG-旧", "approved", "low", "2026-05-01")
	insertHistoryReport(t, pool, foreign, "BG-外", "approved", "high", "2026-05-04")
	recordArchivedApproval(t, pool, newer)
	attachment := insertHistoryFile(t, pool, newer, "规范报告.docx", "report_file", "2026-05-01")
	insertHistoryFile(t, pool, newer, "补充说明.pdf", "supporting", "2026-05-03")
	path := "/api/v1/reports/" + anchor + historyReportsPath
	type archivePage struct {
		Items []struct {
			ID          string  `json:"id"`
			Number      string  `json:"number"`
			SubmittedAt *string `json:"submittedAt"`
			ApprovedAt  *string `json:"approvedAt"`
			ReportFile  *struct {
				ID string `json:"id"`
			} `json:"reportFile"`
		} `json:"items"`
		NextCursor string `json:"nextCursor"`
	}
	first := readHistoryTest[archivePage](t, mux, path+"?limit=1", 200)
	if len(first.Items) != 1 || first.Items[0].ID != newer || first.Items[0].Number != "BG-新" ||
		first.Items[0].ReportFile == nil || first.Items[0].ReportFile.ID != attachment || first.NextCursor == "" ||
		first.Items[0].SubmittedAt != nil || first.Items[0].ApprovedAt == nil {
		t.Fatalf("归档结果不符合实际报告及附件: %+v", first)
	}
	next := readHistoryTest[archivePage](t, mux, path+"?limit=1&cursor="+url.QueryEscape(first.NextCursor), 200)
	if len(next.Items) != 1 || next.Items[0].ID != older || next.Items[0].ReportFile != nil || next.NextCursor != "" {
		t.Fatalf("历史归档范围或排序错误: %+v", next)
	}
}

// recordArchivedApproval 验证归档读取真实终审时间和旧数据缺少提交时间的兼容行为。
// 参数 t 为测试上下文，pool 为隔离库，report 为归档报告标识。
// 返回值 无。
func recordArchivedApproval(t *testing.T, pool *pgxpool.Pool, report string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), "UPDATE business_cases SET submitted_at=NULL WHERE id=$1", report); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO case_review_events
		(case_id,action,from_status,to_status,operator,version,occurred_at)
		VALUES ($1,'approve','pending_final','approved','隔离审核员',4,'2026-05-03T01:00:00Z')`, report); err != nil {
		t.Fatal(err)
	}
}

// TestComparisonUsesConfirmedCurrentEvidence 检查对比实际校对值、同备案限制及未确认正文的表现。
// 参数 t 为测试上下文，仅使用隔离 PostgreSQL schema。
// 返回值 无。
func TestComparisonUsesConfirmedCurrentEvidence(t *testing.T) {
	pool, mux := historyFixture(t)
	filing := insertHistoryFiling(t, pool, "BA-对比")
	other := insertHistoryFiling(t, pool, "BA-其他")
	older := insertHistoryReport(t, pool, filing, "BG-前期", "approved", "high", "2026-05-01")
	newer := insertHistoryReport(t, pool, filing, "BG-后期", "approved", "medium", "2026-05-02")
	pending := insertHistoryReport(t, pool, filing, "BG-草稿", "draft", "medium", "2026-05-03")
	foreign := insertHistoryReport(t, pool, other, "BG-外", "approved", "high", "2026-05-04")
	oldFile := insertHistoryFile(t, pool, older, "前期报告.docx", "report_file", "2026-05-01")
	newFile := insertHistoryFile(t, pool, newer, "后期报告.docx", "report_file", "2026-05-02")
	addHistoryExtraction(t, pool, oldFile, `[{"key":"findings","original":"旧记录\n重复记录","startParagraph":4,"endParagraph":6},
		{"key":"assets","original":"归档系统","startParagraph":1,"endParagraph":1}]`, true)
	addHistoryExtraction(t, pool, newFile, `[{"key":"findings","original":"抽取原值","value":"重复记录\n新增记录","startParagraph":3,"endParagraph":5},
		{"key":"assets","original":"归档系统","startParagraph":1,"endParagraph":1},
		{"key":"recommendations","original":"已截断的建议","startParagraph":7,"endParagraph":10,"truncated":true}]`, true)
	path := "/api/v1/reports/" + pending + historyComparisonPath
	assertComparison(t, mux, path, older, newer, newFile)
	for _, id := range []string{pending, foreign} {
		readHistoryTest[map[string]string](t, mux, path+"?from="+older+"&to="+id, 404)
	}
	readHistoryTest[map[string]string](t, mux, path+"?from="+newer+"&to="+older, 400)
	latest := insertHistoryFile(t, pool, newer, "后期补传报告.docx", "report_file", "2026-05-03")
	addHistoryExtraction(t, pool, latest, `[{"key":"findings","original":"未确认内容","startParagraph":1,"endParagraph":1}]`, false)
	result := readHistoryTest[map[string]json.RawMessage](t, mux, path+"?from="+older+"&to="+newer, 200)
	var period map[string]json.RawMessage
	if json.Unmarshal(result["to"], &period) != nil || result["findings"] != nil || period["evidence"] != nil {
		t.Fatalf("未校对的新正文错误地沿用了旧附件证据: %s", result["to"])
	}
}

// assertComparison 核对已确认对比中的结构化字段、证据和重复文本。
// 参数 t 为测试上下文，mux 为实际应用路由，path 与旧新报告及附件标识构成读取范围。
// 返回值 无。
func assertComparison(t *testing.T, mux http.Handler, path, older, newer, attachment string) {
	t.Helper()
	result := readHistoryTest[struct {
		RiskChange string `json:"riskChange"`
		To         struct {
			Evidence struct {
				AttachmentID string `json:"attachmentId"`
			} `json:"evidence"`
		} `json:"to"`
		Fields []struct {
			Key, State string
			To         *struct{ Value string }
		} `json:"fields"`
		Findings struct{ OnlyFrom, OnlyTo, Common []string } `json:"findings"`
	}](t, mux, path+"?from="+older+"&to="+newer, 200)
	if result.RiskChange != "decreased" || result.To.Evidence.AttachmentID != attachment ||
		!slices.Equal(result.Findings.OnlyFrom, []string{"旧记录"}) ||
		!slices.Equal(result.Findings.OnlyTo, []string{"新增记录"}) ||
		!slices.Equal(result.Findings.Common, []string{"重复记录"}) {
		t.Fatalf("对比结果没有反映实际已校对文本: %+v", result)
	}
	if len(result.Fields) != 4 || result.Fields[0].State != "same" || result.Fields[1].State != "changed" ||
		result.Fields[1].To == nil || result.Fields[1].To.Value != "重复记录\n新增记录" ||
		result.Fields[2].State != "insufficient" || result.Fields[3].State != "insufficient" {
		t.Fatalf("字段级证据或实际状态不符: %+v", result.Fields)
	}
}

// readHistoryTest 验证 HTTP 状态并解码公共报告业务响应。
// 参数 t 为测试上下文，mux 为实际路由，path 和 status 为请求地址和期望状态。
// 返回值 解码后的业务数据。
func readHistoryTest[T any](t *testing.T, mux http.Handler, path string, status int) T {
	t.Helper()
	item := httptest.NewRecorder()
	mux.ServeHTTP(item, httptest.NewRequest(http.MethodGet, path, nil))
	if item.Code != status {
		t.Fatalf("读取 %s 返回 %d，期望 %d: %s", path, item.Code, status, item.Body)
	}
	var out T
	if err := json.Unmarshal(item.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// historyFixture 迁移隔离的 PostgreSQL schema 并挂载实际报告对比路由。
// 参数 t 为测试上下文，测试库由明确的 GRADE_BUSINESS_TEST_DSN 提供。
// 返回值 独立连接池和 HTTP 路由。
func historyFixture(t *testing.T) (*pgxpool.Pool, *http.ServeMux) {
	t.Helper()
	dsn := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if dsn == "" {
		t.Skip("GRADE_BUSINESS_TEST_DSN 未配置")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil || config.ConnConfig.Database != "grade_business_test" {
		t.Fatal("仅使用隔离 grade_business_test 数据库")
	}
	pool := createHistorySchema(t, config)
	if err := migrations.Apply(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	New(pool).Register(mux)
	return pool, mux
}

// createHistorySchema 建立独立 schema 并登记本次创建资源的清理。
// 参数 t 为测试上下文，config 为已验证的测试数据库配置。
// 返回值 仅在当前独立 schema 读写的连接池。
func createHistorySchema(t *testing.T, config *pgxpool.Config) *pgxpool.Pool {
	t.Helper()
	admin, err := pgxpool.NewWithConfig(t.Context(), config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("report_history_%d_%d", os.Getpid(), time.Now().UnixNano())
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
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// insertHistoryFiling 登记测试备案，多个备案复用同一系统名称以检验真正归属。
// 参数 t 为测试上下文，pool 为独立测试连接，number 为备案编号。
// 返回值 已持久化的备案标识。
func insertHistoryFiling(t *testing.T, pool *pgxpool.Pool, number string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(), `INSERT INTO business_cases (kind,number,system_name,assessment_org,level,status)
		VALUES ('filing',$1,'同名系统','隔离机构','2','approved') RETURNING id::text`, number).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// insertHistoryReport 按测试指定的实际时序和状态登记报告。
// 参数 t 为测试上下文，pool、filing、number、status、risk、submitted 分别指定数据库和报告字段。
// 返回值 已持久化的报告标识。
func insertHistoryReport(t *testing.T, pool *pgxpool.Pool, filing, number, status, risk, submitted string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(), `INSERT INTO business_cases
		(kind,filing_id,number,assessment_org,status,risk,created_at,submitted_at)
		VALUES ('report',$1,$2,'隔离机构',$3,$4,$5::text::timestamptz,$5::text::timestamptz) RETURNING id::text`,
		filing, number, status, risk, submitted).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// insertHistoryFile 创建指定归属的测试附件元数据。
// 参数 t 为测试上下文，pool 为隔离库，report、file、kind、uploaded 为归属与材料字段。
// 返回值 已登记附件标识。
func insertHistoryFile(t *testing.T, pool *pgxpool.Pool, report, file, kind, uploaded string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(), `INSERT INTO case_attachments (case_id,kind,file_name,file_key,size_bytes,uploaded_at)
		VALUES ($1,$2,$3,gen_random_uuid()::text,20,$4::text::timestamptz) RETURNING id::text`,
		report, kind, file, uploaded).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// addHistoryExtraction 写入业务提取接口采用的结构化 JSON 和校对时间。
// 参数 t 为测试上下文，pool、attachment、fields、confirmed 为存储、数据和是否已校对。
// 返回值 无。
func addHistoryExtraction(t *testing.T, pool *pgxpool.Pool, attachment, fields string, confirmed bool) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `INSERT INTO report_extractions
		(attachment_id,fields,version,confirmed_at,confirmed_by)
		VALUES ($1,$2::jsonb,CASE WHEN $3 THEN 2 ELSE 1 END,
			CASE WHEN $3 THEN '2026-05-03 08:00:00Z'::timestamptz END,CASE WHEN $3 THEN '隔离校对人' END)`,
		attachment, fields, confirmed); err != nil {
		t.Fatal(err)
	}
}
