package reportextract

import (
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"grade-business/internal/wordtext"
)

const (
	maxParagraphs = 10000
	maxValue      = 4000
	maxFields     = 4
)

var (
	errDocument = errors.New("invalid or oversized report document")
	errNoFields = errors.New("no recognizable report sections")
)

type field struct {
	Key       string  `json:"key"`
	Original  string  `json:"original"`
	Start     int     `json:"startParagraph"`
	End       int     `json:"endParagraph"`
	StartPage int     `json:"startPage,omitempty"`
	EndPage   int     `json:"endPage,omitempty"`
	Source    string  `json:"source,omitempty"`
	Truncated bool    `json:"truncated,omitempty"`
	Value     *string `json:"value,omitempty"`
}

var labels = map[string]string{
	"资产清单": "assets", "资产列表": "assets",
	"漏洞详情": "findings", "发现问题": "findings", "问题清单": "findings",
	"风险等级": "risk", "风险级别": "risk", "风险定级": "risk",
	"整改建议": "recommendations", "安全整改建议": "recommendations",
}

// extractDOCX 解析报告正文并提取含原文段落位置的结构化字段。
// 参数 source 为归档内容，size 为归档的文件大小。
// 返回值 识别到的报告字段，或格式、正文大小及缺少字段的错误。
func extractDOCX(source io.ReaderAt, size int64) ([]field, error) {
	paragraphs, err := wordtext.DOCX(source, size)
	if err != nil {
		return nil, errDocument
	}
	return collectFields(paragraphs, false)
}

// collectFields 根据报告栏目名收集同一栏目中的非空段落。
// 参数 paragraphs 是正文依序排列的段落，pdfLabels 表示允许 PDF 识别产生的标题空格和编号。
// 返回值 首次识别的非空栏目字段，或缺少全部字段的错误。
func collectFields(paragraphs []string, pdfLabels bool) ([]field, error) {
	results := make([]field, 0, maxFields)
	seen := make(map[string]bool)
	current := field{}
	for i, value := range paragraphs {
		title, inline := splitLabel(value)
		if pdfLabels {
			title = normalizePDFLabel(title)
		}
		if key := labels[strings.TrimSpace(title)]; key != "" {
			results = appendField(results, seen, current)
			current = field{Key: key, Start: i + 1, End: i + 1}
			addText(&current, strings.TrimSpace(inline), i+1)
		} else if current.Key != "" {
			addText(&current, value, i+1)
		}
	}
	results = appendField(results, seen, current)
	if len(results) == 0 {
		return nil, errNoFields
	}
	return results, nil
}

// splitLabel 识别第一个中英文冒号并保留其后的原文。
// 参数 value 为当前报告段落。
// 返回值 栏目名与同行原文。
func splitLabel(value string) (string, string) {
	for i, char := range value {
		if char == ':' || char == '：' {
			return value[:i], value[i+utf8.RuneLen(char):]
		}
	}
	return value, ""
}

// appendField 收录某栏目第一次出现的有效原文。
// 参数 result 为已收录字段，seen 记录栏目键，item 为当前栏目。
// 返回值 更新后的字段列表。
func appendField(result []field, seen map[string]bool, item field) []field {
	if item.Original == "" || seen[item.Key] {
		return result
	}
	seen[item.Key] = true
	return append(result, item)
}

// addText 限制栏目文本长度并记录证据所在的正文段落。
// 参数 item 为当前栏目，value 为待追加的文本，paragraph 为段落序号。
// 返回值 无。
func addText(item *field, value string, paragraph int) {
	if value == "" || item.Truncated {
		return
	}
	if item.Original != "" {
		value = "\n" + value
	}
	remaining := maxValue - utf8.RuneCountInString(item.Original)
	if remaining == 0 {
		item.Truncated = true
		return
	}
	runes := []rune(value)
	if len(runes) > remaining {
		runes = runes[:remaining]
		item.Truncated = true
	}
	item.Original += string(runes)
	item.End = paragraph
}
