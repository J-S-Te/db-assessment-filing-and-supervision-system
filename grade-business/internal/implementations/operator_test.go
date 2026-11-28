package implementations

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const operatorTestPath = "/api/v1/operator/implementations"
const operatorTestOrganization = "测试单位"
const replacementInstitution = "拟换入测评机构"

// TestOperatorChangeImplementationScope 验证运营者只能为自有有效备案申请不同机构的变更报备。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestOperatorChangeImplementationScope(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := operatorImplementationTestMux(t, pool)
	own := implementationTestFiling(t, pool, "本单位复测-有效", approved)
	foreign := implementationTestFiling(t, pool, "本单位复测-他方", approved)
	cancelled := implementationTestFiling(t, pool, "本单位复测-注销", approved)
	setOperatorFilingField(t, pool, `UPDATE business_cases SET organization_name='其他单位' WHERE id=$1`, foreign)
	setOperatorFilingField(t, pool, `UPDATE business_cases SET cancelled_at=now() WHERE id=$1`, cancelled)
	scope := operatorImplementationScope(operatorTestOrganization)
	choices := decodeResponse[struct{ Items []choice }](t, callImplementation(mux, http.MethodGet,
		operatorTestPath+"/filing-options"+scope+"&q="+url.QueryEscape("本单位复测"), nil), 200)
	if len(choices.Items) != 1 || choices.Items[0].ID != own {
		t.Fatalf("运营者备案选项 = %+v", choices.Items)
	}
	assertOperatorChangeCreationValidation(t, mux, own, foreign, cancelled)
	item := testInput(own, change)
	item.AssessmentOrg = replacementInstitution
	record := createOperatorImplementation(t, mux, item)
	assertOperatorImplementationVisibility(t, mux, record)
}

// TestOperatorChangeImplementationExactFilingOption 验证运营者可精确加载旧备案且查询不越过归属和有效性。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestOperatorChangeImplementationExactFilingOption(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := operatorImplementationTestMux(t, pool)
	older := implementationTestFiling(t, pool, "较早备案-预填条件", approved)
	for index := range 20 {
		implementationTestFiling(t, pool, fmt.Sprintf("近期备案-%02d", index), approved)
	}
	foreign := implementationTestFiling(t, pool, "其他单位备案", approved)
	cancelled := implementationTestFiling(t, pool, "已注销备案", approved)
	pending := implementationTestFiling(t, pool, "待审核备案", initial)
	setOperatorFilingField(t, pool, `UPDATE business_cases SET organization_name='其他单位' WHERE id=$1`, foreign)
	setOperatorFilingField(t, pool, `UPDATE business_cases SET cancelled_at=now() WHERE id=$1`, cancelled)
	endpoint := operatorTestPath + "/filing-options" + operatorImplementationScope(operatorTestOrganization)
	list := decodeResponse[struct{ Items []choice }](t, callImplementation(mux, http.MethodGet, endpoint, nil), 200)
	if len(list.Items) != 20 || slices.ContainsFunc(list.Items, func(item choice) bool { return item.ID == older }) {
		t.Fatalf("旧备案不应出现于默认有限候选项：%+v", list.Items)
	}
	exact := endpoint + "&filingId=" + older
	result := decodeResponse[struct{ Items []choice }](t, callImplementation(mux, http.MethodGet, exact, nil), 200)
	if len(result.Items) != 1 || result.Items[0].ID != older || result.Items[0].AssessmentOrg != firstImplementationInstitution {
		t.Fatalf("精确候选备案 = %+v", result.Items)
	}
	assertOperatorExcludedExactFilings(t, mux, endpoint, exact, foreign, cancelled, pending)
}

