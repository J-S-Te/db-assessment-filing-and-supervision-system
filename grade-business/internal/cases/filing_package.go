package cases

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"grade-business/internal/wordtext"
)

// ErrFilingPackage 表示备案 ZIP 不符合业务材料要求。
var ErrFilingPackage = errors.New("备案材料包校验未通过")

const (
	filingPackageForm   = "备案表"
	filingPackageReport = "定级报告"
)

var filingTemplatePrefix = regexp.MustCompile(`^附件[0-9]+[._、 \x{3000}-]+`)
var filingTemplateEdition = regexp.MustCompile(`[（(][0-9]{4}版[）)]$`)

// validateFilingPackage 核验备案包的目录、两项必传材料和归档完整性。
// 参数 file 为暂存 ZIP，size 为其压缩后大小。
// 返回值 通过时为 nil，失败返回备案包错误及可展示的静态原因。
func validateFilingPackage(file *os.File, size int64) error {
	archive, err := zip.NewReader(file, size)
	if err != nil {
		return filingPackageError("请上传可正常打开的 ZIP，包内目录须为安全的相对路径")
	}
	members, err := filingPackageMembers(archive, maxPackageSize)
	if err != nil {
		return err
	}
	required, err := requiredFilingPackageFiles(members)
	if err != nil {
		return err
	}
	for _, member := range members {
		category := filingPackageCategory(member.Name)
		if member != required[category] {
			category = ""
		}
		if err := verifyFilingPackageMember(member, category); err != nil {
			return err
		}
	}
	return nil
}

// filingPackageError 将固定的校验原因标记为备案包业务错误。
// 参数 reason 为由服务端编写的静态提示。
// 返回值 可用 errors.Is 分类的错误。
func filingPackageError(reason string) error {
	return fmt.Errorf("%w，%s。", ErrFilingPackage, reason)
}

// filingPackageMembers 检查成员路径、类型、重复和完整解压上限。
// 参数 archive 为待验 ZIP，budget 为允许的总解压大小。
// 返回值 普通文件成员列表，或安全目录及大小错误。
func filingPackageMembers(archive *zip.Reader, budget uint64) ([]*zip.File, error) {
	if len(archive.File) == 0 || len(archive.File) > 2000 {
		return nil, filingPackageError("归档为空或条目过多")
	}
	paths := make(map[string]bool, len(archive.File))
	var files []*zip.File
	for _, member := range archive.File {
		name := strings.TrimSuffix(member.Name, "/")
		mode := member.Mode().Type()
		directory := mode == fs.ModeDir
		if !validFilingPackagePath(name) || (mode != 0 && !directory) {
			return nil, filingPackageError("归档目录或文件路径非法")
		}
		key := strings.ToLower(name)
		if _, duplicate := paths[key]; duplicate {
			return nil, filingPackageError("包含重复的归档路径")
		}
		paths[key] = directory
		if member.UncompressedSize64 > budget || (directory && member.UncompressedSize64 != 0) {
			return nil, filingPackageError("解压后总大小超过限制或目录包含非法数据")
		}
		budget -= member.UncompressedSize64
		if !directory {
			files = append(files, member)
		}
	}
	for name := range paths {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if directory, exists := paths[parent]; exists && !directory {
				return nil, filingPackageError("归档目录与文件路径冲突")
			}
		}
	}
	return files, nil
}

// validFilingPackagePath 校验归档路径可安全跨平台解释。
// 参数 name 为相对路径，不含目录结尾的斜杠。
// 返回值 路径非根目录、无穿越和控制字符时为真。
func validFilingPackagePath(name string) bool {
	return name != "." && len(name) <= 512 && utf8.ValidString(name) && fs.ValidPath(name) &&
		!strings.ContainsAny(name, "\\:") && strings.IndexFunc(name, unicode.IsControl) == -1
}

