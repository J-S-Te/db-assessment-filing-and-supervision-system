package app

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"testing/fstest"
)

// TestBoundaries 验证本地业务应用拒绝非约定主机和外站写入。
// 参数 t 为测试上下文。
// 返回值 无。
func TestBoundaries(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	handler := LocalBoundary("127.0.0.1:18444", netip.Prefix{}, next)
	for _, row := range []struct {
		host, origin, site string
		status             int
	}{
		{"127.0.0.1:18444", "http://127.0.0.1:18444", "same-origin", 204},
		{"127.0.0.1:18444", "", "", 204},
		{"127.0.0.1:18444", "http://example.test", "cross-site", 403},
		{"example.test:18444", "", "", 400},
	} {
		req := httptest.NewRequest("POST", "http://"+row.host+"/api/v1/filings", nil)
		if row.origin != "" {
			req.Header.Set("Origin", row.origin)
		}
		if row.site != "" {
			req.Header.Set("Sec-Fetch-Site", row.site)
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		if out.Code != row.status {
			t.Errorf("%s %s: %d", row.host, row.origin, out.Code)
		}
	}
}

// TestLANBoundaries 验证内网入口只接受配置地址、同网段客户端和同源 API 请求。
// 参数 t 为测试上下文。
// 返回值 无。
func TestLANBoundaries(t *testing.T) {
	const host = "192.168.3.22:18444"
	subnet := netip.MustParsePrefix("192.168.3.0/24")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	handler := LocalBoundary(host, subnet, next)
	for _, row := range []struct {
		name, remote, host, origin string
		status                     int
	}{
		{"同网段", "192.168.3.41:31234", host, "http://" + host, 204},
		{"外部网段", "192.168.4.41:31234", host, "", 403},
		{"错误主机", "192.168.3.41:31234", "localhost:18444", "", 400},
		{"外站来源", "192.168.3.41:31234", host, "http://192.168.3.41", 403},
	} {
		req := httptest.NewRequest(http.MethodPost, "http://"+row.host+"/api/v1/filings", nil)
		req.RemoteAddr = row.remote
		req.Header.Set("Origin", row.origin)
		req.Header.Set("X-Forwarded-For", "192.168.3.41")
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		if out.Code != row.status {
			t.Errorf("%s 状态码为 %d，应为 %d", row.name, out.Code, row.status)
		}
	}
}

// TestAssetAndRouteFallback 检查业务深层路由与接口错误的返回格式。
// 参数 t 为测试上下文。
// 返回值 无。
func TestAssetAndRouteFallback(t *testing.T) {
	handler, err := Frontend(fstest.MapFS{"index.html": {Data: []byte("<html>business</html>")}, "assets/app-123.js": {Data: []byte("export {}; ")}})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		path, cache string
		status      int
	}{
		{"/reports/new", "no-cache", 200}, {"/knowledge/abc", "no-cache", 200},
		{"/implementations/new", "no-cache", 200}, {"/filing-applications/abc/edit", "no-cache", 200},
		{"/remediations/abc", "no-cache", 200},
		{"/hazards/abc", "no-cache", 200},
		{"/institution/report-feedback", "no-store", 404},
		{"/login", "no-cache", 200}, {"/account/password", "no-cache", 200},
		{"/institution/workbench?institution=example", "no-cache", 200},
		{"/institution/publications?institution=example", "no-cache", 200},
		{"/institution/alerts?institution=example&alert=test", "no-cache", 200},
		{"/institution/directory?institution=example&tab=people", "no-cache", 200},
		{"/institution/directory/unrecognized", "no-store", 404},
		{"/institution/publications/unrecognized", "no-store", 404},
		{"/operator/workbench?organization=example", "no-cache", 200},
		{"/operator/workbench/unknown", "no-store", 404},
		{"/supervisions", "no-cache", 200},
		{"/material-expiries", "no-cache", 200},
		{"/institution/material-expiries/abc", "no-cache", 200},
		{"/operator/material-expiries/abc", "no-cache", 200},
		{"/institution/material-expiries/abc/extra", "no-store", 404},
		{"/supervisions/abc", "no-cache", 200},
		{"/operator/supervisions/abc?organization=example", "no-cache", 200},
		{"/operator/supervisions/abc/extra", "no-store", 404},
		{"/institution/supervisions", "no-store", 404},
		{"/institution/reports/abc", "no-cache", 200},
		{"/institution/feedback", "no-cache", 200}, {"/institution/hazards", "no-cache", 200},
		{"/institution/hazards/abc", "no-cache", 200}, {"/institution/hazards/new", "no-store", 404}, {"/institution/filing-applications/abc", "no-cache", 200},
		{"/operator/feedback", "no-cache", 200}, {"/operator/hazards", "no-cache", 200},
		{"/operator/alerts", "no-cache", 200}, {"/operator/alerts/extra", "no-store", 404}, {"/operator/hazards/abc", "no-cache", 200},
		{"/institution/filings", "no-cache", 200}, {"/institution/filings/new", "no-cache", 200},
		{"/institution/filing-applications/new", "no-cache", 200},
		{"/institution/implementations/abc/edit", "no-cache", 200},
		{"/institution/reports/abc/edit", "no-cache", 200},
		{"/operator/remediations", "no-cache", 200}, {"/operator/remediations/new", "no-cache", 200},
		{"/operator/remediations/abc/edit", "no-cache", 200},
		{"/institution/filings/abc/edit", "no-store", 404}, {"/operator/hazards/new", "no-store", 404},
		{"/operator/filings", "no-cache", 200}, {"/operator/filings/new", "no-cache", 200},
		{"/operator/filings/abc", "no-cache", 200}, {"/operator/filing-applications/new", "no-cache", 200},
		{"/operator/implementations/abc/edit", "no-cache", 200},
		{"/operator/filings/abc/edit", "no-store", 404}, {"/operator/publications", "no-cache", 200}, {"/alerts", "no-cache", 200},
		{"/monitor/publications", "no-cache", 200}, {"/dashboard", "no-cache", 200},
		{"/audit", "no-cache", 200},
		{"/flow", "no-cache", 200},
		{"/tag-catalog", "no-cache", 200},
		{"/archives/units/abc", "no-cache", 200}, {"/archives/systems/abc", "no-cache", 200},
		{"/archives/directory?tab=institutions", "no-cache", 200},
		{"/archives/directory/unknown", "no-store", 404},
		{"/archives/people", "no-store", 404}, {"/archives/systems/abc/extra", "no-store", 404},
		{"/operator/reports/abc", "no-store", 404},
		{"/api/v1/unknown", "no-store", 404}, {"/assets/missing.js", "no-store", 404},
		{"/assets/app-123.js", "public, max-age=31536000, immutable", 200},
	} {
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, httptest.NewRequest("GET", row.path, nil))
		if out.Code != row.status || out.Header().Get("Cache-Control") != row.cache {
			t.Errorf("%s: %d/%s", row.path, out.Code, out.Header().Get("Cache-Control"))
		}
	}
}
