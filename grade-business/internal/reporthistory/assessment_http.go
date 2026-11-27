package reporthistory

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/httpx"
)

const operatorArchiveEndpoint = "/api/v1/operator/assessment-history"

// registerAssessmentResults 注册监管核验和运营者发布的数据入口。
// 参数 mux 为应用路由表，base 为监管报告的地址模式。
// 返回值 无。
func (s *Service) registerAssessmentResults(mux *http.ServeMux, base string) {
	const resultPath = "/assessment-result"
	mux.HandleFunc("GET "+base+resultPath, s.getResultHTTP)
	mux.HandleFunc("POST "+base+resultPath, s.postResultHTTP)
	mux.HandleFunc("GET "+base+resultPath+"/history", s.resultHistoryHTTP)
	mux.HandleFunc("GET "+operatorArchiveEndpoint+"/systems", s.operatorSystemsHTTP)
	mux.HandleFunc("GET "+operatorArchiveEndpoint+"/{id}", s.operatorHistoryHTTP)
	mux.HandleFunc("GET "+operatorArchiveEndpoint+"/{id}/comparison", s.operatorCompareHTTP)
	mux.HandleFunc("GET "+operatorArchiveEndpoint+"/{id}/trend", s.operatorTrendHTTP)
	mux.HandleFunc("GET "+operatorArchiveEndpoint+"/{id}/issue-comparison", s.operatorIssuesHTTP)
}

// getResultHTTP 读取指定监管报告的结论核验窗口。
// 参数 w 为响应，r 提供报告标识且不接收查询字段。
// 返回值 无。
func (s *Service) getResultHTTP(w http.ResponseWriter, r *http.Request) {
	id, ok := parseHistoryUUID(r.PathValue("id"))
	if !ok {
		respondHistory(w, nil, pgx.ErrNoRows)
		return
	}
	if r.URL.RawQuery != "" {
		respondHistory(w, nil, errInput)
		return
	}
	result, err := s.resultEditor(r.Context(), id)
	respondHistory(w, result, err)
}

// postResultHTTP 接收正式报告的监管结论登记或更正。
// 参数 w 为响应，r 携带报告标识及精简登记 JSON。
// 返回值 无。
func (s *Service) postResultHTTP(w http.ResponseWriter, r *http.Request) {
	id, ok := parseHistoryUUID(r.PathValue("id"))
	if !ok {
		respondHistory(w, nil, pgx.ErrNoRows)
		return
	}
	var input assessmentResultInput
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil {
		respondHistory(w, nil, errInput)
		return
	}
	result, err := s.recordAssessment(r.Context(), id, input)
	switch {
	case errors.Is(err, errResultConflict):
		httpx.Error(w, 409, "assessment_result_conflict", "报告需终审通过，或本次登记的版本已变化。请刷新后重试。")
	case errors.Is(err, errResultEvidence):
		httpx.Error(w, 400, "invalid_assessment_evidence", "请选择该报告当前有效的结论附件。")
	case errors.Is(err, errInput):
		httpx.Error(w, 400, "invalid_input", "请核对测评结论、0 至 100 分、风险项数和登记版本。")
	default:
		respondHistory(w, result, err)
	}
}

// resultHistoryHTTP 查询监管结果的有界版本留痕。
// 参数 w 为响应，r 可携带上页返回的排除版本。
// 返回值 无。
func (s *Service) resultHistoryHTTP(w http.ResponseWriter, r *http.Request) {
	id, ok := parseHistoryUUID(r.PathValue("id"))
	if !ok {
		respondHistory(w, nil, pgx.ErrNoRows)
		return
	}
	values, err := assessmentQuery(r.URL.RawQuery, "before")
	if err != nil {
		respondHistory(w, nil, err)
		return
	}
	var before int64
	if values.Has("before") {
		before, err = strconv.ParseInt(values.Get("before"), 10, 64)
		if err != nil || before < 1 {
			respondHistory(w, nil, errInput)
			return
		}
	}
	result, err := s.resultHistory(r.Context(), id, before)
	respondHistory(w, result, err)
}

