package workflowreminders

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/reviewinbox"
	"grade-business/migrations"
)

const (
	owner     = "样例运营单位"
	assessor  = "原测评报告机构"
	newAgency = "改派后备案机构"
)

type noticeResult struct {
	ResourceID string `json:"resourceId"`
	State      string `json:"state"`
	Status     string `json:"status"`
}

// TestRegisteredHazardRemindersFollowDispatch 验证直接登记隐患只在派发后产生到期或复测催办。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestRegisteredHazardRemindersFollowDispatch(t *testing.T) {
	ctx := t.Context()
	pool := reminderFixture(t)
	var id string
	err := pool.QueryRow(ctx, `WITH filing AS (
		INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status)
		VALUES ('filing','F-MANUAL','外部来源系统',$1,$2,'2','approved') RETURNING id
	)
	INSERT INTO hazard_items(registration_filing_id,ledger,title,source,discovered_on,severity,
	  description,measure,responsibility,deadline,status)
	SELECT id,'major_risk','直接登记','manual','2026-11-25','high','需整改风险','整改措施',$1,
	  '2026-11-28','pending_dispatch' FROM filing RETURNING id::text`, owner, newAgency).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, pool)
	reviewinbox.Register(mux, pool)
	query, body := "?organization="+url.QueryEscape(owner), `{"id":"`+id+`"}`
	assertReminderStatus(t, postReminder(mux, query, body), http.StatusNotFound)
	assertRegisteredHazardSweep(t, pool, mux, "operator", "organization", owner, nil)
	if _, err := pool.Exec(ctx, `WITH submitted AS (
		UPDATE hazard_items SET status='pending_retest',version=4,
		  dispatched_at=clock_timestamp(),retest_institution=$2 WHERE id=$1 RETURNING id
	)
	INSERT INTO hazard_events(hazard_id,action,from_status,to_status,operator,version,comment)
	SELECT id,'dispatch','pending_dispatch','open','监管席位',2,'' FROM submitted
	UNION ALL
	SELECT id,'submit','open','pending_retest','运营单位',3,'已提交证据' FROM submitted`, id, assessor); err != nil {
		t.Fatal(err)
	}
	assertRegisteredHazardSweep(t, pool, mux, "institution", "institution", assessor,
		[]noticeResult{{id, "deadline_due", "pending_retest"}})
	assertConcurrentManualRequests(t, mux, query, body)
	items := readNotices(t, mux, "institution", "institution", assessor)
	if len(items) != 2 || items[0] != (noticeResult{id, "retest_reminder", "pending_retest"}) {
		t.Fatalf("派发快照机构未收到真实复测催办: %+v", items)
	}
	if items = readNotices(t, mux, "institution", "institution", newAgency); len(items) != 0 {
		t.Fatalf("非派发机构收到意见: %+v", items)
	}
	var stageVersion int64
	err = pool.QueryRow(ctx, `SELECT event_version FROM review_notices
		WHERE resource_id=$1 AND state='deadline_due'`, id).Scan(&stageVersion)
	if err != nil || stageVersion != 3 {
		t.Fatalf("到期提醒应引用待复测状态流转版本: %d, %v", stageVersion, err)
	}
}

// assertRegisteredHazardSweep 核对日期扫描重试时，指定席位只收到预期隐患提醒。
// 参数 t、pool、mux 为测试环境，audience、key、recipient 为收件范围，want 为预期站内消息。
// 返回值 无。
func assertRegisteredHazardSweep(t *testing.T, pool *pgxpool.Pool, mux *http.ServeMux,
	audience, key, recipient string, want []noticeResult,
) {
	t.Helper()
	for range 2 {
		if err := sweepOn(t.Context(), pool, Windows{}, "2026-11-28"); err != nil {
			t.Fatal(err)
		}
	}
	if got := readNotices(t, mux, audience, key, recipient); !slices.Equal(got, want) {
		t.Fatalf("%s 的到期提醒 = %+v, 期望 %+v", recipient, got, want)
	}
}

