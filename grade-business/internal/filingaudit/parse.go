package filingaudit

import (
	"context"
	"io"
	"path"
	"strconv"
	"strings"

	"grade-business/internal/wordtext"
)

// PDFText 从有界的本地 PDF 输入中读取文字层并保留物理分页。
type PDFText func(context.Context, io.ReadSeeker) ([]string, error)

// Parse 根据保存附件的格式读取可用于规则比对的原文。
// 参数 ctx、name、source、size 和 pdf 分别为执行上下文、文件名、内容、大小和文字层解析器。
// 返回值 带实际位置的文档文字，或类型、边界及解析错误。
func Parse(ctx context.Context, name string, source io.ReaderAt, size int64, pdf PDFText) ([]Line, error) {
	switch strings.ToLower(path.Ext(name)) {
	case ".doc":
		text, err := wordtext.DOC(source, size)
		return wordLines(text), err
	case ".docx":
		text, err := wordtext.DOCX(source, size)
		return wordLines(text), err
	case ".pdf":
		if pdf == nil || size < 1 || size > 20<<20 {
			return nil, wordtext.ErrInvalid
		}
		pages, err := pdf(ctx, io.NewSectionReader(source, 0, size))
		return pdfLines(pages), err
	case ".xlsx":
		return workbookLines(source, size)
	default:
		return nil, wordtext.ErrInvalid
	}
}

// wordLines 将 Word 正文及表格段落保留为从一开始的定位。
// 参数 text 为解析得到的全部段落。
// 返回值 带段落序号的文字。
func wordLines(text []string) []Line {
	lines := make([]Line, 0, len(text))
	for i, paragraph := range text {
		lines = append(lines, Line{"第 " + strconv.Itoa(i+1) + " 段", paragraph})
	}
	return lines
}

// pdfLines 将 PDF 的物理页拆为原文行。
// 参数 pages 为正序物理页中的文字层。
// 返回值 带准确页码的原文片段。
func pdfLines(pages []string) []Line {
	var lines []Line
	for i, page := range pages {
		location := "第 " + strconv.Itoa(i+1) + " 页"
		for raw := range strings.SplitSeq(page, "\n") {
			if line := strings.TrimSpace(raw); line != "" {
				lines = append(lines, Line{location, line})
			}
		}
	}
	return lines
}
