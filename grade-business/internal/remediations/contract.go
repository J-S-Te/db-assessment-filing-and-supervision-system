package remediations

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
)

const (
	draft         = "draft"
	pendingReview = "pending_review"
	approved      = "approved"
	returned      = "returned"
	withdrawn     = "withdrawn"
	dateLayout    = "2006-01-02"
	maxRisks      = 50
)

var (
	errInvalid               = errors.New("invalid remediation input")
	errNotFound              = errors.New("remediation record not found")
	errConflict              = errors.New("remediation version or state conflict")
	errReportChange          = errors.New("remediation report change blocked")
	errMaterialReportChanged = errors.New("remediation material belongs to the previous report")
	errMaterials             = errors.New("remediation materials required")
	errReport                = errors.New("remediation report is not eligible")
	errRiskDueDate           = errors.New("review deadline precedes a risk deadline")
	errLatestReport          = errors.New("first plan must use the latest approved report")
	errFilingInReview        = errors.New("filing already has a plan pending review")
)

type planInput struct {
	ReportID        string     `json:"reportId"`
	Deadline        string     `json:"deadline"`
	Risks           []riskItem `json:"risks"`
	ExpectedVersion int64      `json:"expectedVersion,omitempty"`
}

type riskItem struct {
	Risk           string `json:"risk"`
	Description    string `json:"description"`
	Measure        string `json:"measure"`
	Responsibility string `json:"responsibility"`
	Deadline       string `json:"deadline"`
}

type submitInput struct {
	ExpectedVersion int64 `json:"expectedVersion"`
}

type reviewInput struct {
	Decision        string `json:"decision"`
	Deadline        string `json:"deadline"`
	Comment         string `json:"comment"`
	Reason          string `json:"reason,omitempty"`
	ExpectedVersion int64  `json:"expectedVersion"`
}

type riskCounts struct {
	High   int `json:"high"`
	Medium int `json:"medium"`
	Low    int `json:"low"`
}

type summary struct {
	ID               string     `json:"id"`
	ReportID         string     `json:"reportId"`
	ReportNumber     string     `json:"reportNumber"`
	FilingNumber     string     `json:"filingNumber"`
	SystemName       string     `json:"systemName"`
	OrganizationName string     `json:"organizationName"`
	Deadline         string     `json:"deadline"`
	Status           string     `json:"status"`
	Submission       int        `json:"submission"`
	SubmittedAt      *time.Time `json:"submittedAt,omitempty"`
	RiskCounts       riskCounts `json:"riskCounts"`
	ReminderEligible bool       `json:"reminderEligible"`
}

type detail struct {
	summary
	Version        int64      `json:"version"`
	AllowedActions []string   `json:"allowedActions"`
	Risks          []riskItem `json:"risks"`
}

type mutation struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Version int64  `json:"version"`
}

type eligibleReport struct {
	ID               string `json:"id"`
	Number           string `json:"number"`
	FilingNumber     string `json:"filingNumber"`
	SystemName       string `json:"systemName"`
	OrganizationName string `json:"organizationName"`
}