// assertOperatorExcludedExactFilings 核对过滤组合不返回外单位或无效备案并拒绝非法条件。
// 参数 t 为测试上下文，mux 为业务路由，endpoint 和 exact 为普通及精确查询，ids 为不可选择的备案。
// 返回值 无。
func assertOperatorExcludedExactFilings(t *testing.T, mux *http.ServeMux, endpoint, exact string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		result := decodeResponse[struct{ Items []choice }](t,
			callImplementation(mux, http.MethodGet, endpoint+"&filingId="+id, nil), 200)
		if result.Items == nil || len(result.Items) != 0 {
			t.Fatalf("不可选择的备案被返回：%+v", result.Items)
		}
	}
	filtered := decodeResponse[struct{ Items []choice }](t,
		callImplementation(mux, http.MethodGet, exact+"&q="+url.QueryEscape("无匹配关键词"), nil), 200)
	if len(filtered.Items) != 0 {
		t.Fatalf("精确备案与关键词未同时应用：%+v", filtered.Items)
	}
	for _, condition := range []string{endpoint + "&filingId=invalid", endpoint + "&filingId=", exact + "&filingId=duplicate"} {
		decodeResponse[map[string]string](t, callImplementation(mux, http.MethodGet, condition, nil), 400)
	}
}

// TestOperatorChangeImplementationReview 验证申请人补正重提、监管三阶段办理和双边回传。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestOperatorChangeImplementationReview(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := operatorImplementationTestMux(t, pool)
	filing := implementationTestFiling(t, pool, "换机构复测-流转", approved)
	item := testInput(filing, change)
	item.AssessmentOrg = replacementInstitution
	record := createOperatorImplementation(t, mux, item)
	record = performOperatorImplementationAction(t, mux, record, "submit")
	record = performAction(t, mux, record, "accept", "")
	record = performAction(t, mux, record, "return_for_correction", "请补充复测安排。")
	returnedVersion := record.Version
	record = correctOperatorImplementation(t, mux, record, item)
	record = performOperatorImplementationAction(t, mux, record, "submit")
	record = performAction(t, mux, record, "accept", "")
	for _, stage := range []string{final, supervision, approved} {
		record = performAction(t, mux, record, "approve", "通过复测报备。")
		if record.Status != stage {
			t.Fatalf("审核状态 = %+v, 期望 %s", record, stage)
		}
	}
	assertQuota(t, mux, filing, 1)
	assertOperatorReviewDeliveries(t, pool, record, returnedVersion)
	assertOperatorImplementationHistory(t, mux, record)
}

// TestOperatorChangeImplementationQuotaAndWithdrawal 验证运营者报备与监管报备共用年度额度和撤销规则。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestOperatorChangeImplementationQuotaAndWithdrawal(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := operatorImplementationTestMux(t, pool)
	filing := implementationTestFiling(t, pool, "换机构复测-配额", approved)
	item := testInput(filing, change)
	item.AssessmentOrg = replacementInstitution
	admin := decodeResponse[mutation](t, callImplementation(mux, http.MethodPost, path, item), 201)
	admin = performAction(t, mux, admin, "submit", "")
	assertOperatorImplementationError(t, mux, admin, "withdraw", 409, "withdraw_unavailable")
	first := performOperatorImplementationAction(t, mux, createOperatorImplementation(t, mux, item), "submit")
	second := performOperatorImplementationAction(t, mux, createOperatorImplementation(t, mux, item), "submit")
	third := createOperatorImplementation(t, mux, item)
	assertOperatorImplementationError(t, mux, third, "submit", 409, "change_quota")
	first = performOperatorImplementationAction(t, mux, first, "withdraw")
	if first.Status != withdrawn {
		t.Fatalf("撤销状态 = %+v", first)
	}
	assertOperatorImplementationError(t, mux, third, "submit", 409, "resubmission_cooldown")
	setOperatorFilingField(t, pool, `UPDATE implementation_reports SET submitted_at='2026-12-29 00:00:00+08' WHERE id=$1`, second.ID)
	assertOperatorImplementationError(t, mux, second, "withdraw", 409, "withdraw_unavailable")
	second = performAction(t, mux, second, "accept", "")
	assertOperatorImplementationError(t, mux, second, "withdraw", 409, "withdraw_unavailable")
	assertQuota(t, mux, filing, 2)
}

