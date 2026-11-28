package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/access"
	"grade-business/internal/app"
	"grade-business/internal/cases"
	"grade-business/internal/precheck"
	"grade-business/migrations"
)

// TestInstitutionOwnsImplementation 验证实际会话权限及目标机构承接换机构复测的报备入口。
// 参数 t 为显式隔离数据库和临时附件目录的测试上下文。
// 返回值 无。
func TestInstitutionOwnsImplementation(t *testing.T) {
	hit, db, handler := implementationAccessEnvironment(t)
	unit, otherUnit, original, target := accessSubjects(t, db)
	filingID := accessFiling(t, db, "RETEST-HANDOFF", "权限单位甲", "权限机构甲", unit, original)
	otherID := accessFiling(t, db, "PRIVATE-FILING", "权限单位乙", "权限机构甲", otherUnit, original)
	if _, err := db.Exec(t.Context(), "UPDATE business_cases SET status='approved' WHERE id IN ($1,$2)",
		filingID, otherID); err != nil {
		t.Fatal(err)
	}
	var reporterID string
	if err := db.QueryRow(t.Context(), `INSERT INTO directory_people(institution_id,name,code)
		VALUES ($1,'换机构报备人员','HANDOFF-001') RETURNING id::text`, target).Scan(&reporterID); err != nil {
		t.Fatal(err)
	}
	operator := loginImplementationSubject(t, hit, db, "retest-operator", unit, access.Operator)
	institution := loginImplementationSubject(t, hit, db, "retest-institution", target, access.OrgAdmin)
	first := implementationReviewer(t, hit, db, "retest-admin", "", access.SystemAdmin)
	final := implementationReviewer(t, hit, db, "retest-final-admin", "", access.SystemAdmin)
	checkInstitutionHandoff(t, hit, handler, operator, institution, first, final, filingID, otherID, reporterID)
	historicOperator := loginImplementationSubject(t, hit, db, "historic-operator", otherUnit, access.Operator)
	checkHistoricalImplementation(t, hit, db, historicOperator, institution, otherID)
}

// loginImplementationSubject 为隔离测试创建绑定真实主体的账号并完成登录。
// 参数 t、hit、db 为测试环境，username、subject 和 role 指定测试身份。
// 返回值 登录后的会话 Cookie。
func loginImplementationSubject(t *testing.T, hit requestAccess, db *pgxpool.Pool,
	username, subject string, role access.Role,
) *http.Cookie {
	t.Helper()
	account := access.AccountInput{Username: username, Name: username, SubjectID: subject,
		Role: role, Password: testInitialPassword}
	if _, err := access.Provision(t.Context(), db, account); err != nil {
		t.Fatal(err)
	}
	return accessLogin(t, hit, username)
}

