package remediations

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestOperatorRemediationReceiptTracksRecordedReview 核对回执由提交及审核通过留痕生成，并按当前备案归属读取。
// 参数 t 为真实隔离数据库测试上下文。
// 返回值 无。
func TestOperatorRemediationReceiptTracksRecordedReview(t *testing.T) {
	db, router := remediationFixture(t)
	report, _ := insertReports(t, db)
	owner := "?organization=" + url.QueryEscape("测试单位")
	path := createReadyPlan(t, router, operatorRoot, owner, report)
	before := response[map[string]any](t, call(router, http.MethodGet, path+owner, nil), 200)
	initial := response[map[string]string](t, call(router, http.MethodGet, path+"/receipt"+owner, nil), 409)
	if before["receiptAvailable"] != false || initial["code"] != "receipt_unavailable" {
		t.Fatalf("draft exposed a receipt: %v %v", before, initial)
	}
	submitted := response[mutation](t, call(router, http.MethodPost, path+"/submit"+owner, submitInput{4}), 200)
	assertPlanReceiptBody(t, router, path+"/receipt"+owner, "整改规划提交回执")
	response[mutation](t, call(router, http.MethodPost, rootURL+"/"+submitted.ID+"/review-actions", reviewInput{
		Decision: "approve", Comment: "确认规划依据", Deadline: businessDate(9), ExpectedVersion: submitted.Version,
	}), 200)
	assertPlanReceiptBody(t, router, path+"/receipt"+owner, "整改规划审核通过回执")
	after := response[map[string]any](t, call(router, http.MethodGet, path+owner, nil), 200)
	if after["receiptAvailable"] != true {
		t.Fatalf("approved plan hid its receipt: %v", after)
	}
	if _, err := db.Exec(t.Context(), `UPDATE business_cases SET organization_name='新单位' WHERE kind='filing'`); err != nil {
		t.Fatal(err)
	}
	result := response[map[string]string](t, call(router, http.MethodGet, path+"/receipt"+owner, nil), 404)
	if result["code"] != "not_found" {
		t.Fatalf("former unit retained a receipt: %v", result)
	}
}

// assertPlanReceiptBody 核对实际可下载的回执及其审核来源。
// 参数 t 为测试上下文，router、path 和 title 为路由、请求地址及预期标题。
// 返回值 无。
func assertPlanReceiptBody(t *testing.T, router http.Handler, path, title string) {
	t.Helper()
	file := call(router, http.MethodGet, path, nil)
	if file.Code != 200 || file.Header().Get("Content-Type") != "text/plain; charset=utf-8" ||
		!strings.Contains(file.Header().Get("Content-Disposition"), "remediation-receipt.txt") ||
		!strings.Contains(file.Body.String(), title) || !strings.Contains(file.Body.String(), "BA-QA") {
		t.Fatalf("incorrect durable plan receipt: %d %s", file.Code, file.Body.String())
	}
}
