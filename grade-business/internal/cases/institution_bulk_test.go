package cases

import (
	"encoding/csv"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"grade-business/internal/reviewinbox"
)

const bulkInstitution = "批量办理测试机构"
const bulkEndpoint = institutionBase + "/filings"

type testBulkResult struct {
	Items []struct {
		ID      string
		Outcome string
		Code    string
		Version *int64
	}
}

// TestInstitutionFilingBulk 验证同报送规则、逐条结果、版本保护及并发重放。
// 参数 t 为隔离数据库及附件目录的测试上下文。
// 返回值 无。
func TestInstitutionFilingBulk(t *testing.T) {
	service, handler := fixtureService(t)
	own := firstSubmissionScope(bulkInstitution)
	first := createBulkFixture(t, handler, own, "BA-BULK-1")
	second := createBulkFixture(t, handler, own, "BA-BULK-2")
	otherScope := firstSubmissionScope("其他测试机构")
	foreign := createBulkFixture(t, handler, otherScope, "BA-BULK-3")
	postMaterialAt(t, handler, bulkEndpoint+"/"+first.ID+"/attachments"+own, "filing_form")
	postMaterialAt(t, handler, bulkEndpoint+"/"+first.ID+"/attachments"+own, "classification_report")
	records := []map[string]any{{"id": first.ID, "expectedVersion": first.Version + 2},
		{"id": second.ID, "expectedVersion": second.Version}, {"id": foreign.ID, "expectedVersion": foreign.Version}}
	request := map[string]any{"requestId": "2bedc4e1-f57a-43a4-99f4-6cf31f905771", "action": "submit", "items": records}
	path := bulkEndpoint + "/batch" + own
	assertRepeatedBatch(t, handler, path, request, first.ID)
	var events int
	err := service.db.QueryRow(t.Context(), `SELECT count(*) FROM case_review_events
		WHERE case_id=$1 AND action='submit'`, first.ID).Scan(&events)
	if err != nil || events != 1 {
		t.Fatalf("送审留痕未去重: %d %v", events, err)
	}
	request["items"] = records[:1]
	assertError(t, requestJSON(handler, "POST", path, request), 409, "conflict")
	assertBulkDiscard(t, handler, own, first, second)
}

// assertRepeatedBatch 检查并发重放的输出与机构归属。
// 参数 t、handler 为接口测试上下文及路由，path、body、id 分别为接口地址、输入和首推编号。
// 返回值 无。
func assertRepeatedBatch(t *testing.T, handler http.Handler, path string, body map[string]any, id string) {
	t.Helper()
	type result struct {
		code int
		body string
	}
	attempts := make(chan result, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			response := requestJSON(handler, "POST", path, body)
			attempts <- result{response.Code, response.Body.String()}
		})
	}
	group.Wait()
	first, second := <-attempts, <-attempts
	if first.code != 200 || first != second {
		t.Fatalf("并发重放失败: %+v / %+v", first, second)
	}
	response := readResponse[testBulkResult](t, requestJSON(handler, "POST", path, body), 200)
	if len(response.Items) != 3 || response.Items[0].ID != id ||
		response.Items[0].Outcome != "submitted" || response.Items[0].Version == nil ||
		response.Items[1].Code != "materials_required" || response.Items[2].Code != "not_found" {
		t.Fatalf("批量逐条结果不符: %+v", response)
	}
}

// assertBulkDiscard 检查提交后拒绝删除、草稿删除及已留痕版本的同号再用。
// 参数 t、handler 为接口测试上下文及路由，scope 为机构查询，submitted 与 draftItem 为样本。
// 返回值 无。
func assertBulkDiscard(t *testing.T, handler http.Handler, scope string, submitted, draftItem mutation) {
	t.Helper()
	body := map[string]any{"requestId": "5f9a6438-ec9e-4f0a-99a1-d58d2933b8b4", "action": "discard",
		"items": []map[string]any{{"id": submitted.ID, "expectedVersion": submitted.Version + 3},
			{"id": draftItem.ID, "expectedVersion": draftItem.Version}}}
	response := readResponse[testBulkResult](t, requestJSON(handler, "POST", bulkEndpoint+"/batch"+scope, body), 200)
	if len(response.Items) != 2 || response.Items[0].Code != "conflict" || response.Items[1].Outcome != "discarded" {
		t.Fatalf("批量删除越过状态限制: %+v", response)
	}
	assertError(t, requestJSON(handler, "GET", bulkEndpoint+"/"+draftItem.ID+scope, nil), 404, "not_found")
	createBulkFixture(t, handler, scope, "BA-BULK-2")
}

