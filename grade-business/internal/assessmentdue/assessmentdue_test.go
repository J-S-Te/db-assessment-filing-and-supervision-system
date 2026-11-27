package assessmentdue

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/migrations"
)

const testDuePath = "/api/v1/assessment-dues"
const testDueUnit = "测评周期原运营单位"
const testDueInstitution = "测评周期原测评机构"

// TestAssessmentDateSendsEachPhaseOnce 验证显式截止日及同周期两阶段通知。
// 参数 t 为隔离数据库试验上下文。
// 返回值 无。
func TestAssessmentDateSendsEachPhaseOnce(t *testing.T) {
	db, router := dueFixture(t)
	filing, tomorrow, later := seedDueFiling(t, db)
	input := map[string]any{"requestId": "90000000-0000-4000-8000-000000000101",
		"filingId": filing, "dueOn": tomorrow, "evidence": "依据备案现场确认的计划日期"}
	registered := callDue(t, router, "POST", testDuePath, input, 201)
	id := registered["id"].(string)
	if registered["status"] != "due_soon" || registered["warningDays"] != float64(7) {
		t.Fatalf("实际临期及提醒设置错误：%v", registered)
	}
	if again := callDue(t, router, "POST", testDuePath, input, 201); again["id"] != id {
		t.Fatalf("登记重试重复创建记录：%v", again)
	}
	input["requestId"] = "90000000-0000-4000-8000-000000000102"
	callDue(t, router, "POST", testDuePath, input, 409)
	assertDueNotices(t, db, id, "assessment_due_soon", 2)
	assertSweepCount(t, router, 0)
	if _, err := db.Exec(t.Context(), `UPDATE assessment_due_records SET due_on=due_on-1 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	assertSweepCount(t, router, 2)
	assertSweepCount(t, router, 0)
	assertDueNotices(t, db, id, "assessment_due", 2)
	verifyDueRevisionAndClose(t, router, id, later)
}

// TestAssessmentDateUsesCurrentAuthorizedRecipients 验证期限查询及写入符合当前备案归属。
// 参数 t 为隔离数据库试验上下文。
// 返回值 无。
func TestAssessmentDateUsesCurrentAuthorizedRecipients(t *testing.T) {
	db, router := dueFixture(t)
	filing, tomorrow, later := seedDueFiling(t, db)
	register := map[string]any{"requestId": "90000000-0000-4000-8000-000000000103",
		"filingId": filing, "dueOn": later, "evidence": "内部日期核验依据"}
	id := callDue(t, router, "POST", testDuePath, register, 201)["id"].(string)
	const successor = "测评周期承接运营单位"
	if _, err := db.Exec(t.Context(), `UPDATE business_cases SET organization_name=$2 WHERE id=$1`, filing, successor); err != nil {
		t.Fatal(err)
	}
	callDue(t, router, "PATCH", testDuePath+"/"+id, map[string]any{
		"action": "revise", "expectedVersion": 1, "dueOn": tomorrow, "evidence": "新归属明确的评测计划"}, 200)
	operator := "/api/v1/operator/assessment-dues/" + id
	callDue(t, router, "GET", operator+"?organization="+url.QueryEscape(testDueUnit), nil, 404)
	var operatorNotice string
	if err := db.QueryRow(t.Context(), `SELECT recipient FROM review_notices
		WHERE resource='assessment-dues' AND resource_id=$1 AND audience='operator'`, id).Scan(&operatorNotice); err != nil || operatorNotice != successor {
		t.Fatalf("消息未发送给实际承接范围：%q %v", operatorNotice, err)
	}
	view := callDue(t, router, "GET", operator+"?organization="+url.QueryEscape(successor), nil, 200)
	if view["evidence"] != nil || view["history"] != nil || view["organizationName"] != successor {
		t.Fatalf("运营者详情披露监管内部依据或错误归属：%v", view)
	}
}

// TestAssessmentDateWorkerStartsAndStopsWithScope 验证启动扫描及失效备案停止投递。
// 参数 t 为隔离数据库试验上下文。
// 返回值 无。
func TestAssessmentDateWorkerStartsAndStopsWithScope(t *testing.T) {
	db, router := dueFixture(t)
	filing, _, later := seedDueFiling(t, db)
	item := callDue(t, router, "POST", testDuePath, map[string]any{
		"requestId": "90000000-0000-4000-8000-000000000104", "filingId": filing,
		"dueOn": later, "evidence": "备案下一次测评日期确认单"}, 201)
	id := item["id"].(string)
	if _, err := db.Exec(t.Context(), `UPDATE assessment_due_records SET due_on=`+todaySQL+` WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	checkWorkerDelivery(t, db, id)
	if _, err := db.Exec(t.Context(), `UPDATE business_cases SET cancelled_at=clock_timestamp() WHERE id=$1`, filing); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `UPDATE assessment_due_records SET cycle=cycle+1 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	assertSweepCount(t, router, 0)
	view := callDue(t, router, "GET", testDuePath+"/"+id, nil, 200)
	if view["status"] != "suspended" {
		t.Fatalf("已注销备案的期限仍显示为有效：%v", view)
	}
	callDue(t, router, "PATCH", testDuePath+"/"+id, map[string]any{
		"action": "revise", "expectedVersion": 1, "dueOn": later, "evidence": "无效备案不得修订"}, 409)
}

// checkWorkerDelivery 启动真实后台任务并等待数据库观察到该轮投递。
// 参数 t、db 为试验环境，id 是预期获送达消息的登记。
// 返回值 无，测试退出时取消工作任务。
func checkWorkerDelivery(t *testing.T, db *pgxpool.Pool, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		New(db, "测评日期任务").Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		err := db.QueryRow(ctx, `SELECT count(*) FROM review_notices
			WHERE resource='assessment-dues' AND resource_id=$1 AND state='assessment_due'`, id).Scan(&count)
		if err != nil {
			t.Fatal(err)
		}
		if count == 2 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("启动后的测评提醒未实际送达两端：%d", count)
		case <-ticker.C:
		}
	}
}

// verifyDueRevisionAndClose 验证版本保护、明确修订依据和终止后的账本留痕。
// 参数 t、router 为试验环境，id、later 为登记记录和新有效期限。
// 返回值 无。
func verifyDueRevisionAndClose(t *testing.T, router http.Handler, id, later string) {
	t.Helper()
	path := testDuePath + "/" + id
	input := map[string]any{"action": "revise", "expectedVersion": 1, "dueOn": later, "evidence": "复核后约定新日期"}
	revised := callDue(t, router, "PATCH", path, input, 200)
	if revised["status"] != "upcoming" || revised["dueOn"] != later {
		t.Fatalf("修订后期限错误：%v", revised)
	}
	callDue(t, router, "PATCH", path, input, 200)
	input["evidence"] = "陈旧版本不能覆盖已记录的新依据"
	callDue(t, router, "PATCH", path, input, 409)
	closed := callDue(t, router, "PATCH", path, map[string]any{
		"action": "close", "expectedVersion": 2, "evidence": "本轮跟踪已结束"}, 200)
	view := callDue(t, router, "GET", path, nil, 200)
	if closed["status"] != "closed" || len(view["history"].([]any)) != 3 {
		t.Fatalf("关闭未保留原始和修订依据：%v", view)
	}
	assertSweepCount(t, router, 0)
}

// dueFixture 建立应用全量迁移的隔离数据库和真实 HTTP 路由。
// 参数 t 为测试上下文。
// 返回值 专用连接池和 API 路由。
func dueFixture(t *testing.T) (*pgxpool.Pool, *http.ServeMux) {
	t.Helper()
	raw := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if raw == "" {
		t.Skip("需要专用隔离数据库")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil || cfg.ConnConfig.Database != "grade_business_test" {
		t.Fatal("测评周期测试必须使用 grade_business_test 专用数据库")
	}
	admin, err := pgxpool.NewWithConfig(t.Context(), cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var schema string
	if err := admin.QueryRow(t.Context(), `SELECT 'assessment_due_test_'||replace(gen_random_uuid()::text,'-','')`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err = migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	router := http.NewServeMux()
	New(db, "周期联验监管").Register(router)
	return db, router
}

// seedDueFiling 写入受控生效备案和上海日期场景。
// 参数 t、db 为隔离上下文及数据连接。
// 返回值 备案标识、临期截止日和远期截止日。
func seedDueFiling(t *testing.T, db *pgxpool.Pool) (string, string, string) {
	t.Helper()
	var filing, tomorrow, later string
	err := db.QueryRow(t.Context(), `INSERT INTO business_cases
		(kind,number,system_name,organization_name,assessment_org,level,status)
		VALUES('filing','ASSESS-DUE-F1','周期系统',$1,$2,'3','approved')
		RETURNING id::text,((CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date+1)::text,
		((CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date+30)::text`, testDueUnit, testDueInstitution).
		Scan(&filing, &tomorrow, &later)
	if err != nil {
		t.Fatal(err)
	}
	return filing, tomorrow, later
}

// callDue 发出标准 HTTP 请求并核对状态和响应 JSON。
// 参数 t、router 为测试环境，method、path、input、status 为请求及预期状态。
// 返回值 业务响应。
func callDue(t *testing.T, router http.Handler, method, path string, input any, status int) map[string]any {
	t.Helper()
	var body []byte
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf("%s %s = %d，期望 %d：%s", method, path, response.Code, status, response.Body)
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// assertSweepCount 核验扫描仅返回实际新增的站内消息数。
// 参数 t、router 为试验环境，count 是实际应送达的条数。
// 返回值 无。
func assertSweepCount(t *testing.T, router http.Handler, count int) {
	t.Helper()
	if result := callDue(t, router, "POST", testDuePath+"/sweep", nil, 200); result["notices"] != float64(count) {
		t.Fatalf("扫描送达计数错误：%v", result)
	}
}

// assertDueNotices 核对同一周期类型写入的实际通知数量。
// 参数 t、db 为试验上下文及数据连接，id、state、want 为归属、类型和计数。
// 返回值 无。
func assertDueNotices(t *testing.T, db *pgxpool.Pool, id, state string, want int) {
	t.Helper()
	var count int
	err := db.QueryRow(t.Context(), `SELECT count(*) FROM review_notices
		WHERE resource='assessment-dues' AND resource_id=$1 AND state=$2`, id, state).Scan(&count)
	if err != nil || count != want {
		t.Fatalf("%s 消息数量错误：%d %v", state, count, err)
	}
}
