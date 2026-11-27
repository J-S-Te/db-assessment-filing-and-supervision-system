package alerts

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/migrations"
)

const operatorAlertPath = "/api/v1/operator/alerts"
const ownerOrganization = "隔离单位"

type testDisposition struct {
	ID, Action, Status, RequestedDeadline, RequestedBy, DecisionComment string
}

type dispositionResponse struct {
	ID, HazardID, Status, Deadline string
	TriggeredDeadline              *string
	CurrentlyOverdue               *bool
	Version                        int64
	AllowedActions                 []string
	Disposition                    *testDisposition
	History                        []alertEvent
}

// TestRegisteredHazardAlertRequiresDispatch 验证直接登记隐患派发后才生成可审批的真实预警。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestRegisteredHazardAlertRequiresDispatch(t *testing.T) {
	db, router := newAlertFixture(t)
	ctx := t.Context()
	var hazard string
	err := db.QueryRow(ctx, `WITH filing AS (
	    INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status)
	    VALUES ('filing','F-DIRECT','手工登记系统',$1,'复测机构','2','approved') RETURNING id
	)
	INSERT INTO hazard_items(registration_filing_id,ledger,title,source,discovered_on,severity,
	  description,measure,responsibility,deadline,status)
	SELECT id,'major_risk','直接登记','manual',timezone('Asia/Shanghai',CURRENT_TIMESTAMP)::date-3,
	  'medium','需整改风险','整改措施',$1,timezone('Asia/Shanghai',CURRENT_TIMESTAMP)::date-1,
	  'pending_dispatch' FROM filing RETURNING id::text`, ownerOrganization).Scan(&hazard)
	if err != nil {
		t.Fatal(err)
	}
	if sync := readAlertTest[syncResult](t, router, "POST", endpoint+"/sync", "", 200); sync.Created != 0 {
		t.Fatalf("待派发隐患产生了预警: %+v", sync)
	}
	if _, err := db.Exec(ctx, `UPDATE hazard_items
		SET status='open',version=2,dispatched_at=clock_timestamp(),retest_institution='复测机构'
		WHERE id=$1`, hazard); err != nil {
		t.Fatal(err)
	}
	path := syncSingleAlert(t, router)
	deadline := alertTestDate(t, db, 30)
	requested := callOperatorAlert(t, router, path[len(endpoint)+1:], extensionBody(deadline), 200)
	if requested.HazardID != hazard || requested.Disposition == nil || requested.Disposition.RequestedDeadline != deadline {
		t.Fatalf("直接登记隐患的延期申请 = %+v", requested)
	}
	readAlertTest[dispositionResponse](t, router, "POST", path+"/actions",
		`{"action":"approve","expectedVersion":2,"comment":"同意延期"}`, 200)
	assertAlertExtended(t, db, router, path, hazard, deadline, -1, 3)
}

// TestOperatorAlertScopeAndConversion 验证运营者预警范围、首条通知及现有隐患转办后保持唯一。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestOperatorAlertScopeAndConversion(t *testing.T) {
	db, router := newAlertFixture(t)
	plan := seedAlertPlan(t, db)
	hazard := addAlertHazard(t, db, plan, 1, "high", "open", 5)
	adminPath := syncSingleAlert(t, router)
	id := adminPath[len(endpoint)+1:]
	scope := alertOrganizationQuery(ownerOrganization)
	list := readAlertTest[alertPage](t, router, "GET", operatorAlertPath+scope+"&type=high_risk&severity=high", "", 200)
	if len(list.Items) != 1 || list.Items[0].ID != id {
		t.Fatalf("运营者的真实预警列表 = %+v", list)
	}
	readAlertTest[map[string]string](t, router, "GET", operatorAlertPath, "", 400)
	foreign := alertOrganizationQuery("其他单位")
	readAlertTest[map[string]string](t, router, "GET", operatorAlertPath+"/"+id+foreign, "", 404)
	readAlertTest[map[string]string](t, router, "POST", operatorAlertPath+"/"+id+"/actions"+foreign,
		`{"action":"convert","expectedVersion":1}`, 404)
	converted := callOperatorAlert(t, router, id, `{"action":"convert","expectedVersion":1}`, 200)
	if converted.HazardID != hazard || converted.Status != "processing" || converted.Version != 2 ||
		converted.Disposition == nil || converted.Disposition.Status != "completed" {
		t.Fatalf("转办未复用原隐患：%+v", converted)
	}
	callOperatorAlert(t, router, id, `{"action":"convert","expectedVersion":2}`, 409)
	assertAlertConversionEffects(t, db, id, hazard)
	assertAlertOwnerChange(t, db, router, id)
}

