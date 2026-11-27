package cases

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFilingWordPreviewKeepsCitationPositions 核对分节和空段落不会改变依据段号。
// 参数 t 为测试上下文。
// 返回值 无。
func TestFilingWordPreviewKeepsCitationPositions(t *testing.T) {
	var file bytes.Buffer
	writer := zip.NewWriter(&file)
	part, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = part.Write([]byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:p/><w:p><w:r><w:t>一、单位基本情况</w:t></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>系统名称：平台</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
<w:p/><w:p><w:r><w:t>系统名称：平台</w:t></w:r></w:p></w:body></w:document>`))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	preview, err := parseFilingWord(bytes.NewReader(file.Bytes()), int64(file.Len()), "备案表.docx", filingPackageForm)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(preview.Sections)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"titleParagraph":2`, `"paragraphNumbers":[3,5]`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("缺少原件段号 %s，得到 %s", want, got)
		}
	}
}

// TestFilingWordPreviewSelectsExactMember 验证 ZIP 内同名附件只能按完整路径选择。
// 参数 t 为测试上下文。
// 返回值 无。
func TestFilingWordPreviewSelectsExactMember(t *testing.T) {
	const wrapper = `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:body><w:p><w:r><w:t>内容</w:t></w:r></w:p></w:body></w:document>`
	word := func(content string) string {
		return string(buildFilingPackage(t, filingPackageFixtureMember{
			"word/document.xml", strings.Replace(wrapper, "内容", content, 1),
		}))
	}
	archive := buildFilingPackage(t,
		filingPackageFixtureMember{"甲/意见.docx", word("甲案出处")},
		filingPackageFixtureMember{"乙/意见.docx", word("乙案出处")},
	)
	item := Attachment{FileName: "备案材料.zip", Size: int64(len(archive))}
	preview, err := filingTextDocuments(bytes.NewReader(archive), item, true, "乙/意见.docx")
	if err != nil || len(preview) != 1 || preview[0].Member != "乙/意见.docx" {
		t.Fatalf("未选中归档成员: %+v, %v", preview, err)
	}
	if got := preview[0].Sections[0].Paragraphs[0]; got != "乙案出处" {
		t.Fatalf("选中了同名的错误附件: %q", got)
	}
	_, err = filingTextDocuments(bytes.NewReader(archive), item, true, "意见.docx")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("仅提供文件名时应返回不存在: %v", err)
	}
}

// TestLegacyWordAcceptedOnlyForFilingMaterials 验证旧版 Word 仅能用于备案的两类材料。
// 参数 t 为测试上下文。
// 返回值 无。
func TestLegacyWordAcceptedOnlyForFilingMaterials(t *testing.T) {
	templatePath := os.Getenv("FILING_WORD_TEST_DOC")
	if templatePath == "" {
		t.Skip("未提供用于联验的旧版 Word 备案表")
	}
	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	h := endpoint{kind: filing, service: &Service{attachmentDir: t.TempDir()}}
	material := attachmentRow{Attachment: Attachment{Kind: "filing_form", FileName: filepath.Base(templatePath)}}
	if _, err := h.stageCaseMaterial(material, bytes.NewReader(data), false); err != nil {
		t.Fatalf("2025 版备案表上传失败: %v", err)
	}
	h.kind = report
	material.Kind = "report_file"
	if _, err := h.stageCaseMaterial(material, bytes.NewReader(data), false); err == nil {
		t.Fatal("测评报告入口不应开放给旧版 Word")
	}
}

// TestFilingWordPreviewReads2025Templates 验证两份原始 Word 模板内的章节和表格标签。
// 参数 t 为测试上下文。
// 返回值 无。
func TestFilingWordPreviewReads2025Templates(t *testing.T) {
	templatePath := os.Getenv("FILING_WORD_TEST_DOC")
	if templatePath == "" {
		t.Skip("未提供用于联验的 2025 版 Word 模板目录")
	}
	files := []struct {
		path, category string
		words          []string
	}{
		{templatePath, filingPackageForm, []string{"单位基本情况", "单位统一社会信用代码", "定级对象安全保护等级"}},
		{filepath.Join(filepath.Dir(templatePath), "附件1 网络安全等级保护定级报告模版（2025版）.docx"),
			filingPackageReport, []string{"定级对象描述", "业务信息安全保护等级", "定级对象名称"}},
	}
	for _, fixture := range files {
		data, err := os.ReadFile(fixture.path)
		if err != nil {
			t.Fatal(err)
		}
		result, err := parseFilingWord(bytes.NewReader(data), int64(len(data)), filepath.Base(fixture.path), fixture.category)
		if err != nil || result.Truncated {
			t.Fatalf("%s preview: %v", fixture.category, err)
		}
		var text strings.Builder
		for _, section := range result.Sections {
			text.WriteString(section.Title)
			for _, paragraph := range section.Paragraphs {
				text.WriteString(paragraph)
			}
		}
		for _, word := range fixture.words {
			if !strings.Contains(text.String(), word) {
				t.Errorf("%s 未读取到 %s", fixture.category, word)
			}
		}
	}
}

// TestFilingPackageRecognizesVersionedTemplates 验证上载原始模板名称时可区分备案表和定级报告。
// 参数 t 为测试上下文。
// 返回值 无。
func TestFilingPackageRecognizesVersionedTemplates(t *testing.T) {
	for name, want := range map[string]string{
		"附件1 网络安全等级保护定级报告模版（2025版）.docx": filingPackageReport,
		"附件2 网络安全等级保护备案表（2025版）.doc":     filingPackageForm,
		"附件1 网络安全等级保护定级报告草稿（2025版）.docx": "",
	} {
		if got := filingPackageCategory(name); got != want {
			t.Errorf("%s classified as %q, want %q", name, got, want)
		}
	}
}
