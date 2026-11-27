package cases

import (
	"context"
	"errors"
	"os"
)

var ErrReportFormat = errors.New("attachment is not a supported report source")

// OpenReportSource 打开归属于测评报告的 DOCX 或 PDF 正文附件。
// 参数 ctx 为请求上下文，id 和 attachmentID 是测评及材料的标识。
// 返回值 已打开的附件、公开元数据，或归属、格式及文件错误。
func (s *Service) OpenReportSource(
	ctx context.Context, id, attachmentID string,
) (*os.File, Attachment, error) {
	row, err := s.readAttachment(ctx, report, id, attachmentID, "")
	if err != nil {
		return nil, Attachment{}, err
	}
	extension, err := attachmentExtension(row.FileName)
	if err != nil || row.Kind != "report_file" || (extension != ".docx" && extension != ".pdf") {
		return nil, Attachment{}, ErrReportFormat
	}
	file, err := openAttachment(s.attachmentDir, row.fileKey)
	return file, row.Attachment, err
}
