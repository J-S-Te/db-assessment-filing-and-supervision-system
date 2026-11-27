package cases

import (
	"context"
	"errors"
	"io"
	"os/exec"
)

var ErrPDFValidationUnavailable = errors.New("local PDF validation unavailable")

// ValidatePublicationPDF 在发布资料前检查已暂存的 PDF 是否具备可打开的页面结构。
// 参数 ctx 为整批资料校验的限时上下文，key 为数据库保存的受控存储键。
// 返回值 解析成功时为 nil，或文件类型、存储和本地工具错误。
func (s *Service) ValidatePublicationPDF(ctx context.Context, key string) error {
	file, err := openAttachment(s.attachmentDir, key)
	if err != nil {
		return err
	}
	defer file.Close()

	command := exec.CommandContext(ctx, "pdfinfo", "-f", "1", "-l", "1", "-")
	command.Stdin, command.Stdout, command.Stderr = file, io.Discard, io.Discard
	err = command.Run()
	if err == nil {
		return nil
	}
	var exited *exec.ExitError
	if ctx.Err() == nil && errors.As(err, &exited) {
		return ErrFileType
	}
	return ErrPDFValidationUnavailable
}
