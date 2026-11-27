package wordtext

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

const (
	mainPart            = "word/document.xml"
	wordNamespace       = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	strictWordNamespace = "http://purl.oclc.org/ooxml/wordprocessingml/main"
	maxXML              = 2 << 20
	maxParagraphs       = 10000
)

// ErrInvalid 表示文件内容无法作为可读取的 Word 正文。
var ErrInvalid = errors.New("invalid or oversized word document")

// isWordName 识别 Word 的过渡和严格两类正文命名空间。
// 参数 name 为正文中的 XML 元素。
// 返回值 元素使用 Word 命名空间时为真。
func isWordName(name xml.Name) bool {
	return name.Space == wordNamespace || name.Space == strictWordNamespace
}

// DOCX 读取 Word 正文和表格中的段落，保留正文顺序。
// 参数 source 为归档内容，size 为归档的文件大小。
// 返回值 正文段落，或无法读取正文的错误。
func DOCX(source io.ReaderAt, size int64) ([]string, error) {
	archive, err := zip.NewReader(source, size)
	if err != nil || len(archive.File) > 2000 {
		return nil, ErrInvalid
	}
	part, err := readMain(archive.File)
	if err != nil {
		return nil, err
	}
	paragraphs, err := readParagraphs(part)
	if err != nil {
		return nil, err
	}
	return paragraphs, nil
}

// readMain 限量读取 DOCX 的唯一正文 XML，不解析外部关系。
// 参数 entries 为归档中的成员目录。
// 返回值 正文 XML 内容，或缺失、重复及超限错误。
func readMain(entries []*zip.File) ([]byte, error) {
	var result []byte
	for _, entry := range entries {
		if entry.Name != mainPart {
			continue
		}
		if result != nil || entry.UncompressedSize64 > maxXML {
			return nil, ErrInvalid
		}
		reader, err := entry.Open()
		if err != nil {
			return nil, ErrInvalid
		}
		result, err = io.ReadAll(io.LimitReader(reader, maxXML+1))
		reader.Close()
		if err != nil || len(result) == 0 || len(result) > maxXML {
			return nil, ErrInvalid
		}
	}
	if len(result) == 0 {
		return nil, ErrInvalid
	}
	return result, nil
}

type paragraphReader struct {
	stack      []xml.Name
	body       int
	paragraph  int
	text       int
	buffer     strings.Builder
	paragraphs []string
	gotBody    bool
}

// readParagraphs 按正文顺序读取 Word 段落，包括表格单元格内的段落。
// 参数 source 为经限长的 DOCX 正文。
// 返回值 含空段落的有序段落文本，或无法解析的错误。
func readParagraphs(source []byte) ([]string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(source))
	var state paragraphReader
	for {
		token, err := decoder.Token()
		if err == io.EOF && len(state.stack) == 0 && state.gotBody {
			return state.paragraphs, nil
		}
		if err != nil {
			return nil, ErrInvalid
		}
		switch node := token.(type) {
		case xml.StartElement:
			if !state.start(node.Name) {
				return nil, ErrInvalid
			}
		case xml.EndElement:
			if !state.end() {
				return nil, ErrInvalid
			}
		case xml.CharData:
			if state.text > 0 {
				state.buffer.Write(node)
			}
		case xml.Directive:
			return nil, ErrInvalid
		}
	}
}

// start 跟踪正文内的段落与文本元素并限制 XML 嵌套。
// 参数 name 为开始读取的元素名称。
// 返回值 元素结构可接受时为真。
func (p *paragraphReader) start(name xml.Name) bool {
	depth := len(p.stack) + 1
	if depth > 128 || depth == 1 && (p.gotBody || !isWordName(name) || name.Local != "document") {
		return false
	}
	p.stack = append(p.stack, name)
	if !isWordName(name) {
		return true
	}
	switch name.Local {
	case "body":
		if depth == 2 && !p.gotBody {
			p.body, p.gotBody = depth, true
		}
	case "p":
		if p.body > 0 && p.paragraph == 0 {
			p.paragraph = depth
		}
	case "t":
		if p.paragraph > 0 && p.text == 0 {
			p.text = depth
		}
	case "tab", "br":
		if p.paragraph > 0 {
			p.buffer.WriteByte(' ')
		}
	}
	return true
}

// end 结束元素读取并保留已读取的段落及位置。
// 参数 无。
// 返回值 段落数仍在限制内时为真。
func (p *paragraphReader) end() bool {
	depth := len(p.stack)
	p.stack = p.stack[:depth-1]
	switch depth {
	case p.text:
		p.text = 0
	case p.paragraph:
		p.paragraphs = append(p.paragraphs, strings.TrimSpace(p.buffer.String()))
		p.buffer.Reset()
		p.paragraph = 0
	case p.body:
		p.body = 0
	}
	return len(p.paragraphs) <= maxParagraphs
}
