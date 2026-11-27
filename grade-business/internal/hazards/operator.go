package hazards

import (
	"context"
	"net/http"
	"net/url"

	"grade-business/internal/reviewinbox"
)

const operatorBase = "/api/v1/operator/hazards"
const operatorRoute = operatorBase + "/{id}"

type organizationContextKey struct{}

// RegisterOperator 注册企业端的隐患列表、详情、回执、整改办理及证据接口。
// 参数 mux 为统一业务路由。
// 返回值 无。
func (s *Service) RegisterOperator(mux *http.ServeMux) {
	mux.HandleFunc("GET "+operatorBase, s.handleOperatorList)
	mux.HandleFunc("GET "+operatorRoute, withHazardScope(s.handleDetail, "organization"))
	mux.HandleFunc("GET "+operatorRoute+"/history", withHazardScope(s.handleHistory, "organization"))
	mux.HandleFunc("GET "+operatorRoute+"/receipt", withHazardScope(s.handleOperatorReceipt, "organization"))
	mux.HandleFunc("GET "+operatorRoute+"/attachments", withHazardScope(s.handleAttachments, "organization"))
	mux.HandleFunc("GET "+operatorRoute+"/attachments/{attachmentId}", withHazardScope(s.handleDownload, "organization"))
	mux.HandleFunc("POST "+operatorRoute+"/actions", withHazardScope(s.handleScopedAction, "organization"))
	mux.HandleFunc("POST "+operatorRoute+"/attachments", withHazardScope(s.handleScopedUpload, "organization"))
}

// handleOperatorList 查询当前备案单位的整改单并复用台账分页筛选。
// 参数 w 为响应对象，r 携带运营单位、精确备案及可选台账筛选。
// 返回值 无。
func (s *Service) handleOperatorList(w http.ResponseWriter, r *http.Request) {
	s.handleScopedList(w, r, "organization")
}

// handleScopedList 使用与办理端相同的业务范围读取台账，并保留精确备案筛选。
// 参数 w、r 为列表响应与请求，key 为范围查询字段。
// 返回值 无。
func (s *Service) handleScopedList(w http.ResponseWriter, r *http.Request, key string) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		respondError(w, errInvalid)
		return
	}
	scoped := r.Clone(r.Context())
	scoped.URL.RawQuery = url.Values{key: query[key]}.Encode()
	name, err := reviewinbox.Scope(scoped, key)
	if err != nil || len(query["filingId"]) > 1 || (query.Has("filingId") && !validID(query.Get("filingId"))) {
		respondError(w, errInvalid)
		return
	}
	filingID := query.Get("filingId")
	query.Del(key)
	query.Del("filingId")
	filter, err := decodeFilter(query)
	if err != nil {
		respondError(w, err)
		return
	}
	filter.filingID = filingID
	ctx := withHazardContext(r.Context(), key, name)
	result, err := s.list(ctx, filter)
	respond(w, http.StatusOK, result, err)
}

// withHazardScope 校验隐患标识以及指定办理端的唯一范围。
// 参数 next 为按当前机构或运营单位办理隐患的处理器，key 为范围查询字段。
// 返回值 要求有效标识及唯一范围的 HTTP 处理器。
func withHazardScope(next http.HandlerFunc, key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validID(r.PathValue("id")) {
			respondError(w, errNotFound)
			return
		}
		name, err := reviewinbox.Scope(r, key)
		if err != nil {
			respondError(w, errInvalid)
			return
		}
		ctx := withHazardContext(r.Context(), key, name)
		next(w, r.WithContext(ctx))
	}
}

// withHazardContext 保存已经通过校验的运营单位或测评机构范围。
// 参数 ctx 为请求上下文，key 指定办理端，name 为该端精确名称。
// 返回值 由后续服务读写使用的范围上下文。
func withHazardContext(ctx context.Context, key, name string) context.Context {
	if key == "institution" {
		return context.WithValue(ctx, institutionContextKey{}, name)
	}
	return context.WithValue(ctx, organizationContextKey{}, name)
}

// operatorOrganization 读取企业端路由已校验的查询范围。
// 参数 ctx 为当前请求及事务上下文。
// 返回值 企业单位全称，监管端请求返回空字符串。
func operatorOrganization(ctx context.Context) string {
	organization, _ := ctx.Value(organizationContextKey{}).(string)
	return organization
}

// handleScopedAction 接收运营者一次整改提交或机构复测决定。
// 参数 w 为响应对象，r 为范围和标识已校验的请求。
// 返回值 无。
func (s *Service) handleScopedAction(w http.ResponseWriter, r *http.Request) {
	s.handleAction(w, r, r.PathValue("id"))
}

// handleScopedUpload 使用已校验的范围接收运营者证据或机构复测报告。
// 参数 w 为响应对象，r 为范围和标识已校验的上传请求。
// 返回值 无。
func (s *Service) handleScopedUpload(w http.ResponseWriter, r *http.Request) {
	s.handleUpload(w, r, r.PathValue("id"))
}
