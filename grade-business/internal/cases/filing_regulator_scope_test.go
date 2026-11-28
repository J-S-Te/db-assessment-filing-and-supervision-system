package cases

import (
	"net/http"
	"testing"

	"grade-business/internal/access"
)

// TestRegulatorFilingWithdrawalAudit 验证撤回确认与正式历史保存实际监管会话身份。
// 参数 t 为隔离数据库及真实业务路由测试上下文。
// 返回值 无。
func TestRegulatorFilingWithdrawalAudit(t *testing.T) {
	service, raw := operatorFilingFixture(t)
	owner := operatorFilingQuery("辖区撤回单位")
	item := createOperatorFiling(t, raw, owner)
	local := operatorFilingsURL + "/" + item.ID
	submitted := submitOperatorFiling(t, raw, local, owner, item.Version)
	bindRegulatorFixtureUnit(t, service, item.ID)
	pending := withdrawalRequestTest(t, raw, local+"/withdrawal"+owner,
		map[string]any{"expectedVersion": submitted.Version, "reason": "材料需要更正"}, "pending")
	regulator, actorID := regulatorFilingSession(t, service, raw.(*http.ServeMux))
	out := withdrawalRequestTest(t, regulator, filingsURL+"/"+item.ID+"/withdrawal/decision", map[string]any{
		"withdrawalId": pending["id"], "expectedVersion": pending["caseVersion"],
		"decision": approve, "comment": "同意撤回更正",
	}, approved)
	if out["decidedBy"] != "真实辖区审核员" {
		t.Fatalf("撤回回执没有保存实际审核人: %+v", out)
	}
	assertWithdrawalActor(t, service, item.ID, actorID)
}

// assertWithdrawalActor 核对确认回执及正式审批事件关联当前监管账号及角色。
// 参数 t、service 提供测试隔离库，id 与 actorID 分别为备案和通过授权网关的账号标识。
// 返回值 无。
func assertWithdrawalActor(t *testing.T, service *Service, id, actorID string) {
	t.Helper()
	var actor, role, eventActor, eventRole string
	err := service.db.QueryRow(t.Context(), `SELECT w.decided_by_id::text,w.decided_by_role,
		e.operator_id::text,e.operator_role FROM filing_withdrawal_requests w
		JOIN case_review_events e ON e.case_id=w.filing_id AND e.version=w.case_version
		WHERE w.filing_id=$1 AND e.action='withdraw'`, id).Scan(&actor, &role, &eventActor, &eventRole)
	if err != nil || actor != actorID || role != string(access.Regulator) || eventActor != actor || eventRole != role {
		t.Fatalf("撤回人身份与会话不一致: 回执 %s/%s，事件 %s/%s，错误 %v", actor, role, eventActor, eventRole, err)
	}
}

// TestRegulatorCannotApproveFilingOwnerChange 验证辖区监管权限不足以改写主备案所属单位。
// 参数 t 为隔离数据库及真实业务路由测试上下文。
// 返回值 无。
func TestRegulatorCannotApproveFilingOwnerChange(t *testing.T) {
	service, raw := fixtureService(t)
	filing := approvedFiling(t, raw, "REGULATOR-OWNER-CHANGE")
	bindRegulatorFixtureUnit(t, service, filing.ID)
	form := map[string]any{"filingId": filing.ID, "type": "change", "reason": "单位正在调整",
		"changes": map[string]string{"organizationName": "另一法律单位"}}
	draft := readResponse[mutation](t, requestJSON(raw, "POST", applicationURL, form), 201)
	resource := applicationURL + "/" + draft.ID
	item := readResponse[mutation](t, requestJSON(raw, "POST", resource+"/submit", versionInput{draft.Version}), 200)
	regulator, _ := regulatorFilingSession(t, service, raw.(*http.ServeMux))
	assertError(t, reviewRequest(regulator, resource, approve, "", item.Version),
		http.StatusForbidden, "filing_owner_change_requires_admin")
	detail := readResponse[applicationDetail](t, requestJSON(regulator, "GET", resource, nil), 200)
	if detail.Version != item.Version || len(detail.AllowedActions) != 1 || detail.AllowedActions[0] != returnForFix {
		t.Fatalf("辖区详情未按受限权限返回待审操作: %+v", detail)
	}
	initial := readResponse[mutation](t, reviewRequest(raw, resource, approve, "", item.Version), 200)
	result := readResponse[mutation](t, reviewRequest(raw, resource, approve, "", initial.Version), 200)
	if result.Status != approved {
		t.Fatalf("管理端旧流程未保留: %+v", result)
	}
}

// bindRegulatorFixtureUnit 保存与测试备案姓名关联的可信区划单位。
// 参数 t、service 为测试环境，id 为应能由测试监管席位读取的备案。
// 返回值 无。
func bindRegulatorFixtureUnit(t *testing.T, service *Service, id string) {
	t.Helper()
	if _, err := service.db.Exec(t.Context(), `WITH unit AS (
		INSERT INTO directory_parties (kind,scope_name,name,code,district)
		SELECT 'unit',organization_name,organization_name,'913310020000000027','椒江区'
		FROM business_cases WHERE id=$1 RETURNING id
	) UPDATE business_cases c SET directory_unit_id=unit.id FROM unit WHERE c.id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

// regulatorFilingSession 经实际应用登录和改密为隔离测试获取监管用户会话。
// 参数 t、service 为测试环境，mux 为待接入会话网关的业务路由。
// 返回值 附带已改密会话的请求入口及实际审核账号标识。
func regulatorFilingSession(t *testing.T, service *Service, mux *http.ServeMux) (http.Handler, string) {
	t.Helper()
	const username, initial, next = "fixture-county-reviewer", "Fixture!79020a", "Fixture!68230b"
	id, err := access.Provision(t.Context(), service.db, access.AccountInput{
		Username: username, PersonKey: username, Name: "真实辖区审核员", Role: access.Regulator,
		AreaCode: "331002", Password: initial,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := access.New(service.db).Protect(mux)
	first := caseFixtureLogin(t, handler, username, initial)
	if res := requestJSON(withFilingSession(handler, first), "POST", "/api/v1/auth/password",
		map[string]string{"currentPassword": initial, "newPassword": next}); res.Code != 204 {
		t.Fatalf("监管测试账号未完成改密: %d %s", res.Code, res.Body.String())
	}
	return withFilingSession(handler, caseFixtureLogin(t, handler, username, next)), id
}

// caseFixtureLogin 通过应用登录接口取得真实测试会话。
// 参数 t、handler 为测试入口，username、password 为一次性测试账号凭据。
// 返回值 实际签发的会话 Cookie。
func caseFixtureLogin(t *testing.T, handler http.Handler, username, password string) *http.Cookie {
	t.Helper()
	resp := requestJSON(handler, "POST", "/api/v1/auth/login",
		map[string]string{"username": username, "password": password})
	cookies := resp.Result().Cookies()
	if resp.Code != 200 || len(cookies) != 1 {
		t.Fatalf("监管测试账号登录失败: %d %s", resp.Code, resp.Body.String())
	}
	return cookies[0]
}

// withFilingSession 为隔离 HTTP 用例绑定应用已签发的监管会话。
// 参数 handler 为业务入口，session 为登录后的会话 Cookie。
// 返回值 自动携带该会话的测试请求入口。
func withFilingSession(handler http.Handler, session *http.Cookie) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(session)
		handler.ServeHTTP(w, r)
	})
}
