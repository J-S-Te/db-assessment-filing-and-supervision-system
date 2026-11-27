package remediations

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"grade-business/internal/cases"
	"grade-business/internal/httpx"

	"github.com/jackc/pgx/v5/pgxpool"
)

const route = "/api/v1/remediations"

// Service 持有整改规划数据库和经过原服务保护的附件能力。
type Service struct {
	db       *pgxpool.Pool
	records  *cases.Service
	operator string
}

// New 建立负责整改规划与附件的业务服务。
// 参数 pool 为已初始化业务连接池，records 为已初始化的案件服务，operator 为审核留痕席位。
// 返回值 使用同一数据源与文件安全规则的整改服务。
func New(pool *pgxpool.Pool, records *cases.Service, operator string) *Service {
	return &Service{db: pool, records: records, operator: operator}
}

// Register 注册整改规划、催办、台账导出、受控材料和填报模板的 HTTP 方法。
// 参数 mux 为使用标准库并受应用入口保护的路由。
// 返回值 无。
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET "+route, s.handleList)
	mux.HandleFunc("POST "+route, s.handlePlan)
	mux.HandleFunc("GET "+route+"/eligible-reports", s.handleList)
	mux.HandleFunc("GET "+route+"/eligible-reports/{id}/material-templates/{kind}",
		withID(s.handleMaterialTemplate(true)))
	mux.HandleFunc("GET "+route+"/statistics", s.handleStatistics)
	mux.HandleFunc("GET "+route+"/export", s.handleExport)
	mux.HandleFunc("POST "+route+"/reminders", s.handleReminders)
	mux.HandleFunc("GET "+route+"/{id}", withID(s.handleDetail))
	mux.HandleFunc("PATCH "+route+"/{id}", withID(s.handlePlan))
	mux.HandleFunc("POST "+route+"/{id}/submit", withID(s.handleSubmit))
	mux.HandleFunc("POST "+route+"/{id}/review-actions", withID(s.handleReview))
	mux.HandleFunc("GET "+route+"/{id}/review-history", withID(s.handleHistory))
	mux.HandleFunc("GET "+route+"/{id}/material-templates/{kind}", withID(s.handleMaterialTemplate(false)))
	mux.HandleFunc("GET "+route+"/{id}/attachments", withID(s.handleAttachments))
	mux.HandleFunc("POST "+route+"/{id}/attachments", withID(s.handleUpload))
	mux.HandleFunc("GET "+route+"/{id}/attachments/{attachmentId}", withID(s.handleDownload))
}

// withID 拦截无效 UUID，避免将不合法标识传递到业务存储。
// 参数 next 为需要已验证规划标识的处理器。
// 返回值 先校验标识的 HTTP 处理器。
func withID(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validID(r.PathValue("id")) {
			respondError(w, errNotFound)
			return
		}
		next(w, r)
	}
}

// handleList 读取待审核规划或可选择报告列表。
// 参数 w 为响应对象，r 携带查询关键词、状态、期限及分页条件。
// 返回值 无。
func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	eligibleOnly := r.URL.Path == route+"/eligible-reports" || r.URL.Path == operatorRoot+"/eligible-reports"
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		respondError(w, errInvalid)
		return
	}
	input, err := parseList(values, eligibleOnly)
	if err != nil {
		respondError(w, err)
		return
	}
	if eligibleOnly {
		out, err := s.eligible(r.Context(), input)
		respond(w, http.StatusOK, out, err)
		return
	}
	out, err := s.list(r.Context(), input)
	respond(w, http.StatusOK, out, err)
}

// handleStatistics 返回持久化记录的即时整改统计。
// 参数 w 和 r 为业务 HTTP 响应对象和请求。
// 返回值 无。
func (s *Service) handleStatistics(w http.ResponseWriter, r *http.Request) {
	out, err := s.stats(r.Context())
	respond(w, http.StatusOK, out, err)
}

// handleDetail 返回单条整改方案和动作状态。
// 参数 w 为响应对象，r 携带整改方案标识。
// 返回值 无。
func (s *Service) handleDetail(w http.ResponseWriter, r *http.Request) {
	out, err := s.get(r.Context(), r.PathValue("id"))
	respond(w, http.StatusOK, out, err)
}

// handlePlan 新建或编辑一份经过字段及内容校验的整改规划。
// 参数 w 为响应对象，r 携带规划字段，更新时增加原版本。
// 返回值 无。
func (s *Service) handlePlan(w http.ResponseWriter, r *http.Request) {
	var input planInput
	if httpx.Decode(w, r, &input) != nil || validatePlan(&input) != nil {
		respondError(w, errInvalid)
		return
	}
	if r.Method == http.MethodPost {
		if input.ExpectedVersion != 0 {
			respondError(w, errInvalid)
			return
		}
		out, err := s.create(r.Context(), input)
		respond(w, http.StatusCreated, out, err)
		return
	}
	if input.ExpectedVersion < 1 {
		respondError(w, errInvalid)
		return
	}
	out, err := s.edit(r.Context(), r.PathValue("id"), input)
	respond(w, http.StatusOK, out, err)
}

// handleSubmit 在必要材料齐全后提交待审核。
// 参数 w 为响应对象，r 包含整改规划标识及预期版本。
// 返回值 无。
func (s *Service) handleSubmit(w http.ResponseWriter, r *http.Request) {
	var input submitInput
	if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
		respondError(w, errInvalid)
		return
	}
	out, err := s.submit(r.Context(), r.PathValue("id"), input)
	respond(w, http.StatusOK, out, err)
}