// checkInstitutionHandoff 验证运营者只发起备案变更，目标机构才能办理同备案的复测报备。
// 参数 t、hit、handler 为测试和请求入口，各会话代表运营者、目标机构、管理端，标识指定关联记录。
// 返回值 无。
func checkInstitutionHandoff(t *testing.T, hit requestAccess, handler http.Handler,
	operator, institution, first, final *http.Cookie,
	filingID, otherID, reporterID string,
) {
	t.Helper()
	const api = "/api/v1/institution/implementations"
	const application = "/api/v1/operator/filing-applications"
	session := hit(operator, "GET", "/api/v1/auth/session", nil, 200).Body.String()
	if strings.Contains(session, "implementations") {
		t.Fatalf("运营者会话仍包含报备菜单或页面: %s", session)
	}
	hit(operator, "GET", "/api/v1/operator/implementations", nil, 403)
	hit(operator, "POST", "/api/v1/operator/implementations", nil, 403)
	hit(operator, "POST", api, nil, 403)
	hit(institution, "GET", api+"/quota?filingId="+filingID, nil, 400)
	draft := createRetestApplication(t, hit, operator, application, filingID)
	choices := hit(institution, "GET", api+"/filing-options", nil, 200).Body.String()
	if !strings.Contains(choices, filingID) || strings.Contains(choices, otherID) {
		t.Fatalf("机构仅应看到明确选择本机构的复测备案: %s", choices)
	}
	hit(institution, "GET", api+"/quota?filingId="+filingID, nil, 200)
	hit(institution, "GET", api+"/quota?filingId="+otherID, nil, 400)
	reportID := submitInstitutionHandoff(t, hit, institution, filingID, reporterID, api)
	detail := hit(operator, "GET", application+"/"+draft, nil, 200).Body.String()
	if !strings.Contains(detail, `"retestImplementation":{"id":"`+reportID+`","status":"submitted"}`) {
		t.Fatalf("运营者申请未显示目标机构的关联报备状态: %s", detail)
	}
	approveInstitutionHandoff(t, hit, first, final, reportID)
	uploadRetestChangeStatement(t, handler, operator, application+"/"+draft+"/attachments")
	hit(operator, "POST", application+"/"+draft+"/submit", map[string]int64{"expectedVersion": 2}, 200)
	detail = hit(operator, "GET", application+"/"+draft, nil, 200).Body.String()
	if !strings.Contains(detail, `"implementationId":"`+reportID+`"`) {
		t.Fatalf("备案变更未绑定已通过的机构报备: %s", detail)
	}
}

// uploadRetestChangeStatement 通过运营者会话上传换机构申请的合成说明文件。
// 参数 t、handler 为测试环境，operator 为运营者会话，endpoint 为附件上传地址。
// 返回值 无。
func uploadRetestChangeStatement(t *testing.T, handler http.Handler, operator *http.Cookie, endpoint string) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("kind", "change_statement"); err != nil {
		t.Fatal(err)
	}
	file, err := form.CreateFormFile("file", "复测变更说明.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, "%PDF-1.7\n% local retest fixture\n%%EOF"); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18549"+endpoint, &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.AddCookie(operator)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("复测变更说明上传返回 %d: %s", res.Code, res.Body.String())
	}
}

// createRetestApplication 保存运营者指定复测机构的备案变更草稿。
// 参数 t、hit 为测试环境，operator 为运营者会话，application、filingID 为申请入口及主备案。
// 返回值 新建草稿标识。
func createRetestApplication(t *testing.T, hit requestAccess, operator *http.Cookie, application, filingID string) string {
	t.Helper()
	created := hit(operator, "POST", application, map[string]any{
		"filingId": filingID, "type": "change", "reason": "拟由目标机构复测",
		"changes": map[string]string{"assessmentOrg": "权限机构乙"},
	}, 201)
	var draft struct{ ID string }
	if err := json.Unmarshal(created.Body.Bytes(), &draft); err != nil || draft.ID == "" {
		t.Fatalf("未创建变更申请草稿: %s", created.Body.String())
	}
	return draft.ID
}

// approveInstitutionHandoff 通过管理端依序受理、初审、终审和复核目标机构报备。
// 参数 t、hit 为测试环境，first、final 为不同管理端账号的会话，reportID 为已提交的报备。
// 返回值 无。
func approveInstitutionHandoff(t *testing.T, hit requestAccess, first, final *http.Cookie, reportID string) {
	t.Helper()
	resource := "/api/v1/implementations/" + reportID
	implementationAction(hit, first, resource, "accept", "", 2, 200)
	implementationAction(hit, first, resource, "approve", "", 3, 200)
	implementationAction(hit, first, resource, "approve", "", 4, 409)
	implementationAction(hit, first, resource, "restart_initial", "尝试重新发起", 4, 409)
	assertImplementationReviewActions(t, hit, first, resource, "return_for_correction", "approve")
	assertImplementationReviewActions(t, hit, final, resource, "approve", "restart_initial")
	implementationAction(hit, final, resource, "approve", "", 4, 200)
	implementationAction(hit, final, resource, "approve", "", 5, 200)
	history := hit(final, "GET", resource+"/history", nil, 200)
	var result struct {
		Items []struct{ OperatorID, OperatorRole, ToStatus string }
	}
	if err := json.Unmarshal(history.Body.Bytes(), &result); err != nil || len(result.Items) != 6 {
		t.Fatalf("报备审核历史不完整: %s", history.Body.String())
	}
	firstID := implementationSessionID(t, hit, first)
	finalID := implementationSessionID(t, hit, final)
	if result.Items[1].ToStatus != "pending_supervision" || result.Items[2].ToStatus != "pending_final" ||
		result.Items[1].OperatorRole != string(access.SystemAdmin) || result.Items[2].OperatorRole != string(access.SystemAdmin) ||
		result.Items[1].OperatorID != finalID || result.Items[2].OperatorID != firstID || firstID == finalID {
		t.Fatalf("实施报备未保存两个系统管理员的稳定身份: %s", history.Body.String())
	}
}

