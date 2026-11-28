package app

import (
	"bytes"
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// TestFrontendAssetEncoding 验证静态构建资源按客户端能力选择传输格式。
// 参数 t 为测试上下文。
// 返回值 无。
func TestFrontendAssetEncoding(t *testing.T) {
	source := []byte(strings.Repeat("export const value = 42;\n", 100))
	var encoded bytes.Buffer
	writer := gzip.NewWriter(&encoded)
	if _, err := writer.Write(source); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	handler, err := Frontend(fstest.MapFS{
		"index.html":          {Data: []byte("<html>login</html>")},
		"assets/app.js":       {Data: source},
		"assets/app.js.gz":    {Data: encoded.Bytes()},
		"assets/legacy.js":    {Data: source},
		"assets/theme.css":    {Data: source},
		"assets/theme.css.gz": {Data: encoded.Bytes()},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path, method, encoding, byteRange, wantEncoding string
		status                                                int
	}{
		{"静态压缩", "/assets/app.js", "GET", "br, GZIP;q=0.6", "", "gzip", 200},
		{"样式压缩", "/assets/theme.css", "GET", "gzip", "", "gzip", 200},
		{"HEAD", "/assets/app.js", "HEAD", "gzip", "", "gzip", 200},
		{"未声明能力", "/assets/app.js", "GET", "", "", "", 200},
		{"显式拒绝", "/assets/app.js", "GET", "*;q=1, gzip;q=0", "", "", 200},
		{"通配能力", "/assets/app.js", "GET", "*;q=0.5", "", "gzip", 200},
		{"无预压缩文件", "/assets/legacy.js", "GET", "gzip", "", "", 200},
		{"原文件范围", "/assets/app.js", "GET", "identity", "bytes=0-4", "", 206},
		{"压缩文件范围", "/assets/app.js", "GET", "gzip", "bytes=0-4", "gzip", 206},
		{"压缩多区间返回完整文件", "/assets/app.js", "GET", "gzip", "bytes=0-4,20-24", "gzip", 200},
		{"压缩文件直接访问", "/assets/app.js.gz", "GET", "gzip", "", "", 404},
		{"登录页原样响应", "/login", "GET", "gzip", "", "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Accept-Encoding", tc.encoding)
			req.Header.Set("Range", tc.byteRange)
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, req)
			if out.Code != tc.status || out.Header().Get("Content-Encoding") != tc.wantEncoding {
				t.Fatalf("状态码 %d，编码 %q", out.Code, out.Header().Get("Content-Encoding"))
			}
			if strings.HasPrefix(tc.path, "/assets/") && out.Code < 300 &&
				!strings.Contains(out.Header().Get("Vary"), "Accept-Encoding") {
				t.Fatal("静态文件应区分压缩缓存")
			}
			if tc.wantEncoding == "" {
				return
			}
			contentType, _, err := mime.ParseMediaType(out.Header().Get("Content-Type"))
			validType := contentType == "text/javascript" || contentType == "application/javascript"
			if strings.HasSuffix(tc.path, ".css") {
				validType = contentType == "text/css"
			}
			if err != nil || !validType {
				t.Fatalf("压缩资源类型 %q，错误 %v", contentType, err)
			}
			if tc.status == http.StatusPartialContent {
				if out.Result().ContentLength != 5 || !bytes.Equal(out.Body.Bytes(), encoded.Bytes()[:5]) {
					t.Fatal("续传偏移应对应已协商的压缩文件")
				}
				return
			}
			if out.Result().ContentLength != int64(encoded.Len()) {
				t.Fatalf("压缩长度 %d，预期 %d", out.Result().ContentLength, encoded.Len())
			}
			if tc.method == http.MethodHead {
				if out.Body.Len() != 0 {
					t.Fatal("HEAD 不应包含响应体")
				}
				return
			}
			coder, err := gzip.NewReader(out.Body)
			if err != nil {
				t.Fatal(err)
			}
			defer coder.Close()
			decoded, err := io.ReadAll(coder)
			if err != nil || !bytes.Equal(decoded, source) {
				t.Fatalf("解压后内容不一致，错误 %v", err)
			}
		})
	}
}