// TestRetestReminderScopesDeliveryAndDeduplicates 验证真实收件方、并发重试及过期状态的响应。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestRetestReminderScopesDeliveryAndDeduplicates(t *testing.T) {
	pool := reminderFixture(t)
	_, hazard := seedHazard(t, pool, "2099-01-01")
	mux := http.NewServeMux()
	Register(mux, pool)
	reviewinbox.Register(mux, pool)
	query := "?organization=" + url.QueryEscape(owner)
	body := `{"id":"` + hazard + `"}`
	unknown := `{"id":"00000000-0000-4000-8000-000000000001"}`
	assertReminderStatus(t, postReminder(mux, "?organization=其他单位", body), http.StatusNotFound)
	assertReminderStatus(t, postReminder(mux, query, unknown), http.StatusNotFound)
	assertConcurrentManualRequests(t, mux, query, body)
	items := readNotices(t, mux, "institution", "institution", assessor)
	want := noticeResult{hazard, "retest_reminder", "pending_retest"}
	if len(items) != 1 || items[0] != want {
		t.Fatalf("原报告登记机构收到 %+v, 预期 %+v", items, want)
	}
	if reassigned := readNotices(t, mux, "institution", "institution", newAgency); len(reassigned) != 0 {
		t.Fatalf("改派后备案的机构收到 %+v", reassigned)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE review_notices SET workflow_day=workflow_day-1
		WHERE resource_id=$1 AND state='retest_reminder'`, hazard); err != nil {
		t.Fatal(err)
	}
	assertConcurrentManualRequests(t, mux, query, body)
	if _, err := pool.Exec(t.Context(), `UPDATE hazard_items SET status='pending_review' WHERE id=$1`, hazard); err != nil {
		t.Fatal(err)
	}
	assertReminderStatus(t, postReminder(mux, query, body), http.StatusConflict)
}

// assertConcurrentManualRequests 核对同一运营单位对同一隐患的并发请求仅有一次实际投递。
// 参数 t 为测试上下文，mux 为路由，query 为运营范围，body 为当前隐患正文。
// 返回值 无。
func assertConcurrentManualRequests(t *testing.T, mux *http.ServeMux, query, body string) {
	t.Helper()
	responses := make(chan *httptest.ResponseRecorder, 5)
	for range cap(responses) {
		go func() { responses <- postReminder(mux, query, body) }()
	}
	sent := 0
	for range cap(responses) {
		response := <-responses
		assertReminderStatus(t, response, http.StatusOK)
		var result map[string]bool
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result) != 1 {
			t.Fatalf("催办响应 = %s, err=%v", response.Body, err)
		}
		if first, ok := result["sent"]; !ok {
			t.Fatalf("缺少 sent 字段: %s", response.Body)
		} else if first {
			sent++
		}
	}
	if sent != 1 {
		t.Fatalf("实际投递 %d 次，预期恰好一次", sent)
	}
}

// TestDeadlineNoticesFollowStageAndWorkdays 验证跨休息日、到期日和逾期的投递及重试去重。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDeadlineNoticesFollowStageAndWorkdays(t *testing.T) {
	pool := reminderFixture(t)
	filing, hazard := seedHazard(t, pool, "2026-11-23")
	reportID := seedDueReport(t, pool, filing)
	mux := http.NewServeMux()
	reviewinbox.Register(mux, pool)
	for _, phase := range []struct {
		day   string
		count int
	}{
		{"2026-11-19", 0}, {"2026-11-20", 2}, {"2026-11-21", 2},
		{"2026-11-23", 4}, {"2026-11-24", 6}, {"2026-11-25", 6},
	} {
		assertConcurrentSweeps(t, pool, phase.day)
		if got := len(readNotices(t, mux, "institution", "institution", assessor)); got != phase.count {
			t.Fatalf("%s 的提醒数量 = %d，预期 %d", phase.day, got, phase.count)
		}
	}
	items := readNotices(t, mux, "institution", "institution", assessor)
	assertDeadlineStates(t, items, hazard, "pending_retest")
	assertDeadlineStates(t, items, reportID, "pending_initial")
	if items := readNotices(t, mux, "institution", "institution", newAgency); len(items) != 0 {
		t.Fatalf("与业务无关的机构收到 %+v", items)
	}
	if items := readNotices(t, mux, "operator", "organization", owner); len(items) != 0 {
		t.Fatalf("当前机构复测和机构送审的事项送给了运营者: %+v", items)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE hazard_items SET status='closed',closed_at=now(),version=4
		WHERE id=$1`, hazard); err != nil {
		t.Fatal(err)
	}
	assertConcurrentSweeps(t, pool, "2026-11-25")
	assertDeadlineStates(t, readNotices(t, mux, "institution", "institution", assessor), hazard, "closed")
}

