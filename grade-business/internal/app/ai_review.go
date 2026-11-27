package app

import (
	"bytes"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"time"
)

// RegisterAIReview 用途：挂载由平台会话网关保护的独立审核页面及固定静态文件。
// 参数：mux 为应用路由；files 为发布包中的审核前端文件系统。
// 返回值：无；缺少构建资源时返回 503，未知文件返回 404。
// 约束：调用方必须由 access.Protect 包装，不直接对外暴露未鉴权的 mux。
func RegisterAIReview(mux *http.ServeMux, files fs.FS) {
	mux.HandleFunc("GET /review", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/review/", http.StatusSeeOther) })
	serve := func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("asset")
		if name == "" {
			name = "integrated.html"
		}
		allowed := map[string]bool{"integrated.html": true, "review-platform.mjs": true, "styles.css": true, "assets/signin-space.webp": true, "assets/signin-shield.webp": true, "assets/signin-base.webp": true}
		if !allowed[name] {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(files, name)
		if err != nil {
			http.Error(w, "审核前端尚未发布，请联系管理员同步审核页面构建产物。", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	}
	mux.HandleFunc("GET /review/{$}", serve)
	mux.HandleFunc("GET /review/{asset...}", serve)
}
