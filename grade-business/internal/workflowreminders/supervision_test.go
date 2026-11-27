package workflowreminders

import (
	"net/http"
	"testing"

	"grade-business/internal/reviewinbox"
)

// TestSupervisionDeadlineNoticeUsesPhase 验证同阶段补传材料不会重复催办，也不会跨单位送达。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestSupervisionDeadlineNoticeUsesPhase(t *testing.T) {
	pool := reminderFixture(t)
	var id string
	err := pool.QueryRow(t.Context(), `
		WITH filing AS (
			INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status)
			VALUES ('filing','SELF-DEADLINE','自查样例',$1,'自查测试机构','3','approved') RETURNING id
		), task AS (
			INSERT INTO supervision_tasks(request_id,filing_id,filing_number,system_name,
				organization_name,title,due_on,cycle_year,status,version,published_at)
			SELECT gen_random_uuid(),id,'SELF-DEADLINE','自查样例',$1,'年度自查',DATE '2026-11-28',
				2026,'open',4,clock_timestamp() FROM filing RETURNING id
		)
		INSERT INTO supervision_events(task_id,version,action,from_status,to_status,actor)
		SELECT id,3,'publish','draft','open','监管席位' FROM task RETURNING task_id::text
	`, owner).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	reviewinbox.Register(mux, pool)
	want := []noticeResult{{id, "deadline_due", "open"}}
	assertRegisteredHazardSweep(t, pool, mux, "operator", "organization", owner, want)
	if _, err := pool.Exec(t.Context(), "UPDATE supervision_tasks SET version=5 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	assertRegisteredHazardSweep(t, pool, mux, "operator", "organization", owner, want)
	if other := readNotices(t, mux, "operator", "organization", "未分配单位"); len(other) != 0 {
		t.Fatalf("未分配单位收到监督检查提醒：%v", other)
	}
}
