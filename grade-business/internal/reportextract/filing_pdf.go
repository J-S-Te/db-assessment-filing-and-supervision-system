package reportextract

import (
	"bytes"
	"context"
	"io"
	"strconv"

	"grade-business/internal/filingaudit"
)

// FilingPDFText 读取备案 PDF 的文字层与物理页，不对图形补写内容。
// 参数 ctx 控制提取期限，file 为有界受控材料，可重复读。
// 返回值 按物理页排序的文字，或格式和工具错误。
func FilingPDFText(ctx context.Context, file io.ReadSeeker) ([]string, error) {
	pages, err := readPDFText(ctx, file)
	if err != nil {
		return nil, err
	}
	text := make([]string, 0, len(pages))
	for _, page := range pages {
		text = append(text, page.text)
	}
	return text, nil
}

// FilingPDFImages 渲染首尾页和相邻的签章页，保留可复查的物理页码。
// 参数 ctx、file 为限定执行上下文和可重复读的有效 PDF 材料。
// 返回值 能在资源边界内生成的原件页面图像。
func FilingPDFImages(ctx context.Context, file io.ReadSeeker) []filingaudit.Image {
	metadata, err := runPDFTool(ctx, "pdfinfo", file, 64<<10, errPDFUnavailable, "-")
	if err != nil {
		return nil
	}
	last := pdfPageCount(string(metadata))
	if last < 1 || last > maxPDFPages {
		return nil
	}
	pages := []int{last}
	if last > 1 {
		pages = append(pages, 1)
	}
	if last > 2 {
		pages = append(pages, last-1)
	}
	images := make([]filingaudit.Image, 0, len(pages))
	for _, page := range pages {
		n := strconv.Itoa(page)
		data, err := runPDFTool(ctx, "pdftoppm", file, 2<<20, errPDFUnavailable,
			"-jpeg", "-f", n, "-l", n, "-singlefile", "-scale-to", "1600", "-")
		if err == nil && bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}) {
			images = append(images, filingaudit.Image{Location: "第 " + n + " 页",
				MIME: "image/jpeg", Data: data})
		}
	}
	return images
}
