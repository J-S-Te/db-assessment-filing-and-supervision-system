package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/migrations"
)

const endpoint = "/api/v1/alerts"

// TestAlertInputBounds 核对无效筛选和非空同步正文在读取业务库前被拒绝。
// 参数 t 为测试上下文。
// 返回值 无。
func TestAlertInputBounds(t *testing.T) {
	router := http.NewServeMux()
	New(nil, "测试").Register(router)
	readAlertTest[map[string]string](t, router, "GET", endpoint+"?severity=other", "", 400)
	readAlertTest[map[string]string](t, router, "GET", endpoint+"?status=pending&status=closed", "", 400)
	readAlertTest[map[string]string](t, router, "POST", endpoint+"/sync", "{}", 400)
}

// TestWorkerRecordsAutomaticAlert 验证后台工作者启动后实际保存预警和经办标签。
// 参数 t 为测试上下文，连接仅使用当前用例的隔离 schema。
// 返回值 无。
func TestWorkerRecordsAutomaticAlert(t *testing.T) {
	db, _ := newAlertFixture(t)
	planID := seedAlertPlan(t, db)
	hazardID := addAlertHazard(t, db, planID, 1, "high", "open", 5)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		New(db, "本地预警任务").Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	for range 100 {
		var operator string
		err := db.QueryRow(ctx, `SELECT e.operator FROM local_alert_events e
		    JOIN local_alerts a ON a.id=e.alert_id WHERE a.hazard_id=$1`, hazardID).Scan(&operator)
		if err == nil {
			if operator != "本地预警任务" {
				t.Fatalf("自动任务经办留痕错误：%q", operator)
			}
			return
		}
		if err != pgx.ErrNoRows {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("后台工作者未生成预警")
}

// TestSyncUsesLocalHazardsOnce 核对上海业务日边界、来源状态及并发同步不重复。
// 参数 t 为测试上下文，仅使用独立 PostgreSQL schema。
// 返回值 无。
func TestSyncUsesLocalHazardsOnce(t *testing.T) {
	db, router := newAlertFixture(t)
	plan := seedAlertPlan(t, db)
	high := addAlertHazard(t, db, plan, 1, "high", "open", 5)
	addAlertHazard(t, db, plan, 2, "high", "pending_review", 5)
	overdue := addAlertHazard(t, db, plan, 3, "medium", "pending_review", -1)
	addAlertHazard(t, db, plan, 4, "low", "open", 0)
	double := addAlertHazard(t, db, plan, 5, "high", "returned", -1)
	addAlertHazard(t, db, plan, 6, "high", "closed", -1)
	counts := make(chan int, 2)
	for range 2 {
		go func() {
			out := alarmRequest(router, "POST", endpoint+"/sync", "")
			var result struct{ Created int }
			if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &result) != nil {
				counts <- -1
				return
			}
			counts <- result.Created
		}()
	}
	first, second := <-counts, <-counts
	if first < 0 || second < 0 || first+second != 4 {
		t.Fatalf("同步应新增 4 条，实际为 %d + %d", first, second)
	}
	assertSyncList(t, router, high, overdue, double)
}

// assertSyncList 验证归属、类型、级别筛选和实际计数。
// 参数 t 为测试上下文，router 为实际业务路由，high、overdue、double 为预期隐患。
// 返回值 无。
func assertSyncList(t *testing.T, router http.Handler, high, overdue, double string) {
	t.Helper()
	type item struct{ ID, HazardID, Type, Status string }
	type page struct {
		Items      []item
		NextCursor string
	}
	list := readAlertTest[page](t, router, "GET", endpoint+"?status=pending", "", 200)
	want := map[string]int{high + ":high_risk": 1, overdue + ":overdue": 1, double + ":high_risk": 1, double + ":overdue": 1}
	for _, current := range list.Items {
		want[current.HazardID+":"+current.Type]--
	}
	for key, count := range want {
		if count != 0 {
			t.Fatalf("来源或类型与实际记录不符: %s, %d", key, count)
		}
	}
	if len(list.Items) != 4 {
		t.Fatalf("预警记录数 = %d，期望 4", len(list.Items))
	}
	filtered := readAlertTest[page](t, router, "GET", endpoint+"?severity=high&limit=1", "", 200)
	if len(filtered.Items) != 1 || filtered.NextCursor == "" {
		t.Fatalf("分页或级别筛选与实际记录不符: %+v", filtered)
	}
	older := readAlertTest[page](t, router, "GET", endpoint+"?severity=high&cursor="+filtered.NextCursor, "", 200)
	if len(older.Items) != 2 || older.Items[0].ID == filtered.Items[0].ID {
		t.Fatalf("分页结果未跳过已显示的记录: %+v", older)
	}
	stats := readAlertTest[struct{ Total, Pending, Processing, Closed int }](t, router, "GET", endpoint+"/summary", "", 200)
	if stats.Total != 4 || stats.Pending != 4 || stats.Processing != 0 || stats.Closed != 0 {
		t.Fatalf("汇总统计与列表状态不符: %+v", stats)
	}
}

// TestAlertActionsRecordHistory 验证处理、关闭和版本冲突，以及处置不写回隐患状态。
// 参数 t 为测试上下文，仅使用独立 PostgreSQL schema。
// 返回值 无。
func TestAlertActionsRecordHistory(t *testing.T) {
	db, router := newAlertFixture(t)
	plan := seedAlertPlan(t, db)
	hazard := addAlertHazard(t, db, plan, 1, "high", "open", 5)
	path := syncSingleAlert(t, router)
	readAlertTest[map[string]any](t, router, "POST", path+"/actions",
		`{"action":"process","expectedVersion":1,"comment":"已查阅整改期限"}`, 200)
	readAlertTest[map[string]any](t, router, "POST", path+"/actions",
		`{"action":"close","expectedVersion":1,"comment":"版本已过期"}`, 409)
	readAlertTest[map[string]any](t, router, "POST", path+"/actions",
		`{"action":"close","expectedVersion":2,"comment":"已记录后续跟进"}`, 200)
	result := readAlertTest[struct {
		Status         string
		HazardStatus   string
		Version        int
		ClosedAt       *time.Time
		AllowedActions []string
		History        []struct{ Action, FromStatus, ToStatus, Operator, Comment string }
	}](t, router, "GET", path, "", 200)
	if result.Status != "closed" || result.HazardStatus != "open" || result.Version != 3 ||
		result.ClosedAt == nil || len(result.AllowedActions) != 0 || len(result.History) != 3 ||
		result.History[0].Action != "create" ||
		result.History[1].FromStatus != "pending" || result.History[1].ToStatus != "processing" ||
		result.History[2].Comment != "已记录后续跟进" || result.History[2].Operator != "测试监管员" {
		t.Fatalf("处置留痕或来源隐患状态不符: %+v", result)
	}
	sync := readAlertTest[struct{ Created int }](t, router, "POST", endpoint+"/sync", "", 200)
	if sync.Created != 0 {
		t.Fatalf("关闭后的提醒再次同步产生重复记录: %d", sync.Created)
	}
	var live string
	if err := db.QueryRow(t.Context(), "SELECT status FROM hazard_items WHERE id=$1", hazard).Scan(&live); err != nil || live != "open" {
		t.Fatalf("原隐患状态与实际结果不符: %s, %v", live, err)
	}
}

// TestAlertAuditFailureRollsBackStatus 验证留痕写入失败时预警状态、版本与关闭时间回滚。
// 参数 t 为测试上下文，仅操作独立 PostgreSQL schema。
// 返回值 无。
func TestAlertAuditFailureRollsBackStatus(t *testing.T) {
	db, router := newAlertFixture(t)
	plan := seedAlertPlan(t, db)
	addAlertHazard(t, db, plan, 1, "high", "open", 5)
	path := syncSingleAlert(t, router)
	if _, err := db.Exec(t.Context(), `ALTER TABLE local_alert_events
		ADD CONSTRAINT test_close_audit_failure CHECK (action <> 'close')`); err != nil {
		t.Fatal(err)
	}
	readAlertTest[map[string]string](t, router, "POST", path+"/actions",
		`{"action":"close","expectedVersion":1,"comment":"记录写入失败演练"}`, 500)
	current := readAlertTest[struct {
		Status   string
		Version  int
		ClosedAt *time.Time
		History  []struct{ Action string }
	}](t, router, "GET", path, "", 200)
	if current.Status != "pending" || current.Version != 1 || current.ClosedAt != nil || len(current.History) != 1 {
		t.Fatalf("留痕失败后应保留原状态和版本: %+v", current)
	}
}

// syncSingleAlert 同步当前唯一符合条件的隐患并返回其预警详情地址。
// 参数 t 为测试上下文，router 为实际路由。
// 返回值 已入库的预警详情地址。
func syncSingleAlert(t *testing.T, router http.Handler) string {
	t.Helper()
	readAlertTest[struct{ Created int }](t, router, "POST", endpoint+"/sync", "", 200)
	page := readAlertTest[struct{ Items []struct{ ID string } }](t, router, "GET", endpoint, "", 200)
	if len(page.Items) != 1 {
		t.Fatalf("预期单条实际预警，得到 %+v", page)
	}
	return endpoint + "/" + page.Items[0].ID
}

// readAlertTest 发起 HTTP 请求并核对结构化响应。
// 参数 t 为测试上下文，router 为实际路由，method、path、body、status 为请求与期望状态。
// 返回值 已解码的 API 响应。
func readAlertTest[T any](t *testing.T, router http.Handler, method, path, body string, status int) T {
	t.Helper()
	item := alarmRequest(router, method, path, body)
	var result T
	if item.Code != status {
		t.Fatalf("%s %s 返回 %d，预期 %d: %s", method, path, item.Code, status, item.Body)
	}
	if err := json.Unmarshal(item.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// alarmRequest 创建与浏览器相同的 JSON 或空请求。
// 参数 router 为 HTTP 业务路由，method、path 和 body 为方法、地址与正文。
// 返回值 实际 HTTP 响应记录。
func alarmRequest(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	item := httptest.NewRecorder()
	router.ServeHTTP(item, req)
	return item
}

// newAlertFixture 创建并迁移仅供测试运行的 schema。
// 参数 t 为测试上下文，通过环境变量提供明确的测试 DSN。
// 返回值 测试域数据库和实际预警路由。
func newAlertFixture(t *testing.T) (*pgxpool.Pool, *http.ServeMux) {
	t.Helper()
	pool := alertTestPool(t)
	if err := migrations.Apply(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	router := http.NewServeMux()
	service := New(pool, "测试监管员")
	service.Register(router)
	service.RegisterOperator(router)
	return pool, router
}

// alertFixtureBeforeDispositions 准备包含原预警和收件箱表的独立迁移节点。
// 参数 t 为测试上下文，测试数据库来自明确的测试 DSN。
// 返回值 已应用版本 025 之前表结构的隔离连接池。
func alertFixtureBeforeDispositions(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := alertTestPool(t)
	_, err := pool.Exec(t.Context(), `CREATE TABLE schema_migrations (
	    name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasSuffix(name, ".sql") && name < "025_operator_alert_dispositions.sql" {
			applyAlertMigration(t, pool, name)
		}
	}
	return pool
}

// alertTestPool 打开当前预警测试使用的独立 schema。
// 参数 t 为测试上下文，测试数据库来自明确的测试 DSN。
// 返回值 当前测试专用的数据库连接池。
func alertTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if dsn == "" {
		t.Skip("GRADE_BUSINESS_TEST_DSN 未配置")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil || config.ConnConfig.Database != "grade_business_test" {
		t.Fatal("须使用明确隔离的 grade_business_test 数据库")
	}
	return openAlertSchema(t, config)
}

// applyAlertMigration 将指定 SQL 迁移和版本标记安装到当前测试隔离模式。
// 参数 t 为测试上下文，pool 为隔离数据库，path 为迁移文件名。
// 返回值 无。
func applyAlertMigration(t *testing.T, pool *pgxpool.Pool, path string) {
	t.Helper()
	ddl, err := os.ReadFile("../../migrations/" + path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO schema_migrations(name) VALUES($1)`, path); err != nil {
		t.Fatal(err)
	}
}

// openAlertSchema 建立当前用例的临时 schema 并登记清理。
// 参数 t 为测试上下文，config 为已验证的专用测试库连接。
// 返回值 当前测试 schema 的连接池。
func openAlertSchema(t *testing.T, config *pgxpool.Config) *pgxpool.Pool {
	t.Helper()
	admin, err := pgxpool.NewWithConfig(t.Context(), config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("local_alert_%d_%d", os.Getpid(), time.Now().UnixNano())
	id := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+id+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedAlertPlan 准备真实归属链路所需的隔离备案和整改规划。
// 参数 t 为测试上下文，db 为当前独立 PostgreSQL schema。
// 返回值 已批准的隔离整改规划标识。
func seedAlertPlan(t *testing.T, db *pgxpool.Pool) string {
	t.Helper()
	var plan string
	err := db.QueryRow(t.Context(), `WITH f AS (
		INSERT INTO business_cases (kind,number,system_name,organization_name,assessment_org,level,status)
		VALUES ('filing','BA-01','演练系统','隔离单位','隔离测评机构','2','approved') RETURNING id
	), r AS (
		INSERT INTO business_cases (kind,number,assessment_org,filing_id,risk,status)
		SELECT 'report','BG-01','隔离测评机构',id,'high','approved' FROM f RETURNING id
	)
	INSERT INTO remediation_plans (report_id,deadline,risks,status,submission,submitted_at)
	SELECT id,'2099-01-01','[{}]'::jsonb,'approved',1,clock_timestamp() FROM r RETURNING id::text`).Scan(&plan)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// addAlertHazard 登记具备指定风险、状态和业务日截止时间的本地测试隐患。
// 参数 t 为测试上下文，db 为隔离库，plan、index、severity、status、days 为来源及业务字段。
// 返回值 已保存的本地隐患标识。
func addAlertHazard(t *testing.T, db *pgxpool.Pool, plan string, index int, severity, status string, days int) string {
	t.Helper()
	var id string
	err := db.QueryRow(t.Context(), `INSERT INTO hazard_items
		(plan_id,risk_index,severity,description,measure,responsibility,deadline,status,closed_at)
		VALUES ($1,$2,$3,'已登记的隔离隐患','核对业务记录','演练员',
		timezone('Asia/Shanghai',CURRENT_TIMESTAMP)::date+$5::integer,$4,
		CASE WHEN $4='closed' THEN clock_timestamp() END) RETURNING id::text`,
		plan, index, severity, status, days).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