// TestOperatorChangeImplementationRechecksFiling 验证提交时重新校验备案当前机构和当前单位。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestOperatorChangeImplementationRechecksFiling(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := operatorImplementationTestMux(t, pool)
	filing := implementationTestFiling(t, pool, "换机构复测-当前归属", approved)
	item := testInput(filing, change)
	item.AssessmentOrg = replacementInstitution
	record := createOperatorImplementation(t, mux, item)
	setImplementationFilingInstitution(t, pool, filing, replacementInstitution)
	assertOperatorImplementationError(t, mux, record, "submit", 400, "invalid_assessment_org")
	setImplementationFilingInstitution(t, pool, filing, firstImplementationInstitution)
	setOperatorFilingField(t, pool, `UPDATE business_cases SET organization_name='其他单位' WHERE id=$1`, filing)
	assertOperatorImplementationError(t, mux, record, "submit", 404, "not_found")
	other := operatorImplementationScope("其他单位")
	current := decodeResponse[detail](t, callImplementation(mux, http.MethodGet, operatorTestPath+"/"+record.ID+other, nil), 200)
	if len(current.AllowedActions) != 0 {
		t.Fatalf("备案的新所属单位得到了申请人权限 = %+v", current)
	}
}

// TestOperatorReviewNoticeRollback 验证回传写入失败同时回滚实施状态和流转记录。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestOperatorReviewNoticeRollback(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := operatorImplementationTestMux(t, pool)
	filing := implementationTestFiling(t, pool, "换机构复测-通知原子性", approved)
	item := testInput(filing, change)
	item.AssessmentOrg = replacementInstitution
	record := createOperatorImplementation(t, mux, item)
	record = performOperatorImplementationAction(t, mux, record, "submit")
	record = performAction(t, mux, record, "accept", "")
	if _, err := pool.Exec(t.Context(), `ALTER TABLE review_notices ADD CHECK (audience <> 'operator')`); err != nil {
		t.Fatal(err)
	}
	resource := path + "/" + record.ID
	decodeResponse[map[string]string](t, callImplementation(mux, http.MethodPost, resource+"/actions",
		command{Action: "return_for_correction", Comment: "核对监管意见", ExpectedVersion: record.Version}), 500)
	read := decodeResponse[detail](t, callImplementation(mux, http.MethodGet, resource, nil), 200)
	history := decodeResponse[eventPage](t, callImplementation(mux, http.MethodGet, resource+"/history", nil), 200)
	if read.Status != initial || read.Version != record.Version || len(history.Items) != 3 || history.Items[0].Action != "accept" {
		t.Fatalf("失败事务仍保存部分结果：%+v / %+v", read, history)
	}
}

// assertOperatorChangeCreationValidation 检查无效备案、普通报备和相同测评机构均被拒绝。
// 参数 t 为测试上下文，mux 为业务路由，own、foreign、cancelled 为不同状态的备案标识。
// 返回值 无。
func assertOperatorChangeCreationValidation(t *testing.T, mux *http.ServeMux, own, foreign, cancelled string) {
	t.Helper()
	scope := operatorImplementationScope(operatorTestOrganization)
	for _, filing := range []string{foreign, cancelled} {
		item := testInput(filing, change)
		item.AssessmentOrg = replacementInstitution
		decodeResponse[map[string]string](t, callImplementation(mux, http.MethodPost, operatorTestPath+scope, item), 400)
		decodeResponse[map[string]string](t, callImplementation(mux, http.MethodGet,
			operatorTestPath+"/quota"+scope+"&filingId="+filing, nil), 400)
	}
	for _, target := range []string{"", firstImplementationInstitution} {
		item := testInput(own, change)
		item.AssessmentOrg = target
		decodeResponse[map[string]string](t, callImplementation(mux, http.MethodPost, operatorTestPath+scope, item), 400)
	}
	item := testInput(own, routine)
	item.AssessmentOrg = replacementInstitution
	decodeResponse[map[string]string](t, callImplementation(mux, http.MethodPost, operatorTestPath+scope, item), 400)
}

