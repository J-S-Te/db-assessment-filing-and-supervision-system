package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestWorkflowCalendarAndHandoff 验证工作日覆盖以及备案待办只有当前席位可办。
// 参数 t 为使用独立 PostgreSQL schema 的测试对象。
// 返回值 无。
func TestWorkflowCalendarAndHandoff(t *testing.T) {
	ctx := context.Background()
	db := openWorkbenchTestDB(t, ctx)
	_, err := db.Exec(ctx, `
		INSERT INTO workflow_calendar_days (day,is_workday)
		VALUES ('2026-11-28',true), ('2026-11-30',false);
		INSERT INTO business_cases (kind,number,system_name,organization_name,assessment_org,
		    level,status,operator_origin)
		VALUES ('filing','HANDOFF','待办系统','等待单位','当前机构','2','pending_initial',true);
		INSERT INTO case_review_events (case_id,action,from_status,to_status,operator,version,occurred_at)
		SELECT id,'submit','draft','pending_initial','等待单位',2,'2026-11-27 11:10:00+08'
		FROM business_cases WHERE number='HANDOFF';
	`)
	if err != nil {
		t.Fatal(err)
	}
	var due string
	err = db.QueryRow(ctx, `SELECT workflow_due('2026-11-27',2)::text`).Scan(&due)
	if err != nil || due != "2026-12-01" {
		t.Fatalf("补班及休息日下截止日 = %q, err = %v", due, err)
	}
	var audience, recipient, start string
	err = db.QueryRow(ctx, `SELECT audience,recipient,(stage_at AT TIME ZONE 'Asia/Shanghai')::date::text
		FROM workflow_tasks WHERE number='HANDOFF'`).Scan(&audience, &recipient, &start)
	if err != nil || audience != "institution" || recipient != "当前机构" || start != "2026-11-27" {
		t.Fatalf("初审待办 = %q %q %q, err = %v", audience, recipient, start, err)
	}
	_, err = db.Exec(ctx, `
		UPDATE business_cases SET status='pending_final',version=3 WHERE number='HANDOFF';
		INSERT INTO case_review_events (case_id,action,from_status,to_status,operator,version,occurred_at)
		SELECT id,'approve','pending_initial','pending_final','当前机构',3,'2026-12-01 11:00:00+08'
		FROM business_cases WHERE number='HANDOFF';
	`)
	if err != nil {
		t.Fatal(err)
	}
	err = db.QueryRow(ctx, `SELECT audience,recipient,(stage_at AT TIME ZONE 'Asia/Shanghai')::date::text
		FROM workflow_tasks WHERE number='HANDOFF'`).Scan(&audience, &recipient, &start)
	if err != nil || audience != "regulator" || recipient != "" || start != "2026-12-01" {
		t.Fatalf("终审待办 = %q %q %q, err = %v", audience, recipient, start, err)
	}
}

