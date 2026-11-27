package cases

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"

	"grade-business/internal/directoryref"
)

const (
	historyImportLimit     = 100
	historyManifestLimit   = 256 << 10
	historyImportReady     = "ready"
	historyImportDuplicate = "duplicate"
	historyImportConflict  = "conflict"
	historyImportInvalid   = "invalid"
	historyImportImported  = "imported"
)

var (
	historyImportColumns = []string{
		"number", "systemName", "organizationName", "assessmentOrg",
		"level", "filingForm", "classificationReport",
	}
	errHistoryManifest   = errors.New("historical filing manifest invalid")
	errHistoryMergedUnit = errors.New("filing unit has been merged")
)

type historyImportRow struct {
	Line             int    `json:"line"`
	Number           string `json:"number"`
	SystemName       string `json:"systemName"`
	OrganizationName string `json:"organizationName"`
	Status           string `json:"status"`
	Reason           string `json:"reason,omitempty"`
	FilingID         string `json:"filingId,omitempty"`
}

type historyImportCandidate struct {
	historyImportRow
	fields               saveFields
	form, classification *zip.File
	snapshot             *directoryref.Snapshot
	followsPending       bool
}

type historyImportArchive struct {
	files   map[string]*zip.File
	checked map[*zip.File]bool
}

type historyImportResult struct {
	BatchID    string             `json:"batchId,omitempty"`
	Operator   string             `json:"operator,omitempty"`
	ImportedAt *time.Time         `json:"importedAt,omitempty"`
	Rows       []historyImportRow `json:"rows"`
	Counts     map[string]int     `json:"counts"`
}

// parseHistoryImport 读取受限的历史备案 ZIP 及固定清单，逐行验证业务数据与材料。
// 参数 source、size 为上游上传限制后的归档文件及其精确大小。
// 返回值 完整的预检候选项目，或归档与清单错误。
func parseHistoryImport(source io.ReaderAt, size int64) ([]historyImportCandidate, error) {
	archive, err := zip.NewReader(source, size)
	if err != nil {
		return nil, ErrFilingPackage
	}
	members, err := filingPackageMembers(archive, maxPackageSize)
	if err != nil {
		return nil, err
	}
	files := make(map[string]*zip.File, len(members))
	for _, member := range members {
		files[member.Name] = member
	}
	reader, err := historyManifestReader(files["manifest.csv"])
	if err != nil {
		return nil, err
	}
	return readHistoryImportRows(reader, files)
}

// historyManifestReader 验证清单 CRC、固定字段和最多 256 KiB 内容。
// 参数 file 为归档根目录的 manifest.csv。
// 返回值 已确认表头并指向数据记录的读取器，或清单错误。
func historyManifestReader(file *zip.File) (*csv.Reader, error) {
	if file == nil || file.UncompressedSize64 > historyManifestLimit {
		return nil, errHistoryManifest
	}
	var raw bytes.Buffer
	if err := readFilingPackageMember(file, &raw); err != nil {
		return nil, errHistoryManifest
	}
	reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(raw.Bytes(), []byte{0xef, 0xbb, 0xbf})))
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil || !slices.Equal(header, historyImportColumns) {
		return nil, errHistoryManifest
	}
	return reader, nil
}

// readHistoryImportRows 读取有上限的数据记录并保留可定位的 CSV 行号。
// 参数 reader 为去除头行的 CSV 读取器，files 为按路径索引的归档文件。
// 返回值 每条业务结果和可提交数据，或清单结构和额度错误。
func readHistoryImportRows(reader *csv.Reader, files map[string]*zip.File) ([]historyImportCandidate, error) {
	rows := make([]historyImportCandidate, 0)
	archive := historyImportArchive{files: files, checked: make(map[*zip.File]bool)}
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || len(rows) >= historyImportLimit {
			return nil, errHistoryManifest
		}
		line, _ := reader.FieldPos(0)
		rows = append(rows, historyImportCandidateFrom(line, record, archive))
	}
	if len(rows) == 0 {
		return nil, errHistoryManifest
	}
	return rows, nil
}

// historyImportCandidateFrom 检查单条备案字段并验证两件独立材料的真实内容。
// 参数 line、record 为 CSV 行号及字段，archive 为索引和缓存材料的归档。
// 返回值 带有准确失败原因或可进一步核对的业务记录。
func historyImportCandidateFrom(line int, record []string, archive historyImportArchive) historyImportCandidate {
	item := historyImportCandidate{historyImportRow: historyImportRow{
		Line: line, Status: historyImportInvalid,
	}}
	if len(record) != len(historyImportColumns) {
		item.Reason = "列数不符，请按模板填写。"
		return item
	}
	input := filingInput{
		Number: record[0], SystemName: record[1], OrganizationName: record[2],
		AssessmentOrg: record[3], Level: strings.TrimSpace(record[4]),
	}
	fields, err := filingFields(input)
	if err != nil || strings.ContainsFunc(strings.Join(record[:4], ""), unicode.IsControl) {
		item.Reason = "备案编号、系统、单位、机构或等级不符合规则。"
		return item
	}
	item.fields = fields
	item.Number, item.SystemName, item.OrganizationName = fields.number, fields.systemName, fields.organizationName
	item.form = archive.files[strings.TrimSpace(record[5])]
	item.classification = archive.files[strings.TrimSpace(record[6])]
	switch {
	case item.form == nil || item.classification == nil:
		item.Reason = "备案表或定级报告路径缺失。"
	case item.form == item.classification:
		item.Reason = "备案表和定级报告需分别选择文件。"
	case !archive.validDocument(item.form, filingPackageForm):
		item.Reason = "备案表必须是可读的 PDF、DOC 或 DOCX 文件。"
	case !archive.validDocument(item.classification, filingPackageReport):
		item.Reason = "定级报告必须是可读的 PDF、DOC 或 DOCX 文件。"
	default:
		item.Status = historyImportReady
	}
	return item
}

// validDocument 复用备案包的检查并缓存结果，避免反复解压同一材料。
// 参数 file、category 为实际材料和清单指定类别。
// 返回值 材料适合按该类别入档时为真。
func (archive historyImportArchive) validDocument(file *zip.File, category string) bool {
	if identified := filingPackageCategory(file.Name); identified != "" && identified != category {
		return false
	}
	if valid, checked := archive.checked[file]; checked {
		return valid
	}
	_, err := attachmentExtension(path.Base(file.Name))
	valid := err == nil && verifyFilingPackageMember(file, category) == nil
	archive.checked[file] = valid
	return valid
}

// summarizeHistoryImport 投影每一行的对外字段及五类结果计数。
// 参数 candidates 为预检或提交后的条目。
// 返回值 可安全回传的批次报告。
func summarizeHistoryImport(candidates []historyImportCandidate) historyImportResult {
	result := historyImportResult{Rows: make([]historyImportRow, 0, len(candidates)),
		Counts: map[string]int{
			historyImportReady: 0, historyImportDuplicate: 0, historyImportConflict: 0,
			historyImportInvalid: 0, historyImportImported: 0,
		},
	}
	for _, item := range candidates {
		result.Rows = append(result.Rows, item.historyImportRow)
		result.Counts[item.Status]++
	}
	return result
}
