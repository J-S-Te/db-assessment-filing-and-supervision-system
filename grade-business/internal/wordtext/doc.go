package wordtext

import (
	"encoding/binary"
	"io"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/richardlehane/mscfb"
	"golang.org/x/text/encoding/charmap"
)

const (
	maxWordSize     = 20 << 20
	maxWordText     = 1 << 20
	maxControlReads = 256
)

type compoundReader struct {
	io.ReaderAt
	controlReads int
	readingData  bool
}

// ReadAt 为复合文档的目录和分配表读取设置上限。
// 参数 output 为目标缓冲，offset 为附件内的偏移。
// 返回值 已读取的字节数，或读取上限及文件错误。
func (r *compoundReader) ReadAt(output []byte, offset int64) (int, error) {
	if !r.readingData {
		r.controlReads++
		if r.controlReads > maxControlReads {
			return 0, ErrInvalid
		}
	}
	return r.ReaderAt.ReadAt(output, offset)
}

// DOC 从 Word 97–2003 文件读出对用户可见的正文和表格文本。
// 参数 source 为附件内容，size 为实际文件大小。
// 返回值 按正文顺序排列的段落，或文件结构、加密及大小错误。
func DOC(source io.ReaderAt, size int64) ([]string, error) {
	if size < 512 || size > maxWordSize {
		return nil, ErrInvalid
	}
	reader := &compoundReader{ReaderAt: io.NewSectionReader(source, 0, size)}
	archive, err := mscfb.New(reader)
	if err != nil {
		return nil, ErrInvalid
	}
	reader.readingData = true
	document, err := readStream(archive, "WordDocument", size)
	if err != nil {
		return nil, err
	}
	tableName, start, length, chars, err := wordLayout(document)
	if err != nil {
		return nil, err
	}
	table, err := readStream(archive, tableName, size)
	if err != nil || start < 0 || start > len(table) || length <= 0 || length > len(table)-start {
		return nil, ErrInvalid
	}
	pieces, err := pieceTable(table[start : start+length])
	if err != nil {
		return nil, err
	}
	text, err := decodePieces(document, pieces, chars)
	if err != nil {
		return nil, err
	}
	return wordParagraphs(text)
}

// readStream 读取根目录下唯一的 Word 正文或正文索引流。
// 参数 archive 是复合文档，name 是流名，size 为附件大小上限。
// 返回值 完整数据流，或缺失、重复及文件内容错误。
func readStream(archive *mscfb.Reader, name string, size int64) ([]byte, error) {
	var result []byte
	for _, entry := range archive.File {
		if entry.Name != name || len(entry.Path) != 0 || entry.FileInfo().IsDir() {
			continue
		}
		if result != nil || entry.Size < 1 || entry.Size > size {
			return nil, ErrInvalid
		}
		result = make([]byte, int(entry.Size))
		if _, err := io.ReadFull(entry, result); err != nil {
			return nil, ErrInvalid
		}
	}
	if result == nil {
		return nil, ErrInvalid
	}
	return result, nil
}

// wordLayout 从文件信息块取得正文长度和段落片段索引位置。
// 参数 data 为 WordDocument 流。
// 返回值 表流名、索引偏移和字节数、正文 UTF-16 单元数及结构错误。
func wordLayout(data []byte) (string, int, int, int, error) {
	const baseSize, clxIndex = 32, 33
	if len(data) < baseSize+2 || binary.LittleEndian.Uint16(data) != 0xa5ec ||
		binary.LittleEndian.Uint16(data[2:]) < 0x00c1 {
		return "", 0, 0, 0, ErrInvalid
	}
	flags := binary.LittleEndian.Uint16(data[10:])
	if flags&0x8100 != 0 {
		return "", 0, 0, 0, ErrInvalid
	}
	words := int(binary.LittleEndian.Uint16(data[baseSize:]))
	longCount := baseSize + 2 + words*2
	if longCount+2 > len(data) {
		return "", 0, 0, 0, ErrInvalid
	}
	longs := int(binary.LittleEndian.Uint16(data[longCount:]))
	tableCount := longCount + 2 + longs*4
	pair := tableCount + 2 + clxIndex*8
	if longs < 4 || pair+8 > len(data) ||
		int(binary.LittleEndian.Uint16(data[tableCount:])) <= clxIndex {
		return "", 0, 0, 0, ErrInvalid
	}
	chars := int(binary.LittleEndian.Uint32(data[longCount+2+12:]))
	if chars < 1 || chars > maxWordText {
		return "", 0, 0, 0, ErrInvalid
	}
	table := "0Table"
	if flags&0x0200 != 0 {
		table = "1Table"
	}
	start := int(binary.LittleEndian.Uint32(data[pair:]))
	length := int(binary.LittleEndian.Uint32(data[pair+4:]))
	return table, start, length, chars, nil
}

