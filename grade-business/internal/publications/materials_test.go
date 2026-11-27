package publications

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// TestOperatorMaterialsPublicationLifecycle 验证资料库仅提供已送达当前版本及真实文件。
// 参数 t 为隔离数据库和临时文件测试上下文。
// 返回值 无。
func TestOperatorMaterialsPublicationLifecycle(t *testing.T) {
	pool, router, _ := attachmentFixture(t)
	seedPublicationRecipient(t, pool, "MATERIAL-OTHER", "其他单位", "收件机构", "approved", false)
	createMaterialDraft(t, router, "announcement", "通常公告")
	form, material := createMaterialDraft(t, router, "common_material", "监督检查自查表")
	path := adminPath + "/" + material.ID
	list := readerPath + "?group=materials&organization=" + url.QueryEscape("收件单位")
	assertMaterialList(t, router, list, "", 0)
	reject := pubRequest[map[string]string](t, router, "POST", path+"/actions",
		map[string]any{"action": "publish", "expectedVersion": 1}, 400)
	if reject["code"] != "material_attachment_required" {
		t.Fatalf("无附件时应提示补充资料: %+v", reject)
	}
	broken := attachmentPart{"损坏表单.pdf", "%PDF-1.4\nchecked material\n%%EOF\n"}
	brokenDraft := readAttachmentResponse(t, uploadAttachmentRequest(router, path, 1, broken), 200)
	pubAction(t, router, path, "publish", brokenDraft.Version, 400)
	clean := pubRequest[publicationFixtureItem](t, router, "DELETE", path+"/attachments/"+brokenDraft.Attachments[0].ID,
		map[string]any{"expectedVersion": brokenDraft.Version}, 200)
	file := attachmentPart{"自查表.pdf", materialTestPDF(t)}
	saved := readAttachmentResponse(t, uploadAttachmentRequest(router, path, clean.Version, file), 200)
	published := pubAction(t, router, path, "publish", saved.Version, 200)
	assertMaterialList(t, router, list+"&category=common_material&q="+url.QueryEscape("自查表"), saved.Attachments[0].ID, published.Version)
	assertMaterialList(t, router, list+"&category=policy_standard", "", 0)
	assertMaterialList(t, router, readerPath+"?group=materials&organization="+url.QueryEscape("其他单位"), "", 0)
	pubRequest[map[string]string](t, router, "GET", readerPath+"?group=materials", nil, 400)
	checkMaterialRevision(t, router, material.ID, published.Version, saved.Attachments[0].ID, file, form)
}

// createMaterialDraft 建立指定类别的公告，常规公告用于比较资料筛选。
// 参数 t 为测试上下文，router 为隔离路由，category 和 title 为拟提交的类别及标题。
// 返回值 草稿参数与保存的记录。
func createMaterialDraft(t *testing.T, router http.Handler, category, title string) (map[string]any, publicationFixtureItem) {
	t.Helper()
	form := map[string]any{"category": category, "title": title, "publisher": "验收监管", "body": "使用说明",
		"audience": "selected", "targets": []recipient{{Audience: operatorAudience, Recipient: "收件单位"}}}
	item := pubRequest[publicationFixtureItem](t, router, "POST", adminPath, form, 201)
	if category == "announcement" {
		pubAction(t, router, adminPath+"/"+item.ID, "publish", 1, 200)
	}
	return form, item
}

// assertMaterialList 核对资料列表返回的当前版本和可下载附件。
// 参数 t 为测试上下文，router 为隔离路由，path 为带范围的查询，fileID 和 version 为预期附件及版本，空附件表示列表应为空。
// 返回值 无。
func assertMaterialList(t *testing.T, router http.Handler, path, fileID string, version int64) {
	t.Helper()
	list := pubRequest[struct{ Items []attachmentTestDetail }](t, router, "GET", path, nil, 200)
	if fileID == "" && len(list.Items) == 0 {
		return
	}
	if fileID == "" || len(list.Items) != 1 || list.Items[0].Version != version ||
		len(list.Items[0].Attachments) != 1 || list.Items[0].Attachments[0].ID != fileID {
		t.Fatalf("资料目录未与线上版本一致: %+v", list)
	}
}

