package hazards

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"grade-business/internal/access"
	"grade-business/internal/cases"
)

// TestDistrictScopeStabilizesFilingAndUnit 验证写事务授权后其他事务须等待备案及名录归属锁。
// 参数 t 提供隔离 PostgreSQL 及两个独立连接事务。
// 返回值 无。
func TestDistrictScopeStabilizesFilingAndUnit(t *testing.T) {
	s, admin := ledgerFixture(t)
	item := createDistrictHazard(t, s, admin, "辖区归属锁验证", "330802", "913308020000000001")
	ctx := t.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var filingID string
	var visible bool
	err = tx.QueryRow(ctx, `SELECT c.filing_id::text,true FROM hazard_context c WHERE c.id=$1 AND `+
		access.FilingDistrictScope("c.filing_id", "$2", true), item.ID, []string{"330802"}).
		Scan(&filingID, &visible)
	if err != nil || !visible {
		t.Fatalf("未取得有效备案辖区锁: %v", err)
	}
	for _, query := range []string{
		`UPDATE business_cases SET system_name=system_name WHERE id=$1`,
		`UPDATE directory_parties SET district=district
			WHERE id=(SELECT directory_unit_id FROM business_cases WHERE id=$1)`,
	} {
		verifyDistrictUpdateWaits(t, s, filingID, query)
	}
}

// verifyDistrictUpdateWaits 检查并发归属更新被持锁业务事务阻挡。
// 参数 t、s 为测试上下文与数据库服务，filingID、query 为合成备案和固定更新语句。
// 返回值 无。
func verifyDistrictUpdateWaits(t *testing.T, s *Service, filingID, query string) {
	t.Helper()
	ctx := t.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='100ms'"); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, query, filingID)
	var postgresErr *pgconn.PgError
	if !errors.As(err, &postgresErr) || postgresErr.Code != "55P03" {
		t.Fatalf("并发更新应等待读锁，实际 %v", err)
	}
}

// TestDistrictHazardLedger 验证台账查询、选择项和附件沿同一备案辖区授权。
// 参数 t 提供隔离 PostgreSQL 及真实会话路由。
// 返回值 无。
func TestDistrictHazardLedger(t *testing.T) {
	s, admin := ledgerFixture(t)
	local := createDistrictHazard(t, s, admin, "本区系统", "330802", "913308020000000001")
	foreign := createDistrictHazard(t, s, admin, "外区系统", "衢江区", "913308030000000002")
	regulator, _ := districtLedgerSession(t, s)
	page := ledgerReply[hazardPage](t, ledgerCall(regulator, "GET", testLedgerPath, nil), 200)
	if len(page.Items) != 1 || page.Items[0].ID != local.ID {
		t.Fatalf("辖区台账返回错误: %+v", page.Items)
	}
	options := ledgerReply[hazardCollection[registrationFilingOption]](t,
		ledgerCall(regulator, "GET", testLedgerPath+"/filing-options", nil), 200)
	if len(options.Items) != 1 || options.Items[0].SystemName != "本区系统" {
		t.Fatalf("可登记备案返回错误: %+v", options.Items)
	}
	export := ledgerCall(regulator, "GET", testLedgerPath+"/export?ledger=major_risk", nil)
	if export.Code != 200 || !strings.Contains(export.Body.String(), "本区系统") ||
		strings.Contains(export.Body.String(), "外区系统") {
		t.Fatalf("导出与辖区台账不一致，状态 %d", export.Code)
	}
	attachment := ledgerReply[cases.Attachment](t, ledgerUpload(t, admin,
		testLedgerPath+"/"+local.ID, kindEvidence, fixturePDF), 201)
	for target, status := range map[string]int{local.ID: 200, foreign.ID: 404} {
		path := testLedgerPath + "/" + target + "/attachments/" + attachment.ID
		if reply := ledgerCall(regulator, "GET", path, nil); reply.Code != status {
			t.Fatalf("附件响应 %d，预期 %d", reply.Code, status)
		}
	}
}

