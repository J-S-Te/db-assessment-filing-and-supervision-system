package audittrail

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var errFilter = errors.New("invalid audit search filters")

type auditItem struct {
	ID          string    `json:"id"`
	Resource    string    `json:"resource"`
	ResourceID  string    `json:"resourceId"`
	Title       string    `json:"title"`
	Reference   string    `json:"reference,omitempty"`
	Action      string    `json:"action"`
	ActionLabel string    `json:"actionLabel"`
	Operator    string    `json:"operator"`
	OccurredAt  time.Time `json:"occurredAt"`
	Href        string    `json:"href"`
}

type auditPage struct {
	Items      []auditItem `json:"items"`
	NextCursor string      `json:"nextCursor,omitempty"`
}

type auditFilter struct {
	resource, query string
	limit           int
	cursor          auditCursor
}

type auditCursor struct {
	at  time.Time
	key string
}

var basicActions = map[string]string{
	"submit": "提交审核", "approve": "审核通过", "return_for_correction": "退回补正",
	"create": "新建草稿", "edit": "编辑内容", "accept": "受理报备", "withdraw": "撤回",
	"archive": "归档报备", "process": "开始处理", "close": "关闭", "publish": "发布信息",
	"confirm_extraction": "确认报告校对", "record_compliance": "登记合规判断",
}

var approvalStages = map[string]string{
	"pending_initial": "初审通过", "pending_final": "终审通过", "pending_supervision": "监管复核通过",
}
var resourceActions = map[string]map[string]string{
	"filing-applications": {"withdraw": "撤回申请"},
	"implementations":     {"submit": "提交报备", "withdraw": "撤销报备"},
	"hazards": {
		"register": "登记隐患", "dispatch": "派发整改", "supervise": "督办隐患",
		"submit": "提交复核", "close": "复核销项",
	},
	"alerts":           {"create": "生成预警", "close": "关闭预警"},
	"publications":     {"withdraw": "撤回发布"},
	"workday-calendar": {"workday": "设置工作日", "restday": "设置休息日", "default": "恢复默认日历"},
}

// parseFilter 校验审计业务类型、关键词及可选分页条件。
// 参数 raw 为原始 URL 查询字符串，paged 表示是否允许列表分页字段。
// 返回值 有界查询条件或者无效输入错误。
func parseFilter(raw string, paged bool) (auditFilter, error) {
	values, err := url.ParseQuery(raw)
	if err != nil || !paged && (values.Has("cursor") || values.Has("limit")) {
		return auditFilter{}, errFilter
	}
	for key, entries := range values {
		if len(entries) != 1 || (key != "resource" && key != "q" && key != "cursor" && key != "limit") {
			return auditFilter{}, errFilter
		}
	}
	filter := auditFilter{
		resource: values.Get("resource"), query: strings.TrimSpace(values.Get("q")), limit: 20,
	}
	if !knownResource(filter.resource) || !utf8.ValidString(filter.query) ||
		utf8.RuneCountInString(filter.query) > 120 || strings.ContainsFunc(filter.query, unicode.IsControl) {
		return auditFilter{}, errFilter
	}
	if supplied, ok := values["limit"]; ok {
		filter.limit, err = strconv.Atoi(supplied[0])
		if err != nil || filter.limit < 1 || filter.limit > 100 {
			return auditFilter{}, errFilter
		}
	}
	filter.cursor, err = decodeCursor(values.Get("cursor"))
	return filter, err
}

// knownResource 确认请求筛选所用的业务类型属于公开留痕集合。
// 参数 resource 为资源类型代码，允许空值表示所有业务类型。
// 返回值 已支持该筛选时返回真。
func knownResource(resource string) bool {
	switch resource {
	case "", "filings", "filing-applications", "implementations", "reports", "remediations",
		"hazards", "alerts", "publications", "workday-calendar":
		return true
	default:
		return false
	}
}

// decodeCursor 读取由发生时间与唯一事件键组成的审计分页位置。
// 参数 raw 为上一页响应的游标，空值表示最新页。
// 返回值 已验证的时间和键，或游标无效错误。
func decodeCursor(raw string) (auditCursor, error) {
	if raw == "" {
		return auditCursor{}, nil
	}
	if len(raw) > 192 {
		return auditCursor{}, errFilter
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return auditCursor{}, errFilter
	}
	stamp, key, found := strings.Cut(string(data), "|")
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if !found || err != nil || at.IsZero() || !validEventKey(key) {
		return auditCursor{}, errFilter
	}
	return auditCursor{at: at, key: key}, nil
}

// validEventKey 校验审计来源标记与数据库事件键的字符边界。
// 参数 key 为游标中承载的事件键。
// 返回值 符合公开排序键格式范围时为真。
func validEventKey(key string) bool {
	if len(key) == 0 || len(key) > 100 || !strings.Contains(key, ":") {
		return false
	}
	for _, char := range key {
		if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == ':' || char == '-') {
			return false
		}
	}
	return true
}

// encodeCursor 为已显示末尾记录生成稳定的下一页游标。
// 参数 item 为按时间与事件键排序后的末条记录。
// 返回值 可直接用于 URL 的游标。
func encodeCursor(item auditItem) string {
	value := item.OccurredAt.UTC().Format(time.RFC3339Nano) + "|" + item.ID
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

// actionLabel 使用事件实际的前置阶段和业务类型恢复操作显示名称。
// 参数 resource、action、stage 为事件的模块、原始动作和前置阶段。
// 返回值 监管页面展示的业务操作名称。
func actionLabel(resource, action, stage string) string {
	if label := approvalStages[stage]; action == "approve" && label != "" {
		return label
	}
	if label := resourceActions[resource][action]; label != "" {
		return label
	}
	if label := basicActions[action]; label != "" {
		return label
	}
	return action
}

// businessLink 生成指向原业务对象的站内详情地址。
// 参数 resource 为已投影类型，id 和 reference 为公开标识及日期等定位字段。
// 返回值 可由监管界面打开的相对路由。
func businessLink(resource, id, reference string) string {
	switch resource {
	case "alerts":
		return "/alerts?alert=" + url.QueryEscape(id)
	case "publications":
		return "/monitor/publications?publication=" + url.QueryEscape(id)
	case "workday-calendar":
		return "/workday-calendar?day=" + url.QueryEscape(reference)
	default:
		return "/" + resource + "/" + url.PathEscape(id)
	}
}
