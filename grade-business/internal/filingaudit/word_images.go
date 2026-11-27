package filingaudit

import (
	"archive/zip"
	"bytes"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"path"
	"slices"
	"strings"
)

// WordImages 选择至多三张可读取的 DOCX 内嵌图片。
// 参数 source、size 为受控 Word 附件和真实字节数。
// 返回值 可定位、大小受限的原件内嵌图片。
func WordImages(source io.ReaderAt, size int64) []Image {
	archive, err := zip.NewReader(source, size)
	if err != nil {
		return nil
	}
	var candidates []*zip.File
	for _, file := range archive.File {
		if !strings.HasPrefix(file.Name, "word/media/") {
			continue
		}
		switch strings.ToLower(path.Ext(file.Name)) {
		case ".jpeg", ".jpg", ".png":
			candidates = append(candidates, file)
		}
	}
	slices.SortFunc(candidates, func(a, b *zip.File) int { return strings.Compare(a.Name, b.Name) })
	if len(candidates) > 3 {
		candidates = []*zip.File{candidates[0], candidates[len(candidates)/2], candidates[len(candidates)-1]}
	}
	var selected []Image
	for _, candidate := range candidates {
		if result, ok := decodeWordImage(candidate); ok {
			selected = append(selected, result)
		}
	}
	return selected
}

// decodeWordImage 校验原始内嵌图片的媒体类型、解压大小和尺寸。
// 参数 file 为同一 Word 归档中的候选图片。
// 返回值 有界图片和读取是否成功。
func decodeWordImage(file *zip.File) (Image, bool) {
	const limit = 2 << 20
	if file.UncompressedSize64 == 0 || file.UncompressedSize64 > limit {
		return Image{}, false
	}
	reader, err := file.Open()
	if err != nil {
		return Image{}, false
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || len(raw) > limit {
		return Image{}, false
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width < 20 || config.Height < 20 ||
		config.Width > 5000 || config.Height > 5000 || (format != "jpeg" && format != "png") {
		return Image{}, false
	}
	return Image{Location: "内嵌图像 " + path.Base(file.Name), MIME: "image/" + format, Data: raw}, true
}
