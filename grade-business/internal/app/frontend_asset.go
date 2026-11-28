package app

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// frontendAssetType 返回纳入静态资源预压缩的构建文件类型。
// 参数 name 为相对构建目录的文件路径。
// 返回值 对应的内容类型，空值表示直接提供原文件。
func frontendAssetType(name string) string {
	switch path.Ext(name) {
	case ".js", ".mjs", ".css":
		return mime.TypeByExtension(path.Ext(name))
	default:
		return ""
	}
}

// serveFrontendAsset 按请求能力选择静态文件的预压缩版本。
// 参数 w 和 r 为 HTTP 响应和请求，assets、files 为构建文件系统及文件服务，name 为资源路径。
// 返回值 无。
func serveFrontendAsset(w http.ResponseWriter, r *http.Request, assets fs.FS, files http.Handler, name string) {
	if contentType := frontendAssetType(name); contentType != "" {
		w.Header().Add("Vary", "Accept-Encoding")
		if acceptsGzip(r.Header.Values("Accept-Encoding")) {
			if info, err := fs.Stat(assets, name+".gz"); err == nil && info.Mode().IsRegular() {
				w.Header().Set("Content-Type", contentType)
				w.Header().Set("Content-Encoding", "gzip")
				w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
				r = r.Clone(r.Context())
				r.URL.Path += ".gz"
				r.URL.RawPath = ""
				// 多区间响应的封装由文件服务生成，直接返回完整压缩表示以避免编码歧义。
				if strings.Contains(r.Header.Get("Range"), ",") {
					r.Header.Del("Range")
				}
			}
		}
	}
	files.ServeHTTP(w, r)
}

// acceptsGzip 判断客户端能否接收 gzip，显式 gzip 权重优先于通配编码。
// 参数 values 为请求中全部 Accept-Encoding 头字段的值。
// 返回值 在 gzip 或适用的通配权重大于零时为真。
func acceptsGzip(values []string) bool {
	wildcard := false
	for _, entry := range strings.Split(strings.Join(values, ","), ",") {
		name, params, _ := strings.Cut(entry, ";")
		name = strings.TrimSpace(name)
		if strings.EqualFold(name, "gzip") {
			return encodingEnabled(params)
		}
		if name == "*" {
			wildcard = encodingEnabled(params)
		}
	}
	return wildcard
}

// encodingEnabled 判断传输编码的可接受权重。
// 参数 params 为编码名称之后的可选权重参数。
// 返回值 未指定权重或权重合法且大于零时为真。
func encodingEnabled(params string) bool {
	if strings.TrimSpace(params) == "" {
		return true
	}
	key, value, ok := strings.Cut(params, "=")
	if !ok || !strings.EqualFold(strings.TrimSpace(key), "q") {
		return false
	}
	weight, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return err == nil && weight > 0 && weight <= 1
}