// TestDeadlineNoticeCoexistsWithLegacyReminders 验证旧补正和催办回执与自动期限提醒可以分别送达。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDeadlineNoticeCoexistsWithLegacyReminders(t *testing.T) {
	pool := reminderFixture(t)
	_, hazard := seedHazard(t, pool, "2026-11-23")
	var plan string
	err := pool.QueryRow(t.Context(), `INSERT INTO remediation_plans
		(report_id,deadline,risks,status,submission,submitted_at,version)
		SELECT p.report_id,'2026-11-23',p.risks,'returned',1,p.submitted_at,2
		FROM remediation_plans p JOIN hazard_items h ON h.plan_id=p.id WHERE h.id=$1
		RETURNING id::text`, hazard).Scan(&plan)
	if err != nil {
		t.Fatal(err)
	}
	legacyID, sent := deliverLegacyNotices(t, pool, plan)
	if !sent {
		t.Fatal("原有整改催办首次未投递")
	}
	assertConcurrentSweeps(t, pool, "2026-11-23")
	if repeatedID, sent := deliverLegacyNotices(t, pool, plan); repeatedID != legacyID || sent {
		t.Fatalf("原有整改催办重试的标识 = %s，是否新投递 = %t", repeatedID, sent)
	}
	mux := http.NewServeMux()
	reviewinbox.Register(mux, pool)
	items := readNotices(t, mux, "operator", "organization", owner)
	want := []noticeResult{{plan, "deadline_due", "returned"}, {plan, "reminder", "returned"},
		{plan, "returned", "returned"}}
	if !slices.Equal(items, want) {
		t.Fatalf("运营者整改意见 = %+v，预期 %+v", items, want)
	}
}

// TestDeadlineNoticeUsesAdjustedWorkday 验证周末补班计算为正式到期日。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDeadlineNoticeUsesAdjustedWorkday(t *testing.T) {
	pool := reminderFixture(t)
	filing, _ := seedHazard(t, pool, "2099-01-01")
	report := seedDueReport(t, pool, filing)
	if _, err := pool.Exec(t.Context(), `INSERT INTO workflow_calendar_days(day,is_workday)
		VALUES ('2026-11-22',true), ('2026-11-23',false)`); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	reviewinbox.Register(mux, pool)
	for _, phase := range []struct{ day, state string }{
		{"2026-11-20", "deadline_due_soon"}, {"2026-11-22", "deadline_due"},
		{"2026-11-23", "deadline_overdue"},
	} {
		assertConcurrentSweeps(t, pool, phase.day)
		items := readNotices(t, mux, "institution", "institution", assessor)
		if len(items) == 0 || items[0] != (noticeResult{report, phase.state, "pending_initial"}) {
			t.Fatalf("%s 通知 = %+v，预期最新状态 %s", phase.day, items, phase.state)
		}
	}
}

