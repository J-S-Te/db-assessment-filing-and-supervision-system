package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/access"
	"grade-business/internal/app"
	"grade-business/internal/cases"
	"grade-business/internal/precheck"
	"grade-business/migrations"
)

// TestExistingFilingDirectoryUpgrade 验证旧备案只按完全一致的稳定名称补绑空归属。
// 参数 t 为升级前样本和真实隔离数据库的测试上下文。
// 返回值 无。
func TestExistingFilingDirectoryUpgrade(t *testing.T) {
	dsn := os.Getenv("GRADE_ACCESS_TEST_DSN")
	if dsn == "" {
		t.Skip("需要单独隔离的 GRADE_ACCESS_TEST_DSN")
	}
	db := accessTestSchema(t, dsn)
	if err := migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `DELETE FROM schema_migrations
		WHERE name='079_filing_directory_backfill.sql'`); err != nil {
		t.Fatal(err)
	}
	unit, otherUnit, _, _ := accessSubjects(t, db)
	seedExistingFilingDirectoryUpgrade(t, db, unit, otherUnit)
	if err := migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	for number, want := range map[string]string{
		"LEGACY-EXACT": unit, "LEGACY-ALIAS": "", "LEGACY-BOUND": otherUnit,
	} {
		var actual string
		err := db.QueryRow(t.Context(), `SELECT coalesce(directory_unit_id::text,'') FROM business_cases
			WHERE number=$1 AND status='pending_final' AND version=3`, number).Scan(&actual)
		if err != nil || actual != want {
			t.Errorf("升级后备案 %s 的单位 = %q，期望 %q：%v", number, actual, want, err)
		}
	}
}

// seedExistingFilingDirectoryUpgrade 构造待终审的旧归属数据与已有外键的对照数据。
// 参数 t、db 为隔离测试上下文与连接池，unit、otherUnit 为两个不同辖区的名录 UUID。
// 返回值 无。
func seedExistingFilingDirectoryUpgrade(t *testing.T, db *pgxpool.Pool, unit, otherUnit string) {
	t.Helper()
	if _, err := db.Exec(t.Context(), `UPDATE directory_parties SET name='权限单位甲现名' WHERE id=$1`, unit); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `INSERT INTO business_cases(kind,number,system_name,organization_name,
		assessment_org,level,status,version,directory_unit_id)
		VALUES ('filing','LEGACY-EXACT','历史系统','权限单位甲','权限机构甲','2','pending_final',3,NULL),
		       ('filing','LEGACY-ALIAS','历史系统','权限单位甲现名','权限机构甲','2','pending_final',3,NULL),
		       ('filing','LEGACY-BOUND','历史系统','权限单位甲','权限机构甲','2','pending_final',3,$1)`,
		otherUnit); err != nil {
		t.Fatal(err)
	}
}

// TestAdminFilingUsesDirectoryDistrict 验证管理端送审的备案按真实单位区划流转。
// 参数 t 为隔离数据库、账号和 HTTP 路由的测试上下文。
// 返回值 无。
func TestAdminFilingUsesDirectoryDistrict(t *testing.T) {
	dsn := os.Getenv("GRADE_ACCESS_TEST_DSN")
	if dsn == "" {
		t.Skip("需要单独隔离的 GRADE_ACCESS_TEST_DSN")
	}
	db := accessTestSchema(t, dsn)
	if err := migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	_, _, institutionID, _ := accessSubjects(t, db)
	records, err := cases.New(db, t.TempDir(), "辖区测试")
	if err != nil {
		t.Fatal(err)
	}
	cfg := settings{address: "127.0.0.1:18549", reviewWindows: app.ReviewWindows{FilingDays: 3}}
	handler := routes(cfg, db, records, precheck.New(db, materialSource(records)), http.NotFoundHandler())
	hit := accessRequest(t, handler)
	for _, account := range []access.AccountInput{
		{Username: "scope-admin", Name: "系统管理员", Role: access.SystemAdmin, PersonKey: "scope-admin"},
		{Username: "scope-city", Name: "市级监管员", Role: access.Regulator, PersonKey: "scope-city", AreaCode: "331000"},
		{Username: "scope-county", Name: "椒江监管员", Role: access.Regulator, PersonKey: "scope-county", AreaCode: "331002"},
		{Username: "scope-other", Name: "外市监管员", Role: access.Regulator, PersonKey: "scope-other", AreaCode: "330800"},
		{Username: "scope-institution", Name: "测评机构", Role: access.OrgAdmin, SubjectID: institutionID},
	} {
		account.Password = testInitialPassword
		if _, err := access.Provision(t.Context(), db, account); err != nil {
			t.Fatal(err)
		}
	}
	admin := accessLogin(t, hit, "scope-admin")
	city := accessLogin(t, hit, "scope-city", "331000")
	county := accessLogin(t, hit, "scope-county", "331002")
	other := accessLogin(t, hit, "scope-other", "330800")
	institution := accessLogin(t, hit, "scope-institution")
	assertAdminFilingDistrictReview(t, handler, hit, admin, city, county, other, institution)
}

