package reporthistory

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const operatorHistoryTestPath = "/api/v1/operator/assessment-history"

// TestOperatorAssessmentHistoryScope 验证运营者历史只能读取同单位同备案的已通过摘要。
// 参数 t 为独立 PostgreSQL 测试上下文。
// 返回值 无。
func TestOperatorAssessmentHistoryScope(t *testing.T) {
	pool, mux := historyFixture(t)
	owned := assessmentTestFiling(t, pool, "U3-1", "甲单位")
	foreign := assessmentTestFiling(t, pool, "U3-2", "乙单位")
	old := insertHistoryReport(t, pool, owned, "U3-R1", "approved", "high", "2024-01-01T08:00:00Z")
	newer := insertHistoryReport(t, pool, owned, "U3-R2", "approved", "low", "2025-01-01T08:00:00Z")
	unapproved := insertHistoryReport(t, pool, owned, "U3-R3", "pending_final", "low", "2026-01-01T08:00:00Z")
	other := insertHistoryReport(t, pool, foreign, "U3-R4", "approved", "low", "2026-02-01T08:00:00Z")
	scope := "?organization=" + url.QueryEscape("甲单位")
	systems := readHistoryTest[struct{ Items []map[string]any }](t, mux, operatorHistoryTestPath+"/systems"+scope, 200)
	if len(systems.Items) != 1 || systems.Items[0]["id"] != owned {
		t.Fatalf("意外的系统: %v", systems.Items)
	}
	list := readHistoryTest[struct{ Items []map[string]any }](t, mux, operatorHistoryTestPath+"/"+owned+scope, 200)
	if len(list.Items) != 2 || list.Items[0]["id"] != newer || list.Items[1]["id"] != old || list.Items[0]["result"] != nil {
		t.Fatalf("意外的已审核历史: %v", list.Items)
	}
	readHistoryTest[map[string]any](t, mux, operatorHistoryTestPath+"/"+foreign+scope, 404)
	path := operatorHistoryTestPath + "/" + owned + "/comparison" + scope
	for _, id := range []string{unapproved, other} {
		readHistoryTest[map[string]any](t, mux, path+"&from="+old+"&to="+id, 404)
	}
	readHistoryTest[map[string]any](t, mux, path+"&from="+newer+"&to="+old, 400)
	diff := readHistoryTest[map[string]any](t, mux, path+"&from="+old+"&to="+newer, 200)
	if diff["riskChange"] != "decreased" || diff["scoreDelta"] != nil {
		t.Fatalf("禁止虚构待登记得分: %v", diff)
	}
}

// TestAssessmentResultValidationAndVersions 验证证据所属、报告状态、重提去重和历史结果版本。
// 参数 t 为隔离库测试上下文。
// 返回值 无。
func TestAssessmentResultValidationAndVersions(t *testing.T) {
	pool, mux := historyFixture(t)
	filing := assessmentTestFiling(t, pool, "U3-F", "自测单位")
	report := insertHistoryReport(t, pool, filing, "U3-S", "approved", "medium", "2025-01-01T08:00:00Z")
	prior := insertHistoryReport(t, pool, filing, "U3-P", "approved", "high", "2024-01-01T08:00:00Z")
	pending := insertHistoryReport(t, pool, filing, "U3-Q", "pending_initial", "low", "2026-01-01T08:00:00Z")
	attachment := insertHistoryFile(t, pool, report, "现期结论.pdf", "assessment_conclusion", "2025-01-01T08:00:00Z")
	foreign := insertHistoryFile(t, pool, prior, "前期结论.pdf", "assessment_conclusion", "2024-01-01T08:00:00Z")
	extra := insertHistoryFile(t, pool, report, "整改规划.pdf", "remediation_plan", "2025-01-01T08:00:00Z")
	path := "/api/v1/reports/" + report + "/assessment-result"
	input := validAssessmentInput(attachment)
	for _, invalid := range []any{88.50000000001, "88.50", 100.01, -0.01, nil} {
		input["score"] = invalid
		postAssessmentResult(t, mux, path, input, 400)
	}
	input["score"] = 88.5
	for _, id := range []string{foreign, extra} {
		input["evidenceId"] = id
		postAssessmentResult(t, mux, path, input, 400)
	}
	input["evidenceId"] = attachment
	postAssessmentResult(t, mux, "/api/v1/reports/"+pending+"/assessment-result", input, 409)
	saved := postAssessmentResult(t, mux, path, input, 200)
	duplicate := postAssessmentResult(t, mux, path, input, 200)
	if saved["version"] != float64(1) || duplicate["version"] != saved["version"] || duplicate["recordedAt"] != saved["recordedAt"] {
		t.Fatalf("重复重试产生新版本: %v %v", saved, duplicate)
	}
	input["score"] = 91.25
	postAssessmentResult(t, mux, path, input, 409)
	input["expectedResultVersion"] = 1
	postAssessmentResult(t, mux, path, input, 200)
	history := readHistoryTest[struct{ Items []map[string]any }](t, mux, path+"/history", 200)
	if len(history.Items) != 2 || history.Items[0]["score"] != 91.25 || history.Items[1]["score"] != 88.5 {
		t.Fatalf("原结果未得到保留: %v", history.Items)
	}
	verifyAssessmentDiff(t, mux, filing, prior, report, foreign)
}

