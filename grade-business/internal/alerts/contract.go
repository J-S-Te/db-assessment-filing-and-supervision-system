package alerts

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

const (
	statusPending    = "pending"
	statusProcessing = "processing"
	statusClosed     = "closed"
	actionProcess    = "process"
	actionClose      = "close"
	actionConvert    = "convert"
	actionExtension  = "request_extension"
	actionFalseAlarm = "report_false_alarm"
	actionFeedback   = "submit_feedback"
	actionApprove    = "approve"
	actionReject     = "reject"
)

var (
	errInvalid  = errors.New("invalid local alert input")
	errConflict = errors.New("local alert status or version conflict")
	errDeadline = errors.New("requested deadline is not later than the existing deadline and business day")
)

type approvalBlockedError string

// Error 返回监管延期审批被阻止时可公开的具体原因。
// 参数 无，原因已存入错误值。
// 返回值 可显示给监管用户的冲突说明。
func (e approvalBlockedError) Error() string { return string(e) }

type alertItem struct {
	ID                string     `json:"id"`
	HazardID          string     `json:"hazardId"`
	Type              string     `json:"type"`
	Severity          string     `json:"severity"`
	SystemName        string     `json:"systemName"`
	OrganizationName  string     `json:"organizationName"`
	Deadline          string     `json:"deadline"`
	TriggeredDeadline *string    `json:"triggeredDeadline,omitempty"`
	CurrentlyOverdue  *bool      `json:"currentlyOverdue,omitempty"`
	Status            string     `json:"status"`
	TriggeredAt       time.Time  `json:"triggeredAt"`
	ClosedAt          *time.Time `json:"closedAt,omitempty"`
}

type alertDetail struct {
	alertItem
	Version               int64             `json:"version"`
	HazardStatus          string            `json:"hazardStatus"`
	Description           string            `json:"description"`
	AllowedActions        []string          `json:"allowedActions"`
	History               []alertEvent      `json:"history"`
	Disposition           *alertDisposition `json:"disposition,omitempty"`
	ApprovalBlockedReason string            `json:"approvalBlockedReason,omitempty"`
}

type alertEvent struct {
	Action            string    `json:"action"`
	FromStatus        string    `json:"fromStatus,omitempty"`
	ToStatus          string    `json:"toStatus"`
	Operator          string    `json:"operator"`
	Comment           string    `json:"comment,omitempty"`
	OccurredAt        time.Time `json:"occurredAt"`
	DispositionAction string    `json:"dispositionAction,omitempty"`
	RequestedDeadline string    `json:"requestedDeadline,omitempty"`
}

type alertDisposition struct {
	ID                string     `json:"id"`
	Action            string     `json:"action"`
	Status            string     `json:"status"`
	Comment           *string    `json:"comment,omitempty"`
	RequestedDeadline *string    `json:"requestedDeadline,omitempty"`
	RequestedBy       string     `json:"requestedBy"`
	RequesterAudience string     `json:"requesterAudience"`
	RequestedAt       time.Time  `json:"requestedAt"`
	DecisionComment   *string    `json:"decisionComment,omitempty"`
	DecidedBy         *string    `json:"decidedBy,omitempty"`
	DecidedAt         *time.Time `json:"decidedAt,omitempty"`
}

type actionInput struct {
	Action            string `json:"action"`
	ExpectedVersion   int64  `json:"expectedVersion"`
	Comment           string `json:"comment"`
	RequestedDeadline string `json:"requestedDeadline"`
}

type actionResult struct {
	ID          string            `json:"id"`
	Status      string            `json:"status"`
	Version     int64             `json:"version"`
	ClosedAt    *time.Time        `json:"closedAt,omitempty"`
	HazardID    string            `json:"hazardId,omitempty"`
	Disposition *alertDisposition `json:"disposition,omitempty"`
}

type alertPage struct {
	Items      []alertItem `json:"items"`
	NextCursor string      `json:"nextCursor,omitempty"`
}

// Summary 汇总站内预警的实际处理状态。
type Summary struct {
	Total               int64 `json:"total"`
	Pending             int64 `json:"pending"`
	Processing          int64 `json:"processing"`
	Closed              int64 `json:"closed"`
	PendingDispositions int64 `json:"pendingDispositions,omitempty"`
}

type listFilter struct {
	status, severity, kind, review, query string
	limit                                 int
	cursor                                alertCursor
}

type alertCursor struct {
	at time.Time
	id pgtype.UUID
}