// pieceTable 从 CLX 中跳过格式属性并定位字符片段表。
// 参数 clx 为已限定长度的索引流片段。
// 返回值 片段表数据，或非标准结构错误。
func pieceTable(clx []byte) ([]byte, error) {
	for len(clx) >= 3 && clx[0] == 1 {
		length := int(binary.LittleEndian.Uint16(clx[1:])) + 3
		if length > len(clx) {
			return nil, ErrInvalid
		}
		clx = clx[length:]
	}
	if len(clx) < 5 || clx[0] != 2 || int(binary.LittleEndian.Uint32(clx[1:])) != len(clx)-5 {
		return nil, ErrInvalid
	}
	return clx[5:], nil
}

// decodePieces 依照字符位置映射读取主正文，不读取批注或页眉等其他文字流。
// 参数 document 为 WordDocument 流，pieces 为字符片段表，chars 为主正文长度。
// 返回值 完整主正文，或位置和边界错误。
func decodePieces(document, pieces []byte, chars int) ([]rune, error) {
	if len(pieces) < 16 || (len(pieces)-4)%12 != 0 {
		return nil, ErrInvalid
	}
	count := (len(pieces) - 4) / 12
	offsets := pieces[:(count+1)*4]
	descriptors := pieces[len(offsets):]
	if binary.LittleEndian.Uint32(offsets) != 0 {
		return nil, ErrInvalid
	}
	text := make([]uint16, 0, chars)
	for i := 0; i < count && len(text) < chars; i++ {
		next := int(binary.LittleEndian.Uint32(offsets[(i+1)*4:]))
		n := min(next, chars) - len(text)
		if n <= 0 {
			return nil, ErrInvalid
		}
		fc := binary.LittleEndian.Uint32(descriptors[i*8+2:])
		offset := int(fc & 0x3fffffff)
		compressed := fc&0x40000000 != 0
		width := 2
		if compressed {
			offset /= 2
			width = 1
		}
		if fc&0x80000000 != 0 || offset > len(document) || n*width > len(document)-offset {
			return nil, ErrInvalid
		}
		for j := 0; j < n; j++ {
			pos := offset + j*width
			if compressed {
				text = append(text, uint16(charmap.Windows1252.DecodeByte(document[pos])))
			} else {
				text = append(text, binary.LittleEndian.Uint16(document[pos:]))
			}
		}
	}
	if len(text) != chars {
		return nil, ErrInvalid
	}
	return utf16.Decode(text), nil
}

// wordParagraphs 按段落和表格单元格分隔文本，略去域指令并保留显示值。
// 参数 text 为 Word 主正文。
// 返回值 清理后的正文段落，或正文超过段落上限的错误。
func wordParagraphs(text []rune) ([]string, error) {
	var rows []string
	var row strings.Builder
	var fields []bool
	hidden := 0
	for _, char := range text {
		switch char {
		case 0x13:
			fields = append(fields, true)
			hidden++
		case 0x14, 0x15:
			if n := len(fields); n > 0 {
				if fields[n-1] {
					hidden--
					fields[n-1] = false
				}
				if char == 0x15 {
					fields = fields[:n-1]
				}
			}
		default:
			if hidden > 0 {
				continue
			}
			switch char {
			case '\r', '\n', '\f', '\a':
				rows = append(rows, strings.TrimSpace(row.String()))
				row.Reset()
			case '\t', '\v':
				row.WriteByte(' ')
			default:
				if !unicode.IsControl(char) {
					row.WriteRune(char)
				}
			}
		}
		if len(rows) >= maxParagraphs {
			return nil, ErrInvalid
		}
	}
	return append(rows, strings.TrimSpace(row.String())), nil
}
