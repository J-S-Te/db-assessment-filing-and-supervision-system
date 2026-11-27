package audittrail

import (
	"encoding/csv"
	"mime"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// TestAuditExportRejectsPagination 核对导出只接受业务筛选字段。
// 参数 t 为独立 HTTP 测试上下文。
// 返回值 无。
func TestAuditExportRejectsPagination(t *testing.T) {
	router := http.NewServeMux()
	New(nil).Register(router)
	for _, query := range []string{"cursor=", "limit=20", "resource=users", "q=one&q=two"} {
		result := readAuditTest[map[string]string](t, router, auditEndpoint+"/export?"+query, 400)
		if result["code"] != "invalid_input" {
			t.Errorf("导出拒绝无效条件 %s 的响应 = %v", query, result)
		}
	}
}

// TestAuditExportMatchesSearchAndProtectsCells 核对导出已筛选的完整日志并保护文本单元格。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestAuditExportMatchesSearchAndProtectsCells(t *testing.T) {
	pool := auditFixture(t)
	if _, err := pool.Exec(t.Context(), auditBusinessFixtures); err != nil {
		t.Fatal(err)
	}
	var publicationID string
	if err := pool.QueryRow(t.Context(), `SELECT id FROM local_publications`).Scan(&publicationID); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(t.Context(), `INSERT INTO publication_events
		(publication_id,version,action,status,category,title,publisher,body,pinned,operator,occurred_at)
		SELECT $1,i,'edit','published','announcement','＝test 公告 '||i,'内部发布单位',
		       '私有公告内容',false,'@编辑人员','2026-06-01T02:00:00Z'::timestamptz + i*interval '1 second'
		FROM generate_series(2,22) i`, publicationID)
	if err != nil {
		t.Fatal(err)
	}
	router := http.NewServeMux()
	New(pool).Register(router)
	rows := readAuditCSV(t, router, auditEndpoint+"/export?resource=publications&q=TEST")
	header := []string{"操作人", "操作", "业务类型", "业务对象", "对象编号", "发生时间"}
	first := []string{"'@编辑人员", "编辑内容", "信息发布", "'＝test 公告 22", publicationID, "2026-06-01 10:00:22"}
	if len(rows) != 22 || !slices.Equal(rows[0], header) || !slices.Equal(rows[1], first) {
		t.Fatalf("按条件导出 = %q，期望表头、首行 %q 和共 21 条业务记录", rows, first)
	}
	empty := readAuditCSV(t, router, auditEndpoint+"/export?resource=filings&q=TEST")
	if len(empty) != 1 || !slices.Equal(empty[0], header) {
		t.Fatalf("无匹配记录时应只导出表头，得到 %q", empty)
	}
}

// TestAuditExportRejectsOversizedResults 核对超过最大匹配行数时完整拒绝下载。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestAuditExportRejectsOversizedResults(t *testing.T) {
	pool := auditFixture(t)
	_, err := pool.Exec(t.Context(), `INSERT INTO workflow_calendar_events
		(request_id,day,version,mode,reason,operator,before_is_workday,before_override,is_workday,is_override)
		SELECT gen_random_uuid(),DATE '2026-11-27',i,'workday','补班','管理员',false,false,true,true
		FROM generate_series(1,10001) i`)
	if err != nil {
		t.Fatal(err)
	}
	router := http.NewServeMux()
	New(pool).Register(router)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, auditEndpoint+"/export", nil))
	if response.Code != http.StatusUnprocessableEntity || response.Header().Get("Content-Disposition") != "" ||
		!strings.Contains(response.Body.String(), `"code":"export_limit_exceeded"`) {
		t.Fatalf("批量导出超限得到 %d %v %q", response.Code, response.Header(), response.Body.String())
	}
}

// readAuditCSV 核对下载响应的格式及文件名，再解析电子表格数据。
// 参数 t 为测试上下文，router 为真实 HTTP 路由，path 为下载请求地址。
// 返回值 包含表头的所有 CSV 记录。
func readAuditCSV(t *testing.T, router http.Handler, path string) [][]string {
	t.Helper()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != 200 || response.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("审计下载 %s 返回 %d %v %q", path, response.Code, response.Header(), response.Body.String())
	}
	mode, values, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
	if err != nil || mode != "attachment" || values["filename"] != "业务操作日志.csv" {
		t.Fatalf("审计下载附件头无效: %s", response.Header().Get("Content-Disposition"))
	}
	body, ok := strings.CutPrefix(response.Body.String(), "\ufeff")
	if !ok {
		t.Fatal("审计 CSV 缺少 UTF-8 BOM")
	}
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
