package implementations

import (
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const firstImplementationPath = "/api/v1/institution/implementations"
const firstImplementationInstitution = "备案测评机构"

// TestInstitutionFirstImplementationOptions 验证机构只能查询和使用本机构当前有效的备案。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestInstitutionFirstImplementationOptions(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(t, pool)
	own := implementationTestFiling(t, pool, "首次报备-本机构备案", approved)
	foreign := implementationTestFiling(t, pool, "首次报备-外机构备案", approved)
	cancelled := implementationTestFiling(t, pool, "首次报备-历史备案", approved)
	pending := implementationTestFiling(t, pool, "首次报备-未批准备案", initial)
	setImplementationFilingInstitution(t, pool, foreign, "另一家机构")
	if _, err := pool.Exec(t.Context(), `UPDATE business_cases SET cancelled_at=now() WHERE id=$1`, cancelled); err != nil {
		t.Fatal(err)
	}
	scope := firstImplementationScope()
	choices := decodeResponse[struct{ Items []choice }](t, callImplementation(mux, http.MethodGet,
		firstImplementationPath+"/filing-options"+scope+"&q="+url.QueryEscape("首次报备"), nil), 200)
	if len(choices.Items) != 1 || choices.Items[0].ID != own {
		t.Fatalf("机构备案选项 = %+v", choices.Items)
	}
	current := decodeResponse[quota](t, callImplementation(mux, http.MethodGet,
		firstImplementationPath+"/quota"+scope+"&filingId="+own, nil), 200)
	if current != (quota{Year: 2027, Used: 0, Limit: 3}) {
		t.Fatalf("机构额度 = %+v", current)
	}
	for _, filing := range []string{foreign, pending, cancelled} {
		assertInstitutionFilingRejected(t, mux, filing)
	}
	forged := testInput(own, routine)
	forged.AssessmentOrg = "另一家机构"
	result := decodeResponse[map[string]string](t,
		callImplementation(mux, http.MethodPost, firstImplementationPath+scope, forged), 400)
	if result["code"] != "validation_error" {
		t.Fatalf("伪造测评机构响应 = %+v", result)
	}
}

// TestInstitutionFirstImplementationList 验证机构报备列表及游标不会泄漏其他机构的数据。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestInstitutionFirstImplementationList(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(t, pool)
	filing := implementationTestFiling(t, pool, "首次报备-列表分页", approved)
	first := createInstitutionImplementation(t, mux, testInput(filing, change))
	second := createInstitutionImplementation(t, mux, testInput(filing, change))
	other := testInput(filing, change)
	other.AssessmentOrg = "另一家机构"
	decodeResponse[mutation](t, callImplementation(mux, http.MethodPost, path, other), 201)
	resource := firstImplementationPath + firstImplementationScope()
	filter := "&status=draft&type=change&archive=unarchived&limit=1&q=" + url.QueryEscape("列表分页")
	page1 := decodeResponse[page](t, callImplementation(mux, http.MethodGet, resource+filter, nil), 200)
	if len(page1.Items) != 1 || page1.Items[0].ID != second.ID || page1.NextCursor == "" {
		t.Fatalf("机构列表第一页 = %+v", page1)
	}
	page2 := decodeResponse[page](t, callImplementation(mux, http.MethodGet, resource+filter+"&cursor="+page1.NextCursor, nil), 200)
	if len(page2.Items) != 1 || page2.Items[0].ID != first.ID || page2.NextCursor != "" {
		t.Fatalf("机构列表第二页 = %+v", page2)
	}
	for _, endpoint := range []string{firstImplementationPath, resource + "&institution=another", resource + "&unknown=true"} {
		decodeResponse[map[string]string](t, callImplementation(mux, http.MethodGet, endpoint, nil), 400)
	}
}

// TestInstitutionFirstImplementationProgression 验证机构草稿编辑、首次提交、撤销和监管审核留痕。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestInstitutionFirstImplementationProgression(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(t, pool)
	filing := implementationTestFiling(t, pool, "首次报备-流转", approved)
	item := testInput(filing, change)
	record := createInstitutionImplementation(t, mux, item)
	scope := firstImplementationScope()
	resource := firstImplementationPath + "/" + record.ID
	draftDetail := decodeResponse[detail](t, callImplementation(mux, http.MethodGet, resource+scope, nil), 200)
	if draftDetail.AssessmentOrg != firstImplementationInstitution ||
		!slices.Equal(draftDetail.AllowedActions, []string{"edit", "submit"}) {
		t.Fatalf("机构草稿响应 = %+v", draftDetail)
	}
	item.Plan = "先核对割接窗口，再安排测评。"
	updated := decodeResponse[mutation](t, callImplementation(mux, http.MethodPatch, resource+scope,
		patchInput{input: item, ExpectedVersion: record.Version}), 200)
	submittedRecord := institutionImplementationAction(t, mux, updated, "submit", 200)
	if submittedRecord.Status != submitted || submittedRecord.Version != updated.Version+1 {
		t.Fatalf("机构首次提交响应 = %+v", submittedRecord)
	}
	assertQuota(t, mux, filing, 1)
	assertInstitutionFirstWithdrawal(t, mux, filing, submittedRecord)
	assertInstitutionFirstAudit(t, mux, submittedRecord.ID, item.Plan)
}

