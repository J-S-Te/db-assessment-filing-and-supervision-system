package publications

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/cases"
	"grade-business/internal/httpx"
)

// Service 管理本地站内公告、版本留痕与公开展示范围。
type Service struct {
	db       *pgxpool.Pool
	operator string
	store    AttachmentStore
}

// New 创建站内信息发布服务。
// 参数 pool 为业务连接池，operator 为配置的监管席位。
// 返回值 可注册接口的服务。
func New(pool *pgxpool.Pool, operator string) *Service {
	return NewWithAttachments(pool, operator, nil)
}

// NewWithAttachments 创建可读写公文附件的站内信息发布服务。
// 参数 pool 为业务连接池，operator 为监管席位，store 为共享的受控文件存储。
// 返回值 可注册公告与附件接口的服务。
func NewWithAttachments(pool *pgxpool.Pool, operator string, store AttachmentStore) *Service {
	return &Service{db: pool, operator: operator, store: store}
}

// Register 挂载监管发布、名册和门户公告查收接口。
// 参数 mux 为业务路由表。
// 返回值 无。
func (s *Service) Register(mux *http.ServeMux) {
	const admin = "/api/v1/publications"
	mux.HandleFunc("GET "+admin, s.handleList(""))
	mux.HandleFunc("GET "+admin+"/recipients", s.handleRecipients)
	mux.HandleFunc("GET "+admin+"/{id}", withID(s.handleDetail("")))
	mux.HandleFunc("POST "+admin, s.handleCreate)
	mux.HandleFunc("PATCH "+admin+"/{id}", withID(s.handleEdit))
	mux.HandleFunc("POST "+admin+"/{id}/actions", withID(s.handleAction))
	mux.HandleFunc("GET "+admin+"/{id}/history", withID(s.handleHistory))
	mux.HandleFunc("GET "+admin+"/{id}/deliveries", withID(s.handleDeliveries))
	s.registerAttachmentRoutes(mux, admin)
	s.registerReader(mux, operatorAudience)
	s.registerReader(mux, institutionAudience)
}

// registerReader 按实际门户角色注册已发布公告和收件状态接口。
// 参数 mux 为业务路由表，audience 为收取公告的角色。
// 返回值 无。
func (s *Service) registerReader(mux *http.ServeMux, audience string) {
	base := "/api/v1/" + audience + "/publications"
	mux.HandleFunc("GET "+base, s.handleList(audience))
	mux.HandleFunc("GET "+base+"/{id}", withID(s.handleDetail(audience)))
	mux.HandleFunc("GET "+base+"/{id}/attachments/{attachmentId}", withID(s.handleAttachmentDownload(audience)))
	mux.HandleFunc("GET "+base+"/unread-count", s.handleUnreadCount(audience))
	mux.HandleFunc("POST "+base+"/read-all", s.handleReadAll(audience))
	mux.HandleFunc("POST "+base+"/{id}/read", withID(s.handleReceipt(audience, false)))
	mux.HandleFunc("POST "+base+"/{id}/acknowledge", withID(s.handleReceipt(audience, true)))
}

// withID 校验公告 UUID，再调用业务访问处理器。
// 参数 next 为接受规范公告标识的处理器。
// 返回值 先验证路径标识的 HTTP 处理器。
func withID(next func(http.ResponseWriter, *http.Request, pgtype.UUID)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseUUID(r.PathValue("id"))
		if !ok {
			respond(w, 0, nil, errMissing)
			return
		}
		next(w, r, id)
	}
}

// handleList 建立按实际公开范围和条件读取发布列表的处理器。
// 参数 audience 为空时读取监管列表，否则为门户角色。
// 返回值 监管或运营者列表的 HTTP 处理器。
func (s *Service) handleList(audience string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query, err := parseFilter(r.URL.RawQuery, audience)
		if err != nil {
			respond(w, 0, nil, err)
			return
		}
		query.scope, err = s.canonicalRecipient(r.Context(), query.scope)
		if err != nil {
			respond(w, 0, nil, err)
			return
		}
		out, err := s.readList(r.Context(), query, audience != "")
		respond(w, http.StatusOK, out, err)
	}
}

