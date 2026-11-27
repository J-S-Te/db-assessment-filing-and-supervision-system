package cases

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

const filingPackagePDF = "%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n%%EOF\n"

type filingPackageFixtureMember struct {
	name, body string
}

// TestFilingPackageValidation 验证备案包目录、必传材料和内容检查。
// 参数 t 为测试上下文。
// 返回值 无。
func TestFilingPackageValidation(t *testing.T) {
	form := filingPackageFixtureMember{"备案表.pdf", filingPackagePDF}
	report := filingPackageFixtureMember{"定级报告.pdf", filingPackagePDF}
	manifest := filingPackageFixtureMember{"[Content_Types].xml", strings.TrimSpace(`
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>
`)}
	document := filingPackageFixtureMember{"word/document.xml", strings.TrimSpace(`
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body/>
</w:document>
`)}
	docx := buildFilingPackage(t, manifest, document)
	document.body = strings.Replace(document.body, "<w:body/>", "<w:body>", 1)
	badDocx := buildFilingPackage(t, manifest, document)
	for _, test := range []struct {
		name, reason string
		members      []filingPackageFixtureMember
	}{
		{"标准中文命名", "", []filingPackageFixtureMember{form, report}},
		{"标准英文命名", "", []filingPackageFixtureMember{
			{"filing_form.pdf", filingPackagePDF},
			{"classification_report.pdf", filingPackagePDF},
		}},
		{"带目录和佐证材料的混合格式", "", []filingPackageFixtureMember{
			{"项目/01_信息系统安全等级保护备案表.pdf", filingPackagePDF},
			{"项目/02-网络安全等级保护定级报告.docx", string(docx)},
			{"项目/附录/其他资料.txt", "测试佐证"},
		}},
		{"缺少备案表", "缺少备案表", []filingPackageFixtureMember{report}},
		{"缺少定级报告", "缺少定级报告", []filingPackageFixtureMember{form}},
		{"两项必传均缺少", "缺少备案表和定级报告", []filingPackageFixtureMember{
			{"其他说明.pdf", filingPackagePDF},
		}},
		{"目录分散", "同一目录", []filingPackageFixtureMember{
			form, {"另一目录/定级报告.pdf", filingPackagePDF},
		}},
		{"重复类别", "重复", []filingPackageFixtureMember{
			form, report, {"信息系统安全等级保护备案表.pdf", filingPackagePDF},
		}},
		{"重复路径", "重复", []filingPackageFixtureMember{form, report, form}},
		{"不安全路径", "目录", []filingPackageFixtureMember{
			form, report, {"../不应泄露的名称.txt", "测试"},
		}},
		{"文件作为目录", "目录", []filingPackageFixtureMember{
			form, report, {"附件", "单个文件"}, {"附件/子文件.txt", "不允许"},
		}},
		{"读取损坏的 PDF", "PDF", []filingPackageFixtureMember{
			{"备案表.pdf", "%PDF-1.7\n缺少结束标记"}, report,
		}},
		{"读取伪造 PDF 版本", "PDF", []filingPackageFixtureMember{
			{"备案表.pdf", "%PDF-bad\n%%EOF\n"}, report,
		}},
		{"读取伪造的 DOCX", "DOCX", []filingPackageFixtureMember{
			{"备案表.docx", "纯文本"}, report,
		}},
		{"读取损坏的内部 DOCX", "DOCX", []filingPackageFixtureMember{
			{"备案表.docx", string(badDocx)}, report,
		}},
		{"读取名称伪装的文件", "格式", []filingPackageFixtureMember{
			{"备案表.txt", "纯文本"}, report,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateFilingPackageFixture(t, buildFilingPackage(t, test.members...))
			if test.reason == "" {
				if err != nil {
					t.Fatalf("应通过备案包校验，得到 %v", err)
				}
				return
			}
			if !errors.Is(err, ErrFilingPackage) || !strings.Contains(err.Error(), test.reason) ||
				strings.Contains(err.Error(), "不应泄露的名称") {
				t.Fatalf("应给出可操作的 %q 错误，得到 %v", test.reason, err)
			}
		})
	}
}

// TestFilingPackageExpandedSize 验证按归档成员的实际声明限制总解压规模。
// 参数 t 为测试上下文。
// 返回值 无。
func TestFilingPackageExpandedSize(t *testing.T) {
	data := buildFilingPackage(t,
		filingPackageFixtureMember{"备案表.pdf", filingPackagePDF},
		filingPackageFixtureMember{"定级报告.pdf", filingPackagePDF},
	)
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = filingPackageMembers(reader, 2*uint64(len(filingPackagePDF))-1)
	if !errors.Is(err, ErrFilingPackage) || !strings.Contains(err.Error(), "解压后总大小") {
		t.Fatalf("应拒绝总解压量超限，得到 %v", err)
	}
}

// TestFilingPackageCorruptMember 验证归档目录完整时仍能检测成员校验和错误。
// 参数 t 为测试上下文。
// 返回值 无。
func TestFilingPackageCorruptMember(t *testing.T) {
	const marked = "可定位的佐证文本"
	body := buildFilingPackage(t,
		filingPackageFixtureMember{"备案表.pdf", filingPackagePDF},
		filingPackageFixtureMember{"定级报告.pdf", filingPackagePDF},
		filingPackageFixtureMember{"佐证.txt", marked},
	)
	index := bytes.Index(body, []byte(marked))
	if index < 0 {
		t.Fatal("未找到未压缩成员")
	}
	body[index] ^= 1
	err := validateFilingPackageFixture(t, body)
	if err == nil || !strings.Contains(err.Error(), "损坏") {
		t.Fatalf("应报告成员内容损坏，得到 %v", err)
	}
}

// buildFilingPackage 生成不压缩的测试 ZIP，以便构造可定位的成员损坏。
// 参数 t 为测试上下文，members 为成员名及内容。
// 返回值 测试归档字节。
func buildFilingPackage(t *testing.T, members ...filingPackageFixtureMember) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, item := range members {
		member, err := writer.CreateHeader(&zip.FileHeader{Name: item.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write([]byte(item.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// validateFilingPackageFixture 将测试归档写入临时文件并调用实际文件校验入口。
// 参数 t 为测试上下文，data 为归档字节。
// 返回值 备案包校验结果。
func validateFilingPackageFixture(t *testing.T, data []byte) error {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "package-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	return validateFilingPackage(file, int64(len(data)))
}
