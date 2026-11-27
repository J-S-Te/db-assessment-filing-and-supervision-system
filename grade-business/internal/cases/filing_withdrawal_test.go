package cases

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestOperatorFilingWithdrawalConfirmsAndReleasesNumber 验证撤回等待、并发重放、留痕和旧编号续办。
// 参数 t 为真实隔离业务库和临时附件测试上下文。
// 返回值 无。
func TestOperatorFilingWithdrawalConfirmsAndReleasesNumber(t *testing.T) {
	s, handler := operatorFilingFixture(t)
	owner := operatorFilingQuery("本地备案单位")
	foreign := operatorFilingQuery("其他单位")
	item := createOperatorFiling(t, handler, owner)
	if _, err := s.db.Exec(t.Context(), `UPDATE business_cases SET number='BA-SELF' WHERE id=$1`, item.ID); err != nil {
		t.Fatal(err)
	}
	local, admin := operatorFilingsURL+"/"+item.ID, filingsURL+"/"+item.ID
	partner, scope := institutionBase+"/filings/"+item.ID, firstSubmissionScope("隔离测评机构")
	sent := submitOperatorFiling(t, handler, local, owner, item.Version)
	input := map[string]any{"expectedVersion": sent.Version, "reason": "备案材料需重新报送"}
	requestURL := local + "/withdrawal"
	snapshot := readResponse[map[string]any](t, requestJSON(handler, http.MethodGet, requestURL+owner, nil), 200)
	if snapshot["canRequest"] != true {
		t.Fatalf("缺少在审撤回入口: %+v", snapshot)
	}
	assertError(t, requestJSON(handler, http.MethodPost, requestURL+foreign, input), 404, "not_found")
	assertError(t, requestJSON(handler, http.MethodPost, requestURL+owner,
		map[string]any{"expectedVersion": sent.Version, "reason": "  "}), 400, "invalid_input")
	pending, duplicate := concurrentWithdrawalTest(t, handler, requestURL+owner, input)
	if pending["id"] != duplicate["id"] {
		t.Fatalf("相同撤回请求产生多个编号: %+v, %+v", pending, duplicate)
	}
	version := int64(pending["caseVersion"].(float64))
	assertError(t, scopedReview(handler, partner, scope, approve, version), 409, "withdrawal_pending")
	assigned := readResponse[listResponse](t, requestJSON(handler, http.MethodGet,
		institutionBase+"/filings"+scope+"&review=assigned", nil), 200)
	if len(assigned.Items) != 0 {
		t.Fatalf("等待监管撤回确认的首报还留在初审列表: %+v", assigned)
	}
	assertError(t, requestJSON(handler, http.MethodGet,
		partner+"/withdrawal"+firstSubmissionScope("其他机构"), nil), 404, "not_found")
	assertOperatorFilingScopeStatus(t, handler, item.ID, owner, pendingInitial)
	var taskAudience string
	err := s.db.QueryRow(t.Context(), `SELECT audience FROM workflow_tasks WHERE id=$1`, item.ID).Scan(&taskAudience)
	if err != nil || taskAudience != "regulator" {
		t.Fatalf("撤回未转监管待办: %q, %v", taskAudience, err)
	}
	accept := map[string]any{"withdrawalId": pending["id"], "expectedVersion": version,
		"decision": "approve", "comment": "允许撤回后重新提交"}
	done := withdrawalRequestTest(t, handler, admin+"/withdrawal/decision", accept, "approved")
	replay := withdrawalRequestTest(t, handler, admin+"/withdrawal/decision", accept, "approved")
	if done["caseVersion"] != replay["caseVersion"] {
		t.Fatalf("重复审批意外改变版本: %+v, %+v", done, replay)
	}
	assertOperatorFilingScopeStatus(t, handler, item.ID, owner, applicationWithdrawn)
	assertOperatorFilingReceipt(t, handler, local+"/receipt"+owner, "提交回执", "BA-SELF")
	assertAssignedNotice(t, s, "filings", item.ID, version+1, "withdrawal_approved", "institution", "隔离测评机构")
	assertAssignedNotice(t, s, "filings", item.ID, version+1, "withdrawal_approved", "operator", "本地备案单位")
	if next := createFiling(t, handler, "BA-SELF"); next.ID == item.ID {
		t.Fatalf("同一编号的新草稿覆盖旧记录: %+v", next)
	}
	active := readResponse[listResponse](t, requestJSON(handler, http.MethodGet,
		operatorFilingsURL+owner+"&lifecycle=active", nil), 200)
	if len(active.Items) != 0 {
		t.Fatalf("已撤回首报仍出现在有效筛选: %+v", active)
	}
}

