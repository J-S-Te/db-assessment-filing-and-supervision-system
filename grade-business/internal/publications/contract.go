package publications

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

const (
	draft          = "draft"
	published      = "published"
	withdrawn      = "withdrawn"
	pageLimit      = 20
	materialsGroup = "materials"

	operatorAudience    = "operator"
	institutionAudience = "institution"
	allAudience         = "all"
	selectedAudience    = "selected"
	recipientLimit      = 200
)

var (
	errInvalid                    = errors.New("invalid publication input")
	errMissing                    = errors.New("publication not found")
	errConflict                   = errors.New("publication status or version changed")
	errMaterialAttachmentRequired = errors.New("material requires an attachment")
)

type content struct {
	Category  string      `json:"category"`
	Title     string      `json:"title"`
	Publisher string      `json:"publisher"`
	Body      string      `json:"body"`
	Pinned    bool        `json:"pinned"`
	AlertID   *string     `json:"alertId,omitempty"`
	Audience  *string     `json:"audience,omitempty"`
	Targets   []recipient `json:"targets,omitempty"`
}

type editInput struct {
	content
	ExpectedVersion int64 `json:"expectedVersion"`
}

type actionInput struct {
	Action          string `json:"action"`
	ExpectedVersion int64  `json:"expectedVersion"`
}

