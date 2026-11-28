package main

import (
	"encoding/json"
	"net/http"
	"net/url"
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

// TestQuzhouRegulatorScope 验证衢州账号可开通且真实路由按辖区返回业务与驾驶舱数据。
// 参数 t 提供显式隔离的数据库和本轮测试上下文。
// 返回值 无，越界、漏计或账号错误时记录失败。
func TestQuzhouRegulatorScope(t *testing.T) {
	dsn := os.Getenv("GRADE_ACCESS_TEST_DSN")
	if dsn == "" {
		t.Skip("需要单独隔离的 GRADE_ACCESS_TEST_DSN")
	}
	db := accessTestSchema(t, dsn)
	if err := migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	first, second, org, _ := accessSubjects(t, db)
	if _, err := db.Exec(t.Context(), `UPDATE directory_parties SET district=CASE id
		WHEN $1 THEN '330802' ELSE '衢江区' END WHERE id IN ($1,$2)`, first, second); err != nil {
		t.Fatal(err)
	}
	own := accessFiling(t, db, "QZ-KC-ONLY", "权限单位甲", "权限机构甲", first, org)
	other := accessFiling(t, db, "QZ-QJ-ONLY", "权限单位乙", "权限机构甲", second, org)
	if _, err := db.Exec(t.Context(), `INSERT INTO business_cases(kind,number,system_name,
		organization_name,assessment_org,level)
		VALUES ('filing','QZ-UNBOUND','QZ-UNBOUND','权限单位甲','权限机构甲','2')`); err != nil {
		t.Fatal(err)
	}
	seedQuzhouDashboardRisks(t, db, own, other)
	hit := quzhouRegulatorRoutes(t, db)
	accounts := []string{"330800", "330802", "330803", "330822", "330824", "330825", "330881"}
	for _, code := range accounts {
		_, err := access.Provision(t.Context(), db, access.AccountInput{Username: "reg-" + code,
			Name: code + "监管员", Role: access.Regulator, AreaCode: code, PersonKey: "qz-" + code,
			Password: testInitialPassword})
		if err != nil {
			t.Fatalf("无法开通 %s 监管员: %v", code, err)
		}
	}
	assertQuzhouRegulatorScope(t, hit, own, other)
}

// TestQuzhouDistrictAssignment 验证机构可登记单位，监管区划及已核定单位信用代码由系统管理员调整。
// 参数 t 提供隔离数据库和测试上下文。
// 返回值 无，权限或版本行为不符时记录失败。
func TestQuzhouDistrictAssignment(t *testing.T) {
	dsn := os.Getenv("GRADE_ACCESS_TEST_DSN")
	if dsn == "" {
		t.Skip("需要单独隔离的 GRADE_ACCESS_TEST_DSN")
	}
	db := accessTestSchema(t, dsn)
	if err := migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	_, _, org, _ := accessSubjects(t, db)
	for _, account := range []access.AccountInput{
		{Username: "quzhou-org", Name: "隔离机构", Role: access.OrgAdmin, SubjectID: org, Password: testInitialPassword},
		{Username: "quzhou-sys", Name: "隔离系统管理员", Role: access.SystemAdmin, PersonKey: "quzhou-sys", Password: testInitialPassword},
	} {
		if _, err := access.Provision(t.Context(), db, account); err != nil {
			t.Fatal(err)
		}
	}
	hit := quzhouRegulatorRoutes(t, db)
	institution := accessLogin(t, hit, "quzhou-org")
	admin := accessLogin(t, hit, "quzhou-sys")
	created := hit(institution, "POST", "/api/v1/institution/directory/units",
		map[string]any{"name": "衢州名录权限测试", "code": "913308020000000001", "district": "330802"}, 201)
	var item struct {
		ID, District string
		Usable       bool
		CodeManaged  bool
	}
	if err := json.Unmarshal(created.Body.Bytes(), &item); err != nil || item.ID == "" {
		t.Fatalf("无法读取测试单位: %v", err)
	}
	if item.District != "" {
		t.Errorf("机构新建时的单位辖区应等待核定，实际为 %q", item.District)
	}
	if !item.Usable {
		t.Error("待核定辖区的机构新建单位应能用于首次报送")
	}
	if item.CodeManaged {
		t.Error("机构新建的未核定单位应允许维护信用代码")
	}
	institutionPath := "/api/v1/institution/directory/units/" + item.ID
	adminPath := "/api/v1/directory/units/" + item.ID
	patch := map[string]any{"expectedVersion": 1, "district": "衢江区"}
	hit(institution, "PATCH", institutionPath, patch, 403)
	hit(admin, "PATCH", adminPath, map[string]any{"expectedVersion": 1, "district": "台州"}, 200)
	alias := hit(institution, "PATCH", institutionPath,
		map[string]any{"expectedVersion": 2, "district": " 331000 "}, 200)
	var kept struct {
		District    string
		Version     int
		CodeManaged bool
	}
	if err := json.Unmarshal(alias.Body.Bytes(), &kept); err != nil || kept.District != "台州市" ||
		kept.Version != 2 {
		t.Fatalf("等价区划应保留已核定值和版本: %+v，%v", kept, err)
	}
	for _, result := range []string{
		alias.Body.String(),
		hit(institution, "GET", institutionPath, nil, 200).Body.String(),
		hit(institution, "GET", "/api/v1/institution/directory/units", nil, 200).Body.String(),
	} {
		if !strings.Contains(result, `"codeManaged":true`) {
			t.Error("机构读取已核定单位时缺少信用代码维护权限标记")
		}
	}
	if result := hit(admin, "GET", adminPath, nil, 200); strings.Contains(result.Body.String(), `"codeManaged":true`) {
		t.Error("管理员仍可维护已核定单位的信用代码")
	}
	for _, district := range []string{"椒江区", "衢江区", "", "待核对"} {
		hit(institution, "PATCH", institutionPath, map[string]any{"expectedVersion": 2, "district": district}, 403)
	}
	hit(institution, "PATCH", institutionPath,
		map[string]any{"expectedVersion": 2, "code": "913308020000000002"}, 403)
	hit(admin, "PATCH", adminPath, map[string]any{"expectedVersion": 2, "district": "衢江区"}, 200)
}

// seedQuzhouDashboardRisks 建立两个区县的在册隐患和预警。
// 参数 t 为测试上下文，db 为隔离业务库，own、other 为不同区县的已绑定备案。
// 返回值 无，测试数据写入失败时终止测试。
func seedQuzhouDashboardRisks(t *testing.T, db *pgxpool.Pool, own, other string) {
	t.Helper()
	if _, err := db.Exec(t.Context(), `UPDATE business_cases SET status='approved' WHERE id IN ($1,$2)`, own, other); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `INSERT INTO hazard_items(registration_filing_id,ledger,title,source,
		discovered_on,severity,description,measure,responsibility,deadline,status,dispatched_at,retest_institution)
		SELECT id,'major_risk',system_name,'manual',current_date,'high','说明','措施','责任',
			current_date+3,'open',now(),'权限机构甲'
		FROM business_cases WHERE id IN ($1,$2)`, own, other); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `INSERT INTO local_alerts(hazard_id,kind,severity,system_name,organization_name,deadline)
		SELECT id,'high_risk',severity,title,'历史归属',deadline
		FROM hazard_items WHERE registration_filing_id IN ($1,$2)`, own, other); err != nil {
		t.Fatal(err)
	}
}