// handleReview 提交带整改期限和意见的审核结果。
// 参数 w 为响应对象，r 带有审核结果和止期。
// 返回值 无。
func (s *Service) handleReview(w http.ResponseWriter, r *http.Request) {
	var input reviewInput
	if httpx.Decode(w, r, &input) != nil || validateReview(&input) != nil {
		respondError(w, errInvalid)
		return
	}
	out, err := s.review(r.Context(), r.PathValue("id"), input)
	respond(w, http.StatusOK, out, err)
}

// handleHistory 返回整改提交与审核记录。
// 参数 w 为响应对象，r 携带计划标识。
// 返回值 无。
func (s *Service) handleHistory(w http.ResponseWriter, r *http.Request) {
	out, err := s.history(r.Context(), r.PathValue("id"))
	respond(w, http.StatusOK, out, err)
}

// handleAttachments 读取该整改方案拥有的材料列表。
// 参数 w 为响应对象，r 携带整改方案标识。
// 返回值 无。
func (s *Service) handleAttachments(w http.ResponseWriter, r *http.Request) {
	out, err := s.attachments(r.Context(), r.PathValue("id"))
	respond(w, http.StatusOK, out, err)
}

// handleUpload 接收一个经过附件安全校验的整改材料。
// 参数 w 和 r 为响应与 multipart 请求，含材料类别、页面所见的报告标识和可选企业范围。
// 返回值 无。
func (s *Service) handleUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.canStageMaterial(r.Context(), id); err != nil {
		respondError(w, err)
		return
	}
	item, key, report, err := s.records.StageBusinessAttachmentForReport(w, r, materialKind, validID)
	if err != nil {
		respondError(w, err)
		return
	}
	out, err := s.addMaterial(r.Context(), id, report, item, key)
	if err != nil {
		if cleanup := s.discardMaterial(r.Context(), key); cleanup != nil {
			slog.Warn("未登记整改材料待清理", "error_type", fmt.Sprintf("%T", cleanup))
		}
	}
	respond(w, http.StatusCreated, out, err)
}

// handleDownload 向指定整改方案发起材料下载。
// 参数 w 为文件响应对象，r 携带方案和材料标识。
// 返回值 无。
func (s *Service) handleDownload(w http.ResponseWriter, r *http.Request) {
	attachmentID := r.PathValue("attachmentId")
	if !validID(attachmentID) {
		respondError(w, errNotFound)
		return
	}
	if err := s.downloadMaterial(w, r, r.PathValue("id"), attachmentID); err != nil {
		respondError(w, err)
	}
}

// respond 输出已声明的最小业务响应，或通用业务错误。
// 参数 w 为响应对象，status 为成功状态，value 为公开响应结构，err 为业务写入结果。
// 返回值 无。
func respond(w http.ResponseWriter, status int, value any, err error) {
	if err != nil {
		respondError(w, err)
		return
	}
	httpx.JSON(w, status, value)
}

// respondError 将整改错误映射为不披露内网或存储路径的公开响应。
// 参数 w 为响应对象，err 为业务操作错误。
// 返回值 无。
func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errInvalid), errors.Is(err, cases.ErrInvalid):
		httpx.Error(w, http.StatusBadRequest, "invalid_input", "请核对必填字段、风险措施及整改期限。")
	case errors.Is(err, errReport):
		httpx.Error(w, http.StatusBadRequest, "report_not_eligible", "请选择有效备案下已通过的测评报告。")
	case errors.Is(err, errLatestReport):
		httpx.Error(w, http.StatusConflict, "report_not_latest", "备案已有更新的通过报告，请选择最新测评报告编制整改规划。")
	case errors.Is(err, errFilingInReview):
		httpx.Error(w, http.StatusConflict, "filing_in_review", "该备案已有待审核的整改规划，请在其退出审核流程后再提交。")
	case errors.Is(err, errRiskDueDate):
		httpx.Error(w, http.StatusBadRequest, "invalid_deadline", "总体整改期限不能早于任一风险项期限。")
	case errors.Is(err, errMaterials):
		httpx.Error(w, http.StatusBadRequest, "materials_required", "请先上传整改规划方案、责任分工和实施计划。")
	case errors.Is(err, cases.ErrFileType):
		httpx.Error(w, http.StatusBadRequest, "invalid_file_type", "请检查文件名和 PDF、DOCX、XLSX 或 ZIP 文件格式。")
	case errors.Is(err, cases.ErrFileSize):
		httpx.Error(w, http.StatusBadRequest, "invalid_file_size", "请检查文件是否为空或超过允许的大小。")
	case errors.Is(err, errNotFound), errors.Is(err, cases.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "整改规划或所属材料不存在。")
	case errors.Is(err, errReportChange):
		httpx.Error(w, http.StatusConflict, "report_change_blocked", "整改规划已有材料或提交记录，无法更换关联报告。")
	case errors.Is(err, errMaterialReportChanged):
		httpx.Error(w, http.StatusConflict, "report_changed", "关联测评报告已变更，请刷新规划并重新选择材料。")
	case errors.Is(err, errConflict):
		httpx.Error(w, http.StatusConflict, "conflict", "整改规划状态或版本已变化，请刷新后重试。")
	default:
		slog.Error("整改规划操作未完成", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "整改规划操作暂时无法完成。")
	}
}