// implementationSessionID 从正在使用的登录会话获得回归测试的实际操作者标识。
// 参数 t、hit 为测试与请求入口，session 为待核对会话。
// 返回值 该会话对应的账号标识。
func implementationSessionID(t *testing.T, hit requestAccess, session *http.Cookie) string {
	t.Helper()
	resp := hit(session, "GET", "/api/v1/auth/session", nil, 200)
	var item struct{ ID string }
	if err := json.Unmarshal(resp.Body.Bytes(), &item); err != nil || item.ID == "" {
		t.Fatalf("会话未返回账号标识: %s", resp.Body.String())
	}
	return item.ID
}

// assertImplementationReviewActions 核对详情已同步过滤审核人可用操作。
// 参数 t、hit、session 为测试环境和审核会话，resource、allowed、blocked 指定权限断言。
// 返回值 无。
func assertImplementationReviewActions(t *testing.T, hit requestAccess, session *http.Cookie,
	resource, allowed, blocked string,
) {
	t.Helper()
	resp := hit(session, "GET", resource, nil, 200)
	var item struct {
		AllowedActions []string
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &item); err != nil ||
		!slices.Contains(item.AllowedActions, allowed) || slices.Contains(item.AllowedActions, blocked) {
		t.Fatalf("详情的实施报备动作与审核约束不一致: %s", resp.Body.String())
	}
}

// implementationAction 提交一次版本绑定的实施报备状态动作。
// 参数 hit 为请求入口，session 为审核会话，resource、action、comment、version 和 status 指定请求及预期状态。
// 返回值 状态动作的响应正文。
func implementationAction(hit requestAccess, session *http.Cookie, resource, action, comment string, version, status int) string {
	return hit(session, "POST", resource+"/actions", map[string]any{
		"action": action, "expectedVersion": version, "comment": comment,
	}, status).Body.String()
}

// TestRegulatorImplementationScope 验证真实辖区会话只办理可信名录归属范围内的报备。
// 参数 t 为隔离数据库和合成备案的测试上下文。
// 返回值 无。
func TestRegulatorImplementationScope(t *testing.T) {
	hit, db, _ := implementationAccessEnvironment(t)
	unit, otherUnit, org, _ := accessSubjects(t, db)
	own := legacyImplementation(t, db, accessFiling(t, db, "IMPL-AREA-OWN", "权限单位甲", "权限机构甲", unit, org))
	other := legacyImplementation(t, db, accessFiling(t, db, "IMPL-AREA-OTHER", "权限单位乙", "权限机构甲", otherUnit, org))
	unbound := legacyImplementation(t, db, accessFiling(t, db, "IMPL-AREA-UNBOUND", "权限单位甲", "权限机构甲", unit, org))
	if _, err := db.Exec(t.Context(), `UPDATE business_cases SET directory_unit_id=NULL
		WHERE id=(SELECT filing_id FROM implementation_reports WHERE id=$1)`, unbound); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `INSERT INTO review_notices
	    (audience,resource,resource_id,event_version,state,recipient,reference,subject,comment)
	  SELECT 'institution','implementations',id,version,'returned',assessment_org,filing_number,
	    system_name,'历史回执' FROM implementation_reports WHERE id IN ($1,$2,$3)`, own, other, unbound); err != nil {
		t.Fatal(err)
	}
	county := implementationReviewer(t, hit, db, "implementation-county", "331002", access.Regulator)
	city := implementationReviewer(t, hit, db, "implementation-city", "331000", access.Regulator)
	assertImplementationArea(t, hit, county, own, other, unbound)
	assertImplementationArea(t, hit, city, other, "", unbound)
}

