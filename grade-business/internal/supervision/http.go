package supervision

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/internal/cases"
	"grade-business/internal/httpx"
)

// Service 管理监管监督检查与运营者自查。
type Service struct {
	db    *pgxpool.Pool
	files *cases.Service
	actor string
}

// New 创建监督检查业务服务。
// 参数 db 为业务数据库，files 为共用材料服务，actor 为监管经办人。
// 返回值 可注册业务路由的服务。
func New(db *pgxpool.Pool, files *cases.Service, actor string) *Service {
	return &Service{db: db, files: files, actor: actor}
}

// Register 挂载监督检查任务的监管及运营者接口。
// 参数 mux 为同源 HTTP 路由。
// 返回值 无。
func (s *Service) Register(mux *http.ServeMux) {
	s.registerAudience(mux, regulatorPath, false)
	s.registerAudience(mux, operatorPath, true)
	s.registerBatchRoutes(mux)
	mux.HandleFunc("GET "+regulatorPath+"/filing-options", s.optionsHTTP)
	mux.HandleFunc("POST "+regulatorPath, s.createHTTP)
}

// registerAudience 注册两个业务端均能查看或办理的受控任务路径。
// 参数 mux、base、operator 为服务器路由、路径前缀及办理端标记。
// 返回值 无。
func (s *Service) registerAudience(mux *http.ServeMux, base string, operator bool) {
	item := base + "/{id}"
	s.registerSiteAudience(mux, item, operator)
	mux.HandleFunc("GET "+base, s.listHTTP(operator))
	mux.HandleFunc("GET "+item, taskRequest(operator, s.detailHTTP))
	mux.HandleFunc("GET "+item+"/history", s.historyHTTP(operator))
	mux.HandleFunc("POST "+item+"/actions", s.actionHTTP(operator))
	mux.HandleFunc("POST "+item+"/attachments", taskRequest(operator, s.uploadHTTP))
	mux.HandleFunc("GET "+item+"/attachments/{attachmentId}", taskRequest(operator, s.downloadHTTP))
}

// taskRequest 在进入任务处理前统一校验归属、参数和标准记录标识。
// 参数 operator 指办理端，handler 是接收已校验任务标识及归属的处理器。
// 返回值 经过范围校验的任务端点。
func taskRequest(operator bool, handler func(http.ResponseWriter, *http.Request, string, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, owner, err := parseQuery(r.URL.RawQuery, operator)
		if err != nil || !validID(r.PathValue("id")) {
			respond(w, 200, nil, errInput)
			return
		}
		handler(w, r, strings.ToLower(r.PathValue("id")), owner)
	}
}

// optionsHTTP 读取监管可选择的有效备案或真实超期线索的指定备案。
// 参数 w、r 分别为响应输出和筛选请求。
// 返回值 无。
func (s *Service) optionsHTTP(w http.ResponseWriter, r *http.Request) {
	values, _, err := parseQuery(r.URL.RawQuery, false, "q", "leadAlertId")
	query, lead := strings.TrimSpace(values.Get("q")), values.Get("leadAlertId")
	if err != nil || (query != "" && !validText(query, 120)) || (lead != "" && !validID(lead)) {
		respond(w, 200, nil, errInput)
		return
	}
	items, err := s.options(r.Context(), query, lead)
	respond(w, 200, page[filingOption]{Items: items}, err)
}

// createHTTP 校验监管草稿的最小业务字段并保存任务。
// 参数 w、r 分别为响应输出和创建请求。
// 返回值 无。
func (s *Service) createHTTP(w http.ResponseWriter, r *http.Request) {
	var input createInput
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil || !validCreate(&input) {
		respond(w, http.StatusCreated, nil, errInput)
		return
	}
	item, err := s.create(r.Context(), input)
	respond(w, http.StatusCreated, item, err)
}

// listHTTP 读取监管或指定原接收单位的有界任务清单。
// 参数 operator 标记是否需要运营单位过滤。
// 返回值 任务列表 HTTP 处理器。
func (s *Service) listHTTP(operator bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query, owner, err := parseQuery(r.URL.RawQuery, operator, "status", "before")
		before, beforeErr := cursorValue(query)
		status := query.Get("status")
		if err != nil || beforeErr != nil || !validStatus(status, operator) {
			respond(w, 200, nil, errInput)
			return
		}
		items, err := s.list(r.Context(), owner, status, before)
		respond(w, 200, items, err)
	}
}

// detailHTTP 读取当前任务及角色可以查看的附件元数据。
// 参数 w、r、id、owner 为应答、请求、任务标识与范围。
// 返回值 无。
func (s *Service) detailHTTP(w http.ResponseWriter, r *http.Request, id, owner string) {
	item, err := s.detail(r.Context(), id, owner)
	respond(w, 200, item, err)
}

// historyHTTP 验证分页并返回可追溯的办理记录。
// 参数 operator 为办理端。
// 返回值 任务历史 HTTP 处理器。
func (s *Service) historyHTTP(operator bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query, owner, err := parseQuery(r.URL.RawQuery, operator, "before")
		before, beforeErr := cursorValue(query)
		if err != nil || beforeErr != nil || !validID(r.PathValue("id")) {
			respond(w, 200, nil, errInput)
			return
		}
		items, err := s.history(r.Context(), r.PathValue("id"), owner, before)
		respond(w, 200, items, err)
	}
}

