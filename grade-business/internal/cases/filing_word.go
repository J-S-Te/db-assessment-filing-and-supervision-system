package cases

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"grade-business/internal/httpx"
	"grade-business/internal/wordtext"
)

const maxFilingPreviewChars = 40000

var filingSectionHeading = regexp.MustCompile(`^(表[一二三四五六七八九十]+[ 　（(]|[一二三四五六七八九十]+、)`)
var errLegacyFilingWord = fmt.Errorf("%w: unreadable Word 97–2003 document", ErrFileType)

type filingTextSection struct {
	Title      string   `json:"title"`
	Paragraphs []string `json:"paragraphs"`
}

type filingTextDocument struct {
	FileName  string              `json:"fileName"`
	Category  string              `json:"category"`
	Sections  []filingTextSection `json:"sections"`
	Truncated bool                `json:"truncated,omitempty"`
	Message   string              `json:"message,omitempty"`
}

// isFilingWordKind 限定允许上传旧版 Word 的两类独立备案材料。
// 参数 kind 为当前材料类别。
// 返回值 是备案表或定级报告时为真。
func isFilingWordKind(kind string) bool {
	return kind == "filing_form" || kind == "classification_report"
}

// validateLegacyFilingWord 校验旧版 Word 的真实正文，保持文件原样入库。
// 参数 file 为已经限量暂存的附件，_ 为调用方已检查的扩展名，size 为实际文件大小。
// 返回值 可读取 Word 正文时为空，否则返回附件格式错误。
func validateLegacyFilingWord(file *os.File, _ string, size int64) error {
	if _, err := wordtext.DOC(file, size); err != nil {
		return errLegacyFilingWord
	}
	return nil
}

// filingMaterialText 读取监管端当前备案的 Word 材料预览。
// 参数 w 和 r 为响应和包含备案及附件标识的请求。
// 返回值 无。
func (h endpoint) filingMaterialText(w http.ResponseWriter, r *http.Request) {
	h.filingMaterialTextFromScope(w, r, "")
}

// filingMaterialTextFromScope 沿用下载归属规则，按需返回原件的正文和表格文字。
// 参数 w 和 r 为响应及请求，institution 为调用方选定的测评机构范围。
// 返回值 无，成功返回中文文字分节，失败返回可展示的静态错误。
func (h endpoint) filingMaterialTextFromScope(w http.ResponseWriter, r *http.Request, institution string) {
	item, err := h.service.readAttachment(r.Context(), filing, r.PathValue("id"), r.PathValue("attachmentId"), institution)
	if err != nil {
		respondError(w, err)
		return
	}
	extension := strings.ToLower(path.Ext(item.FileName))
	isPackage := item.Kind == "filing_package" && extension == ".zip"
	if !isPackage && (!isFilingWordKind(item.Kind) || (extension != ".doc" && extension != ".docx")) {
		httpx.Error(w, 415, "unsupported_filing_word", "请选择备案表或定级报告的 DOC、DOCX 文件，或备案材料包。")
		return
	}
	file, err := openAttachment(h.service.attachmentDir, item.fileKey)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			err = ErrNotFound
		}
		respondError(w, err)
		return
	}
	defer file.Close()
	documents, err := filingTextDocuments(file, item.Attachment, isPackage)
	if err != nil {
		httpx.Error(w, 422, "filing_word_unreadable", "无法读取材料文字，请核对原件格式、加密状态或文件完整性。")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, 200, itemResponse[filingTextDocument]{Items: documents})
}

// filingTextDocuments 解析单件 Word，或从已保存备案 ZIP 分别读取两类正式材料。
// 参数 source 为受控附件，item 为外层附件元数据，isPackage 为备案 ZIP 标识。
// 返回值 与实际材料一一对应的解析结果，或文件解析错误。
func filingTextDocuments(source io.ReaderAt, item Attachment, isPackage bool) ([]filingTextDocument, error) {
	if !isPackage {
		category := filingPackageForm
		if item.Kind == "classification_report" {
			category = filingPackageReport
		}
		file, err := parseFilingWord(source, item.Size, item.FileName, category)
		if err != nil {
			return nil, err
		}
		return []filingTextDocument{file}, nil
	}
	archive, err := zip.NewReader(source, item.Size)
	if err != nil {
		return nil, err
	}
	members, err := filingPackageMembers(archive, maxPackageSize)
	if err != nil {
		return nil, err
	}
	files, err := requiredFilingPackageFiles(members)
	if err != nil {
		return nil, err
	}
	result := make([]filingTextDocument, 0, 2)
	for _, category := range []string{filingPackageForm, filingPackageReport} {
		result = append(result, previewFilingMember(files[category], category))
	}
	return result, nil
}

// previewFilingMember 对备案包内一项正式材料解析文字，保留其他材料的结果。
// 参数 member 为已定位的正式材料，category 为页面展示的业务类别。
// 返回值 该成员的正文或简短提示。
func previewFilingMember(member *zip.File, category string) filingTextDocument {
	name := path.Base(member.Name)
	result := filingTextDocument{FileName: name, Category: category, Sections: []filingTextSection{}}
	extension := strings.ToLower(path.Ext(name))
	if extension != ".doc" && extension != ".docx" {
		result.Message = "当前材料为 PDF，请下载原件查看。"
		return result
	}
	if member.UncompressedSize64 <= maxDocumentSize {
		var content bytes.Buffer
		if err := readFilingPackageMember(member, &content); err == nil {
			parsed, err := parseFilingWord(bytes.NewReader(content.Bytes()), int64(content.Len()), name, category)
			if err == nil {
				return parsed
			}
		}
	}
	result.Message = "无法读取该材料文字，请下载核对原件。"
	return result
}

// parseFilingWord 根据格式读取文字，并按模板可识别的章节和表名组织预览。
// 参数 source 含 Word 原件，size 为大小，name 和 category 用于展示材料身份。
// 返回值 保留顺序的正文和表格文字，或文档无法解析错误。
func parseFilingWord(source io.ReaderAt, size int64, name, category string) (filingTextDocument, error) {
	read := wordtext.DOCX
	if strings.EqualFold(path.Ext(name), ".doc") {
		read = wordtext.DOC
	}
	paragraphs, err := read(source, size)
	if err != nil {
		return filingTextDocument{}, err
	}
	result := filingTextDocument{FileName: name, Category: category}
	section := filingTextSection{Title: "正文"}
	left := maxFilingPreviewChars
	for _, text := range paragraphs {
		if text == "" {
			continue
		}
		count := utf8.RuneCountInString(text)
		if count > left {
			if left > 0 {
				section.Paragraphs = append(section.Paragraphs, string([]rune(text)[:left]))
			}
			result.Truncated = true
			break
		}
		left -= count
		if filingSectionHeading.MatchString(text) {
			if len(section.Paragraphs) > 0 || section.Title != "正文" {
				result.Sections = append(result.Sections, section)
			}
			section = filingTextSection{Title: text, Paragraphs: []string{}}
		} else {
			section.Paragraphs = append(section.Paragraphs, text)
		}
	}
	if len(section.Paragraphs) > 0 || section.Title != "正文" {
		result.Sections = append(result.Sections, section)
	}
	if len(result.Sections) == 0 {
		return filingTextDocument{}, wordtext.ErrInvalid
	}
	return result, nil
}
