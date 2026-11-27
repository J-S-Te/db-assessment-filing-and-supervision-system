package wordtext

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
)

// TestWordPiecesReadsMixedText 验证同一 Word 正文中的压缩文字、中文表格和可见域值。
// 参数 t 为测试上下文。
// 返回值 无。
func TestWordPiecesReadsMixedText(t *testing.T) {
	source := []byte{'A', 0x80, 7, 0x68, 0x88, 0x3c, 0x68, 0x0d, 0x00}
	pieces := make([]byte, 28)
	binary.LittleEndian.PutUint32(pieces[4:], 3)
	binary.LittleEndian.PutUint32(pieces[8:], 6)
	binary.LittleEndian.PutUint32(pieces[14:], 0x40000000)
	binary.LittleEndian.PutUint32(pieces[22:], 3)
	result, err := decodePieces(source, pieces, 6)
	if err != nil {
		t.Fatal(err)
	}
	got, err := wordParagraphs(append(result, []rune("\x13FORMTEXT\x14选中值\x15")...))
	want := []string{"A€", "表格", "选中值"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded %q, want %q, err=%v", got, want, err)
	}
	binary.LittleEndian.PutUint32(pieces[22:], 100)
	if _, err := decodePieces(source, pieces, 6); !errors.Is(err, ErrInvalid) {
		t.Fatalf("越界片段应拒绝，err=%v", err)
	}
}
