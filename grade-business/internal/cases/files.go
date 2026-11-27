package cases

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxDocumentSize = 20 << 20
	maxPackageSize  = 200 << 20
)

var (
	ErrFileType = errors.New("unsupported attachment content or name")
	ErrFileSize = errors.New("empty or oversized attachment")
)

// attachmentExtension 校验要向用户展示的文件名和允许的附件格式。
// 参数 name 为上传表单提供的文件名。
// 返回值 小写扩展名，或文件类型与名称错误。
func attachmentExtension(name string) (string, error) {
	if !validText(name, 150) || strings.ContainsAny(name, "/\\") ||
		strings.IndexFunc(name, unicode.IsControl) >= 0 || !utf8.ValidString(name) {
		return "", ErrFileType
	}
	switch extension := strings.ToLower(filepath.Ext(name)); extension {
	case ".pdf", ".doc", ".docx", ".xlsx", ".zip":
		return extension, nil
	default:
		return "", ErrFileType
	}
}

// stageFile 限量写入并校验上传文件，然后赋予独立生成的存储标识。
// 参数 directory 为受控附件目录，name 为显示文件名，source 为上传数据。
// 返回值 临时登记的附件元数据，或文件校验及持久化错误。
func stageFile(directory, name string, source io.Reader) (attachmentRow, error) {
	return stageFileWithValidation(directory, name, source, checkFileContent)
}

// stageFileWithValidation 保存校验合格的文件，允许指定业务内容校验策略。
// 参数 directory、name、source 为存储目录、显示名称和文件流，validate 为校验器。
// 返回值 文件元数据与内部存储键，或格式、业务规则及存储错误。
func stageFileWithValidation(directory, name string, source io.Reader,
	validate func(*os.File, string, int64) error,
) (attachmentRow, error) {
	extension, err := attachmentExtension(name)
	if err != nil {
		return attachmentRow{}, err
	}
	file, err := os.CreateTemp(directory, ".upload-*")
	if err != nil {
		return attachmentRow{}, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	size, err := writeValidatedFileWithPolicy(file, source, extension, validate)
	if err != nil {
		return attachmentRow{}, err
	}
	var random [24]byte
	rand.Read(random[:])
	key := hex.EncodeToString(random[:])
	if err := os.Link(file.Name(), filepath.Join(directory, key)); err != nil {
		return attachmentRow{}, err
	}
	return attachmentRow{Attachment: Attachment{FileName: name, Size: size}, fileKey: key}, nil
}

// writeValidatedFile 按文件种类限量复制并核对内容。
// 参数 file 为临时材料，source 为上传数据，extension 为已验证的扩展名。
// 返回值 已写入大小，或文件格式、体积及写入错误。
func writeValidatedFile(file *os.File, source io.Reader, extension string) (int64, error) {
	return writeValidatedFileWithPolicy(file, source, extension, checkFileContent)
}

// writeValidatedFileWithPolicy 限量写入材料，按调用方的文件规则验证后同步磁盘。
// 参数 file、source、extension 为目标、内容流和后缀，validate 为实际校验方式。
// 返回值 文件字节数或体积、内容、存储错误。
func writeValidatedFileWithPolicy(file *os.File, source io.Reader, extension string,
	validate func(*os.File, string, int64) error,
) (int64, error) {
	var limit int64 = maxDocumentSize
	if extension == ".zip" {
		limit = maxPackageSize
	}
	size, err := io.Copy(file, io.LimitReader(source, limit+1))
	if err != nil {
		return 0, err
	}
	if size == 0 || size > limit {
		return 0, ErrFileSize
	}
	if err := validate(file, extension, size); err != nil {
		return 0, err
	}
	return size, file.Sync()
}

// checkFileContent 区分 PDF 与支持的压缩文件结构。
// 参数 file 为已写入附件，extension 为扩展名，size 为实际文件大小。
// 返回值 文件不符合允许的格式时返回类型错误。
func checkFileContent(file *os.File, extension string, size int64) error {
	if extension == ".doc" {
		return ErrFileType
	}
	if extension == ".pdf" {
		var signature [5]byte
		if _, err := file.ReadAt(signature[:], 0); err != nil || string(signature[:]) != "%PDF-" {
			return ErrFileType
		}
		return nil
	}
	reader, err := zip.NewReader(file, size)
	if err != nil || len(reader.File) == 0 || len(reader.File) > 2000 {
		return ErrFileType
	}
	return checkArchiveEntries(reader.File, extension)
}

// checkInstitutionPDF 读取有界页头和文件尾，拦截常见截断上传。
// 参数 file 为限量暂存的机构材料，size 为已写入字节数。
// 返回值 格式合格时为 nil，或内容、磁盘读取错误。
func checkInstitutionPDF(file *os.File, size int64) error {
	const headerSize, tailSize = 9, 1024
	if size < headerSize+5 {
		return ErrFileType
	}
	var content [headerSize + tailSize]byte
	if _, err := file.ReadAt(content[:headerSize], 0); err != nil {
		return err
	}
	tail := min(size-headerSize, tailSize)
	if _, err := file.ReadAt(content[headerSize:headerSize+tail], size-tail); err != nil {
		return err
	}
	if !validFilingPackagePDF(content[:headerSize+tail]) {
		return ErrFileType
	}
	return nil
}

// checkArchiveEntries 校验归档成员的相对路径及 Office 文档预期结构。
// 参数 entries 为归档目录成员，extension 为 ZIP 或 Office 文档扩展名。
// 返回值 发现异常成员或缺少必要文档结构时返回类型错误。
func checkArchiveEntries(entries []*zip.File, extension string) error {
	contentTypes, document, workbook, hasContent := false, false, false, false
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name, "/")
		if name == "." || !fs.ValidPath(name) || strings.ContainsAny(name, "\\:") ||
			entry.Mode()&os.ModeType != 0 && !entry.Mode().IsDir() {
			return ErrFileType
		}
		if entry.Mode().IsDir() {
			continue
		}
		hasContent = hasContent || entry.UncompressedSize64 > 0
		switch name {
		case "[Content_Types].xml":
			contentTypes = true
		case "word/document.xml":
			document = true
		case "xl/workbook.xml":
			workbook = true
		}
	}
	if hasContent && (extension == ".zip" || contentTypes && (extension == ".docx" && document ||
		extension == ".xlsx" && workbook)) {
		return nil
	}
	return ErrFileType
}

// openAttachment 打开已登记文件并限定读取在附件目录内。
// 参数 directory 为附件目录，key 为服务端生成的存储标识。
// 返回值 已打开文件或不可用错误。
func openAttachment(directory, key string) (*os.File, error) {
	if len(key) != 48 {
		return nil, ErrNotFound
	}
	if _, err := hex.DecodeString(key); err != nil {
		return nil, ErrNotFound
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Open(key)
}

// discardUnreferenced 清理已判定未登记的上传文件，保留提交状态不明的文件。
// 参数 ctx 为原请求上下文，key 为本次上传生成的存储标识。
// 返回值 没有清理错误时返回 nil，数据库状态不明或删除错误时返回错误。
func (s *Service) discardUnreferenced(ctx context.Context, key string) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	var exists bool
	err := s.db.QueryRow(cleanup, `SELECT EXISTS(SELECT 1 FROM case_attachments WHERE file_key=$1)`,
		key).Scan(&exists)
	if err != nil || exists {
		return err
	}
	return os.Remove(filepath.Join(s.attachmentDir, key))
}
