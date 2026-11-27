package cases

import (
	"net/http"
	"net/url"
	"strconv"

	"grade-business/internal/httpx"
)

// registerReportFeedback 挂载监管回执和机构收件及补正接口。
// 参数 mux 为回环业务服务的 HTTP 路由。
// 返回值 无。
func (s *Service) registerReportFeedback(mux *http.ServeMux) {
	const inbox = "/api/v1/institution/report-feedback"
	const records = "/api/v1/institution/reports/{id}"
	h := endpoint{service: s, kind: report}
	mux.HandleFunc("GET /api/v1/reports/{id}/feedback", s.feedbackDeliveriesHTTP)
	mux.HandleFunc("GET "+inbox, s.feedbackInboxHTTP)
	mux.HandleFunc("POST "+inbox+"/{messageId}/acknowledge", withInstitution(s.acknowledgeFeedbackHTTP))
	mux.HandleFunc("GET "+records, withInstitution(s.institutionReportHTTP))
	mux.HandleFunc("PATCH "+records, withInstitution(h.updateInstitutionReport))
	mux.HandleFunc("GET "+records+"/attachments", withInstitution(h.institutionMaterials))
	mux.HandleFunc("POST "+records+"/attachments", withInstitution(h.uploadToScope))
	mux.HandleFunc("GET "+records+"/attachments/{attachmentId}", withInstitution(h.downloadFromScope))
	mux.HandleFunc("POST "+records+"/submit", withInstitution(s.institutionSubmitHTTP))
}

// withInstitution 为机构操作核对当前所选机构和请求字段。
// 参数 handle 接收已校验的本地机构范围。
// 返回值 校验范围后执行的 HTTP 处理器。
func withInstitution(handle func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		institution, _, err := parseInstitutionQuery(r.URL.RawQuery, false)
		if err != nil {
			respondError(w, err)
			return
		}
		handle(w, r, institution)
	}
}

// parseInstitutionQuery 解析本地选定的机构和可选页游标。
// 参数 raw 为原始 URL 查询，allowCursor 表示当前接口接受分页。
// 返回值 机构名称、游标或无效输入错误。
func parseInstitutionQuery(raw string, allowCursor bool) (string, int64, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "", 0, ErrInvalid
	}
	for key, entries := range values {
		if len(entries) != 1 || (key != "institution" && !(allowCursor && key == "before")) {
			return "", 0, ErrInvalid
		}
	}
	institution, err := consumeInstitution(values)
	if err != nil {
		return "", 0, err
	}
	if cursor := values.Get("before"); cursor != "" {
		before, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || before <= 0 {
			return "", 0, ErrInvalid
		}
		return institution, before, nil
	}
	return institution, 0, nil
}

// feedbackInboxHTTP 返回本地选定机构的测评审核反馈。
// 参数 w 为响应对象，r 带机构名称和可选页游标。
// 返回值 无，结果为最近的消息及下一页游标。
func (s *Service) feedbackInboxHTTP(w http.ResponseWriter, r *http.Request) {
	institution, before, err := parseInstitutionQuery(r.URL.RawQuery, true)
	if err != nil {
		respondError(w, err)
		return
	}
	result, err := s.readFeedbackInbox(r.Context(), institution, before)
	respond(w, http.StatusOK, result, err)
}

// acknowledgeFeedbackHTTP 签收发送给当前机构的一条测评反馈。
// 参数 w 为响应对象，r 通过路径指定消息，institution 为所选机构。
// 返回值 无，返回首次签收时间。
func (s *Service) acknowledgeFeedbackHTTP(w http.ResponseWriter, r *http.Request, institution string) {
	if r.ContentLength != 0 {
		respondError(w, ErrInvalid)
		return
	}
	result, err := s.acknowledgeFeedback(r.Context(), institution, r.PathValue("messageId"))
	respond(w, http.StatusOK, result, err)
}

// feedbackDeliveriesHTTP 为监管端提供本报告的回传和签收时间。
// 参数 w 为响应对象，r 通过路径指定报告。
// 返回值 无，返回该报告最近的回传摘要。
func (s *Service) feedbackDeliveriesHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respondError(w, ErrInvalid)
		return
	}
	items, err := s.readFeedbackDeliveries(r.Context(), r.PathValue("id"))
	respond(w, http.StatusOK, itemResponse[feedbackDelivery]{Items: items}, err)
}

// institutionReportHTTP 查询当前机构可见的测评报告及补正材料。
// 参数 w 为响应对象，r 通过路径指定报告，institution 为所选机构。
// 返回值 无，返回最新报告状态和材料。
func (s *Service) institutionReportHTTP(w http.ResponseWriter, r *http.Request, institution string) {
	result, err := s.readInstitutionReport(r.Context(), institution, r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}

// institutionSubmitHTTP 使用既有状态机提交本机构草稿或已补正的报告。
// 参数 w 为响应对象，r 携带报告及预期版本，institution 为所选机构。
// 返回值 无，返回实际登记的新状态和版本。
func (s *Service) institutionSubmitHTTP(w http.ResponseWriter, r *http.Request, institution string) {
	var input versionInput
	if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
		respondError(w, ErrInvalid)
		return
	}
	result, err := s.changeStatus(r.Context(), report, r.PathValue("id"),
		reviewInput{versionInput: input, Decision: submit, institution: institution})
	respond(w, http.StatusOK, result, err)
}