// TestOperatorAlertExtensionDecision 验证延期的审批边界和实际隐患日期、版本、留痕同步。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestOperatorAlertExtensionDecision(t *testing.T) {
	db, router := newAlertFixture(t)
	plan := seedAlertPlan(t, db)
	hazard := addAlertHazard(t, db, plan, 1, "medium", "open", -5)
	adminPath := syncSingleAlert(t, router)
	id := adminPath[len(endpoint)+1:]
	deadline := alertTestDate(t, db, 30)
	callOperatorAlert(t, router, id, extensionBody(alertTestDate(t, db, -3)), 400)
	requested := callOperatorAlert(t, router, id, extensionBody(deadline), 200)
	if requested.Status != "pending" || requested.Version != 2 || requested.Disposition == nil ||
		requested.Disposition.Status != "pending" || requested.Disposition.RequestedDeadline != deadline {
		t.Fatalf("延期申请 = %+v", requested)
	}
	assertRegulatoryAlertQueue(t, router, id)
	admin := readAlertTest[dispositionResponse](t, router, "GET", adminPath, "", 200)
	if !slices.Equal(admin.AllowedActions, []string{"approve", "reject"}) {
		t.Fatalf("政府可执行的申请动作 = %+v", admin.AllowedActions)
	}
	for _, action := range []string{"close", "process"} {
		readAlertTest[map[string]string](t, router, "POST", adminPath+"/actions",
			fmt.Sprintf(`{"action":%q,"expectedVersion":2,"comment":"越过在审请求"}`, action), 409)
	}
	approved := readAlertTest[dispositionResponse](t, router, "POST", adminPath+"/actions",
		`{"action":"approve","expectedVersion":2,"comment":"同意延期。"}`, 200)
	if approved.Status != "pending" || approved.Version != 3 || approved.Disposition.Status != "approved" {
		t.Fatalf("延期审批结果 = %+v", approved)
	}
	assertAlertExtended(t, db, router, adminPath, hazard, deadline, -5, 2)
	assertRegulatoryAlertQueue(t, router, "")
}

// TestAlertMigrationBackfillsExistingActiveAlerts 验证存量有效预警补两端首条通知并保留历史终态。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestAlertMigrationBackfillsExistingActiveAlerts(t *testing.T) {
	pool := alertFixtureBeforeDispositions(t)
	plan := seedAlertPlan(t, pool)
	first := addAlertHazard(t, pool, plan, 1, "high", "open", 5)
	second := addAlertHazard(t, pool, plan, 2, "high", "open", 5)
	active := insertLegacyAlert(t, pool, first, "processing")
	closed := insertLegacyAlert(t, pool, second, "closed")
	if err := migrations.Apply(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	service := New(pool, "监管测试")
	if actual, err := service.sync(t.Context()); err != nil || actual.Created != 0 {
		t.Fatalf("再次同步已存在预警 = %+v, %v", actual, err)
	}
	var operators, institutions, closedNotices int
	err := pool.QueryRow(t.Context(), `SELECT
	    count(*) FILTER (WHERE resource_id=$1 AND event_version=1 AND audience='operator' AND recipient='隔离单位'),
	    count(*) FILTER (WHERE resource_id=$1 AND event_version=1 AND audience='institution' AND recipient='隔离测评机构'),
	    count(*) FILTER (WHERE resource_id=$2) FROM review_notices WHERE resource='alerts'`, active, closed).
		Scan(&operators, &institutions, &closedNotices)
	if err != nil || operators != 1 || institutions != 1 || closedNotices != 0 {
		t.Fatalf("存量预警迁移后两端及关闭项通知数 = %d / %d / %d，错误 %v", operators, institutions, closedNotices, err)
	}
}

