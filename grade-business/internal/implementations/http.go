package implementations

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/internal/directoryref"
	"grade-business/internal/httpx"
	"grade-business/internal/reviewinbox"
)

// Service 提供实施报备管理接口和当前测试办理人上下文。
type Service struct {
	pool     *pgxpool.Pool
	operator string
	now      func() time.Time
}

// New 创建实施管理服务。
// 参数 pool 为业务库连接池，operator 为服务端设置的本地办理人。
// 返回值 实施报备服务。
func New(pool *pgxpool.Pool, operator string) *Service {
	return &Service{pool: pool, operator: operator, now: time.Now}
}

// Register 将监管和机构的实施报备接口注册到现有 HTTP 路由器。
// 参数 mux 为业务路由表。
// 返回值 无。
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET "+path, reportEndpoint(s.listHTTP, false))
	mux.HandleFunc("POST "+path, reportEndpoint(s.createHTTP, false))
	mux.HandleFunc("GET "+path+"/filing-options", reportEndpoint(s.choicesHTTP, false))
	mux.HandleFunc("GET "+path+"/quota", reportEndpoint(s.quotaHTTP, false))
	mux.HandleFunc("GET "+path+"/{id}", reportEndpoint(s.detailHTTP, false))
	mux.HandleFunc("GET "+path+"/{id}/precheck", reportEndpoint(s.precheckHTTP, false))
	mux.HandleFunc("PATCH "+path+"/{id}", reportEndpoint(s.editHTTP, false))
	mux.HandleFunc("POST "+path+"/{id}/actions", reportEndpoint(s.executeHTTP, false))
	mux.HandleFunc("GET "+path+"/{id}/history", s.historyHTTP)
	mux.HandleFunc("GET "+institutionalPath, reportEndpoint(s.listHTTP, true, listFilterKeys...))
	mux.HandleFunc("POST "+institutionalPath, reportEndpoint(s.createHTTP, true))
	mux.HandleFunc("GET "+institutionalPath+"/filing-options", reportEndpoint(s.choicesHTTP, true, "q"))
	mux.HandleFunc("GET "+institutionalPath+"/quota", reportEndpoint(s.quotaHTTP, true, "filingId"))
	mux.HandleFunc("GET "+institutionalPath+"/{id}", reportEndpoint(s.detailHTTP, true))
	mux.HandleFunc("GET "+institutionalPath+"/{id}/precheck", reportEndpoint(s.precheckHTTP, true, "expectedVersion"))
	mux.HandleFunc("PATCH "+institutionalPath+"/{id}", reportEndpoint(s.editHTTP, true))
	mux.HandleFunc("POST "+institutionalPath+"/{id}/actions", reportEndpoint(s.executeHTTP, true))
}

// reportEndpoint 为机构端校验请求范围，再复用报备端点的处理逻辑。
// 参数 handle 为报备端点，scoped 表示是否要求机构范围，filters 是机构端允许的业务筛选。
// 返回值 可注册到路由器的 HTTP 处理函数。
func reportEndpoint(handle func(http.ResponseWriter, *http.Request, string), scoped bool, filters ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !scoped {
			handle(w, r, "")
			return
		}
		request, institution, err := scopedImplementationRequest(r, "institution", filters)
		if err != nil {
			writeError(w, errInvalid)
			return
		}
		handle(w, request, institution)
	}
}

// scopedImplementationRequest 单独检查机构范围，再交给原业务处理器读取剩余筛选。
// 参数 r 为原请求，key 为机构或单位范围字段，filters 为允许的业务筛选字段。
// 返回值 保留业务筛选的请求、当前业务范围或校验错误。
func scopedImplementationRequest(r *http.Request, key string, filters []string) (*http.Request, string, error) {
	if len(filters) == 0 {
		scope, err := reviewinbox.Scope(r, key)
		return r, scope, err
	}
	values, err := filterValues(r.URL.RawQuery, append([]string{key}, filters...)...)
	if err != nil {
		return nil, "", err
	}
	checked := r.Clone(r.Context())
	checked.URL.RawQuery = url.Values{key: values[key]}.Encode()
	scope, err := reviewinbox.Scope(checked, key)
	values.Del(key)
	checked.URL.RawQuery = values.Encode()
	return checked, scope, err
}

// listHTTP 返回按状态、类型和关键词筛选的报备摘要。
// 参数 w 为响应对象，r 为列表请求，institution 为机构范围，监管端留空。
// 返回值 无。
func (s *Service) listHTTP(w http.ResponseWriter, r *http.Request, institution string) {
	options, err := parseList(r.URL.RawQuery)
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := s.readPage(r.Context(), institution, options)
	respond(w, http.StatusOK, result, err)
}

