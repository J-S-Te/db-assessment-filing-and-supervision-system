package httpx

import (
	"context"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// DownloadCSV 写完临时文件后再发送附件，确保查询出错时仍能返回业务错误。
// 参数 w 和 r 为下载响应与请求，filename 为下载名，write 负责产生完整文件。
// 返回值 下载开始前发生的临时文件或编码错误，正常发送时为 nil。
func DownloadCSV(w http.ResponseWriter, r *http.Request, filename string,
	write func(context.Context, io.Writer) error,
) error {
	file, err := os.CreateTemp("", "grade-export-*.csv")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := write(r.Context(), file); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, filename, time.Time{}, file)
	return nil
}

// CSVText 将可能被电子表格执行的业务字段保存为文本。
// 参数 value 为待导出的字符串，可含空白和分隔符。
// 返回值 必要时加文本前缀的值，CSV 引号由编码器处理。
func CSVText(value string) string {
	trimmed := strings.TrimLeftFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	})
	first, _ := utf8.DecodeRuneInString(trimmed)
	if strings.ContainsAny(value[:len(value)-len(trimmed)], "\t\r\n") ||
		strings.ContainsRune("=+-@＝＋－＠", first) {
		return "'" + value
	}
	return value
}