// concurrentWithdrawalTest 核对相同操作并发重放时都得到已保存的同一个申请。
// 参数 t、handler 为测试环境，path、input 为操作路径与相同字段。
// 返回值 两个实际 HTTP 请求所读到的撤回。
func concurrentWithdrawalTest(t *testing.T, handler http.Handler, path string,
	input map[string]any,
) (map[string]any, map[string]any) {
	t.Helper()
	type response struct{ Request map[string]any }
	var requests sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		requests.Go(func() {
			results <- requestJSON(handler, http.MethodPost, path, input)
		})
	}
	requests.Wait()
	first := readResponse[response](t, <-results, 200)
	second := readResponse[response](t, <-results, 200)
	if first.Request["status"] != "pending" || second.Request["status"] != "pending" {
		t.Fatalf("并发未返回待确认申请: %+v, %+v", first, second)
	}
	return first.Request, second.Request
}

// TestWithdrawalAndInitialReviewSerialize 验证同版本的撤回和正常初审批只能有一个优先落库。
// 参数 t 为隔离数据库及临时材料测试上下文。
// 返回值 无。
func TestWithdrawalAndInitialReviewSerialize(t *testing.T) {
	_, handler := operatorFilingFixture(t)
	owner := operatorFilingQuery("本地备案单位")
	item := createOperatorFiling(t, handler, owner)
	local := operatorFilingsURL + "/" + item.ID
	sent := submitOperatorFiling(t, handler, local, owner, item.Version)
	var both sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	both.Go(func() {
		results <- requestJSON(handler, http.MethodPost, local+"/withdrawal"+owner,
			map[string]any{"expectedVersion": sent.Version, "reason": "按并发版本核对"})
	})
	both.Go(func() {
		results <- scopedReview(handler, institutionBase+"/filings/"+item.ID,
			firstSubmissionScope("隔离测评机构"), approve, sent.Version)
	})
	both.Wait()
	first, second := <-results, <-results
	if !((first.Code == 200 && second.Code == 409) || (first.Code == 409 && second.Code == 200)) {
		t.Fatalf("同版本竞争应只有一条成功: %d %s；%d %s",
			first.Code, first.Body.String(), second.Code, second.Body.String())
	}
}

// TestWithdrawalNoticeFailureRollsBack 验证通知落库失败时撤回请求和原版本一并回滚。
// 参数 t 为可独立施加数据库约束的测试上下文。
// 返回值 无。
func TestWithdrawalNoticeFailureRollsBack(t *testing.T) {
	service, handler := operatorFilingFixture(t)
	owner := operatorFilingQuery("本地备案单位")
	item := createOperatorFiling(t, handler, owner)
	local := operatorFilingsURL + "/" + item.ID
	sent := submitOperatorFiling(t, handler, local, owner, item.Version)
	_, err := service.db.Exec(t.Context(), `ALTER TABLE review_notices ADD CONSTRAINT block_withdrawal_notice
		CHECK (state<>'withdrawal_requested')`)
	if err != nil {
		t.Fatal(err)
	}
	path := local + "/withdrawal" + owner
	input := map[string]any{"expectedVersion": sent.Version, "reason": "验证原子恢复"}
	assertError(t, requestJSON(handler, http.MethodPost, path, input), 500, "internal_error")
	detail := readResponse[recordDetail](t, requestJSON(handler, http.MethodGet, local+owner, nil), 200)
	if detail.Version != sent.Version || detail.Status != pendingInitial {
		t.Fatalf("撤回拒写后原审核状态有部分变更: %+v", detail)
	}
	out := readResponse[map[string]any](t, requestJSON(handler, http.MethodGet, path, nil), 200)
	if out["request"] != nil || out["canRequest"] != true {
		t.Fatalf("回滚后仍出现撤回记录: %+v", out)
	}
	_, err = service.db.Exec(t.Context(), `ALTER TABLE review_notices DROP CONSTRAINT block_withdrawal_notice`)
	if err != nil {
		t.Fatal(err)
	}
	withdrawalRequestTest(t, handler, path, input, "pending")
}

