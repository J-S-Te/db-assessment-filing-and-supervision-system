package access

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"grade-business/internal/httpx"
)

// registerOperatorRegistration 挂载申请查询与审核接口，权限由现有网关处理。
// 参数 mux 为应用路由。
// 返回值 无。
func (s *Service) registerOperatorRegistration(mux *http.ServeMux) {
	mux.HandleFunc(registrationSubmitPattern, s.createRegistration)
	mux.HandleFunc(registrationStatusPattern, s.lookupRegistration)
	mux.HandleFunc(registrationAreasPattern, registrationAreas)
	mux.HandleFunc(registrationListPattern, s.registrationList)
	mux.HandleFunc(registrationReviewPattern, s.registrationAction)
}

// registrationAreas 返回与衢州驾驶舱共用的注册辖区名录。
// 参数 w 为 HTTP 响应，匿名请求不携带申请资料。
// 返回值 无。
func registrationAreas(w http.ResponseWriter, _ *http.Request) {
	type option struct {
		Code  string `json:"code"`
		Label string `json:"label"`
	}
	codes := cityDistricts[quzhouCityCode]
	result := struct {
		Items []option `json:"items"`
	}{Items: make([]option, 0, len(codes))}
	for _, code := range codes {
		result.Items = append(result.Items, option{Code: code, Label: registrationDistrictLabel(code)})
	}
	httpx.JSON(w, 200, result)
}

// createRegistration 受理匿名运营单位注册申请并发放私密查询码。
// 参数 w、r 为 HTTP 响应和注册请求。
// 返回值 无。
func (s *Service) createRegistration(w http.ResponseWriter, r *http.Request) {
	if !s.limitRegistration(s.authSource(r), "submit", 15) {
		registrationThrottled(w)
		return
	}
	var input registrationInput
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil || !normalizeRegistration(&input) {
		httpx.Error(w, 400, "invalid_registration",
			"请核对单位名称、统一社会信用代码、行政归属、联系人、手机号、登录账号及密码。")
		return
	}
	result, err := s.submitRegistration(r.Context(), input)
	if err != nil {
		registrationUnavailable(w, err)
		return
	}
	httpx.JSON(w, 201, result)
}

