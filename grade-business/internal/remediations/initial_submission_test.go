package remediations

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/cases"
)

const operatorCollection = "/api/v1/operator/remediations"

// TestOperatorInitialSubmissionUsesNewestScopedReport 贯通本单位候选报告、草稿、可选证据、材料及首次上报。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestOperatorInitialSubmissionUsesNewestScopedReport(t *testing.T) {
	db, router := remediationFixture(t)
	old, _ := insertReports(t, db)
	owner := "?organization=" + url.QueryEscape("测试单位")
	other := "?organization=" + url.QueryEscape("测试单位分部")
	obsolete := createReadyPlan(t, router, operatorCollection, owner, old)
	latest := insertLaterReport(t, db, old)
	stale := response[map[string]string](t, call(router, "POST", obsolete+"/submit"+owner, submitInput{4}), 409)
	if stale["code"] != "report_not_latest" {
		t.Fatalf("outdated draft was not rechecked at submission: %v", stale)
	}
	actual := response[collection[eligibleReport]](t, call(router, "GET", operatorCollection+"/eligible-reports"+owner, nil), 200)
	if len(actual.Items) != 1 || actual.Items[0].ID != latest {
		t.Fatalf("ineligible report offered: %+v", actual)
	}
	response[map[string]string](t, call(router, "POST", operatorCollection+other, newPlanInput(latest)), 400)
	stale = response[map[string]string](t, call(router, "POST", operatorCollection+owner, newPlanInput(old)), 409)
	if stale["code"] != "report_not_latest" {
		t.Fatalf("unexpected stale selection result: %v", stale)
	}
	created := response[mutation](t, call(router, "POST", operatorCollection+owner, newPlanInput(latest)), 201)
	path := operatorCollection + "/" + created.ID
	responded := response[detail](t, call(router, "GET", path+owner, nil), 200)
	if !slices.Contains(responded.AllowedActions, "edit") || !slices.Contains(responded.AllowedActions, "submit") {
		t.Fatalf("draft actions missing: %v", responded.AllowedActions)
	}
	for _, kind := range []string{"plan", "responsibility", "high_risk_evidence"} {
		response[cases.Attachment](t, upload(router, path+owner, kind, samplePDF, latest), 201)
	}
	response[map[string]string](t, call(router, "POST", path+"/submit"+owner, submitInput{4}), 400)
	response[cases.Attachment](t, upload(router, path+owner, "schedule", samplePDF, latest), 201)
	submitted := response[mutation](t, call(router, "POST", path+"/submit"+owner, submitInput{5}), 200)
	if submitted.Status != pendingReview {
		t.Fatalf("draft was not submitted: %+v", submitted)
	}
	listed := response[page](t, call(router, "GET", operatorCollection+owner+"&status=pending_review", nil), 200)
	otherList := response[page](t, call(router, "GET", operatorCollection+other, nil), 200)
	if len(listed.Items) != 1 || listed.Items[0].ID != created.ID || len(otherList.Items) != 0 {
		t.Fatalf("incorrect scoped plans: %+v %+v", listed, otherList)
	}
}

// TestFilingAllowsOnePendingPlanAcrossRoutes 验证不同报告及端并发竞争同一备案，撤回后释放审核名额。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestFilingAllowsOnePendingPlanAcrossRoutes(t *testing.T) {
	db, router := remediationFixture(t)
	old, _ := insertReports(t, db)
	latest := insertLaterReport(t, db, old)
	owner := "?organization=" + url.QueryEscape("测试单位")
	regulator := createReadyPlan(t, router, rootURL, "", old)
	operator := createReadyPlan(t, router, operatorCollection, owner, latest)
	var workers sync.WaitGroup
	var codes [2]int
	for i, target := range []string{regulator + "/submit", operator + "/submit" + owner} {
		workers.Go(func() { codes[i] = call(router, "POST", target, submitInput{4}).Code })
	}
	workers.Wait()
	if min(codes[0], codes[1]) != 200 || max(codes[0], codes[1]) != 409 {
		t.Fatalf("concurrent submissions were not limited by filing: %v", codes)
	}
	winning := operator
	losing := regulator + "/submit"
	if codes[0] == 200 {
		winning = operatorCollection + strings.TrimPrefix(regulator, rootURL)
		losing = operator + "/submit" + owner
	}
	response[map[string]string](t, call(router, "POST", winning+"/withdraw?organization=其他单位", submitInput{5}), 404)
	closed := response[mutation](t, call(router, "POST", winning+"/withdraw"+owner, submitInput{5}), 200)
	if closed.Status != "withdrawn" {
		t.Fatalf("withdrawal is not terminal: %+v", closed)
	}
	response[map[string]string](t, call(router, "POST", winning+"/submit"+owner, submitInput{closed.Version}), 409)
	events := response[collection[reviewEvent]](t, call(router, "GET", winning+"/review-history"+owner, nil), 200)
	if len(events.Items) != 2 || events.Items[0].Action != "withdraw" || events.Items[0].Operator != "测试单位" {
		t.Fatalf("withdrawal history is incomplete: %+v", events)
	}
	response[mutation](t, call(router, "POST", losing, submitInput{4}), 200)
}