// requiredFilingPackageFiles 按标准命名选出同一目录下的备案表和定级报告。
// 参数 files 为已经完成路径边界检查的成员。
// 返回值 分类对应的文件，或缺失、重复和目录错误。
func requiredFilingPackageFiles(files []*zip.File) (map[string]*zip.File, error) {
	found := make(map[string]*zip.File, 2)
	for _, member := range files {
		if category := filingPackageCategory(member.Name); category != "" {
			if found[category] != nil {
				return nil, filingPackageError("备案表或定级报告重复，每类请保留一份")
			}
			found[category] = member
		}
	}
	form, report := found[filingPackageForm], found[filingPackageReport]
	switch {
	case form == nil && report == nil:
		return nil, filingPackageError("缺少备案表和定级报告，请按材料名称命名")
	case form == nil:
		return nil, filingPackageError("缺少备案表，请按材料名称命名")
	case report == nil:
		return nil, filingPackageError("缺少定级报告，请按材料名称命名")
	}
	folder := path.Dir(form.Name)
	if folder != path.Dir(report.Name) || strings.Contains(folder, "/") {
		return nil, filingPackageError("备案表和定级报告须放在根目录或顶层的同一目录")
	}
	if folder != "." {
		for _, member := range files {
			if !strings.HasPrefix(member.Name, folder+"/") && !filingPackageMetadata(member.Name) {
				return nil, filingPackageError("附加材料须放在备案表与定级报告所在的顶层目录中")
			}
		}
	}
	return found, nil
}

// filingPackageMetadata 识别不会作为业务材料归类的常见归档元数据。
// 参数 name 为已验证的归档成员路径。
// 返回值 系统归档元数据返回真。
func filingPackageMetadata(name string) bool {
	return strings.HasPrefix(name, "__MACOSX/") || path.Base(name) == ".DS_Store"
}

// filingPackageCategory 识别两类必传文件及常见名称，不把说明和草稿当作提交材料。
// 参数 name 为已验证的文件路径。
// 返回值 必传材料类别，其他材料返回空。
func filingPackageCategory(name string) string {
	if filingPackageMetadata(name) {
		return ""
	}
	base := path.Base(name)
	stem := strings.TrimSpace(strings.TrimSuffix(base, path.Ext(base)))
	stem = filingTemplatePrefix.ReplaceAllString(stem, "")
	stem = strings.TrimSpace(filingTemplateEdition.ReplaceAllString(stem, ""))
	stem = strings.TrimSuffix(strings.TrimSuffix(stem, "模板"), "模版")
	digits := strings.TrimLeft(stem, "0123456789")
	withoutNumber := strings.TrimLeft(digits, "._-、 　")
	if len(withoutNumber) < len(digits) && len(digits) < len(stem) {
		stem = withoutNumber
	}
	switch strings.ToLower(stem) {
	case "filing_form", "备案表", "备案登记表", "等保备案表",
		"信息系统安全等级保护备案表", "网络安全等级保护备案表",
		"信息系统安全等级保护备案登记表", "网络安全等级保护备案登记表",
		"信息系统安全等级保护定级备案表", "网络安全等级保护定级备案表":
		return filingPackageForm
	case "classification_report", "定级报告", "系统定级报告", "信息系统定级报告",
		"等级保护定级报告", "等保定级报告",
		"信息系统安全等级保护定级报告", "网络安全等级保护定级报告":
		return filingPackageReport
	default:
		return ""
	}
}

