package supervision

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const testPDF = "%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n%%EOF\n"

// TestInspectionWorkflow 验证材料严格校验、并发幂等、按类补正及端到端留档。
// 参数 t 为独立数据库及附件目录测试环境。
// 返回值 无。
func TestInspectionWorkflow(t *testing.T) {
	db, router := inspectionFixture(t)
	filing, due := seedInspectionFiling(t, db, testOperator, "SU-流程")
	first := inspectionCall(t, router, "POST", testBase, map[string]any{
		"requestId": sampleRequest, "filingId": filing, "title": "年度自查流程", "dueOn": due,
	}, 201)
	id := first["id"].(string)
	reg, op, other := testBase+"/"+id, testOperatorBase+"/"+id, "?organization=错误单位"
	scope := "?organization=" + url.QueryEscape(testOperator)

	inspectionAction(t, router, reg, "", "publish", 1, 422)
	inspectionUpload(t, router, reg, "", inspectionTemplate, 1, "伪装内容", 415)
	template := inspectionUpload(t, router, reg, "", inspectionTemplate, 1, testPDF, 201)
	repeat := inspectionUpload(t, router, reg, "", inspectionTemplate, 1, testPDF, 201)
	if template["attachment"].(map[string]any)["id"] != repeat["attachment"].(map[string]any)["id"] ||
		template["version"] != repeat["version"] {
		t.Fatal("相同模板重复上传推进了版本")
	}
	inspectionUpload(t, router, reg, "", summaryTemplate, 2, testPDF, 201)
	published := concurrentlyExecute(t, router, reg+"/actions", map[string]any{"action": "publish", "expectedVersion": 3})
	if published["status"] != "open" {
		t.Fatalf("下发后状态 = %v", published)
	}
	inspectionCall(t, router, "GET", op+other, nil, 404)
	inspectionUpload(t, router, op, other, inspectionForm, 4, testPDF, 404)
	inspectionAction(t, router, op, scope, "submit", 4, 422)
	firstForm := inspectionUpload(t, router, op, scope, inspectionForm, 4, testPDF, 201)
	fileID := firstForm["attachment"].(map[string]any)["id"].(string)
	inspectionCall(t, router, "GET", reg+"/attachments/"+fileID, nil, 404)
	inspectionCall(t, router, "GET", op+"/attachments/"+fileID+other, nil, 404)
	inspectionAction(t, router, op, scope, "submit", 5, 422)
	inspectionUpload(t, router, op, scope, rectificationForm, 5, testPDF, 201)

	submitted := concurrentlyExecute(t, router, op+"/actions"+scope, map[string]any{"action": "submit", "expectedVersion": 6})
	if submitted["status"] != "submitted" {
		t.Fatalf("首次送审失败：%v", submitted)
	}
	finishReviewCycle(t, router, reg, op, scope)
	assertWorkflowHistory(t, router, reg, op, scope, fileID)
	assertInspectionDelivery(t, db, id)
}

// finishReviewCycle 复核退回材料约束及更新版本后的再次提交与通过。
// 参数 t、router 为测试环境，reg、op、scope 为两端任务及运营端范围。
// 返回值 无。
func finishReviewCycle(t *testing.T, router http.Handler, reg, op, scope string) {
	t.Helper()
	inspectionCall(t, router, "POST", reg+"/actions", map[string]any{
		"action": "return", "expectedVersion": 7,
		"comment": "请更新整改情况汇总", "correctionKind": rectificationForm,
	}, 200)
	inspectionAction(t, router, op, scope, "submit", 8, 422)
	inspectionUpload(t, router, op, scope, rectificationForm, 8, testPDF, 201)
	inspectionAction(t, router, op, scope, "submit", 8, 422)
	inspectionUpload(t, router, op, scope, rectificationForm, 8, testPDF+"\n", 201)
	inspectionAction(t, router, op, scope, "submit", 9, 200)
	approved := inspectionAction(t, router, reg, "", "approve", 10, 200)
	if approved["status"] != "approved" || approved["version"] != float64(11) {
		t.Fatalf("终态版本错误：%v", approved)
	}
	inspectionAction(t, router, reg, "", "approve", 10, 200)
	inspectionAction(t, router, op, scope, "submit", 11, 409)
	inspectionUpload(t, router, op, scope, inspectionForm, 11, testPDF, 409)
}

