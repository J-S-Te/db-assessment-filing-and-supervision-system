package compliance

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/httpx"
)

// Service 管理监管员登记的测评合规依据与历史。
type Service struct {
	db       *pgxpool.Pool
	operator string
}

// New 创建使用现有业务数据库的合规依据留痕服务。
// 参数 pool 为业务连接池，operator 为经办席位。
// 返回值 可注册 HTTP 路由的服务。
func New(pool *pgxpool.Pool, operator string) *Service {
	return &Service{db: pool, operator: operator}
}

// Register 挂载测评合规记录的查询、候选查看与登记接口。
// 参数 mux 为监管业务路由。
// 返回值 无。
func (s *Service) Register(mux *http.ServeMux) {
	const path = "/api/v1/reports/{id}/compliance"
	mux.HandleFunc("GET "+path, s.handleList)
	mux.HandleFunc("POST "+path, s.handleRecord)
	mux.HandleFunc("GET "+path+"/candidates", s.handleCandidates)
	mux.HandleFunc("GET "+path+"/clause-candidates", s.handleClauseCandidates)
	const catalog = "/api/v1/knowledge/{id}/clauses"
	mux.HandleFunc("GET "+catalog, s.handleClauses)
	mux.HandleFunc("POST "+catalog, s.handleSaveClause)
}

// handleList 返回按最近登记顺序排列的合规依据留痕。
// 参数 w 为响应对象，r 携带报告标识及可选的历史分页游标。
// 返回值 无。
func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(r.PathValue("id"))
	if !ok {
		respondError(w, errMissing)
		return
	}
	before, err := parseBefore(r.URL.RawQuery)
	if err != nil {
		respondError(w, err)
		return
	}
	page, err := s.list(r.Context(), id, before)
	if err != nil {
		respondError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, page)
}

// handleRecord 保存监管员选择的合规判定与来源，保持报告审核流程独立。
// 参数 w 为响应对象，r 携带报告标识及依据登记内容。
// 返回值 无。
func (s *Service) handleRecord(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(r.PathValue("id"))
	if !ok {
		respondError(w, errMissing)
		return
	}
	var input recordInput
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil || !validInput(&input) {
		respondError(w, errInvalid)
		return
	}
	item, err := s.record(r.Context(), id, input)
	if err != nil {
		respondError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, item)
}

// parseUUID 核对公开标识格式并规范其大小写供查询和响应复用。
// 参数 raw 为外部提供的报告、附件或知识条目标识。
// 返回值 规范 UUID 和有效标志。
func parseUUID(raw string) (string, bool) {
	var id pgtype.UUID
	if len(raw) != 36 || id.Scan(raw) != nil || !id.Valid || !strings.EqualFold(raw, id.String()) {
		return "", false
	}
	return id.String(), true
}

// parseBefore 解析唯一的正整数历史游标，未提供时返回首页位置。
// 参数 raw 为原始查询字符串。
// 返回值 游标或查询校验错误。
func parseBefore(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	values, err := url.ParseQuery(raw)
	if err != nil || len(values) != 1 || len(values["before"]) != 1 {
		return 0, errInvalid
	}
	text := values.Get("before")
	before, err := strconv.ParseInt(text, 10, 64)
	if err != nil || before < 1 || strconv.FormatInt(before, 10) != text {
		return 0, errInvalid
	}
	return before, nil
}

// validInput 核对选定的报告与知识版本、证据字段和人工判定说明。
// 参数 input 为待规范化的登记表单。
// 返回值 内容符合接口约束时为真。
func validInput(input *recordInput) bool {
	attachment, attachmentOK := parseUUID(input.AttachmentID)
	knowledge, knowledgeOK := parseUUID(input.KnowledgeID)
	input.AttachmentID, input.KnowledgeID = attachment, knowledge
	input.Comment = strings.TrimSpace(input.Comment)
	if !attachmentOK || !knowledgeOK || input.ReportVersion < 1 || input.ExtractionVersion < 1 ||
		input.KnowledgeVersion < 1 || input.Comment == "" || !utf8.ValidString(input.Comment) ||
		utf8.RuneCountInString(input.Comment) > 1000 || strings.ContainsRune(input.Comment, 0) {
		return false
	}
	if input.RequestID != "" {
		request, ok := parseUUID(input.RequestID)
		if !ok {
			return false
		}
		input.RequestID = request
	}
	if input.ClauseNumber != "" || input.ClauseVersion != 0 {
		if input.RequestID == "" || !validClauseNumber(input.ClauseNumber) || input.ClauseVersion < 1 {
			return false
		}
	}
	if !validFieldKey(input.FieldKey) {
		return false
	}
	switch input.Decision {
	case "compliant", "needs_remediation", "insufficient_evidence":
		return true
	default:
		return false
	}
}

// validFieldKey 校验合规查询和登记共享的证据栏目代码。
// 参数 key 为报告提取栏目代码。
// 返回值 栏目已受支持时为真。
func validFieldKey(key string) bool {
	switch key {
	case "assets", "findings", "risk", "recommendations":
		return true
	default:
		return false
	}
}

// respondError 将留痕服务错误映射为安全的业务提示。
// 参数 w 为响应对象，err 为当前请求的业务或存储错误。
// 返回值 无。
func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errMissing):
		httpx.Error(w, http.StatusNotFound, "not_found", "测评报告不存在。")
	case errors.Is(err, errInvalid):
		httpx.Error(w, http.StatusBadRequest, "invalid_input", "请核对证据、依据、判定和说明。")
	case errors.Is(err, errState):
		httpx.Error(w, http.StatusConflict, "version_conflict", "报告版本或审核状态已变化，请刷新后核对。")
	case errors.Is(err, errRequestConflict):
		httpx.Error(w, http.StatusConflict, "request_conflict", "此提交标识已用于不同的登记内容，请核对后重新提交。")
	case errors.Is(err, errSource):
		httpx.Error(w, http.StatusConflict, "source_conflict", "校对证据、知识或条款版本不可用，请刷新来源后重新选择。")
	default:
		slog.Error("合规依据登记或读取失败", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "暂时无法处理合规依据。")
	}
}
