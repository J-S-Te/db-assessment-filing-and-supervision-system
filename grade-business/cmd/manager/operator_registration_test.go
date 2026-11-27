package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/internal/access"
	"grade-business/internal/cases"
	"grade-business/internal/precheck"
	"grade-business/migrations"
)

const registrationPath = "/api/v1/operator-registrations"
const publicRegistrationPath = "/api/v1/auth/operator-registrations"

type registrationTestResult struct {
	ID         string `json:"id"`
	LookupCode string `json:"lookupCode"`
	Status     string `json:"status"`
	Reason     string `json:"reason"`
	AreaCode   string `json:"areaCode"`
	SubjectID  string `json:"subjectId"`
}

// TestOperatorRegistrationOnIsolatedDB 验证申请、辖区审核、留痕和会话开通的真实接口。
// 参数 t 为隔离 PostgreSQL 及 HTTP 路由测试。
// 返回值 无。
func TestOperatorRegistrationOnIsolatedDB(t *testing.T) {
	db, handler, admin, regulator, org := registrationFixture(t, "331002")
	hit := accessRequest(t, handler)
	first := registrationInput("自助注册单位甲", "91331002MA1000001R", "registration-first")
	invalid := registrationInput("无效信用代码单位", "91331002MA1000001W", "bad-registration")
	hit(nil, "POST", publicRegistrationPath, invalid, 400)
	hit(nil, "POST", publicRegistrationPath,
		registrationInput("域外单位", "91330102MA1000001J", "outside-registration"), 422)
	first["roleCode"] = "ROLE_SYS_ADMIN"
	hit(nil, "POST", publicRegistrationPath, first, 400)
	delete(first, "roleCode")
	submitted := submitRegistration(t, hit, first)
	credentials := map[string]string{"username": first["username"], "password": testInitialPassword}
	hit(nil, "POST", "/api/v1/auth/login", credentials, 401)
	hit(nil, "GET", registrationPath, nil, 401)
	hit(org, "GET", registrationPath, nil, 403)
	hit(nil, "POST", publicRegistrationPath, first, 409)
	hit(nil, "POST", publicRegistrationPath+"/status", map[string]string{"lookupCode": strings.Repeat("A", 43)}, 404)
	query := map[string]string{"lookupCode": submitted.LookupCode}
	readRegistration(t, hit(nil, "POST", publicRegistrationPath+"/status", query, 200), "pending")
	other := registrationInput("自助注册单位乙", "91331003MA10000018", "registration-other")
	otherResult := submitRegistration(t, hit, other)
	visible := hit(regulator, "GET", registrationPath+"?status=pending", nil, 200).Body.String()
	if !strings.Contains(visible, submitted.ID) || strings.Contains(visible, otherResult.ID) ||
		strings.Contains(visible, "lookupCode") || strings.Contains(visible, "password") {
		t.Fatal("审核清单返回了不适当的数据")
	}
	approve := map[string]any{"decision": "approve", "verified": true}
	hit(regulator, "POST", registrationPath+"/"+otherResult.ID+"/review-actions", approve, 404)
	hit(org, "POST", registrationPath+"/"+submitted.ID+"/review-actions", approve, 403)
	hit(regulator, "POST", registrationPath+"/"+submitted.ID+"/review-actions",
		map[string]string{"decision": "approve"}, 400)
	approved := readRegistration(t, hit(regulator, "POST", registrationPath+"/"+submitted.ID+"/review-actions",
		approve, 200), "approved")
	hit(admin, "POST", registrationPath+"/"+submitted.ID+"/review-actions", approve, 409)
	readRegistration(t, hit(nil, "POST", publicRegistrationPath+"/status", query, 200), "approved")
	cookie := hit(nil, "POST", "/api/v1/auth/login", credentials, 200).Result().Cookies()[0]
	session := hit(cookie, "GET", "/api/v1/auth/session", nil, 200).Body.String()
	if !strings.Contains(session, "ROLE_OPERATOR") || !strings.Contains(session, approved.SubjectID) ||
		!strings.Contains(session, `"mustChangePassword":false`) {
		t.Fatalf("账号未按申请开通：%s", session)
	}
	hit(cookie, "GET", "/api/v1/operator/filings", nil, 200)
	hit(cookie, "GET", registrationPath, nil, 403)
	assertRegistrationAudit(t, db, submitted.ID, approved.SubjectID)
	t.Run("驳回后重提及当日限额", func(t *testing.T) {
		assertRegistrationRejection(t, hit, admin, other, otherResult)
	})
}

