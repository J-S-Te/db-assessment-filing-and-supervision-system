package app

import (
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"grade-business/internal/httpx"
)

// LocalBoundary 约束业务版的服务器地址、内网客户端和浏览器请求来源。
// 参数 address 为监听地址，lan 为可选内网网段，next 为业务及静态页面处理器。
// 返回值 已应用访问范围及浏览器安全响应头的处理器。
func LocalBoundary(address string, lan netip.Prefix, next http.Handler) http.Handler {
	_, port, _ := net.SplitHostPort(address)
	allowed := map[string]bool{
		net.JoinHostPort("127.0.0.1", port): true,
		net.JoinHostPort("localhost", port): true,
		net.JoinHostPort("::1", port):       true,
	}
	if lan.IsValid() {
		allowed = map[string]bool{address: true}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; object-src 'none'; base-uri 'none'; "+
				"frame-ancestors 'none'; form-action 'self'")
		if !allowed[r.Host] {
			httpx.Error(w, 400, "invalid_host", "请使用服务配置的地址访问业务页面")
			return
		}
		if lan.IsValid() {
			remote, err := netip.ParseAddrPort(r.RemoteAddr)
			if err != nil || !lan.Contains(remote.Addr().Unmap()) {
				httpx.Error(w, 403, "outside_lan", "当前设备不在业务服务的内网网段")
				return
			}
		}
		if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r.Host) {
			httpx.Error(w, 403, "invalid_origin", "请求来源与业务页面不一致")
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" &&
			(r.Method != http.MethodGet && r.Method != http.MethodHead || strings.HasPrefix(r.URL.Path, "/api/")) {
			httpx.Error(w, 403, "invalid_origin", "请直接访问业务页面")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin 判断浏览器来源是否指向本业务主机，同时兼容 HTTP 内网与 HTTPS 部署。
// 参数 origin 为请求的 Origin 头，host 为当前请求的 Host 头。
// 返回值 来源主机端口与本机一致且方案为 http 或 https 时为真。
func sameOrigin(origin, host string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.Host != host {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

// Frontend 提供应用页面与缓存友好的构建资源。
// 参数 assets 为前端构建产物所在的只读文件系统。
// 返回值 前端处理器或构建产物读取错误。
func Frontend(assets fs.FS) (http.Handler, error) {
	html, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, err
	}
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if appRoute(r.URL.Path) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-cache")
				if r.Method != http.MethodHead {
					_, _ = w.Write(html)
				}
				return
			}
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				name := strings.TrimPrefix(r.URL.Path, "/")
				// 预压缩文件只通过原资源地址协商，缓存和文件类型仍沿用原资源。
				if !strings.HasSuffix(name, ".gz") {
					if info, err := fs.Stat(assets, name); err == nil && !info.IsDir() {
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
						serveFrontendAsset(w, r, assets, files, name)
						return
					}
				}
			}
		}
		httpx.Error(w, 404, "not_found", "未找到该业务页面或接口")
	}), nil
}

// appRoute 判断地址是否属于实际业务模块的客户端路由。
// 参数 path 为用户访问的路径。
// 返回值 true 表示可以回退到统一的应用入口。
func appRoute(path string) bool {
	switch path {
	case "/", "/login", "/account/password", "/alerts", "/monitor/publications", "/dashboard", "/audit", "/flow", "/tag-catalog",
		"/archives/directory", "/institution/directory", "/operator/assessment-history", "/workday-calendar",
		"/filing-material-policy", "/institution-accounts", "/operator-register", "/operator-registrations":
		return true
	}
	module, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	switch module {
	case "filings", "filing-applications", "implementations", "reports", "remediations", "hazards", "knowledge":
		return true
	case "supervisions", "material-expiries", "assessment-dues":
		detail := strings.TrimPrefix(path, "/"+module+"/")
		return path == "/"+module || (detail != "" && !strings.Contains(detail, "/"))
	case "institution", "operator":
		return scopedAppRoute(path, module)
	case "archives":
		section, id, detail := strings.Cut(strings.TrimPrefix(path, "/archives/"), "/")
		return (section == "units" || section == "systems") &&
			(!detail || (id != "" && !strings.Contains(id, "/")))
	default:
		return false
	}
}

// scopedAppRoute 判断本地机构端和运营者端的真实页面入口。
// 参数 path 为浏览器路径，module 为路径首段表示的办理端。
// 返回值 属于已注册工作台、收件箱、业务列表、表单和详情时为真。
func scopedAppRoute(path, module string) bool {
	item := strings.TrimPrefix(path, "/"+module+"/")
	if item == "workbench" || item == "feedback" || item == "publications" || item == "alerts" {
		return true
	}
	resource, detail, found := strings.Cut(item, "/")
	if resource == "material-expiries" || resource == "assessment-dues" {
		return !found || (detail != "new" && scopedRecordRoute(detail, false))
	}
	if module == "operator" {
		switch resource {
		case "remediations", "implementations":
			return !found || scopedRecordRoute(detail, true)
		case "filings", "filing-applications":
			return !found || scopedRecordRoute(detail, false)
		case "hazards", "supervisions":
			return !found || (detail != "new" && scopedRecordRoute(detail, false))
		default:
			return false
		}
	}
	switch resource {
	case "hazards":
		return !found || (detail != "new" && scopedRecordRoute(detail, false))
	case "filings", "filing-applications", "implementations", "reports":
		return !found || scopedRecordRoute(detail, resource == "reports" || resource == "implementations")
	default:
		return false
	}
}

// scopedRecordRoute 判断记录层路径是否可加载详情、新建或编辑。
// 参数 detail 为资源后的子路径，canEdit 表示是否有单独编辑页。
// 返回值 可作为客户端记录入口时为真。
func scopedRecordRoute(detail string, canEdit bool) bool {
	id, action, found := strings.Cut(detail, "/")
	return id != "" && (!found || (canEdit && id != "new" && action == "edit"))
}