// parseFilter 校验预警列表的状态、级别、触发类型、审批、关键词和分页字段。
// 参数 raw 为客户端的原始查询字符串。
// 返回值 业务过滤条件或无效输入错误。
func parseFilter(raw string) (listFilter, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return listFilter{}, errInvalid
	}
	for key, entries := range values {
		if len(entries) != 1 {
			return listFilter{}, errInvalid
		}
		switch key {
		case "status", "severity", "type", "review", "q", "cursor", "limit":
		default:
			return listFilter{}, errInvalid
		}
	}
	filter := listFilter{
		status: values.Get("status"), severity: values.Get("severity"),
		kind: values.Get("type"), review: values.Get("review"),
		query: strings.TrimSpace(values.Get("q")), limit: 20,
	}
	if !filter.valid() {
		return listFilter{}, errInvalid
	}
	if entries, present := values["limit"]; present {
		filter.limit, err = strconv.Atoi(entries[0])
		if err != nil || filter.limit < 1 || filter.limit > 100 {
			return listFilter{}, errInvalid
		}
	}
	filter.cursor, err = decodeCursor(values.Get("cursor"))
	return filter, err
}

// valid 判断筛选值是否使用预警领域枚举及有限长度的关键词。
// 参数 无，校验当前筛选对象。
// 返回值 所有筛选值符合预警领域规则时为真。
func (filter listFilter) valid() bool {
	switch filter.status {
	case "", statusPending, statusProcessing, statusClosed:
	default:
		return false
	}
	switch filter.severity {
	case "", "high", "medium", "low":
	default:
		return false
	}
	return (filter.kind == "" || filter.kind == "high_risk" || filter.kind == "overdue") &&
		(filter.review == "" || filter.review == statusPending) && validText(filter.query, 120)
}

// validText 允许可展示的文本及正常换行，拒绝超限和不支持的控制字符。
// 参数 value 为输入原文，limit 为最大字符数。
// 返回值 文字符合边界时为真。
func validText(value string, limit int) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit {
		return false
	}
	return !strings.ContainsFunc(value, func(char rune) bool {
		return unicode.IsControl(char) && char != '\n' && char != '\r' && char != '\t'
	})
}

// parseAlertID 将预警公开地址中的标识解析为数据库参数。
// 参数 raw 为客户端传入的 UUID。
// 返回值 UUID 及其格式是否合法。
func parseAlertID(raw string) (pgtype.UUID, bool) {
	var id pgtype.UUID
	if len(raw) != 36 || raw[8] != '-' || raw[13] != '-' || raw[18] != '-' || raw[23] != '-' {
		return id, false
	}
	err := id.Scan(raw)
	return id, err == nil && id.Valid
}

// decodeCursor 从上一页返回值读取稳定排序位置。
// 参数 raw 为编码后的触发时间及预警标识，空值表示第一页。
// 返回值 分页位置或游标错误。
func decodeCursor(raw string) (alertCursor, error) {
	if raw == "" {
		return alertCursor{}, nil
	}
	if len(raw) > 110 {
		return alertCursor{}, errInvalid
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return alertCursor{}, errInvalid
	}
	stamp, identifier, found := strings.Cut(string(data), ",")
	at, dateErr := time.Parse(time.RFC3339Nano, stamp)
	id, valid := parseAlertID(identifier)
	if !found || !valid || dateErr != nil || at.IsZero() {
		return alertCursor{}, errInvalid
	}
	return alertCursor{at: at, id: id}, nil
}

// encodeCursor 生成下一页使用的时间和预警标识。
// 参数 item 为当前页面已返回的最后一条预警。
// 返回值 用于分页查询的游标。
func encodeCursor(item alertItem) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(item.TriggeredAt.UTC().Format(time.RFC3339Nano) + "," + item.ID),
	)
}

// validateAction 校验指定办理端的动作、备注和延期字段。
// 参数 input 为客户端提交的数据，audience 为办理端，空值代表监管端。
// 返回值 字段合法时返回真，并清理备注两端空白。
func validateAction(input *actionInput, audience string) bool {
	input.Comment = strings.TrimSpace(input.Comment)
	if input.ExpectedVersion < 1 || !validText(input.Comment, 1000) {
		return false
	}
	hasComment := input.Comment != "" && input.RequestedDeadline == ""
	if audience == audienceInstitution {
		return hasComment && (input.Action == actionProcess || input.Action == actionFeedback)
	}
	if audience == "" {
		return hasComment && (input.Action == actionProcess || input.Action == actionClose ||
			input.Action == actionApprove || input.Action == actionReject)
	}
	switch input.Action {
	case actionConvert:
		return input.RequestedDeadline == ""
	case actionFalseAlarm:
		return hasComment
	case actionExtension:
		requested, err := time.Parse(time.DateOnly, input.RequestedDeadline)
		return input.Comment != "" && err == nil && requested.Year() > 0 &&
			requested.Format(time.DateOnly) == input.RequestedDeadline
	default:
		return false
	}
}

// allowedActions 计算当前预警状态允许的管理动作。
// 参数 status 为表中保存的预警状态。
// 返回值 该预警当前可执行的操作。
func allowedActions(status string) []string {
	switch status {
	case statusPending:
		return []string{actionProcess, actionClose}
	case statusProcessing:
		return []string{actionClose}
	default:
		return []string{}
	}
}
