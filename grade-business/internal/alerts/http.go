package alerts

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

// Service 管理由本地已登记隐患生成的站内预警。
type Service struct {
	db          *pgxpool.Pool
	operator    string
	ruleWindows RuleWindows
}

// New 创建站内预警服务。
// 参数 pool 为业务数据库连接池，operator 为由应用配置的办理人。
// 返回值 可注册业务路由的预警服务。
func New(pool *pgxpool.Pool, operator string) *Service {
	return NewWithRuleWindows(pool, operator, RuleWindows{InitialDays: 3, FinalDays: 5})
}

// NewWithRuleWindows 使用统一审核日历创建有定向规则的预警服务。
// 参数 pool、operator 为连接池和经办标签，windows 为两个备案阶段的实际期限。
// 返回值 可挂载路由并运行预警任务的服务。
func NewWithRuleWindows(pool *pgxpool.Pool, operator string, windows RuleWindows) *Service {
	return &Service{db: pool, operator: operator, ruleWindows: windows}
}

// Register 注册站内预警列表、同步和办理路由。
// 参数 mux 为应用业务路由。
// 返回值 无。
func (s *Service) Register(mux *http.ServeMux) {
	const path = "/api/v1/alerts"
	s.registerRules(mux)
	mux.HandleFunc("GET "+path, s.handleList)
	mux.HandleFunc("GET "+path+"/summary", s.handleSummary)
	mux.HandleFunc("GET "+path+"/supervision-leads", s.handleSupervisionLeads)
	mux.HandleFunc("POST "+path+"/sync", s.handleSync)
	mux.HandleFunc("GET "+path+"/{id}", s.handleDetail)
	mux.HandleFunc("POST "+path+"/{id}/actions", s.handleAction)
}

// handleList 解析筛选并返回有界的已生成预警。
// 参数 w 为响应写入端，r 为列表请求。
// 返回值 无。
func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	filters, err := parseFilter(r.URL.RawQuery)
	if err != nil {
		respond(w, nil, err)
		return
	}
	value, err := s.list(r.Context(), filters)
	respond(w, value, err)
}

// handleSummary 返回预警中心及工作台可以使用的实际计数。
// 参数 w 为响应写入端，r 为无查询条件的汇总请求。
// 返回值 无。
func (s *Service) handleSummary(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respond(w, nil, errInvalid)
		return
	}
	value, err := ReadSummary(r.Context(), s.db)
	respond(w, value, err)
}

// handleSync 同步已经登记的本地隐患，不接受上传或额外参数。
// 参数 w 为响应写入端，r 为同步请求。
// 返回值 无，通过响应报告真实新增数量。
func (s *Service) handleSync(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respond(w, nil, errInvalid)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
	if err != nil || len(body) != 0 {
		respond(w, nil, errInvalid)
		return
	}
	value, err := s.sync(r.Context())
	respond(w, value, err)
}

// handleDetail 返回当前预警、来源隐患及历史处置记录。
// 参数 w 为响应写入端，r 为带预警标识的详情请求。
// 返回值 无。
func (s *Service) handleDetail(w http.ResponseWriter, r *http.Request) {
	id, valid := parseAlertID(r.PathValue("id"))
	if !valid {
		respond(w, nil, pgx.ErrNoRows)
		return
	}
	if r.URL.RawQuery != "" {
		respond(w, nil, errInvalid)
		return
	}
	value, err := s.detail(r.Context(), id)
	respond(w, value, err)
}

// handleAction 保存指定预警的正常处置、运营者申请或审批决定。
// 参数 w 为响应写入端，r 为含标识、业务版本和处置备注的请求。
// 返回值 无。
func (s *Service) handleAction(w http.ResponseWriter, r *http.Request) {
	id, valid := parseAlertID(r.PathValue("id"))
	if !valid {
		respond(w, nil, pgx.ErrNoRows)
		return
	}
	var input actionInput
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil ||
		!validateAction(&input, currentAlertScope(r.Context()).audience) {
		respond(w, nil, errInvalid)
		return
	}
	value, err := s.execute(r.Context(), id, input)
	respond(w, value, err)
}

// respond 将预警操作结果或安全的业务错误写入响应。
// 参数 w 为响应写入端，value 为业务响应，err 为查询或办理错误。
// 返回值 无。
func respond(w http.ResponseWriter, value any, err error) {
	var approvalBlocked approvalBlockedError
	switch {
	case err == nil:
		httpx.JSON(w, 200, value)
	case errors.Is(err, errInvalid):
		httpx.Error(w, 400, "invalid_input", "请检查预警筛选、版本或处置说明。")
	case errors.Is(err, errDeadline):
		httpx.Error(w, 400, "invalid_deadline", "申请期限必须晚于当前整改期限及上海业务日。")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, 404, "not_found", "预警不存在。")
	case errors.As(err, &approvalBlocked):
		httpx.Error(w, 409, "conflict", approvalBlocked.Error())
	case errors.Is(err, errConflict):
		httpx.Error(w, 409, "conflict", "预警状态已变化，请重新加载后办理。")
	default:
		slog.Error("本地预警处理失败", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, 500, "internal_error", "预警服务暂时无法完成请求，请稍后重试。")
	}
}
