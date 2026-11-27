package materialexpiry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/internal/cases"
	"grade-business/internal/httpx"
)

// Service 管理真实已核准材料的显式有效期、续传及站内提醒。
type Service struct {
	db    *pgxpool.Pool
	files *cases.Service
	actor string
}

// New 组装有效期业务使用的共享数据库和正式材料存储。
// 参数 db、files、actor 分别为业务池、附件服务及监管席位。
// 返回值 可注册路由与后台扫描的业务服务。
func New(db *pgxpool.Pool, files *cases.Service, actor string) *Service {
	return &Service{db: db, files: files, actor: actor}
}

// Register 注册三端台账与受版本保护的有效期、续传和审核操作。
// 参数 mux 为业务同源路由。
// 返回值 无。
func (s *Service) Register(mux *http.ServeMux) {
	admin := "/api/v1/" + resource
	mux.HandleFunc("GET "+admin+"/source-options", s.sourceHTTP)
	mux.HandleFunc("POST "+admin, s.registerHTTP)
	mux.HandleFunc("POST "+admin+"/{id}/actions", withRecord("", s.actionHTTP))
	mux.HandleFunc("POST "+admin+"/sweep", s.sweepHTTP)
	s.registerAudience(mux, admin, "")
	for _, audience := range []string{"institution", "operator"} {
		base := "/api/v1/" + audience + "/" + resource
		s.registerAudience(mux, base, audience)
		mux.HandleFunc("POST "+base+"/{id}/renewals", withRecord(audience, s.renewHTTP))
	}
}

// registerAudience 挂载指定席位仅有读取权限的接口。
// 参数 mux、base、audience 为业务路由、路径前缀及访问角色。
// 返回值 无。
func (s *Service) registerAudience(mux *http.ServeMux, base, audience string) {
	mux.HandleFunc("GET "+base, s.listHTTP(audience))
	mux.HandleFunc("GET "+base+"/{id}", withRecord(audience, s.detailHTTP))
	mux.HandleFunc("GET "+base+"/{id}/renewals/{renewalId}/download", withRecord(audience, s.downloadHTTP))
}

// withRecord 核对只允许的查询字段、角色收件范围及业务编号。
// 参数 audience 为席位，next 为读取已校验标识及范围的端点。
// 返回值 可绑定到同源路由的请求函数。
func withRecord(audience string, next func(http.ResponseWriter, *http.Request, scope, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		seat, _, err := splitQuery(r.URL.RawQuery, audience)
		raw := r.PathValue("id")
		if err != nil || !validID(raw) {
			respond(w, 200, nil, errInvalid)
			return
		}
		next(w, r, seat, strings.ToLower(raw))
	}
}

// sourceHTTP 筛选可登记的正式材料并返回续页位置。
// 参数 w、r 为响应输出和来源材料查询。
// 返回值 无。
func (s *Service) sourceHTTP(w http.ResponseWriter, r *http.Request) {
	_, query, err := splitQuery(r.URL.RawQuery, "", "q", "caseKind", "before", "reportId")
	keyword := strings.TrimSpace(query.Get("q"))
	kind := query.Get("caseKind")
	cursor, cursorErr := parseSourceCursor(query)
	reportID, reportErr := parseReportFilter(query)
	if err != nil || cursorErr != nil || reportErr != nil || !validText(keyword, 0, 120) ||
		(query.Has("caseKind") && kind != "filing" && kind != "report") ||
		(reportID != "" && kind == "filing") {
		respond(w, 200, nil, errInvalid)
		return
	}
	result, err := s.sourceOptions(r.Context(), keyword, kind, reportID, cursor)
	respond(w, 200, result, err)
}

// registerHTTP 校验明确的材料编号、有效期和监管登记依据。
// 参数 w、r 为应答和 JSON 登记请求。
// 返回值 无。
func (s *Service) registerHTTP(w http.ResponseWriter, r *http.Request) {
	var input registerInput
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil || !sanitizeRegistration(&input) {
		respond(w, 201, nil, errInvalid)
		return
	}
	result, err := s.register(r.Context(), input)
	respond(w, 201, result, err)
}