// handleDetail 建立返回指定公告当前内容或已发布快照的处理器。
// 参数 audience 为空时读取监管详情，否则为门户角色。
// 返回值 接受已校验公告标识的详情处理器。
func (s *Service) handleDetail(audience string) func(http.ResponseWriter, *http.Request, pgtype.UUID) {
	return func(w http.ResponseWriter, r *http.Request, id pgtype.UUID) {
		var scope recipient
		var err error
		if audience != "" {
			scope, err = s.readerScope(r, audience, audience == institutionAudience)
		} else if r.URL.RawQuery != "" {
			err = errInvalid
		}
		if err != nil {
			respond(w, 0, nil, err)
			return
		}
		out, err := s.readDetail(r.Context(), id, scope)
		respond(w, http.StatusOK, out, err)
	}
}

// handleCreate 将规范化后的站内信息保存为未公开草稿。
// 参数 w 为响应对象，r 为表单请求。
// 返回值 无。
func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var input content
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil || !validateContent(&input) {
		respond(w, 0, nil, errInvalid)
		return
	}
	out, err := s.create(r.Context(), input)
	respond(w, http.StatusCreated, out, err)
}

// handleEdit 追加修订版本，并保留可能已经公开的快照。
// 参数 w 为响应对象，r 为修订请求，id 为公告标识。
// 返回值 无。
func (s *Service) handleEdit(w http.ResponseWriter, r *http.Request, id pgtype.UUID) {
	var input editInput
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil ||
		input.ExpectedVersion < 1 || !validateContent(&input.content) {
		respond(w, 0, nil, errInvalid)
		return
	}
	out, err := s.change(r.Context(), id, input.ExpectedVersion, "edit", &input.content)
	respond(w, http.StatusOK, out, err)
}

// handleAction 发布当前草稿版本或撤回已发布信息。
// 参数 w 为响应，r 为操作、预期版本请求，id 为公告标识。
// 返回值 无。
func (s *Service) handleAction(w http.ResponseWriter, r *http.Request, id pgtype.UUID) {
	var input actionInput
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 ||
		(input.Action != "publish" && input.Action != "withdraw") {
		respond(w, 0, nil, errInvalid)
		return
	}
	out, err := s.change(r.Context(), id, input.ExpectedVersion, input.Action, nil)
	respond(w, http.StatusOK, out, err)
}

// handleHistory 管理侧读取发布与修订留痕。
// 参数 w 为响应，r 为含游标的请求，id 为公告标识。
// 返回值 无。
func (s *Service) handleHistory(w http.ResponseWriter, r *http.Request, id pgtype.UUID) {
	before, err := historyCursor(r.URL.RawQuery)
	if err != nil {
		respond(w, 0, nil, err)
		return
	}
	out, err := s.readHistory(r.Context(), id, before)
	respond(w, http.StatusOK, out, err)
}

// respond 输出业务数据或不会暴露内部细节的错误。
// 参数 w 为响应对象，status 为成功状态，out 为业务响应数据，err 为错误。
// 返回值 无。
func respond(w http.ResponseWriter, status int, out any, err error) {
	switch {
	case err == nil:
		httpx.JSON(w, status, out)
	case errors.Is(err, errInvalid):
		httpx.Error(w, http.StatusBadRequest, "invalid_input", "请核对公告内容、发布范围、收件方或查询条件。")
	case errors.Is(err, errMissing):
		httpx.Error(w, http.StatusNotFound, "not_found", "未找到该站内信息。")
	case errors.Is(err, cases.ErrFileType):
		httpx.Error(w, http.StatusBadRequest, "attachment_type", "公文附件须为有效的 PDF 或 DOCX 文件。")
	case errors.Is(err, cases.ErrFileSize):
		httpx.Error(w, http.StatusRequestEntityTooLarge, "attachment_size", "附件须非空且单个不超过 20 MiB。本批上传总大小上限为 40 MiB。")
	case errors.Is(err, errAttachmentLimit):
		httpx.Error(w, http.StatusConflict, "attachment_limit", "同一版本最多保存 8 个附件，总大小不超过 40 MiB。")
	case errors.Is(err, cases.ErrPDFValidationUnavailable):
		httpx.Error(w, http.StatusServiceUnavailable, "material_validation_unavailable", "暂时无法校验 PDF 资料，请稍后重试或上传 DOCX。")
	case errors.Is(err, errMaterialAttachmentRequired):
		httpx.Error(w, http.StatusBadRequest, "material_attachment_required", "政策标准或常用资料至少需要一份有效附件才能发布。")
	case errors.Is(err, errConflict):
		httpx.Error(w, http.StatusConflict, "version_conflict", "公告状态或版本已变化，请刷新后重新确认。")
	default:
		slog.Error("站内公告操作失败", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "站内公告处理失败，请稍后重试。")
	}
}
