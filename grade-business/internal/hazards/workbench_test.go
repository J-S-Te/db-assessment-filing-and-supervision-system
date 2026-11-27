package hazards

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWorkbenchRouteRejectsUnusedParameters 验证工作台仅接受声明的输入。
// 参数 t 记录接口响应和测试结果。
// 返回值 无。
func TestWorkbenchRouteRejectsUnusedParameters(t *testing.T) {
	mux := http.NewServeMux()
	new(Service).RegisterWorkbench(mux)
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, httptest.NewRequest("GET", "/api/v1/hazards/workbench?unused=1", nil))
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &problem); err != nil ||
		out.Code != http.StatusBadRequest || problem.Code != "invalid_input" {
		t.Fatalf("got %d and %s (%v)", out.Code, out.Body.String(), err)
	}
}

// TestWorkbenchDeadlineBoundary 验证十日窗口以及待复核和待审规划的独立口径。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestWorkbenchDeadlineBoundary(t *testing.T) {
	s, routes := ledgerFixture(t)
	plan, report, _ := seedPlan(t, s.db)
	ledgerReply[map[string]int](t, ledgerCall(routes, "POST", testLedgerPath+"/sync/"+plan, nil), 200)
	const deadlineUpdate = `
		UPDATE hazard_items SET
		  deadline=(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date
		        + CASE WHEN risk_index=1 THEN 10 ELSE 11 END,
		  status=CASE WHEN risk_index=1 THEN 'open' ELSE 'pending_review' END
		WHERE plan_id=$1`
	if _, err := s.db.Exec(t.Context(), deadlineUpdate, plan); err != nil {
		t.Fatal(err)
	}
	_, err := s.db.Exec(t.Context(), `INSERT INTO remediation_plans(report_id,deadline,risks,status,submission,submitted_at)
		SELECT $1,deadline,risks,'pending_review',1,clock_timestamp()
		FROM remediation_plans WHERE id=$2`, report, plan)
	if err != nil {
		t.Fatal(err)
	}
	work := http.NewServeMux()
	s.RegisterWorkbench(work)
	got := ledgerReply[WorkbenchSummary](t, ledgerCall(work, "GET", testLedgerPath+"/workbench", nil), 200)
	if got.PendingPlanReview != 1 || got.PendingReview != 1 || got.DueWithin10Days != 1 ||
		got.Overdue != 0 || len(got.PendingPlans) != 1 || len(got.Items) != 2 ||
		got.Items[0].Status != statusPending {
		t.Fatalf("unexpected workbench classification: %+v", got)
	}
}