// withdrawalRequestTest 提交撤回变更并校验状态。
// 参数 t、handler、path 为测试入口和目标，input、state 为提交内容和预期状态。
// 返回值 已保存的撤回回执。
func withdrawalRequestTest(t *testing.T, handler http.Handler, path string,
	input map[string]any, state string,
) map[string]any {
	t.Helper()
	payload := readResponse[struct{ Request map[string]any }](t,
		requestJSON(handler, http.MethodPost, path, input), 200)
	if payload.Request["status"] != state {
		t.Fatalf("撤回状态应为 %s: %+v", state, payload)
	}
	return payload.Request
}

// TestOperatorApplicationWithdrawalRejectsThenApproves 验证驳回恢复审核，批准释放占用且不修改主备案。
// 参数 t 为使用隔离 PostgreSQL 的测试上下文。
// 返回值 无。
func TestOperatorApplicationWithdrawalRejectsThenApproves(t *testing.T) {
	service, handler := fixtureService(t)
	service.RegisterOperatorApplicationRoutes(handler.(*http.ServeMux))
	filing := approvedFiling(t, handler, "BA-WITH-APP")
	scope := operatorFilingQuery("测试单位")
	input := map[string]any{"filingId": filing.ID, "type": "cancel", "reason": "原定下线"}
	created := readResponse[mutation](t, requestJSON(handler, http.MethodPost,
		operatorApplicationURL+scope, input), http.StatusCreated)
	local, admin := operatorApplicationURL+"/"+created.ID, applicationURL+"/"+created.ID
	file := uploadApplicationFile(t, handler, local, scope, "cancellation_statement")
	readResponse[Attachment](t, file, http.StatusCreated)
	sent := readResponse[mutation](t, requestJSON(handler, http.MethodPost,
		local+"/submit"+scope, versionInput{created.Version + 1}), 200)
	pending := withdrawalRequestTest(t, handler, local+"/withdrawal"+scope,
		map[string]any{"expectedVersion": sent.Version, "reason": "下线窗口调整"}, "pending")
	rejected := withdrawalRequestTest(t, handler, admin+"/withdrawal/decision", map[string]any{
		"withdrawalId": pending["id"], "expectedVersion": pending["caseVersion"], "decision": "reject", "comment": "请继续原审核",
	}, "rejected")
	partner := institutionBase + "/filing-applications/" + created.ID
	result := readResponse[mutation](t, scopedReview(handler, partner, firstSubmissionScope("测评中心"),
		approve, int64(rejected["caseVersion"].(float64))), 200)
	if result.Status != pendingFinal {
		t.Fatalf("撤回驳回后未恢复正常审核: %+v", result)
	}
	pending = withdrawalRequestTest(t, handler, local+"/withdrawal"+scope,
		map[string]any{"expectedVersion": result.Version, "reason": "停用计划撤销"}, "pending")
	withdrawalRequestTest(t, handler, admin+"/withdrawal/decision", map[string]any{
		"withdrawalId": pending["id"], "expectedVersion": pending["caseVersion"], "decision": "approve", "comment": "确认撤回",
	}, "approved")
	detail := readResponse[recordDetail](t, requestJSON(handler, http.MethodGet, filingsURL+"/"+filing.ID, nil), 200)
	if detail.Status != approved || detail.CancelledAt != nil || detail.Version != filing.Version {
		t.Fatalf("撤回误改主备案: %+v", detail)
	}
	readResponse[mutation](t, requestJSON(handler, http.MethodPost, operatorApplicationURL+scope, input), http.StatusCreated)
}
