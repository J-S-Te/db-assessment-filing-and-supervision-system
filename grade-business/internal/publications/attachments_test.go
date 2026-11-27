package publications

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

type attachmentPart struct {
	name, body string
}

type attachmentTestDetail struct {
	ID               string
	Version          int64
	PublishedVersion *int64
	Attachments      []struct {
		ID, FileName, UploadedAt string
		Size                     int64
	}
}

// TestPublicationAttachmentsKeepVersionedCopies 验证发布、修订和撤回期间附件各自的版本和范围。
// 参数 t 为隔离 PostgreSQL 与临时文件测试上下文。
// 返回值 无。
func TestPublicationAttachmentsKeepVersionedCopies(t *testing.T) {
	_, router, _ := attachmentFixture(t)
	path := createAttachmentTestDraft(t, router)
	file := attachmentPart{"公文.pdf", "%PDF-1.4\npublic notice\n%%EOF\n"}
	draft := readAttachmentResponse(t, uploadAttachmentRequest(router, path, 1, file, file), 200)
	if draft.Version != 2 || len(draft.Attachments) != 1 {
		t.Fatalf("相同文件未被去重: %+v", draft)
	}
	replayed := readAttachmentResponse(t, uploadAttachmentRequest(router, path, 2, file), 200)
	if replayed.Version != 2 || len(replayed.Attachments) != 1 || replayed.Attachments[0] != draft.Attachments[0] {
		t.Fatalf("重复上传应返回相同的附件值: 初次=%+v，重复=%+v", draft.Attachments, replayed.Attachments)
	}
	attachment := "/attachments/" + draft.Attachments[0].ID
	portal := "/api/v1/institution/publications/" + draft.ID
	assertAttachmentDownload(t, router, portal+attachment, 2, "收件机构", "", 404)
	pubAction(t, router, path, "publish", 2, 200)
	assertAttachmentDownload(t, router, portal+attachment, 3, "收件机构", file.body, 200)
	assertAttachmentDownload(t, router, portal+attachment, 3, "其他机构", "", 404)
	checkAttachmentRevision(t, router, path, portal, attachment, file)
}

// createAttachmentTestDraft 建立包含确定接收对象的隔离测试公告。
// 参数 t 为测试上下文，router 为真实公告路由。
// 返回值 本次公告的监管接口地址。
func createAttachmentTestDraft(t *testing.T, router http.Handler) string {
	t.Helper()
	form := map[string]any{"category": "bulletin", "title": "附件公文", "publisher": "验收监管",
		"body": "公文正文", "pinned": false, "audience": "selected",
		"targets": []recipient{{Audience: "institution", Recipient: "收件机构"}}}
	created := pubRequest[publicationFixtureItem](t, router, "POST", adminPath, form, 201)
	return adminPath + "/" + created.ID
}

// checkAttachmentRevision 核对草稿移除、撤回及历史文件仍能逐版本追溯。
// 参数 t、router 为测试上下文和路由，path、portal 为两端地址，attachment、file 为附件信息。
// 返回值 无。
func checkAttachmentRevision(t *testing.T, router http.Handler, path, portal, attachment string,
	file attachmentPart,
) {
	t.Helper()
	revised := pubRequest[attachmentTestDetail](t, router, "DELETE", path+attachment,
		map[string]any{"expectedVersion": 3}, 200)
	if revised.Version != 4 || len(revised.Attachments) != 0 {
		t.Fatalf("修订未移除附件: %+v", revised)
	}
	assertAttachmentDownload(t, router, portal+attachment, 3, "收件机构", file.body, 200)
	pubAction(t, router, path, "publish", 4, 200)
	assertAttachmentDownload(t, router, portal+attachment, 3, "收件机构", "", 404)
	assertAttachmentDownload(t, router, portal+attachment, 5, "收件机构", "", 404)
	pubAction(t, router, path, "withdraw", 5, 200)
	assertAttachmentDownload(t, router, path+attachment, 3, "", file.body, 200)
	assertAttachmentDownload(t, router, path+attachment, 6, "", "", 404)
	receipt := pubRequest[attachmentTestDetail](t, router, "GET", path+"/deliveries?version=3", nil, 200)
	if len(receipt.Attachments) != 1 || receipt.Attachments[0].FileName != file.name {
		t.Fatalf("历史发布回执未保留同版本附件: %+v", receipt)
	}
}

// uploadAttachmentRequest 使用与浏览器相同的多文件表单调用公告附件接口。
// 参数 router、path、version、parts 为应用路由、公告、预期版本与附件内容。
// 返回值 原始状态与响应体。
func uploadAttachmentRequest(router http.Handler, path string, version int64, parts ...attachmentPart) *httptest.ResponseRecorder {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("expectedVersion", fmt.Sprint(version))
	for _, part := range parts {
		out, _ := writer.CreateFormFile("files", part.name)
		out.Write([]byte(part.body))
	}
	writer.Close()
	req := httptest.NewRequest("POST", path+"/attachments", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res
}

// readAttachmentResponse 确认批量上传状态并读取保存后的受控公告详情。
// 参数 t、res、status 分别是测试上下文、上传响应与预期状态。
// 返回值 成功时的公告详情。
func readAttachmentResponse(t *testing.T, res *httptest.ResponseRecorder, status int) attachmentTestDetail {
	t.Helper()
	if res.Code != status {
		t.Fatalf("上传响应 %d，期望 %d，正文 %s", res.Code, status, res.Body.String())
	}
	var result attachmentTestDetail
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// assertAttachmentDownload 核对准确的下载对象，并区分角色范围和版本。
// 参数 t、router、path、version、institution、body、code 为测试、URL、范围、预期内容和状态。
// 返回值 无。
func assertAttachmentDownload(t *testing.T, router http.Handler, path string, version int64,
	institution, body string, code int,
) {
	t.Helper()
	values := url.Values{"version": {fmt.Sprint(version)}}
	if institution != "" {
		values.Set("institution", institution)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", path+"?"+values.Encode(), nil))
	if response.Code != code || code == 200 && response.Body.String() != body {
		t.Fatalf("下载结果不符: %s, %d, %s", path, response.Code, response.Body.String())
	}
}
