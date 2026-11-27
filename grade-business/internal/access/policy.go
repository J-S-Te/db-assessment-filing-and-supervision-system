package access

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

type Role string

const (
	SystemAdmin Role = "ROLE_SYS_ADMIN"
	Regulator   Role = "ROLE_REGULATOR"
	OrgAdmin    Role = "ROLE_ORG_ADMIN"
	Operator    Role = "ROLE_OPERATOR"
)

type Principal struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Role       Role   `json:"roleCode"`
	AreaCode   string `json:"areaCode,omitempty"`
	SubjectID  string `json:"subjectId,omitempty"`
	Subject    string `json:"subjectName,omitempty"`
	MustChange bool   `json:"mustChangePassword"`
}

type principalKey struct{}

// Current 从已验证的会话上下文读取账号身份。
// 参数 ctx 为业务请求上下文。
// 返回值 可信身份以及会话是否已由权限网关验证。
func Current(ctx context.Context) (Principal, bool) {
	value, ok := ctx.Value(principalKey{}).(Principal)
	return value, ok
}

const aiReviewContextPattern = "GET /api/v1/ai-review/context"

// isAIReviewPattern 仅匹配固定的审核入口与只读会话接口。
func isAIReviewPattern(pattern string) bool {
	return pattern == aiReviewContextPattern || pattern == "GET /review/{$}" || pattern == "GET /review/{asset...}" || pattern == "GET /review"
}

const directoryOptions = "/api/v1/directory/options"

// allowed 按实际匹配的服务端路由与方法判断角色授权。
// 参数 actor 为当前会话身份，pattern 为 ServeMux 解析的路由模式。
// 返回值 当前角色能进入该业务路由时为真。
func allowed(actor Principal, pattern string) bool {
	role := actor.Role
	if isAIReviewPattern(pattern) {
		return role == SystemAdmin || role == Regulator
	}
	if pattern == institutionAccountPattern {
		return role == SystemAdmin
	}
	if pattern == registrationListPattern || pattern == registrationReviewPattern {
		return role == SystemAdmin || role == Regulator
	}
	method, path, ok := strings.Cut(pattern, " ")
	if !ok || !strings.HasPrefix(path, "/api/v1/") {
		return false
	}
	if path == directoryOptions {
		return method == "GET" && (role == SystemAdmin || role == OrgAdmin || role == Operator)
	}
	switch {
	case strings.HasPrefix(path, "/api/v1/institution/"):
		if strings.HasSuffix(path, "/review-actions") {
			return role == OrgAdmin && (path == "/api/v1/institution/filings/{id}/review-actions" ||
				path == "/api/v1/institution/filing-applications/{id}/review-actions")
		}
		return role == OrgAdmin
	case strings.HasPrefix(path, "/api/v1/operator/"):
		return role == Operator
	case role == SystemAdmin:
		return true
	case role == Regulator:
		if pattern == "GET /api/v1/dashboard/district-response" ||
			pattern == "GET /api/v1/dashboard/risk-by-district" {
			return slices.Contains(cityDistricts[quzhouCityCode], actor.AreaCode)
		}
		return regulatorAllowed(method, path)
	default:
		return false
	}
}

// regulatorAllowed 只开放已经接通辖区筛选的业务接口和共享知识条目。
// 参数 method、path 为实际匹配的接口方法和路径。
// 返回值 确认具备辖区校验或属于共享知识数据时为真。
func regulatorAllowed(method, path string) bool {
	if method == "PUT" {
		return path == "/api/v1/filings/{id}/prechecks/review/{ruleId}"
	}
	if method == "POST" {
		return path == "/api/v1/filings/{id}/review-actions" ||
			path == "/api/v1/reports/{id}/review-actions" || path == "/api/v1/filings/{id}/prechecks"
	}
	if method != "GET" {
		return false
	}
	switch path {
	case "/api/v1/filings", "/api/v1/filings/{id}", "/api/v1/filings/{id}/review-history",
		"/api/v1/filings/{id}/prechecks/{jobId}", "/api/v1/filings/{id}/prechecks/review",
		"/api/v1/reports", "/api/v1/reports/{id}", "/api/v1/reports/{id}/review-history",
		"/api/v1/filings/{id}/attachments", "/api/v1/filings/{id}/attachments/{attachmentId}",
		"/api/v1/filings/{id}/attachments/{attachmentId}/text",
		"/api/v1/reports/{id}/attachments", "/api/v1/reports/{id}/attachments/{attachmentId}",
		"/api/v1/knowledge", "/api/v1/knowledge/{id}",
		"/api/v1/knowledge/{id}/versions", "/api/v1/knowledge/{id}/versions/{version}":
		return true
	}
	return false
}