// lookupRegistration 通过独立随机凭据返回申请状态和统一的驳回提示。
// 参数 w、r 为 HTTP 响应和带查询码的请求。
// 返回值 无。
func (s *Service) lookupRegistration(w http.ResponseWriter, r *http.Request) {
	if !s.limitRegistration(s.authSource(r), "status", 60) {
		registrationThrottled(w)
		return
	}
	var input struct {
		LookupCode string `json:"lookupCode"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil {
		httpx.Error(w, 400, "invalid_lookup_code", "请填写申请回执中的查询码。")
		return
	}
	fingerprint, ok := lookupFingerprint(strings.TrimSpace(input.LookupCode))
	if !ok {
		httpx.Error(w, 400, "invalid_lookup_code", "请填写申请回执中的查询码。")
		return
	}
	var result registrationResult
	err := s.db.QueryRow(r.Context(), `SELECT status,created_at,reviewed_at
		FROM operator_registrations WHERE lookup_hash=$1`, fingerprint[:]).
		Scan(&result.Status, &result.CreatedAt, &result.ReviewedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, 404, "registration_not_found", "未找到该注册申请。")
		} else {
			registrationUnavailable(w, err)
		}
		return
	}
	if result.Status == "rejected" {
		result.Reason = "申请未通过，请联系监管部门核对资料。"
	}
	httpx.JSON(w, 200, result)
}

// registrationList 返回审核账号可见的申请清单。
// 参数 w、r 为 HTTP 响应和带状态筛选或游标的请求。
// 返回值 无。
func (s *Service) registrationList(w http.ResponseWriter, r *http.Request) {
	filter, ok := parseRegistrationFilter(r.URL.RawQuery)
	if !ok {
		httpx.Error(w, 400, "invalid_filter", "申请状态或翻页参数不正确。")
		return
	}
	items, next, err := s.listRegistrations(r.Context(), filter)
	if err != nil {
		writeRegistrationError(w, err)
		return
	}
	result := struct {
		Items      []registrationItem `json:"items"`
		NextCursor string             `json:"nextCursor,omitempty"`
	}{Items: items}
	if next != 0 {
		result.NextCursor = strconv.FormatInt(next, 10)
	}
	httpx.JSON(w, 200, result)
}

type registrationFilter struct {
	status, query, creditCode string
	cursor                    int64
	limit                     int
}

// parseRegistrationFilter 用途为校验状态、检索、单位分组明细和分页条件。
// 参数 raw 为原始查询字符串。
// 返回值 正规化列表条件和是否有效。
func parseRegistrationFilter(raw string) (registrationFilter, bool) {
	values, err := url.ParseQuery(raw)
	filter := registrationFilter{status: "pending", limit: 20}
	if err != nil {
		return filter, false
	}
	for key, items := range values {
		switch key {
		case "status", "q", "creditCode", "cursor", "limit":
			if len(items) != 1 {
				return filter, false
			}
		default:
			return filter, false
		}
	}
	if values.Has("status") {
		filter.status = values.Get("status")
	}
	if filter.status != "pending" && filter.status != "approved" && filter.status != "rejected" {
		return filter, false
	}
	if values.Has("cursor") {
		filter.cursor, err = strconv.ParseInt(values.Get("cursor"), 10, 64)
		if err != nil || filter.cursor < 1 {
			return filter, false
		}
	}
	if values.Has("limit") {
		filter.limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil || filter.limit < 1 || filter.limit > 100 {
			return filter, false
		}
	}
	filter.query = strings.TrimSpace(values.Get("q"))
	if values.Has("creditCode") {
		filter.creditCode = strings.ToUpper(strings.TrimSpace(values.Get("creditCode")))
		if !validCreditCode(filter.creditCode) {
			return filter, false
		}
	}
	return filter, registrationText(filter.query, 120, true)
}

// registrationAction 校验人工核验或驳回理由并办理一次决定。
// 参数 w、r 为 HTTP 响应和具备审核会话的请求。
// 返回值 无。
func (s *Service) registrationAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var parsed pgtype.UUID
	var input registrationDecision
	if len(id) != 36 || parsed.Scan(id) != nil || !parsed.Valid {
		httpx.Error(w, 404, "registration_not_found", "未找到该注册申请。")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil || !validateRegistrationDecision(&input) {
		httpx.Error(w, 400, "invalid_registration_decision",
			"通过前须确认已核实单位资料、行政归属及联系人授权，驳回须填写原因。")
		return
	}
	actor, _ := Current(r.Context())
	result, err := s.reviewRegistration(r.Context(), actor, id, input)
	if err != nil {
		writeRegistrationError(w, err)
		return
	}
	httpx.JSON(w, 200, result)
}

// validateRegistrationDecision 验证审核动作和对应的必填凭据。
// 参数 input 为监管端填写的审核意见及核验确认。
// 返回值 仅允许通过或驳回且满足对应要求时为真。
func validateRegistrationDecision(input *registrationDecision) bool {
	input.Reason = strings.TrimSpace(input.Reason)
	switch input.Decision {
	case "approve":
		return input.Verified && registrationText(input.Reason, 500, true)
	case "reject":
		return !input.Verified && registrationText(input.Reason, 500, false)
	default:
		return false
	}
}

// registrationThrottled 返回匿名注册入口的短时频率限制。
// 参数 w 为 HTTP 响应。
// 返回值 无。
func registrationThrottled(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "300")
	httpx.Error(w, 429, "registration_throttled", "尝试次数过多，请五分钟后重试。")
}

// writeRegistrationError 返回可处理的错误并隐藏连接与个人信息。
// 参数 w 为 HTTP 响应，err 为当前注册或审批失败。
// 返回值 无。
func writeRegistrationError(w http.ResponseWriter, err error) {
	switch registrationStoreError(err) {
	case errRegistrationArea:
		httpx.Error(w, 422, "registration_outside_area", "无法确定本平台办理辖区，请联系监管部门。")
	case errRegistrationUnit:
		httpx.Error(w, 409, "registration_unit_conflict", "单位资料与可用名录不一致，请核对后联系监管部门。")
	case errRegistrationAccount:
		httpx.Error(w, 409, "registration_unit_has_account", "该单位已开通账号，请联系本单位管理员。")
	case errRegistrationUsername:
		httpx.Error(w, 409, "registration_username_taken", "登录账号已被占用，请使用其他账号。")
	case errRegistrationPending:
		httpx.Error(w, 409, "registration_exists", "该单位已存在审批通过的申请。")
	case errRegistrationReviewed:
		httpx.Error(w, 409, "registration_reviewed", "申请已审核，请刷新后查看最新结果。")
	default:
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, 404, "registration_not_found", "未找到该注册申请。")
			return
		}
		registrationUnavailable(w, err)
	}
}

// registrationUnavailable 记录错误类型并向调用方返回统一的服务异常。
// 参数 w 为 HTTP 响应，err 为内部持久化或读取错误。
// 返回值 无。
func registrationUnavailable(w http.ResponseWriter, err error) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		slog.Error("运营单位注册处理失败", "sqlState", pgErr.Code, "constraint", pgErr.ConstraintName)
	} else {
		slog.Error("运营单位注册处理失败", "errorType", fmt.Sprintf("%T", err))
	}
	httpx.Error(w, 503, "registration_unavailable", "暂时无法办理注册申请，请稍后重试。")
}