type summary struct {
	ID               string     `json:"id"`
	Category         string     `json:"category"`
	Title            string     `json:"title"`
	Publisher        string     `json:"publisher"`
	Pinned           bool       `json:"pinned"`
	PublishedAt      *time.Time `json:"publishedAt,omitempty"`
	Status           string     `json:"status"`
	Version          int64      `json:"version"`
	PublishedVersion *int64     `json:"publishedVersion,omitempty"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	Sequence         int64      `json:"-"`
	Audience         string     `json:"audience"`
	DeliveredAt      *time.Time `json:"deliveredAt,omitempty"`
	readState
	Attachments []documentAttachment `json:"attachments,omitempty"`
}

type detail struct {
	summary
	Body           string      `json:"body"`
	AlertID        *string     `json:"alertId,omitempty"`
	Targets        []recipient `json:"targets,omitempty"`
	AllowedActions []string    `json:"allowedActions,omitempty"`
}

type revision struct {
	Version int64  `json:"version"`
	Action  string `json:"action"`
	Status  string `json:"status"`
	content
	Operator    string               `json:"operator"`
	OccurredAt  time.Time            `json:"occurredAt"`
	Attachments []documentAttachment `json:"attachments,omitempty"`
}

type page[T any] struct {
	Items      []T    `json:"items"`
	NextBefore string `json:"nextBefore,omitempty"`
}

type listFilter struct {
	category, status, query string
	pinned                  bool
	before                  int64
	scope                   recipient
	read                    string
	materials               bool
}

// validCategory 核对站内信息使用的公告与资料类别。
// 参数 category 为表单或筛选器输入的类型。
// 返回值 受支持时为真。
func validCategory(category string) bool {
	return category == "bulletin" || category == "security_advisory" ||
		category == "announcement" || isMaterialCategory(category)
}

// isMaterialCategory 判断发布内容是否属于运营者资料库。
// 参数 category 为公告类别。
// 返回值 政策标准和常用资料返回真。
func isMaterialCategory(category string) bool {
	return category == "policy_standard" || category == "common_material"
}

// boundedText 校验业务字段长度和控制字符。
// 参数 value 为规范化的文字，limit 为最大字符数，multiline 允许正文换行和缩进。
// 返回值 有效的必填文字为真。
func boundedText(value string, limit int, multiline bool) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit {
		return false
	}
	return strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\r' || r == '\t'))
	}) < 0
}

// validateContent 核对公告标题、单位、正文和类型并清理首尾空白。
// 参数 item 为外部提供的发布草稿字段。
// 返回值 有效时为真，item 保留规范化内容。
func validateContent(item *content) bool {
	item.Title = strings.TrimSpace(item.Title)
	item.Publisher = strings.TrimSpace(item.Publisher)
	item.Body = strings.TrimSpace(item.Body)
	if !normalizePolicy(item) {
		return false
	}
	if item.AlertID != nil {
		id, valid := parseUUID(*item.AlertID)
		if !valid || item.Category != "security_advisory" {
			return false
		}
		canonical := id.String()
		item.AlertID = &canonical
	}
	return validCategory(item.Category) && boundedText(item.Title, 200, false) &&
		boundedText(item.Publisher, 120, false) && boundedText(item.Body, 8000, true)
}

// parseUUID 转换公告公开标识供参数化查询使用。
// 参数 value 为路由中的原始标识。
// 返回值 UUID 和格式有效标志。
func parseUUID(value string) (pgtype.UUID, bool) {
	var id pgtype.UUID
	if len(value) != 36 || id.Scan(value) != nil || !id.Valid {
		return pgtype.UUID{}, false
	}
	return id, true
}

// parseFilter 解析公告列表的筛选、收件范围和分页游标。
// 参数 raw 为原始查询，audience 为空时读取监管列表，否则为门户角色。
// 返回值 有效筛选，或参数错误。
func parseFilter(raw, audience string) (listFilter, error) {
	fields := []string{"category", "q", "before"}
	if audience == "" {
		fields = append(fields, "status")
	} else {
		fields = append(fields, scopeField(audience), "read")
	}
	if audience == operatorAudience {
		fields = append(fields, "group")
	}
	values, err := publicationQuery(raw, fields...)
	if err != nil {
		return listFilter{}, err
	}
	query := listFilter{
		category: values.Get("category"), status: values.Get("status"),
		query: strings.TrimSpace(values.Get("q")), read: values.Get("read"),
		materials: values.Get("group") == materialsGroup,
	}
	if values.Has("group") && !query.materials {
		return listFilter{}, errInvalid
	}
	if audience != "" {
		query.scope, err = queryScope(values, audience, audience == institutionAudience || query.materials)
		if err != nil || (values.Has("read") && !validReadFilter(query)) {
			return listFilter{}, errInvalid
		}
	}
	if !validListFilter(query) {
		return listFilter{}, errInvalid
	}
	if values.Has("before") {
		flag, before, ok := strings.Cut(values.Get("before"), ":")
		if !ok || (flag != "1" && flag != "0") {
			return listFilter{}, errInvalid
		}
		query.pinned = flag == "1"
		query.before, err = positiveNumber(before)
	}
	return query, err
}

// validListFilter 校验列表的公告类型、状态与关键词。
// 参数 query 为提取后的列表筛选。
// 返回值 所有条件受支持时为真。
func validListFilter(query listFilter) bool {
	categoryOK := query.category == "" || validCategory(query.category) &&
		(!query.materials || isMaterialCategory(query.category))
	textOK := query.query == "" || boundedText(query.query, 120, false)
	statusOK := query.status == "" || query.status == draft ||
		query.status == published || query.status == withdrawn
	return categoryOK && textOK && statusOK
}

// validReadFilter 限制已读筛选仅用于具有明确收件方的门户。
// 参数 query 为带有门户范围的列表筛选。
// 返回值 收件方存在且筛选值受支持时为真。
func validReadFilter(query listFilter) bool {
	return query.scope.Recipient != "" && (query.read == "read" || query.read == "unread")
}

// historyCursor 读取公告版本历史的唯一游标。
// 参数 raw 为原始查询字符串。
// 返回值 首页零值、正版本号或校验失败。
func historyCursor(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	values, err := url.ParseQuery(raw)
	if err != nil || len(values) != 1 || len(values["before"]) != 1 {
		return 0, errInvalid
	}
	return positiveNumber(values.Get("before"))
}

// positiveNumber 解析服务端生成的规范正整数。
// 参数 value 为游标中传回的序列或版本号。
// 返回值 正整数或校验错误。
func positiveNumber(value string) (int64, error) {
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number < 1 || strconv.FormatInt(number, 10) != value {
		return 0, errInvalid
	}
	return number, nil
}
