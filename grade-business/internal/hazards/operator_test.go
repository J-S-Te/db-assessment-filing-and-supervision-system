package hazards

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"grade-business/internal/cases"
)

// TestOperatorHazardRequiresOrganization 验证企业端需要唯一的精确单位范围。
// 参数 t 为 HTTP 合同测试上下文。
// 返回值 无。
func TestOperatorHazardRequiresOrganization(t *testing.T) {
	mux := http.NewServeMux()
	New(nil, nil, "测试席位").RegisterOperator(mux)
	path := "/api/v1/operator/hazards/a550b84f-a8ec-4f2e-9dfa-49ecb7bc681b"
	for _, query := range []string{"", "?organization=", "?organization=甲&organization=乙", "?organization=甲&other=x"} {
		assertLedgerError(t, ledgerCall(mux, "GET", path+query, nil), 400, "invalid_input")
	}
}

// TestOperatorHandlesOwnHazard 验证企业的整改证据及提交权限，并防止越权销号。
// 参数 t 为隔离数据库和证据文件测试上下文。
// 返回值 无。
func TestOperatorHandlesOwnHazard(t *testing.T) {
	service, router := ledgerFixture(t)
	path, _, _ := syncedPaths(t, service, router)
	operator := "/api/v1/operator/hazards/" + strings.TrimPrefix(path, testLedgerPath+"/")
	owner := "?organization=" + url.QueryEscape("试点单位")
	other := "?organization=" + url.QueryEscape("试点")
	assertLedgerError(t, ledgerCall(router, "GET", operator+other, nil), 404, "not_found")
	open := ledgerReply[hazardDetail](t, ledgerCall(router, "GET", operator+owner, nil), 200)
	if !slices.Equal(open.AllowedActions, []string{actionSubmit}) {
		t.Fatalf("open hazard actions: %v", open.AllowedActions)
	}
	assertLedgerError(t, ledgerCall(router, "GET", operator+"/attachments"+other, nil), 404, "not_found")
	assertLedgerError(t, ledgerUpload(t, router, operator+other, kindEvidence, fixturePDF, 1), 404, "not_found")
	file := ledgerReply[cases.Attachment](t, ledgerUpload(t, router, operator+owner, kindEvidence, fixturePDF, 1), 201)
	download := ledgerCall(router, "GET", operator+"/attachments/"+file.ID+owner, nil)
	if download.Code != 200 || download.Body.String() != fixturePDF {
		t.Fatalf("incorrect scoped download: %d %q", download.Code, download.Body.String())
	}
	assertLedgerError(t, ledgerCall(router, "GET", operator+"/attachments/"+file.ID+other, nil), 404, "not_found")
	submit := actionInput{Action: actionSubmit, ExpectedVersion: open.Version + 1, Comment: "企业已整改"}
	assertLedgerError(t, ledgerCall(router, "POST", operator+"/actions"+other, submit), 404, "not_found")
	pending := ledgerReply[hazardMutation](t, ledgerCall(router, "POST", operator+"/actions"+owner, submit), 200)
	state := ledgerReply[hazardDetail](t, ledgerCall(router, "GET", operator+owner, nil), 200)
	if pending.Status != statusRetest || len(state.AllowedActions) != 0 {
		t.Fatalf("operator could review own evidence: %+v", state)
	}
	closeReq := actionInput{Action: actionClose, ExpectedVersion: pending.Version, Comment: "复核证据"}
	assertLedgerError(t, ledgerCall(router, "POST", operator+"/actions"+owner, closeReq), 409, "conflict")
	closeReq.ExpectedVersion = passInstitutionRetest(t, router, pending.ID, pending.Version).Version
	ledgerReply[hazardMutation](t, ledgerCall(router, "POST", path+"/actions", closeReq), 200)
	var actual []string
	err := service.db.QueryRow(t.Context(), `SELECT array_agg(state ORDER BY event_version) FROM review_notices
		WHERE audience='operator' AND resource='hazards' AND resource_id=$1 AND recipient='试点单位'`,
		open.ID).Scan(&actual)
	if err != nil || !slices.Equal(actual, []string{"open", "pending_review", "closed"}) {
		t.Fatalf("incorrect hazard notices: %v, %v", actual, err)
	}
}