// TestExistingPendingDuplicatesRemainAfterMigration 验证迁移保留旧重复记录并阻止更多提交直到全部退出待审。
// 参数 t 为实际 PostgreSQL 隔离验证上下文。
// 返回值 无。
func TestExistingPendingDuplicatesRemainAfterMigration(t *testing.T) {
	db := isolatedDB(t, false)
	router := remediationRouter(t, db, t.TempDir())
	firstReport, _ := insertReports(t, db)
	secondReport := insertLaterReport(t, db, firstReport)
	older := seedFollowupPlan(t, db, router, firstReport, pendingReview, 5)
	newer := seedFollowupPlan(t, db, router, secondReport, pendingReview, 5)
	ready := createReadyPlan(t, router, rootURL, "", secondReport)
	applyRemediationMigration(t, db, "021_remediation_in_review")
	for _, id := range []string{older, newer} {
		before := response[detail](t, call(router, "GET", rootURL+"/"+id, nil), 200)
		if before.Status != pendingReview || before.Submission != 1 || before.Version != 1 {
			t.Fatalf("migration changed historical pending record: %+v", before)
		}
	}
	blocked := response[map[string]string](t, call(router, "POST", ready+"/submit", submitInput{4}), 409)
	if blocked["code"] != "filing_in_review" {
		t.Fatalf("unexpected filing barrier: %v", blocked)
	}
	response[mutation](t, call(router, "POST", rootURL+"/"+older+"/review-actions", reviewInput{
		Decision: "return_for_correction", Deadline: businessDate(5), ExpectedVersion: 1,
		Comment: "旧记录需补充事项", Reason: "补充责任分工",
	}), 200)
	response[map[string]string](t, call(router, "POST", ready+"/submit", submitInput{4}), 409)
	response[mutation](t, call(router, "POST", rootURL+"/"+newer+"/review-actions", reviewInput{
		Decision: "approve", Deadline: businessDate(5), ExpectedVersion: 1, Comment: "原规划核对通过",
	}), 200)
	response[mutation](t, call(router, "POST", ready+"/submit", submitInput{4}), 200)
}

// insertLaterReport 为已有备案加入时间靠后的另一份通过报告。
// 参数 t 为测试上下文，db 为隔离库，old 为原报告标识。
// 返回值 新报告标识。
func insertLaterReport(t *testing.T, db *pgxpool.Pool, old string) string {
	t.Helper()
	var latest string
	err := db.QueryRow(t.Context(), `INSERT INTO business_cases(kind,number,assessment_org,filing_id,risk,status,submitted_at)
		SELECT kind,'BG-NEW',assessment_org,filing_id,risk,'approved',clock_timestamp()
		FROM business_cases WHERE id=$1 RETURNING id::text`, old).Scan(&latest)
	if err != nil {
		t.Fatal(err)
	}
	return latest
}

// createReadyPlan 经真实业务接口保存草稿并上传三类必传材料。
// 参数 t 为测试上下文，router 为业务路由，base、scope 和 report 为端、归属及报告标识。
// 返回值 不带查询参数的详情路径。
func createReadyPlan(t *testing.T, router http.Handler, base, scope, report string) string {
	t.Helper()
	created := response[mutation](t, call(router, "POST", base+scope, newPlanInput(report)), 201)
	path := base + "/" + created.ID
	for _, kind := range []string{"plan", "responsibility", "schedule"} {
		response[cases.Attachment](t, upload(router, path+scope, kind, samplePDF, report), 201)
	}
	return path
}
