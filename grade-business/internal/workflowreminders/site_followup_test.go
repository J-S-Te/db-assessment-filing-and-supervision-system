package workflowreminders

import (
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/internal/reviewinbox"
)

// TestSiteFollowUpDeadlineUsesIndependentRound 验证现场提醒按阶段而非材料版本去重且仅投递原单位。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestSiteFollowUpDeadlineUsesIndependentRound(t *testing.T) {
	db := reminderFixture(t)
	id := seedSiteReminder(t, db)
	mux := http.NewServeMux()
	reviewinbox.Register(mux, db)
	runConcurrentSiteSweep(t, db, "2026-11-27")
	assertSiteNoticeCount(t, db, id, "deadline_due_soon", 6, 1)

	_, err := db.Exec(t.Context(), "UPDATE supervision_site_inspections SET version=8 WHERE task_id=$1", id)
	if err != nil {
		t.Fatal(err)
	}
	runConcurrentSiteSweep(t, db, "2026-11-27")
	assertSiteNoticeCount(t, db, id, "deadline_due_soon", 6, 1)
	runConcurrentSiteSweep(t, db, "2026-11-28")
	assertSiteNoticeCount(t, db, id, "deadline_due", 6, 1)
	assertSiteNoticeCount(t, db, id, "deadline_due", 8, 0)

	items := readNotices(t, mux, "operator", "organization", owner)
	var currentSite, parallelSelfCheck bool
	for _, item := range items {
		if item.ResourceID == id && item.State == "deadline_due" {
			currentSite = currentSite || item.Status == "following_up"
			parallelSelfCheck = parallelSelfCheck || item.Status == "open"
		}
	}
	if !currentSite || !parallelSelfCheck {
		t.Fatalf("站内消息应分别投影视现场与自查的当前阶段：%+v", items)
	}
	if other := readNotices(t, mux, "operator", "organization", "未分配单位"); len(other) != 0 {
		t.Fatalf("其他运营单位收到了现场提醒：%+v", other)
	}
}

// TestSiteFollowUpDeadlineStopsOnHandoff 验证监管待复核、办结及归属失效时停止，退补开启新一轮。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestSiteFollowUpDeadlineStopsOnHandoff(t *testing.T) {
	db := reminderFixture(t)
	id := seedSiteReminder(t, db)
	setReminderSitePhase(t, db, id, "follow_up_submitted", 8)
	runConcurrentSiteSweep(t, db, "2026-11-29")
	assertSiteNoticeCount(t, db, id, "deadline_overdue", 6, 0)

	setReminderSitePhase(t, db, id, "following_up", 9)
	runConcurrentSiteSweep(t, db, "2026-11-29")
	assertSiteNoticeCount(t, db, id, "deadline_overdue", 9, 1)
	setReminderSitePhase(t, db, id, "closed", 10)
	runConcurrentSiteSweep(t, db, "2026-11-28")
	assertSiteNoticeCount(t, db, id, "deadline_due", 9, 0)

	setReminderSitePhase(t, db, id, "following_up", 11)
	_, err := db.Exec(t.Context(), `UPDATE business_cases SET organization_name='调出后单位'
		WHERE id=(SELECT filing_id FROM supervision_tasks WHERE id=$1)`, id)
	if err != nil {
		t.Fatal(err)
	}
	runConcurrentSiteSweep(t, db, "2026-11-29")
	assertSiteNoticeCount(t, db, id, "deadline_overdue", 11, 0)
}

// seedSiteReminder 创建同日自查和现场跟进的真实数据库约束夹具。
// 参数 t、db 为测试和隔离数据源。
// 返回值 监督任务标识。
func seedSiteReminder(t *testing.T, db *pgxpool.Pool) string {
	t.Helper()
	_, err := db.Exec(t.Context(), `INSERT INTO workflow_calendar_days(day,is_workday)
		VALUES (DATE '2026-11-28',true) ON CONFLICT(day) DO UPDATE SET is_workday=true`)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	err = db.QueryRow(t.Context(), `WITH filing AS (
		INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status)
		VALUES ('filing','SITE-DEADLINE','现场样例',$1,'测试机构','3','approved')
		RETURNING id
	), task AS (
		INSERT INTO supervision_tasks(request_id,filing_id,filing_number,system_name,organization_name,
		   title,due_on,cycle_year,status,version,published_at)
		SELECT gen_random_uuid(),id,'SITE-DEADLINE','现场样例',$1,'测试自查',
		   DATE '2026-11-28',2026,'open',6,clock_timestamp() FROM filing RETURNING id
	), annual AS (
		INSERT INTO supervision_events(task_id,version,action,from_status,to_status,actor)
		SELECT id,6,'publish','draft','open','监管' FROM task RETURNING task_id
	), site AS (
		INSERT INTO supervision_site_inspections
		   (task_id,status,version,visit_at,location,inspectors,outcome,follow_up_due_on)
		SELECT task_id,'following_up',7,clock_timestamp(),'现场','检查组','follow_up_required',
		   DATE '2026-11-28' FROM annual RETURNING task_id
	)
	INSERT INTO supervision_site_events(task_id,version,action,status,actor,input_hash)
	SELECT task_id,6,'follow_up','following_up','监管',repeat('a',64) FROM site
	RETURNING task_id::text`, owner).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// runConcurrentSiteSweep 同时运行两个期限扫描，核对真实写入幂等路径。
// 参数 t、db 为测试环境，day 为要模拟的上海业务日。
// 返回值 无。
func runConcurrentSiteSweep(t *testing.T, db *pgxpool.Pool, day string) {
	t.Helper()
	results := make(chan error, 2)
	for range cap(results) {
		go func() { results <- sweepOn(t.Context(), db, Windows{}, day) }()
	}
	for range cap(results) {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}

// assertSiteNoticeCount 核对指定阶段与原因实际投递的现场站内消息。
// 参数 t、db 为测试环境，id、state、version 为提醒键，want 为预期投递数。
// 返回值 无。
func assertSiteNoticeCount(t *testing.T, db *pgxpool.Pool, id, state string, version int64, want int) {
	t.Helper()
	var got int
	err := db.QueryRow(t.Context(), `SELECT count(*) FROM review_notices
		WHERE resource='supervision-site' AND resource_id=$1 AND state=$2 AND event_version=$3
		  AND audience='operator' AND recipient=$4 AND workflow_day=DATE '2026-11-28'`,
		id, state, version, owner).Scan(&got)
	if err != nil || got != want {
		t.Fatalf("现场 %s 第 %d 版提醒 = %d，预期 %d，错误 %v", state, version, got, want, err)
	}
}

// setReminderSitePhase 模拟现场阶段流转，退补时添加新的真实阶段事件。
// 参数 t、db 为测试环境，id、status、version 为样例任务及新阶段。
// 返回值 无。
func setReminderSitePhase(t *testing.T, db *pgxpool.Pool, id, status string, version int64) {
	t.Helper()
	_, err := db.Exec(t.Context(), `WITH moved AS (
		UPDATE supervision_site_inspections SET status=$2,version=$3 WHERE task_id=$1
		RETURNING task_id,status,version
	)
	INSERT INTO supervision_site_events(task_id,version,action,status,actor,input_hash)
	SELECT task_id,version,'return_follow_up',status,'监管',repeat('b',64)
	FROM moved WHERE status='following_up'`, id, status, version)
	if err != nil {
		t.Fatal(err)
	}
}
