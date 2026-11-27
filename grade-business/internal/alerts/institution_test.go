package alerts

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/reviewinbox"
)

const institutionAlertPath = "/api/v1/institution/alerts"
const alertInstitutionName = "隔离测评机构"

// TestInstitutionAlertFeedback 保证机构提交、监管退回重提与确认使用同一预警版本。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestInstitutionAlertFeedback(t *testing.T) {
	db, router := institutionAlertFixture(t)
	plan := seedAlertPlan(t, db)
	hazard := addAlertHazard(t, db, plan, 1, "high", "open", 5)
	adminPath := syncSingleAlert(t, router)
	id := adminPath[len(endpoint)+1:]
	path := institutionAlertPath + "/" + id
	scope := alertInstitutionScope(alertInstitutionName)
	assertInstitutionAlertActions(t, router, path+scope, "pending", "process")
	institutionAlertAction(t, router, path, scope, "submit_feedback", 1, "已核实", 409)
	institutionAlertAction(t, router, path, scope, "convert", 1, "机构不负责转工单", 400)
	institutionAlertAction(t, router, path, scope, "close", 1, "机构无销项权限", 400)
	institutionAlertAction(t, router, path, scope, "process", 1, "已领取处理", 200)
	assertInstitutionAlertActions(t, router, path+scope, "processing", "submit_feedback")
	institutionAlertAction(t, router, path, scope, "submit_feedback", 2, "  ", 400)
	institutionAlertAction(t, router, path, scope, "submit_feedback", 2, "核对整改情况", 200)
	assertInstitutionAlertActions(t, router, path+scope, "processing")
	assertRegulatoryAlertQueue(t, router, id)
	admin := readAlertTest[alertDetail](t, router, "GET", adminPath, "", 200)
	if admin.Disposition == nil || admin.Disposition.Action != "submit_feedback" ||
		admin.Disposition.RequestedBy != alertInstitutionName || admin.Disposition.RequesterAudience != "institution" ||
		!slices.Equal(admin.AllowedActions, []string{"approve", "reject"}) {
		t.Fatalf("监管详情的机构反馈或审批操作不符：%+v", admin)
	}
	institutionAlertAction(t, router, adminPath, "", "close", 3, "等待预警反馈复核", 409)
	institutionAlertAction(t, router, adminPath, "", "reject", 3, "补充核查说明", 200)
	assertInstitutionAlertActions(t, router, path+scope, "processing", "submit_feedback")
	institutionAlertAction(t, router, path, scope, "submit_feedback", 3, "使用旧版本", 409)
	institutionAlertAction(t, router, path, scope, "submit_feedback", 4, "补充整改说明", 200)
	institutionAlertAction(t, router, adminPath, "", "approve", 5, "确认反馈情况", 200)
	closed := assertInstitutionAlertActions(t, router, path+scope, "closed")
	operator := readAlertTest[alertDetail](t, router, "GET", operatorAlertPath+"/"+id+alertOrganizationQuery(ownerOrganization), "", 200)
	if closed.Version != 6 || closed.HazardStatus != "open" || closed.HazardID != hazard ||
		operator.Version != 6 || operator.Status != "closed" || operator.HazardStatus != "open" {
		t.Fatalf("预警关闭后两端详情或原隐患不符：机构=%+v，运营者=%+v", closed, operator)
	}
	assertInstitutionAlertNotices(t, router, id, scope)
}

