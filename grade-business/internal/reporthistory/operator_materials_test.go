package reporthistory

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/cases"
)

// TestOperatorReportMaterialsScope 验证只对当前归属公开终审材料和经审核的整改要求。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestOperatorReportMaterialsScope(t *testing.T) {
	pool, mux := historyFixture(t)
	filing := assessmentTestFiling(t, pool, "交付-1", "留存单位")
	foreignFiling := assessmentTestFiling(t, pool, "交付-2", "其他单位")
	const at = "2025-01-01T08:00:00Z"
	report := insertHistoryReport(t, pool, filing, "交付-归档", "approved", "high", at)
	pending := insertHistoryReport(t, pool, filing, "交付-待审", "pending_final", "low", at)
	missing := insertHistoryReport(t, pool, filing, "交付-未留件", "approved", "low", at)
	foreign := insertHistoryReport(t, pool, foreignFiling, "交付-外部", "approved", "low", at)
	insertHistoryFile(t, pool, report, "旧正文.pdf", "report_file", "2025-01-01T08:00:00Z")
	body := insertHistoryFile(t, pool, report, "终审正文.pdf", "report_file", "2025-01-01T08:01:00Z")
	plan := insertHistoryFile(t, pool, report, "随附整改.pdf", "remediation_plan", "2025-01-01T08:01:00Z")
	insertHistoryFile(t, pool, report, "内部补充.pdf", "supporting", at)
	recordArchivedApproval(t, pool, report)
	insertHistoryPlan(t, pool, report, "approved", "已核验整改措施")
	insertHistoryPlan(t, pool, report, "draft", "内部草稿")
	scope := "?organization=" + url.QueryEscape("留存单位")
	path := operatorMaterialsTestPath(filing, report)
	delivery := readHistoryTest[map[string]any](t, mux, path+scope, 200)
	items, _ := delivery["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["id"] != body || items[1].(map[string]any)["id"] != plan {
		t.Fatalf("公开材料不符合终审范围: %v", delivery)
	}
	requirements, _ := delivery["requirements"].(map[string]any)
	if requirements["deadline"] != "2026-10-01" || len(delivery) != 2 {
		t.Fatalf("缺少审核通过的整改要求或响应携带多余字段: %v", delivery)
	}
	risks := requirements["items"].([]any)
	row := risks[0].(map[string]any)
	if len(risks) != 1 || len(row) != 4 || row["measure"] != "已核验整改措施" {
		t.Fatalf("整改要求应只携带本次报告经审批的项目: %v", risks)
	}
	unavailable := readHistoryTest[map[string]any](t, mux, operatorMaterialsTestPath(filing, missing)+scope, 200)
	if len(unavailable["items"].([]any)) != 0 || len(unavailable) != 1 {
		t.Fatalf("缺少材料应明确返回空列表: %v", unavailable)
	}
	readHistoryTest[map[string]any](t, mux, path+"?organization=其他单位", 404)
	readHistoryTest[map[string]any](t, mux, operatorMaterialsTestPath(filing, foreign)+scope, 404)
	readHistoryTest[map[string]any](t, mux, operatorMaterialsTestPath(filing, pending)+scope, 404)
	readHistoryTest[map[string]any](t, mux, path+scope+"&extra=1", 400)
}

// TestOperatorReportDownloadChecksReleasedVersion 验证真实文件、Range、归属和终审附件版本。
// 参数 t 为 PostgreSQL 隔离测试上下文，真实内容只写本次临时文件目录。
// 返回值 无。
func TestOperatorReportDownloadChecksReleasedVersion(t *testing.T) {
	pool, _ := historyFixture(t)
	dir := t.TempDir()
	storage, err := cases.New(pool, dir, "测试席位")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewWithAttachments(pool, storage, "测试席位").Register(mux)
	filing := assessmentTestFiling(t, pool, "附件-1", "交付试验单位")
	const at = "2026-01-01T08:00:00Z"
	report := insertHistoryReport(t, pool, filing, "附件-报告", "approved", "high", at)
	older := insertHistoryFile(t, pool, report, "旧正文.pdf", "report_file", at)
	kept := insertHistoryFile(t, pool, report, "终审正文.pdf", "report_file", "2026-01-02T08:00:00Z")
	late := insertHistoryFile(t, pool, report, "终审后文件.pdf", "report_file", "2026-05-05T08:00:00Z")
	internal := insertHistoryFile(t, pool, report, "内部结论.pdf", "assessment_conclusion", at)
	otherReport := insertHistoryReport(t, pool, filing, "附件-其他报告", "approved", "high", at)
	offTarget := insertHistoryFile(t, pool, otherReport, "其他报告.pdf", "report_file", at)
	recordArchivedApproval(t, pool, report)
	storePublishedTestFile(t, pool, kept, dir, "%PDF-1.4\n发布原件\n%%EOF")
	path := operatorMaterialsTestPath(filing, report)
	scope := "?organization=" + url.QueryEscape("交付试验单位")
	verifyDeliveredFile(t, mux, path+"/"+kept+scope)
	for _, id := range []string{older, late, internal, offTarget} {
		readHistoryTest[map[string]any](t, mux, path+"/"+id+scope, 404)
	}
	readHistoryTest[map[string]any](t, mux, path+"/"+kept+"?organization=其他单位", 404)
	readHistoryTest[map[string]any](t, mux, path+"/"+kept, 400)
	readHistoryTest[map[string]any](t, mux, path+"/"+kept+scope+"&organization=重复范围", 400)
}

