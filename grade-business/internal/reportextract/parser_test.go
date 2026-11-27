package reportextract

import (
	"archive/zip"
	"bytes"
	"errors"
	"testing"
)

// TestExtractDOCX 验证真实 DOCX 中的分节、表格单元及可定位的原文。
// 参数 t 为测试结果接收器。
// 返回值 无。
func TestExtractDOCX(t *testing.T) {
	document := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
	<w:body><w:p><w:r><w:t>测评报告</w:t></w:r></w:p>
	<w:p><w:r><w:t>资产清单</w:t></w:r><w:r><w:t>：业务管理服务</w:t></w:r></w:p>
	<w:tbl><w:tr><w:tc><w:p><w:r><w:t>漏洞详情</w:t></w:r></w:p></w:tc>
	<w:tc><w:p><w:r><w:t>日志留存周期不满足基线</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
	<w:p><w:r><w:t>风险等级：中风险</w:t></w:r></w:p>
	<w:p><w:r><w:t>整改建议：延长留存周期</w:t></w:r></w:p></w:body></w:document>`
	source := docx(t, document)
	got, err := extractDOCX(bytes.NewReader(source), int64(len(source)))
	if err != nil || len(got) != 4 {
		t.Fatalf("got %#v, error %v", got, err)
	}
	if got[0].Key != "assets" || got[0].Original != "业务管理服务" || got[0].Start != 2 ||
		got[1].Key != "findings" || got[1].Original != "日志留存周期不满足基线" ||
		got[1].Start != 3 || got[1].End != 4 ||
		got[2].Key != "risk" || got[2].Original != "中风险" ||
		got[3].Key != "recommendations" || got[3].Original != "延长留存周期" {
		t.Fatalf("unexpected extraction: %#v", got)
	}
}

// TestExtractRejectsUnrecognizedReport 验证无可识别字段时返回明确错误。
// 参数 t 为测试结果接收器。
// 返回值 无。
func TestExtractRejectsUnrecognizedReport(t *testing.T) {
	source := docx(t, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
	<w:body><w:p><w:r><w:t>只有封面</w:t></w:r></w:p></w:body></w:document>`)
	_, err := extractDOCX(bytes.NewReader(source), int64(len(source)))
	if !errors.Is(err, errNoFields) {
		t.Fatalf("got %v", err)
	}
}

// docx 生成含 Word 正文及内容清单的本地测试报告。
// 参数 t 为测试结果接收器，document 为要写入的正文 XML。
// 返回值 DOCX 归档字节。
func docx(t *testing.T, document string) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, content := range map[string]string{
		"[Content_Types].xml": "<Types/>", "word/document.xml": document,
	} {
		part, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