// registrationFixture 以项目现有测试模式启动真实路由并准备三个审核权限样本。
// 参数 t 为测试环境，areaCode 为区县监管员的授权辖区。
// 返回值 隔离连接池、实际路由及管理员、辖区监管员、机构会话。
func registrationFixture(t *testing.T, areaCode string) (*pgxpool.Pool, http.Handler, *http.Cookie, *http.Cookie, *http.Cookie) {
	t.Helper()
	dsn := os.Getenv("GRADE_ACCESS_TEST_DSN")
	if dsn == "" {
		t.Skip("需要单独隔离的 GRADE_ACCESS_TEST_DSN")
	}
	db := accessTestSchema(t, dsn)
	if err := migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	_, _, orgID, _ := accessSubjects(t, db)
	records, err := cases.New(db, t.TempDir(), "注册审批测试")
	if err != nil {
		t.Fatal(err)
	}
	handler := routes(settings{address: "127.0.0.1:18549"}, db, records,
		precheck.New(db, materialSource(records)), http.NotFoundHandler())
	hit := accessRequest(t, handler)
	accounts := []access.AccountInput{
		{Username: "registration-admin", Name: "系统管理员", Role: access.SystemAdmin, PersonKey: "reg-admin"},
		{Username: "registration-area", Name: "区县监管员", Role: access.Regulator, PersonKey: "reg-area", AreaCode: areaCode},
		{Username: "registration-org", Name: "机构负责人", Role: access.OrgAdmin, SubjectID: orgID},
	}
	for _, input := range accounts {
		input.Password = testInitialPassword
		if _, err := access.Provision(t.Context(), db, input); err != nil {
			t.Fatal(err)
		}
	}
	return db, handler, accessLogin(t, hit, accounts[0].Username),
		accessLogin(t, hit, accounts[1].Username), accessLogin(t, hit, accounts[2].Username)
}

// registrationInput 构造可直接提交申请测试的合成单位资料。
// 参数 unit、code、username 为单位名称、统一社会信用代码和登录名。
// 返回值 公开注册请求体。
func registrationInput(unit, code, username string) map[string]string {
	return map[string]string{"unitName": unit, "creditCode": code, "contactName": "测试联系人",
		"phone": "13900000001", "username": username, "password": testInitialPassword}
}

// submitRegistration 提交申请并校验能用于后续流程的受理回执。
// 参数 t、hit 为测试及真实路由，input 为注册资料。
// 返回值 含申请标识、私密查询码和待审核状态的回执。
func submitRegistration(t *testing.T, hit requestAccess, input map[string]string) registrationTestResult {
	t.Helper()
	result := readRegistration(t, hit(nil, "POST", publicRegistrationPath, input, 201), "pending")
	if result.ID == "" || len(result.LookupCode) != 43 {
		t.Fatal("受理回执缺少申请标识或私密查询码")
	}
	return result
}

// readRegistration 检查申请或审核接口返回的持久化状态。
// 参数 t、res 为测试和 HTTP 响应，status 为预期状态。
// 返回值 可供后续业务验证的响应字段。
func readRegistration(t *testing.T, res *httptest.ResponseRecorder, status string) registrationTestResult {
	t.Helper()
	var result registrationTestResult
	body := res.Result().Body
	defer body.Close()
	if err := json.NewDecoder(body).Decode(&result); err != nil || result.Status != status {
		t.Fatalf("申请状态为 %q，预期 %q，解码错误 %v", result.Status, status, err)
	}
	return result
}

// assertRegistrationAudit 核对开通的主档与审核账号留痕。
// 参数 t、db 为测试库环境，id、subject 为申请及单位标识。
// 返回值 无。
func assertRegistrationAudit(t *testing.T, db *pgxpool.Pool, id, subject string) {
	t.Helper()
	var count int
	err := db.QueryRow(t.Context(), `SELECT count(*) FROM access_events e
		JOIN access_accounts a ON a.id=e.operator_id JOIN directory_parties p ON p.id=$2
		WHERE e.target=$1 AND e.event='operator_registration_approved' AND e.role_code='ROLE_REGULATOR'
		  AND a.username='registration-area' AND p.district='椒江区' AND p.kind='unit'`, id, subject).Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("开通主档和审批留痕不完整：%d %v", count, err)
	}
}

