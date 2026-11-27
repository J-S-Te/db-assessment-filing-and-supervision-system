package materialexpiry

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
)

const resource = "material-expiries"
const pageLength = 20
const maxDocumentBody = (20 << 20) + (64 << 10)
const defaultWarningDays = 7

var (
	errInvalid   = errors.New("invalid material expiry input")
	errConflict  = errors.New("material expiry note conflict")
	errScope     = errors.New("material expiry evidence scope unavailable")
	errDate      = errors.New("replacement does not extend date")
	errUnchanged = errors.New("replacement equals current file")
)

type scope struct{ audience, name string }

type expirySummary struct {
	ID               string `json:"id"`
	CaseID           string `json:"caseId"`
	CaseKind         string `json:"caseKind"`
	Number           string `json:"number"`
	SystemName       string `json:"systemName"`
	OrganizationName string `json:"organizationName"`
	InstitutionName  string `json:"institutionName,omitempty"`
	AttachmentKind   string `json:"attachmentKind"`
	ValidUntil       string `json:"validUntil"`
	WarningDays      int    `json:"warningDays"`
	DateStatus       string `json:"dateStatus"`
	RenewalPending   bool   `json:"renewalPending"`
	Version          int64  `json:"version"`
}

type expiryRecord struct {
	expirySummary
	sequence, cycle int64
	sourceID, name  string
	evidence        string
	historical      bool
}

type expiryDetail struct {
	expirySummary
	SourceAttachmentID string        `json:"sourceAttachmentId,omitempty"`
	SourceFileName     string        `json:"sourceFileName"`
	Evidence           string        `json:"evidence"`
	AllowedActions     []string      `json:"allowedActions"`
	History            []expiryEvent `json:"history"`
	Renewals           []renewal     `json:"renewals"`
}