// assertOperatorImplementationVisibility 核对报备只对当前单位、申请指定机构和监管可见。
// 参数 t 为测试上下文，mux 为业务路由，record 为创建的实施报备。
// 返回值 无。
func assertOperatorImplementationVisibility(t *testing.T, mux *http.ServeMux, record mutation) {
	t.Helper()
	scope := operatorImplementationScope(operatorTestOrganization)
	resource := operatorTestPath + "/" + record.ID
	list := decodeResponse[page](t, callImplementation(mux, http.MethodGet, operatorTestPath+scope+"&type=change&status=draft", nil), 200)
	if len(list.Items) != 1 || list.Items[0].ID != record.ID || list.Items[0].AssessmentOrg != replacementInstitution {
		t.Fatalf("运营者列表 = %+v", list)
	}
	other := operatorImplementationScope("其他单位")
	list = decodeResponse[page](t, callImplementation(mux, http.MethodGet, operatorTestPath+other, nil), 200)
	if len(list.Items) != 0 {
		t.Fatalf("泄漏其他单位报备 = %+v", list.Items)
	}
	decodeResponse[map[string]string](t, callImplementation(mux, http.MethodGet, resource+other, nil), 404)
	decodeResponse[map[string]string](t, callImplementation(mux, http.MethodGet, resource+"/review-history"+other, nil), 404)
	decodeResponse[map[string]string](t, callImplementation(mux, http.MethodGet, operatorTestPath, nil), 400)
	decodeResponse[map[string]string](t, callImplementation(mux, http.MethodGet, institutionalPath+"/"+record.ID+firstImplementationScope(), nil), 404)
	result := decodeResponse[detail](t, callImplementation(mux, http.MethodGet, resource+scope, nil), 200)
	if !slices.Equal(result.AllowedActions, []string{"edit", "submit"}) {
		t.Fatalf("发起人的草稿动作 = %+v", result)
	}
	decodeResponse[detail](t, callImplementation(mux, http.MethodGet, institutionalPath+"/"+record.ID+
		"?institution="+url.QueryEscape(replacementInstitution), nil), 200)
}

// correctOperatorImplementation 校验退回的实施报备只有发起人可以补正并且关键字段固定。
// 参数 t 为测试上下文，mux 为业务路由，record、item 为现存版本与报备表单。
// 返回值 补正后的报备版本。
func correctOperatorImplementation(t *testing.T, mux *http.ServeMux, record mutation, item input) mutation {
	t.Helper()
	resource := operatorTestPath + "/" + record.ID
	scope := operatorImplementationScope(operatorTestOrganization)
	assigned := institutionalPath + "/" + record.ID + "?institution=" + url.QueryEscape(replacementInstitution)
	view := decodeResponse[detail](t, callImplementation(mux, http.MethodGet, assigned, nil), 200)
	if len(view.AllowedActions) != 0 {
		t.Fatalf("被指定机构不应获得运营者补正权限 = %+v", view.AllowedActions)
	}
	item.Plan = "补充复测窗口并联系新机构。"
	patch := patchInput{input: item, ExpectedVersion: record.Version}
	decodeResponse[map[string]string](t, callImplementation(mux, http.MethodPatch, assigned, patch), 409)
	patch.AssessmentOrg = "另一目标机构"
	decodeResponse[map[string]string](t, callImplementation(mux, http.MethodPatch, resource+scope, patch), 409)
	patch.AssessmentOrg = ""
	return decodeResponse[mutation](t, callImplementation(mux, http.MethodPatch, resource+scope, patch), 200)
}

