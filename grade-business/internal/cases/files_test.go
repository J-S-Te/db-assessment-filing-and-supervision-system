package cases

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

// TestStageFileFormats 核对有效材料落盘及无效材料清理。
// 参数 t 为测试上下文。
// 返回值 无。
func TestStageFileFormats(t *testing.T) {
	dir := t.TempDir()
	valid := documentArchive(t, "[Content_Types].xml", "word/document.xml")
	wrong := documentArchive(t, "[Content_Types].xml", "xl/workbook.xml")
	material, err := stageFile(dir, "定级报告.docx", bytes.NewReader(valid))
	if err != nil || material.Size != int64(len(valid)) || material.FileName != "定级报告.docx" {
		t.Fatalf("valid upload = %+v, %v", material, err)
	}
	for _, tc := range []struct {
		name  string
		bytes []byte
		err   error
	}{
		{"错误格式.docx", wrong, ErrFileType},
		{"空材料.pdf", nil, ErrFileSize},
		{"无内容.zip", documentArchive(t, "empty/"), ErrFileType},
		{"普通文本.pdf", []byte("本地测试材料"), ErrFileType},
	} {
		if _, err := stageFile(dir, tc.name, bytes.NewReader(tc.bytes)); !errors.Is(err, tc.err) {
			t.Errorf("stageFile(%q) = %v", tc.name, err)
		}
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 || files[0].Name() != material.fileKey {
		t.Fatalf("unexpected persisted files: %v, %v", files, err)
	}
}

// documentArchive 生成只包含指定成员的本地测试归档。
// 参数 t 为测试上下文，names 为成员名。
// 返回值 ZIP 字节。
func documentArchive(t *testing.T, names ...string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range names {
		member, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(name, "/") {
			if _, err := member.Write([]byte("fixture")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
