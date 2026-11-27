package filingaudit

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"regexp"
	"strconv"
	"strings"

	"grade-business/internal/wordtext"
)

const workbookTextLimit = 4 << 20

var worksheetFile = regexp.MustCompile(`^xl/worksheets/sheet([0-9]+)\.xml$`)

type workbookString struct {
	Text string   `xml:"t"`
	Runs []string `xml:"r>t"`
}

type workbookCell struct {
	Type  string         `xml:"t,attr"`
	Value string         `xml:"v"`
	Text  workbookString `xml:"is"`
}

type workbookRow struct {
	Number int            `xml:"r,attr"`
	Cells  []workbookCell `xml:"c"`
}

// workbookLines 读取数据摸底等附件的标准 OOXML 工作表文字。
// 参数 source、size 为受控附件及归档大小。
// 返回值 带表序号和行号的单元格文字，或有界解析错误。
func workbookLines(source io.ReaderAt, size int64) ([]Line, error) {
	archive, err := zip.NewReader(source, size)
	if err != nil || len(archive.File) > 512 {
		return nil, wordtext.ErrInvalid
	}
	var shared struct {
		Items []workbookString `xml:"si"`
	}
	for _, member := range archive.File {
		if member.Name == "xl/sharedStrings.xml" {
			if err := readWorksheetXML(member, &shared); err != nil {
				return nil, err
			}
			break
		}
	}
	budget := uint64(workbookTextLimit)
	var lines []Line
	for _, member := range archive.File {
		match := worksheetFile.FindStringSubmatch(member.Name)
		if match == nil {
			continue
		}
		if member.UncompressedSize64 > budget {
			return nil, wordtext.ErrInvalid
		}
		budget -= member.UncompressedSize64
		var worksheet struct {
			Rows []workbookRow `xml:"sheetData>row"`
		}
		if err := readWorksheetXML(member, &worksheet); err != nil {
			return nil, err
		}
		lines = append(lines, sheetRows(match[1], worksheet.Rows, shared.Items)...)
	}
	return lines, nil
}

// sheetRows 按物理工作表和行生成证据，保留字段与邻接单元格。
// 参数 sheet 为工作表序号，rows 为行数据，shared 为共享字符串表。
// 返回值 带工作表行号的原文。
func sheetRows(sheet string, rows []workbookRow, shared []workbookString) []Line {
	var result []Line
	for index, row := range rows {
		var visible []string
		for _, cell := range row.Cells {
			switch cell.Type {
			case "s":
				id, err := strconv.Atoi(cell.Value)
				if err == nil && id >= 0 && id < len(shared) {
					visible = append(visible, shared[id].Text+strings.Join(shared[id].Runs, ""))
				}
			case "inlineStr":
				visible = append(visible, cell.Text.Text+strings.Join(cell.Text.Runs, ""))
			default:
				if cell.Value != "" {
					visible = append(visible, cell.Value)
				}
			}
		}
		if len(visible) > 0 {
			number := row.Number
			if number <= 0 {
				number = index + 1
			}
			result = append(result, Line{"工作表 " + sheet + " 第 " + strconv.Itoa(number) + " 行",
				strings.Join(visible, "\t")})
		}
	}
	return result
}

// readWorksheetXML 限制归档中每个 XML 成员的解压大小。
// 参数 file 为归档成员，into 为待填充的固定结构。
// 返回值 数据有效时为空，否则为解析或边界错误。
func readWorksheetXML(file *zip.File, into any) error {
	if file.UncompressedSize64 > workbookTextLimit {
		return wordtext.ErrInvalid
	}
	reader, err := file.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, workbookTextLimit+1))
	if err != nil || len(raw) > workbookTextLimit {
		return wordtext.ErrInvalid
	}
	return xml.Unmarshal(raw, into)
}
