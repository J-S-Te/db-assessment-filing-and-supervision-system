package access

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestPolicy 核对三端隔离、辖区入口和系统配置权限。
// 参数 t 用于报告授权矩阵偏差。
// 返回值 无。
func TestPolicy(t *testing.T) {
	for _, item := range []struct {
		role    Role
		pattern string
		want    bool
	}{
		{SystemAdmin, "POST /api/v1/alerts/rules", true},
		{Regulator, "POST /api/v1/alerts/rules", false},
		{Regulator, "GET /api/v1/knowledge/{id}", true},
		{Regulator, "GET /api/v1/archives/units", false},
		{Regulator, "GET /api/v1/filings/{id}", true},
		{OrgAdmin, "GET /api/v1/institution/reports/{id}", true},
		{OrgAdmin, "POST /api/v1/institution/filings/{id}/review-actions", true},
		{OrgAdmin, "POST /api/v1/institution/filing-applications/{id}/review-actions", true},
		{OrgAdmin, "POST /api/v1/institution/reports/{id}/review-actions", false},
		{OrgAdmin, "GET /api/v1/dashboard/statistics", false},
		{Operator, "GET /api/v1/operator/filings", true},
		{Operator, "GET /api/v1/institution/filings", false},
		{SystemAdmin, "GET /api/v1/operator/filings", false},
		{"", "GET /api/v1/filings", false},
	} {
		if got := allowed(Principal{Role: item.role}, item.pattern); got != item.want {
			t.Errorf("%s %s = %v, want %v", item.role, item.pattern, got, item.want)
		}
	}
}

// TestBindSubject 验证客户端查询参数不能覆盖会话归属。
// 参数 t 用于报告辖区绑定偏差。
// 返回值 无。
func TestBindSubject(t *testing.T) {
	actor := Principal{Role: Operator, Subject: "单位甲"}
	pattern := "GET /api/v1/operator/filings"
	for _, query := range []string{"", "?organization=单位甲", "?organization=单位乙",
		"?organization=单位甲&organization=单位乙", "?institution=机构乙", "?organization=%ZZ"} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/operator/filings"+query, nil)
		ok := bindSubject(r, actor, pattern)
		want := query == "" || query == "?organization=单位甲"
		if ok != want || ok && r.URL.Query().Get("organization") != "单位甲" {
			t.Errorf("bindSubject(%q) = %v, want %v", query, ok, want)
		}
	}
}

// TestBindSubjectAssignedReview 核对机构待初审筛选只对机构备案集合开放。
// 参数 t 用于报告待初审筛选的越权进入偏差。
// 返回值 无。
func TestBindSubjectAssignedReview(t *testing.T) {
	actor := Principal{Role: OrgAdmin, Subject: "机构甲"}
	for _, item := range []struct {
		pattern, query string
		want           bool
	}{
		{"GET /api/v1/institution/filings", "?review=assigned", true},
		{"GET /api/v1/institution/filing-applications", "?review=assigned", true},
		{"GET /api/v1/institution/hazards", "?review=assigned", false},
		{"GET /api/v1/institution/filings", "?review=assigned&institution=机构乙", false},
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/institution/filings"+item.query, nil)
		if ok := bindSubject(r, actor, item.pattern); ok != item.want {
			t.Errorf("bindSubject(%q %q) = %v, want %v", item.pattern, item.query, ok, item.want)
		}
	}
}

// TestAIReviewPolicy 用途：核验用户明确授权的 M8 角色范围和菜单一致性。
// 参数：t 为测试环境。
// 返回值：无，角色越权或缺少页面声明时失败。
func TestAIReviewPolicy(t *testing.T) {
	for _, role := range []Role{SystemAdmin, Regulator, OrgAdmin, Operator, "ROLE_UNKNOWN"} {
		want := role == SystemAdmin || role == Regulator
		for _, pattern := range []string{aiReviewContextPattern, "GET /review/{$}", "GET /review/{asset...}", "GET /review"} {
			if allowed(Principal{Role: role}, pattern) != want {
				t.Errorf("%s %s", role, pattern)
			}
		}
		v := view(Principal{Role: role})
		found := false
		for _, page := range v.Pages {
			if page == "/review" {
				found = true
			}
		}
		if found != want {
			t.Errorf("页面声明 %s", role)
		}
	}
}

// TestAIReviewRejectsForgedIdentity 用途：匿名用户不能用伪造身份头进入 M8。
// 参数：t 为测试环境。
// 返回值：无；页面须返回原登录入口，API 须拒绝。
func TestAIReviewRejectsForgedIdentity(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /review/{$}", func(w http.ResponseWriter, r *http.Request) { t.Error("匿名到达页面处理器") })
	h := New(nil).Protect(mux)
	for _, path := range []string{"/review/", "/api/v1/ai-review/context"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-User-ID", "admin")
		r.Header.Set("X-Role", "ROLE_SYS_ADMIN")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 401
		if path == "/review/" {
			want = 303
			if w.Header().Get("Location") != "/login?redirect=%2Freview%2F" {
				t.Error("未回到平台登录")
			}
		}
		if w.Code != want {
			t.Errorf("%s %d", path, w.Code)
		}
	}
}