// TestInstitutionFilingCopy 验证复制产生独立草稿，只继承办理字段。
// 参数 t 为隔离数据库及附件目录的测试上下文。
// 返回值 无。
func TestInstitutionFilingCopy(t *testing.T) {
	_, handler := fixtureService(t)
	scope := firstSubmissionScope(bulkInstitution)
	source := createBulkFixture(t, handler, scope, "BA-COPY-ORIGIN")
	path := bulkEndpoint + "/" + source.ID
	postMaterialAt(t, handler, path+"/attachments"+scope, "filing_form")
	in := map[string]any{"requestId": "da564d5c-ba07-4224-b678-eebbba4902f4",
		"number": "BA-COPY-NEW", "expectedVersion": source.Version + 1}
	duplicate := readResponse[mutation](t, requestJSON(handler, "POST", path+"/copy"+scope, in), 201)
	if duplicate.ID == source.ID || duplicate.Status != draft || duplicate.Version != 1 ||
		duplicate != readResponse[mutation](t, requestJSON(handler, "POST", path+"/copy"+scope, in), 201) {
		t.Fatalf("草稿复制或幂等回执错误: %+v", duplicate)
	}
	target := bulkEndpoint + "/" + duplicate.ID
	copied := readResponse[recordDetail](t, requestJSON(handler, "GET", target+scope, nil), 200)
	if copied.Number != "BA-COPY-NEW" || copied.SystemName != "批量测试系统" || copied.AssessmentOrg != bulkInstitution {
		t.Fatalf("复制未使用源业务字段: %+v", copied)
	}
	files := readResponse[itemResponse[Attachment]](t, requestJSON(handler, "GET", target+"/attachments"+scope, nil), 200)
	if len(files.Items) != 0 {
		t.Fatalf("材料错误复制: %+v", files)
	}
	assertError(t, requestJSON(handler, "POST", path+"/copy"+firstSubmissionScope("非所属机构"), in), 404, "not_found")
	in["requestId"] = "f3a235a8-3998-4824-b281-b6b481cf4bfa"
	in["expectedVersion"] = source.Version
	assertError(t, requestJSON(handler, "POST", path+"/copy"+scope, in), 409, "conflict")
}

// TestInstitutionReportExport 核对机构台账按归属和筛选导出安全单元格。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestInstitutionReportExport(t *testing.T) {
	_, handler := fixtureService(t)
	file := approvedFiling(t, handler, "BA-SCOPED-EXPORT")
	for _, row := range []reportInput{
		{Number: "+BG-INSTITUTION", FilingID: file.ID, AssessmentOrg: bulkInstitution, Risk: "high"},
		{Number: "BG-LOW", FilingID: file.ID, AssessmentOrg: bulkInstitution, Risk: "low"},
		{Number: "BG-OTHER", FilingID: file.ID, AssessmentOrg: "其他测试机构", Risk: "high"},
	} {
		readResponse[mutation](t, requestJSON(handler, "POST", reportsURL, row), 201)
	}
	base := institutionBase + "/reports/export"
	response := requestJSON(handler, "GET", base+firstSubmissionScope(bulkInstitution)+"&risk=high", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("机构导出未返回 CSV: %d %s", response.Code, response.Body.String())
	}
	result, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(response.Body.String(), "\ufeff"))).ReadAll()
	if err != nil || len(result) != 2 || len(result[1]) != 6 || result[1][0] != "'+BG-INSTITUTION" {
		t.Fatalf("导出跨机构、忽略风险筛选或包含不安全字段: %v %v", result, err)
	}
	assertError(t, requestJSON(handler, "GET", base+"?risk=high", nil), 400, "invalid_input")
	assertError(t, requestJSON(handler, "GET", base+firstSubmissionScope(bulkInstitution)+"&limit=1", nil), 400, "invalid_input")
}

