package filingaudit

import (
	"regexp"
	"strconv"
	"strings"
)

// Image 保存从实际备案附件提取的有界图片及其原件位置。
type Image struct {
	Location string
	MIME     string
	Data     []byte
}

var personalPhone = regexp.MustCompile(`\b1[3-9][0-9]{9}\b`)

// maskPersonalData 隐去文本中的身份证号码和手机号中间字段。
// 参数 text 为出现在材料、输出或证据内的原文。
// 返回值 可用于推理和展示的脱敏文字。
func maskPersonalData(text string) string {
	text = identityNumber.ReplaceAllStringFunc(text, func(number string) string {
		return number[:6] + "********" + number[14:]
	})
	return personalPhone.ReplaceAllStringFunc(text, func(number string) string {
		return number[:3] + "****" + number[7:]
	})
}

// prepareAISources 为原件正文及启用的图像建立限定大小的引用表。
// 参数 docs 为当前材料快照的解析结果，includeImages 为是否送检选定图像。
// 返回值 带固定来源标识和原件定位的输入。
func prepareAISources(docs []Document, includeImages bool) []preparedSource {
	const textLimit = 240 << 10
	var sources []preparedSource
	textBytes := 0
	for _, doc := range docs {
		remaining := min(24<<10, textLimit-textBytes)
		for _, line := range doc.Lines {
			if remaining < 120 || len(sources) >= 480 {
				break
			}
			text := limitedPromptLine(line.Text)
			if text == "" || len(text) > remaining {
				continue
			}
			entry := aiSource(len(sources)+1, doc, line.Location)
			entry.Prompt.Text = text
			sources = append(sources, entry)
			remaining -= len(text)
			textBytes += len(text)
		}
	}
	if includeImages {
		sources = appendAISourceImages(sources, docs)
	}
	return sources
}

// limitedPromptLine 过滤模板占位文字并限定单条依据的长度。
// 参数 raw 为原件文字。
// 返回值 保留有意义内容的脱敏原文。
func limitedPromptLine(raw string) string {
	text := strings.Join(strings.Fields(maskPersonalData(raw)), " ")
	if !filled(text) {
		return ""
	}
	runes := []rune(text)
	if len(runes) > 1000 {
		return string(runes[:1000])
	}
	return text
}

// appendAISourceImages 将原件图像按总数和总字节上限加入引用表。
// 参数 sources、docs 为已选择的原文及同一批材料。
// 返回值 追加图像后的输入。
func appendAISourceImages(sources []preparedSource, docs []Document) []preparedSource {
	count, budget := 0, 8<<20
	for _, doc := range docs {
		for _, image := range doc.Images {
			if count >= 10 {
				return sources
			}
			if len(image.Data) == 0 || len(image.Data) > budget ||
				(image.MIME != "image/jpeg" && image.MIME != "image/png") {
				continue
			}
			entry := aiSource(len(sources)+1, doc, image.Location)
			entry.Prompt.Image, entry.Image = true, image
			sources = append(sources, entry)
			count++
			budget -= len(image.Data)
		}
	}
	return sources
}

// aiSource 创建材料引用，模型读取可读类型、文件名和位置。
// 参数 ordinal、doc、location 为顺序、原件与物理位置。
// 返回值 可用于对照模型引用的来源。
func aiSource(ordinal int, doc Document, location string) preparedSource {
	return preparedSource{
		Prompt: promptSource{ID: "S" + strconv.Itoa(ordinal), Category: doc.Kind,
			File: maskPersonalData(doc.FileName), Location: location},
		Ref: Evidence{AttachmentID: doc.AttachmentID, FileName: doc.FileName, Location: location},
	}
}