// insertLegacyAlert 保存变更前真实结构的预警及两个事件。
// 参数 t 为测试上下文，db 为隔离库，hazard 和 status 为实际来源及原处置状态。
// 返回值 原预警标识。
func insertLegacyAlert(t *testing.T, db *pgxpool.Pool, hazard, status string) string {
	t.Helper()
	var id string
	err := db.QueryRow(t.Context(), `WITH inserted AS (
	  INSERT INTO local_alerts(hazard_id,kind,severity,system_name,organization_name,deadline,status,version,closed_at)
	  SELECT id,'high_risk','high','演练系统','隔离单位',deadline,$2,2,
	    CASE WHEN $2='closed' THEN clock_timestamp() END FROM hazard_items WHERE id=$1 RETURNING id
	), recorded AS (
	  INSERT INTO local_alert_events(alert_id,version,action,from_status,to_status,operator,comment)
	  SELECT id,1,'create',NULL,'pending','系统','' FROM inserted
	  UNION ALL SELECT id,2,CASE WHEN $2='closed' THEN 'close' ELSE 'process' END,
	    'pending',$2,'测试监管员','既有处置记录' FROM inserted
	) SELECT id::text FROM inserted`, hazard, status).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestOperatorAlertDispositionsSerializeAcrossSources 验证同隐患并发申请、驳回与误报通过的真实后果。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestOperatorAlertDispositionsSerializeAcrossSources(t *testing.T) {
	db, router := newAlertFixture(t)
	plan := seedAlertPlan(t, db)
	hazard := addAlertHazard(t, db, plan, 1, "high", "open", -5)
	readAlertTest[map[string]any](t, router, "POST", endpoint+"/sync", "", 200)
	page := readAlertTest[alertPage](t, router, "GET", endpoint, "", 200)
	if len(page.Items) != 2 {
		t.Fatalf("同源预警 = %+v", page)
	}
	winner, blocked := requestSiblingAlerts(t, router, page.Items[0].ID, page.Items[1].ID)
	readAlertTest[map[string]string](t, router, "POST", endpoint+"/"+blocked+"/actions",
		`{"action":"close","expectedVersion":1,"comment":"不能绕过同源在审"}`, 409)
	denied := readAlertTest[dispositionResponse](t, router, "POST", endpoint+"/"+winner+"/actions",
		`{"action":"reject","expectedVersion":2,"comment":"补交误报依据。"}`, 200)
	if denied.Status != "pending" || denied.Disposition.Status != "rejected" {
		t.Fatalf("驳回后的原预警 = %+v", denied)
	}
	callOperatorAlert(t, router, blocked, `{"action":"report_false_alarm","expectedVersion":1,"comment":"已核对本地记录。"}`, 200)
	closed := readAlertTest[dispositionResponse](t, router, "POST", endpoint+"/"+blocked+"/actions",
		`{"action":"approve","expectedVersion":2,"comment":"确认误报。"}`, 200)
	var status string
	var version int64
	err := db.QueryRow(t.Context(), "SELECT status,version FROM hazard_items WHERE id=$1", hazard).Scan(&status, &version)
	if closed.Status != "closed" || closed.Disposition.Status != "approved" || err != nil || status != "open" || version != 1 {
		t.Fatalf("误报批准应仅关闭预警：alert=%+v hazard=%s v%d err=%v", closed, status, version, err)
	}
}

// TestOperatorAlertApprovalRollsBackNoticeFailure 验证通知失败时回滚审批、隐患期限和留痕。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestOperatorAlertApprovalRollsBackNoticeFailure(t *testing.T) {
	db, router := newAlertFixture(t)
	plan := seedAlertPlan(t, db)
	hazard := addAlertHazard(t, db, plan, 1, "high", "open", 5)
	adminPath := syncSingleAlert(t, router)
	id := adminPath[len(endpoint)+1:]
	callOperatorAlert(t, router, id, extensionBody(alertTestDate(t, db, 30)), 200)
	if _, err := db.Exec(t.Context(), `ALTER TABLE review_notices ADD CHECK (state <> 'approved')`); err != nil {
		t.Fatal(err)
	}
	readAlertTest[map[string]string](t, router, "POST", adminPath+"/actions",
		`{"action":"approve","expectedVersion":2,"comment":"同意延期。"}`, 500)
	current := readAlertTest[dispositionResponse](t, router, "GET", adminPath, "", 200)
	var count int
	err := db.QueryRow(t.Context(), `SELECT count(*) FROM hazard_events WHERE hazard_id=$1`, hazard).Scan(&count)
	if current.Version != 2 || current.Deadline != alertTestDate(t, db, 5) || current.Disposition.Status != "pending" ||
		len(current.History) != 2 || count != 0 || err != nil {
		t.Fatalf("通知失败后事务仍部分保存：%+v hazard events=%d err=%v", current, count, err)
	}
}

