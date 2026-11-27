package cases

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"grade-business/internal/classification"
	"grade-business/internal/directoryref"
	"grade-business/internal/filingpolicy"
	"grade-business/internal/httpx"
)

type endpoint struct {
	service *Service
	kind    string
}

// Register 挂载备案、生命周期申请、测评及本地机构回传接口。
// 参数 mux 为管理端使用的 HTTP 路由器。
// 返回值 无。
func (s *Service) Register(mux *http.ServeMux) {
	for _, resource := range []struct{ path, kind string }{
		{"/api/v1/filings", filing}, {"/api/v1/reports", report},
	} {
		h := endpoint{service: s, kind: resource.kind}
		path := resource.path
		mux.HandleFunc("GET "+path, h.list)
		mux.HandleFunc("POST "+path, h.create)
		mux.HandleFunc("GET "+path+"/{id}", h.detail)
		mux.HandleFunc("PATCH "+path+"/{id}", h.update)
		mux.HandleFunc("GET "+path+"/{id}/attachments", h.listAttachments)
		mux.HandleFunc("POST "+path+"/{id}/attachments", h.upload)
		mux.HandleFunc("GET "+path+"/{id}/attachments/{attachmentId}", h.download)
		if resource.kind == filing {
			mux.HandleFunc("GET "+path+"/{id}/attachments/{attachmentId}/text", h.filingMaterialText)
		}
		mux.HandleFunc("POST "+path+"/{id}/submit", h.submit)
		mux.HandleFunc("POST "+path+"/{id}/review-actions", h.review)
		mux.HandleFunc("GET "+path+"/{id}/review-history", h.history)
	}
	s.registerApplications(mux)
	s.registerReportFeedback(mux)
	s.registerInstitutionFilings(mux)
	s.registerInstitutionCollections(mux)
	s.registerInstitutionBulk(mux)
	s.registerMaterialIssues(mux)
	s.registerFilingHistoryImport(mux)
	s.registerWithdrawals(mux)
	s.registerFilingMaterialPolicy(mux)
	s.registerMaterialReviews(mux)
	mux.HandleFunc("GET /api/v1/reports/export", s.downloadReports)
	mux.HandleFunc("POST /api/v1/filings/classification-preview", previewClassification)
}

// previewClassification 生成和备案草稿存储一致的初步定级建议。
// 参数 w 为响应，r 为携带申报事实的预览请求。
// 返回值 无，响应建议及依据，预览不写入备案。
func previewClassification(w http.ResponseWriter, r *http.Request) {
	var input classification.Input
	if httpx.Decode(w, r, &input) != nil {
		respondError(w, ErrInvalid)
		return
	}
	result, err := classification.Evaluate(input)
	respond(w, http.StatusOK, result, err)
}

// list 处理指定业务类型的筛选分页。
// 参数 w 为响应对象，r 为携带筛选参数的请求。
// 返回值 无。
func (h endpoint) list(w http.ResponseWriter, r *http.Request) {
	h.listRecords(w, r, false)
}

// listRecords 校验备案或测评的分页条件，并按接口要求限制机构范围。
// 参数 w 与 r 为响应及列表请求，scoped 决定是否要求机构名称。
// 返回值 无。
func (h endpoint) listRecords(w http.ResponseWriter, r *http.Request, scoped bool) {
	query, institution, err := listRequestValues(r.URL.RawQuery, scoped)
	if err != nil {
		respondError(w, ErrInvalid)
		return
	}
	assigned, err := consumeAssignedReview(query, scoped && h.kind == filing)
	if err != nil {
		respondError(w, err)
		return
	}
	valid := validStatus
	if h.kind == filing {
		valid = validFilingStatus
	}
	input, err := parseListQuery(query, valid)
	if err != nil || (h.kind != filing && query.Has("lifecycle")) || (h.kind != report && query.Has("risk")) {
		respondError(w, ErrInvalid)
		return
	}
	input.institution = institution
	input.assignedReview = assigned
	result, err := h.service.list(r.Context(), h.kind, input)
	respond(w, http.StatusOK, result, err)
}