// bindSubject 将已认证的主体名称绑定到迁移前已实现数据隔离的接口。
// 参数 r 为待处理的请求，actor 为当前账号，pattern 为匹配接口。
// 返回值 请求范围与可信主体一致或已从会话自动补齐时为真。
func bindSubject(r *http.Request, actor Principal, pattern string) bool {
	if actor.Role != OrgAdmin && actor.Role != Operator {
		return true
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || actor.Subject == "" {
		return false
	}
	key, other := "institution", "organization"
	if actor.Role == Operator {
		key, other = other, key
	}
	if query.Has(other) || query.Has(key) &&
		(len(query[key]) != 1 || query.Get(key) != actor.Subject) {
		return false
	}
	if pattern == "GET "+directoryOptions && actor.Role == Operator {
		return !query.Has(key) && len(query["kind"]) == 1 && query.Get("kind") == "institution"
	}
	if actor.Role == OrgAdmin && query.Get("review") == "assigned" {
		return pattern == "GET /api/v1/institution/filings" ||
			pattern == "GET /api/v1/institution/filing-applications"
	}
	query.Set(key, actor.Subject)
	r.URL.RawQuery = query.Encode()
	return true
}

type sessionView struct {
	Principal
	Portal      string         `json:"portal"`
	DataScope   string         `json:"dataScope"`
	Home        string         `json:"home"`
	Pages       []string       `json:"pages"`
	Menu        []string       `json:"menu"`
	Permissions []string       `json:"permissions"`
	Dashboard   *dashboardArea `json:"dashboardArea,omitempty"`
}

// view 提供当前角色可用的数据范围、页面、菜单和操作权限。
// 参数 actor 为已验证的账号信息。
// 返回值 三端路由守卫与导航可直接使用的会话权限视图。
func view(actor Principal) sessionView {
	data := sessionView{Principal: actor, Portal: "admin", DataScope: "ALL", Home: "/",
		Permissions: []string{}}
	switch actor.Role {
	case SystemAdmin:
		data.Menu = []string{"home", "grade", "archive", "monitor", "agent", "screen", "system", "ai-review"}
		data.Pages = []string{"/", "/flow", "/filings", "/filing-applications", "/filing-material-policy",
			"/implementations", "/reports", "/remediations", "/hazards", "/archives", "/alerts",
			"/supervisions", "/material-expiries", "/assessment-dues", "/monitor/publications",
			"/knowledge", "/dashboard", "/audit", "/workday-calendar", "/tag-catalog", "/institution-accounts", "/operator-registrations"}
		data.Permissions = []string{"admin.agent.kb.edit", "admin.monitor.rule.config",
			"admin.grade.case.edit", "admin.grade.filing.audit", "admin.grade.report.audit", "admin.sys.user.edit", registrationPermission}
	case Regulator:
		data.DataScope, data.Home = "AREA", "/filings"
		data.Menu = []string{"grade", "agent", "ai-review", "system"}
		data.Pages = []string{"/filings", "/reports", "/knowledge", "/flow", "/operator-registrations"}
		if data.Dashboard = quzhouDashboard(actor.AreaCode); data.Dashboard != nil {
			data.Menu = append(data.Menu, "screen")
			data.Pages = append(data.Pages, "/dashboard")
		}
		data.Permissions = []string{"admin.grade.filing.audit", "admin.grade.report.audit", registrationPermission}
	case OrgAdmin:
		return scopedView(data, "institution", "ORG", []string{"workbench", "filings", "implementations",
			"reports", "hazards", "directory", "publications", "alerts", "feedback"},
			[]string{"filing-applications", "material-expiries", "assessment-dues"})
	case Operator:
		return scopedView(data, "operator", "UNIT", []string{"workbench", "filings",
			"hazards", "remediations", "assessment-history", "supervisions", "feedback", "alerts",
			"publications"}, []string{"filing-applications", "material-expiries", "assessment-dues"})
	}
	if actor.Role == SystemAdmin || actor.Role == Regulator {
		data.Pages = append(data.Pages, "/review")
		data.Permissions = append(data.Permissions, "admin.ai-review.view")
	}
	return data
}

// scopedView 生成主体端的首页及有权进入的办事页面。
// 参数 data 为会话视图，portal、scope 为办理端及范围，menu、extra 为导航和关联页。
// 返回值 当前主体端的完整导航权限。
func scopedView(data sessionView, portal, scope string, menu, extra []string) sessionView {
	data.Portal, data.DataScope, data.Home = portal, scope, "/"+portal+"/workbench"
	data.Menu = menu
	for _, key := range append(append([]string{}, menu...), extra...) {
		data.Pages = append(data.Pages, "/"+portal+"/"+key)
	}
	return data
}