// TestDeadlineSweepDefersLockedHazard 验证正在销项的隐患不会发送过期提醒或阻塞其他投递。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDeadlineSweepDefersLockedHazard(t *testing.T) {
	pool := reminderFixture(t)
	filing, hazard := seedHazard(t, pool, "2026-11-23")
	report := seedDueReport(t, pool, filing)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), `UPDATE hazard_items SET status='closed',closed_at=now(),version=4
		WHERE id=$1`, hazard); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := sweepOn(ctx, pool, Windows{Report: 1}, "2026-11-23"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertConcurrentSweeps(t, pool, "2026-11-23")
	mux := http.NewServeMux()
	reviewinbox.Register(mux, pool)
	items := readNotices(t, mux, "institution", "institution", assessor)
	if !slices.Equal(items, []noticeResult{{report, "deadline_due", "pending_initial"}}) {
		t.Fatalf("并发销项后的通知 = %+v", items)
	}
}

// deliverLegacyNotices 调用旧站内投递服务，登记同阶段的补正决定和人工催办。
// 参数 t 为测试上下文，pool 为独立测试库，plan 为待补正规划标识。
// 返回值 旧人工催办标识及是否首次送达。
func deliverLegacyNotices(t *testing.T, pool *pgxpool.Pool, plan string) (string, bool) {
	t.Helper()
	var noticeID string
	var sent bool
	if err := pgx.BeginFunc(t.Context(), pool, func(tx pgx.Tx) error {
		notice := reviewinbox.Notice{Audience: "operator", Resource: "remediations", ResourceID: plan,
			State: "returned", Recipient: owner, Version: 2, Reference: "TEST-R", Subject: "样例系统"}
		if err := reviewinbox.Deliver(t.Context(), tx, notice); err != nil {
			return err
		}
		notice.State = "reminder"
		var err error
		noticeID, sent, err = reviewinbox.DeliverReminder(t.Context(), tx, notice, "2026-11-23")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return noticeID, sent
}

// assertDeadlineStates 核对指定事项可见的三种自动提醒及当前实际业务状态。
// 参数 t 为测试上下文，items 为当前收件方内容，id 和 status 为目标标识和预期状态。
// 返回值 无。
func assertDeadlineStates(t *testing.T, items []noticeResult, id, status string) {
	t.Helper()
	got := make([]string, 0, 3)
	for _, item := range items {
		if item.ResourceID == id {
			got = append(got, item.State)
			if item.Status != status {
				t.Errorf("提醒的当前状态 = %q, 预期 %q", item.Status, status)
			}
		}
	}
	if !slices.Equal(got, []string{"deadline_overdue", "deadline_due", "deadline_due_soon"}) {
		t.Fatalf("事项 %s 自动提醒 = %v", id, got)
	}
}

// assertConcurrentSweeps 模拟两个管理服务同时扫描并检查扫描结果。
// 参数 t 为测试上下文，pool 为测试库连接池，day 为明确给定的上海业务日期。
// 返回值 无。
func assertConcurrentSweeps(t *testing.T, pool *pgxpool.Pool, day string) {
	t.Helper()
	results := make(chan error, 2)
	for range cap(results) {
		go func() { results <- sweepOn(t.Context(), pool, Windows{Report: 1}, day) }()
	}
	for range cap(results) {
		if err := <-results; err != nil {
			t.Errorf("扫描 %s 失败: %v", day, err)
		}
	}
}

// reminderFixture 在实际测试库创建仅属于当前用例的模式并运行全部迁移。
// 参数 t 为隔离测试生命周期及清理上下文。
// 返回值 使用新建模式的 PostgreSQL 连接池。
func reminderFixture(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if dsn == "" {
		t.Skip("需要显式设置隔离测试库 GRADE_BUSINESS_TEST_DSN")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil || config.ConnConfig.Database != "grade_business_test" ||
		config.ConnConfig.Host != "127.0.0.1" || config.ConnConfig.Port != 15444 {
		t.Fatal("限定本地隔离库 grade_business_test 127.0.0.1:15444")
	}
	control, err := pgxpool.NewWithConfig(t.Context(), config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(control.Close)
	schema := fmt.Sprintf("workflow_reminders_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := control.Exec(t.Context(), "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := control.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrations.Apply(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

// seedHazard 建立有明确归属并已提交机构复测的当前隐患。
// 参数 t 为隔离测试上下文，pool 为测试库连接池，deadline 为批准过的隐患期限。
// 返回值 备案标识及隐患标识。
func seedHazard(t *testing.T, pool *pgxpool.Pool, deadline string) (string, string) {
	t.Helper()
	var filing, hazard string
	err := pool.QueryRow(t.Context(), `WITH f AS (
		INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status)
		VALUES('filing','TEST-F','样例系统',$1,$2,'3','approved') RETURNING id
	), r AS (
		INSERT INTO business_cases(kind,number,assessment_org,filing_id,risk,status)
		SELECT 'report','TEST-R',$3,id,'high','approved' FROM f RETURNING id
	), p AS (
		INSERT INTO remediation_plans(report_id,deadline,risks,status,submission,submitted_at)
		SELECT id,$4::text::date,'[{"risk":"high","description":"隐患","measure":"整改措施",
		    "responsibility":"负责人","deadline":"2026-11-23"}]',
		    'approved',1,clock_timestamp() FROM r RETURNING id
	), h AS (
		INSERT INTO hazard_items(plan_id,risk_index,severity,description,measure,responsibility,deadline,status,version)
		SELECT id,1,'high','隐患','整改措施','负责人',$4::text::date,'pending_retest',2 FROM p RETURNING id
	), event AS (
		INSERT INTO hazard_events(hazard_id,action,from_status,to_status,operator,comment,version,occurred_at)
		SELECT id,'submit','open','pending_retest',$1,'已提交证据',2,'2026-11-19 10:00+08' FROM h
	)
	SELECT f.id::text,h.id::text FROM f,h`, owner, newAgency, assessor, deadline).Scan(&filing, &hazard)
	if err != nil {
		t.Fatal(err)
	}
	return filing, hazard
}

// seedDueReport 建立周五进入评审且原登记机构有权处理的待审核报告。
// 参数 t 为隔离测试上下文，pool 为测试库连接池，filing 为已存在的备案标识。
// 返回值 报告标识。
func seedDueReport(t *testing.T, pool *pgxpool.Pool, filing string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(), `WITH r AS (
		INSERT INTO business_cases(kind,number,assessment_org,filing_id,risk,status,version,submitted_at)
		VALUES('report','TEST-PENDING',$2,$1,'high','pending_initial',2,'2026-11-20 10:00+08') RETURNING id
	), event AS (
		INSERT INTO case_review_events(case_id,action,from_status,to_status,operator,version,occurred_at)
		SELECT id,'submit','draft','pending_initial',$2,2,'2026-11-20 10:00+08' FROM r
	) SELECT id::text FROM r`, filing, assessor).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// readNotices 通过真实站内收件箱读取明确接收范围的可见状态。
// 参数 t 为测试上下文，mux 为测试路由，audience、key、name 为收件方接口及范围。
// 返回值 可见消息集合。
func readNotices(t *testing.T, mux *http.ServeMux, audience, key, name string) []noticeResult {
	t.Helper()
	path := "/api/v1/" + audience + "/feedback?" + key + "=" + url.QueryEscape(name)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	assertReminderStatus(t, response, http.StatusOK)
	var result struct{ Items []noticeResult }
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Items
}

// assertReminderStatus 核对接口成功或错误语义并保留实际响应以供诊断。
// 参数 t 为测试上下文，response 为实际响应，want 为预期 HTTP 状态。
// 返回值 无。
func assertReminderStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status=%d, want %d, body=%s", response.Code, want, response.Body)
	}
}
