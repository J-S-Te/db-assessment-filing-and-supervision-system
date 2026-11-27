package reporthistory

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var fieldKeys = [...]string{"assets", "findings", "risk", "recommendations"}

const stateInsufficient = "insufficient"
const stateSame = "same"
const stateChanged = "changed"

type storedField struct {
	Key       string  `json:"key"`
	Original  string  `json:"original"`
	Value     *string `json:"value"`
	Start     int     `json:"startParagraph"`
	End       int     `json:"endParagraph"`
	Truncated bool    `json:"truncated"`
}

type reportEvidence struct {
	AttachmentID string    `json:"attachmentId"`
	FileName     string    `json:"fileName"`
	Version      int64     `json:"version"`
	ConfirmedAt  time.Time `json:"confirmedAt"`
	ConfirmedBy  *string   `json:"confirmedBy,omitempty"`
}

type period struct {
	Report   historyReport   `json:"report"`
	Evidence *reportEvidence `json:"evidence,omitempty"`
	fields   []storedField
}

type fieldValue struct {
	Value     string `json:"value"`
	Start     int    `json:"startParagraph"`
	End       int    `json:"endParagraph"`
	Truncated bool   `json:"truncated,omitempty"`
}

type fieldComparison struct {
	Key   string      `json:"key"`
	State string      `json:"state"`
	From  *fieldValue `json:"from,omitempty"`
	To    *fieldValue `json:"to,omitempty"`
}

type findingsComparison struct {
	OnlyFrom []string `json:"onlyFrom"`
	OnlyTo   []string `json:"onlyTo"`
	Common   []string `json:"common"`
}

type comparison struct {
	From       period              `json:"from"`
	To         period              `json:"to"`
	RiskChange string              `json:"riskChange"`
	Fields     []fieldComparison   `json:"fields"`
	Findings   *findingsComparison `json:"findings,omitempty"`
}

// compare 在同一数据库快照读取两份归档正文的已确认字段并计算差异。
// 参数 ctx 控制读取，anchor 保证同备案归属，from 与 to 为选择的前后期报告。
// 返回值 比对结果或参数、顺序、归属及读取错误。
func (s *Service) compare(ctx context.Context, anchor, from, to pgtype.UUID) (comparison, error) {
	rows, err := s.db.Query(ctx, `SELECT `+archiveProjection+`, e.fields,e.version,e.confirmed_at,e.confirmed_by`+
		archiveFrom+`LEFT JOIN report_extractions e ON e.attachment_id=body.id AND e.confirmed_at IS NOT NULL
		WHERE anchor.id=$1 AND anchor.kind='report' AND anchor.discarded_at IS NULL AND r.id IN ($2,$3)
		ORDER BY `+reportChronology+`,r.id`, anchor, from, to)
	if err != nil {
		return comparison{}, err
	}
	items, err := pgx.CollectRows(rows, readPeriod)
	if err != nil {
		return comparison{}, err
	}
	if len(items) != 2 {
		return comparison{}, pgx.ErrNoRows
	}
	if items[0].Report.ID != from.String() {
		return comparison{}, errOrder
	}
	return comparePeriods(items[0], items[1]), nil
}

// readPeriod 读取报告基本信息以及最新正文中已确认的结构化字段。
// 参数 row 为指定归档报告与对应正文的查询行。
// 返回值 一期实际报告与校对证据，或扫描错误。
func readPeriod(row pgx.CollectableRow) (period, error) {
	var base archiveRow
	var fields []storedField
	var version *int64
	var confirmedAt *time.Time
	var confirmedBy *string
	err := row.Scan(append(base.targets(), &fields, &version, &confirmedAt, &confirmedBy)...)
	if err != nil {
		return period{}, err
	}
	out := period{Report: base.publicReport()}
	if confirmedAt != nil {
		out.Evidence = &reportEvidence{
			AttachmentID: out.Report.ReportFile.ID, FileName: out.Report.ReportFile.FileName,
			Version: *version, ConfirmedAt: *confirmedAt, ConfirmedBy: confirmedBy,
		}
		out.fields = fields
	}
	return out, nil
}

// comparePeriods 对比持久化风险等级、人工确认字段与两期原文行。
// 参数 from 和 to 为已按实际时序读取的归档报告。
// 返回值 内容变化和证据充分性，不生成业务结论。
func comparePeriods(from, to period) comparison {
	result := comparison{
		From: from, To: to, RiskChange: compareRisk(from.Report.Risk, to.Report.Risk),
		Fields: make([]fieldComparison, 0, len(fieldKeys)),
	}
	left, right := confirmedFields(from.fields), confirmedFields(to.fields)
	for _, key := range fieldKeys {
		a, b := left[key], right[key]
		state := stateInsufficient
		if a.complete() && b.complete() {
			state = stateSame
			if a.Value != b.Value {
				state = stateChanged
			}
			if key == "findings" {
				result.Findings = groupFindings(a.Value, b.Value)
			}
		}
		result.Fields = append(result.Fields, fieldComparison{Key: key, State: state, From: a, To: b})
	}
	return result
}

// confirmedFields 选择已保存的校对值，缺省时保留已确认原文。
// 参数 fields 为数据库所存的提取及人工确认结果。
// 返回值 按领域栏目键组织的实际文本和段落位置。
func confirmedFields(fields []storedField) map[string]*fieldValue {
	result := make(map[string]*fieldValue, len(fields))
	for _, field := range fields {
		value := field.Original
		if field.Value != nil {
			value = *field.Value
		}
		result[field.Key] = &fieldValue{
			Value: strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n")),
			Start: field.Start, End: field.End, Truncated: field.Truncated,
		}
	}
	return result
}

// complete 判断当前字段能否作为完整的前后期差异证据。
// 参数 无，检查文本、截断标记与原文段落位置。
// 返回值 存在有效全文及证据位置时为真。
func (field *fieldValue) complete() bool {
	return field != nil && field.Value != "" && !field.Truncated && field.Start > 0 && field.End >= field.Start
}

// compareRisk 按业务库记录的低、中、高风险顺序判断等级变化。
// 参数 from、to 为前后期各自持久化的风险等级。
// 返回值 风险等级升高、降低或相同的业务代码。
func compareRisk(from, to string) string {
	ranks := map[string]int{"low": 1, "medium": 2, "high": 3}
	switch {
	case ranks[from] < ranks[to]:
		return "increased"
	case ranks[from] > ranks[to]:
		return "decreased"
	default:
		return "same"
	}
}

// groupFindings 使用逐行一致的已校对文本标出两期记录中的可验证差异。
// 参数 from、to 为两侧完整的问题栏目内容。
// 返回值 只在前期、只在后期和两期记录都有的原文行。
func groupFindings(from, to string) *findingsComparison {
	result := &findingsComparison{OnlyFrom: []string{}, OnlyTo: []string{}, Common: []string{}}
	left, leftSet := findingLines(from)
	right, rightSet := findingLines(to)
	for _, line := range left {
		if rightSet[line] {
			result.Common = append(result.Common, line)
		} else {
			result.OnlyFrom = append(result.OnlyFrom, line)
		}
	}
	for _, line := range right {
		if !leftSet[line] {
			result.OnlyTo = append(result.OnlyTo, line)
		}
	}
	return result
}

// findingLines 忽略空白行并按原有顺序去重实际校对文本。
// 参数 content 为完整问题栏目中的文本。
// 返回值 可展示的有序非空行以及用于精确匹配的集合。
func findingLines(content string) ([]string, map[string]bool) {
	var ordered []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !seen[line] {
			seen[line] = true
			ordered = append(ordered, line)
		}
	}
	return ordered, seen
}