// detail 处理现有记录详情读取。
// 参数 w 为响应对象，r 为携带业务记录标识的请求。
// 返回值 无。
func (h endpoint) detail(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.detail(r.Context(), h.kind, r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}

// create 根据对应业务表单保存草稿。
// 参数 w 为响应对象，r 为草稿录入请求。
// 返回值 无。
func (h endpoint) create(w http.ResponseWriter, r *http.Request) {
	input, _, err := h.decodeForm(w, r, false)
	if err != nil {
		respondError(w, err)
		return
	}
	result, err := h.service.create(r.Context(), h.kind, input, "")
	respond(w, http.StatusCreated, result, err)
}

// update 按当前表单版本更新尚可编辑的记录。
// 参数 w 为响应对象，r 为业务编辑请求。
// 返回值 无。
func (h endpoint) update(w http.ResponseWriter, r *http.Request) {
	input, version, err := h.decodeForm(w, r, true)
	if err != nil {
		respondError(w, err)
		return
	}
	result, err := h.service.update(r.Context(), h.kind, r.PathValue("id"), version, input, "")
	respond(w, http.StatusOK, result, err)
}

// decodeForm 使用对应操作的结构解码，并只允许该表单所需的请求字段。
// 参数 w 为响应对象，r 为业务表单请求，updating 表示是否需要已有版本。
// 返回值 数据库写入字段、预期版本，或无效参数错误。
func (h endpoint) decodeForm(w http.ResponseWriter, r *http.Request, updating bool) (saveFields, int64, error) {
	if updating {
		return h.decodeUpdate(w, r)
	}
	if h.kind == filing {
		var input filingInput
		if httpx.Decode(w, r, &input) != nil {
			return saveFields{}, 0, ErrInvalid
		}
		result, err := filingFields(input)
		return result, 0, err
	}
	var input reportInput
	if httpx.Decode(w, r, &input) != nil {
		return saveFields{}, 0, ErrInvalid
	}
	result, err := reportFields(input)
	return result, 0, err
}

// decodeUpdate 校验带已有版本的编辑表单。
// 参数 w 为响应对象，r 为业务编辑请求。
// 返回值 数据库写入字段、预期版本，或无效参数错误。
func (h endpoint) decodeUpdate(w http.ResponseWriter, r *http.Request) (saveFields, int64, error) {
	if h.kind == filing {
		var input struct {
			filingInput
			versionInput
		}
		if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
			return saveFields{}, 0, ErrInvalid
		}
		result, err := parseFilingFields(input.filingInput, false)
		return result, input.ExpectedVersion, err
	}
	var input struct {
		reportInput
		versionInput
	}
	if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
		return saveFields{}, 0, ErrInvalid
	}
	result, err := reportFields(input.reportInput)
	return result, input.ExpectedVersion, err
}

// submit 处理备案或测评材料的提交初审命令。
// 参数 w 为响应对象，r 为携带已读版本的提交请求。
// 返回值 无。
func (h endpoint) submit(w http.ResponseWriter, r *http.Request) {
	var input versionInput
	if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
		respondError(w, ErrInvalid)
		return
	}
	result, err := h.service.changeStatus(r.Context(), h.kind, r.PathValue("id"),
		reviewInput{versionInput: input, Decision: submit})
	respond(w, http.StatusOK, result, err)
}

// review 应用初审、终审或退回补正决定。
// 参数 w 为响应对象，r 为携带决定、已读版本和可选意见的请求。
// 返回值 无。
func (h endpoint) review(w http.ResponseWriter, r *http.Request) {
	input, err := decodeReviewAction(w, r, false)
	if err != nil {
		respondError(w, err)
		return
	}
	result, err := h.service.changeStatus(r.Context(), h.kind, r.PathValue("id"), input)
	respond(w, http.StatusOK, result, err)
}

type itemResponse[T any] struct {
	Items []T `json:"items"`
}

// history 查询记录真实保存的审核留痕。
// 参数 w 为响应对象，r 为携带业务记录标识的请求。
// 返回值 无。
func (h endpoint) history(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.history(r.Context(), h.kind, r.PathValue("id"), "")
	respond(w, http.StatusOK, itemResponse[reviewEvent]{Items: items}, err)
}

// respond 完成成功响应或转换统一业务错误。
// 参数 w 为响应对象，status 为成功状态，result 为最小业务输出，err 为操作结果。
// 返回值 无。
func respond(w http.ResponseWriter, status int, result any, err error) {
	if err != nil {
		respondError(w, err)
		return
	}
	httpx.JSON(w, status, result)
}

