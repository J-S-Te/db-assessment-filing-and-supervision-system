package assessmentdue

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/internal/httpx"
)

// Service 管理备案下一次测评日期和站内提醒。
type Service struct {
	db    *pgxpool.Pool
	actor string
}

// New 建立下一次测评日期服务。
// 参数 db、actor 分别为业务数据库和监管经办席位。
// 返回值 可注册路由和运行提醒工作的服务。
func New(db *pgxpool.Pool, actor string) *Service {
	return &Service{db: db, actor: actor}
}

// Register 注册监管登记和各端日期读取路由。
// 参数 mux 为当前同源业务路由。
// 返回值 无。
func (s *Service) Register(mux *http.ServeMux) {
	base := "/api/v1/" + resource
	s.registerReads(mux, base, "")
	mux.HandleFunc("POST "+base, s.registerHTTP)
	mux.HandleFunc("PATCH "+base+"/{id}", s.changeHTTP)
	mux.HandleFunc("POST "+base+"/sweep", s.sweepHTTP)
	for _, audience := range []string{"operator", "institution"} {
		s.registerReads(mux, "/api/v1/"+audience+"/"+resource, audience)
	}
}

// registerReads 提供各端按当前备案范围查看日期清单和详情的路由。
// 参数 mux、base、audience 为路由、业务前缀和办理端。
// 返回值 无。
func (s *Service) registerReads(mux *http.ServeMux, base, audience string) {
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		query, err := parseFilters(r.URL.RawQuery, audience, true)
		if err != nil {
			respond(w, 200, nil, err)
			return
		}
		result, err := s.list(r.Context(), query)
		respond(w, 200, result, err)
	})
	mux.HandleFunc("GET "+base+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		query, err := parseFilters(r.URL.RawQuery, audience, false)
		if err != nil || !validID(id) {
			respond(w, 200, nil, errInput)
			return
		}
		result, err := s.detail(r.Context(), query, id)
		respond(w, 200, result, err)
	})
}

// registerHTTP 核对监管提供的备案、明确日期与登记依据。
// 参数 w、r 为响应和监管请求。
// 返回值 无。
func (s *Service) registerHTTP(w http.ResponseWriter, r *http.Request) {
	var input registration
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil || !validRegistration(&input) {
		respond(w, 201, nil, errInput)
		return
	}
	result, err := s.storeRegistration(r.Context(), input)
	respond(w, 201, result, err)
}

// changeHTTP 解析监管明确的修订或结束指令及其并发版本。
// 参数 w、r 为响应和监管请求。
// 返回值 无。
func (s *Service) changeHTTP(w http.ResponseWriter, r *http.Request) {
	var input change
	id := r.PathValue("id")
	if r.URL.RawQuery != "" || !validID(id) || httpx.Decode(w, r, &input) != nil || !validChange(&input) {
		respond(w, 200, nil, errInput)
		return
	}
	result, err := s.storeChange(r.Context(), id, input)
	respond(w, 200, result, err)
}

// sweepHTTP 处理监管手动同步当前日期提醒。
// 参数 w、r 为响应和不含参数的扫描请求。
// 返回值 无。
func (s *Service) sweepHTTP(w http.ResponseWriter, r *http.Request) {
	if data, err := io.ReadAll(io.LimitReader(r.Body, 1)); err != nil || len(data) != 0 || r.URL.RawQuery != "" {
		respond(w, 200, nil, errInput)
		return
	}
	count, err := s.Sweep(r.Context())
	respond(w, 200, struct {
		Notices int64 `json:"notices"`
	}{count}, err)
}

// respond 映射受控业务错误并隐藏内部数据库信息。
// 参数 w、status、result、err 为 HTTP 输出、成功状态、结果和错误。
// 返回值 无。
func respond(w http.ResponseWriter, status int, result any, err error) {
	switch {
	case err == nil:
		httpx.JSON(w, status, result)
	case errors.Is(err, errInput):
		httpx.Error(w, 400, "invalid_input", "请核对测评日期、依据和办理范围。")
	case errors.Is(err, errScope):
		httpx.Error(w, 409, "assessment_due_scope", "当前备案已失效或无法登记下一次测评日期。")
	case errors.Is(err, errConflict):
		httpx.Error(w, 409, "assessment_due_conflict", "已有在办登记或版本已更新，请刷新后确认。")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, 404, "not_found", "未找到当前范围内的下一次测评日期。")
	default:
		slog.Error("下一次测评日期办理失败", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, 500, "internal_error", "暂时无法办理下一次测评日期。")
	}
}