// assertAlertConversionEffects 检查真实转办和初始通知都只持久化一次。
// 参数 t 为测试上下文，db 为隔离库，alert 与 hazard 为两条业务关联的标识。
// 返回值 无。
func assertAlertConversionEffects(t *testing.T, db *pgxpool.Pool, alert, hazard string) {
	t.Helper()
	var sources, conversions, notices int
	err := db.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM hazard_items),
	  (SELECT count(*) FROM local_alert_dispositions WHERE hazard_id=$1 AND action='convert'),
	  (SELECT count(*) FROM review_notices WHERE audience='operator' AND resource='alerts'
	    AND resource_id=$2 AND recipient='隔离单位' AND reference='BA-01' AND state='pending')`, hazard, alert).
		Scan(&sources, &conversions, &notices)
	if err != nil || sources != 1 || conversions != 1 || notices != 1 {
		t.Fatalf("转办 / 下发计数 = %d / %d / %d，错误 = %v", sources, conversions, notices, err)
	}
}

// assertAlertExtended 核对提前告警、真实隐患及延期审核意见使用同一实际日期。
// 参数 t、db、router 为测试环境，path、hazard、deadline 为预期关联和期限，原期限用 elapsed 表示，wantVersion 为隐患版本。
// 返回值 无。
func assertAlertExtended(t *testing.T, db *pgxpool.Pool, router http.Handler, path, hazard, deadline string,
	elapsed int, wantVersion int64,
) {
	t.Helper()
	var stored string
	var version int64
	var history, notice int
	err := db.QueryRow(t.Context(), `SELECT to_char(h.deadline,'YYYY-MM-DD'), h.version,
	  (SELECT count(*) FROM hazard_events WHERE hazard_id=h.id AND action='extend_deadline'),
	  (SELECT count(*) FROM review_notices n JOIN local_alerts a ON a.id=n.resource_id
	    WHERE a.hazard_id=h.id AND n.resource='alerts' AND n.audience='operator' AND n.state='approved')
	  FROM hazard_items h WHERE h.id=$1`, hazard).Scan(&stored, &version, &history, &notice)
	current := readAlertTest[dispositionResponse](t, router, "GET", path, "", 200)
	if err != nil || stored != deadline || current.Deadline != deadline || version != wantVersion || history != 1 || notice != 1 {
		t.Fatalf("延期结果未同步：date=%s v%d history=%d notice=%d detail=%+v err=%v", stored, version, history, notice, current, err)
	}
	if current.TriggeredDeadline == nil || *current.TriggeredDeadline != alertTestDate(t, db, elapsed) ||
		current.CurrentlyOverdue == nil || *current.CurrentlyOverdue {
		t.Fatalf("延期后仍被视为当前逾期或未保留触发期限：%+v", current)
	}
}

// assertRegulatoryAlertQueue 核对监管待审筛选与工作台数量来自同一真实申请。
// 参数 t 为测试上下文，router 为业务路由，id 为应出现的预警，空值要求待审为空。
// 返回值 无。
func assertRegulatoryAlertQueue(t *testing.T, router http.Handler, id string) {
	t.Helper()
	stats := readAlertTest[Summary](t, router, "GET", endpoint+"/summary", "", 200)
	page := readAlertTest[alertPage](t, router, "GET", endpoint+"?review=pending", "", 200)
	if id == "" && stats.PendingDispositions == 0 && len(page.Items) == 0 {
		return
	}
	if id != "" && stats.PendingDispositions == 1 && len(page.Items) == 1 && page.Items[0].ID == id {
		return
	}
	t.Fatalf("待审筛选与汇总不一致：%+v / %+v", stats, page)
}

// assertAlertOwnerChange 验证预警读取范围取自当前备案且原单位无法继续办理。
// 参数 t 为测试上下文，db 为隔离库，router 为业务路由，id 为预警标识。
// 返回值 无。
func assertAlertOwnerChange(t *testing.T, db *pgxpool.Pool, router http.Handler, id string) {
	t.Helper()
	if _, err := db.Exec(t.Context(), `UPDATE business_cases SET organization_name='现归属单位' WHERE kind='filing'`); err != nil {
		t.Fatal(err)
	}
	path := operatorAlertPath + "/" + id
	readAlertTest[map[string]string](t, router, "GET", path+alertOrganizationQuery(ownerOrganization), "", 404)
	callOperatorAlert(t, router, id, `{"action":"report_false_alarm","expectedVersion":2,"comment":"原单位不能申请"}`, 404)
	current := readAlertTest[alertDetail](t, router, "GET", path+alertOrganizationQuery("现归属单位"), "", 200)
	if current.OrganizationName != "现归属单位" {
		t.Fatalf("详情仍显示过期归属：%+v", current)
	}
}

// requestSiblingAlerts 并发提交同一隐患两种触发预警的申请。
// 参数 t 为测试上下文，router 为业务路由，first、second 为同源预警标识。
// 返回值 成功和被阻止的预警标识。
func requestSiblingAlerts(t *testing.T, router http.Handler, first, second string) (string, string) {
	t.Helper()
	type requestResult struct {
		id     string
		status int
	}
	results := make(chan requestResult, 2)
	for _, id := range []string{first, second} {
		go func() {
			out := alarmRequest(router, "POST", operatorAlertPath+"/"+id+"/actions"+alertOrganizationQuery(ownerOrganization),
				`{"action":"report_false_alarm","expectedVersion":1,"comment":"核对告警来源。"}`)
			results <- requestResult{id, out.Code}
		}()
	}
	one, two := <-results, <-results
	if one.status != 200 {
		one, two = two, one
	}
	if one.status != 200 || two.status != 409 {
		t.Fatalf("同源并发请求返回：%+v / %+v", one, two)
	}
	return one.id, two.id
}

// alertTestDate 返回真实数据库上海业务日的相对日期。
// 参数 t 为测试上下文，db 为隔离库，days 为偏移天数。
// 返回值 YYYY-MM-DD 日期文本。
func alertTestDate(t *testing.T, db *pgxpool.Pool, days int) string {
	t.Helper()
	var value string
	if err := db.QueryRow(t.Context(), `SELECT to_char(timezone('Asia/Shanghai',CURRENT_TIMESTAMP)::date+$1::int,'YYYY-MM-DD')`, days).
		Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

// extensionBody 为测试生成要求延期到具体日期的业务请求。
// 参数 date 为目标期限。
// 返回值 JSON 请求正文。
func extensionBody(date string) string {
	return fmt.Sprintf(`{"action":"request_extension","expectedVersion":1,"comment":"需要重新安排整改。","requestedDeadline":%q}`, date)
}

// callOperatorAlert 调用当前单位的预警处置接口。
// 参数 t 为测试上下文，router 为路由，id、body、status 为请求及预期状态。
// 返回值 已解码的业务响应。
func callOperatorAlert(t *testing.T, router http.Handler, id, body string, status int) dispositionResponse {
	t.Helper()
	return readAlertTest[dispositionResponse](t, router, "POST", operatorAlertPath+"/"+id+"/actions"+
		alertOrganizationQuery(ownerOrganization), body, status)
}

// alertOrganizationQuery 编码运营者的本地查询范围。
// 参数 organization 为当前运营单位名称。
// 返回值 适用于 URL 的查询部分。
func alertOrganizationQuery(organization string) string {
	return "?organization=" + url.QueryEscape(organization)
}