// createHTTP 创建关联有效备案的实施草稿。
// 参数 w 为响应对象，r 为带条目信息的写请求，institution 为机构范围，监管端留空。
// 返回值 无。
func (s *Service) createHTTP(w http.ResponseWriter, r *http.Request, institution string) {
	var item input
	if err := httpx.Decode(w, r, &item); err != nil {
		writeError(w, errInvalid)
		return
	}
	item, err := validateInput(item)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	result, err := s.createReport(r.Context(), institution, item)
	respond(w, http.StatusCreated, result, err)
}

// choicesHTTP 返回仍有效且已经终审通过的备案选项，运营者可按备案标识精确查询。
// 参数 w 为响应对象，r 为带可选筛选的请求，institution 为机构范围，监管端留空。
// 返回值 无。
func (s *Service) choicesHTTP(w http.ResponseWriter, r *http.Request, institution string) {
	filters := []string{"q"}
	if operatorOrganization(r.Context()) != "" {
		filters = append(filters, "filingId")
	}
	values, err := filterValues(r.URL.RawQuery, filters...)
	filingID := values.Get("filingId")
	if err != nil || !boundedText(values.Get("q"), 120, false) ||
		(values.Has("filingId") && !validID(filingID)) {
		writeError(w, errInvalid)
		return
	}
	items, err := s.filingChoices(r.Context(), institution, strings.TrimSpace(values.Get("q")), filingID)
	respond(w, http.StatusOK, struct {
		Items []choice `json:"items"`
	}{items}, err)
}

// quotaHTTP 返回所选备案当年占用的变更额度。
// 参数 w 为响应对象，r 为包含备案标识的请求，institution 为机构范围，监管端留空。
// 返回值 无。
func (s *Service) quotaHTTP(w http.ResponseWriter, r *http.Request, institution string) {
	values, err := filterValues(r.URL.RawQuery, "filingId")
	if err != nil || !validID(values.Get("filingId")) {
		writeError(w, errInvalid)
		return
	}
	result, err := s.readQuota(r.Context(), values.Get("filingId"), institution)
	respond(w, http.StatusOK, result, err)
}

// detailHTTP 返回报备详情和当前可执行的业务动作。
// 参数 w 为响应对象，r 为包含报备标识的请求，institution 为机构范围，监管端留空。
// 返回值 无。
func (s *Service) detailHTTP(w http.ResponseWriter, r *http.Request, institution string) {
	id, ok := requestID(w, r)
	if !ok {
		return
	}
	result, err := getRecord(r.Context(), s.pool, id, institution, false)
	if err == nil {
		now := s.now()
		result.CanPrecheck = s.canPrecheck(r.Context(), institution, result)
		switch {
		case operatorOrganization(r.Context()) != "":
			result.AllowedActions = operatorActions(result, s.reportOperator(r.Context(), ""), now)
			result.ReporterID, result.DirectorySnapshot = nil, nil
		case institution != "":
			result.AllowedActions = institutionActions(result, s.reportOperator(r.Context(), institution), now)
		default:
			setReviewActions(r.Context(), &result, s.operator, now)
		}
		err = s.detailRuleTimes(r.Context(), &result, now)
	}
	respond(w, http.StatusOK, result.detail, err)
}

// detailRuleTimes 返回计划修订时间及已获提交权限的冷却提示。
// 参数 ctx 为查询上下文，record 为已确认动作范围的详情，now 为一致的规则时刻。
// 返回值 所需冷却查询失败时的数据库错误。
func (s *Service) detailRuleTimes(ctx context.Context, record *storedRecord, now time.Time) error {
	switch record.Status {
	case submitted, initial, final, supervision, returned:
		record.PlanEditDeadline = planEditDeadline(*record)
	}
	if !slices.Contains(record.AllowedActions, "submit") {
		return nil
	}
	available, err := readResubmitAvailableAt(ctx, s.pool, record.FilingID, now)
	if available != nil {
		record.ResubmitAt = available
		record.AllowedActions = slices.DeleteFunc(record.AllowedActions, func(action string) bool {
			return action == "submit"
		})
	}
	return err
}

// editHTTP 保存草稿或补正报备的已校验字段。
// 参数 w 为响应对象，r 为包含字段及版本的编辑请求，institution 为机构范围，监管端留空。
// 返回值 无。
func (s *Service) editHTTP(w http.ResponseWriter, r *http.Request, institution string) {
	id, ok := requestID(w, r)
	if !ok {
		return
	}
	var patch patchInput
	if err := httpx.Decode(w, r, &patch); err != nil || patch.ExpectedVersion < 1 {
		writeError(w, errInvalid)
		return
	}
	clean, err := validateInput(patch.input)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	patch.input = clean
	result, err := s.editReport(r.Context(), id, institution, patch)
	respond(w, http.StatusOK, result, err)
}

