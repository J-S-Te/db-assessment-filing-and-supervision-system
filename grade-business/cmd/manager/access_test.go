package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/access"
	"grade-business/internal/cases"
	"grade-business/internal/precheck"
	"grade-business/migrations"
)

const testInitialPassword = "local-test-initial-PQ7"
const testUpdatedPassword = "local-test-updated-JK4"

// TestRoleAccessOnIsolatedDB 验证实际路由、身份会话、归属和拒绝留痕。
// 参数 t 只在显式指定隔离数据库时建立测试账号与业务记录。
// 返回值 无。
func TestRoleAccessOnIsolatedDB(t *testing.T) {
	dsn := os.Getenv("GRADE_ACCESS_TEST_DSN")
	if dsn == "" {
		t.Skip("需要单独隔离的 GRADE_ACCESS_TEST_DSN")
	}
	db := accessTestSchema(t, dsn)
	if err := migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	unitID, otherUnit, orgID, otherOrg := accessSubjects(t, db)
	own := accessFiling(t, db, "Access-A", "权限单位甲", "权限机构甲", unitID, orgID)
	other := accessFiling(t, db, "Access-B", "权限单位乙", "权限机构乙", otherUnit, otherOrg)
	records, err := cases.New(db, t.TempDir(), "权限测试")
	if err != nil {
		t.Fatal(err)
	}
	handler := routes(settings{address: "127.0.0.1:18549"}, db, records,
		precheck.New(db, materialSource(records)), http.NotFoundHandler())

	credential := func(username, name string, role access.Role) access.AccountInput {
		return access.AccountInput{Username: username, Name: name, Role: role, Password: testInitialPassword}
	}
	admin := credential("access-admin", "系统管理员", access.SystemAdmin)
	admin.PersonKey = "access-operator-1"
	regulator := credential("access-area", "椒江监管员", access.Regulator)
	regulator.PersonKey, regulator.AreaCode = "access-operator-2", "331002"
	institution := credential("access-org", "机构负责人", access.OrgAdmin)
	institution.SubjectID = orgID
	operator := credential("access-unit", "单位负责人", access.Operator)
	operator.SubjectID = unitID
	for _, account := range []access.AccountInput{admin, regulator, institution, operator} {
		if _, err := access.Provision(t.Context(), db, account); err != nil {
			t.Fatal(err)
		}
	}
	operator.Username = "access-unit-duplicate"
	if _, err := access.Provision(t.Context(), db, operator); err == nil {
		t.Fatal("同一单位绑定了第二个账号")
	}
	regulator.Username, regulator.PersonKey = "access-person-duplicate", admin.PersonKey
	if _, err := access.Provision(t.Context(), db, regulator); err == nil {
		t.Fatal("同一人员同时获得系统管理员和监管员账号")
	}

	hit := accessRequest(t, handler)
	hit(nil, "GET", "/api/v1/filings", nil, 401)
	sys := accessLogin(t, hit, admin.Username)
	area := accessLogin(t, hit, "access-area")
	org := accessLogin(t, hit, institution.Username)
	unit := accessLogin(t, hit, "access-unit")
	snapshot := hit(area, "GET", "/api/v1/filings", nil, 200).Body.String()
	if !strings.Contains(snapshot, own) || strings.Contains(snapshot, other) {
		t.Fatalf("辖区备案列表超出范围: %s", snapshot)
	}
	hit(area, "GET", "/api/v1/filings/"+own+"/review-history", nil, 200)
	hit(area, "GET", "/api/v1/filings/"+other, nil, 404)
	hit(area, "POST", "/api/v1/knowledge", map[string]string{}, 403)
	hit(area, "GET", "/api/v1/knowledge", nil, 200)
	hit(area, "GET", "/api/v1/archives/units", nil, 403)
	hit(sys, "GET", "/api/v1/filings/"+other, nil, 200)
	hit(sys, "GET", "/api/v1/institution/filings", nil, 403)
	hit(org, "GET", "/api/v1/institution/filings/"+own, nil, 200)
	hit(org, "GET", "/api/v1/institution/filings/"+other, nil, 404)
	hit(org, "POST", "/api/v1/institution/filings/"+own+"/review-actions", map[string]string{}, 403)
	hit(unit, "GET", "/api/v1/operator/filings/"+own, nil, 200)
	hit(unit, "GET", "/api/v1/operator/filings/"+other, nil, 404)
	hit(unit, "GET", "/api/v1/operator/filings?organization=其他单位", nil, 403)
	hit(unit, "GET", "/api/v1/dashboard/statistics", nil, 403)

	hit(area, "GET", "/api/v1/filings/"+other+"/attachments", nil, 404)
	hit(area, "GET", "/api/v1/filings/"+own+"/attachments", nil, 200)
	if _, err := db.Exec(t.Context(), `UPDATE business_cases
		SET status='pending_initial', operator_origin=true, submitted_at=clock_timestamp()
		WHERE id IN ($1,$2)`, own, other); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `INSERT INTO case_attachments(case_id,kind,file_name,file_key,size_bytes)
		VALUES ($1,'filing_form','form.pdf','fixture-form',16),
		       ($1,'classification_report','report.pdf','fixture-report',16)`, own); err != nil {
		t.Fatal(err)
	}
	review := map[string]any{"decision": "approve", "expectedVersion": 1}
	hit(area, "POST", "/api/v1/filings/"+other+"/review-actions", review, 404)
	hit(area, "POST", "/api/v1/filings/"+own+"/review-actions", review, 200)
	review["expectedVersion"] = 2
	blocked := hit(area, "POST", "/api/v1/filings/"+own+"/review-actions", review, 409)
	if !strings.Contains(blocked.Body.String(), "different_reviewer_required") {
		t.Fatal("终审拒绝原因不是初审终审同人")
	}
	hit(sys, "POST", "/api/v1/filings/"+own+"/review-actions", review, 200)
	var distinctActors int
	err = db.QueryRow(t.Context(), `SELECT count(DISTINCT operator_id)
		FROM case_review_events WHERE case_id=$1 AND operator_role IS NOT NULL`, own).Scan(&distinctActors)
	if err != nil || distinctActors != 2 {
		t.Fatalf("审核事件未保留两个独立账号: %d %v", distinctActors, err)
	}

	if _, err := db.Exec(t.Context(), "UPDATE directory_parties SET valid_until=current_date-2 WHERE id=$1", orgID); err != nil {
		t.Fatal(err)
	}
	hit(org, "GET", "/api/v1/institution/filings", nil, 401)
	if _, err := db.Exec(t.Context(), "UPDATE directory_parties SET valid_until=current_date+30 WHERE id=$1", orgID); err != nil {
		t.Fatal(err)
	}
	hit(org, "GET", "/api/v1/institution/filings", nil, 200)
	hit(unit, "POST", "/api/v1/auth/logout", nil, 204)
	hit(unit, "GET", "/api/v1/auth/session", nil, 401)
	var denied int
	if err := db.QueryRow(t.Context(), "SELECT count(*) FROM access_events WHERE event='denied' AND operator_id IS NOT NULL").Scan(&denied); err != nil || denied < 10 {
		t.Fatalf("未逐笔登记拒绝事件: count=%d error=%v", denied, err)
	}
}