// TestOperatorLegacyDeliveryAfterTransfer 验证无名录快照的历史材料仍按已知归属变化隔离。
// 参数 t 为实际 PostgreSQL 测试上下文。
// 返回值 无。
func TestOperatorLegacyDeliveryAfterTransfer(t *testing.T) {
	pool, mux := historyFixture(t)
	filing := assessmentTestFiling(t, pool, "转移-1", "来源单位")
	old := insertHistoryReport(t, pool, filing, "转移-旧", "approved", "high", "2025-01-01T08:00:00Z")
	oldPath := operatorMaterialsTestPath(filing, old)
	readHistoryTest[map[string]any](t, mux, oldPath+"?organization=来源单位", 200)
	if _, err := pool.Exec(t.Context(), `UPDATE business_cases SET organization_name='接收单位' WHERE id=$1`,
		filing); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO filing_applications
		(filing_id,application_type,filing_number,filing_version,reason,before_values,after_values,status,applied_at)
		VALUES($1,'change','转移-1',1,'测试已办结的单位移交','{"organizationName":"来源单位"}',
			'{"organizationName":"接收单位"}','approved','2025-06-01')`, filing); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"?organization=来源单位", "?organization=接收单位"} {
		readHistoryTest[map[string]any](t, mux, oldPath+scope, 404)
	}
	newer := insertHistoryReport(t, pool, filing, "转移-新", "approved", "low", "2026-01-01T08:00:00Z")
	readHistoryTest[map[string]any](t, mux, operatorMaterialsTestPath(filing, newer)+"?organization=接收单位", 200)
}

// storePublishedTestFile 将合成材料写入本次临时目录并关联测试附件记录。
// 参数 t、pool 为隔离测试上下文，id、dir、content 为材料标识、临时目录和测试内容。
// 返回值 无。
func storePublishedTestFile(t *testing.T, pool *pgxpool.Pool, id, dir, content string) {
	t.Helper()
	key := strings.Repeat("a", 48)
	if err := os.WriteFile(filepath.Join(dir, key), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE case_attachments SET file_key=$2,size_bytes=$3 WHERE id=$1",
		id, key, len(content)); err != nil {
		t.Fatal(err)
	}
}

// verifyDeliveredFile 使用真实 HTTP 处理器验证部分内容、下载类型和安全响应头。
// 参数 t、mux、path 为隔离测试上下文、业务路由及已核对的下载地址。
// 返回值 无。
func verifyDeliveredFile(t *testing.T, mux http.Handler, path string) {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Range", "bytes=0-3")
	mux.ServeHTTP(response, request)
	if response.Code != 206 || response.Body.String() != "%PDF" ||
		response.Header().Get("X-Content-Type-Options") != "nosniff" ||
		response.Header().Get("Cache-Control") != "no-store" ||
		!strings.HasPrefix(response.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("归档下载响应: %d %v %q", response.Code, response.Header(), response.Body.String())
	}
}

// operatorMaterialsTestPath 构造测评归档材料读取路由。
// 参数 filing、report 分别为所属备案和报告标识。
// 返回值 查询范围前的同源路由。
func operatorMaterialsTestPath(filing, report string) string {
	return operatorHistoryTestPath + "/" + filing + "/reports/" + report + "/materials"
}

// insertHistoryPlan 为归档报告创建独立的整改审批测试记录。
// 参数 t 和 pool 为隔离测试上下文，report、status、measure 为关联、状态和措施。
// 返回值 无。
func insertHistoryPlan(t *testing.T, pool *pgxpool.Pool, report, status, measure string) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `INSERT INTO remediation_plans
		(report_id,deadline,risks,status,submission,submitted_at)
		VALUES ($1,'2026-10-01',jsonb_build_array(jsonb_build_object(
			'risk','high','description','待处理事项','measure',$3::text,
			'responsibility','内部负责人','deadline','2026-09-30'
		)),$2,CASE WHEN $2='draft' THEN 0 ELSE 1 END,
		CASE WHEN $2='draft' THEN NULL ELSE clock_timestamp() END)`, report, status, measure)
	if err != nil {
		t.Fatal(err)
	}
}
