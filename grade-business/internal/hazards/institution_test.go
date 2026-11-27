package hazards

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"grade-business/internal/cases"
)

// TestInstitutionRetestRequiresCurrentRoundReport 验证机构复测退回重提、重新提交的证据及后续监管复核。
// 参数 t 为隔离真实业务测试上下文。
// 返回值 无。
func TestInstitutionRetestRequiresCurrentRoundReport(t *testing.T) {
	service, router := ledgerFixture(t)
	admin, _, _ := syncedPaths(t, service, router)
	id := strings.TrimPrefix(admin, testLedgerPath+"/")
	if _, err := service.db.Exec(t.Context(),
		`UPDATE business_cases SET assessment_org='当前备案机构' WHERE kind='filing'`); err != nil {
		t.Fatal(err)
	}
	operator := operatorBase + "/" + id
	owner := "?organization=" + url.QueryEscape("试点单位")
	institution := "/api/v1/institution/hazards/" + id
	scope := "?institution=" + url.QueryEscape("测评中心")
	ledgerReply[cases.Attachment](t, ledgerUpload(t, router, operator+owner, kindEvidence, fixturePDF, 1), 201)
	submitted := ledgerReply[hazardMutation](t, ledgerCall(router, http.MethodPost, operator+"/actions"+owner,
		actionInput{actionSubmit, 2, "已完成首轮整改"}), 200)
	if submitted.Status != "pending_retest" {
		t.Fatalf("submission skipped institution retest: %+v", submitted)
	}
	assertLedgerError(t, ledgerCall(router, http.MethodPost, admin+"/actions", actionInput{actionClose, submitted.Version, ""}), 409, "conflict")
	wrong := "?institution=" + url.QueryEscape("当前备案机构")
	assertLedgerError(t, ledgerCall(router, http.MethodGet, institution+wrong, nil), 404, "not_found")
	assertLedgerError(t, ledgerCall(router, http.MethodPost, institution+"/actions"+wrong,
		actionInput{actionRetestReturn, submitted.Version, "非关联机构尝试决定"}), 404, "not_found")
	list := ledgerReply[hazardPage](t, ledgerCall(router, http.MethodGet, institutionBase+scope+"&status=pending_retest", nil), 200)
	if len(list.Items) != 1 || list.Items[0].ID != id || list.Items[0].AssessmentOrg != "测评中心" {
		t.Fatalf("waiting retest missing from approved report institution: %+v", list)
	}
	first := ledgerReply[hazardDetail](t, ledgerCall(router, http.MethodGet, institution+scope, nil), 200)
	if !slices.Equal(first.AllowedActions, []string{"retest_pass", "retest_return"}) {
		t.Fatalf("unexpected institution actions: %+v", first)
	}
	assertLedgerError(t, ledgerCall(router, http.MethodPost, institution+"/actions"+scope,
		actionInput{"retest_pass", submitted.Version, ""}), 400, "retest_report_required")
	assertLedgerError(t, ledgerUpload(t, router, institution+scope, kindEvidence, fixturePDF, submitted.Version), 400, "invalid_input")
	assertLedgerError(t, ledgerCall(router, http.MethodPost, institution+"/actions"+scope,
		actionInput{"retest_return", submitted.Version, ""}), 400, "invalid_input")
	pass := finishInstitutionRetestRound(t, router, operator, owner, institution, scope, submitted.Version)
	ledgerReply[hazardMutation](t, ledgerCall(router, http.MethodPost, admin+"/actions",
		actionInput{actionClose, pass.Version, "监管销号"}), 200)
	assertInstitutionsReceiveRetestUpdates(t, service, id)
}

// TestRetestNoticeFailureRollsBackSubmission 验证机构送达失败时提交和留痕一并回滚。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestRetestNoticeFailureRollsBackSubmission(t *testing.T) {
	service, router := ledgerFixture(t)
	path, _, _ := syncedPaths(t, service, router)
	ledgerReply[cases.Attachment](t, ledgerUpload(t, router, path, kindEvidence, fixturePDF), 201)
	if _, err := service.db.Exec(t.Context(), `ALTER TABLE review_notices ADD CONSTRAINT test_retest_notice
		CHECK (audience<>'institution' OR state<>'pending_retest')`); err != nil {
		t.Fatal(err)
	}
	input := actionInput{actionSubmit, 2, "提交首轮复测"}
	assertLedgerError(t, ledgerCall(router, http.MethodPost, path+"/actions", input), 500, "internal_error")
	current := ledgerReply[hazardDetail](t, ledgerCall(router, http.MethodGet, path, nil), 200)
	history := ledgerReply[hazardCollection[hazardEvent]](t, ledgerCall(router, http.MethodGet, path+"/history", nil), 200)
	if current.Status != statusOpen || current.Version != 2 || len(history.Items) != 0 {
		t.Fatalf("failed retest notice left a partially accepted submission: %+v %+v", current, history)
	}
	if _, err := service.db.Exec(t.Context(), `ALTER TABLE review_notices DROP CONSTRAINT test_retest_notice`); err != nil {
		t.Fatal(err)
	}
	confirmed := ledgerReply[hazardMutation](t, ledgerCall(router, http.MethodPost, path+"/actions", input), 200)
	if confirmed.Status != statusRetest || confirmed.Version != 3 {
		t.Fatalf("retest request did not recover: %+v", confirmed)
	}
}

