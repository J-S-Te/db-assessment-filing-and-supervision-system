package alerts

import (
	"context"
	"net/http"
	"net/url"

	"grade-business/internal/reviewinbox"
)

const (
	audienceOperator    = "operator"
	audienceInstitution = "institution"
)

type alertScopeKey struct{}

type alertScope struct {
	audience, recipient string
}

// RegisterOperator 注册运营者预警查看和处置接口。
// 参数 mux 为应用业务路由。
// 返回值 无。
func (s *Service) RegisterOperator(mux *http.ServeMux) {
	s.registerScoped(mux, audienceOperator, "organization")
	mux.HandleFunc("GET /api/v1/operator/alerts/export",
		scopedAlertEndpoint(s.handleOperatorExport, audienceOperator, "organization", true))
}

// RegisterInstitution 注册关联测评机构的预警查看和反馈接口。
// 参数 mux 为应用业务路由。
// 返回值 无。
func (s *Service) RegisterInstitution(mux *http.ServeMux) {
	s.registerScoped(mux, audienceInstitution, "institution")
}

// registerScoped 为机构或运营者注册相应范围的预警路由。
// 参数 mux 为应用业务路由，audience 为办理端，key 为对应的范围参数。
// 返回值 无。
func (s *Service) registerScoped(mux *http.ServeMux, audience, key string) {
	const base = "/api/v1/"
	path := base + audience + "/alerts"
	mux.HandleFunc("GET "+path, scopedAlertEndpoint(s.handleList, audience, key, true))
	mux.HandleFunc("GET "+path+"/{id}", scopedAlertEndpoint(s.handleDetail, audience, key, false))
	mux.HandleFunc("POST "+path+"/{id}/actions", scopedAlertEndpoint(s.handleAction, audience, key, false))
}

// scopedAlertEndpoint 核对本地机构或运营单位范围，剥离归属字段后复用预警端点。
// 参数 handle 为共用处理器，audience 和 key 指定办理端及范围参数，list 表示允许列表筛选。
// 返回值 带单位范围的 HTTP 处理函数。
func scopedAlertEndpoint(handle http.HandlerFunc, audience, key string, list bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(values[key]) != 1 || values.Has("review") || (!list && len(values) != 1) {
			respond(w, nil, errInvalid)
			return
		}
		checked := r.Clone(r.Context())
		checked.URL.RawQuery = url.Values{key: values[key]}.Encode()
		recipient, err := reviewinbox.Scope(checked, key)
		if err != nil {
			respond(w, nil, errInvalid)
			return
		}
		values.Del(key)
		checked.URL.RawQuery = values.Encode()
		scope := alertScope{audience: audience, recipient: recipient}
		handle(w, checked.WithContext(context.WithValue(checked.Context(), alertScopeKey{}, scope)))
	}
}

// currentAlertScope 取得已由业务路由校验的办理端和范围。
// 参数 ctx 为当前请求上下文。
// 返回值 当前办理范围，监管路由返回空值。
func currentAlertScope(ctx context.Context) alertScope {
	scope, _ := ctx.Value(alertScopeKey{}).(alertScope)
	return scope
}
