package cases

import (
	"errors"
	"net/http"

	"grade-business/internal/httpx"
)

const filingApplicationPath = "/api/v1/filing-applications"

// registerApplications 挂载备案变更和注销申请的查询及办理接口。
// 参数 mux 为管理端 HTTP 路由器。
// 返回值 无。
func (s *Service) registerApplications(mux *http.ServeMux) {
	path := filingApplicationPath
	mux.HandleFunc("GET "+path, s.listApplicationsHTTP)
	mux.HandleFunc("POST "+path, s.createApplicationHTTP)
	mux.HandleFunc("GET "+path+"/{id}", s.applicationHTTP)
	mux.HandleFunc("PATCH "+path+"/{id}", s.updateApplicationHTTP)
	mux.HandleFunc("POST "+path+"/{id}/submit", s.submitApplicationHTTP)
	mux.HandleFunc("POST "+path+"/{id}/review-actions", s.reviewApplicationHTTP)
	mux.HandleFunc("GET "+path+"/{id}/review-history", s.applicationHistoryHTTP)
	mux.HandleFunc("GET "+path+"/{id}/attachments", s.adminApplicationMaterials)
	mux.HandleFunc("GET "+path+"/{id}/attachments/{attachmentId}", s.adminApplicationDownload)
	const scoped = "/api/v1/institution/filing-applications/{id}/attachments"
	mux.HandleFunc("GET "+scoped, withInstitution(s.applicationMaterialsHTTP))
	mux.HandleFunc("GET "+scoped+"/{attachmentId}", withInstitution(s.applicationDownloadHTTP))
}

// listApplicationsHTTP 解析备案申请的查询条件并返回列表页。
// 参数 w 为响应对象，r 为包含查询条件的请求。
// 返回值 无。
func (s *Service) listApplicationsHTTP(w http.ResponseWriter, r *http.Request) {
	s.listApplicationsRequest(w, r, false)
}

// listApplicationsRequest 复用申请筛选，并按需要限制持久化的原发起机构。
// 参数 w 和 r 为列表响应及请求，scoped 指定是否要求机构。
// 返回值 无。
func (s *Service) listApplicationsRequest(w http.ResponseWriter, r *http.Request, scoped bool) {
	query, institution, err := listRequestValues(r.URL.RawQuery, scoped)
	if err != nil || ((scoped || applicationOrganization(r.Context()) != "") && query.Has("risk")) {
		respondError(w, ErrInvalid)
		return
	}
	assigned, err := consumeAssignedReview(query, scoped)
	if err != nil {
		respondError(w, err)
		return
	}
	input, err := parseApplicationQuery(query)
	if err != nil {
		respondError(w, err)
		return
	}
	input.institution = institution
	input.assignedReview = assigned
	input.organization = applicationOrganization(r.Context())
	result, err := s.listApplications(r.Context(), input)
	respond(w, http.StatusOK, result, err)
}

// createApplicationHTTP 接收新申请草稿并检查所选备案和申请类型。
// 参数 w 为响应对象，r 为携带新申请的请求。
// 返回值 无。
func (s *Service) createApplicationHTTP(w http.ResponseWriter, r *http.Request) {
	s.createApplicationRequest(w, r, "")
}

// createApplicationRequest 校验申请表单及关联备案，再经持锁校验创建草稿。
// 参数 w 和 r 为响应及请求，institution 为可选发起机构范围。
// 返回值 无，返回申请标识、状态和版本。
func (s *Service) createApplicationRequest(w http.ResponseWriter, r *http.Request, institution string) {
	var input struct {
		applicationForm
		FilingID string `json:"filingId"`
		Type     string `json:"type"`
	}
	if httpx.Decode(w, r, &input) != nil || !validID(input.FilingID) ||
		(input.Type != applicationChange && input.Type != applicationCancel) {
		respondError(w, ErrInvalid)
		return
	}
	result, err := s.createApplication(r.Context(), input.FilingID, input.Type, input.applicationForm, institution)
	respond(w, http.StatusCreated, result, err)
}