// createBulkFixture 新建用于批量操作的一条机构备案草稿。
// 参数 t、handler 为测试上下文与路由，scope、number 为机构查询和备案编号。
// 返回值 持久化的备案摘要。
func createBulkFixture(t *testing.T, handler http.Handler, scope, number string) mutation {
	t.Helper()
	return readResponse[mutation](t, requestJSON(handler, "POST", bulkEndpoint+scope, map[string]any{
		"number": number, "systemName": "批量测试系统", "organizationName": "批量测试单位", "level": "3",
	}), 201)
}

// TestInstitutionDiscardClosesMaterialIssue 验证删除草稿时既有材料异常和收件状态同步关闭。
// 参数 t 为隔离数据库及临时材料目录的测试上下文。
// 返回值 无。
func TestInstitutionDiscardClosesMaterialIssue(t *testing.T) {
	service, handler := fixtureService(t)
	mux := handler.(*http.ServeMux)
	service.RegisterOperatorFilingRoutes(mux)
	reviewinbox.Register(mux, service.db)
	scope := firstSubmissionScope(bulkInstitution)
	source := createBulkFixture(t, handler, scope, "BULK-DISCARD-MATERIAL")
	path := bulkEndpoint + "/" + source.ID
	postMaterialAt(t, handler, path+"/attachments"+scope, "filing_form")
	assertError(t, materialRecoveryUpload(handler, path+"/attachments"+scope,
		"classification_report", "定级报告.pdf", "损坏的文件", ""), 400, "attachment_type")
	body := map[string]any{"requestId": "fdf93d1e-1d57-40c0-8352-c9e086395208", "action": "discard",
		"items": []map[string]any{{"id": source.ID, "expectedVersion": source.Version + 1}}}
	response := readResponse[testBulkResult](t, requestJSON(handler, "POST", bulkEndpoint+"/batch"+scope, body), 200)
	if len(response.Items) != 1 || response.Items[0].Outcome != "discarded" {
		t.Fatalf("删除批次失败：%+v", response)
	}
	assertDiscardedMaterials(t, service, handler, scope, source.ID)
	organization := "?organization=" + url.QueryEscape("批量测试单位")
	assertError(t, requestJSON(handler, "GET", "/api/v1/operator/filings/"+source.ID+"/receipt"+organization, nil), 404, "not_found")
	assertError(t, requestJSON(handler, "POST", institutionBase+"/filing-applications"+scope, map[string]any{
		"filingId": source.ID, "type": "cancel", "reason": "不再使用",
	}), 404, "not_found")
}

// assertDiscardedMaterials 核对关闭原因、原文件保留及两类收件方通知。
// 参数 t、s、handler 为测试上下文、服务和路由，scope、id 为机构及源记录标识。
// 返回值 无。
func assertDiscardedMaterials(t *testing.T, s *Service, handler http.Handler, scope, id string) {
	t.Helper()
	items := readResponse[itemResponse[map[string]any]](t, requestJSON(handler, "GET",
		institutionBase+"/material-issues"+scope+"&status=all", nil), 200)
	if len(items.Items) != 1 || items.Items[0]["status"] != "closed" || items.Items[0]["resolution"] != "discarded" {
		t.Fatalf("草稿删除未关闭关联异常：%+v", items)
	}
	var files, notices int
	err := s.db.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM case_attachments WHERE case_id=$1),
		(SELECT count(*) FROM review_notices n JOIN material_issues i ON i.id=n.resource_id
			WHERE i.case_id=$1 AND n.resource='material-issues' AND n.state='material_closed')`, id).
		Scan(&files, &notices)
	if err != nil || files != 1 || notices != 2 {
		t.Fatalf("关闭提醒或历史文件丢失：%d, %d, %v", files, notices, err)
	}
}