// respondError 将内部错误转换成可公开的状态、代码和业务提示。
// 参数 w 为响应对象，err 为业务操作或数据库错误。
// 返回值 无。
func respondError(w http.ResponseWriter, err error) {
	var missing policyMaterialMissing
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "未找到对应的业务记录或材料。")
	case errors.Is(err, errFilingNumberRequired):
		httpx.Error(w, http.StatusBadRequest, "filing_number_required", "请填写正式备案编号后确认终审通过。")
	case errors.Is(err, ErrDuplicate):
		httpx.Error(w, http.StatusConflict, "duplicate_number", "该编号已存在，可核对并修改。")
	case errors.Is(err, errWithdrawalPending):
		httpx.Error(w, http.StatusConflict, "withdrawal_pending", "此单正在等待监管确认撤回，原审核已暂停。")
	case errors.Is(err, errSameReviewer):
		httpx.Error(w, http.StatusConflict, "different_reviewer_required", "终审须由不同于初审的监管账号办理。")
	case errors.Is(err, ErrConflict):
		httpx.Error(w, http.StatusConflict, "conflict", "记录已变化，请刷新后核对可操作状态。")
	case errors.Is(err, ErrFilingInactive):
		httpx.Error(w, http.StatusConflict, "filing_inactive", "请核对备案是否已通过并且仍然有效。")
	case errors.Is(err, directoryref.ErrUnavailable):
		httpx.Error(w, http.StatusConflict, "directory_unavailable", err.Error())
	case errors.Is(err, directoryref.ErrPersonRequired):
		httpx.Error(w, http.StatusBadRequest, "directory_person_required", err.Error())
	case errors.Is(err, directoryref.ErrPersonInvalid):
		httpx.Error(w, http.StatusBadRequest, "directory_person_invalid", err.Error())
	case errors.Is(err, ErrActiveApplication):
		httpx.Error(w, http.StatusConflict, "active_application", "该备案已有未办结的变更或注销申请。")
	case errors.Is(err, ErrMaterials):
		httpx.Error(w, http.StatusBadRequest, "materials_required", "请上传本业务要求的全部必传材料。")
	case errors.Is(err, ErrOpenMaterialIssues):
		httpx.Error(w, http.StatusConflict, "material_issues_open", "请先补正仍未关闭的材料异常，再提交审核。")
	case errors.Is(err, errHistoryManifest):
		httpx.Error(w, http.StatusBadRequest, "history_import_manifest", "请按模板填写 ZIP 根目录中的 manifest.csv，单包限 100 条备案。")
	case errors.Is(err, errHistoryMergedUnit):
		httpx.Error(w, http.StatusConflict, "filing_unit_merged", "该单位已合并，请选择承接单位后重试。")
	case errors.Is(err, ErrFilingPackage):
		httpx.Error(w, http.StatusBadRequest, "filing_package", err.Error())
	case errors.Is(err, ErrUploadInterrupted):
		httpx.Error(w, http.StatusBadRequest, "attachment_interrupted", "材料上传中断，请从本地重新选取文件。")
	case errors.Is(err, ErrFileSize):
		httpx.Error(w, http.StatusRequestEntityTooLarge, "attachment_size", "材料为空或超过允许大小。")
	case errors.Is(err, errLegacyFilingWord):
		httpx.Error(w, http.StatusBadRequest, "attachment_type", "无法读取该 Word（DOC）材料，请检查文件是否损坏或加密。")
	case errors.Is(err, ErrFileType):
		httpx.Error(w, http.StatusBadRequest, "attachment_type", "请上传内容有效的 PDF、DOCX、XLSX 或 ZIP 材料。")
	case errors.Is(err, classification.ErrInvalid):
		httpx.Error(w, http.StatusBadRequest, "invalid_classification_assessment", err.Error())
	case errors.Is(err, filingpolicy.ErrInvalid):
		httpx.Error(w, http.StatusBadRequest, "invalid_material_policy", err.Error())
	case errors.As(err, &missing):
		httpx.Error(w, http.StatusBadRequest, "configured_material_required", missing.Error())
	case errors.Is(err, ErrInvalid):
		httpx.Error(w, http.StatusBadRequest, "invalid_input", "请检查业务字段、审核意见或操作参数。")
	default:
		slog.Error("业务请求处理失败", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "处理失败，请稍后重试。")
	}
}
