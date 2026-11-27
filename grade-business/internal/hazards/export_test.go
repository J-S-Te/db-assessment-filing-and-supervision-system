package hazards

import (
	"encoding/csv"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// TestHazardExportRejectsInvalidQueriesAndScopedRoutes 验证导出只接受监管端业务筛选。
// 参数 t 为独立接口测试上下文。
// 返回值 无。
func TestHazardExportRejectsInvalidQueriesAndScopedRoutes(t *testing.T) {
	mux := http.NewServeMux()
	service := New(nil, nil, "")
	service.Register(mux)
	service.RegisterOperator(mux)
	service.RegisterInstitution(mux)
	for _, query := range []string{"cursor=", "limit=20", "ledger=other", "filingId=oops",
		"q=a&q=b", "ledger=vulnerability&ledger=vulnerability", "organization=other", "q=%ZZ"} {
		t.Run(query, func(t *testing.T) {
			response := ledgerCall(mux, "GET", testLedgerPath+"/export?"+query, nil)
			assertLedgerError(t, response, http.StatusBadRequest, "invalid_input")
			if response.Header().Get("Content-Disposition") != "" {
				t.Fatal("invalid export filter returned a download")
			}
		})
	}
	for _, path := range []string{operatorBase + "/export?organization=试点单位",
		institutionBase + "/export?institution=测评中心"} {
		response := ledgerCall(mux, "GET", path, nil)
		assertLedgerError(t, response, http.StatusNotFound, "not_found")
	}
}

// TestHazardExportMatchesFiltersAndProtectsCells 核对筛选、业务日期及文本编码。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestHazardExportMatchesFiltersAndProtectsCells(t *testing.T) {
	service, router := ledgerFixture(t)
	plan, _, filing := seedPlan(t, service.db)
	synced := ledgerCall(router, "POST", testLedgerPath+"/sync/"+plan, nil)
	ledgerReply[struct{ Count int }](t, synced, http.StatusOK)
	setHazardExportFixture(t, service, plan, filing)
	query := url.Values{
		"q": {"风险一"}, "status": {"open"}, "severity": {"high"}, "due": {"overdue"},
		"planId": {plan}, "filingId": {filing}, "ledger": {"vulnerability"},
	}
	path := testLedgerPath + "/export?"
	response := ledgerCall(router, "GET", path+query.Encode(), nil)
	rows := readHazardExport(t, response, "系统漏洞台账.csv")
	want := []string{
		"'\u3000=系统甲", "试点单位", "系统漏洞台账", "风险一", "高风险", "整改规划",
		"", "2025-01-02", "", "2025-01-10", "待整改",
		"'\u200b\x01＠责任人,\"甲\"\n继续", "BA-001", "风险一",
	}
	if len(rows) != 2 || !slices.Equal(rows[1], want) {
		t.Fatalf("filtered CSV rows = %q, want header and %q", rows, want)
	}
	query.Set("ledger", "major_risk")
	response = ledgerCall(router, "GET", path+query.Encode(), nil)
	rows = readHazardExport(t, response, "重大风险台账.csv")
	if len(rows) != 1 {
		t.Fatalf("major risk CSV leaked records from another ledger: %q", rows)
	}
}

// setHazardExportFixture 创建包含危险开头及标准 CSV 转义字符的业务记录。
// 参数 t 为测试上下文，service 为隔离服务，plan 和 filing 为已登记的业务标识。
// 返回值 无。
func setHazardExportFixture(t *testing.T, service *Service, plan, filing string) {
	t.Helper()
	if _, err := service.db.Exec(t.Context(), `UPDATE business_cases SET system_name=$1 WHERE id=$2`,
		"\u3000=系统甲", filing); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(t.Context(), `UPDATE hazard_items SET responsibility=$1,
		created_at='2025-01-01 17:00:00+00', deadline='2025-01-10' WHERE plan_id=$2 AND severity='high'`,
		"\u200b\x01＠责任人,\"甲\"\n继续", plan); err != nil {
		t.Fatal(err)
	}
}