// TestDistrictHazardAuditRecordsSessionAccount 验证监管督办理由和账号关联同一版本事件。
// 参数 t 提供隔离 PostgreSQL 及真实会话路由。
// 返回值 无。
func TestDistrictHazardAuditRecordsSessionAccount(t *testing.T) {
	s, admin := ledgerFixture(t)
	local := createDistrictHazard(t, s, admin, "本区系统", "柯城区", "913308020000000001")
	regulator, accountID := districtLedgerSession(t, s)
	path := testLedgerPath + "/" + local.ID + "/actions"
	result := ledgerReply[hazardMutation](t, ledgerCall(regulator, "POST", path,
		actionInput{Action: actionSupervise, ExpectedVersion: local.Version, Comment: "请及时处理"}), 200)
	var actor, role, name string
	err := s.db.QueryRow(t.Context(), `SELECT operator_id::text,operator_role,operator
		FROM hazard_events WHERE hazard_id=$1 AND version=$2`, local.ID, result.Version).
		Scan(&actor, &role, &name)
	if err != nil || actor != accountID || role != string(access.Regulator) || name != "本区监管员" {
		t.Fatalf("实际经办身份不符: %q %q %q %v", actor, role, name, err)
	}
}

// createDistrictHazard 通过备案名录绑定创建可办理的隔离隐患样本。
// 参数 t、s、router 为测试上下文、服务和路由，name、district、code 为合成目录资料。
// 返回值 已派发样本的业务状态和版本。
func createDistrictHazard(t *testing.T, s *Service, router http.Handler,
	name, district, code string,
) hazardMutation {
	t.Helper()
	var filing string
	err := s.db.QueryRow(t.Context(), `WITH unit AS (
		INSERT INTO directory_parties(kind,scope_name,name,code,district) VALUES('unit',$1,$1,$2,$3) RETURNING id
	)
	INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status,directory_unit_id)
	SELECT 'filing',$2,$1,$1,'隔离测评机构','3','approved',id FROM unit RETURNING id::text`, name, code, district).
		Scan(&filing)
	if err != nil {
		t.Fatal(err)
	}
	input := registrationInput{Ledger: ledgerMajorRisk, FilingID: filing, Title: "例行风险检查",
		Severity: "high", Source: "manual", DiscoveredOn: "2025-01-01", Deadline: "2099-01-01",
		Description: "检查业务配置", Measure: "跟踪整改", Responsibility: "设施维护组"}
	item := ledgerReply[hazardMutation](t, ledgerCall(router, "POST", testLedgerPath, input), 201)
	return ledgerReply[hazardMutation](t, ledgerCall(router, "POST", testLedgerPath+"/"+item.ID+"/actions",
		actionInput{Action: actionDispatch, ExpectedVersion: item.Version}), 200)
}

// districtLedgerSession 使用真实权限网关为测试账号建立已改密的辖区会话。
// 参数 t、s 为隔离测试上下文和业务服务。
// 返回值 携带会话的实际业务路由与监管账号标识。
func districtLedgerSession(t *testing.T, s *Service) (http.Handler, string) {
	t.Helper()
	const username, password = "hazard-local-regulator", "Synthetic-Test_9308!"
	id, err := access.Provision(t.Context(), s.db, access.AccountInput{Username: username, Password: password,
		Name: "本区监管员", Role: access.Regulator, AreaCode: "330802", PersonKey: "hazard-local-person"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(t.Context(), `UPDATE access_accounts SET must_change_password=false WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	api := access.New(s.db).Protect(mux)
	login := ledgerCall(api, "POST", "/api/v1/auth/login", map[string]string{"username": username, "password": password})
	cookies := login.Result().Cookies()
	if login.Code != 200 || len(cookies) != 1 {
		t.Fatalf("监管测试账号登录失败，状态 %d", login.Code)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(cookies[0])
		api.ServeHTTP(w, r)
	}), id
}
