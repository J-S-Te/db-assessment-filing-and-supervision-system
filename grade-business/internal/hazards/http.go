package hazards

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"grade-business/internal/access"
	"grade-business/internal/cases"
	"grade-business/internal/httpx"
)

const ledgerRoute = "/api/v1/hazards"

// Register 挂载隐患台账、同步、整改办理和证据的管理端接口。
// 参数 mux 为标准库业务 API 路由器。
// 返回值 无。
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET "+ledgerRoute, s.handleList)
	mux.HandleFunc("POST "+ledgerRoute, s.handleRegister)
	mux.HandleFunc("GET "+ledgerRoute+"/filing-options", s.handleFilingOptions)
	mux.HandleFunc("GET "+ledgerRoute+"/export", s.handleExport)
	mux.HandleFunc("GET "+ledgerRoute+"/{id}", s.handleDetail)
	mux.HandleFunc("GET "+ledgerRoute+"/{id}/attachments", s.handleAttachments)
	mux.HandleFunc("GET "+ledgerRoute+"/{id}/attachments/{attachmentId}", s.handleDownload)
	mux.HandleFunc("GET "+ledgerRoute+"/{id}/history", s.handleHistory)
	// 固定同步前缀与条目操作后缀在 ServeMux 同层互斥，统一解析两个业务段。
	mux.HandleFunc("POST "+ledgerRoute+"/{target}/{operation}", s.handleWrite)
}

// handleList 读取已校验的筛选与分页参数并返回公开台账摘要。
// 参数 w 为响应对象，r 为列表读取请求。
// 返回值 无。
func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		respondError(w, errInvalid)
		return
	}
	filter, err := decodeFilter(query)
	if err != nil {
		respondError(w, err)
		return
	}
	value, err := s.list(r.Context(), filter)
	respond(w, http.StatusOK, value, err)
}

// handleDetail 返回隐患的来源及当前业务详情。
// 参数 w 为响应对象，r 为携带隐患标识的读取请求。
// 返回值 无。
func (s *Service) handleDetail(w http.ResponseWriter, r *http.Request) {
	value, err := s.detail(r.Context(), r.PathValue("id"))
	respond(w, http.StatusOK, value, err)
}

// handleWrite 分派手动同步、整改办理和证据上传的无歧义路由。
// 参数 w 为响应对象，r 为含两个写入路径段的请求。
// 返回值 无。
func (s *Service) handleWrite(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respondError(w, errInvalid)
		return
	}
	target, operation := r.PathValue("target"), r.PathValue("operation")
	if actor, _ := access.Current(r.Context()); actor.Role == access.Regulator &&
		(target == "sync" || operation == "attachments") {
		httpx.Error(w, http.StatusForbidden, "permission_denied", "当前账号无权执行此操作。")
		return
	}
	if target == "sync" {
		s.handleSync(w, r, operation)
		return
	}
	if !validID(target) {
		respondError(w, errNotFound)
		return
	}
	switch operation {
	case "actions":
		s.handleAction(w, r, target)
	case "attachments":
		s.handleUpload(w, r, target)
	default:
		respondError(w, errNotFound)
	}
}

// handleSync 同步历史已批准规划，仅接受 URL 标识和空请求体。
// 参数 w 为响应对象，r 为同步请求，id 为整改规划标识。
// 返回值 无。
func (s *Service) handleSync(w http.ResponseWriter, r *http.Request, id string) {
	if !validID(id) {
		respondError(w, errNotFound)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
	if err != nil || len(body) != 0 {
		respondError(w, errInvalid)
		return
	}
	count, err := s.syncPlan(r.Context(), id)
	respond(w, http.StatusOK, struct {
		Count int `json:"count"`
	}{Count: count}, err)
}

// handleAction 读取严格定义的整改提交、退回或销号请求。
// 参数 w 与 r 为响应和请求对象，id 为已验证的隐患标识。
// 返回值 无。
func (s *Service) handleAction(w http.ResponseWriter, r *http.Request, id string) {
	var input actionInput
	if httpx.Decode(w, r, &input) != nil || validateAction(&input) != nil ||
		(hazardInstitution(r.Context()) != "" && !actionWithinScope(r.Context(), input.Action)) {
		respondError(w, errInvalid)
		return
	}
	value, err := s.act(r.Context(), id, input)
	respond(w, http.StatusOK, value, err)
}

// handleAttachments 返回属于指定台账条目的证据列表。
// 参数 w 为响应对象，r 为取证据元数据的请求。
// 返回值 无。
func (s *Service) handleAttachments(w http.ResponseWriter, r *http.Request) {
	items, err := s.attachments(r.Context(), r.PathValue("id"))
	respond(w, http.StatusOK, hazardCollection[cases.Attachment]{Items: items}, err)
}

// handleUpload 核验版本后暂存材料，在登记失败且无数据库引用时清理文件。
// 参数 w 与 r 为响应和上传请求对象，id 为隐患标识。
// 返回值 无。
func (s *Service) handleUpload(w http.ResponseWriter, r *http.Request, id string) {
	expected, err := expectedUploadVersion(r)
	if err != nil {
		respondError(w, err)
		return
	}
	version, err := s.evidenceUploadVersion(r.Context(), id, expected)
	if err != nil {
		respondError(w, err)
		return
	}
	item, key, err := s.records.StageBusinessAttachment(w, r, func(kind string) bool {
		return validHazardMaterial(r.Context(), kind)
	})
	if err != nil {
		respondError(w, err)
		return
	}
	item, err = s.addEvidence(r.Context(), id, item, key, version)
	if err != nil {
		if cleanupErr := s.discardEvidence(r.Context(), key); cleanupErr != nil {
			slog.Error("隐患证据清理失败", "error_type", fmt.Sprintf("%T", cleanupErr))
		}
	}
	respond(w, http.StatusCreated, item, err)
}

// expectedUploadVersion 解析单个强版本条件，监管旧客户端可以省略该请求头。
// 参数 r 是带已校验办理端范围的上传请求。
// 返回值 正整数版本、监管未指定时的零，或无效请求错误。
func expectedUploadVersion(r *http.Request) (int64, error) {
	values := r.Header.Values("If-Match")
	if len(values) == 0 && operatorOrganization(r.Context()) == "" && hazardInstitution(r.Context()) == "" {
		return 0, nil
	}
	if len(values) != 1 {
		return 0, errInvalid
	}
	value := strings.Trim(values[0], " \t")
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' {
		return 0, errInvalid
	}
	digits := value[1 : len(value)-1]
	version, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || version <= 0 || strconv.FormatInt(version, 10) != digits {
		return 0, errInvalid
	}
	return version, nil
}