// TestInstitutionImplementationRechecksFiling 校验备案重新分配后无法提交旧草稿或篡改关联机构。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestInstitutionImplementationRechecksFiling(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(t, pool)
	filing := implementationTestFiling(t, pool, "首次报备-备案改派", approved)
	newFiling := implementationTestFiling(t, pool, "首次报备-有效替换备案", approved)
	item := testInput(filing, routine)
	record := createInstitutionImplementation(t, mux, item)
	setImplementationFilingInstitution(t, pool, filing, "另一家机构")
	resource := firstImplementationPath + "/" + record.ID
	scope := firstImplementationScope()
	for _, attempted := range []struct {
		method, endpoint string
		payload          any
	}{
		{http.MethodPatch, resource + scope, patchInput{input: item, ExpectedVersion: record.Version}},
		{http.MethodPost, resource + "/actions" + scope, command{Action: "submit", ExpectedVersion: record.Version}},
	} {
		result := decodeResponse[map[string]string](t, callImplementation(mux, attempted.method, attempted.endpoint, attempted.payload), 400)
		if result["code"] != "invalid_filing" {
			t.Fatalf("备案改派后的操作结果 = %+v", result)
		}
	}
	item.FilingID = newFiling
	updated := decodeResponse[mutation](t, callImplementation(mux, http.MethodPatch, resource+scope,
		patchInput{input: item, ExpectedVersion: record.Version}), 200)
	if result := institutionImplementationAction(t, mux, updated, "submit", 200); result.Status != submitted {
		t.Fatalf("有效新备案的提交 = %+v", result)
	}
}

// TestInstitutionFirstImplementationSharesAnnualQuota 验证两个办理入口共用同一备案年度变更限额。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestInstitutionFirstImplementationSharesAnnualQuota(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(t, pool)
	filing := implementationTestFiling(t, pool, "首次报备-共享配额", approved)
	item := testInput(filing, change)
	regulatory := decodeResponse[mutation](t, callImplementation(mux, http.MethodPost, path, item), 201)
	performAction(t, mux, regulatory, "submit", "")
	item.AssessmentOrg = firstImplementationInstitution
	first := createInstitutionImplementation(t, mux, item)
	institutionImplementationAction(t, mux, first, "submit", 200)
	second := createInstitutionImplementation(t, mux, item)
	institutionImplementationAction(t, mux, second, "submit", 200)
	third := createInstitutionImplementation(t, mux, item)
	endpoint := firstImplementationPath + "/" + third.ID + "/actions" + firstImplementationScope()
	failed := decodeResponse[map[string]string](t, callImplementation(mux, http.MethodPost, endpoint,
		command{Action: "submit", ExpectedVersion: third.Version}), 409)
	if failed["code"] != "change_quota" {
		t.Fatalf("第四份变更报备响应 = %+v", failed)
	}
	readQuota := firstImplementationPath + "/quota" + firstImplementationScope() + "&filingId=" + filing
	actual := decodeResponse[quota](t, callImplementation(mux, http.MethodGet, readQuota, nil), 200)
	if actual != (quota{Year: 2027, Used: 3, Limit: 3}) {
		t.Fatalf("两个入口的共享额度 = %+v", actual)
	}
}

// assertInstitutionFilingRejected 核对禁止使用取消、未通过或其他机构备案。
// 参数 t 为测试上下文，mux 为业务路由，filing 为不应关联的备案标识。
// 返回值 无。
func assertInstitutionFilingRejected(t *testing.T, mux *http.ServeMux, filing string) {
	t.Helper()
	scope := firstImplementationScope()
	result := decodeResponse[map[string]string](t,
		callImplementation(mux, http.MethodPost, firstImplementationPath+scope, testInput(filing, routine)), 400)
	quotaResult := decodeResponse[map[string]string](t,
		callImplementation(mux, http.MethodGet, firstImplementationPath+"/quota"+scope+"&filingId="+filing, nil), 400)
	if result["code"] != "invalid_filing" || quotaResult["code"] != "invalid_filing" {
		t.Fatalf("无效备案的报备和额度结果 = %+v / %+v", result, quotaResult)
	}
}

