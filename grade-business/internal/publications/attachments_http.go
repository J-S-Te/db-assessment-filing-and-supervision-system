package publications

import (
	"errors"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"grade-business/internal/cases"
	"grade-business/internal/httpx"
)

type attachmentChangeInput struct {
	ExpectedVersion int64 `json:"expectedVersion"`
}

// registerAttachmentRoutes 将附件编辑限定在监管端，并在每个门户挂载范围下载。
// 参数 mux、admin 为路由器及监管公告路径。
// 返回值 无。
func (s *Service) registerAttachmentRoutes(mux *http.ServeMux, admin string) {
	path := admin + "/{id}/attachments"
	mux.HandleFunc("POST "+path, withID(s.handleAttachmentUpload))
	mux.HandleFunc("DELETE "+path+"/{attachmentId}", withID(s.handleAttachmentRemoval))
	mux.HandleFunc("GET "+path+"/{attachmentId}", withID(s.handleAttachmentDownload("")))
}

// handleAttachmentUpload 验证并暂存一批材料，再原子更新整个公告草稿。
// 参数 w、r、id 分别为响应、上传请求和已验证公告标识。
// 返回值 无。
func (s *Service) handleAttachmentUpload(w http.ResponseWriter, r *http.Request, id pgtype.UUID) {
	if r.URL.RawQuery != "" {
		respond(w, 0, nil, errInvalid)
		return
	}
	if s.store == nil {
		respond(w, 0, nil, errStorageUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentSum+(512<<10))
	err := r.ParseMultipartForm(1 << 20)
	if form := r.MultipartForm; form != nil {
		defer form.RemoveAll()
	}
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			err = cases.ErrFileSize
		} else {
			err = errInvalid
		}
		respond(w, 0, nil, err)
		return
	}
	expected, parts, err := attachmentUploadParts(r.MultipartForm)
	if err != nil {
		respond(w, 0, nil, err)
		return
	}
	staged, err := s.stageDocuments(parts)
	defer s.cleanupDocuments(r.Context(), staged)
	if err != nil {
		respond(w, 0, nil, err)
		return
	}
	out, err := s.addAttachments(r.Context(), id, expected, staged)
	respond(w, http.StatusOK, out, err)
}

// attachmentUploadParts 限定上传表单只接收当前版本和所选文件。
// 参数 form 为经过请求大小限制的多部分表单。
// 返回值 合法预期版本、文件声明、或参数和大小错误。
func attachmentUploadParts(form *multipart.Form) (int64, []*multipart.FileHeader, error) {
	values := form.Value["expectedVersion"]
	parts := form.File["files"]
	if len(form.Value) != 1 || len(values) != 1 || len(form.File) != 1 || len(parts) == 0 {
		return 0, nil, errInvalid
	}
	version, err := attachmentVersion(values[0])
	if err != nil {
		return 0, nil, err
	}
	if len(parts) > maxAttachments {
		return 0, nil, errAttachmentLimit
	}
	var total int64
	for _, file := range parts {
		if file.Size <= 0 || file.Size > maxAttachmentSize {
			return 0, nil, cases.ErrFileSize
		}
		total += file.Size
	}
	if total > maxAttachmentSum {
		return 0, nil, cases.ErrFileSize
	}
	return version, parts, nil
}

// attachmentVersion 解析上传或下载需要的正整数版本。
// 参数 raw 为客户端提交的十进制版本字符串。
// 返回值 合法版本或参数错误。
func attachmentVersion(raw string) (int64, error) {
	if strings.Trim(raw, "0123456789") != "" {
		return 0, errInvalid
	}
	version, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || version < 1 {
		return 0, errInvalid
	}
	return version, nil
}

// stageDocuments 收集已经暂存的文件，供最终写入或出错后清理。
// 参数 parts 为已验证数量和声明大小的材料。
// 返回值 已暂存文件列表，以及首个上传校验或存储错误。
func (s *Service) stageDocuments(parts []*multipart.FileHeader) ([]stagedDocument, error) {
	output := make([]stagedDocument, 0, len(parts))
	for _, part := range parts {
		item, err := s.stageDocument(part)
		if err != nil {
			return output, err
		}
		output = append(output, item)
	}
	return output, nil
}

// stageDocument 使用受控文件存储立即校验一项材料。
// 参数 part 为当前多部分上传文件。
// 返回值 实际大小和内部存储信息，或打开、校验和存储错误。
func (s *Service) stageDocument(part *multipart.FileHeader) (stagedDocument, error) {
	source, err := part.Open()
	if err != nil {
		return stagedDocument{}, err
	}
	defer source.Close()
	file, key, digest, err := s.store.StagePublicationAttachment(part.Filename, source)
	if err != nil {
		return stagedDocument{}, err
	}
	return stagedDocument{fileName: file.FileName, size: file.Size, key: key, digest: digest}, nil
}

// handleAttachmentRemoval 接收监管对当前草稿附件引用的移除命令。
// 参数 w、r、id 分别为响应、移除请求与公告标识。
// 返回值 无。
func (s *Service) handleAttachmentRemoval(w http.ResponseWriter, r *http.Request, id pgtype.UUID) {
	attachment, ok := parseUUID(r.PathValue("attachmentId"))
	if !ok {
		respond(w, 0, nil, errMissing)
		return
	}
	var input attachmentChangeInput
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
		respond(w, 0, nil, errInvalid)
		return
	}
	out, err := s.removeAttachment(r.Context(), id, input.ExpectedVersion, attachment.String())
	respond(w, http.StatusOK, out, err)
}

// handleAttachmentDownload 校验版本与收件范围后，使用共享的受控原件下载能力。
// 参数 audience 为门户角色，监管端为空。
// 返回值 针对公告附件的实际下载接口。
func (s *Service) handleAttachmentDownload(audience string) func(http.ResponseWriter, *http.Request, pgtype.UUID) {
	return func(w http.ResponseWriter, r *http.Request, id pgtype.UUID) {
		attachment, ok := parseUUID(r.PathValue("attachmentId"))
		if !ok {
			respond(w, 0, nil, errMissing)
			return
		}
		version, scope, err := s.attachmentDownloadScope(r, audience)
		if err != nil {
			respond(w, 0, nil, err)
			return
		}
		file, key, err := s.readAttachmentFile(r.Context(), id, version, attachment, scope)
		if err == nil {
			if s.store == nil {
				err = errStorageUnavailable
			} else {
				err = s.store.ServeBusinessAttachment(w, r, file, key)
			}
		}
		if errors.Is(err, cases.ErrNotFound) {
			err = errMissing
		}
		if err != nil {
			respond(w, 0, nil, err)
		}
	}
}

// attachmentDownloadScope 读取强制版本和门户名录的固定收件范围。
// 参数 r 为下载请求，audience 为门户角色，监管请求的角色为空。
// 返回值 精确版本、已验证范围，或参数与名录查询错误。
func (s *Service) attachmentDownloadScope(r *http.Request, audience string) (int64, recipient, error) {
	fields := []string{"version"}
	if audience != "" {
		fields = append(fields, scopeField(audience))
	}
	values, err := publicationQuery(r.URL.RawQuery, fields...)
	if err != nil {
		return 0, recipient{}, err
	}
	version, err := attachmentVersion(values.Get("version"))
	if err != nil || audience == "" {
		return version, recipient{}, err
	}
	scope, err := queryScope(values, audience, true)
	if err != nil {
		return 0, recipient{}, err
	}
	scope, err = s.canonicalRecipient(r.Context(), scope)
	return version, scope, err
}