// TestInstitutionAlertPreservesSourceScope 保证备案换机构不改变历史报告或登记派发的预警归属。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestInstitutionAlertPreservesSourceScope(t *testing.T) {
	db, router := institutionAlertFixture(t)
	plan := seedAlertPlan(t, db)
	first := addAlertHazard(t, db, plan, 1, "high", "open", 5)
	var direct string
	err := db.QueryRow(t.Context(), `INSERT INTO hazard_items(registration_filing_id,ledger,title,source,
	  discovered_on,severity,description,measure,responsibility,deadline,status)
	SELECT id,'major_risk','隔离登记','manual',CURRENT_DATE-3,'high','风险说明','整改措施',
	  organization_name,CURRENT_DATE-1,'pending_dispatch' FROM business_cases WHERE kind='filing'
	RETURNING id::text`).Scan(&direct)
	if err != nil {
		t.Fatal(err)
	}
	scope := alertInstitutionScope(alertInstitutionName)
	path := syncSingleAlert(t, router)
	readAlertTest[map[string]string](t, router, "GET", institutionAlertPath, "", 400)
	readAlertTest[map[string]string](t, router, "GET", institutionAlertPath+scope+"&review=pending", "", 400)
	page := readAlertTest[alertPage](t, router, "GET", institutionAlertPath+scope, "", 200)
	if len(page.Items) != 1 || page.Items[0].HazardID != first {
		t.Fatalf("历史规划预警或待派发隐患的可见范围不符：%+v", page)
	}
	if _, err := db.Exec(t.Context(), `UPDATE hazard_items SET status='open',dispatched_at=clock_timestamp(),
	  retest_institution='派发快照机构' WHERE id=$1`, direct); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `UPDATE business_cases SET assessment_org='现备案机构' WHERE kind='filing'`); err != nil {
		t.Fatal(err)
	}
	readAlertTest[syncResult](t, router, "POST", endpoint+"/sync", "", 200)
	page = readAlertTest[alertPage](t, router, "GET", institutionAlertPath+alertInstitutionScope("派发快照机构")+"&type=overdue", "", 200)
	if len(page.Items) != 1 || page.Items[0].HazardID != direct {
		t.Fatalf("已派发的逾期预警未归属原快照机构：%+v", page)
	}
	other := alertInstitutionScope("现备案机构")
	page = readAlertTest[alertPage](t, router, "GET", institutionAlertPath+other, "", 200)
	if len(page.Items) != 0 {
		t.Fatalf("当前备案机构得到了历史预警：%+v", page)
	}
	institutionPath := institutionAlertPath + path[len(endpoint):]
	readAlertTest[map[string]string](t, router, "GET", institutionPath+other, "", 404)
	institutionAlertAction(t, router, institutionPath, other, "process", 1, "跨机构处理", 404)
	assertInstitutionAlertActions(t, router, institutionPath+scope, "pending", "process")
	if _, err := db.Exec(t.Context(), `UPDATE business_cases SET status='returned' WHERE kind='report'`); err != nil {
		t.Fatal(err)
	}
	readAlertTest[map[string]string](t, router, "GET", institutionPath+scope, "", 404)
	institutionAlertAction(t, router, institutionPath, scope, "process", 1, "来源报告未批准", 404)
}

// TestInstitutionFeedbackAndOperatorRequestSerialize 验证同源两类预警共享待审申请的唯一性。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestInstitutionFeedbackAndOperatorRequestSerialize(t *testing.T) {
	db, router := institutionAlertFixture(t)
	plan := seedAlertPlan(t, db)
	addAlertHazard(t, db, plan, 1, "high", "open", -5)
	readAlertTest[syncResult](t, router, "POST", endpoint+"/sync", "", 200)
	page := readAlertTest[alertPage](t, router, "GET", endpoint+"?type=high_risk", "", 200)
	other := readAlertTest[alertPage](t, router, "GET", endpoint+"?type=overdue", "", 200)
	if len(page.Items) != 1 || len(other.Items) != 1 {
		t.Fatalf("申请缺少两个触发源：%+v %+v", page, other)
	}
	high, overdue := page.Items[0].ID, other.Items[0].ID
	path := institutionAlertPath + "/" + high
	scope := alertInstitutionScope(alertInstitutionName)
	institutionAlertAction(t, router, path, scope, "process", 1, "已领取处理", 200)
	responses := make(chan int, 2)
	go func() {
		responses <- alarmRequest(router, "POST", path+"/actions"+scope,
			`{"action":"submit_feedback","expectedVersion":2,"comment":"机构核查说明"}`).Code
	}()
	go func() {
		responses <- alarmRequest(router, "POST", operatorAlertPath+"/"+overdue+"/actions"+alertOrganizationQuery(ownerOrganization),
			`{"action":"report_false_alarm","expectedVersion":1,"comment":"运营者核查说明"}`).Code
	}()
	statuses := []int{<-responses, <-responses}
	slices.Sort(statuses)
	queue := readAlertTest[alertPage](t, router, "GET", endpoint+"?review=pending", "", 200)
	if !slices.Equal(statuses, []int{200, 409}) || len(queue.Items) != 1 {
		t.Fatalf("机构和运营者在同源预警产生了多个在审结果：%v，队列 %+v", statuses, queue.Items)
	}
	assertInstitutionAlertActions(t, router, path+scope, "processing")
}

