package cases

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// listAttachments 返回当前记录实际登记的公开材料字段。
// 参数 w 为响应对象，r 为带有业务记录标识的请求。
// 返回值 无。
func (h endpoint) listAttachments(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.materials(r.Context(), h.kind, r.PathValue("id"))
	respond(w, http.StatusOK, itemResponse[Attachment]{Items: result.Attachments}, err)
}

// upload 校验业务归属、表单和材料数据后登记新附件。
// 参数 w 为响应对象，r 为带有文件及材料类别的上传请求。
// 返回值 无。
func (h endpoint) upload(w http.ResponseWriter, r *http.Request) {
	h.uploadToScope(w, r, "")
}

// uploadToScope 核对材料所属记录及录入权限后，再接收和登记文件。
// 参数 w、r 为响应及上传请求，institution 为本地机构范围，空值为监管端。
// 返回值 无，成功返回真实登记的附件。
func (h endpoint) uploadToScope(w http.ResponseWriter, r *http.Request, institution string) {
	id := r.PathValue("id")
	current, err := h.service.detailInScope(r.Context(), h.kind, id, institution)
	if err == nil && (!editable(current.Status) || (institution != "" && current.operatorOrigin)) {
		err = ErrConflict
	}
	if err != nil {
		respondError(w, err)
		return
	}
	profile := legacySubmissionProfile
	if len(current.RequiredMaterials) != 0 {
		profile = institutionSubmissionProfile
	}
	result, err := h.receiveAttachment(w, r, profile, institution != "")
	if err != nil {
		if institution != "" && result.Kind != "" {
			err = h.saveUploadFailure(r.Context(), id, institution, current.Version, result, err)
		}
		respondError(w, err)
		return
	}
	material, reused, err := h.service.insertAttachment(r.Context(), h.kind, id, institution, result)
	if err != nil || reused {
		if cleanup := h.service.discardUnreferenced(r.Context(), result.fileKey); cleanup != nil {
			slog.Warn("未登记材料待清理", "error_type", fmt.Sprintf("%T", cleanup))
		}
	}
	respond(w, http.StatusCreated, material, err)
}

// receiveAttachment 解码仅含一个附件及其业务类别的上传表单。
// 参数 w、r 为上传响应和请求，profile 为材料配置，scoped 表示机构端入口。
// 返回值 文件存储信息及材料类别，或请求校验与文件错误。
func (h endpoint) receiveAttachment(w http.ResponseWriter, r *http.Request, profile string,
	scoped bool,
) (attachmentRow, error) {
	var upload attachmentRow
	declared := r.Header.Values("X-Material-Kind")
	if scoped && len(declared) > 0 {
		if len(declared) != 1 || !allowedUploadKind(h.kind, declared[0], profile, scoped) {
			return upload, ErrInvalid
		}
		upload.Kind = declared[0]
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPackageSize+(64<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			return upload, ErrFileSize
		}
		if errors.Is(err, io.ErrUnexpectedEOF) || r.Context().Err() != nil {
			return upload, ErrUploadInterrupted
		}
		return upload, ErrInvalid
	}
	defer r.MultipartForm.RemoveAll()
	values, files := r.MultipartForm.Value, r.MultipartForm.File
	retry := values["issueId"]
	if len(values["kind"]) != 1 || len(files) != 1 || len(files["file"]) != 1 ||
		!allowedUploadKind(h.kind, values["kind"][0], profile, scoped) {
		return attachmentRow{}, ErrInvalid
	}
	kind := values["kind"][0]
	if (upload.Kind != "" && upload.Kind != kind) || !validUploadMetadata(values, retry, scoped) {
		return attachmentRow{}, ErrInvalid
	}
	upload.Kind = kind
	if len(retry) == 1 {
		upload.issueID = retry[0]
	}
	part := files["file"][0]
	upload.FileName = part.Filename
	source, err := part.Open()
	if err != nil {
		return attachmentRow{}, err
	}
	defer source.Close()
	result, err := h.stageCaseMaterial(upload, source, scoped)
	if err != nil {
		return upload, err
	}
	return result, err
}

// allowedUploadKind 保留通用类别，并开放机构专用的已验证备案 ZIP。
// 参数 caseKind、materialKind、profile 描述类别和配置，scoped 为机构端上传入口。
// 返回值 类别允许提交时为真。
func allowedUploadKind(caseKind, materialKind, profile string, scoped bool) bool {
	return validMaterialKind(caseKind, materialKind, profile) ||
		(scoped && caseKind == filing && materialKind == "filing_package")
}