// assertInstitutionFirstWithdrawal 验证测评开始前的机构撤销授权、额度释放和重报冷却。
// 参数 t 为测试上下文，mux 为业务路由，filing 为用于校验额度的备案，current 为已提交的报备。
// 返回值 无。
func assertInstitutionFirstWithdrawal(t *testing.T, mux *http.ServeMux, filing string, current mutation) {
	t.Helper()
	scope := firstImplementationScope()
	resource := firstImplementationPath + "/" + current.ID
	detail := decodeResponse[detail](t, callImplementation(mux, http.MethodGet, resource+scope, nil), 200)
	if !slices.Equal(detail.AllowedActions, []string{"edit", "withdraw"}) {
		t.Fatalf("首次提交后可办理动作 = %+v", detail.AllowedActions)
	}
	wrong := "?institution=" + url.QueryEscape("另一家机构")
	decodeResponse[map[string]string](t, callImplementation(mux, http.MethodPost, resource+"/actions"+wrong,
		command{Action: "withdraw", ExpectedVersion: current.Version}), 404)
	if value := institutionImplementationAction(t, mux, current, "withdraw", 200); value.Status != withdrawn {
		t.Fatalf("机构撤销后的状态 = %+v", value)
	}
	assertQuota(t, mux, filing, 0)
	second := createInstitutionImplementation(t, mux, testInput(filing, change))
	response := callImplementation(mux, http.MethodPost, firstImplementationPath+"/"+second.ID+"/actions"+scope,
		command{Action: "submit", ExpectedVersion: second.Version})
	assertImplementationRuleFailure(t, response, "resubmission_cooldown")
}

// assertInstitutionFirstAudit 查看监管侧同一报备的资料和服务端机构留痕。
// 参数 t 为测试上下文，mux 为业务路由，id 与 plan 为实际报备标识和期望资料。
// 返回值 无。
func assertInstitutionFirstAudit(t *testing.T, mux *http.ServeMux, id, plan string) {
	t.Helper()
	resource := path + "/" + id
	current := decodeResponse[detail](t, callImplementation(mux, http.MethodGet, resource, nil), 200)
	history := decodeResponse[eventPage](t, callImplementation(mux, http.MethodGet, resource+"/history", nil), 200)
	if current.Plan != plan || current.AssessmentOrg != firstImplementationInstitution || len(history.Items) != 4 {
		t.Fatalf("监管收到的实施详情和留痕 = %+v / %+v", current, history.Items)
	}
	for _, event := range history.Items {
		if event.Operator != "本地机构："+firstImplementationInstitution {
			t.Fatalf("错误的实施报备经办机构 = %+v", event)
		}
	}
}

// createInstitutionImplementation 通过真实机构路由保存首次实施报备草稿。
// 参数 t 为测试上下文，mux 为业务路由，item 为完整报备表单。
// 返回值 机构草稿的标识、状态及版本。
func createInstitutionImplementation(t *testing.T, mux *http.ServeMux, item input) mutation {
	t.Helper()
	return decodeResponse[mutation](t,
		callImplementation(mux, http.MethodPost, firstImplementationPath+firstImplementationScope(), item), 201)
}

// institutionImplementationAction 执行当前测试机构的报备动作。
// 参数 t 为测试上下文，mux 为业务路由，record 为已见版本，action 和 status 为办理和响应条件。
// 返回值 已更新的报备。
func institutionImplementationAction(t *testing.T, mux *http.ServeMux, record mutation, action string, status int) mutation {
	t.Helper()
	return decodeResponse[mutation](t, callImplementation(mux, http.MethodPost,
		firstImplementationPath+"/"+record.ID+"/actions"+firstImplementationScope(),
		command{Action: action, ExpectedVersion: record.Version}), status)
}

// setImplementationFilingInstitution 模拟已批准的备案变更生效后的新机构归属。
// 参数 t 为测试上下文，pool 为隔离数据库，id 和 institution 为原备案及新归属。
// 返回值 无。
func setImplementationFilingInstitution(t *testing.T, pool *pgxpool.Pool, id, institution string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE business_cases SET assessment_org=$2 WHERE id=$1`, id, institution); err != nil {
		t.Fatal(err)
	}
}

// firstImplementationScope 为首次报备测试生成正确的机构查询参数。
// 参数 无。
// 返回值 包含机构名称的查询部分。
func firstImplementationScope() string {
	return "?institution=" + url.QueryEscape(firstImplementationInstitution)
}