// assertWorkflowHistory 比较两轮送审证据并验证原文件可继续追溯下载。
// 参数 t、router 为测试环境，reg、op、scope 为任务两端，fileID 为第一次自查表。
// 返回值 无。
func assertWorkflowHistory(t *testing.T, router http.Handler, reg, op, scope, fileID string) {
	t.Helper()
	events := inspectionCall(t, router, "GET", op+"/history"+scope, nil, 200)["items"].([]any)
	if len(events) != 5 {
		t.Fatalf("运营者业务留痕数量错误：%v", events)
	}
	first := events[3].(map[string]any)["materials"].([]any)
	fixed := events[1].(map[string]any)["materials"].([]any)
	if first[0].(map[string]any)["id"] != fixed[0].(map[string]any)["id"] ||
		first[1].(map[string]any)["id"] == fixed[1].(map[string]any)["id"] {
		t.Fatalf("原证据和补正证据未独立保存：%v %v", first, fixed)
	}
	snapshot := events[1].(map[string]any)["rectification"].(map[string]any)
	if snapshot["open"] != float64(0) || snapshot["closed"] != float64(0) {
		t.Fatalf("整改汇总未固化：%v", snapshot)
	}
	received := httptest.NewRecorder()
	router.ServeHTTP(received, httptest.NewRequest("GET", reg+"/attachments/"+fileID, nil))
	if received.Code != 200 || received.Body.String() != testPDF {
		t.Fatalf("监管归档下载不一致：%d %q", received.Code, received.Body.String())
	}
}

// assertInspectionDelivery 检查成功下发、补正与通过各只产生一条实际站内回执。
// 参数 t、db 为测试环境，id 为任务标识。
// 返回值 无。
func assertInspectionDelivery(t *testing.T, db *pgxpool.Pool, id string) {
	t.Helper()
	var all, correct int
	err := db.QueryRow(t.Context(), `SELECT count(*),count(*) FILTER(WHERE recipient=$2
		AND state IN ('open','returned','approved')) FROM review_notices
		WHERE resource='supervisions' AND resource_id=$1`, id, testOperator).Scan(&all, &correct)
	if err != nil || all != 3 || correct != 3 {
		t.Fatalf("消息送达或去重错误：%d、%d、%v", all, correct, err)
	}
}

// inspectionAction 提交带业务版本的状态动作。
// 参数 t、router、path、scope 为请求环境，action、version、status 为动作和预期。
// 返回值 实际状态响应。
func inspectionAction(t *testing.T, router http.Handler, path, scope, action string, version, status int) map[string]any {
	t.Helper()
	return inspectionCall(t, router, "POST", path+"/actions"+scope,
		map[string]any{"action": action, "expectedVersion": version}, status)
}

// inspectionUpload 提交一份明确归类的监督检查附件。
// 参数 t、router、path、scope 指请求环境，kind、version、body、status 指文件及预期。
// 返回值 实际附件响应。
func inspectionUpload(t *testing.T, router http.Handler, path, scope, kind string,
	version int, body string, status int,
) map[string]any {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if err := writer.WriteField("kind", kind); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("expectedVersion", strconv.Itoa(version)); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", kind+".pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", path+"/attachments"+scope, &buffer)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("材料上传 %s %d 得到 %d，期望 %d，%s", kind, version, w.Code, status, w.Body)
	}
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// concurrentlyExecute 同时重放两次相同操作并验证服务器响应一致。
// 参数 t、router、path、payload 为测试环境、操作地址及原请求。
// 返回值 两个相同结果中的状态响应。
func concurrentlyExecute(t *testing.T, router http.Handler, path string, payload map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var responses [2]*httptest.ResponseRecorder
	var wait sync.WaitGroup
	for index := range responses {
		wait.Go(func() {
			request := httptest.NewRequest("POST", path, bytes.NewReader(raw))
			request.Header.Set("Content-Type", "application/json")
			responses[index] = httptest.NewRecorder()
			router.ServeHTTP(responses[index], request)
		})
	}
	wait.Wait()
	if responses[0].Code != 200 || responses[1].Code != 200 ||
		responses[0].Body.String() != responses[1].Body.String() {
		t.Fatalf("并发回执不一致：%d %s，%d %s", responses[0].Code, responses[0].Body,
			responses[1].Code, responses[1].Body)
	}
	var result map[string]any
	if err := json.Unmarshal(responses[0].Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
