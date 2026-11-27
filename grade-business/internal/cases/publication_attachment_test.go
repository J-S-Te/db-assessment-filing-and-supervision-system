package cases

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStagePublicationAttachmentPreservesBytes 核对公文附件的原始字节、摘要和清理。
// 参数 t 为使用独立目录的测试上下文。
// 返回值 无。
func TestStagePublicationAttachmentPreservesBytes(t *testing.T) {
	service := Service{attachmentDir: t.TempDir()}
	for _, file := range []struct {
		name string
		body []byte
	}{
		{"测评通报.PDF", []byte(filingPackagePDF)},
		{"工作通知.docx", validPublicationDOCXFixture(t)},
	} {
		t.Run(file.name, func(t *testing.T) {
			material, key, digest, err := service.StagePublicationAttachment(file.name, bytes.NewReader(file.body))
			if err != nil {
				t.Fatal(err)
			}
			expected := sha256.Sum256(file.body)
			if material.FileName != file.name || material.Size != int64(len(file.body)) ||
				digest != hex.EncodeToString(expected[:]) {
				t.Fatalf("暂存附件 = %+v，摘要 = %q", material, digest)
			}
			onDisk, err := os.ReadFile(filepath.Join(service.attachmentDir, key))
			if err != nil || !bytes.Equal(onDisk, file.body) {
				t.Fatalf("存储的文件应与原始附件逐字节相同: %v", err)
			}
			if err := service.RemoveUnregisteredAttachment(key); err != nil {
				t.Fatal(err)
			}
		})
	}
	assertEmptyPublicationAttachmentDirectory(t, service.attachmentDir)
}

// TestStagePublicationAttachmentRejectsBadFiles 核对损坏、伪装和超限文件都不落盘。
// 参数 t 为使用独立目录的测试上下文。
// 返回值 无。
func TestStagePublicationAttachmentRejectsBadFiles(t *testing.T) {
	service := Service{attachmentDir: t.TempDir()}
	for _, file := range []struct {
		name string
		body []byte
		err  error
	}{
		{"头尾不全.pdf", []byte("%PDF-1.7\n本地截断样本"), ErrFileType},
		{"无效版本.pdf", []byte("%PDF-test\n%%EOF\n"), ErrFileType},
		{"公开通报.pdf", nil, ErrFileSize},
		{"超过大小.pdf", []byte(strings.Repeat("x", maxDocumentSize+1)), ErrFileSize},
		{"普通文本.docx", []byte("测试附件"), ErrFileType},
		{"无效内部文件.docx", documentArchive(t, "[Content_Types].xml", "word/document.xml"), ErrFileType},
		{"旧格式.doc", validPublicationDOCXFixture(t), ErrFileType},
		{"电子表格.xlsx", documentArchive(t, "[Content_Types].xml", "xl/workbook.xml"), ErrFileType},
		{"打包材料.zip", documentArchive(t, "公文.txt"), ErrFileType},
		{"目录/公告.pdf", []byte(filingPackagePDF), ErrFileType},
	} {
		t.Run(file.name, func(t *testing.T) {
			_, key, digest, err := service.StagePublicationAttachment(file.name, bytes.NewReader(file.body))
			if !errors.Is(err, file.err) || key != "" || digest != "" {
				t.Fatalf("应以 %v 拒绝附加并清理，得到键 %q、摘要 %q 和错误 %v",
					file.err, key, digest, err)
			}
		})
	}
	assertEmptyPublicationAttachmentDirectory(t, service.attachmentDir)
}

// validPublicationDOCXFixture 制作含类型清单和正文的本地公文文件。
// 参数 t 为测试上下文。
// 返回值 有效 DOCX 压缩文件的原始字节。
func validPublicationDOCXFixture(t *testing.T) []byte {
	t.Helper()
	return buildFilingPackage(t,
		filingPackageFixtureMember{"[Content_Types].xml", strings.TrimSpace(`
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Override PartName="/word/document.xml"
  ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`)},
		filingPackageFixtureMember{"word/document.xml", strings.TrimSpace(`
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body><w:p><w:r><w:t>公文正文</w:t></w:r></w:p></w:body>
</w:document>`)},
	)
}

// assertEmptyPublicationAttachmentDirectory 断言暂存目录没有残留附件。
// 参数 t 为测试上下文，dir 为本用例独立的附件目录。
// 返回值 无。
func assertEmptyPublicationAttachmentDirectory(t *testing.T, dir string) {
	t.Helper()
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("附件目录残留文件: %v，读取错误 %v", files, err)
	}
}