// executeHTTP 接收与当前版本绑定的业务动作。
// 参数 w 为响应对象，r 为命令请求，institution 为机构范围，监管端留空。
// 返回值 无。
func (s *Service) executeHTTP(w http.ResponseWriter, r *http.Request, institution string) {
	id, ok := requestID(w, r)
	if !ok {
		return
	}
	var change command
	if err := httpx.Decode(w, r, &change); err != nil || change.ExpectedVersion < 1 ||
		!boundedText(change.Comment, 1000, true) {
		writeError(w, errInvalid)
		return
	}
	change.Comment = strings.TrimSpace(change.Comment)
	result, err := s.execute(r.Context(), id, institution, change)
	respond(w, http.StatusOK, result, err)
}

// historyHTTP 分页读取报备的执行和审核事件。
// 参数 w 为响应对象，r 为带报备标识及分页字段的请求。
// 返回值 无。
func (s *Service) historyHTTP(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w, r)
	if !ok {
		return
	}
	before, limit, err := historyOptions(r.URL.RawQuery)
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := s.readHistory(r.Context(), id, before, limit)
	respond(w, http.StatusOK, result, err)
}

// historyOptions 解析审核记录的页大小和版本边界。
// 参数 raw 为原始查询参数。
// 返回值 版本边界、页大小和参数不合法错误。
func historyOptions(raw string) (int64, int, error) {
	values, err := filterValues(raw, "beforeVersion", "limit")
	if err != nil {
		return 0, 0, err
	}
	limit, err := parseLimit(values.Get("limit"))
	if err != nil {
		return 0, 0, err
	}
	var before int64
	if value := values.Get("beforeVersion"); value != "" {
		before, err = strconv.ParseInt(value, 10, 64)
		if err != nil || before < 1 {
			return 0, 0, errInvalid
		}
	}
	return before, limit, nil
}

// requestID 取得有效的路径标识并统一不存在响应。
// 参数 w 为响应对象，r 为当前请求。
// 返回值 路径标识及其合法性。
func requestID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !validID(id) {
		writeError(w, errNotFound)
		return "", false
	}
	return id, true
}

// respond 返回最小业务结构，或按领域错误映射状态码。
// 参数 w 为响应对象，status 为成功状态，result 为业务结果，err 为可能发生的错误。
// 返回值 无。
func respond(w http.ResponseWriter, status int, result any, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, status, result)
}

// writeError 返回可公开的错误而隐藏数据库及运行环境信息。
// 参数 w 为响应对象，err 为端点错误。
// 返回值 无。
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", errNotFound.Error())
	case errors.Is(err, errFiling):
		httpx.Error(w, http.StatusBadRequest, "invalid_filing", errFiling.Error())
	case errors.Is(err, errTarget):
		httpx.Error(w, http.StatusBadRequest, "invalid_assessment_org", errTarget.Error())
	case errors.Is(err, directoryref.ErrUnavailable):
		httpx.Error(w, http.StatusConflict, "directory_unavailable", err.Error())
	case errors.Is(err, directoryref.ErrPersonRequired):
		httpx.Error(w, http.StatusBadRequest, "directory_person_required", err.Error())
	case errors.Is(err, directoryref.ErrPersonInvalid):
		httpx.Error(w, http.StatusBadRequest, "directory_person_invalid", err.Error())
	case errors.Is(err, errStale):
		httpx.Error(w, http.StatusConflict, "stale_version", errStale.Error())
	case errors.Is(err, errPermission):
		httpx.Error(w, http.StatusForbidden, "permission_denied", errPermission.Error())
	case errors.Is(err, errSameReviewer):
		httpx.Error(w, http.StatusConflict, "same_reviewer", errSameReviewer.Error())
	case errors.Is(err, errUnknownReviewer):
		httpx.Error(w, http.StatusConflict, "first_reviewer_unverified", errUnknownReviewer.Error())
	case errors.Is(err, errUnverifiedReviewers):
		httpx.Error(w, http.StatusConflict, "review_separation_unverified", errUnverifiedReviewers.Error())
	case errors.Is(err, errRestartUnavailable):
		httpx.Error(w, http.StatusConflict, "legacy_review_restart_unavailable", errRestartUnavailable.Error())
	case errors.Is(err, errConflict):
		httpx.Error(w, http.StatusConflict, "invalid_state", errConflict.Error())
	case errors.Is(err, errQuota):
		httpx.Error(w, http.StatusConflict, "change_quota", errQuota.Error())
	case errors.Is(err, errWithdrawal):
		httpx.Error(w, http.StatusConflict, "withdraw_unavailable", errWithdrawal.Error())
	case errors.Is(err, errPlanClosed):
		httpx.Error(w, http.StatusConflict, "plan_edit_deadline", errPlanClosed.Error())
	case errors.Is(err, errCooldown):
		httpx.Error(w, http.StatusConflict, "resubmission_cooldown", errCooldown.Error())
	case errors.Is(err, errInvalid), errors.Is(err, errComment), errors.Is(err, errRestartComment):
		httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
	default:
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "实施报备暂时无法处理，请稍后重试。")
	}
}
