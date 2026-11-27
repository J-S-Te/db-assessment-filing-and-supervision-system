package cases

import (
	"encoding/hex"
	"errors"
	"mime"
	"net/http"
	"os"
)

// StageBusinessAttachment 使用现有附件大小、格式、文件名规则处理其他业务的上传。
// 参数 w 和 r 用于读取受限表单，validKind 核对调用方业务允许的材料类别。
// 返回值 附件显示信息、内部存储标识，或格式与落盘错误。
func (s *Service) StageBusinessAttachment(w http.ResponseWriter, r *http.Request,
	validKind func(string) bool) (Attachment, string, error) {
	item, key, _, err := s.stageBusinessAttachment(w, r, validKind, nil)
	return item, key, err
}

// StageBusinessAttachmentForReport 校验整改材料表单并返回发起页所读取的报告标识。
// 参数 w、r 和 validKind 描述上传请求，validReport 检查报告标识格式。
// 返回值 受控材料、内部存储键、页面报告标识，或表单和文件错误。
func (s *Service) StageBusinessAttachmentForReport(w http.ResponseWriter, r *http.Request,
	validKind, validReport func(string) bool,
) (Attachment, string, string, error) {
	return s.stageBusinessAttachment(w, r, validKind, validReport)
}

// stageBusinessAttachment 解析精确匹配业务声明的材料表单并受控落盘。
// 参数 w 和 r 用于限制请求，validKind 校验类别，validReport 非空时需要报告标识。
// 返回值 材料摘要、存储键、可选报告标识，或请求校验和写入错误。
func (s *Service) stageBusinessAttachment(w http.ResponseWriter, r *http.Request,
	validKind, validReport func(string) bool,
) (Attachment, string, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPackageSize+(64<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			return Attachment{}, "", "", ErrFileSize
		}
		return Attachment{}, "", "", ErrInvalid
	}
	defer r.MultipartForm.RemoveAll()
	values, files := r.MultipartForm.Value, r.MultipartForm.File
	var report string
	if validReport != nil {
		ids := values["reportId"]
		if len(ids) != 1 || !validReport(ids[0]) {
			return Attachment{}, "", "", ErrInvalid
		}
		report = ids[0]
		delete(values, "reportId")
	}
	if len(values) != 1 || len(values["kind"]) != 1 || len(files) != 1 ||
		len(files["file"]) != 1 || !validKind(values["kind"][0]) {
		return Attachment{}, "", "", ErrInvalid
	}
	part := files["file"][0]
	source, err := part.Open()
	if err != nil {
		return Attachment{}, "", "", err
	}
	defer source.Close()
	result, err := stageFile(s.attachmentDir, part.Filename, source)
	result.Kind = values["kind"][0]
	return result.Attachment, result.fileKey, report, err
}

// ServeBusinessAttachment 以禁止嗅探的下载响应发送已验证归属的受控文件。
// 参数 w 和 r 为下载请求，material 为公开附件信息，key 为调用方已校验归属的内部键。
// 返回值 文件已写出时为 nil，打开文件失败时返回错误。
func (s *Service) ServeBusinessAttachment(w http.ResponseWriter, r *http.Request,
	material Attachment, key string) error {
	file, err := openAttachment(s.attachmentDir, key)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("attachment", map[string]string{"filename": material.FileName}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, material.FileName, material.UploadedAt, file)
	return nil
}

// RemoveUnregisteredAttachment 删除调用方已确认未被本次写入引用的受控文件。
// 参数 key 为上传生成的内部文件键。
// 返回值 清理成功时为 nil，键不合法或文件操作失败时返回错误。
func (s *Service) RemoveUnregisteredAttachment(key string) error {
	if len(key) != 48 {
		return ErrNotFound
	}
	if _, err := hex.DecodeString(key); err != nil {
		return ErrNotFound
	}
	root, err := os.OpenRoot(s.attachmentDir)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.Remove(key)
}