// assertImplementationArea 核对列表、详情、历史、预检和写入共享当前会话的数据范围。
// 参数 t、hit 为测试入口，actor 为监管会话，own、other、unbound 为各类归属的报备。
// 返回值 无。
func assertImplementationArea(t *testing.T, hit requestAccess, actor *http.Cookie, own, other, unbound string) {
	t.Helper()
	const base = "/api/v1/implementations"
	page := hit(actor, "GET", base, nil, 200).Body.String()
	if !strings.Contains(page, own) || (other != "" && strings.Contains(page, other)) || strings.Contains(page, unbound) {
		t.Fatalf("实施报备列表与辖区授权不一致: %s", page)
	}
	resource := base + "/" + own
	const receipts = "/api/v1/review-feedback/implementations/"
	if body := hit(actor, "GET", receipts+own, nil, 200).Body.String(); !strings.Contains(body, "权限机构甲") {
		t.Fatalf("辖区内的实施报备未返回真实回执: %s", body)
	}
	for _, suffix := range []string{"", "/history", "/precheck?expectedVersion=4"} {
		hit(actor, "GET", resource+suffix, nil, 200)
		for _, hidden := range []string{other, unbound} {
			if hidden != "" {
				hit(actor, "GET", base+"/"+hidden+suffix, nil, 404)
			}
		}
	}
	implementationAction(hit, actor, resource, "archive", "", 4, 403)
	for _, hidden := range []string{other, unbound} {
		if hidden == "" {
			continue
		}
		if body := hit(actor, "GET", receipts+hidden, nil, 200).Body.String(); strings.Contains(body, "权限机构甲") {
			t.Fatalf("监管员读取了范围外的报备回执: %s", body)
		}
		implementationAction(hit, actor, base+"/"+hidden, "return_for_correction", "补充材料", 4, 404)
	}
}

// TestImplementationLegacyReviewRestart 验证缺少身份的历史初审可退回初审节点并重新实名办理。
// 参数 t 为隔离数据库及真实应用路由的测试上下文。
// 返回值 无。
func TestImplementationLegacyReviewRestart(t *testing.T) {
	hit, db, _ := implementationAccessEnvironment(t)
	unit, _, org, _ := accessSubjects(t, db)
	filing := accessFiling(t, db, "IMPL-LEGACY", "权限单位甲", "权限机构甲", unit, org)
	resource := "/api/v1/implementations/" + legacyImplementation(t, db, filing)
	admin := implementationReviewer(t, hit, db, "implementation-admin", "", access.SystemAdmin)
	county := implementationReviewer(t, hit, db, "implementation-legacy", "331002", access.Regulator)
	assertLegacyImplementationRestart(t, hit, county, admin, resource)
}

// TestImplementationLegacySupervisionRestart 保证旧复核单据需要满足新审批身份规则后才可办结。
// 参数 t 为隔离数据库与真实路由测试上下文。
// 返回值 无。
func TestImplementationLegacySupervisionRestart(t *testing.T) {
	hit, db, _ := implementationAccessEnvironment(t)
	unit, _, org, _ := accessSubjects(t, db)
	filing := accessFiling(t, db, "IMPL-LEGACY-SUPERVISION", "权限单位甲", "权限机构甲", unit, org)
	legacy := legacyImplementation(t, db, filing)
	if _, err := db.Exec(t.Context(), `WITH report AS (
	    UPDATE implementation_reports SET status='pending_supervision',version=5 WHERE id=$1 RETURNING id
	  ) INSERT INTO implementation_events (report_id,version,action,from_status,to_status,operator)
	    SELECT id,5,'approve','pending_final','pending_supervision','旧办理人' FROM report`, legacy); err != nil {
		t.Fatal(err)
	}
	admin := implementationReviewer(t, hit, db, "implementation-supervision", "", access.SystemAdmin)
	resource := "/api/v1/implementations/" + legacy
	implementationAction(hit, admin, resource, "approve", "", 5, 409)
	assertImplementationReviewActions(t, hit, admin, resource, "restart_initial", "approve")
	implementationAction(hit, admin, resource, "restart_initial", "补齐历史初审和终审身份", 5, 200)
}