type reviewEvent struct {
	Action     string    `json:"action"`
	FromStatus string    `json:"fromStatus"`
	ToStatus   string    `json:"toStatus"`
	Operator   string    `json:"operator"`
	Comment    string    `json:"comment,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	Deadline   string    `json:"deadline"`
	Submission int       `json:"submission"`
	OccurredAt time.Time `json:"occurredAt"`
}

type statistics struct {
	Pending         int64  `json:"pending"`
	Returned        int64  `json:"returned"`
	DueWithin10Days int64  `json:"dueWithin10Days"`
	FirstPassRate   *int64 `json:"firstPassRate"`
}

type collection[T any] struct {
	Items []T `json:"items"`
}

type page struct {
	Items      []summary `json:"items"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

type cursorKey struct {
	At string
	ID string
}

type listInput struct {
	Query  string
	Status string
	Due    string
	Limit  int
	Key    cursorKey
}

// validatePlan 清理整改录入值并核对报告、风险级别与期限范围。
// 参数 input 为新建或编辑表单对应的请求。
// 返回值 校验错误，或者规范化后返回 nil。
func validatePlan(input *planInput) error {
	if !validID(input.ReportID) || !validDate(input.Deadline) ||
		len(input.Risks) == 0 || len(input.Risks) > maxRisks {
		return errInvalid
	}
	for i := range input.Risks {
		risk := &input.Risks[i]
		risk.Description = strings.TrimSpace(risk.Description)
		risk.Measure = strings.TrimSpace(risk.Measure)
		risk.Responsibility = strings.TrimSpace(risk.Responsibility)
		if !validRisk(*risk, input.Deadline) {
			return errInvalid
		}
	}
	return nil
}

// validRisk 判断单个风险项是否具有可执行的措施、责任和期限。
// 参数 risk 已清理输入文字，deadline 为整改方案的最终期限。
// 返回值 所有必要内容有效时为真。
func validRisk(risk riskItem, deadline string) bool {
	level := risk.Risk == "high" || risk.Risk == "medium" || risk.Risk == "low"
	return level && validText(risk.Description, 300) && validText(risk.Measure, 600) &&
		validText(risk.Responsibility, 160) && validDate(risk.Deadline) && risk.Deadline <= deadline
}

// validateReview 核对审核期限、意见与退回补正原因。
// 参数 input 为即将提交的审核结论。
// 返回值 请求无效时返回校验错误。
func validateReview(input *reviewInput) error {
	input.Comment = strings.TrimSpace(input.Comment)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.ExpectedVersion < 1 || !validDate(input.Deadline) || !validText(input.Comment, 1000) {
		return errInvalid
	}
	switch input.Decision {
	case "approve":
		if input.Reason == "" {
			return nil
		}
	case "return_for_correction":
		if validText(input.Reason, 1000) {
			return nil
		}
	}
	return errInvalid
}

// validText 核对有长度边界的必填业务文本。
// 参数 value 为实际文本，limit 为最大字符数。
// 返回值 文字有效时为真。
func validText(value string, limit int) bool {
	return value != "" && utf8.ValidString(value) &&
		utf8.RuneCountInString(value) <= limit && !strings.ContainsRune(value, 0)
}

// validDate 检验受支持的日期格式与日期值。
// 参数 date 为客户端提供的公历日期。
// 返回值 yyyy-mm-dd 公历日期有效时为真。
func validDate(date string) bool {
	parsed, err := time.Parse(dateLayout, date)
	return err == nil && parsed.Year() >= 1 && parsed.Format(dateLayout) == date
}

// validID 核对计划、报告或附件标识的格式。
// 参数 id 为应为标准 UUID 的资源标识。
// 返回值 有效 UUID 返回真。
func validID(id string) bool {
	var value pgtype.UUID
	return len(id) == 36 && value.Scan(id) == nil && value.Valid
}

// allowedActions 根据当前状态和材料登记情况给出页面操作权限。
// 参数 status 为整改状态，hasMaterials 表示当前已有登记材料。
// 返回值 可执行的动作，终止状态返回空数组。
func allowedActions(status string, hasMaterials bool) []string {
	switch status {
	case draft:
		if !hasMaterials {
			return []string{"edit", "submit", "change_report"}
		}
		return []string{"edit", "submit"}
	case returned:
		return []string{"edit", "submit"}
	case pendingReview:
		return []string{"approve", "return_for_correction"}
	default:
		return []string{}
	}
}

// parseList 核对整改列表筛选并提取游标。
// 参数 values 为已解码的 URL 查询，eligibleOnly 表示只查询可选报告。
// 返回值 有界分页和筛选条件，或输入错误。
func parseList(values url.Values, eligibleOnly bool) (listInput, error) {
	input := listInput{Query: strings.TrimSpace(values.Get("q")), Limit: 20}
	if err := validateQuery(values, eligibleOnly); err != nil {
		return listInput{}, err
	}
	if input.Query != "" && !validText(input.Query, 120) {
		return listInput{}, errInvalid
	}
	if n := values.Get("limit"); n != "" {
		limit, err := strconv.Atoi(n)
		if err != nil || limit < 1 || limit > 50 {
			return listInput{}, errInvalid
		}
		input.Limit = limit
	}
	input.Status, input.Due = values.Get("status"), values.Get("due")
	if !validFilter(input.Status, input.Due) {
		return listInput{}, errInvalid
	}
	key, err := parseCursor(values.Get("cursor"))
	input.Key = key
	return input, err
}

// validateQuery 拒绝未知或重复的查询条件。
// 参数 values 为客户端查询，eligibleOnly 标识报告简表。
// 返回值 查询字段不符合接口契约时返回错误。
func validateQuery(values url.Values, eligibleOnly bool) error {
	for key, entries := range values {
		common := key == "q" || key == "limit"
		filter := !eligibleOnly && (key == "status" || key == "due" || key == "cursor")
		if (!common && !filter) || len(entries) != 1 {
			return errInvalid
		}
	}
	return nil
}

// validFilter 判断状态和期限是否属于支持的筛选值。
// 参数 status 为审核状态，due 为期限范围选项。
// 返回值 两个筛选值均可用时为真。
func validFilter(status, due string) bool {
	switch status {
	case "", draft, pendingReview, approved, returned, withdrawn:
	default:
		return false
	}
	switch due {
	case "", "overdue", "within_10_days", "within_30_days":
		return true
	default:
		return false
	}
}

// parseCursor 恢复供分页比较的创建时间和标识。
// 参数 encoded 为列表公开的跳转游标，可为空。
// 返回值 定位信息或非法游标错误。
func parseCursor(encoded string) (cursorKey, error) {
	if encoded == "" {
		return cursorKey{}, nil
	}
	if len(encoded) > 150 {
		return cursorKey{}, errInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	at, id, found := strings.Cut(string(raw), "|")
	if err != nil || !found || !validID(id) {
		return cursorKey{}, errInvalid
	}
	if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
		return cursorKey{}, errInvalid
	}
	return cursorKey{At: at, ID: id}, nil
}

// makeCursor 生成上一页末条记录后的定位标识。
// 参数 at 为创建时间的 RFC3339 表示，id 为记录标识。
// 返回值 URL 安全编码的游标。
func makeCursor(at, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at + "|" + id))
}
