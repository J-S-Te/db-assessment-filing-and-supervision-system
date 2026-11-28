package alerts

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/access"
)

// TestDistrictAlertDecision 验证预警、汇总和线索遵循同一辖区，延期审批记录真实账号。
// 参数 t 提供隔离 PostgreSQL、可信目录和已验证监管会话。
// 返回值 无。
func TestDistrictAlertDecision(t *testing.T) {
	db, admin := newAlertFixture(t)
	const organization = "本区隔离单位"
	local := seedDistrictAlertHazard(t, db, organization, "330802", "A00000000000000001")
	seedDistrictAlertHazard(t, db, "外区隔离单位", "衢江区", "A00000000000000002")
	synced := readAlertTest[syncResult](t, admin, "POST", endpoint+"/sync", "", 200)
	if synced.Created != 2 {
		t.Fatalf("预期两条待跟踪预警，实际 %d", synced.Created)
	}
	regulator, actorID := districtAlertSession(t, db, admin)
	summary := readAlertTest[Summary](t, regulator, "GET", endpoint+"/summary", "", 200)
	page := readAlertTest[alertPage](t, regulator, "GET", endpoint, "", 200)
	leads := readAlertTest[supervisionLeadPage](t, regulator, "GET", endpoint+"/supervision-leads", "", 200)
	if summary.Total != 1 || summary.Pending != 1 || len(page.Items) != 1 || page.Items[0].HazardID != local ||
		len(leads.Items) != 1 || leads.Items[0].AlertID != page.Items[0].ID {
		t.Fatalf("辖区预警聚合不一致: %+v %+v %+v", summary, page, leads)
	}
	id := page.Items[0].ID
	deadline := alertTestDate(t, db, 15)
	readAlertTest[actionResult](t, admin, "POST", operatorAlertPath+"/"+id+"/actions"+
		alertOrganizationQuery(organization), extensionBody(deadline), 200)
	decision := readAlertTest[actionResult](t, regulator, "POST", endpoint+"/"+id+"/actions",
		`{"action":"approve","expectedVersion":2,"comment":"核实当地整改安排后同意"}`, 200)
	if decision.Disposition == nil || decision.Disposition.Status != "approved" {
		t.Fatalf("延期审批未落实: %+v", decision)
	}
	assertDistrictAlertActor(t, db, id, local, actorID, deadline)
}

// assertDistrictAlertActor 核对审批与实际隐患延期的两个事件保存同一可信账号。
// 参数 t、db 为隔离测试环境，alert、hazard、actor、deadline 为目标和验收值。
// 返回值 无。
func assertDistrictAlertActor(t *testing.T, db *pgxpool.Pool, alert, hazard, actor, deadline string) {
	t.Helper()
	var approved, extended, currentDeadline string
	err := db.QueryRow(t.Context(), `SELECT
		(SELECT operator_id::text FROM local_alert_events WHERE alert_id=$1 AND action='approve'),
		(SELECT operator_id::text FROM hazard_events WHERE hazard_id=$2 AND action='extend_deadline'),
		(SELECT deadline::text FROM hazard_items WHERE id=$2)`, alert, hazard).
		Scan(&approved, &extended, &currentDeadline)
	if err != nil || approved != actor || extended != actor || currentDeadline != deadline {
		t.Fatalf("预警和隐患留痕或期限不符: %q %q %q %v", approved, extended, currentDeadline, err)
	}
}

// seedDistrictAlertHazard 以可信目录和真实备案关联创建已派发的超期测试隐患。
// 参数 t、db 为隔离测试环境，name、district、code 为合成的运营主体资料。
// 返回值 指定辖区的隐患标识。
func seedDistrictAlertHazard(t *testing.T, db *pgxpool.Pool, name, district, code string) string {
	t.Helper()
	var hazard string
	err := db.QueryRow(t.Context(), `WITH unit AS (
		INSERT INTO directory_parties(kind,scope_name,name,code,district) VALUES('unit',$1,$1,$2,$3) RETURNING id
	), filing AS (
		INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status,directory_unit_id)
		SELECT 'filing',$2,$1,$1,'隔离测评机构','3','approved',id FROM unit RETURNING id
	)
	INSERT INTO hazard_items(registration_filing_id,ledger,title,source,discovered_on,severity,description,
		measure,responsibility,deadline,status,version,dispatched_at,retest_institution)
	SELECT id,'major_risk','例行风险复查','manual',CURRENT_DATE-3,'medium','需整改事项','复核配置',
		$1,CURRENT_DATE-2,'open',2,clock_timestamp(),'隔离测评机构' FROM filing RETURNING id::text`, name, code, district).
		Scan(&hazard)
	if err != nil {
		t.Fatal(err)
	}
	return hazard
}

// districtAlertSession 登录明确辖区的合成账号并为后续预警请求绑定真实会话。
// 参数 t、db、router 为测试上下文、隔离数据库和真实业务路由。
// 返回值 经权限网关处理的路由与实际账号标识。
func districtAlertSession(t *testing.T, db *pgxpool.Pool, router *http.ServeMux) (http.Handler, string) {
	t.Helper()
	const username, password = "alerts-test-regulator", "Synthetic-Test_3109!"
	id, err := access.Provision(t.Context(), db, access.AccountInput{Username: username,
		Password: password, Name: "预警测试监管员", AreaCode: "330802", PersonKey: username, Role: access.Regulator})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(t.Context(), `UPDATE access_accounts SET must_change_password=false WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	api := access.New(db).Protect(router)
	login := httptest.NewRequestWithContext(t.Context(), "POST", "/api/v1/auth/login",
		strings.NewReader(`{"username":"`+username+`","password":"`+password+`","areaCode":"330802"}`))
	login.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, login)
	cookies := response.Result().Cookies()
	if response.Code != 200 || len(cookies) != 1 {
		t.Fatalf("监管测试账号登录失败，状态 %d", response.Code)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(cookies[0])
		api.ServeHTTP(w, r)
	}), id
}
