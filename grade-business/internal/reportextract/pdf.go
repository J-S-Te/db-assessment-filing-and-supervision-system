package reportextract

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	maxPDFSize     = 20 << 20
	maxPDFPages    = 200
	maxOCRPages    = 20
	maxPDFText     = 2 << 20
	maxPDFRaster   = 6 << 20
	pdfTimeLimit   = 90 * time.Second
	ocrLanguages   = "chi_sim+eng"
	pdfTextSource  = "pdf_text"
	pdfOCRSource   = "pdf_ocr"
	pdfMixedSource = "pdf_mixed"
)

var (
	errPDFUnavailable = errors.New("local PDF tools unavailable")
	errOCRUnavailable = errors.New("local OCR tools or languages unavailable")
	errPDFBusy        = errors.New("local PDF extraction busy")
	errPDFTimeout     = errors.New("local PDF extraction deadline exceeded")
	pdfHeadingNumber  = regexp.MustCompile(`^(?:[0-9０-９]+(?:[.．][0-9０-９]+)*[.．、)）]?|` +
		`[一二三四五六七八九十]+[、.．]|[（(][0-9０-９一二三四五六七八九十]+[）)])`)
)

type pdfPage struct {
	text   string
	source string
}

type pdfPosition struct {
	page   int
	source string
}

// extractPDF 提取 PDF 文字层，在无正文文字的页调用本地 OCR。
// 参数 ctx 为请求上下文，file 为受控正文，size 为附件登记字节数。
// 返回值 带位置和来源的结构化字段，或格式、资源和识别错误。
func extractPDF(ctx context.Context, file *os.File, size int64) ([]field, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || size <= 0 || size > maxPDFSize || info.Size() != size {
		return nil, errDocument
	}
	ctx, cancel := context.WithTimeout(ctx, pdfTimeLimit)
	defer cancel()
	pages, err := readPDFText(ctx, file)
	if err != nil {
		return nil, err
	}
	if err := readPDFScans(ctx, file, pages); err != nil {
		return nil, err
	}
	return collectPDFFields(pages)
}

// readPDFText 检查页数与加密状态并按页读取 PDF 的文字层。
// 参数 ctx 限制提取时间，file 为受控 PDF 正文。
// 返回值 按物理页排序的文字层，或工具、格式及边界错误。
func readPDFText(ctx context.Context, file *os.File) ([]pdfPage, error) {
	info, err := runPDFTool(ctx, "pdfinfo", file, 64<<10, errPDFUnavailable, "-")
	if err != nil {
		return nil, err
	}
	pages := pdfPageCount(string(info))
	if pages < 1 || pages > maxPDFPages {
		return nil, errDocument
	}
	text, err := runPDFTool(ctx, "pdftotext", file, maxPDFText,
		errPDFUnavailable, "-enc", "UTF-8", "-eol", "unix", "-", "-")
	if err != nil {
		return nil, err
	}
	raw := strings.Split(strings.TrimSuffix(string(text), "\f"), "\f")
	if len(raw) != pages {
		return nil, errDocument
	}
	result := make([]pdfPage, len(raw))
	for index, page := range raw {
		result[index] = pdfPage{text: page, source: pdfTextSource}
	}
	return result, nil
}

// pdfPageCount 从工具输出的页数与加密信息判断可处理的 PDF 页数。
// 参数 info 为以固定语言输出的 PDF 元信息。
// 返回值 未加密 PDF 的页数，信息不合格时为零。
func pdfPageCount(info string) int {
	pages := 0
	unencrypted := false
	for line := range strings.SplitSeq(info, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		switch key {
		case "Pages":
			pages, _ = strconv.Atoi(strings.TrimSpace(value))
		case "Encrypted":
			unencrypted = strings.TrimSpace(value) == "no"
		}
	}
	if !unencrypted {
		return 0
	}
	return pages
}

// readPDFScans 仅对没有可用正文文字的物理页执行扫描识别。
// 参数 ctx 限制整份报告耗时，file 为受控 PDF，pages 为已读的各页文字层。
// 返回值 识别错误，成功时就地补全对应页内容。
func readPDFScans(ctx context.Context, file *os.File, pages []pdfPage) error {
	scans := make([]int, 0)
	total := 0
	for index, page := range pages {
		if strings.IndexFunc(page.text, unicode.IsLetter) == -1 {
			scans = append(scans, index)
			continue
		}
		total += len(page.text)
	}
	if len(scans) == 0 {
		return nil
	}
	if len(scans) > maxOCRPages {
		return errDocument
	}
	if err := requireOCRLanguages(ctx); err != nil {
		return err
	}
	for _, index := range scans {
		text, err := readPDFScan(ctx, file, index+1)
		if err != nil {
			return err
		}
		total += len(text)
		if total > maxPDFText {
			return errDocument
		}
		pages[index] = pdfPage{text: text, source: pdfOCRSource}
	}
	return nil
}

