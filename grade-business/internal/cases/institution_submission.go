package cases

import (
	"net/http"
	"net/url"
	"strings"

	"grade-business/internal/classification"
	"grade-business/internal/httpx"
)

type institutionFilingInput struct {
	Number           string                `json:"number"`
	SystemName       string                `json:"systemName"`
	OrganizationName string                `json:"organizationName"`
	Level            string                `json:"level"`
	Classification   *classification.Input `json:"classificationAssessment,omitempty"`
}

type institutionReportInput struct {
	Number             string  `json:"number"`
	FilingID           string  `json:"filingId"`
	Risk               string  `json:"risk"`
	AssessmentPersonID *string `json:"assessmentPersonId,omitempty"`
}

// registerInstitutionCollections 挂载机构可查询和创建草稿的业务列表。
// 参数 mux 为本地业务路由。
// 返回值 无。
func (s *Service) registerInstitutionCollections(mux *http.ServeMux) {
	for _, resource := range []struct{ path, kind string }{{"filings", filing}, {"reports", report}} {
		h := endpoint{service: s, kind: resource.kind}
		path := "/api/v1/institution/" + resource.path
		mux.HandleFunc("GET "+path, h.institutionList)
		mux.HandleFunc("POST "+path, withInstitution(h.institutionCreate))
	}
	const applications = "/api/v1/institution/filing-applications"
	mux.HandleFunc("GET "+applications, s.institutionApplicationList)
	mux.HandleFunc("POST "+applications, withInstitution(s.createScopedApplication))
}

// institutionList 返回所选机构的备案或测评报告列表。
// 参数 w 为响应，r 带当前机构、业务筛选与分页字段。
// 返回值 无。
func (h endpoint) institutionList(w http.ResponseWriter, r *http.Request) {
	h.listRecords(w, r, true)
}

// listRequestValues 分离机构范围与既有领域筛选。
// 参数 raw 为查询字符串，scoped 表示是否必须提供机构范围。
// 返回值 可交由原校验器处理的筛选、已验证机构名或输入错误。
func listRequestValues(raw string, scoped bool) (url.Values, string, error) {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return nil, "", ErrInvalid
	}
	if !scoped {
		return query, "", nil
	}
	institution, err := consumeInstitution(query)
	return query, institution, err
}

// consumeInstitution 取出必填的单一机构范围，避免将其当作业务筛选项。
// 参数 query 为请求解码后的查询字段。
// 返回值 规范化机构名或输入错误。
func consumeInstitution(query url.Values) (string, error) {
	entries := query["institution"]
	if len(entries) != 1 {
		return "", ErrInvalid
	}
	institution := strings.TrimSpace(entries[0])
	if !validText(institution, 200) {
		return "", ErrInvalid
	}
	query.Del("institution")
	return institution, nil
}

// institutionCreate 用途为创建本机构的备案或测评草稿，备案可选填预填编号。
// 参数 w 和 r 为响应及草稿请求，institution 为当前机构。
// 返回值 无，成功返回持久化的草稿标识及版本。
func (h endpoint) institutionCreate(w http.ResponseWriter, r *http.Request, institution string) {
	var fields saveFields
	var err error
	if h.kind == filing {
		var input institutionFilingInput
		if httpx.Decode(w, r, &input) != nil {
			respondError(w, ErrInvalid)
			return
		}
		fields, err = parseFilingFields(filingInput{
			Number: input.Number, SystemName: input.SystemName, OrganizationName: input.OrganizationName,
			AssessmentOrg: institution, Level: input.Level, Classification: input.Classification,
		}, false)
	} else {
		var input institutionReportInput
		if httpx.Decode(w, r, &input) != nil {
			respondError(w, ErrInvalid)
			return
		}
		fields, err = input.withInstitution(institution)
	}
	if err != nil {
		respondError(w, err)
		return
	}
	item, err := h.service.create(r.Context(), h.kind, fields, institution)
	respond(w, http.StatusCreated, item, err)
}

// withInstitution 将 URL 中经过校验的机构绑定到一般测评录入字段。
// 参数 institution 为当前选定的机构范围。
// 返回值 已清理的测评字段或校验错误。
func (input institutionReportInput) withInstitution(institution string) (saveFields, error) {
	return reportFields(reportInput{
		Number: input.Number, FilingID: input.FilingID, AssessmentOrg: institution, Risk: input.Risk,
		AssessmentPersonID: input.AssessmentPersonID,
	})
}

// updateInstitutionReport 保存机构草稿或补正报告的业务字段。
// 参数 w 和 r 为响应及带版本的更新请求，institution 为当前机构。
// 返回值 无，成功返回实际版本。
func (h endpoint) updateInstitutionReport(w http.ResponseWriter, r *http.Request, institution string) {
	var input struct {
		institutionReportInput
		versionInput
	}
	if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
		respondError(w, ErrInvalid)
		return
	}
	fields, err := input.withInstitution(institution)
	if err != nil {
		respondError(w, err)
		return
	}
	item, err := h.service.update(r.Context(), report, r.PathValue("id"), input.ExpectedVersion, fields, institution)
	respond(w, http.StatusOK, item, err)
}

// institutionApplicationList 返回原发起机构的备案申请，不以当前备案的新归属覆盖历史。
// 参数 w 和 r 为响应及含机构名称的列表请求。
// 返回值 无。
func (s *Service) institutionApplicationList(w http.ResponseWriter, r *http.Request) {
	s.listApplicationsRequest(w, r, true)
}

// createScopedApplication 为当前机构的有效备案发起变更或注销申请。
// 参数 w 和 r 为响应及草稿请求，institution 为发起机构。
// 返回值 无，返回同一业务内核生成的真实申请。
func (s *Service) createScopedApplication(w http.ResponseWriter, r *http.Request, institution string) {
	s.createApplicationRequest(w, r, institution)
}

// withdrawScopedApplication 由原机构撤回仍符合原申请规则的草稿或补正申请。
// 参数 w 和 r 为响应及撤回请求，institution 为原发起机构。
// 返回值 无，返回撤回结果或业务冲突。
func (s *Service) withdrawScopedApplication(w http.ResponseWriter, r *http.Request, institution string) {
	var input struct {
		versionInput
		Comment string `json:"comment"`
	}
	if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
		respondError(w, ErrInvalid)
		return
	}
	item, err := s.changeApplicationStatus(r.Context(), r.PathValue("id"), reviewInput{
		versionInput: input.versionInput, Decision: withdrawApplication, Comment: input.Comment, institution: institution,
	})
	respond(w, http.StatusOK, item, err)
}