// validUploadMetadata 约束上传文本字段以及机构重发标识的形状。
// 参数 values 为表单文本字段，retry 为对应异常标识，scoped 表示机构入口。
// 返回值 请求只含可接受字段且标识有效时为真。
func validUploadMetadata(values map[string][]string, retry []string, scoped bool) bool {
	if _, exists := values["issueId"]; exists {
		return scoped && len(values) == 2 && len(retry) == 1 && validID(retry[0])
	}
	return len(values) == 1
}

// stageCaseMaterial 选择机构适用的文件校验并计算摘要，以便安全重试。
// 参数 upload 为材料元数据，source 为文件流，scoped 标记机构入口。
// 返回值 已验证的材料及其内部摘要，或格式、大小和存储错误。
func (h endpoint) stageCaseMaterial(upload attachmentRow, source io.Reader, scoped bool) (attachmentRow, error) {
	validator := checkFileContent
	if scoped {
		validator = h.caseMaterialValidator(upload.Kind)
	}
	if h.kind == filing && isFilingWordKind(upload.Kind) && strings.EqualFold(filepath.Ext(upload.FileName), ".doc") {
		validator = validateLegacyFilingWord
	}
	if !scoped {
		result, err := stageFileWithValidation(h.service.attachmentDir, upload.FileName, source, validator)
		result.Kind = upload.Kind
		return result, err
	}
	digest := sha256.New()
	result, err := stageFileWithValidation(h.service.attachmentDir, upload.FileName,
		io.TeeReader(source, digest), validator)
	result.Kind, result.issueID = upload.Kind, upload.issueID
	result.digest = hex.EncodeToString(digest.Sum(nil))
	return result, err
}

// caseMaterialValidator 为备案 ZIP 选择业务校验，禁止将 ZIP 当作普通必传单件。
// 参数 kind 为机构填写的材料类别。
// 返回值 根据材料类别校验实际文件的函数。
func (h endpoint) caseMaterialValidator(kind string) func(*os.File, string, int64) error {
	return func(file *os.File, extension string, size int64) error {
		if kind == "filing_package" {
			if extension != ".zip" {
				return fmt.Errorf("%w，请选择 ZIP 文件。", ErrFilingPackage)
			}
			return validateFilingPackage(file, size)
		}
		if h.kind == filing && kind != "supporting" && extension == ".zip" {
			return fmt.Errorf("%w，请按备案 ZIP 材料包类别上传。", ErrFilingPackage)
		}
		if extension == ".pdf" {
			return checkInstitutionPDF(file, size)
		}
		return checkFileContent(file, extension, size)
	}
}

// saveUploadFailure 持久化真实校验失败，连接断开时也给予短暂的登记时间。
// 参数 ctx 为上传上下文，id、institution、version 为记录及版本，upload 与 cause 描述失败。
// 返回值 已登记的原业务错误，或未能登记时的状态及存储错误。
func (h endpoint) saveUploadFailure(ctx context.Context, id, institution string, version int64,
	upload attachmentRow, cause error,
) error {
	code, reason := materialFailureReason(cause)
	if code == "" {
		return cause
	}
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	err := h.service.recordMaterialFailure(bounded, h.kind, id, institution, version, upload, code, reason)
	if err != nil {
		return err
	}
	return cause
}

// download 以附件模式返回当前记录所拥有的实际材料。
// 参数 w 为响应对象，r 为携带业务及附件标识的请求。
// 返回值 无。
func (h endpoint) download(w http.ResponseWriter, r *http.Request) {
	h.downloadFromScope(w, r, "")
}

// downloadFromScope 在限定机构和附件归属后发送文件。
// 参数 w、r 为响应及下载请求，institution 为本地机构范围，空值为监管端。
// 返回值 无，材料内容通过附件响应发送。
func (h endpoint) downloadFromScope(w http.ResponseWriter, r *http.Request, institution string) {
	a, err := h.service.readAttachment(r.Context(), h.kind, r.PathValue("id"), r.PathValue("attachmentId"), institution)
	if err != nil {
		respondError(w, err)
		return
	}
	file, err := openAttachment(h.service.attachmentDir, a.fileKey)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			err = ErrNotFound
		}
		respondError(w, err)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.FileName}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, a.FileName, a.UploadedAt, file)
}