// sanitizeRegistration 规范登记输入并拒绝无依据或异常日期。
// 参数 input 为 JSON 解码后的登记申请。
// 返回值 可以进入事务办理时返回真。
func sanitizeRegistration(input *registerInput) bool {
	input.Evidence = strings.TrimSpace(input.Evidence)
	input.RequestID = strings.ToLower(input.RequestID)
	input.SourceAttachmentID = strings.ToLower(input.SourceAttachmentID)
	warning := input.WarningDays
	return validID(input.RequestID) && validID(input.SourceAttachmentID) &&
		validDate(input.ValidUntil) && validText(input.Evidence, 1, 1000) &&
		(warning == nil || *warning >= 0 && *warning <= 90)
}

// listHTTP 以既有位置和日期状态查询访问席位的有界清单。
// 参数 audience 为路由绑定席位。
// 返回值 只读清单端点。
func (s *Service) listHTTP(audience string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		seat, query, err := splitQuery(r.URL.RawQuery, audience, "before", "status", "reportId")
		cursor, cursorErr := pageCursor(query)
		reportID, reportErr := parseReportFilter(query)
		status := query.Get("status")
		if err != nil || cursorErr != nil || reportErr != nil || !validStatus(status) {
			respond(w, 200, nil, errInvalid)
			return
		}
		out, err := s.listRecords(r.Context(), seat, status, reportID, cursor)
		respond(w, 200, out, err)
	}
}

// detailHTTP 查询记录、可见证据及台账历史。
// 参数 w、r、seat、id 是响应、请求及已解析的归属与标识。
// 返回值 无。
func (s *Service) detailHTTP(w http.ResponseWriter, r *http.Request, seat scope, id string) {
	out, err := s.detail(r.Context(), seat, id)
	respond(w, 200, out, err)
}

// actionHTTP 按原版本解释监管的通过、驳回或结束登记操作。
// 参数 w、r 为应答和请求，id 为已校验台账标识。
// 返回值 无。
func (s *Service) actionHTTP(w http.ResponseWriter, r *http.Request, _ scope, id string) {
	var input actionInput
	if httpx.Decode(w, r, &input) != nil || !sanitizeAction(&input) {
		respond(w, 200, nil, errInvalid)
		return
	}
	out, err := s.act(r.Context(), id, input)
	respond(w, 200, out, err)
}

// sanitizeAction 限制显式监管操作和审核意见。
// 参数 input 为 JSON 解码的操作。
// 返回值 可进入状态机检查时返回真。
func sanitizeAction(input *actionInput) bool {
	input.Comment = strings.TrimSpace(input.Comment)
	if input.ExpectedVersion < 1 || !validText(input.Comment, 0, 1000) {
		return false
	}
	switch input.Action {
	case "approve":
		return true
	case "reject", "retire":
		return input.Comment != ""
	default:
		return false
	}
}

// sweepHTTP 只在本机监管显式触发时扫描当前有临期或逾期的记录。
// 参数 w、r 为响应和无请求正文的触发请求。
// 返回值 无。
func (s *Service) sweepHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if err != nil || len(body) != 0 || r.URL.RawQuery != "" {
		respond(w, 200, nil, errInvalid)
		return
	}
	count, err := s.Sweep(r.Context())
	respond(w, 200, struct {
		Notices int64 `json:"notices"`
	}{count}, err)
}

// renewHTTP 接收经席位和记录范围校验的一份新附件和日期依据。
// 参数 w、r、seat、id 为响应、请求、访问范围和记录标识。
// 返回值 无。
func (s *Service) renewHTTP(w http.ResponseWriter, r *http.Request, seat scope, id string) {
	item, err := loadRecord(r.Context(), s.db, seat, id)
	if err == nil && item.historical {
		err = pgx.ErrNoRows
	}
	if err != nil {
		respond(w, 201, nil, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentBody)
	err = r.ParseMultipartForm(1 << 20)
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			err = cases.ErrFileSize
		} else {
			err = errInvalid
		}
		respond(w, 201, nil, err)
		return
	}
	out, err := s.readAndRenew(r, seat, id)
	respond(w, 201, out, err)
}