// assertOperatorReviewDeliveries 核对实际退回和监管复核通过后存在双边意见。
// 参数 t 为测试上下文，pool 为隔离数据库，item 为最终报备，returnedVersion 为退回事件版本。
// 返回值 无。
func assertOperatorReviewDeliveries(t *testing.T, pool *pgxpool.Pool, item mutation, returnedVersion int64) {
	t.Helper()
	var matching, all int
	err := pool.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE
	  ((audience='operator' AND recipient=$2) OR (audience='institution' AND recipient=$3))
	  AND ((state='returned' AND event_version=$4) OR (state='approved' AND event_version=$5))), count(*)
	  FROM review_notices WHERE resource='implementations' AND resource_id=$1`,
		item.ID, operatorTestOrganization, replacementInstitution, returnedVersion, item.Version).Scan(&matching, &all)
	if err != nil || matching != 4 || all != 4 {
		t.Fatalf("双边回传条数 = %d / %d，数据库错误 = %v", matching, all, err)
	}
}

// assertOperatorImplementationHistory 检查发起人留痕及审核事件分页。
// 参数 t 为测试上下文，mux 为业务路由，record 为报备记录。
// 返回值 无。
func assertOperatorImplementationHistory(t *testing.T, mux *http.ServeMux, record mutation) {
	t.Helper()
	resource := operatorTestPath + "/" + record.ID + "/review-history" + operatorImplementationScope(operatorTestOrganization)
	history := decodeResponse[eventPage](t, callImplementation(mux, http.MethodGet, resource+"&limit=2", nil), 200)
	if len(history.Items) != 2 || history.Items[0].ToStatus != approved || history.NextBeforeVersion == 0 {
		t.Fatalf("运营者审核记录 = %+v", history)
	}
	all := decodeResponse[eventPage](t, callImplementation(mux, http.MethodGet, resource, nil), 200)
	last := all.Items[len(all.Items)-1]
	if last.Action != "create" || last.Operator != "本地运营者："+operatorTestOrganization {
		t.Fatalf("运营者发起人留痕为 %+v", last)
	}
}

// assertOperatorImplementationError 校验运营者命令拒绝时的状态和错误代码。
// 参数 t 为测试上下文，mux 为路由，record、action 为命令，status、code 为预期错误。
// 返回值 无。
func assertOperatorImplementationError(t *testing.T, mux *http.ServeMux, record mutation, action string, status int, code string) {
	t.Helper()
	endpoint := operatorTestPath + "/" + record.ID + "/" + action + operatorImplementationScope(operatorTestOrganization)
	value := decodeResponse[map[string]string](t, callImplementation(mux, http.MethodPost, endpoint,
		map[string]int64{"expectedVersion": record.Version}), status)
	if value["code"] != code {
		t.Fatalf("运营者动作 %s 的响应 = %+v", action, value)
	}
}

// createOperatorImplementation 为当前测试运营者创建实施草稿。
// 参数 t 为测试上下文，mux 为路由，item 为实施报备方式及关联信息。
// 返回值 草稿的标识、状态与版本。
func createOperatorImplementation(t *testing.T, mux *http.ServeMux, item input) mutation {
	t.Helper()
	return decodeResponse[mutation](t, callImplementation(mux, http.MethodPost,
		operatorTestPath+operatorImplementationScope(operatorTestOrganization), item), 201)
}

// performOperatorImplementationAction 提交一个运营者版本化命令并读取结果。
// 参数 t 为测试上下文，mux 为路由，record 与 action 为命令版本及名称。
// 返回值 命令成功后的报备数据。
func performOperatorImplementationAction(t *testing.T, mux *http.ServeMux, record mutation, action string) mutation {
	t.Helper()
	endpoint := operatorTestPath + "/" + record.ID + "/" + action + operatorImplementationScope(operatorTestOrganization)
	return decodeResponse[mutation](t, callImplementation(mux, http.MethodPost, endpoint,
		map[string]int64{"expectedVersion": record.Version}), 200)
}

// operatorImplementationScope 对测试单位的查询参数进行 URL 编码。
// 参数 organization 为当前运营单位名称。
// 返回值 可用于请求路径的查询部分。
func operatorImplementationScope(organization string) string {
	return "?organization=" + url.QueryEscape(organization)
}

// setOperatorFilingField 为隔离测试模拟备案归属、状态或实施时间已变更。
// 参数 t 为测试上下文，pool 为隔离连接，query、id 为测试 SQL 和记录标识。
// 返回值 无。
func setOperatorFilingField(t *testing.T, pool *pgxpool.Pool, query, id string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), query, id); err != nil {
		t.Fatal(err)
	}
}
