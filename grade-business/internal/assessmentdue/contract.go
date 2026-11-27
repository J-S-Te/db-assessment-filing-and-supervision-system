package assessmentdue

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
)

const resource = "assessment-dues"
const defaultWarningDays = 7
const pageSize = 20

var errInput = errors.New("invalid assessment date input")
var errScope = errors.New("active filing required")
var errConflict = errors.New("assessment date conflict")

type summary struct {
	ID               string `json:"id"`
	FilingID         string `json:"filingId"`
	FilingNumber     string `json:"filingNumber"`
	SystemName       string `json:"systemName"`
	OrganizationName string `json:"organizationName"`
	InstitutionName  string `json:"institutionName,omitempty"`
	DueOn            string `json:"dueOn"`
	WarningDays      int    `json:"warningDays"`
	Status           string `json:"status"`
	Version          int64  `json:"version"`
}

type event struct {
	Version     int64     `json:"version"`
	Cycle       int64     `json:"cycle"`
	Action      string    `json:"action"`
	DueOn       string    `json:"dueOn"`
	WarningDays int       `json:"warningDays"`
	Evidence    string    `json:"evidence"`
	Actor       string    `json:"actor"`
	CreatedAt   time.Time `json:"createdAt"`
}

type detail struct {
	summary
	Evidence  string    `json:"evidence,omitempty"`
	History   []event   `json:"history,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type record struct {
	summary
	evidence         string
	sequence, cycle  int64
	created, updated time.Time
}

type recordPage struct {
	Items      []summary `json:"items"`
	NextBefore string    `json:"nextBefore,omitempty"`
}

type registration struct {
	RequestID   string `json:"requestId"`
	FilingID    string `json:"filingId"`
	DueOn       string `json:"dueOn"`
	Evidence    string `json:"evidence"`
	WarningDays *int   `json:"warningDays,omitempty"`
}

type change struct {
	Action          string `json:"action"`
	ExpectedVersion int64  `json:"expectedVersion"`
	DueOn           string `json:"dueOn,omitempty"`
	Evidence        string `json:"evidence"`
	WarningDays     *int   `json:"warningDays,omitempty"`
}

type filters struct {
	owner, audience string
	filing, status  string
	before          int64
}

// validID 核对规范的 UUID 形式，禁止将不可转换的标识交给数据库。
// 参数 raw 为外部记录标识。
// 返回值 符合标准形式时为真。
func validID(raw string) bool {
	var id pgtype.UUID
	return id.Scan(raw) == nil && id.Valid && strings.EqualFold(raw, id.String())
}

// validDate 判断是否为明确可核验的公历业务日期。
// 参数 raw 为 YYYY-MM-DD 格式的日期。
// 返回值 合法日期为真。
func validDate(raw string) bool {
	date, err := time.Parse(time.DateOnly, raw)
	return err == nil && date.Year() > 0 && date.Format(time.DateOnly) == raw
}

// validText 判断日期依据或收件范围是否可安全展示。
// 参数 raw、max 分别为已清理文本和允许的码点数。
// 返回值 有实际内容且符合边界时为真。
func validText(raw string, max int) bool {
	return raw != "" && utf8.ValidString(raw) && utf8.RuneCountInString(raw) <= max &&
		strings.IndexFunc(raw, unicode.IsControl) < 0
}

// validWarning 核对可选的提前提醒日数。
// 参数 days 为用户选择的日数，也可省略。
// 返回值 处于允许区间或省略时为真。
func validWarning(days *int) bool {
	return days == nil || (*days >= 0 && *days <= 90)
}

// validRegistration 整理登记依据与标识，确认显式日期和提醒配置。
// 参数 input 为监管登记请求。
// 返回值 具备可保存字段时为真。
func validRegistration(input *registration) bool {
	input.RequestID = strings.ToLower(input.RequestID)
	input.FilingID = strings.ToLower(input.FilingID)
	input.Evidence = strings.TrimSpace(input.Evidence)
	return validID(input.RequestID) && validID(input.FilingID) && validDate(input.DueOn) &&
		validText(input.Evidence, 1000) && validWarning(input.WarningDays)
}

// validChange 验证修订或关闭的明确依据及适用字段。
// 参数 input 为监管提交的版本保护操作。
// 返回值 字段组合符合动作语义时为真。
func validChange(input *change) bool {
	input.Evidence = strings.TrimSpace(input.Evidence)
	if input.ExpectedVersion < 1 || !validText(input.Evidence, 1000) {
		return false
	}
	switch input.Action {
	case "revise":
		return validDate(input.DueOn) && validWarning(input.WarningDays)
	case "close":
		return input.DueOn == "" && input.WarningDays == nil
	default:
		return false
	}
}

// listStatus 判断日期状态是否属于台账的公开枚举。
// 参数 raw 为台账状态筛选。
// 返回值 受支持状态或省略时为真。
func listStatus(raw string) bool {
	switch raw {
	case "", "upcoming", "due_soon", "due", "expired", "closed", "suspended":
		return true
	default:
		return false
	}
}

// parseFilters 验证角色范围、分页及列表限定条件。
// 参数 raw、audience 为查询串和端点角色，list 表示允许列表条件。
// 返回值 已规范的范围或输入错误。
func parseFilters(raw, audience string, list bool) (filters, error) {
	result := filters{audience: audience}
	query, err := url.ParseQuery(raw)
	if err != nil {
		return result, errInput
	}
	ownerKey := map[string]string{"operator": "organization", "institution": "institution"}[audience]
	for key, values := range query {
		if len(values) != 1 || (key != ownerKey && (!list ||
			(key != "filingId" && key != "status" && key != "before"))) || key == "" {
			return result, errInput
		}
	}
	result.owner = strings.TrimSpace(query.Get(ownerKey))
	result.filing, result.status = query.Get("filingId"), query.Get("status")
	if (audience != "" && !validText(result.owner, 200)) ||
		(query.Has("filingId") && !validID(result.filing)) || !listStatus(result.status) {
		return result, errInput
	}
	if query.Has("before") {
		raw := query.Get("before")
		result.before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || result.before < 1 || strconv.FormatInt(result.before, 10) != raw {
			return result, errInput
		}
	}
	return result, nil
}
