package cases

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"grade-business/internal/reviewinbox"
)

// TestInstitutionMaterialFailureRecovery 核对真实上传失败、去重、归属、补正与三端关闭。
// 参数 t 为使用隔离数据库和临时附件路径的测试上下文。
// 返回值 无。
func TestInstitutionMaterialFailureRecovery(t *testing.T) {
	service, _ := fixtureService(t)
	router := http.NewServeMux()
	service.Register(router)
	reviewinbox.Register(router, service.db)
	active := approvedFiling(t, router, "BA-MATERIAL")
	own := firstSubmissionScope("测评中心")
	created := readResponse[mutation](t, requestJSON(router, "POST", institutionBase+"/reports"+own,
		map[string]any{"number": "BG-MATERIAL", "risk": "medium", "filingId": active.ID}), 201)
	base := institutionBase + "/reports/" + created.ID
	upload := base + "/attachments" + own
	for _, invalid := range []string{"坏材料", "%PDF-1.7\n未完整上传"} {
		assertError(t, materialRecoveryUpload(router, upload, "report_file", "待补正.pdf", invalid, ""),
			400, "attachment_type")
	}
	id := assertMaterialIssueOpen(t, router, own)
	readResponse[Attachment](t, materialRecoveryUpload(router, upload, "report_file", "报告.pdf", testPDF, id), 201)
	assertError(t, materialRecoveryUpload(router, upload, "report_file", "报告.pdf", testPDF, id), 409, "conflict")
	assertMaterialIssueClosed(t, router, own, id)
}

// assertMaterialIssueOpen 核对重复失败只生成一条异常与一条机构提醒。
// 参数 t 为测试上下文，router 为真实路由，own 为机构范围。
// 返回值 活动异常标识。
func assertMaterialIssueOpen(t *testing.T, router http.Handler, own string) string {
	t.Helper()
	list := readResponse[itemResponse[map[string]any]](
		t, requestJSON(router, "GET", institutionBase+"/material-issues"+own, nil), 200)
	if len(list.Items) != 1 || list.Items[0]["reasonCode"] != "attachment_type" ||
		list.Items[0]["status"] != "open" || list.Items[0]["canDelete"] != true {
		t.Fatalf("异常范围或去重错误: %+v", list)
	}
	notices := readResponse[itemResponse[map[string]any]](
		t, requestJSON(router, "GET", institutionBase+"/feedback"+own, nil), 200)
	count := 0
	for _, notice := range notices.Items {
		if notice["resource"] == "material-issues" &&
			notice["resourceId"] == list.Items[0]["id"] && notice["state"] == "material_failed" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("首次通知应仅有一条: %+v", notices)
	}
	other := readResponse[itemResponse[map[string]any]](t,
		requestJSON(router, "GET", institutionBase+"/material-issues"+firstSubmissionScope("其他机构"), nil), 200)
	if len(other.Items) != 0 {
		t.Fatalf("不属于本机构的异常可见: %+v", other)
	}
	return list.Items[0]["id"].(string)
}

// assertMaterialIssueClosed 核对三端均看到已关闭且运营者响应只包含公开字段。
// 参数 t 为测试上下文，router 为真实路由，own 为机构范围，id 为异常记录。
// 返回值 无。
func assertMaterialIssueClosed(t *testing.T, router http.Handler, own, id string) {
	t.Helper()
	for _, path := range []string{"/api/v1/material-issues?status=all", institutionBase + "/material-issues" + own + "&status=all",
		"/api/v1/operator/material-issues?organization=测试单位&status=all"} {
		items := readResponse[itemResponse[map[string]any]](t, requestJSON(router, "GET", path+"&id="+id, nil), 200).Items
		if len(items) != 1 || items[0]["status"] != "closed" || items[0]["resolution"] != "corrected" {
			t.Fatalf("异常未同步关闭 %s: %+v", path, items)
		}
	}
}

// materialRecoveryUpload 将指定文本制作成与机构前端一致的材料上传请求。
// 参数 router 为真实路由，path、kind、name、contents、issue 为附件请求字段。
// 返回值 HTTP 响应。
func materialRecoveryUpload(router http.Handler, path, kind, name, contents, issue string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("kind", kind)
	if issue != "" {
		_ = writer.WriteField("issueId", issue)
	}
	file, _ := writer.CreateFormFile("file", name)
	_, _ = file.Write([]byte(contents))
	_ = writer.Close()
	request := httptest.NewRequest("POST", path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