// assertAdminFilingDistrictReview 用途为核对市县可见范围、初终审及机构代报补齐归属。
// 参数 t、handler、hit 提供测试路由，五个会话对应管理员、市级、区县、外市及机构。
// 返回值 无。
func assertAdminFilingDistrictReview(t *testing.T, handler http.Handler, hit requestAccess,
	admin, city, county, other, institution *http.Cookie,
) {
	t.Helper()
	unit := hit(admin, "POST", "/api/v1/directory/units",
		map[string]string{"name": "台州测试单位", "code": "913310000000000001", "district": "台州"}, 201)
	var registered struct{ ID string }
	if err := json.Unmarshal(unit.Body.Bytes(), &registered); err != nil || registered.ID == "" {
		t.Fatalf("无法读取名录登记结果：%s", unit.Body.String())
	}
	form := map[string]any{"number": "SCOPE-A", "systemName": "测试系统", "organizationName": "待核实单位",
		"assessmentOrg": "权限机构甲", "level": "2"}
	const collection = "/api/v1/filings"
	created := hit(admin, "POST", collection, form, 201)
	var draft struct{ ID string }
	if err := json.Unmarshal(created.Body.Bytes(), &draft); err != nil || draft.ID == "" {
		t.Fatalf("无法读取备案草稿：%s", created.Body.String())
	}
	path := collection + "/" + draft.ID
	hit(city, "GET", path, nil, 404)
	form["organizationName"], form["expectedVersion"] = "台州测试单位", 1
	hit(admin, "PATCH", path, form, 200)
	uploadWorkflowMaterial(t, handler, admin, path+"/attachments", "filing_form")
	uploadWorkflowMaterial(t, handler, admin, path+"/attachments", "classification_report")
	hit(admin, "POST", path+"/submit", map[string]int{"expectedVersion": 4}, 200)
	if data := hit(city, "GET", collection+"?status=pending_initial", nil, 200).Body.String(); !strings.Contains(data, draft.ID) {
		t.Fatalf("市级监管员未看到已绑定名录单位的送审备案：%s", data)
	}
	hit(county, "GET", path, nil, 404)
	hit(other, "GET", path, nil, 404)
	hit(admin, "PATCH", "/api/v1/directory/units/"+registered.ID,
		map[string]any{"expectedVersion": 1, "district": "椒江区"}, 200)
	hit(county, "GET", path, nil, 200)
	review := map[string]any{"decision": "approve", "expectedVersion": 5}
	hit(county, "POST", path+"/review-actions", review, 200)
	review["expectedVersion"] = 6
	hit(county, "POST", path+"/review-actions", review, 409)
	hit(city, "POST", path+"/review-actions", review, 200)
	form["number"] = "SCOPE-B"
	delete(form, "expectedVersion")
	created = hit(admin, "POST", collection, form, 201)
	var second struct{ ID string }
	if err := json.Unmarshal(created.Body.Bytes(), &second); err != nil || second.ID == "" {
		t.Fatalf("无法读取新建备案草稿：%s", created.Body.String())
	}
	hit(county, "GET", collection+"/"+second.ID, nil, 200)
	form["systemName"], form["expectedVersion"] = "机构更新系统名称", 1
	hit(institution, "PATCH", "/api/v1/institution/filings/"+second.ID, form, 200)
	hit(county, "GET", collection+"/"+second.ID, nil, 200)
	form["organizationName"], form["expectedVersion"] = "另一个待核实单位", 2
	hit(institution, "PATCH", "/api/v1/institution/filings/"+second.ID, form, 200)
	hit(county, "GET", collection+"/"+second.ID, nil, 404)
	assertUnboundInstitutionFilingHandoff(t, handler, hit, admin, county, other, institution)
}

// assertUnboundInstitutionFilingHandoff 用途为核对管理员补齐机构代报名录后的辖区分派。
// 参数 t、handler、hit 提供隔离路由，admin、county、other、institution 为各角色会话。
// 返回值 无。
func assertUnboundInstitutionFilingHandoff(t *testing.T, handler http.Handler, hit requestAccess,
	admin, county, other, institution *http.Cookie,
) {
	t.Helper()
	form := map[string]any{"systemName": "机构代报测试系统", "organizationName": "待核实名录单位", "level": "2"}
	const collection = "/api/v1/institution/filings"
	response := hit(institution, "POST", collection, form, 201)
	var result struct{ ID string }
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.ID == "" {
		t.Fatal("未创建机构代报草稿")
	}
	scoped, management := collection+"/"+result.ID, "/api/v1/filings/"+result.ID
	uploadWorkflowMaterial(t, handler, institution, scoped+"/attachments", "filing_form")
	uploadWorkflowMaterial(t, handler, institution, scoped+"/attachments", "classification_report")
	hit(institution, "POST", scoped+"/submit", map[string]int{"expectedVersion": 3}, 200)
	hit(admin, "GET", management, nil, 200)
	hit(county, "GET", management, nil, 404)
	hit(admin, "POST", management+"/review-actions", map[string]any{
		"decision": "return_for_correction", "comment": "请核对单位名录", "expectedVersion": 4,
	}, 200)
	form["organizationName"], form["assessmentOrg"], form["expectedVersion"] = "台州测试单位", "权限机构甲", 5
	hit(admin, "PATCH", management, form, 200)
	hit(institution, "POST", scoped+"/submit", map[string]int{"expectedVersion": 6}, 200)
	hit(county, "GET", management, nil, 200)
	hit(other, "GET", management, nil, 404)
}
