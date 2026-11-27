package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/access"
	"grade-business/internal/cases"
	"grade-business/internal/precheck"
	"grade-business/migrations"
)

// TestInstitutionOwnsImplementation 验证实际会话权限及目标机构承接换机构复测的报备入口。
// 参数 t 为显式隔离数据库和临时附件目录的测试上下文。
// 返回值 无。
func TestInstitutionOwnsImplementation(t *testing.T) {
	dsn := os.Getenv("GRADE_ACCESS_TEST_DSN")
	if dsn == "" {
		t.Skip("需要单独隔离的 GRADE_ACCESS_TEST_DSN")
	}
	db := accessTestSchema(t, dsn)
	if err := migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
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
	records, err := cases.New(db, t.TempDir(), "报备权限测试")
	if err != nil {
		t.Fatal(err)
	}
	handler := routes(settings{address: "127.0.0.1:18549"}, db, records,
		precheck.New(db, materialSource(records)), http.NotFoundHandler())
	hit := accessRequest(t, handler)
	operator := loginImplementationSubject(t, hit, db, "retest-operator", unit, access.Operator)
	institution := loginImplementationSubject(t, hit, db, "retest-institution", target, access.OrgAdmin)
	admin := access.AccountInput{Username: "retest-admin", Name: "复测监管员", PersonKey: "retest-test",
		Role: access.SystemAdmin, Password: testInitialPassword}
	if _, err := access.Provision(t.Context(), db, admin); err != nil {
		t.Fatal(err)
	}
	regulator := accessLogin(t, hit, admin.Username)
	checkInstitutionHandoff(t, hit, handler, operator, institution, regulator, filingID, otherID, reporterID)
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
	operator, institution, admin *http.Cookie,
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
	approveInstitutionHandoff(t, hit, admin, reportID)
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
// 参数 t、hit 为测试环境，admin 为管理端会话，reportID 为已提交的报备。
// 返回值 无。
func approveInstitutionHandoff(t *testing.T, hit requestAccess, admin *http.Cookie, reportID string) {
	t.Helper()
	resource := "/api/v1/implementations/" + reportID + "/actions"
	for i, action := range []string{"accept", "approve", "approve", "approve"} {
		hit(admin, "POST", resource, map[string]any{"action": action, "expectedVersion": i + 2}, 200)
	}
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