// assertLegacyImplementationRestart 核对旧报备不能直接终审，重启后按新一轮身份判定。
// 参数 t、hit 为测试入口，first、final 为不同审核会话，resource 为历史报备详情路径。
// 返回值 无。
func assertLegacyImplementationRestart(t *testing.T, hit requestAccess, first, final *http.Cookie, resource string) {
	t.Helper()
	blocked := implementationAction(hit, final, resource, "approve", "", 4, 409)
	if !strings.Contains(blocked, `"code":"first_reviewer_unverified"`) {
		t.Fatalf("历史报备缺少初审身份时未返回明确原因: %s", blocked)
	}
	assertImplementationReviewActions(t, hit, first, resource, "restart_initial", "approve")
	implementationAction(hit, first, resource, "restart_initial", "", 4, 400)
	implementationAction(hit, first, resource, "restart_initial", "核对历史报备的初审身份", 4, 200)
	implementationAction(hit, final, resource, "approve", "已重新初审", 5, 200)
	implementationAction(hit, final, resource, "approve", "", 6, 409)
	implementationAction(hit, first, resource, "approve", "异人终审", 6, 200)
	implementationAction(hit, first, resource, "approve", "复核通过", 7, 200)
}

// legacyImplementation 创建保留名称留痕、尚无真实初审身份的历史报备夹具。
// 参数 t、db 为测试环境，filing 为关联备案标识。
// 返回值 已通过旧初审的报备标识。
func legacyImplementation(t *testing.T, db *pgxpool.Pool, filing string) string {
	t.Helper()
	var id string
	err := db.QueryRow(t.Context(), `WITH report AS (
		INSERT INTO implementation_reports (filing_id,filing_number,system_name,organization_name,
		assessment_org,report_type,reporter,planned_start,planned_end,plan,status,created_by,
		submitted_at,accepted_at,version)
		SELECT id,number,system_name,organization_name,assessment_org,'routine','历史人员',
		current_date-1,current_date-1,'历史计划','pending_final','旧办理人',now(),now(),4
		FROM business_cases WHERE id=$1 RETURNING id
	) INSERT INTO implementation_events (report_id,version,action,from_status,to_status,operator)
	  SELECT id,4,'approve','pending_initial','pending_final','旧办理人' FROM report
	  RETURNING report_id::text`, filing).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// implementationReviewer 创建并登录指定区划或全域的真实测试账号。
// 参数 t、hit、db 为测试入口，username、area、role 为账号及辖区角色。
// 返回值 已改密并登录的会话 Cookie。
func implementationReviewer(t *testing.T, hit requestAccess, db *pgxpool.Pool,
	username, area string, role access.Role,
) *http.Cookie {
	t.Helper()
	if _, err := access.Provision(t.Context(), db, access.AccountInput{
		Username: username, PersonKey: username, Name: "报备审核员", Role: role, AreaCode: area,
		Password: testInitialPassword,
	}); err != nil {
		t.Fatal(err)
	}
	return accessLogin(t, hit, username, area)
}

// implementationAccessEnvironment 为管理端报备权限验收挂载隔离库与真实业务路由。
// 参数 t 为资源生命周期与配置检查所用测试上下文。
// 返回值 已挂载的请求入口、隔离连接池和可直接提交附件的应用路由。
func implementationAccessEnvironment(t *testing.T) (requestAccess, *pgxpool.Pool, http.Handler) {
	t.Helper()
	dsn := os.Getenv("GRADE_ACCESS_TEST_DSN")
	if dsn == "" {
		t.Skip("需要单独隔离的 GRADE_ACCESS_TEST_DSN")
	}
	db := accessTestSchema(t, dsn)
	if err := migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	records, err := cases.New(db, t.TempDir(), "报备权限测试")
	if err != nil {
		t.Fatal(err)
	}
	cfg := settings{address: "127.0.0.1:18549", reviewWindows: app.ReviewWindows{FilingDays: 3, FilingFinalDays: 5}}
	handler := routes(cfg, db, records,
		precheck.New(db, materialSource(records)), http.NotFoundHandler())
	return accessRequest(t, handler), db, handler
}

// checkHistoricalImplementation 核对旧运营者报备的历史记录和已通过的复测依据仍可复用。
// 参数 t、hit、db 为隔离测试环境，operator、institution 为会话，filingID 为历史依据所属备案。
// 返回值 无。
func checkHistoricalImplementation(t *testing.T, hit requestAccess, db *pgxpool.Pool,
	operator, institution *http.Cookie, filingID string,
) {
	t.Helper()
	var historicalID string
	err := db.QueryRow(t.Context(), `INSERT INTO implementation_reports
		(filing_id,filing_number,system_name,organization_name,assessment_org,report_type,reporter,
		 planned_start,planned_end,plan,status,created_by,submitted_at,accepted_at,quota_year)
		SELECT id,number,system_name,organization_name,'权限机构乙','change','历史报备人',
		       current_date,current_date,'历史变更报备','approved','本地运营者：'||organization_name,
		       clock_timestamp(),clock_timestamp(),EXTRACT(YEAR FROM current_date)
		FROM business_cases WHERE id=$1 RETURNING id::text`, filingID).Scan(&historicalID)
	if err != nil {
		t.Fatal(err)
	}
	hit(institution, "GET", "/api/v1/institution/implementations/"+historicalID, nil, 200)
	hit(operator, "GET", "/api/v1/operator/implementations/"+historicalID, nil, 403)
	const application = "/api/v1/operator/filing-applications"
	draft := createRetestApplication(t, hit, operator, application, filingID)
	detail := hit(operator, "GET", application+"/"+draft, nil, 200).Body.String()
	if !strings.Contains(detail, `"retestImplementation":{"id":"`+historicalID+`","status":"approved"}`) {
		t.Fatalf("历史已通过报备不再可供换机构申请使用: %s", detail)
	}
}

// submitInstitutionHandoff 通过目标机构路由保存并提交与原备案关联的变更报备。
// 参数 t、hit 为测试和请求入口，institution 为会话，filingID、reporterID 和 endpoint 为办理条件。
// 返回值 已提交的报备标识。
func submitInstitutionHandoff(t *testing.T, hit requestAccess, institution *http.Cookie,
	filingID, reporterID, endpoint string,
) string {
	t.Helper()
	start := time.Now().AddDate(0, 0, 7).Format(time.DateOnly)
	form := map[string]string{
		"filingId": filingID, "reporterId": reporterID, "reporter": "换机构报备人员",
		"type": "routine", "plannedStart": start, "plannedEnd": start, "plan": "验证复测报备与变更申请的关联。",
	}
	hit(institution, "POST", endpoint, form, 400)
	form["type"] = "change"
	created := hit(institution, "POST", endpoint, form, 201)
	var draft struct {
		ID      string
		Version int64
	}
	if err := json.Unmarshal(created.Body.Bytes(), &draft); err != nil || draft.ID == "" {
		t.Fatalf("未创建目标机构报备: %s", created.Body.String())
	}
	hit(institution, "POST", endpoint+"/"+draft.ID+"/actions",
		map[string]any{"action": "submit", "expectedVersion": draft.Version}, 200)
	return draft.ID
}
