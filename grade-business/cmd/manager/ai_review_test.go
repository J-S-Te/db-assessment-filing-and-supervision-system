package main

import (
	"grade-business/internal/access"
	"grade-business/internal/app"
	"grade-business/internal/cases"
	"grade-business/internal/precheck"
	"grade-business/migrations"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAIReviewOnIsolatedDB 用途：通过原平台登录验证 M8 会话、角色、辖区和撤权。
// 参数：t 为测试环境，数据库来自显式隔离配置。
// 返回值：无；没有数据库配置时明确跳过，其余偏差失败。
func TestAIReviewOnIsolatedDB(t *testing.T) {
	dsn := os.Getenv("GRADE_ACCESS_TEST_DSN")
	if dsn == "" {
		t.Skip("需要单独隔离的 GRADE_ACCESS_TEST_DSN")
	}
	db := accessTestSchema(t, dsn)
	if err := migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	unit, otherUnit, org, otherOrg := accessSubjects(t, db)
	ownF := accessFiling(t, db, "AI-OWN", "权限单位甲", "权限机构甲", unit, org)
	otherF := accessFiling(t, db, "AI-OTHER", "权限单位乙", "权限机构乙", otherUnit, otherOrg)
	makeReport := func(filing, num string) string {
		var id string
		err := db.QueryRow(t.Context(), `INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,filing_id,risk) SELECT 'report',$2,system_name,organization_name,assessment_org,id,'low' FROM business_cases WHERE id=$1 RETURNING id::text`, filing, num).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	own := makeReport(ownF, "AI-REPORT-OWN")
	other := makeReport(otherF, "AI-REPORT-OTHER")
	assets := t.TempDir()
	if err := os.Mkdir(filepath.Join(assets, "review"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "review/integrated.html"), []byte("real-ai-review-entry"), 0600); err != nil {
		t.Fatal(err)
	}
	records, err := cases.New(db, t.TempDir(), "M8测试")
	if err != nil {
		t.Fatal(err)
	}
	h := routes(settings{address: "127.0.0.1:18549", assets: assets, reviewWindows: app.ReviewWindows{}}, db, records, precheck.New(db, materialSource(records)), http.NotFoundHandler())
	hit := accessRequest(t, h)
	accounts := []access.AccountInput{{Username: "ai-admin", Name: "合成管理员", Role: access.SystemAdmin, Password: testInitialPassword, PersonKey: "ai-person-1"}, {Username: "ai-area", Name: "合成监管员", Role: access.Regulator, Password: testInitialPassword, PersonKey: "ai-person-2", AreaCode: "331002"}, {Username: "ai-org", Name: "合成机构", Role: access.OrgAdmin, Password: testInitialPassword, SubjectID: org}, {Username: "ai-unit", Name: "合成运营者", Role: access.Operator, Password: testInitialPassword, SubjectID: unit}}
	for _, a := range accounts {
		if _, err = access.Provision(t.Context(), db, a); err != nil {
			t.Fatal(err)
		}
	}
	hit(nil, "GET", "/review/", nil, 303)
	hit(nil, "GET", "/api/v1/ai-review/context", nil, 401)
	first := hit(nil, "POST", "/api/v1/auth/login", map[string]string{"username": "ai-admin", "password": testInitialPassword}, 200).Result().Cookies()[0]
	hit(first, "GET", "/review/", nil, 303)
	hit(first, "GET", "/api/v1/ai-review/context", nil, 403)
	admin := accessLogin(t, hit, "ai-admin")
	area := accessLogin(t, hit, "ai-area", "331002")
	institution := accessLogin(t, hit, "ai-org")
	operator := accessLogin(t, hit, "ai-unit")
	for _, cookie := range []*http.Cookie{admin, area} {
		hit(cookie, "GET", "/review/", nil, 200)
		res := hit(cookie, "GET", "/api/v1/ai-review/context", nil, 200)
		if !strings.Contains(res.Body.String(), "admin.ai-review.view") {
			t.Fatal("上下文未声明审核入口")
		}
	}
	for _, cookie := range []*http.Cookie{institution, operator} {
		hit(cookie, "GET", "/review/", nil, 403)
		hit(cookie, "GET", "/api/v1/ai-review/context", nil, 403)
	}
	res := hit(area, "GET", "/api/v1/reports", nil, 200).Body.String()
	if !strings.Contains(res, own) || strings.Contains(res, other) {
		t.Fatalf("辖区串读 %s", res)
	}
	hit(area, "GET", "/api/v1/reports/"+other, nil, 404)
	hit(area, "GET", "/api/v1/reports/"+other+"/attachments", nil, 404)
	hit(admin, "GET", "/api/v1/reports/"+other, nil, 200)
	if _, err = db.Exec(t.Context(), `UPDATE access_accounts SET enabled=false WHERE username='ai-area'`); err != nil {
		t.Fatal(err)
	}
	hit(area, "GET", "/api/v1/ai-review/context", nil, 401)
	hit(area, "GET", "/review/", nil, 303)
	hit(admin, "POST", "/api/v1/auth/logout", nil, 204)
	hit(admin, "GET", "/review/", nil, 303)
}
