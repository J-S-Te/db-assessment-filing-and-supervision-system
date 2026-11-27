package hazards

import (
	"net/http"
	"net/url"
	"sync"
	"testing"

	"grade-business/internal/cases"
)

// TestDirectRegistrationDeduplicatesBeforeDispatch 验证监管登记重试和预派单隔离。
// 参数 t 为真实隔离 PostgreSQL 和文件目录的测试上下文。
// 返回值 无。
func TestDirectRegistrationDeduplicatesBeforeDispatch(t *testing.T) {
	service, handler := ledgerFixture(t)
	_, _, filing := seedPlan(t, service.db)
	payload := map[string]string{"ledger": "major_risk", "filingId": filing,
		"title": "Fixture Risk  Alpha", "severity": "medium", "source": "manual",
		"discoveredOn": "2026-01-01", "deadline": "2099-01-01", "description": "监管验收发现",
		"measure": "落实整改并提交证明", "responsibility": "运维组"}
	assertLedgerError(t, ledgerCall(handler, http.MethodPost, testLedgerPath+"?debug=1", payload), 400, "invalid_input")
	created := ledgerReply[hazardMutation](t, ledgerCall(handler, http.MethodPost, testLedgerPath, payload), 201)
	path := testLedgerPath + "/" + created.ID
	if created.Status != "pending_dispatch" || created.Version != 1 {
		t.Fatalf("登记不应自动派单: %+v", created)
	}
	owner := "?organization=" + url.QueryEscape("试点单位")
	institution := "?institution=" + url.QueryEscape("测评中心")
	assertLedgerError(t, ledgerCall(handler, "GET", operatorBase+"/"+created.ID+owner, nil), 404, "not_found")
	assertLedgerError(t, ledgerCall(handler, "GET", institutionBase+"/"+created.ID+institution, nil), 404, "not_found")
	payload["title"] = "  fixture risk alpha  "
	payload["ledger"] = "vulnerability"
	assertLedgerError(t, ledgerCall(handler, "POST", testLedgerPath, payload), 409, "duplicate_registration")
	dispatched := ledgerReply[hazardMutation](t, ledgerCall(handler, "POST", path+"/actions",
		actionInput{Action: "dispatch", ExpectedVersion: created.Version}), 200)
	if dispatched.Status != "open" {
		t.Fatalf("派单状态错误: %+v", dispatched)
	}
	item := ledgerReply[hazardDetail](t, ledgerCall(handler, "GET", operatorBase+"/"+created.ID+owner, nil), 200)
	if item.FilingID != filing || item.PlanID != "" || item.ReportID != "" || item.Status != "open" {
		t.Fatalf("直接登记不需要关联规划及报告: %+v", item)
	}
	assertLedgerError(t, ledgerCall(handler, "POST", path+"/actions",
		actionInput{Action: "dispatch", ExpectedVersion: created.Version}), 409, "conflict")
}

// TestNewRegistrationConcurrentDuplicates 验证不同 HTTP 请求同时登记一项问题只落一条。
// 参数 t 为隔离数据库的测试上下文。
// 返回值 无。
func TestNewRegistrationConcurrentDuplicates(t *testing.T) {
	service, handler := ledgerFixture(t)
	_, _, filing := seedPlan(t, service.db)
	input := map[string]string{"ledger": "vulnerability", "filingId": filing,
		"title": "同一并发发现", "severity": "low", "source": "assessment",
		"discoveredOn": "2026-01-01", "deadline": "2099-01-01", "description": "合成测试",
		"measure": "复核配置", "responsibility": "技术部"}
	var calls sync.WaitGroup
	status := make(chan int, 4)
	for range cap(status) {
		calls.Go(func() { status <- ledgerCall(handler, "POST", testLedgerPath, input).Code })
	}
	calls.Wait()
	close(status)
	counts := map[int]int{}
	for code := range status {
		counts[code]++
	}
	if counts[201] != 1 || counts[409] != 3 {
		t.Fatalf("并发登记结果 %+v", counts)
	}
}

// TestDirectSupervisionAndRetest 验证无测评报告的登记事项也能督办、复测及监管销项。
// 参数 t 为真实隔离 PostgreSQL 和证据存储测试上下文。
// 返回值 无。
func TestDirectSupervisionAndRetest(t *testing.T) {
	service, handler := ledgerFixture(t)
	var filing string
	err := service.db.QueryRow(t.Context(), `INSERT INTO business_cases
		(kind,number,system_name,organization_name,assessment_org,level,status)
		VALUES('filing','DIRECT-ONLY','直接登记系统','试点单位','测评中心','3','approved')
		RETURNING id::text`).Scan(&filing)
	if err != nil {
		t.Fatal(err)
	}
	created := ledgerReply[hazardMutation](t, ledgerCall(handler, "POST", testLedgerPath,
		map[string]string{"ledger": "major_risk", "filingId": filing,
			"title": "复测与督办验收", "severity": "high", "source": "scan",
			"discoveredOn": "2026-01-01", "deadline": "2099-01-01", "description": "合成登记验证",
			"measure": "完善业务控制", "responsibility": "运营值班组"}), 201)
	admin := testLedgerPath + "/" + created.ID
	actions := admin + "/actions"
	dispatched := ledgerReply[hazardMutation](t, ledgerCall(handler, "POST", actions,
		actionInput{actionDispatch, created.Version, ""}), 200)
	supervised := ledgerReply[hazardMutation](t, ledgerCall(handler, "POST", actions,
		actionInput{actionSupervise, dispatched.Version, "请按期完成整改"}), 200)
	assertLedgerError(t, ledgerCall(handler, "POST", actions,
		actionInput{actionSupervise, supervised.Version, "同日重复提交"}), 409, "already_supervised")
	assertDirectSupervisionNotice(t, service, created.ID)
	owner := "?organization=" + url.QueryEscape("试点单位")
	operator := operatorBase + "/" + created.ID
	ledgerReply[cases.Attachment](t, ledgerUpload(t, handler, operator+owner,
		kindEvidence, fixturePDF, supervised.Version), 201)
	submitted := ledgerReply[hazardMutation](t, ledgerCall(handler, "POST", operator+"/actions"+owner,
		actionInput{actionSubmit, supervised.Version + 1, "已经整改，申请复测"}), 200)
	assertLedgerError(t, ledgerCall(handler, "POST", actions,
		actionInput{actionClose, submitted.Version, ""}), 409, "conflict")
	passed := passInstitutionRetest(t, handler, created.ID, submitted.Version)
	closed := ledgerReply[hazardMutation](t, ledgerCall(handler, "POST", actions,
		actionInput{actionClose, passed.Version, ""}), 200)
	if closed.Status != statusClosed {
		t.Fatalf("直接登记事项未销项: %+v", closed)
	}
}

// assertDirectSupervisionNotice 检查督办重复失败时消息和办理事件均只保留一条。
// 参数 t 为隔离测试上下文，s 为台账服务，id 为已登记重大风险。
// 返回值 无。
func assertDirectSupervisionNotice(t *testing.T, s *Service, id string) {
	t.Helper()
	var notices, events int
	err := s.db.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM review_notices WHERE resource_id=$1 AND state='hazard_supervision'
		 AND audience='operator' AND recipient='试点单位'),
		(SELECT count(*) FROM hazard_events WHERE hazard_id=$1 AND action='supervise')`, id).
		Scan(&notices, &events)
	if err != nil || notices != 1 || events != 1 {
		t.Fatalf("督办出现重复或收件方错误: 消息 %d，留痕 %d，错误 %v", notices, events, err)
	}
}