// verifyAssessmentDiff 核对差值来源和运营者所见结果字段。
// 参数 t、mux、filing 等提供测试路由、备案和前后期资料。
// 返回值 无。
func verifyAssessmentDiff(t *testing.T, mux http.Handler, filing, prior, report, attachment string) {
	t.Helper()
	input := validAssessmentInput(attachment)
	input["score"] = 84.5
	input["high"] = 4
	postAssessmentResult(t, mux, "/api/v1/reports/"+prior+"/assessment-result", input, 200)
	query := "?organization=" + url.QueryEscape("自测单位") + "&from=" + prior + "&to=" + report
	diff := readHistoryTest[map[string]any](t, mux, operatorHistoryTestPath+"/"+filing+"/comparison"+query, 200)
	if diff["scoreDelta"] != 6.75 || diff["highDelta"] != float64(-3) {
		t.Fatalf("差值未取自已登记的结果: %v", diff)
	}
	point := diff["to"].(map[string]any)["result"].(map[string]any)
	if len(point) != 5 || point["score"] != 91.25 {
		t.Fatalf("运营者仅允许读取摘要: %v", point)
	}
}

// validAssessmentInput 生成报告结果登记用的合成业务数据。
// 参数 evidence 为同报告证据标识。
// 返回值 可用的登记请求。
func validAssessmentInput(evidence string) map[string]any {
	return map[string]any{"expectedReportVersion": 1, "expectedResultVersion": 0, "evidenceId": evidence,
		"score": 88.5, "conclusion": "basically_conforming", "high": 1, "medium": 2, "low": 0}
}

// assessmentTestFiling 为归属验证登记拥有指定单位的备案。
// 参数 t、pool、number、organization 为测试、连接、编号和当前归属。
// 返回值 备案的数据库标识。
func assessmentTestFiling(t *testing.T, pool *pgxpool.Pool, number, organization string) string {
	t.Helper()
	id := insertHistoryFiling(t, pool, number)
	if _, err := pool.Exec(t.Context(), "UPDATE business_cases SET organization_name=$1 WHERE id=$2", organization, id); err != nil {
		t.Fatal(err)
	}
	return id
}

// postAssessmentResult 向真实处理器发送结构化结果登记请求。
// 参数 t、mux、path、input、status 分别为测试、路由、地址、请求和预期状态。
// 返回值 解码后的业务对象。
func postAssessmentResult(t *testing.T, mux http.Handler, path string, input map[string]any, status int) map[string]any {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	writer := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(writer, req)
	if writer.Code != status {
		t.Fatalf("%s: 状态 %d，预期 %d: %s", path, writer.Code, status, writer.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(writer.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
