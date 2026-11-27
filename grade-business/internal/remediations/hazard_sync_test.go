package remediations

import (
	"net/http"
	"testing"
)

// TestApprovalRollsBackWhenLedgerSyncFails 验证台账写入失败时整改审核、留痕和反馈一起回滚。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestApprovalRollsBackWhenLedgerSyncFails(t *testing.T) {
	db, router := remediationFixture(t)
	report, _ := insertReports(t, db)
	input := newPlanInput(report)
	created := response[mutation](t, call(router, "POST", rootURL, input), http.StatusCreated)
	path := rootURL + "/" + created.ID
	checkAttachments(t, router, path, input)
	submission := response[mutation](t, call(router, "POST", path+"/submit", submitInput{4}), http.StatusOK)
	approval := reviewInput{
		Decision: "approve", Deadline: input.Deadline, Comment: "核对整改规划",
		ExpectedVersion: submission.Version,
	}

	_, err := db.Exec(t.Context(), `ALTER TABLE hazard_items ADD CONSTRAINT test_forbid_ledger CHECK (false)`)
	if err != nil {
		t.Fatal(err)
	}
	response[map[string]string](t, call(router, "POST", path+"/review-actions", approval), http.StatusInternalServerError)
	detail := response[mutation](t, call(router, "GET", path, nil), http.StatusOK)
	history := response[collection[reviewEvent]](t, call(router, "GET", path+"/review-history", nil), http.StatusOK)
	var notices int
	err = db.QueryRow(t.Context(), `SELECT count(*) FROM review_notices WHERE resource_id=$1`, created.ID).
		Scan(&notices)
	if err != nil || notices != 0 || detail.Status != pendingReview ||
		detail.Version != submission.Version || len(history.Items) != 1 {
		t.Fatalf("failed ledger sync changed plan: %+v, %+v, notices=%d, err=%v", detail, history, notices, err)
	}
	_, err = db.Exec(t.Context(), `ALTER TABLE hazard_items DROP CONSTRAINT test_forbid_ledger`)
	if err != nil {
		t.Fatal(err)
	}
	result := response[mutation](t, call(router, "POST", path+"/review-actions", approval), http.StatusOK)
	var count int
	err = db.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM hazard_items WHERE plan_id=$1),
		(SELECT count(*) FROM review_notices WHERE audience='operator' AND resource='remediations'
		 AND resource_id=$1 AND state='approved' AND recipient='测试单位' AND event_version=$2)`,
		created.ID, result.Version).Scan(&count, &notices)
	if err != nil || result.Status != approved || count != len(input.Risks) || notices != 1 {
		t.Fatalf("retry failed complete approval: %+v, items=%d, notices=%d, err=%v", result, count, notices, err)
	}
}