// verifyFilingPackageMember 读取成员以验证完整性，并检查必传材料的实际格式。
// 参数 member 为已验证路径和总大小的归档成员，category 为可选必传材料名。
// 返回值 成员内容错误或 nil。
func verifyFilingPackageMember(member *zip.File, category string) error {
	var content bytes.Buffer
	var destination io.Writer = io.Discard
	if category != "" {
		if member.UncompressedSize64 == 0 || member.UncompressedSize64 > maxDocumentSize {
			return filingPackageError(category + "为空或超过 20 MB")
		}
		destination = &content
	}
	if err := readFilingPackageMember(member, destination); err != nil {
		return filingPackageError("归档内有材料损坏，请重新打包")
	}
	if category == "" {
		return nil
	}
	switch strings.ToLower(path.Ext(member.Name)) {
	case ".pdf":
		if validFilingPackagePDF(content.Bytes()) {
			return nil
		}
		return filingPackageError(category + "的 PDF 内容损坏或不完整")
	case ".doc":
		if _, err := wordtext.DOC(bytes.NewReader(content.Bytes()), int64(content.Len())); err == nil {
			return nil
		}
		return filingPackageError(category + "的 Word（DOC）内容损坏、已加密或版本不受支持")
	case ".docx":
		if validFilingPackageDOCX(content.Bytes()) {
			return nil
		}
		return filingPackageError(category + "的 DOCX 内容损坏或结构错误")
	}
	return filingPackageError(category + "格式仅支持 PDF、DOC 或 DOCX")
}

// validFilingPackagePDF 检查 PDF 头部版本与末尾结束标记，识别常见截断。
// 参数 data 为经过归档校验和检查的原始文件内容。
// 返回值 PDF 头尾符合格式时为真。
func validFilingPackagePDF(data []byte) bool {
	return len(data) > 8 && bytes.HasPrefix(data, []byte("%PDF-")) &&
		(data[5] == '1' || data[5] == '2') && data[6] == '.' && data[7] >= '0' && data[7] <= '9' &&
		(data[8] == '\r' || data[8] == '\n') &&
		bytes.HasSuffix(bytes.TrimRight(data, " \r\n\t\f\x00"), []byte("%%EOF"))
}

// readFilingPackageMember 读取归档成员至结束，确保 ZIP 校验和得到核验。
// 参数 member 为待解压成员，destination 为内容缓冲或丢弃接收器。
// 返回值 打开、解压、校验和或大小错误。
func readFilingPackageMember(member *zip.File, destination io.Writer) error {
	reader, err := member.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	size, err := io.Copy(destination, io.LimitReader(reader, int64(member.UncompressedSize64)+1))
	if err != nil {
		return err
	}
	if uint64(size) != member.UncompressedSize64 {
		return zip.ErrFormat
	}
	return nil
}

// validFilingPackageDOCX 验证 Word 容器实际含有良构的类型清单和正文 XML。
// 参数 data 为外层备案包中有界的文件内容。
// 返回值 DOCX 结构完整时为真。
func validFilingPackageDOCX(data []byte) bool {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false
	}
	members, err := filingPackageMembers(archive, maxDocumentSize)
	if err != nil {
		return false
	}
	var manifest, document bool
	for _, member := range members {
		switch member.Name {
		case "[Content_Types].xml":
			manifest = validFilingPackageXML(member, "Types",
				"http://schemas.openxmlformats.org/package/2006/content-types")
		case "word/document.xml":
			document = validFilingPackageXML(member, "document",
				"http://schemas.openxmlformats.org/wordprocessingml/2006/main",
				"http://purl.oclc.org/ooxml/wordprocessingml/main")
		default:
			if readFilingPackageMember(member, io.Discard) != nil {
				return false
			}
		}
	}
	return manifest && document
}

// validFilingPackageXML 检查必需 XML 的唯一根节点和整份文档语法。
// 参数 member 为已校验归档大小的成员，root 与 namespaces 指定所需根元素。
// 返回值 XML 完整并匹配根元素时为真。
func validFilingPackageXML(member *zip.File, root string, namespaces ...string) bool {
	body, err := member.Open()
	if err != nil {
		return false
	}
	defer body.Close()
	limited := &io.LimitedReader{R: body, N: int64(member.UncompressedSize64) + 1}
	reader := xml.NewDecoder(limited)
	depth, roots := 0, 0
	for {
		token, err := reader.Token()
		if err != nil {
			return err == io.EOF && limited.N == 1 && roots == 1 && depth == 0
		}
		switch value := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || value.Name.Local != root ||
					!slices.Contains(namespaces, value.Name.Space) {
					return false
				}
			}
			depth++
			if depth > 256 {
				return false
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(value)) != 0 {
				return false
			}
		}
	}
}