// quzhouRegulatorRoutes 以真实管理端装配验证监管员会话及查询。
// 参数 t 为测试上下文，db 为已迁移的隔离数据库。
// 返回值 可以发起带会话请求的测试函数。
func quzhouRegulatorRoutes(t *testing.T, db *pgxpool.Pool) requestAccess {
	t.Helper()
	records, err := cases.New(db, t.TempDir(), "辖区测试")
	if err != nil {
		t.Fatal(err)
	}
	cfg := settings{address: "127.0.0.1:18549", reviewWindows: app.ReviewWindows{FilingDays: 3}}
	return accessRequest(t, routes(cfg, db, records, precheck.New(db, materialSource(records)),
		http.NotFoundHandler()))
}

// assertQuzhouRegulatorScope 核对市本级和区县所能读取的备案及驾驶舱范围。
// 参数 t 为测试上下文，hit 发送真实路由请求，own、other 是不同区县的备案 ID。
// 返回值 无，内容或 HTTP 权限不符时记录失败。
func assertQuzhouRegulatorScope(t *testing.T, hit requestAccess, own, other string) {
	t.Helper()
	county := accessLogin(t, hit, "reg-330802")
	city := accessLogin(t, hit, "reg-330800")
	session := hit(county, "GET", "/api/v1/auth/session", nil, 200).Body.String()
	if !strings.Contains(session, `"/dashboard"`) || !strings.Contains(session, `"柯城区"`) {
		t.Fatalf("监管员会话缺少驾驶舱或辖区: %s", session)
	}
	hit(county, "GET", "/api/v1/filings/"+own, nil, 200)
	hit(county, "GET", "/api/v1/filings/"+other, nil, 404)
	hit(city, "GET", "/api/v1/filings/"+other, nil, 200)
	const dashboard = "/api/v1/dashboard/"
	assertQuzhouDashboardCount(t, hit, county, dashboard+"district-response?scope=all", "柯城区", 1)
	assertQuzhouDashboardCount(t, hit, city, dashboard+"district-response?scope=all", "", 2)
	assertQuzhouDashboardCount(t, hit, city, dashboard+"district-response?district=330803", "衢江区", 1)
	hit(county, "GET", dashboard+"district-response?district="+url.QueryEscape("衢江区"), nil, 403)
	hit(county, "GET", dashboard+"district-response?district=", nil, 403)
	hit(city, "GET", dashboard+"district-response?district=331002", nil, 403)
	hit(county, "GET", dashboard+"statistics", nil, 403)
	hit(county, "GET", dashboard+"analytics", nil, 403)
	risk := hit(county, "GET", dashboard+"risk-by-district", nil, 200).Body.String()
	if !strings.Contains(risk, `"district":"柯城区"`) || strings.Contains(risk, "衢江区") {
		t.Fatalf("区县风险分布范围错误: %s", risk)
	}
	ownSnapshot := hit(county, "GET", dashboard+"district-response?district=330802", nil, 200)
	if !strings.Contains(ownSnapshot.Body.String(), "QZ-KC-ONLY") ||
		strings.Contains(ownSnapshot.Body.String(), "QZ-QJ-ONLY") {
		t.Fatalf("隐患、预警的辖区筛选错误: %s", ownSnapshot.Body.String())
	}
}

// assertQuzhouDashboardCount 核对驾驶舱快照范围、计数及必要统计分区。
// 参数 t、hit、cookie 为测试会话，path 为接口地址，district 和 count 为预期结果。
// 返回值 无，快照内容不符时记录失败。
func assertQuzhouDashboardCount(t *testing.T, hit requestAccess, cookie *http.Cookie,
	path, district string, count int,
) {
	t.Helper()
	w := hit(cookie, "GET", path, nil, 200)
	var result struct {
		District   string
		Overview   *struct{ FilingTotal int }
		Statistics json.RawMessage
		Analytics  json.RawMessage
	}
	err := json.Unmarshal(w.Body.Bytes(), &result)
	if err != nil || result.Overview == nil || result.Overview.FilingTotal != count ||
		result.District != district || len(result.Statistics) == 0 || len(result.Analytics) == 0 {
		t.Fatalf("驾驶舱范围错误 %s: %s %v", path, w.Body.String(), err)
	}
}