// TestInstitutionAlertMutationRechecksLockedReport 验证申请等待来源行锁后重验真实报告归属。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestInstitutionAlertMutationRechecksLockedReport(t *testing.T) {
	db, router := institutionAlertFixture(t)
	plan := seedAlertPlan(t, db)
	addAlertHazard(t, db, plan, 1, "high", "open", 5)
	path := institutionAlertPath + syncSingleAlert(t, router)[len(endpoint):]
	tx, err := db.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	var blocker int
	if err := tx.QueryRow(t.Context(), `UPDATE business_cases SET assessment_org='新的报告机构'
	  WHERE kind='report' RETURNING pg_backend_pid()`).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	result := make(chan int, 1)
	go func() {
		result <- alarmRequest(router, "POST", path+"/actions"+alertInstitutionScope(alertInstitutionName),
			`{"action":"process","expectedVersion":1,"comment":"旧机构等待办理"}`).Code
	}()
	awaitAlertLockWait(t, db, blocker)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if code := <-result; code != 404 {
		t.Fatalf("来源更新后旧机构仍可办理，状态码 %d", code)
	}
	assertInstitutionAlertActions(t, router, path+alertInstitutionScope("新的报告机构"), "pending", "process")
}

// awaitAlertLockWait 等待 PostgreSQL 确认处置请求正等待来源行锁。
// 参数 t、db 为隔离用例，blocker 是持有来源行锁的当前测试会话。
// 返回值 无。
func awaitAlertLockWait(t *testing.T, db *pgxpool.Pool, blocker int) {
	t.Helper()
	for range 100 {
		var waiting bool
		err := db.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity active
		  WHERE $1=ANY(pg_blocking_pids(active.pid)))`, blocker).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("处置请求未在预期时间内等待来源锁")
}

// institutionAlertFixture 注册隔离库中的预警及站内消息入口。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 当前用例数据库和实际业务路由。
func institutionAlertFixture(t *testing.T) (*pgxpool.Pool, *http.ServeMux) {
	t.Helper()
	db, router := newAlertFixture(t)
	New(db, "测试监管员").RegisterInstitution(router)
	reviewinbox.Register(router, db)
	return db, router
}

// alertInstitutionScope 编码当前机构的本地业务范围。
// 参数 name 为机构全称。
// 返回值 已编码的 HTTP 查询字符串。
func alertInstitutionScope(name string) string {
	return "?institution=" + url.QueryEscape(name)
}

// institutionAlertAction 发送机构或监管办理请求，并验证实际响应状态。
// 参数 t、router 为用例与路由，path、scope 为业务地址与范围，action、version、comment 为办理字段，status 为期望状态。
// 返回值 无。
func institutionAlertAction(t *testing.T, router http.Handler, path, scope, action string, version int, comment string, status int) {
	t.Helper()
	readAlertTest[map[string]any](t, router, "POST", path+"/actions"+scope,
		fmt.Sprintf(`{"action":%q,"expectedVersion":%d,"comment":%q}`, action, version, comment), status)
}

// assertInstitutionAlertActions 核对机构可执行动作使用已持久化的预警状态。
// 参数 t、router 为用例与业务路由，path 为详情地址，status、actions 为期望状态和操作。
// 返回值 已验证的预警详情。
func assertInstitutionAlertActions(t *testing.T, router http.Handler, path, status string, actions ...string) alertDetail {
	t.Helper()
	item := readAlertTest[alertDetail](t, router, "GET", path, "", 200)
	if item.Status != status || !slices.Equal(item.AllowedActions, actions) {
		t.Fatalf("预警状态或动作不符：得到 %s %v，预期 %s %v", item.Status, item.AllowedActions, status, actions)
	}
	return item
}

// assertInstitutionAlertNotices 核对当前机构和运营者仅收到初始预警和确认结论。
// 参数 t、router 为用例与路由，id 为预警标识，scope 为机构范围。
// 返回值 无。
func assertInstitutionAlertNotices(t *testing.T, router http.Handler, id, scope string) {
	t.Helper()
	type items struct {
		Items []struct{ ResourceID, State, Status string }
	}
	for _, path := range []string{"/api/v1/institution/feedback" + scope,
		"/api/v1/operator/feedback" + alertOrganizationQuery(ownerOrganization)} {
		inbox := readAlertTest[items](t, router, "GET", path, "", 200)
		if len(inbox.Items) != 3 || inbox.Items[0].State != "approved" ||
			inbox.Items[1].State != "rejected" || inbox.Items[2].State != "pending" {
			t.Fatalf("预警收件箱应只收到初始预警和监管决定：%+v", inbox)
		}
		for _, notice := range inbox.Items {
			if notice.ResourceID != id || notice.Status != "closed" {
				t.Fatalf("预警消息的关联或当前状态不符：%+v", notice)
			}
		}
	}
}