// applicationHTTP 返回申请业务详情和现有状态允许的操作。
// 参数 w 为响应对象，r 为包含申请标识的请求。
// 返回值 无。
func (s *Service) applicationHTTP(w http.ResponseWriter, r *http.Request) {
	result, err := s.application(r.Context(), r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}

// updateApplicationHTTP 处理带预期版本的申请保存请求。
// 参数 w 为响应对象，r 为编辑已有申请的请求。
// 返回值 无。
func (s *Service) updateApplicationHTTP(w http.ResponseWriter, r *http.Request) {
	s.updateApplicationInScope(w, r, "")
}

// updateApplicationInScope 校验既有申请表单并委托锁定的业务更新。
// 参数 w、r 为响应与编辑请求，institution 为可选原发起机构。
// 返回值 无。
func (s *Service) updateApplicationInScope(w http.ResponseWriter, r *http.Request, institution string) {
	var input struct {
		applicationForm
		versionInput
	}
	if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
		respondError(w, ErrInvalid)
		return
	}
	result, err := s.updateApplication(r.Context(), r.PathValue("id"), input.ExpectedVersion, input.applicationForm, institution)
	respond(w, http.StatusOK, result, err)
}

// submitApplicationHTTP 将有效草稿或补正申请送入初审。
// 参数 w 为响应对象，r 为携带预期版本的提交请求。
// 返回值 无。
func (s *Service) submitApplicationHTTP(w http.ResponseWriter, r *http.Request) {
	s.submitApplicationInScope(w, r, "")
}

// submitApplicationInScope 按相同状态机提交草稿或机构补正申请。
// 参数 w、r 为响应和提交请求，institution 为可选原发起机构。
// 返回值 无。
func (s *Service) submitApplicationInScope(w http.ResponseWriter, r *http.Request, institution string) {
	var input versionInput
	if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
		respondError(w, ErrInvalid)
		return
	}
	result, err := s.changeApplicationStatus(r.Context(), r.PathValue("id"),
		reviewInput{versionInput: input, Decision: submit, institution: institution})
	respondApplication(w, result, err)
}

// reviewApplicationHTTP 接收进入审核后的通过或退回，以及草稿和补正阶段的撤回。
// 参数 w 为响应对象，r 为携带版本、决定和可选意见的请求。
// 返回值 无。
func (s *Service) reviewApplicationHTTP(w http.ResponseWriter, r *http.Request) {
	input, err := decodeReviewAction(w, r, true)
	if err != nil {
		respondError(w, err)
		return
	}
	result, err := s.changeApplicationStatus(r.Context(), r.PathValue("id"), input)
	respondApplication(w, result, err)
}

// respondApplication 保留通用申请错误映射并提示未满足的具体业务前置。
// 参数 w 为响应对象，result 为申请状态变更结果，err 为领域错误。
// 返回值 无。
func respondApplication(w http.ResponseWriter, result mutation, err error) {
	switch {
	case errors.Is(err, errApplicationRetest):
		httpx.Error(w, http.StatusConflict, "retest_required", "换机构复测须先通过同一备案及目标机构的变更实施报备。")
	case errors.Is(err, errApplicationMaterials):
		httpx.Error(w, http.StatusBadRequest, "application_materials_required", "请先上传与申请类型对应的说明材料。")
	default:
		respond(w, http.StatusOK, result, err)
	}
}

// applicationHistoryHTTP 返回实际保存的申请审核历史。
// 参数 w 为响应对象，r 为携带申请标识的请求。
// 返回值 无。
func (s *Service) applicationHistoryHTTP(w http.ResponseWriter, r *http.Request) {
	items, err := s.applicationHistory(r.Context(), r.PathValue("id"), "")
	respond(w, http.StatusOK, itemResponse[reviewEvent]{Items: items}, err)
}
