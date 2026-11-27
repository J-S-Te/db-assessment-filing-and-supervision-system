package reporthistory

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/cases"
	"grade-business/internal/httpx"
)

var errInput = errors.New("invalid report history filters")
var errOrder = errors.New("reversed report comparison order")

// Service 查询同一备案的历史测评报告并维护已核验的测评结果。
type Service struct {
	db       *pgxpool.Pool
	operator string
	files    *cases.Service
}

type historyCursor struct {
	time time.Time
	id   pgtype.UUID
}

type archiveFilters struct {
	cursor historyCursor
	limit  int
}

// New 创建测评归档、结果核验和对比服务。
// 参数 pool 为业务 PostgreSQL 连接池，operators 可提供当前监管经办人名称。
// 返回值 可登记路由的测评对比服务。
func New(pool *pgxpool.Pool, operators ...string) *Service {
	operator := "本地监管"
	if len(operators) > 0 && strings.TrimSpace(operators[0]) != "" {
		operator = strings.TrimSpace(operators[0])
	}
	return &Service{db: pool, operator: operator}
}

// NewWithAttachments 创建可查阅和受控下载终审测评材料的服务。
// 参数 pool 为业务库，files 为备案和测评共享文件存储，operator 为监管经办人。
// 返回值 可挂载运营者归档下载及原有历史对比路由的服务。
func NewWithAttachments(pool *pgxpool.Pool, files *cases.Service, operator string) *Service {
	service := New(pool, operator)
	service.files = files
	return service
}

// Register 登记管理端报告归档、结果核验和运营者查询接口。
// 参数 mux 为应用路由表。
// 返回值 无。
func (s *Service) Register(mux *http.ServeMux) {
	const base = "/api/v1/reports/{id}"
	mux.HandleFunc("GET "+base+"/history-reports", s.archiveHTTP)
	mux.HandleFunc("GET "+base+"/history-comparison", s.compareHTTP)
	s.registerAssessmentResults(mux, base)
	s.registerOperatorMaterials(mux)
}

// archiveHTTP 查询当前报告对应备案中已通过的测评归档。
// 参数 w 为响应写入端，r 携带用于确定归属的报告标识及游标。
// 返回值 无，报告列表通过响应返回。
func (s *Service) archiveHTTP(w http.ResponseWriter, r *http.Request) {
	anchor, ok := parseHistoryUUID(r.PathValue("id"))
	if !ok {
		respondHistory(w, nil, pgx.ErrNoRows)
		return
	}
	filters, err := parseArchiveFilters(r.URL.RawQuery)
	if err != nil {
		respondHistory(w, nil, err)
		return
	}
	result, err := s.archive(r.Context(), anchor, filters)
	respondHistory(w, result, err)
}

// compareHTTP 对比由当前备案约束的两个已通过测评报告。
// 参数 w 为响应写入端，r 携带归属报告、前期报告和后期报告标识。
// 返回值 无，返回报告元数据和可追溯字段差异。
func (s *Service) compareHTTP(w http.ResponseWriter, r *http.Request) {
	anchor, ok := parseHistoryUUID(r.PathValue("id"))
	if !ok {
		respondHistory(w, nil, pgx.ErrNoRows)
		return
	}
	from, to, err := pairIDs(r.URL.RawQuery)
	if err != nil {
		respondHistory(w, nil, err)
		return
	}
	result, err := s.compare(r.Context(), anchor, from, to)
	respondHistory(w, result, err)
}

// parseArchiveFilters 校验历史归档支持的分页参数。
// 参数 raw 为 URL 中的原始查询字符串。
// 返回值 页大小、稳定游标及可能的输入错误。
func parseArchiveFilters(raw string) (archiveFilters, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return archiveFilters{}, errInput
	}
	for key, entries := range values {
		if len(entries) != 1 || (key != "limit" && key != "cursor") {
			return archiveFilters{}, errInput
		}
	}
	filters := archiveFilters{limit: 20}
	if entries, ok := values["limit"]; ok {
		limit, err := strconv.Atoi(entries[0])
		if err != nil || limit < 1 || limit > 100 {
			return archiveFilters{}, errInput
		}
		filters.limit = limit
	}
	filters.cursor, err = decodeHistoryCursor(values.Get("cursor"))
	return filters, err
}

// pairIDs 验证两份待比较报告的必填标识。
// 参数 raw 为只允许包含 from 和 to 的原始查询字符串。
// 返回值 前后期报告的 UUID 或输入错误。
func pairIDs(raw string) (pgtype.UUID, pgtype.UUID, error) {
	values, err := url.ParseQuery(raw)
	if err != nil || len(values) != 2 || len(values["from"]) != 1 || len(values["to"]) != 1 {
		return pgtype.UUID{}, pgtype.UUID{}, errInput
	}
	from, fromValid := parseHistoryUUID(values.Get("from"))
	to, toValid := parseHistoryUUID(values.Get("to"))
	if !fromValid || !toValid || from == to {
		return pgtype.UUID{}, pgtype.UUID{}, errInput
	}
	return from, to, nil
}

// parseHistoryUUID 校验 URL 公开标识的 UUID 格式。
// 参数 raw 为客户端提供的报告标识。
// 返回值 数据库 UUID 以及其是否有效。
func parseHistoryUUID(raw string) (pgtype.UUID, bool) {
	var id pgtype.UUID
	if len(raw) != 36 || raw[8] != '-' || raw[13] != '-' || raw[18] != '-' || raw[23] != '-' {
		return id, false
	}
	err := id.Scan(raw)
	return id, err == nil && id.Valid
}

// decodeHistoryCursor 读取最近一次分页尾条的提交排序点。
// 参数 raw 为已编码的时间及报告标识，空值表示首页。
// 返回值 排序位置或分页校验错误。
func decodeHistoryCursor(raw string) (historyCursor, error) {
	if raw == "" {
		return historyCursor{}, nil
	}
	if len(raw) > 110 {
		return historyCursor{}, errInput
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return historyCursor{}, errInput
	}
	timestamp, identifier, found := strings.Cut(string(payload), ",")
	at, dateErr := time.Parse(time.RFC3339Nano, timestamp)
	id, valid := parseHistoryUUID(identifier)
	if !found || dateErr != nil || at.IsZero() || !valid {
		return historyCursor{}, errInput
	}
	return historyCursor{time: at, id: id}, nil
}

// encodeHistoryCursor 生成对应归档排序的后续页游标。
// 参数 item 为已返回给客户端的末条归档报告。
// 返回值 可用于下一页请求的 URL 安全游标。
func encodeHistoryCursor(item historyReport) string {
	at := item.CreatedAt
	if item.SubmittedAt != nil {
		at = *item.SubmittedAt
	}
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "," + item.ID))
}

// respondHistory 将只读查询结果或安全业务错误返回给监管页面。
// 参数 w 为响应写入端，result 为查询结果，err 为查询错误。
// 返回值 无。
func respondHistory(w http.ResponseWriter, result any, err error) {
	switch {
	case err == nil:
		httpx.JSON(w, 200, result)
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, 404, "not_found", "未找到同一备案下可查看的测评报告。")
	case errors.Is(err, errInput):
		httpx.Error(w, 400, "invalid_input", "请检查分页条件，或选择两份不同的前后期报告。")
	case errors.Is(err, errOrder):
		httpx.Error(w, 400, "invalid_input", "请选择时间顺序正确的前期、后期报告。")
	default:
		slog.Error("读取测评历史对比失败", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, 500, "internal_error", "历史报告暂时无法读取，请稍后重试。")
	}
}