// handleDownload 验证附件归属后提供受控证据下载。
// 参数 w 为响应对象，r 为含条目和证据标识的下载请求。
// 返回值 无。
func (s *Service) handleDownload(w http.ResponseWriter, r *http.Request) {
	err := s.downloadEvidence(w, r, r.PathValue("id"), r.PathValue("attachmentId"))
	if err != nil {
		respondError(w, err)
	}
}

// handleHistory 返回持久化的台账办理轨迹。
// 参数 w 为响应对象，r 为含条目标识的读取请求。
// 返回值 无。
func (s *Service) handleHistory(w http.ResponseWriter, r *http.Request) {
	items, err := s.history(r.Context(), r.PathValue("id"))
	respond(w, http.StatusOK, hazardCollection[hazardEvent]{Items: items}, err)
}

// respond 写出声明的成功响应或转换隐患业务错误。
// 参数 w 为响应对象，status 为成功状态，value 为公开响应，err 为流程结果。
// 返回值 无。
func respond(w http.ResponseWriter, status int, value any, err error) {
	if err != nil {
		respondError(w, err)
		return
	}
	httpx.JSON(w, status, value)
}

// respondError 将业务错误映射为稳定、无内部细节的 HTTP 提示。
// 参数 w 为响应对象，err 为服务返回的实际错误。
// 返回值 无。
func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotFound), errors.Is(err, cases.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "未找到对应的隐患、整改规划或证据。")
	case errors.Is(err, errDuplicateRegistration):
		httpx.Error(w, http.StatusConflict, "duplicate_registration", "该系统已有相同名称的在办事项或同日登记。")
	case errors.Is(err, errRegistrationFiling):
		httpx.Error(w, http.StatusConflict, "filing_ineligible", "请选择登记了运营单位及测评机构的有效系统备案。")
	case errors.Is(err, errAlreadySupervised):
		httpx.Error(w, http.StatusConflict, "already_supervised", "该重大风险今日已督办。")
	case errors.Is(err, errConflict):
		httpx.Error(w, http.StatusConflict, "conflict", "隐患状态或版本已变化，请刷新后核对。")
	case errors.Is(err, errPlanNotApproved):
		httpx.Error(w, http.StatusConflict, "plan_not_approved", "只有已批准的整改规划可以同步台账。")
	case errors.Is(err, errEvidence):
		httpx.Error(w, http.StatusBadRequest, "evidence_required", "请上传本轮整改证据后提交。")
	case errors.Is(err, errRetestReport):
		httpx.Error(w, http.StatusBadRequest, "retest_report_required", "请上传本轮复测报告后确认通过。")
	case errors.Is(err, cases.ErrFileSize):
		httpx.Error(w, http.StatusRequestEntityTooLarge, "attachment_size", "文件为空或超过允许大小。")
	case errors.Is(err, cases.ErrFileType):
		httpx.Error(w, http.StatusBadRequest, "attachment_type", "请上传有效的 PDF、DOCX、XLSX 或 ZIP 文件。")
	case errors.Is(err, errInvalid), errors.Is(err, cases.ErrInvalid):
		httpx.Error(w, http.StatusBadRequest, "invalid_input", "请检查台账字段、办理说明或操作参数。")
	default:
		slog.Error("隐患台账处理失败", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "隐患台账处理失败，请稍后重试。")
	}
}