// requireOCRLanguages 确认扫描识别具备中英文所需的本地语言数据。
// 参数 ctx 为整份报告共享的限时上下文。
// 返回值 工具、语言包或执行错误。
func requireOCRLanguages(ctx context.Context) error {
	out, err := runPDFTool(ctx, "tesseract", nil, 64<<10, errOCRUnavailable, "--list-langs")
	if err != nil {
		if errors.Is(err, errDocument) {
			return errOCRUnavailable
		}
		return err
	}
	languages := strings.Fields(string(out))
	for language := range strings.SplitSeq(ocrLanguages, "+") {
		found := false
		for _, installed := range languages {
			if installed == language {
				found = true
				break
			}
		}
		if !found {
			return errOCRUnavailable
		}
	}
	return nil
}

// readPDFScan 按物理页渲染有界的灰度图并执行本地中英文识别。
// 参数 ctx 为整份报告共享的限时上下文，file 为受控正文，page 为物理页码。
// 返回值 OCR 识别文本，或渲染与识别错误。
func readPDFScan(ctx context.Context, file *os.File, page int) (string, error) {
	number := strconv.Itoa(page)
	image, err := runPDFTool(ctx, "pdftoppm", file, maxPDFRaster, errOCRUnavailable,
		"-f", number, "-l", number, "-singlefile", "-scale-to", "2400", "-gray", "-")
	if err != nil {
		return "", err
	}
	text, err := runPDFTool(ctx, "tesseract", bytes.NewReader(image), maxPDFText, errOCRUnavailable,
		"stdin", "stdout", "-l", ocrLanguages, "--psm", "3", "--dpi", "300")
	return string(text), err
}

// runPDFTool 以标准流调用本地工具，限制期限、输出和子进程环境。
// 参数 ctx 控制期限，name 为工具名，input 为标准输入，limit 为输出上限，
// unavailable 为工具缺失错误，args 为受控参数。
// 返回值 有界的标准输出，或工具、进程、输入输出限制错误。
func runPDFTool(ctx context.Context, name string, input io.Reader, limit int,
	unavailable error, args ...string,
) ([]byte, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, unavailable
	}
	if file, ok := input.(*os.File); ok {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
	}
	output := &pdfOutput{remaining: limit}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin, cmd.Stdout = input, output
	cmd.WaitDelay = time.Second
	cmd.Env = []string{"LANG=C", "LC_ALL=C", "OMP_THREAD_LIMIT=1"}
	if prefix, ok := os.LookupEnv("TESSDATA_PREFIX"); name == "tesseract" && ok {
		cmd.Env = append(cmd.Env, "TESSDATA_PREFIX="+prefix)
	}
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, errPDFTimeout
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errDocument
	}
	return output.buffer.Bytes(), nil
}

type pdfOutput struct {
	buffer    bytes.Buffer
	remaining int
}

// Write 给本地 PDF 工具的标准输出设置硬上限。
// 参数 data 为工具本次产生的字节。
// 返回值 写入长度，超出限制时返回格式错误。
func (b *pdfOutput) Write(data []byte) (int, error) {
	if len(data) > b.remaining {
		return 0, errDocument
	}
	b.remaining -= len(data)
	return b.buffer.Write(data)
}

// collectPDFFields 将提取块和物理页关联，再为每个栏目确定证据来源。
// 参数 pages 为按物理页排序的文字识别结果。
// 返回值 保留原文与页码的栏目字段，或无栏目和超限错误。
func collectPDFFields(pages []pdfPage) ([]field, error) {
	var paragraphs []string
	var positions []pdfPosition
	for index, page := range pages {
		for line := range strings.SplitSeq(page.text, "\n") {
			line = cleanPDFLine(line)
			if line == "" {
				continue
			}
			paragraphs = append(paragraphs, line)
			positions = append(positions, pdfPosition{page: index + 1, source: page.source})
			if len(paragraphs) > maxParagraphs {
				return nil, errDocument
			}
		}
	}
	fields, err := collectFields(paragraphs, true)
	for index := range fields {
		item := &fields[index]
		start, end := positions[item.Start-1], positions[item.End-1]
		item.StartPage, item.EndPage, item.Source = start.page, end.page, start.source
		for _, position := range positions[item.Start:item.End] {
			if position.source != item.Source {
				item.Source = pdfMixedSource
				break
			}
		}
	}
	return fields, err
}

// cleanPDFLine 移除换行外的控制字符和段落边缘空白，保留原文可见内容。
// 参数 line 为工具输出中的单个提取块。
// 返回值 可持久化的原文块。
func cleanPDFLine(line string) string {
	return strings.TrimSpace(strings.Map(func(char rune) rune {
		if char == '\t' {
			return ' '
		}
		if unicode.IsControl(char) {
			return -1
		}
		return char
	}, line))
}

// normalizePDFLabel 识别 PDF 标题中的 OCR 空格和常见章节序号。
// 参数 label 为候选栏目标题。
// 返回值 用于匹配栏目名称的规范化标题。
func normalizePDFLabel(label string) string {
	label = strings.Join(strings.Fields(label), "")
	return pdfHeadingNumber.ReplaceAllString(label, "")
}
