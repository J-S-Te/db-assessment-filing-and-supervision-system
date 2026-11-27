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
		role Role
		pattern string
		want bool
	}{
		{SystemAdmin, "POST /api/v1/alerts/rules", true},
		{Regulator, "POST /api/v1/alerts/rules", false},
		{Regulator, "GET /api/v1/knowledge/{id}", true},
		{Regulator, "GET /api/v1/archives/units", false},
		{Regulator, "GET /api/v1/filings/{id}", true},
		{OrgAdmin, "GET /api/v1/institution/reports/{id}", true},
		{OrgAdmin, "POST /api/v1/institution/filings/{id}/review-actions", false},
		{OrgAdmin, "GET /api/v1/dashboard/statistics", false},
		{Operator, "GET /api/v1/operator/filings", true},
		{Operator, "GET /api/v1/institution/filings", false},
		{SystemAdmin, "GET /api/v1/operator/filings", false},
		{"", "GET /api/v1/filings", false},
	} {
		if got := allowed(item.role, item.pattern); got != item.want {
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
