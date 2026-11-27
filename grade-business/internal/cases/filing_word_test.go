package cases

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
