package hazards

import (
	"context"
	"net/http"
)

const institutionBase = "/api/v1/institution/hazards"

type institutionContextKey struct{}

// RegisterInstitution 注册测评机构的隐患列表、办理历史、材料及复测决定接口。
// 参数 mux 为统一业务路由。
// 返回值 无。
func (s *Service) RegisterInstitution(mux *http.ServeMux) {
	const path = institutionBase + "/{id}"
	mux.HandleFunc("GET "+institutionBase, s.handleInstitutionList)
	mux.HandleFunc("GET "+path, withHazardScope(s.handleDetail, "institution"))
	mux.HandleFunc("GET "+path+"/history", withHazardScope(s.handleHistory, "institution"))
	mux.HandleFunc("GET "+path+"/attachments", withHazardScope(s.handleAttachments, "institution"))
	mux.HandleFunc("GET "+path+"/attachments/{attachmentId}", withHazardScope(s.handleDownload, "institution"))
	mux.HandleFunc("POST "+path+"/attachments", withHazardScope(s.handleScopedUpload, "institution"))
	mux.HandleFunc("POST "+path+"/actions", withHazardScope(s.handleScopedAction, "institution"))
}

// handleInstitutionList 查询与本机构通过测评报告关联的隐患和复测状态。
// 参数 w、r 为响应对象和带唯一机构范围的筛选请求。
// 返回值 无。
func (s *Service) handleInstitutionList(w http.ResponseWriter, r *http.Request) {
	s.handleScopedList(w, r, "institution")
}

// hazardInstitution 提取已由机构端路由核验的测评机构全称。
// 参数 ctx 为请求或业务事务上下文。
// 返回值 当前机构全称；其他办理端返回空字符串。
func hazardInstitution(ctx context.Context) string {
	name, _ := ctx.Value(institutionContextKey{}).(string)
	return name
}
