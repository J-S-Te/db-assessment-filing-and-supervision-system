package implementations

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestImplementationPlanChangesCloseAtShanghaiMidnight 验证发起人可在测评开始前修订计划，开始后可撤销在办报备。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestImplementationPlanChangesCloseAtShanghaiMidnight(t *testing.T) {
	pool := isolatedImplementationPool(t)
	start := time.Date(2027, 1, 4, 0, 0, 0, 0, shanghai)
	now := start.Add(-time.Minute)
	mux := implementationPlanTestMux(pool, &now)
	filing := implementationTestFiling(t, pool, "RULE-PLAN-DEADLINE", approved)
	form := testInput(filing, routine)
	record := createInstitutionImplementation(t, mux, form)
	record = institutionImplementationAction(t, mux, record, "submit", http.StatusOK)
	scope := firstImplementationScope()
	current := institutionalPath + "/" + record.ID
	form.Plan = "补充影响范围和停机计划。"
	assertImplementationRuleFailure(t, callImplementation(mux, http.MethodPatch, path+"/"+record.ID,
		patchInput{input: form, ExpectedVersion: record.Version}), "invalid_state")
	record = decodeResponse[mutation](t, callImplementation(mux, http.MethodPatch, current+scope,
		patchInput{input: form, ExpectedVersion: record.Version}), http.StatusOK)
	before := assertImplementationPlan(t, mux, current+scope, "补充影响范围和停机计划。", submitted, "edit")
	if before.PlanEditDeadline == nil || !before.PlanEditDeadline.Equal(start) {
		t.Fatalf("计划的测评日零点 = %v", before.PlanEditDeadline)
	}
	now = start
	form.Plan = "不得覆盖的计划。"
	assertImplementationRuleFailure(t, callImplementation(mux, http.MethodPatch, current+scope,
		patchInput{input: form, ExpectedVersion: record.Version}), "plan_edit_deadline")
	record = performAction(t, mux, record, "accept", "")
	record = performAction(t, mux, record, "return_for_correction", "补充联系人。")
	assertImplementationRuleFailure(t, callImplementation(mux, http.MethodPost, current+"/actions"+scope,
		command{Action: "submit", ExpectedVersion: record.Version}), "plan_edit_deadline")
	assertImplementationPlan(t, mux, current+scope, "补充影响范围和停机计划。", returned, "withdraw")
	institutionImplementationAction(t, mux, record, "withdraw", http.StatusOK)
	result := assertImplementationPlan(t, mux, current+scope, "补充影响范围和停机计划。", withdrawn, "")
	if result.AcceptedAt == nil || !result.AcceptedAt.Equal(start) {
		t.Fatalf("撤销未保留首次受理时间：%+v", result.AcceptedAt)
	}
}