// checkMaterialRevision 核对分类修订、撤回和旧版回执的附件范围。
// 参数 t 为测试上下文，router 为实际路由，id、version、fileID、file 及 form 为已发布资料和修订表单。
// 返回值 无。
func checkMaterialRevision(t *testing.T, router http.Handler, id string, version int64,
	fileID string, file attachmentPart, form map[string]any,
) {
	t.Helper()
	path := adminPath + "/" + id
	publicFile := readerPath + "/" + id + "/attachments/" + fileID
	publicationDownload := func(version int64, organization, body string, want int) {
		assertMaterialDownload(t, router, publicFile, version, organization, body, want)
	}
	publicationDownload(version, "收件单位", file.body, 200)
	publicationDownload(version, "其他单位", "", 404)
	form["category"], form["expectedVersion"] = "policy_standard", version
	edited := pubRequest[publicationFixtureItem](t, router, "PATCH", path, form, 200)
	list := readerPath + "?group=materials&organization=" + url.QueryEscape("收件单位")
	assertMaterialList(t, router, list+"&category=policy_standard", "", 0)
	latest := pubAction(t, router, path, "publish", edited.Version, 200)
	assertMaterialList(t, router, list+"&category=policy_standard", fileID, latest.Version)
	publicationDownload(version, "收件单位", "", 404)
	pubAction(t, router, path, "withdraw", latest.Version, 200)
	assertMaterialList(t, router, list, "", 0)
	publicationDownload(latest.Version, "收件单位", "", 404)
	assertAttachmentDownload(t, router, path+"/attachments/"+fileID, version, "", file.body, 200)
}

// assertMaterialDownload 核对运营单位使用指定发布版本获取文件的结果。
// 参数 t 为测试上下文，router 为隔离路由，path、version、organization 为下载范围，body、want 为预期内容和状态。
// 返回值 无。
func assertMaterialDownload(t *testing.T, router http.Handler, path string, version int64,
	organization, body string, want int,
) {
	t.Helper()
	values := url.Values{"version": {strconv.FormatInt(version, 10)}, "organization": {organization}}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", path+"?"+values.Encode(), nil))
	if response.Code != want || want == 200 && response.Body.String() != body {
		t.Fatalf("资料下载结果不符: %d, %s", response.Code, response.Body.String())
	}
}

// TestBroadcastMaterialsStillRequireDeliveredOrganization 验证全部推送的资料也只向明确收件方展示。
// 参数 t 为隔离库与实际 HTTP 路由测试上下文。
// 返回值 无。
func TestBroadcastMaterialsStillRequireDeliveredOrganization(t *testing.T) {
	_, router, _ := attachmentFixture(t)
	form := map[string]any{"category": "policy_standard", "title": "广播合成标准",
		"publisher": "验收监管", "body": "使用须知", "audience": "all"}
	item := pubRequest[publicationFixtureItem](t, router, "POST", adminPath, form, 201)
	path := adminPath + "/" + item.ID
	file := attachmentPart{"政策说明.pdf", materialTestPDF(t)}
	saved := readAttachmentResponse(t, uploadAttachmentRequest(router, path, 1, file), 200)
	pubAction(t, router, path, "publish", saved.Version, 200)

	portal := readerPath + "/" + item.ID
	pubRequest[publicationFixtureItem](t, router, "GET", portal+"?organization="+url.QueryEscape("收件单位"), nil, 200)
	pubRequest[map[string]string](t, router, "GET", portal, nil, 404)
	assertMaterialList(t, router, readerPath+"?category=policy_standard", "", 0)
}

// materialTestPDF 构造带有效页树与交叉引用表的最小测试 PDF。
// 参数 t 为运行 PDF 校验的测试上下文。
// 返回值 合成的 PDF 字节对应的字符串。
func materialTestPDF(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("pdfinfo"); err != nil {
		t.Skip("资料发布验证需要本地 pdfinfo")
	}
	var pdf strings.Builder
	pdf.WriteString("%PDF-1.4\n")
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>",
	}
	var offsets []int
	for n, object := range objects {
		offsets = append(offsets, pdf.Len())
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", n+1, object)
	}
	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Root 1 0 R /Size %d >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return pdf.String()
}