type expiryEvent struct {
	Version    int64     `json:"version"`
	Cycle      int64     `json:"cycle"`
	Action     string    `json:"action"`
	Evidence   string    `json:"evidence"`
	ValidUntil string    `json:"validUntil"`
	Actor      string    `json:"actor"`
	RenewalID  string    `json:"renewalId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

type renewal struct {
	ID          string     `json:"id"`
	Audience    string     `json:"audience"`
	Status      string     `json:"status"`
	ValidUntil  string     `json:"validUntil"`
	Evidence    string     `json:"evidence,omitempty"`
	FileName    string     `json:"fileName,omitempty"`
	Size        int64      `json:"size,omitempty"`
	Download    bool       `json:"download,omitempty"`
	SubmittedAt time.Time  `json:"submittedAt"`
	DecidedAt   *time.Time `json:"decidedAt,omitempty"`
}

type sourceOption struct {
	AttachmentID     string `json:"attachmentId"`
	CaseID           string `json:"caseId"`
	FilingID         string `json:"-"`
	CaseKind         string `json:"caseKind"`
	Number           string `json:"number"`
	SystemName       string `json:"systemName"`
	OrganizationName string `json:"organizationName"`
	InstitutionName  string `json:"institutionName,omitempty"`
	AttachmentKind   string `json:"attachmentKind"`
	FileName         string `json:"fileName"`
	uploadedAt       time.Time
}

type sourceCursor struct {
	uploadedAt   *time.Time
	attachmentID string
}

type registerInput struct {
	RequestID          string `json:"requestId"`
	SourceAttachmentID string `json:"sourceAttachmentId"`
	ValidUntil         string `json:"validUntil"`
	WarningDays        *int   `json:"warningDays,omitempty"`
	Evidence           string `json:"evidence"`
}

type actionInput struct {
	Action          string `json:"action"`
	ExpectedVersion int64  `json:"expectedVersion"`
	Comment         string `json:"comment,omitempty"`
}

type renewalInput struct {
	version         int64
	until, evidence string
}

type page[T any] struct {
	Items      []T    `json:"items"`
	NextBefore string `json:"nextBefore,omitempty"`
}

// validID 校验从路径或请求取得的 UUID。
// 参数 value 为公开的标识值。
// 返回值 可按 UUID 查询时返回真。
func validID(value string) bool {
	var id pgtype.UUID
	return len(value) == 36 && id.Scan(value) == nil && id.Valid
}

// validText 检查用户文本的编码、边界和可打印字符。
// 参数 value 为已清理的文本，min 和 max 为允许的字符数。
// 返回值 整体文本有效时返回真。
func validText(value string, min, max int) bool {
	count := utf8.RuneCountInString(value)
	return utf8.ValidString(value) && count >= min && count <= max &&
		strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) < 0
}

// validDate 校验明确指定的公历日期。
// 参数 value 为 YYYY-MM-DD 字符串。
// 返回值 能准确记录的日期返回真。
func validDate(value string) bool {
	date, err := time.Parse(time.DateOnly, value)
	return err == nil && date.Year() > 0 && value == date.Format(time.DateOnly)
}

// splitQuery 拒绝未知参数和重复键，验证各端的收件范围。
// 参数 raw 为查询串，audience 为席位，filters 为入口额外接受的参数。
// 返回值 请求范围、原始过滤值或校验错误。
func splitQuery(raw, audience string, filters ...string) (scope, url.Values, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return scope{}, nil, errInvalid
	}
	nameKey := map[string]string{"operator": "organization", "institution": "institution"}[audience]
	allowed := make(map[string]bool, len(filters)+1)
	for _, filter := range filters {
		allowed[filter] = true
	}
	if nameKey != "" {
		allowed[nameKey] = true
	}
	for name, fields := range values {
		if !allowed[name] || len(fields) != 1 {
			return scope{}, nil, errInvalid
		}
	}
	name := strings.TrimSpace(values.Get(nameKey))
	if audience != "" && (!validText(name, 1, 200) || strings.ContainsAny(name, "\n\t")) {
		return scope{}, nil, errInvalid
	}
	return scope{audience: audience, name: name}, values, nil
}

// parseReportFilter 读取可选的精确报告范围。
// 参数 values 为已确认未重复的查询字段。
// 返回值 指定的有效报告 UUID、空筛选或输入错误。
func parseReportFilter(values url.Values) (string, error) {
	if !values.Has("reportId") {
		return "", nil
	}
	raw := values.Get("reportId")
	var id pgtype.UUID
	if id.Scan(raw) != nil || !strings.EqualFold(id.String(), raw) {
		return "", errInvalid
	}
	return id.String(), nil
}

// pageCursor 解析台账前翻的正向序列标识。
// 参数 values 为已拒绝多值的筛选字段。
// 返回值 首屏标识、合法游标或输入错误。
func pageCursor(values url.Values) (int64, error) {
	if !values.Has("before") {
		return 0, nil
	}
	value := values.Get("before")
	cursor, err := strconv.ParseInt(value, 10, 64)
	if err != nil || cursor < 1 || value != strconv.FormatInt(cursor, 10) {
		return 0, errInvalid
	}
	return cursor, nil
}

// parseSourceCursor 解析材料上传时刻和标识组成的续页位置。
// 参数 values 为已拒绝多值的来源筛选字段。
// 返回值 首屏位置、有效的扫描界限或输入错误。
func parseSourceCursor(values url.Values) (sourceCursor, error) {
	if !values.Has("before") {
		return sourceCursor{}, nil
	}
	raw := values.Get("before")
	if len(raw) > 90 {
		return sourceCursor{}, errInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	clock, id, ok := strings.Cut(string(decoded), "/")
	if err != nil || !ok || !validID(id) || base64.RawURLEncoding.EncodeToString(decoded) != raw {
		return sourceCursor{}, errInvalid
	}
	uploadedAt, err := time.Parse(time.RFC3339Nano, clock)
	if err != nil || uploadedAt.Year() < 1 || uploadedAt.UTC().Format(time.RFC3339Nano) != clock {
		return sourceCursor{}, errInvalid
	}
	return sourceCursor{uploadedAt: &uploadedAt, attachmentID: id}, nil
}

// sourcePageCursor 封装本页最后一份原件的位置供下一页使用。
// 参数 source 为已展示的末条原件。
// 返回值 URL 安全的游标。
func sourcePageCursor(source sourceOption) string {
	value := source.uploadedAt.UTC().Format(time.RFC3339Nano) + "/" + source.AttachmentID
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

// validStatus 判断允许按哪些真实期限状态查询。
// 参数 value 为查询输入。
// 返回值 支持的状态或空筛选返回真。
func validStatus(value string) bool {
	switch value {
	case "", "valid", "due", "expired", "retired", "suspended":
		return true
	default:
		return false
	}
}
