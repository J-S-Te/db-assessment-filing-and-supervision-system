package cases

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

// StagePublicationAttachment 暂存校验合格的 PDF 或 DOCX 公文并计算内容摘要。
// 参数 name 为对收件方展示的文件名，source 为单份上传附件的原始字节流。
// 返回值 附件元数据、内部存储键、SHA-256 摘要或校验及存储错误。
func (s *Service) StagePublicationAttachment(name string, source io.Reader) (Attachment, string, string, error) {
	extension, err := attachmentExtension(name)
	if err != nil || (extension != ".pdf" && extension != ".docx") {
		return Attachment{}, "", "", ErrFileType
	}
	digest := sha256.New()
	staged, err := stageFileWithValidation(
		s.attachmentDir, name, io.TeeReader(source, digest), validatePublicationAttachment,
	)
	if err != nil {
		return Attachment{}, "", "", err
	}
	return staged.Attachment, staged.fileKey, hex.EncodeToString(digest.Sum(nil)), nil
}

// validatePublicationAttachment 检查已按文档上限落盘的 PDF 和 DOCX 内容。
// 参数 file 为共用存储暂存文件，extension 为已允许的扩展名，size 为实际字节数。
// 返回值 格式、内容及归档结构合格时为空，或校验与读取错误。
func validatePublicationAttachment(file *os.File, extension string, size int64) error {
	if extension == ".pdf" {
		return checkInstitutionPDF(file, size)
	}
	data := make([]byte, size)
	if _, err := file.ReadAt(data, 0); err != nil {
		return err
	}
	if !validFilingPackageDOCX(data) {
		return ErrFileType
	}
	return nil
}
