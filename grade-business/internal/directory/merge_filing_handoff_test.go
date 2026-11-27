package directory

import (
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestComplexMergeTransfersOperatorFilingReview 验证首报归并保留当前阶段、原机构及历史范围。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestComplexMergeTransfersOperatorFilingReview(t *testing.T) {
	pool := directoryTestPool(t)
	mux := http.NewServeMux()
	Register(mux, pool)
	old := mergeTestUnit(t, mux, "在审首报原单位", "913300001000001081")
	next := mergeTestUnit(t, mux, "在审首报承接单位", "913300001000001082")
	states := []string{"pending_initial", "pending_final", "returned"}
	filings := make([]string, 0, len(states))
	for _, status := range states {
		filings = append(filings, seedOperatorFilingReview(t, pool, old, status))
	}
	body := map[string]any{"targetId": next.ID, "sourceIds": []string{old.ID}}
	simple := testJSON[mergePlan](t, testRequest(t, mux, "POST", mergePath+"/preview", body, 200))
	if simple.Token != "" || len(simple.Blockers) != 1 || simple.Blockers[0].Code != "filing_open" {
		t.Fatalf("简单归并未限制在审首报：%+v", simple)
	}
	body["mode"] = complexMerge
	plan := testJSON[mergePlan](t, testRequest(t, mux, "POST", mergePath+"/preview", body, 200))
	if plan.Token == "" || len(plan.Moves) != len(states) || len(plan.Impacts) != len(states) {
		t.Fatalf("复杂归并未识别三种可办首报阶段：%+v", plan)
	}
	body["token"] = plan.Token
	testRequest(t, mux, "POST", mergePath, body, 200)
	for i, id := range filings {
		verifyFilingReviewHandoff(t, pool, id, states[i], old, next)
	}
}

// TestComplexMergeBlocksPendingFilingWithdrawal 验证归并不使已进入确认队列的撤回申请失效。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestComplexMergeBlocksPendingFilingWithdrawal(t *testing.T) {
	pool := directoryTestPool(t)
	mux := http.NewServeMux()
	Register(mux, pool)
	old := mergeTestUnit(t, mux, "首报撤回原单位", "913300001000001083")
	next := mergeTestUnit(t, mux, "首报撤回预接续单位", "913300001000001084")
	filing := seedOperatorFilingReview(t, pool, old, "pending_initial")
	if _, err := pool.Exec(t.Context(), `WITH updated AS (
		UPDATE business_cases SET version=2 WHERE id=$1 RETURNING id
	)
		INSERT INTO filing_withdrawal_requests(resource,filing_id,requested_from_version,case_version,reason)
		SELECT 'filings',id,1,2,'申请撤回首报' FROM updated`, filing); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"targetId": next.ID, "sourceIds": []string{old.ID}, "mode": complexMerge}
	plan := testJSON[mergePlan](t, testRequest(t, mux, "POST", mergePath+"/preview", body, 200))
	if plan.Token != "" || len(plan.Blockers) != 1 || plan.Blockers[0].Code != "filing_withdrawal_open" {
		t.Fatalf("待确认撤回未阻止接续：%+v", plan)
	}
}

// seedOperatorFilingReview 构造带原始材料和流转快照的运营者首报记录。
// 参数 t、pool 为测试上下文与隔离库，unit、status 为原单位和在办阶段。
// 返回值 隔离备案的标识。
func seedOperatorFilingReview(t *testing.T, pool *pgxpool.Pool, unit testEntry, status string) string {
	t.Helper()
	id := seedMergeFiling(t, pool, unit.Name, "FLOW-"+status, "首报系统 "+status, status, false)
	if _, err := pool.Exec(t.Context(), `UPDATE business_cases SET operator_origin=true WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	addChainAttachment(t, pool, id, unit.Name)
	if _, err := pool.Exec(t.Context(), `INSERT INTO case_review_events
		(case_id,action,from_status,to_status,operator,version) VALUES($1,'submit','draft',$2,$3,1)`,
		id, status, unit.Name); err != nil {
		t.Fatal(err)
	}
	return id
}

// verifyFilingReviewHandoff 确认承办者、办理阶段、审核机构和旧回执资料均匹配归并快照。
// 参数 t、pool 为测试上下文与隔离库，id、status、old、next 为记录、在审状态及交接单位。
// 返回值 无。
func verifyFilingReviewHandoff(t *testing.T, pool *pgxpool.Pool, id, status string, old, next testEntry) {
	t.Helper()
	var matched bool
	err := pool.QueryRow(t.Context(), `SELECT f.organization_name=$2 AND f.directory_unit_id=$5
		AND f.status=$4 AND f.version=2 AND f.assessment_org='测试机构'
		AND f.directory_snapshot#>>'{unit,name}'=$3 AND a.merge_history_scope=$3 AND e.merge_history_scope=$3
		AND EXISTS (SELECT 1 FROM review_notices n WHERE n.resource='filings' AND n.resource_id=f.id
		AND n.recipient=$2 AND n.state='merge_handoff' AND n.event_version=f.version)
		FROM business_cases f JOIN case_attachments a ON a.case_id=f.id
		JOIN case_review_events e ON e.case_id=f.id WHERE f.id=$1`, id, next.Name, old.Name, status, next.ID).Scan(&matched)
	if err != nil || !matched {
		t.Fatalf("首报归属、历史或送达未正确接续：%v，错误 %v", matched, err)
	}
}