// TestHazardExportIncludesRegisteredMajorRisk 核对直接登记的重大风险可筛选导出。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestHazardExportIncludesRegisteredMajorRisk(t *testing.T) {
	service, router := ledgerFixture(t)
	_, _, filing := seedPlan(t, service.db)
	input := registrationInput{
		Ledger: "major_risk", FilingID: filing, Title: "＝核心数据风险", Severity: "medium",
		Source: "assessment", DiscoveredOn: "2025-01-01", Deadline: "2025-02-01",
		Description: "+风险说明", Measure: "整改措施", Responsibility: "运维组",
	}
	registered := ledgerCall(router, "POST", testLedgerPath, input)
	item := ledgerReply[hazardMutation](t, registered, http.StatusCreated)
	_, err := service.db.Exec(t.Context(), `UPDATE hazard_items
		SET created_at='2025-01-01 17:00:00+00' WHERE id=$1`, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{
		"q": {"核心数据风险"}, "ledger": {"major_risk"}, "filingId": {filing}, "status": {"pending_dispatch"},
	}
	response := ledgerCall(router, "GET", testLedgerPath+"/export?"+query.Encode(), nil)
	rows := readHazardExport(t, response, "重大风险台账.csv")
	want := []string{
		"系统甲", "试点单位", "重大风险台账", "'＝核心数据风险", "中风险", "测评发现",
		"2025-01-01", "2025-01-02", "", "2025-02-01", "待派单", "运维组", "BA-001", "'+风险说明",
	}
	if len(rows) != 2 || !slices.Equal(rows[1], want) {
		t.Fatalf("major risk CSV rows = %q, want header and %q", rows, want)
	}
}

// TestHazardExportCapsRowsWithoutSendingPartialFile 检查导出上限及超出后的普通错误响应。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestHazardExportCapsRowsWithoutSendingPartialFile(t *testing.T) {
	service, router := ledgerFixture(t)
	plan, _, _ := seedPlan(t, service.db)
	_, err := service.db.Exec(t.Context(), `INSERT INTO hazard_items
		(plan_id,risk_index,severity,description,measure,responsibility,deadline)
		SELECT $1,i,'high','风险','措施','责任人',DATE '2099-01-01'
		FROM generate_series(1,10000) i`, plan)
	if err != nil {
		t.Fatal(err)
	}
	path := testLedgerPath + "/export?planId=" + plan
	rows := readHazardExport(t, ledgerCall(router, "GET", path, nil), "隐患台账.csv")
	if len(rows) != 10001 {
		t.Fatalf("CSV at export limit has %d data rows, want 10000", len(rows)-1)
	}
	_, err = service.db.Exec(t.Context(), `INSERT INTO hazard_items
		(plan_id,risk_index,severity,description,measure,responsibility,deadline)
		VALUES($1,10001,'high','风险','措施','责任人',DATE '2099-01-01')`, plan)
	if err != nil {
		t.Fatal(err)
	}
	response := ledgerCall(router, "GET", path, nil)
	assertLedgerError(t, response, http.StatusUnprocessableEntity, "export_limit_exceeded")
	if response.Header().Get("Content-Disposition") != "" ||
		!strings.Contains(response.Body.String(), "缩小筛选范围") {
		t.Fatalf("oversized export should return a filter hint without CSV: %s", response.Body.String())
	}
}

// readHazardExport 核对下载头与 UTF-8 BOM 并按 CSV 标准解码。
// 参数 t 为测试上下文，response 为实际响应，filename 为预期下载文件名。
// 返回值 包含表头在内的 CSV 行。
func readHazardExport(t *testing.T, response *httptest.ResponseRecorder, filename string) [][]string {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("expected UTF-8 CSV attachment, got HTTP %d: %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "text/csv; charset=utf-8" {
		t.Fatalf("unexpected CSV content type: %q", contentType)
	}
	disposition, params, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
	if err != nil || disposition != "attachment" || params["filename"] != filename {
		t.Fatalf("invalid download filename: %s (%v)", response.Header().Get("Content-Disposition"), err)
	}
	body, ok := strings.CutPrefix(response.Body.String(), "\ufeff")
	if !ok {
		t.Fatal("CSV missing UTF-8 BOM")
	}
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("invalid CSV body: %v", err)
	}
	header := []string{
		"系统名称", "运营单位", "台账类型", "问题标题", "风险等级", "问题来源",
		"发现日期", "登记日期", "派单日期", "整改期限",
		"办理状态", "责任人或单位", "备案编号", "问题描述",
	}
	if len(rows) == 0 || !slices.Equal(rows[0], header) {
		t.Fatalf("incorrect CSV columns, want %q", header)
	}
	return rows
}