// TestRegisteredHazardWorkbenchHandoff 验证直接登记隐患派发前后的真实办理范围和时限。
// 参数 t 为使用独立 PostgreSQL schema 的测试对象。
// 返回值 无。
func TestRegisteredHazardWorkbenchHandoff(t *testing.T) {
	ctx := context.Background()
	db := openWorkbenchTestDB(t, ctx)
	var id string
	err := db.QueryRow(ctx, `WITH filing AS (
		INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status)
		VALUES ('filing','F-DIRECT','直接登记系统','运营单位','备案中的机构','2','approved') RETURNING id
	)
	INSERT INTO hazard_items(registration_filing_id,ledger,title,source,discovered_on,severity,
	  description,measure,responsibility,deadline,status)
	SELECT id,'major_risk','直接登记','manual',timezone('Asia/Shanghai',CURRENT_TIMESTAMP)::date-3,
	  'high','需整改风险','整改措施','运营单位',timezone('Asia/Shanghai',CURRENT_TIMESTAMP)::date,
	  'pending_dispatch' FROM filing RETURNING id::text`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	regulator, err := readOverview(ctx, db, ReviewWindows{})
	if err != nil || len(regulator.Tasks) != 1 || regulator.Tasks[0].ID != id ||
		regulator.Tasks[0].Deadline != "" || regulator.Tasks[0].Status != "pending_dispatch" ||
		regulator.HazardDueWithin30Days != 0 {
		t.Fatalf("待派发应仅进入监管队列且无截止日期: %+v, %v", regulator.Tasks, err)
	}
	assertRegisteredHazardWorkbench(t, db, queueScope{"operator", "运营单位"}, "", "", 0)
	if _, err := db.Exec(ctx, `UPDATE hazard_items
		SET status='open',version=2,dispatched_at=clock_timestamp(),retest_institution='派发选定机构'
		WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	assertRegisteredHazardWorkbench(t, db, queueScope{"operator", "运营单位"}, id, "open", 1)
	if _, err := db.Exec(ctx, `UPDATE hazard_items SET status='pending_retest',version=3 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	assertRegisteredHazardWorkbench(t, db, queueScope{"institution", "派发选定机构"}, id, "pending_retest", 1)
	assertRegisteredHazardWorkbench(t, db, queueScope{"institution", "备案中的机构"}, "", "", 0)
	operator, err := readScopedWorkbench(ctx, db, ReviewWindows{}, queueScope{"operator", "运营单位"})
	if err != nil || len(operator.Tasks) != 1 || operator.Tasks[0].ID != id ||
		!operator.Tasks[0].CanRemind || operator.Counts.Pending != 0 {
		t.Fatalf("运营单位待复测事项应可催办且不计入待办: %+v, %v", operator, err)
	}
}

// assertRegisteredHazardWorkbench 核对一个席位可见的登记隐患待办和整改计数。
// 参数 t、db 为测试及隔离数据库，scope 为待核对席位，id、status、count 为预期结果。
// 返回值 无。
func assertRegisteredHazardWorkbench(t *testing.T, db *pgxpool.Pool, scope queueScope,
	id, status string, count int64,
) {
	t.Helper()
	workbench, err := readScopedWorkbench(t.Context(), db, ReviewWindows{}, scope)
	if err != nil || int64(len(workbench.Tasks)) != count || workbench.Counts.Pending != count ||
		workbench.Rectification.Open+workbench.Rectification.PendingRetest != count {
		t.Fatalf("%+v 的隐患待办和汇总 = %+v, err = %v", scope, workbench, err)
	}
	if count > 0 && (workbench.Tasks[0].ID != id || workbench.Tasks[0].Status != status ||
		workbench.Tasks[0].Urgency != "near") {
		t.Fatalf("%+v 的隐患待办 = %+v", scope, workbench.Tasks[0])
	}
}

// TestScopedWorkbenchShowsOnlyAssignedActions 验证本机构任务与备案归属不会串到其他席位。
// 参数 t 为具备隔离业务库的测试对象。
// 返回值 无。
func TestScopedWorkbenchShowsOnlyAssignedActions(t *testing.T) {
	ctx := context.Background()
	db := openWorkbenchTestDB(t, ctx)
	_, err := db.Exec(ctx, `INSERT INTO business_cases
		(kind,number,system_name,organization_name,assessment_org,level,status,operator_origin)
		VALUES ('filing','F-IN','系统一','运营一','机构一','2','pending_initial',true),
		       ('filing','F-OUT','系统二','运营二','机构二','2','pending_initial',true),
		       ('filing','F-RETURN','系统三','运营一','机构一','2','returned',true)`)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	RegisterWorkbenchOverview(mux, db, ReviewWindows{FilingDays: 3})
	fixtures := []struct{ route, query, number string }{
		{"institution", "institution=" + url.QueryEscape("机构一"), "F-IN"},
		{"operator", "organization=" + url.QueryEscape("运营一"), "F-RETURN"},
	}
	for _, fixture := range fixtures {
		base := "/api/v1/" + fixture.route + "/workbench?" + fixture.query
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, base, nil))
		var response struct {
			Tasks  []struct{ Number string }
			Counts struct{ Pending int }
		}
		err := json.Unmarshal(recorder.Body.Bytes(), &response)
		if recorder.Code != http.StatusOK || err != nil || response.Counts.Pending != 1 ||
			len(response.Tasks) != 1 || response.Tasks[0].Number != fixture.number {
			t.Fatalf("%s 待办状态 %d，响应 %s", fixture.route, recorder.Code, recorder.Body)
		}
		recorder = httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, base+"&"+fixture.query, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s 重复查询字段状态 %d", fixture.route, recorder.Code)
		}
	}
}