// readAndRenew 解析精确表单并暂存完整校验的新文书。
// 参数 r 为已解析的续传请求，seat、id 为范围及台账。
// 返回值 待审状态或表单、文件及事务错误。
func (s *Service) readAndRenew(r *http.Request, seat scope, id string) (expirySummary, error) {
	form := r.MultipartForm
	if len(form.Value) != 3 || len(form.Value["expectedVersion"]) != 1 ||
		len(form.Value["validUntil"]) != 1 || len(form.Value["evidence"]) != 1 ||
		len(form.File) != 1 || len(form.File["file"]) != 1 {
		return expirySummary{}, errInvalid
	}
	version, err := strconv.ParseInt(form.Value["expectedVersion"][0], 10, 64)
	input := renewalInput{version: version, until: form.Value["validUntil"][0],
		evidence: strings.TrimSpace(form.Value["evidence"][0])}
	if err != nil || version < 1 || !validDate(input.until) || !validText(input.evidence, 1, 1000) {
		return expirySummary{}, errInvalid
	}
	part := form.File["file"][0]
	from, err := part.Open()
	if err != nil {
		return expirySummary{}, errInvalid
	}
	defer from.Close()
	file, key, digest, err := s.files.StagePublicationAttachment(part.Filename, from)
	if err != nil {
		return expirySummary{}, err
	}
	defer s.releaseUnused(r.Context(), key)
	return s.renew(r.Context(), seat, id, input, stagedRenewal{file, key, digest})
}

// downloadHTTP 仅给有权访问指定续传版本的请求发出真实受控文件。
// 参数 w、r、seat、id 为应答及完整范围请求。
// 返回值 无。
func (s *Service) downloadHTTP(w http.ResponseWriter, r *http.Request, seat scope, id string) {
	renewalID := r.PathValue("renewalId")
	if !validID(renewalID) {
		respond(w, 200, nil, pgx.ErrNoRows)
		return
	}
	if err := s.downloadRenewal(w, r, seat, id, renewalID); err != nil {
		respond(w, 200, nil, err)
	}
}

// respond 将可预期的状态错误转换为明确且不含内部信息的业务响应。
// 参数 w、status、out、err 指定应答、成功状态、可公开结果或错误。
// 返回值 无。
func respond(w http.ResponseWriter, status int, out any, err error) {
	switch {
	case err == nil:
		httpx.JSON(w, status, out)
	case errors.Is(err, errInvalid):
		httpx.Error(w, 400, "invalid_input", "请核对材料、办理范围、日期和字段。")
	case errors.Is(err, pgx.ErrNoRows) || errors.Is(err, cases.ErrNotFound):
		httpx.Error(w, 404, "not_found", "未找到可查看的材料有效期或证据。")
	case errors.Is(err, errConflict):
		httpx.Error(w, 409, "material_expiry_conflict", "记录已变化、同类材料已登记或已有待审续传，请刷新核对。")
	case errors.Is(err, errScope):
		httpx.Error(w, 409, "material_expiry_scope_changed", "原材料、终审状态或归属已变化，请重新查看正式材料。")
	case errors.Is(err, errDate):
		httpx.Error(w, 422, "material_expiry_date", "新有效期须晚于当前有效期和上海当日。")
	case errors.Is(err, errUnchanged):
		httpx.Error(w, 422, "material_expiry_unchanged", "续期须提供不同于已批准版本的材料。")
	case errors.Is(err, cases.ErrFileSize):
		httpx.Error(w, 413, "attachment_size", "请上传不超过 20 MiB 的非空附件。")
	case errors.Is(err, cases.ErrFileType):
		httpx.Error(w, 415, "unsupported_attachment", "请上传内容有效的 PDF 或 DOCX 材料。")
	default:
		if errors.Is(err, context.Canceled) {
			return
		}
		slog.Error("材料有效期办理失败", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, 500, "internal_error", "材料有效期服务暂时无法处理，请稍后重试。")
	}
}
