package audittrail

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/httpx"
)

// Service 汇总已持久化的本地业务操作留痕。
type Service struct {
	db *pgxpool.Pool
}

// New 创建只读操作审计服务。
// 参数 pool 为业务 PostgreSQL 连接池。
// 返回值 可注册本地审计路由的服务。
func New(pool *pgxpool.Pool) *Service {
	return &Service{db: pool}
}

// Register 挂载监管端业务操作审计。
// 参数 mux 为应用 HTTP 路由。
// 返回值 无。
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/audit", s.handleList)
	mux.HandleFunc("GET /api/v1/audit/export", s.handleExport)
}

// handleList 验证输入并按已持久化的操作时间返回有界结果。
// 参数 w 为响应写入端，r 为审计列表请求。
// 返回值 无。
func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	filters, err := parseFilter(r.URL.RawQuery, true)
	if err != nil {
		httpx.Error(w, 400, "invalid_input", "请核对业务类型、关键词和分页条件。")
		return
	}
	result, err := s.list(r.Context(), filters)
	if err != nil {
		slog.Error("业务操作审计读取失败", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, 500, "internal_error", "暂时无法读取业务操作审计，请稍后重试。")
		return
	}
	httpx.JSON(w, 200, result)
}