// TestImplementationWithdrawalCooldownSpansSubmissionEntrypoints 验证同备案三端在冷却期内无法提交。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestImplementationWithdrawalCooldownSpansSubmissionEntrypoints(t *testing.T) {
	pool := isolatedImplementationPool(t)
	withdrawal := time.Date(2027, 1, 3, 12, 0, 0, 0, shanghai)
	now := withdrawal
	mux := implementationPlanTestMux(pool, &now)
	filing := implementationTestFiling(t, pool, "RULE-COOLDOWN", approved)
	form := testInput(filing, routine)
	record := decodeResponse[mutation](t, callImplementation(mux, http.MethodPost, path, form), http.StatusCreated)
	record = performAction(t, mux, record, "submit", "")
	form.PlannedStart, form.PlannedEnd = "2027-01-25", "2027-01-27"
	admin := decodeResponse[mutation](t, callImplementation(mux, http.MethodPost, path, form), http.StatusCreated)
	admin = performAction(t, mux, admin, "submit", "")
	admin = performAction(t, mux, admin, "accept", "")
	admin = performAction(t, mux, admin, "return_for_correction", "核对实施范围。")
	performAction(t, mux, record, "withdraw", "")
	institution := createInstitutionImplementation(t, mux, form)
	form.Type, form.AssessmentOrg = change, replacementInstitution
	operator := createOperatorImplementation(t, mux, form)
	available := withdrawal.AddDate(0, 0, 15)
	now = available.Add(-time.Nanosecond)
	assertImplementationCooldown(t, mux, path+"/"+admin.ID, available)
	quotaView := decodeResponse[quota](t,
		callImplementation(mux, http.MethodGet, path+"/quota?filingId="+filing, nil), http.StatusOK)
	if quotaView.ResubmitAt == nil || !quotaView.ResubmitAt.Equal(available) {
		t.Fatalf("额度未返回真正的冷却截止时刻：%+v", quotaView)
	}
	assertImplementationRuleFailure(t, callImplementation(mux, http.MethodPost, path+"/"+admin.ID+"/actions",
		command{Action: "submit", ExpectedVersion: admin.Version}), "resubmission_cooldown")
	assertImplementationRuleFailure(t, callImplementation(mux, http.MethodPost,
		institutionalPath+"/"+institution.ID+"/actions"+firstImplementationScope(),
		command{Action: "submit", ExpectedVersion: institution.Version}), "resubmission_cooldown")
	assertOperatorImplementationError(t, mux, operator, "submit", http.StatusConflict, "resubmission_cooldown")
	now = available
	performAction(t, mux, admin, "submit", "")
	institutionImplementationAction(t, mux, institution, "submit", http.StatusOK)
	performOperatorImplementationAction(t, mux, operator, "submit")
}

// assertImplementationCooldown 核对冷却期详情显示实际截止并停用提交。
// 参数 t、mux 为测试上下文及路由，target 为详情地址，available 为预期冷却截止。
// 返回值 无。
func assertImplementationCooldown(t *testing.T, mux *http.ServeMux, target string, available time.Time) {
	t.Helper()
	view := decodeResponse[detail](t, callImplementation(mux, http.MethodGet, target, nil), http.StatusOK)
	if view.ResubmitAt == nil || !view.ResubmitAt.Equal(available) || slices.Contains(view.AllowedActions, "submit") {
		t.Fatalf("详情未返回实际冷却规则：%+v", view)
	}
}

// implementationPlanTestMux 注册三端真实报备路由，并提供可控的业务时钟。
// 参数 pool 为隔离数据库，now 为测试当前模拟时刻。
// 返回值 监听三个办理范围的路由。
func implementationPlanTestMux(pool *pgxpool.Pool, now *time.Time) *http.ServeMux {
	service := New(pool, "测试办理人")
	service.now = func() time.Time { return *now }
	mux := http.NewServeMux()
	service.Register(mux)
	service.RegisterOperatorRoutes(mux)
	return mux
}

// assertImplementationPlan 核对详情中保存的计划、状态及当前可执行动作。
// 参数 t、mux 为测试上下文和路由，target、plan、state、action 为路径及预期。
// 返回值 已读取的实际详情。
func assertImplementationPlan(t *testing.T, mux *http.ServeMux, target, plan, state, action string) detail {
	t.Helper()
	record := decodeResponse[detail](t, callImplementation(mux, http.MethodGet, target, nil), http.StatusOK)
	if record.Plan != plan || record.Status != state || (action != "" && !slices.Contains(record.AllowedActions, action)) {
		t.Fatalf("实施计划、状态或可执行动作与预期不符：%+v", record)
	}
	return record
}

// assertImplementationRuleFailure 验证请求被预期的实施规则拒绝。
// 参数 t 为测试上下文，response 为响应，code 为业务错误码。
// 返回值 无。
func assertImplementationRuleFailure(t *testing.T, response *httptest.ResponseRecorder, code string) {
	t.Helper()
	result := decodeResponse[map[string]string](t, response, http.StatusConflict)
	if result["code"] != code {
		t.Fatalf("规则错误为 %v，预期 %s", result, code)
	}
}
