package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// TestAIReviewAssets 用途：审核发布只开放固定产物，不暴露凭据及源码。
// 参数：t 为测试环境。
// 返回值：无，静态路径或浏览器安全策略偏差时失败。
func TestAIReviewAssets(t *testing.T) {
	mux := http.NewServeMux()
	RegisterAIReview(mux, fstest.MapFS{"integrated.html": {Data: []byte("review-page")}, "review-platform.mjs": {Data: []byte("export const ok=true")}, "styles.css": {Data: []byte("body{}")}, "secret.env": {Data: []byte("private")}})
	for _, item := range []struct {
		path   string
		status int
	}{{"/review/", 200}, {"/review/review-platform.mjs", 200}, {"/review/styles.css", 200}, {"/review/secret.env", 404}, {"/review/AGENTS.md", 404}, {"/review/auth.js", 404}, {"/review/assets/signin-base.webp", 503}} {
		r := httptest.NewRequest("GET", item.path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != item.status {
			t.Errorf("%s %d", item.path, w.Code)
		}
		if item.status == 200 && (w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Header().Get("Content-Security-Policy"), "unsafe-inline")) {
			t.Error("静态策略未收紧")
		}
	}
}
