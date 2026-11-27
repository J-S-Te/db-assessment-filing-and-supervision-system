package supervision

import (
	"errors"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"grade-business/internal/cases"
)

const regulatorPath = "/api/v1/supervisions"
const operatorPath = "/api/v1/operator/supervisions"
const pageSize = 20
const maxUploadBody = (20 << 20) + (64 << 10)

const (
	inspectionTemplate = "inspection_template"
	summaryTemplate    = "summary_template"
	inspectionForm     = "inspection_form"
	rectificationForm  = "rectification_summary"
)

var (
	errInput       = errors.New("invalid supervision input")
	errState       = errors.New("supervision state conflict")
	errDuplicate   = errors.New("supervision duplicate filing task")
	errScope       = errors.New("filing scope unavailable")
	errMaterials   = errors.New("supervision materials missing or stale")
	errAttachment  = errors.New("invalid supervision attachment")
	errSitePending = errors.New("field inspection still active")
)

type taskSummary struct {
	ID               string     `json:"id"`
	FilingID         string     `json:"filingId"`
	FilingNumber     string     `json:"filingNumber"`
	SystemName       string     `json:"systemName"`
	OrganizationName string     `json:"organizationName"`
	Title            string     `json:"title"`
	DueOn            string     `json:"dueOn"`
	Status           string     `json:"status"`
	Version          int64      `json:"version"`
	LeadAlertID      *string    `json:"leadAlertId,omitempty"`
	PublishedAt      *time.Time `json:"publishedAt,omitempty"`
	SubmittedAt      *time.Time `json:"submittedAt,omitempty"`
}

type taskRecord struct {
	taskSummary
	Sequence       int64
	SubmittedVer   int64
	ReturnedVer    int64
	CorrectionKind string
	HistoryVersion int64
	Historical     bool
}

type taskDetail struct {
	taskSummary
	Attachments    []cases.Attachment   `json:"attachments"`
	AllowedActions []string             `json:"allowedActions"`
	CorrectionKind string               `json:"correctionKind,omitempty"`
	Rectification  *rectificationCounts `json:"rectification,omitempty"`
}

type rectificationCounts struct {
	Open   int64 `json:"open"`
	Closed int64 `json:"closed"`
}

type reviewEvent struct {
	Version        int64                `json:"version"`
	Action         string               `json:"action"`
	FromStatus     string               `json:"fromStatus,omitempty"`
	ToStatus       string               `json:"toStatus"`
	Actor          string               `json:"actor"`
	Comment        string               `json:"comment,omitempty"`
	CorrectionKind string               `json:"correctionKind,omitempty"`
	CreatedAt      time.Time            `json:"createdAt"`
	Materials      []cases.Attachment   `json:"materials,omitempty"`
	Rectification  *rectificationCounts `json:"rectification,omitempty"`
}

type page[T any] struct {
	Items      []T    `json:"items"`
	NextBefore string `json:"nextBefore,omitempty"`
}

type filingOption struct {
	ID               string `json:"id"`
	Number           string `json:"number"`
	SystemName       string `json:"systemName"`
	OrganizationName string `json:"organizationName"`
}

type createInput struct {
	RequestID   string `json:"requestId"`
	FilingID    string `json:"filingId"`
	Title       string `json:"title"`
	DueOn       string `json:"dueOn"`
	LeadAlertID string `json:"leadAlertId,omitempty"`
}

type actionInput struct {
	Action          string `json:"action"`
	ExpectedVersion int64  `json:"expectedVersion"`
	Comment         string `json:"comment,omitempty"`
	CorrectionKind  string `json:"correctionKind,omitempty"`
}

type mutation struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Version int64  `json:"version"`
}

type attachmentMutation struct {
	Attachment cases.Attachment `json:"attachment"`
	Version    int64            `json:"version"`
}

// validID 判断路由和请求字段是否含规范 UUID。
// 参数 value 是外部记录标识。
// 返回值 标识格式正确时为真。
func validID(value string) bool {
	var id pgtype.UUID
	return len(value) == 36 && id.Scan(value) == nil && id.Valid
}

// validText 验证可展示文本并拒绝无效编码与控制字符。
// 参数 value 为输入文字，limit 为字段的最大码点数。
// 返回值 内容和长度均合理时为真。
func validText(value string, limit int) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

// validCreate 规范化草稿数据，拒绝非法业务标识和日历日期。
// 参数 input 为待验证的草稿请求。
// 返回值 输入可用于持久化时为真。
func validCreate(input *createInput) bool {
	input.RequestID = strings.ToLower(input.RequestID)
	input.FilingID = strings.ToLower(input.FilingID)
	input.LeadAlertID = strings.ToLower(input.LeadAlertID)
	input.Title = strings.TrimSpace(input.Title)
	date, err := time.Parse(time.DateOnly, input.DueOn)
	return validID(input.RequestID) && validID(input.FilingID) && validText(input.Title, 200) &&
		(input.LeadAlertID == "" || validID(input.LeadAlertID)) && err == nil && date.Year() > 0
}

// actionParameters 核对动作席位、版本和补正理由的组合。
// 参数 input 为操作请求，operator 标记是否由运营者办理。
// 返回值 参数组合有效时为真。
func actionParameters(input *actionInput, operator bool) bool {
	input.Comment = strings.TrimSpace(input.Comment)
	if input.ExpectedVersion < 1 || input.ExpectedVersion == math.MaxInt64 ||
		(input.Comment != "" && !validText(input.Comment, 1000)) {
		return false
	}
	if operator {
		return input.Action == "submit" && input.CorrectionKind == "" && input.Comment == ""
	}
	if input.Action == "return" {
		return input.Comment != "" && slices.Contains([]string{inspectionForm, rectificationForm, "both"}, input.CorrectionKind)
	}
	switch input.Action {
	case "cancel":
		return input.Comment != "" && input.CorrectionKind == ""
	case "publish":
		return input.Comment == "" && input.CorrectionKind == ""
	case "approve":
		return input.CorrectionKind == ""
	default:
		return false
	}
}

// parseQuery 约束接口接受的筛选字段，并按席位读取唯一单位名称。
// 参数 raw 为请求参数，operator 区分办理方，keys 为当前接口允许的筛选字段。
// 返回值 查询参数和单位范围，或输入错误。
func parseQuery(raw string, operator bool, keys ...string) (url.Values, string, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, "", errInput
	}
	owner := strings.TrimSpace(values.Get("organization"))
	if operator {
		keys = append(keys, "organization")
		if !validText(owner, 200) {
			return nil, "", errInput
		}
	}
	for key, fields := range values {
		if len(fields) != 1 || !slices.Contains(keys, key) {
			return nil, "", errInput
		}
	}
	return values, owner, nil
}

// cursorValue 验证用于向前分页的有界历史标识。
// 参数 values 为已去除重复字段的筛选参数。
// 返回值 从首屏开始时为零，否则为有效游标或参数错误。
func cursorValue(values url.Values) (int64, error) {
	if !values.Has("before") {
		return 0, nil
	}
	before, err := strconv.ParseInt(values.Get("before"), 10, 64)
	if err != nil || before < 1 {
		return 0, errInput
	}
	return before, nil
}

// validStatus 判断是否能在任务列表筛选指定状态。
// 参数 status 为状态筛选项，operator 表示运营者列表。
// 返回值 状态支持时返回真。
func validStatus(status string, operator bool) bool {
	if status == "draft" {
		return !operator
	}
	return slices.Contains([]string{"", "open", "submitted", "returned", "approved", "cancelled"}, status)
}