// TestOperatorHazardLedgerHistoryAndReceipt 验证运营者查询、历史及回执始终按当前备案单位收敛。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestOperatorHazardLedgerHistoryAndReceipt(t *testing.T) {
	service, router := ledgerFixture(t)
	admin, _, plan := syncedPaths(t, service, router)
	item := ledgerReply[hazardDetail](t, ledgerCall(router, "GET", admin, nil), 200)
	base := "/api/v1/operator/hazards"
	path := base + "/" + item.ID
	owner := "?organization=" + url.QueryEscape("试点单位")
	filters := "&planId=" + plan + "&filingId=" + item.FilingID + "&q=" + url.QueryEscape(item.SystemName)
	listed := ledgerReply[hazardPage](t, ledgerCall(router, "GET", base+owner+filters+"&severity="+item.Severity, nil), 200)
	if len(listed.Items) != 1 || listed.Items[0].ID != item.ID {
		t.Fatalf("incorrect scoped ledger: %+v", listed)
	}
	assertOperatorHazardPageBoundary(t, router, base+owner+filters)
	assertLedgerError(t, ledgerCall(router, "GET", base+owner+"&filingId=bad", nil), 400, "invalid_input")
	assertLedgerError(t, ledgerCall(router, "GET", base+owner+"&organization=重复单位", nil), 400, "invalid_input")
	assertLedgerError(t, ledgerCall(router, "GET", path+"/receipt"+owner, nil), 409, "receipt_unavailable")
	initial := ledgerReply[map[string]any](t, ledgerCall(router, "GET", path+owner, nil), 200)
	if initial["receiptAvailable"] != false {
		t.Fatalf("unexpected draft receipt flag: %v", initial)
	}
	openHistory := ledgerReply[hazardCollection[hazardEvent]](t, ledgerCall(router, "GET", path+"/history"+owner, nil), 200)
	if openHistory.Items == nil || len(openHistory.Items) != 0 {
		t.Fatalf("new hazard should have an empty history: %+v", openHistory)
	}
	ledgerReply[cases.Attachment](t, ledgerUpload(t, router, path+owner, kindEvidence, fixturePDF, 1), 201)
	submitted := ledgerReply[hazardMutation](t, ledgerCall(router, "POST", path+"/actions"+owner,
		actionInput{Action: actionSubmit, ExpectedVersion: item.Version + 1, Comment: "提交复核"}), 200)
	assertHazardReceipt(t, router, path+"/receipt"+owner, "整改提交回执", item.FilingNumber)
	pending := passInstitutionRetest(t, router, submitted.ID, submitted.Version)
	ledgerReply[hazardMutation](t, ledgerCall(router, "POST", admin+"/actions",
		actionInput{Action: actionClose, ExpectedVersion: pending.Version}), 200)
	assertHazardReceipt(t, router, path+"/receipt"+owner, "隐患销项回执", item.FilingNumber)
	assertHazardReadScopeAfterTransfer(t, service, router, base, path, owner)
}

// assertOperatorHazardPageBoundary 核对同一范围的分页连续性及去重。
// 参数 t 为测试上下文，router 为实际路由，path 为包含单位及备案筛选的列表地址。
// 返回值 无。
func assertOperatorHazardPageBoundary(t *testing.T, router http.Handler, path string) {
	t.Helper()
	first := ledgerReply[hazardPage](t, ledgerCall(router, "GET", path+"&limit=1", nil), 200)
	if len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("scoped list omitted its next cursor: %+v", first)
	}
	second := ledgerReply[hazardPage](t, ledgerCall(router, "GET", path+"&limit=1&cursor="+url.QueryEscape(first.NextCursor), nil), 200)
	if len(second.Items) != 1 || second.NextCursor != "" || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("scoped pagination duplicated a record: first=%+v second=%+v", first, second)
	}
}

// assertHazardReceipt 核对实际 HTTP 下载使用事件类型和备案标识生成回执。
// 参数 t 为测试上下文，router、path、title 和 filing 指定路由、回执及备案。
// 返回值 无。
func assertHazardReceipt(t *testing.T, router http.Handler, path, title, filing string) {
	t.Helper()
	file := ledgerCall(router, "GET", path, nil)
	if file.Code != 200 || !strings.Contains(file.Body.String(), title) || !strings.Contains(file.Body.String(), filing) ||
		file.Header().Get("Content-Type") != "text/plain; charset=utf-8" ||
		!strings.Contains(file.Header().Get("Content-Disposition"), "hazard-receipt.txt") {
		t.Fatalf("incorrect audit receipt: %d %s", file.Code, file.Body.String())
	}
}

// assertHazardReadScopeAfterTransfer 核对备案单位变更后的访问范围。
// 参数 t 为测试上下文，service、router 为隔离服务和路由，base、path 和 owner 为列表、详情及原范围。
// 返回值 无。
func assertHazardReadScopeAfterTransfer(t *testing.T, service *Service, router http.Handler, base, path, owner string) {
	t.Helper()
	if _, err := service.db.Exec(t.Context(), `UPDATE business_cases SET organization_name='新登记单位' WHERE kind='filing'`); err != nil {
		t.Fatal(err)
	}
	listed := ledgerReply[hazardPage](t, ledgerCall(router, "GET", base+owner, nil), 200)
	if len(listed.Items) != 0 {
		t.Fatalf("former organization received stale items: %+v", listed)
	}
	assertLedgerError(t, ledgerCall(router, "GET", path+"/history"+owner, nil), 404, "not_found")
	assertLedgerError(t, ledgerCall(router, "GET", path+"/receipt"+owner, nil), 404, "not_found")
	newOwner := "?organization=" + url.QueryEscape("新登记单位")
	events := ledgerReply[hazardCollection[hazardEvent]](t, ledgerCall(router, "GET", path+"/history"+newOwner, nil), 200)
	if len(events.Items) != 3 || events.Items[0].Action != actionClose || events.Items[1].Action != actionRetestPass ||
		events.Items[2].Action != actionSubmit {
		t.Fatalf("processing history is incomplete: %+v", events)
	}
	current := ledgerReply[map[string]any](t, ledgerCall(router, "GET", path+newOwner, nil), 200)
	if current["receiptAvailable"] != true {
		t.Fatalf("receipt flag did not reflect events: %v", current)
	}
}
