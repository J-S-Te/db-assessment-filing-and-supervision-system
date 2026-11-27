package remediations

import (
	"archive/zip"
	"bytes"
	"io"
	"mime"
	"net/http/httptest"
	"strings"
	"testing"

	"grade-business/internal/cases"
)

const testTemplateOwner = "?organization=测试单位"

// TestMaterialTemplatesDownload 验证三类模板独立下载、内容可填写且下载不会登记材料。
// 参数 t 为隔离数据库和 HTTP 测试上下文。
// 返回值 无。
func TestMaterialTemplatesDownload(t *testing.T) {
	db, router := remediationFixture(t)
	report, _ := insertReports(t, db)
	created := response[mutation](t, call(router, "POST", rootURL, newPlanInput(report)), 201)
	planPath := rootURL + "/" + created.ID
	operatorPlan := operatorRoot + "/" + created.ID + "/material-templates/"
	regulatorReport := rootURL + "/eligible-reports/" + report + "/material-templates/"
	for _, template := range []struct{ kind, name, part, field string }{
		{"plan", "整改规划方案模板.docx", "word/document.xml", "验收标准"},
		{"responsibility", "整改责任分工表模板.xlsx", "xl/worksheets/sheet1.xml", "协作部门"},
		{"schedule", "整改实施计划表模板.xlsx", "xl/worksheets/sheet1.xml", "计划完成日期"},
	} {
		t.Run(template.kind, func(t *testing.T) {
			reportDownload := call(router, "GET", regulatorReport+template.kind, nil)
			assertMaterialTemplate(t, reportDownload, template.name, template.part, template.field)
			planDownload := call(router, "GET", operatorPlan+template.kind+testTemplateOwner, nil)
			assertMaterialTemplate(t, planDownload, template.name, template.part, template.field)
			if !bytes.Equal(reportDownload.Body.Bytes(), planDownload.Body.Bytes()) {
				t.Fatal("同类空白模板应独立于规划和报告的实际数据")
			}
		})
	}
	files := response[collection[cases.Attachment]](t, call(router, "GET", planPath+"/attachments", nil), 200)
	plan := response[detail](t, call(router, "GET", planPath, nil), 200)
	submit := response[map[string]string](t, call(router, "POST", planPath+"/submit", submitInput{1}), 400)
	if len(files.Items) != 0 || plan.Version != created.Version || submit["code"] != "materials_required" {
		t.Fatalf("模板下载改变了材料门禁或规划版本，详情 %+v，提交响应 %v", plan, submit)
	}
}

// TestMaterialTemplateScope 验证运营者下载权限跟随有效备案、当前单位和报告资格。
// 参数 t 为隔离数据库和 HTTP 测试上下文。
// 返回值 无。
func TestMaterialTemplateScope(t *testing.T) {
	db, router := remediationFixture(t)
	report, pending := insertReports(t, db)
	created := response[mutation](t, call(router, "POST", rootURL, newPlanInput(report)), 201)
	const template = "/material-templates/plan"
	reportPath := operatorRoot + "/eligible-reports/"
	planPath := operatorRoot + "/" + created.ID + template

	for _, test := range []struct {
		name, path string
		status     int
	}{
		{"缺少单位", planPath, 400},
		{"重复单位", planPath + testTemplateOwner + "&organization=其他单位", 400},
		{"其他备案单位", planPath + "?organization=其他单位", 404},
		{"其他单位报告", reportPath + report + template + "?organization=其他单位", 404},
		{"未通过报告", reportPath + pending + template + testTemplateOwner, 404},
		{"未知材料类型", operatorRoot + "/" + created.ID + "/material-templates/high_risk_evidence" + testTemplateOwner, 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := call(router, "GET", test.path, nil)
			if result.Code != test.status || result.Header().Get("Content-Disposition") != "" {
				t.Fatalf("预期无附件的 HTTP %d，实际响应 %d %s", test.status, result.Code, result.Body.String())
			}
		})
	}

	later := insertLaterReport(t, db, report)
	response[map[string]string](t, call(router, "GET", reportPath+report+template+testTemplateOwner, nil), 404)
	assertMaterialTemplate(t, call(router, "GET", reportPath+later+template+testTemplateOwner, nil),
		"整改规划方案模板.docx", "word/document.xml", "整改目标")
	assertMaterialTemplate(t, call(router, "GET", planPath+testTemplateOwner, nil),
		"整改规划方案模板.docx", "word/document.xml", "整改目标")
	if _, err := db.Exec(t.Context(), "UPDATE business_cases SET cancelled_at=now() WHERE kind='filing'"); err != nil {
		t.Fatal(err)
	}
	response[map[string]string](t, call(router, "GET", reportPath+later+template+testTemplateOwner, nil), 404)
	response[map[string]string](t, call(router, "GET", planPath+testTemplateOwner, nil), 404)
}

// assertMaterialTemplate 核对真实可解析的 Office 文件、附件头及填报内容。
// 参数 t 为测试上下文，result 为下载响应，name 为文件名，part 为内容 XML，field 为必含字段。
// 返回值 无。
func assertMaterialTemplate(t *testing.T, result *httptest.ResponseRecorder, name, part, field string) {
	t.Helper()
	if result.Code != 200 {
		t.Fatalf("下载模板预期 HTTP 200，实际 %d %s", result.Code, result.Body.String())
	}
	disposition, params, err := mime.ParseMediaType(result.Header().Get("Content-Disposition"))
	if err != nil || disposition != "attachment" || params["filename"] != name ||
		result.Header().Get("Cache-Control") != "no-store" ||
		!strings.HasPrefix(result.Header().Get("Content-Type"), "application/vnd.openxmlformats-officedocument.") {
		t.Fatalf("不符合预期的下载响应头 %v", result.Header())
	}
	file, err := zip.NewReader(bytes.NewReader(result.Body.Bytes()), int64(result.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	xml, err := file.Open(part)
	if err != nil {
		t.Fatal(err)
	}
	defer xml.Close()
	data, err := io.ReadAll(xml)
	if err != nil || !bytes.Contains(data, []byte(field)) || !bytes.Contains(data, []byte("本地填报参考")) {
		t.Fatalf("%s 缺少填报字段或用途提示，读取错误 %v", name, err)
	}
}