// assertRegistrationRejection 确认可见的驳回理由及同一信用代码重提额度。
// 参数 t、hit、admin 为测试环境，input、submitted 为申请资料及首单回执。
// 返回值 无。
func assertRegistrationRejection(t *testing.T, hit requestAccess, admin *http.Cookie,
	input map[string]string, submitted registrationTestResult,
) {
	t.Helper()
	review := registrationPath + "/" + submitted.ID + "/review-actions"
	hit(admin, "POST", review, map[string]string{"decision": "reject"}, 400)
	for i := 0; i < 3; i++ {
		result := readRegistration(t, hit(admin, "POST", review,
			map[string]string{"decision": "reject", "reason": "请核对管理员授权"}, 200), "rejected")
		status := readRegistration(t, hit(nil, "POST", publicRegistrationPath+"/status",
			map[string]string{"lookupCode": submitted.LookupCode}, 200), "rejected")
		if result.Reason != "请核对管理员授权" || status.Reason != result.Reason {
			t.Fatal("申请人未能查到持久化的驳回原因")
		}
		if i < 2 {
			submitted = submitRegistration(t, hit, input)
			review = registrationPath + "/" + submitted.ID + "/review-actions"
		}
	}
	hit(nil, "POST", publicRegistrationPath, input, 429)
}

// TestOperatorRegistrationExistingDirectoryOnIsolatedDB 验证存量单位归属及并发审批。
// 参数 t 为真实路由和隔离数据库测试。
// 返回值 无。
func TestOperatorRegistrationExistingDirectoryOnIsolatedDB(t *testing.T) {
	db, handler, admin, regulator, _ := registrationFixture(t, "331002")
	hit := accessRequest(t, handler)
	const code = "91331003MA1000002B"
	const name = "存量单位甲"
	var unitID string
	if err := db.QueryRow(t.Context(), `INSERT INTO directory_parties(kind,scope_name,name,code,district)
		VALUES('unit','原有业务范围',$1,$2,'椒江区') RETURNING id::text`, name, code).Scan(&unitID); err != nil {
		t.Fatal(err)
	}
	hit(nil, "POST", publicRegistrationPath, registrationInput("名称不匹配", code, "wrong-name"), 409)
	result := submitRegistration(t, hit, registrationInput(name, code, "existing-registration"))
	scoped := hit(regulator, "GET", registrationPath+"?q=存量单位&limit=1", nil, 200).Body.String()
	if !strings.Contains(scoped, result.ID) || !strings.Contains(scoped, "331002") {
		t.Fatal("在册单位未按实际归属进入审核范围")
	}
	changeRegistrationDistrict(t, db, unitID, "黄岩区")
	approve := map[string]any{"decision": "approve", "verified": true}
	path := registrationPath + "/" + result.ID + "/review-actions"
	hit(regulator, "POST", path, approve, 409)
	changeRegistrationDistrict(t, db, unitID, "椒江区")
	assertConcurrentRegistrationReview(t, handler, regulator, admin, path, unitID)

	duplicate := hit(nil, "POST", publicRegistrationPath, registrationInput(name, code, "duplicate-unit"), 409)
	if !strings.Contains(duplicate.Body.String(), "registration_unit_has_account") {
		t.Fatal("已有账号的单位未获得明确提示")
	}
}

// changeRegistrationDistrict 模拟待审期间在名录中发生的辖区调整。
// 参数 t、db 为隔离测试，id、district 为单位标识和新辖区。
// 返回值 无。
func changeRegistrationDistrict(t *testing.T, db *pgxpool.Pool, id, district string) {
	t.Helper()
	if _, err := db.Exec(t.Context(), `UPDATE directory_parties SET district=$2 WHERE id=$1`, id, district); err != nil {
		t.Fatal(err)
	}
}

// assertConcurrentRegistrationReview 同时尝试两次审批并核对只有一个结果能绑定原单位。
// 参数 t、handler 为测试与真实路由，reviewers 为两个会话，path 和 unitID 为单据路径及预期主档。
// 返回值 无。
func assertConcurrentRegistrationReview(t *testing.T, handler http.Handler,
	regulator, admin *http.Cookie, path, unitID string,
) {
	t.Helper()
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	for _, reviewer := range []*http.Cookie{regulator, admin} {
		go func() {
			request := httptest.NewRequest("POST", "http://127.0.0.1:18549"+path,
				bytes.NewBufferString(`{"decision":"approve","verified":true}`))
			request.Header.Set("Content-Type", "application/json")
			request.AddCookie(reviewer)
			response := httptest.NewRecorder()
			<-start
			handler.ServeHTTP(response, request)
			responses <- response
		}()
	}
	close(start)
	statuses := map[int]int{}
	for range 2 {
		response := <-responses
		statuses[response.Code]++
		if response.Code == 200 && readRegistration(t, response, "approved").SubjectID != unitID {
			t.Fatal("审批没有复用原单位标识")
		}
	}
	if statuses[200] != 1 || statuses[409] != 1 {
		t.Fatalf("并发审批必须恰有一笔成功，实际状态为 %v", statuses)
	}
}