// passInstitutionRetest 通过真实机构路由以送测版本上传本轮报告并确认通过。
// 参数 t、router 为隔离验证上下文，id 为隐患标识，version 为送复测版本。
// 返回值 待监管复核的隐患状态与版本。
func passInstitutionRetest(t *testing.T, router http.Handler, id string, version int64) hazardMutation {
	t.Helper()
	path := institutionBase + "/" + id
	scope := "?institution=" + url.QueryEscape("测评中心")
	ledgerReply[cases.Attachment](t, ledgerUpload(t, router, path+scope, kindRetestReport, fixturePDF, version), 201)
	ready := ledgerReply[hazardDetail](t, ledgerCall(router, http.MethodGet, path+scope, nil), 200)
	if ready.RetestReportReady == nil || !*ready.RetestReportReady {
		t.Fatalf("new retest report not reflected in detail: %+v", ready)
	}
	return ledgerReply[hazardMutation](t, ledgerCall(router, http.MethodPost, path+"/actions"+scope,
		actionInput{actionRetestPass, version + 1, ""}), 200)
}

// finishInstitutionRetestRound 验证旧报告和旧证据不能跨轮，每次上传都使用该轮版本。
// 参数 t、router 为隔离验证上下文，operator、owner、institution、scope、version 为当前阶段和两端范围。
// 返回值 最新轮通过机构复测的结果。
func finishInstitutionRetestRound(t *testing.T, router http.Handler, operator, owner, institution, scope string, version int64) hazardMutation {
	t.Helper()
	old := ledgerReply[cases.Attachment](t, ledgerUpload(t, router, institution+scope, "retest_report", fixturePDF, version), 201)
	assertLedgerError(t, ledgerCall(router, http.MethodGet, institution+"/attachments/"+old.ID+"?institution=其他机构", nil), 404, "not_found")
	returned := ledgerReply[hazardMutation](t, ledgerCall(router, http.MethodPost, institution+"/actions"+scope,
		actionInput{"retest_return", version + 1, "仍需补充整改证据"}), 200)
	assertLedgerError(t, ledgerCall(router, http.MethodPost, operator+"/actions"+owner,
		actionInput{actionSubmit, returned.Version, "仅使用旧证据"}), 400, "evidence_required")
	ledgerReply[cases.Attachment](t, ledgerUpload(t, router, operator+owner, kindEvidence, fixturePDF, returned.Version), 201)
	resubmitted := ledgerReply[hazardMutation](t, ledgerCall(router, http.MethodPost, operator+"/actions"+owner,
		actionInput{actionSubmit, returned.Version + 1, "补交了本轮证据"}), 200)
	assertLedgerError(t, ledgerCall(router, http.MethodPost, institution+"/actions"+scope,
		actionInput{"retest_pass", resubmitted.Version, "仅使用旧报告"}), 400, "retest_report_required")
	ledgerReply[cases.Attachment](t, ledgerUpload(t, router, institution+scope, "retest_report", fixturePDF, resubmitted.Version), 201)
	pass := ledgerReply[hazardMutation](t, ledgerCall(router, http.MethodPost, institution+"/actions"+scope,
		actionInput{"retest_pass", resubmitted.Version + 1, "复测通过"}), 200)
	if pass.Status != statusPending {
		t.Fatalf("retest must proceed to regulator review: %+v", pass)
	}
	return pass
}

// assertInstitutionsReceiveRetestUpdates 核对复测与监管各阶段真实站内通知。
// 参数 t、service 和 id 为隔离测试上下文、服务与隐患标识。
// 返回值 无。
func assertInstitutionsReceiveRetestUpdates(t *testing.T, service *Service, id string) {
	t.Helper()
	var institutions, operators []string
	err := service.db.QueryRow(t.Context(), `SELECT
		array_agg(state ORDER BY event_version) FILTER(WHERE audience='institution' AND recipient='测评中心'),
		array_agg(state ORDER BY event_version) FILTER(WHERE audience='operator' AND recipient='试点单位')
		FROM review_notices WHERE resource='hazards' AND resource_id=$1`, id).Scan(&institutions, &operators)
	if err != nil || !slices.Equal(institutions, []string{"pending_retest", "pending_retest", "closed"}) ||
		!slices.Equal(operators, []string{"open", "returned", "pending_review", "closed"}) {
		t.Fatalf("retet delivery mismatch: institutions=%v operators=%v error=%v", institutions, operators, err)
	}
}
