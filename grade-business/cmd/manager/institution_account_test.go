package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/internal/access"
	"grade-business/internal/cases"
	"grade-business/internal/precheck"
	"grade-business/migrations"
)

const institutionAccountPath = "/api/v1/institution-accounts"

type institutionAccountReceipt struct {
	ID                string
	Username          string
	TemporaryPassword string
}

// TestInstitutionAccountProvisionOnIsolatedDB 验证管理端开通、唯一约束和机构首登改密。
// 参数 t 为真实应用路由及独立 PostgreSQL 验证环境。
// 返回值 无。
func TestInstitutionAccountProvisionOnIsolatedDB(t *testing.T) {
	dsn := os.Getenv("GRADE_ACCESS_TEST_DSN")
	if dsn == "" {
		t.Skip("需要单独隔离的 GRADE_ACCESS_TEST_DSN")
	}
	db := accessTestSchema(t, dsn)
	if err := migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	unit, _, org, expired := accessSubjects(t, db)
	if _, err := db.Exec(t.Context(), "UPDATE directory_parties SET valid_until=current_date-2 WHERE id=$1", expired); err != nil {
		t.Fatal(err)
	}
	adminID, areaUser := provisionInstitutionAccountTestReviewers(t, db)
	records, err := cases.New(db, t.TempDir(), "机构开户验收")
	if err != nil {
		t.Fatal(err)
	}
	handler := routes(settings{address: "127.0.0.1:18549"}, db, records,
		precheck.New(db, materialSource(records)), http.NotFoundHandler())
	hit := accessRequest(t, handler)
	admin := accessLogin(t, hit, "institution-account-admin")
	regulator := accessLogin(t, hit, areaUser, "331002")
	form := map[string]string{"institutionId": org, "name": "机构联络人", "username": "QA-MEASURE-01"}
	hit(nil, "POST", institutionAccountPath, form, 401)
	hit(regulator, "POST", institutionAccountPath, form, 403)
	assertInstitutionAccountValidation(t, hit, admin, form, unit, expired)
	response := hit(admin, "POST", institutionAccountPath, form, 201)
	var receipt institutionAccountReceipt
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil ||
		receipt.ID == "" || receipt.Username != "qa-measure-01" || len(receipt.TemporaryPassword) < 12 ||
		response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("开户回执缺少安全交接字段：%v", err)
	}
	form["username"] = "qa-measure-02"
	hit(admin, "POST", institutionAccountPath, form, 409)
	assertInstitutionAccountLogin(t, hit, receipt, org)
	assertInstitutionAccountAudit(t, db, adminID, receipt.ID)
}

// provisionInstitutionAccountTestReviewers 准备两种监管身份以核对开户授权。
// 参数 t、db 为独立测试环境及可写连接池。
// 返回值 系统管理员账号标识及普通监管员用户名。
func provisionInstitutionAccountTestReviewers(t *testing.T, db *pgxpool.Pool) (string, string) {
	t.Helper()
	adminID, err := access.Provision(t.Context(), db, access.AccountInput{
		Username: "institution-account-admin", Name: "开户系统管理员", Role: access.SystemAdmin,
		PersonKey: "institution-admin-001", Password: testInitialPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	const user = "institution-account-regulator"
	if _, err := access.Provision(t.Context(), db, access.AccountInput{
		Username: user, Name: "城区监管员", Role: access.Regulator, AreaCode: "331002",
		PersonKey: "institution-regulator-001", Password: testInitialPassword,
	}); err != nil {
		t.Fatal(err)
	}
	return adminID, user
}

// assertInstitutionAccountValidation 核对服务端拒绝额外角色、过期机构及其他主体类型。
// 参数 t、hit、actor 为测试路由和管理员会话，form 为有效表单，unit、expired 为无效开户主体。
// 返回值 无。
func assertInstitutionAccountValidation(t *testing.T, hit requestAccess, actor *http.Cookie,
	form map[string]string, unit, expired string,
) {
	t.Helper()
	form["roleCode"] = "ROLE_SYS_ADMIN"
	hit(actor, "POST", institutionAccountPath, form, 400)
	delete(form, "roleCode")
	org := form["institutionId"]
	for _, invalid := range []string{unit, expired} {
		form["institutionId"] = invalid
		hit(actor, "POST", institutionAccountPath, form, 409)
	}
	form["institutionId"] = org
}

// assertInstitutionAccountLogin 验证临时密码只允许进入改密，改密后以绑定机构进入业务。
// 参数 t、hit 为隔离测试路由，receipt 为开户回执，org 为机构主档标识。
// 返回值 无。
func assertInstitutionAccountLogin(t *testing.T, hit requestAccess, receipt institutionAccountReceipt, org string) {
	t.Helper()
	credentials := map[string]string{"username": receipt.Username, "password": receipt.TemporaryPassword}
	temporary := hit(nil, "POST", "/api/v1/auth/login", credentials, 200).Result().Cookies()[0]
	session := hit(temporary, "GET", "/api/v1/auth/session", nil, 200).Body.String()
	if !strings.Contains(session, `"mustChangePassword":true`) || !strings.Contains(session, org) ||
		!strings.Contains(session, "ROLE_ORG_ADMIN") {
		t.Fatal("新账号缺少首登要求或未绑定指定机构")
	}
	hit(temporary, "GET", "/api/v1/institution/filings", nil, 403)
	hit(temporary, "POST", "/api/v1/auth/password", map[string]string{
		"currentPassword": receipt.TemporaryPassword, "newPassword": testUpdatedPassword,
	}, 204)
	hit(temporary, "GET", "/api/v1/auth/session", nil, 401)
	hit(nil, "POST", "/api/v1/auth/login", credentials, 401)
	credentials["password"] = testUpdatedPassword
	active := hit(nil, "POST", "/api/v1/auth/login", credentials, 200).Result().Cookies()[0]
	hit(active, "GET", "/api/v1/institution/filings", nil, 200)
	hit(active, "POST", institutionAccountPath, map[string]string{}, 403)
}

// assertInstitutionAccountAudit 核对开户留痕使用实际经办人及开户目标。
// 参数 t、db 为隔离测试，admin、opened 分别为经办账号及新机构账号标识。
// 返回值 无。
func assertInstitutionAccountAudit(t *testing.T, db *pgxpool.Pool, admin, opened string) {
	t.Helper()
	var count int
	err := db.QueryRow(t.Context(), `SELECT count(*) FROM access_events WHERE operator_id=$1
		AND role_code='ROLE_SYS_ADMIN' AND event='account_provisioned' AND target=$2`, admin, opened).Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("机构开户经办记录为 %d 条：%v", count, err)
	}
}