// TestOperatorRegistrationRollsBackOnAccountConflict 验证账号冲突时名录及审批不发生部分提交。
// 参数 t 为真实路由和隔离数据库测试。
// 返回值 无。
func TestOperatorRegistrationRollsBackOnAccountConflict(t *testing.T) {
	db, handler, admin, _, _ := registrationFixture(t, "331002")
	hit := accessRequest(t, handler)
	const name = "回滚测试申请单位"
	const code = "91331002MA10000030"
	const username = "registration-collision"
	result := submitRegistration(t, hit, registrationInput(name, code, username))
	_, err := access.Provision(t.Context(), db, access.AccountInput{
		Username: username, Name: "在途冲突账号", Role: access.SystemAdmin,
		PersonKey: "registration-collision-fixture", Password: testInitialPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	hit(admin, "POST", registrationPath+"/"+result.ID+"/review-actions",
		map[string]any{"decision": "approve", "verified": true}, 409)
	var unitCreated, doneEvent bool
	err = db.QueryRow(t.Context(), `SELECT
		EXISTS(SELECT 1 FROM directory_parties WHERE kind='unit' AND code=$1),
		EXISTS(SELECT 1 FROM access_events WHERE target=$2 AND event='operator_registration_approved')`,
		code, result.ID).Scan(&unitCreated, &doneEvent)
	if err != nil || unitCreated || doneEvent {
		t.Fatalf("冲突后出现部分提交，单位 %v，审核事件 %v，查询错误 %v", unitCreated, doneEvent, err)
	}
	readRegistration(t, hit(nil, "POST", publicRegistrationPath+"/status",
		map[string]string{"lookupCode": result.LookupCode}, 200), "pending")
}

// TestOperatorRegistrationQuzhouScopeOnIsolatedDB 验证恢复启用后的衢州市及区县审核范围。
// 参数 t 为真实路由及独立 PostgreSQL 测试环境。
// 返回值 无。
func TestOperatorRegistrationQuzhouScopeOnIsolatedDB(t *testing.T) {
	db, handler, _, district, _ := registrationFixture(t, "330802")
	hit := accessRequest(t, handler)
	const username = "registration-quzhou-city"
	if _, err := access.Provision(t.Context(), db, access.AccountInput{
		Username: username, Name: "衢州市级监管员", Role: access.Regulator, AreaCode: "330800",
		PersonKey: "registration-city-fixture", Password: testInitialPassword,
	}); err != nil {
		t.Fatal(err)
	}
	city := accessLogin(t, hit, username)
	own := submitRegistration(t, hit, registrationInput("柯城申请单位", "91330802MA1000001N", "registration-kecheng"))
	other := submitRegistration(t, hit, registrationInput("衢江申请单位", "91330803MA10000015", "registration-qujiang"))
	list := hit(district, "GET", registrationPath, nil, 200).Body.String()
	if !strings.Contains(list, own.ID) || strings.Contains(list, other.ID) {
		t.Fatal("衢州区县只应看到本辖区运营单位注册申请")
	}
	list = hit(city, "GET", registrationPath, nil, 200).Body.String()
	if !strings.Contains(list, own.ID) || !strings.Contains(list, other.ID) {
		t.Fatal("衢州市级账号未获得全市注册申请范围")
	}
	approve := map[string]any{"decision": "approve", "verified": true}
	path := registrationPath + "/" + other.ID + "/review-actions"
	hit(district, "POST", path, approve, 404)
	approved := readRegistration(t, hit(city, "POST", path, approve, 200), "approved")
	var storedDistrict string
	if err := db.QueryRow(t.Context(), "SELECT district FROM directory_parties WHERE id=$1",
		approved.SubjectID).Scan(&storedDistrict); err != nil || storedDistrict != "衢江区" {
		t.Fatalf("开通后的单位未按衢江区登记：%q %v", storedDistrict, err)
	}
}
