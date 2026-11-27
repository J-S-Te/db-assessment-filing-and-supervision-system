package supervision

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestDownloadOnlyPublishedEvidence 检查替换掉的草稿模板和未送审材料不进入收件方范围。
// 参数 t 为隔离数据库及附件目录测试环境。
// 返回值 无。
func TestDownloadOnlyPublishedEvidence(t *testing.T) {
	db, router := inspectionFixture(t)
	filing, due := seedInspectionFiling(t, db, testOperator, "SU-证据")
	task := inspectionCall(t, router, "POST", testBase, map[string]any{
		"requestId": sampleRequest, "filingId": filing, "title": "附件范围", "dueOn": due,
	}, 201)
	id := task["id"].(string)
	reg, op := testBase+"/"+id, testOperatorBase+"/"+id
	scope := "?organization=" + url.QueryEscape(testOperator)

	staleTemplate := inspectionUpload(t, router, reg, "", inspectionTemplate, 1, testPDF, 201)
	publishedTemplate := inspectionUpload(t, router, reg, "", inspectionTemplate, 2, testPDF+"\n", 201)
	inspectionUpload(t, router, reg, "", summaryTemplate, 3, testPDF, 201)
	inspectionAction(t, router, reg, "", "publish", 4, 200)
	assertFileStatus(t, router, attachedFile(op, staleTemplate)+scope, 404)
	assertFileStatus(t, router, attachedFile(op, publishedTemplate)+scope, 200)
	assertFileStatus(t, router, attachedFile(reg, staleTemplate), 200)

	staleResult := inspectionUpload(t, router, op, scope, inspectionForm, 5, testPDF, 201)
	submittedResult := inspectionUpload(t, router, op, scope, inspectionForm, 6, testPDF+"\n", 201)
	inspectionUpload(t, router, op, scope, rectificationForm, 7, testPDF, 201)
	inspectionAction(t, router, op, scope, "submit", 8, 200)
	assertFileStatus(t, router, attachedFile(reg, staleResult), 404)
	assertFileStatus(t, router, attachedFile(reg, submittedResult), 200)
	assertFileStatus(t, router, attachedFile(op, staleResult)+scope, 200)
}

// attachedFile 将上传结果中的文件标识拼到当前任务的附件入口。
// 参数 base 是任务页面对应的接口，upload 是真实上传结果。
// 返回值 文件下载地址。
func attachedFile(base string, upload map[string]any) string {
	return base + "/attachments/" + upload["attachment"].(map[string]any)["id"].(string)
}

// assertFileStatus 核对直接访问材料地址时的实际下载范围。
// 参数 t、router 为隔离测试环境，path 和 status 为下载地址及预期状态。
// 返回值 无。
func assertFileStatus(t *testing.T, router http.Handler, path string, status int) {
	t.Helper()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
	if response.Code != status {
		t.Errorf("文件下载得到 %d，期望 %d：%s", response.Code, status, response.Body)
	}
}