// operatorSystemsHTTP 查找该运营单位可用于历史对比的系统。
// 参数 w 为响应，r 携带单位、关键词和分页信息。
// 返回值 无。
func (s *Service) operatorSystemsHTTP(w http.ResponseWriter, r *http.Request) {
	values, organization, err := operatorAssessmentQuery(r.URL.RawQuery, "q", "limit", "before")
	if err != nil {
		respondHistory(w, nil, err)
		return
	}
	keyword := strings.TrimSpace(values.Get("q"))
	filters, limitErr := operatorAssessmentFilters(values, "limit")
	before, beforeValid := parseHistoryUUID(values.Get("before"))
	if !validAssessmentText(keyword, 120) || limitErr != nil || (values.Has("before") && !beforeValid) {
		respondHistory(w, nil, errInput)
		return
	}
	result, err := s.listOperatorSystems(r.Context(), organization, keyword, before, filters.limit)
	respondHistory(w, result, err)
}

// operatorHistoryHTTP 查询同备案已终审报告的对外摘要。
// 参数 w 为响应，r 携带备案标识、单位范围及分页。
// 返回值 无。
func (s *Service) operatorHistoryHTTP(w http.ResponseWriter, r *http.Request) {
	filing, ok := parseHistoryUUID(r.PathValue("id"))
	if !ok {
		respondHistory(w, nil, pgx.ErrNoRows)
		return
	}
	values, organization, err := operatorAssessmentQuery(r.URL.RawQuery, "cursor", "limit")
	if err != nil {
		respondHistory(w, nil, err)
		return
	}
	filters, err := operatorAssessmentFilters(values, "cursor", "limit")
	if err != nil {
		respondHistory(w, nil, err)
		return
	}
	result, err := s.operatorHistory(r.Context(), organization, filing, filters)
	respondHistory(w, result, err)
}

// operatorCompareHTTP 返回指定一前一后的两期同备案结果。
// 参数 w 为响应，r 提供备案、运营者及报告两个标识。
// 返回值 无。
func (s *Service) operatorCompareHTTP(w http.ResponseWriter, r *http.Request) {
	pair, err := readOperatorPair(r)
	if err != nil {
		respondHistory(w, nil, err)
		return
	}
	result, err := s.compareOperatorHistory(r.Context(), pair.organization, pair.filing, pair.from, pair.to)
	respondHistory(w, result, err)
}

// operatorAssessmentFilters 仅转交当前操作所需的分页参数。
// 参数 values 为已经去重的输入，keys 为适用于当前页面的分页字段。
// 返回值 复用的历史分页契约或输入错误。
func operatorAssessmentFilters(values url.Values, keys ...string) (archiveFilters, error) {
	pagination := make(url.Values, len(keys))
	for _, key := range keys {
		if values.Has(key) {
			pagination[key] = values[key]
		}
	}
	return parseArchiveFilters(pagination.Encode())
}

// assessmentQuery 拒绝未知参数和重复字段。
// 参数 raw 是 URL 原始参数，accepted 是该接口支持的字段。
// 返回值 查询映射或输入异常。
func assessmentQuery(raw string, accepted ...string) (url.Values, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, errInput
	}
	for key, entries := range values {
		if len(entries) != 1 || !slices.Contains(accepted, key) {
			return nil, errInput
		}
	}
	return values, nil
}

// operatorAssessmentQuery 解析并要求明确的运营单位范围。
// 参数 raw 是 URL 查询，accepted 为该业务允许的其他字段。
// 返回值 去重参数、所选单位或输入错误。
func operatorAssessmentQuery(raw string, accepted ...string) (url.Values, string, error) {
	values, err := assessmentQuery(raw, append(accepted, "organization")...)
	if err != nil {
		return nil, "", err
	}
	name := strings.TrimSpace(values.Get("organization"))
	if name == "" || !validAssessmentText(name, 200) {
		return nil, "", errInput
	}
	return values, name, nil
}

// validAssessmentText 核对查询关键字的编码、长度和控制字符。
// 参数 value 为查询文本，max 为允许的 Unicode 字符数。
// 返回值 满足限制时为真。
func validAssessmentText(value string, max int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= max && strings.IndexFunc(value, unicode.IsControl) == -1
}
