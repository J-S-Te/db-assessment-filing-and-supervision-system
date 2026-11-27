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

	"grade-business/internal/access"
	"grade-business/internal/httpx"
	"grade-business/internal/wordtext"
)

const maxFilingPreviewChars = 40000

var filingSectionHeading = regexp.MustCompile(`^(表[一二三四五六七八九十]+[ 　（(]|[一二三四五六七八九十]+、)`)
var errLegacyFilingWord = fmt.Errorf("%w: unreadable Word 97–2003 document", ErrFileType)

type filingTextSection struct {
	Title            string   `json:"title"`
	TitleParagraph   int      `json:"titleParagraph,omitempty"`
	Paragraphs       []string `json:"paragraphs"`
	ParagraphNumbers []int    `json:"paragraphNumbers"`
}

type filingTextDocument struct {
	FileName  string              `json:"fileName"`
	Member    string              `json:"member,omitempty"`
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
	review := r.URL.Query().Get("review") == "1"
	var member string
	if review {
		actor, ok := access.Current(r.Context())
		if !ok || actor.Role != access.Regulator && actor.Role != access.SystemAdmin {
			httpx.Error(w, http.StatusForbidden, "permission_denied", "当前账号无权审阅备案材料。")
			return
		}
		member = r.URL.Query().Get("member")
	}
	item, err := h.service.readAttachment(r.Context(), filing, r.PathValue("id"), r.PathValue("attachmentId"), institution)
	if err != nil {
		respondError(w, err)
		return
	}
	extension := strings.ToLower(path.Ext(item.FileName))
	isPackage := item.Kind == "filing_package" && extension == ".zip"
	isWord := extension == ".doc" || extension == ".docx"
	if !isPackage && (member != "" || !isWord || !review && !isFilingWordKind(item.Kind)) {
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
	documents, err := filingTextDocuments(file, item.Attachment, isPackage, member)
	if errors.Is(err, ErrNotFound) {
		respondError(w, err)
		return
	}
	if err != nil {
		httpx.Error(w, 422, "filing_word_unreadable", "无法读取材料文字，请核对原件格式、加密状态或文件完整性。")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, 200, itemResponse[filingTextDocument]{Items: documents})
}

// filingTextDocuments 解析单件 Word，或从已保存备案 ZIP 分别读取两类正式材料。
// 参数 source 为受控附件，item 为外层附件元数据，isPackage 为备案 ZIP 标识，member 为指定成员路径。
// 返回值 与实际材料一一对应的解析结果，或文件解析错误。
func filingTextDocuments(source io.ReaderAt, item Attachment, isPackage bool, member string) ([]filingTextDocument, error) {
	if !isPackage {
		category := "其他材料"
		switch item.Kind {
		case "filing_form":
			category = filingPackageForm
		case "classification_report":
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
	if member != "" {
		return filingTextMember(members, member)
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

// filingTextMember 从受控归档成员中按完整路径选择预览目标。
// 参数 members 是已校验的成员，name 是审核依据中的完整路径。
// 返回值 单份文档的文字预览，或成员不存在错误。
func filingTextMember(members []*zip.File, name string) ([]filingTextDocument, error) {
	for _, entry := range members {
		if entry.Name != name || filingPackageMetadata(entry.Name) {
			continue
		}
		category := filingPackageCategory(entry.Name)
		if category == "" {
			category = "其他材料"
		}
		return []filingTextDocument{previewFilingMember(entry, category)}, nil
	}
	return nil, ErrNotFound
}

// previewFilingMember 解析备案包内单份材料并保留原始成员路径。
// 参数 member 为已定位的备案包成员，category 为页面展示的业务类别。
// 返回值 该成员的正文或简短提示。
func previewFilingMember(member *zip.File, category string) filingTextDocument {
	name := path.Base(member.Name)
	result := filingTextDocument{FileName: name, Member: member.Name, Category: category, Sections: []filingTextSection{}}
	extension := strings.ToLower(path.Ext(name))
	if extension != ".doc" && extension != ".docx" {
		result.Message = "当前材料不支持 Word 文字预览，请下载原件核对。"
		return result
	}
	if member.UncompressedSize64 <= maxDocumentSize {
		var content bytes.Buffer
		if err := readFilingPackageMember(member, &content); err == nil {
			parsed, err := parseFilingWord(bytes.NewReader(content.Bytes()), int64(content.Len()), name, category)
			if err == nil {
				parsed.Member = member.Name
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
	section := filingTextSection{Title: "正文", Paragraphs: []string{}, ParagraphNumbers: []int{}}
	left := maxFilingPreviewChars
	for index, text := range paragraphs {
		if text == "" {
			continue
		}
		count := utf8.RuneCountInString(text)
		if count > left {
			if left > 0 {
				section.Paragraphs = append(section.Paragraphs, string([]rune(text)[:left]))
				section.ParagraphNumbers = append(section.ParagraphNumbers, index+1)
			}
			result.Truncated = true
			break
		}
		left -= count
		if filingSectionHeading.MatchString(text) {
			if len(section.Paragraphs) > 0 || section.Title != "正文" {
				result.Sections = append(result.Sections, section)
			}
			section = filingTextSection{Title: text, TitleParagraph: index + 1,
				Paragraphs: []string{}, ParagraphNumbers: []int{}}
		} else {
			section.Paragraphs = append(section.Paragraphs, text)
			section.ParagraphNumbers = append(section.ParagraphNumbers, index+1)
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