// accessTestSchema 在显式隔离的数据库中为本次运行建立独立模式。
// 参数 t 为测试环境，dsn 为调用方给出的测试连接。
// 返回值 只使用本次运行模式并由测试清理的连接池。
func accessTestSchema(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	root, err := pgxpool.NewWithConfig(t.Context(), config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(root.Close)
	name := fmt.Sprintf("access_role_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := root.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := root.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	config.ConnConfig.RuntimeParams["search_path"] = name
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// accessSubjects 登记两区县的单位和有效期内的机构测试夹具。
// 参数 t 为测试，db 为专用隔离数据库。
// 返回值 两个单位及两个机构的稳定标识。
func accessSubjects(t *testing.T, db *pgxpool.Pool) (string, string, string, string) {
	t.Helper()
	var unit, otherUnit, org, otherOrg string
	for _, row := range []struct {
		name, code, district string
		id                   *string
	}{
		{"权限单位甲", "913310020000000001", "椒江区", &unit},
		{"权限单位乙", "913310030000000001", "黄岩区", &otherUnit},
	} {
		err := db.QueryRow(t.Context(), `INSERT INTO directory_parties(kind, scope_name, name, code, district)
			VALUES ('unit',$1,$1,$2,$3) RETURNING id::text`,
			row.name, row.code, row.district).Scan(row.id)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		name string
		id   *string
	}{{"权限机构甲", &org}, {"权限机构乙", &otherOrg}} {
		err := db.QueryRow(t.Context(), `INSERT INTO directory_parties(kind, scope_name, name, code, qualification, valid_until)
			VALUES ('institution',$1,$1,$1,'测试资质',current_date+30) RETURNING id::text`, row.name).Scan(row.id)
		if err != nil {
			t.Fatal(err)
		}
	}
	return unit, otherUnit, org, otherOrg
}

// accessFiling 创建由真实名录 ID 归属的隔离备案夹具。
// 参数 t、db 为测试环境，number、unit、org 和关联 ID 描述备案归属。
// 返回值 新测试备案标识。
func accessFiling(t *testing.T, db *pgxpool.Pool, number, unit, org, unitID, orgID string) string {
	t.Helper()
	var id string
	err := db.QueryRow(t.Context(), `INSERT INTO business_cases(kind, number, system_name, organization_name,
		assessment_org, level, directory_unit_id, directory_institution_id)
		VALUES ('filing',$1,$1,$2,$3,'2',$4,$5) RETURNING id::text`, number, unit, org, unitID, orgID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type requestAccess func(*http.Cookie, string, string, any, int) *httptest.ResponseRecorder

// accessRequest 发送带可选会话的真实应用路由请求并校验 HTTP 状态。
// 参数 t 为测试，handler 为由 cmd/manager 装配的 HTTP 路由。
// 返回值 可执行指定请求与 HTTP 断言的函数。
func accessRequest(t *testing.T, handler http.Handler) requestAccess {
	return func(cookie *http.Cookie, method, path string, input any, status int) *httptest.ResponseRecorder {
		t.Helper()
		var body []byte
		if input != nil {
			body, _ = json.Marshal(input)
		}
		r := httptest.NewRequest(method, "http://127.0.0.1:18549"+path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s 实际 %d，预期 %d: %s", method, path, w.Code, status, w.Body.String())
		}
		return w
	}
}

// accessLogin 执行首次登录、改密、旧会话撤销与正式重登。
// 参数 t 为测试，hit 为实际 HTTP 请求入口，username 为测试账号。
// 返回值 已完成首登要求的新会话 Cookie。
func accessLogin(t *testing.T, hit requestAccess, username string) *http.Cookie {
	t.Helper()
	first := hit(nil, "POST", "/api/v1/auth/login",
		map[string]string{"username": username, "password": testInitialPassword}, 200).Result().Cookies()[0]
	if !first.HttpOnly || first.SameSite != http.SameSiteStrictMode {
		t.Fatal("会话 Cookie 缺少浏览器防护")
	}
	hit(first, "GET", "/api/v1/filings", nil, 403)
	hit(first, "POST", "/api/v1/auth/password",
		map[string]string{"currentPassword": testInitialPassword, "newPassword": testUpdatedPassword}, 204)
	hit(first, "GET", "/api/v1/auth/session", nil, 401)
	return hit(nil, "POST", "/api/v1/auth/login",
		map[string]string{"username": username, "password": testUpdatedPassword}, 200).Result().Cookies()[0]
}