// actionHTTP 检查操作席位和版本后办理一次业务动作。
// 参数 operator 指真正的业务操作端。
// 返回值 任务动作 HTTP 处理器。
func (s *Service) actionHTTP(operator bool) http.HandlerFunc {
	return taskRequest(operator, func(w http.ResponseWriter, r *http.Request, id, owner string) {
		var input actionInput
		if httpx.Decode(w, r, &input) != nil || !actionParameters(&input, operator) {
			respond(w, 200, nil, errInput)
			return
		}
		result, err := s.execute(r.Context(), id, owner, input)
		respond(w, 200, result, err)
	})
}

// uploadHTTP 接收一份模板或结果材料，校验内容并在事务中确认版本和归属。
// 参数 w、r、id、owner 为应答、表单请求、任务标识与范围。
// 返回值 无。
func (s *Service) uploadHTTP(w http.ResponseWriter, r *http.Request, id, owner string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBody)
	err := r.ParseMultipartForm(1 << 20)
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			err = cases.ErrFileSize
		} else {
			err = errInput
		}
		respond(w, 201, nil, err)
		return
	}
	item, err := s.handleMaterial(r, id, owner)
	respond(w, 201, item, err)
}

// handleMaterial 规范表单字段，核验真实文件并安全处理暂存清理。
// 参数 r 为已通过大小限制的表单，id、owner 为任务及允许的单位。
// 返回值 文件与版本信息，或业务冲突及输入错误。
func (s *Service) handleMaterial(r *http.Request, id, owner string) (attachmentMutation, error) {
	form := r.MultipartForm
	if len(form.Value) != 2 || len(form.Value["kind"]) != 1 || len(form.Value["expectedVersion"]) != 1 ||
		len(form.File) != 1 || len(form.File["file"]) != 1 {
		return attachmentMutation{}, errInput
	}
	kind := form.Value["kind"][0]
	expected, err := strconv.ParseInt(form.Value["expectedVersion"][0], 10, 64)
	if err != nil || expected < 1 || (kind != inspectionForm && kind != rectificationForm && !isTemplate(kind)) {
		return attachmentMutation{}, errInput
	}
	record, err := loadTask(r.Context(), s.db, id, owner, false)
	if err != nil {
		return attachmentMutation{}, err
	}
	if record.Historical {
		return attachmentMutation{}, pgx.ErrNoRows
	}
	if !canUpload(record, owner, kind) {
		return attachmentMutation{}, errState
	}
	header := form.File["file"][0]
	file, err := header.Open()
	if err != nil {
		return attachmentMutation{}, errAttachment
	}
	defer file.Close()
	item, key, digest, err := s.files.StagePublicationAttachment(header.Filename, file)
	if err != nil {
		return attachmentMutation{}, err
	}
	defer s.discardUnused(r.Context(), key)
	item.Kind = kind
	return s.writeMaterial(r.Context(), id, owner, expected, item, key, digest)
}

// downloadHTTP 向有明确任务归属及材料权限的请求发送已登记文件。
// 参数 w、r、id、owner 为响应、材料请求、任务标识和范围。
// 返回值 无。
func (s *Service) downloadHTTP(w http.ResponseWriter, r *http.Request, id, owner string) {
	if !validID(r.PathValue("attachmentId")) {
		respond(w, 200, nil, errInput)
		return
	}
	if err := s.download(w, r, id, owner); err != nil {
		respond(w, 200, nil, err)
	}
}

// respond 将业务错误映射到可操作的状态，不返回数据库和存储路径。
// 参数 w、status、item、err 为响应目标、成功状态、业务结果和错误。
// 返回值 无。
func respond(w http.ResponseWriter, status int, item any, err error) {
	switch {
	case err == nil:
		httpx.JSON(w, status, item)
	case errors.Is(err, errInput):
		httpx.Error(w, 400, "invalid_input", "请核对监督检查范围、日期、操作及请求字段。")
	case errors.Is(err, pgx.ErrNoRows), errors.Is(err, cases.ErrNotFound):
		httpx.Error(w, 404, "not_found", "未找到当前范围内的任务或材料。")
	case errors.Is(err, errDuplicate):
		httpx.Error(w, 409, "supervision_duplicate", "该备案在同一年度或同一线索下已有监督检查任务。")
	case errors.Is(err, errState):
		httpx.Error(w, 409, "supervision_conflict", "办理状态或版本已变化，请刷新后继续。")
	case errors.Is(err, errSitePending):
		httpx.Error(w, 409, "supervision_site_pending", "现场检查尚在进行，请先办结或取消现场检查。")
	case errors.Is(err, errScope):
		httpx.Error(w, 409, "supervision_scope_changed", "备案、收件单位、线索或下发期限已发生变化。")
	case errors.Is(err, errBatchTooLarge):
		httpx.Error(w, 422, "supervision_batch_limit", "每批最多核对 100 份备案，请缩小区县或等保等级范围。")
	case errors.Is(err, errMaterials):
		httpx.Error(w, 422, "supervision_materials_required", "请补齐两类所需材料，并按补正意见上传本轮更新文件。")
	case errors.Is(err, errAttachment), errors.Is(err, cases.ErrFileType):
		httpx.Error(w, 415, "unsupported_attachment", "请上传内容有效的 PDF 或 DOCX 材料。")
	case errors.Is(err, cases.ErrFileSize):
		httpx.Error(w, 413, "attachment_size", "请上传非空且不超过 20 MiB 的材料。")
	default:
		slog.Error("监督检查办理失败", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, 500, "internal_error", "监督检查服务暂时无法处理，请稍后重试。")
	}
}
