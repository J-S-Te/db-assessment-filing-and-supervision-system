package audittrail

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/migrations"
)

const auditEndpoint = "/api/v1/audit"

// TestAuditLabelsImplementationRestartInitial 核对实施报备重新初审的审计动作名称。
// 参数 t 为审计动作名称的测试上下文。
// 返回值 无。
func TestAuditLabelsImplementationRestartInitial(t *testing.T) {
	if label := actionLabel("implementations", "restart_initial", "pending_final"); label != "重新进入初审" {
		t.Errorf("重新初审的审计动作名称 = %q", label)
	}
}

// TestAuditRejectsUnknownFilters 核对只读审计接口在查询数据库前拒绝无效筛选。
// 参数 t 为测试上下文。
// 返回值 无。
func TestAuditRejectsUnknownFilters(t *testing.T) {
	router := http.NewServeMux()
	New(nil).Register(router)
	for _, search := range []string{"?resource=users", "?q=one&q=two", "?cursor=bad", "?limit=0", "?token=value"} {
		readAuditTest[map[string]string](t, router, auditEndpoint+search, 400)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, auditEndpoint, nil))
	if response.Code != 405 {
		t.Fatalf("审计不能写入: %d", response.Code)
	}
}

// TestAuditUnifiesActualEvents 核对八类持久化留痕的筛选、隐私字段和同秒分页。
// 参数 t 为测试上下文，仅使用明确隔离 PostgreSQL schema。
// 返回值 无。
func TestAuditUnifiesActualEvents(t *testing.T) {
	pool := auditFixture(t)
	if _, err := pool.Exec(t.Context(), auditBusinessFixtures); err != nil {
		t.Fatal(err)
	}
	router := http.NewServeMux()
	New(pool).Register(router)
	coverage := collectAuditPages(t, router)
	for _, resource := range []string{"filings", "filing-applications", "implementations", "reports",
		"remediations", "hazards", "alerts", "publications"} {
		if coverage[resource] == 0 {
			t.Errorf("来源缺失: %s", resource)
		}
	}
	verifyAuditLinksAndFilters(t, router)
}

// TestAuditSearchesRecordedBusinessText 核对报告审核意见和对象标识可检索。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestAuditSearchesRecordedBusinessText(t *testing.T) {
	pool := auditFixture(t)
	if _, err := pool.Exec(t.Context(), auditBusinessFixtures); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
		UPDATE case_review_events SET comment='test 报告结论需补正'
		WHERE action='return_for_correction'`); err != nil {
		t.Fatal(err)
	}
	router := http.NewServeMux()
	New(pool).Register(router)
	page := readAuditTest[testAuditPage](t, router, auditEndpoint+"?resource=reports&q=TEST", 200)
	if len(page.Items) != 1 || page.Items[0].ActionLabel != "退回补正" {
		t.Errorf("按审核意见检索报告事件 = %+v，期望退回补正事件", page.Items)
	}
	published := readAuditTest[testAuditPage](t, router, auditEndpoint+"?resource=publications", 200)
	if len(published.Items) != 1 {
		t.Fatalf("公告夹具记录不唯一: %+v", published.Items)
	}
	id := published.Items[0].ResourceID
	matched := readAuditTest[testAuditPage](t, router, auditEndpoint+"?q="+id, 200)
	if len(matched.Items) != 1 || matched.Items[0].ResourceID != id {
		t.Errorf("按业务对象标识 %s 检索 = %+v", id, matched.Items)
	}
}

// TestAuditShowsDirectHazardRegistration 验证登记事件可按真实备案编号或隐患标题检索。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestAuditShowsDirectHazardRegistration(t *testing.T) {
	pool := auditFixture(t)
	_, err := pool.Exec(t.Context(), `WITH filing AS (
	    INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status)
	    VALUES ('filing','F-DIRECT','直接登记系统','运营单位','复测机构','2','approved') RETURNING id
	), hazard AS (
	    INSERT INTO hazard_items(registration_filing_id,ledger,title,source,discovered_on,severity,
	      description,measure,responsibility,deadline,status)
	    SELECT id,'major_risk','test 手工登记','manual','2026-11-22','high','需整改风险','整改措施','运营单位',
	        '2026-12-01','pending_dispatch' FROM filing RETURNING id
	)
	INSERT INTO hazard_events(hazard_id,action,from_status,to_status,operator,version)
	SELECT id,'register','','pending_dispatch','登记席位',1 FROM hazard`)
	if err != nil {
		t.Fatal(err)
	}
	router := http.NewServeMux()
	New(pool).Register(router)
	for _, keyword := range []string{"F-DIRECT", "TEST"} {
		page := readAuditTest[testAuditPage](t, router, auditEndpoint+"?resource=hazards&q="+keyword, 200)
		if len(page.Items) != 1 || page.Items[0].Action != "register" || page.Items[0].ActionLabel != "登记隐患" ||
			page.Items[0].Title != "直接登记系统" || page.Items[0].Operator != "登记席位" {
			t.Errorf("按 %s 检索登记事件 = %+v", keyword, page.Items)
		}
	}
}

type testAuditItem struct {
	ID, Resource, ResourceID, Title, Action, ActionLabel, Operator, Href string
	OccurredAt                                                           time.Time
}

type testAuditPage struct {
	Items      []testAuditItem
	NextCursor string
}

// collectAuditPages 按游标读取真实事件并验证同时间排序不会重复丢失。
// 参数 t 为测试上下文，router 为实际业务处理器。
// 返回值 每个业务来源的已观察事件数。
func collectAuditPages(t *testing.T, router http.Handler) map[string]int {
	t.Helper()
	coverage := make(map[string]int)
	seen := make(map[string]bool)
	cursor, previous := "", ""
	for range 7 {
		query := "?limit=2"
		if cursor != "" {
			query += "&cursor=" + url.QueryEscape(cursor)
		}
		page := readAuditTest[testAuditPage](t, router, auditEndpoint+query, 200)
		for _, item := range page.Items {
			if seen[item.ID] || (previous != "" && item.ID >= previous) {
				t.Fatalf("同时间分页有重复或顺序错误: %s → %s", previous, item.ID)
			}
			if item.Operator != "隔离办理席位" || item.ActionLabel == "" || item.Title == "" {
				t.Fatalf("真实操作摘要字段缺失: %+v", item)
			}
			seen[item.ID] = true
			coverage[item.Resource]++
			previous = item.ID
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 10 || cursor != "" || coverage["reports"] != 3 {
		t.Fatalf("读取到 %d 个事件，测评事件 %d 个，游标 %q", len(seen), coverage["reports"], cursor)
	}
	return coverage
}

// verifyAuditLinksAndFilters 核对已保存的版本标题、阶段动作和站内对象链接。
// 参数 t 为测试上下文，router 为实际业务处理器。
// 返回值 无。
func verifyAuditLinksAndFilters(t *testing.T, router http.Handler) {
	t.Helper()
	filter := "?resource=publications&q=" + url.QueryEscape("版本标题")
	published := readAuditTest[testAuditPage](t, router, auditEndpoint+filter, 200)
	if len(published.Items) != 1 || published.Items[0].Action != "publish" ||
		published.Items[0].Href != "/monitor/publications?publication="+published.Items[0].ResourceID {
		t.Fatalf("发布事件筛选与链接错误: %+v", published)
	}
	payload := readAuditTest[struct{ Items []map[string]json.RawMessage }](t, router, auditEndpoint+filter, 200)
	for _, forbidden := range []string{"body", "comment", "ip", "credentials", "publisher", "attachmentId"} {
		if _, exists := payload.Items[0][forbidden]; exists {
			t.Errorf("返回了范围外字段 %s", forbidden)
		}
	}
	query := "?resource=filings&q=" + url.QueryEscape("审计备案")
	filing := readAuditTest[testAuditPage](t, router, auditEndpoint+query, 200)
	if len(filing.Items) != 1 || filing.Items[0].ActionLabel != "初审通过" ||
		filing.Items[0].Href != "/filings/"+filing.Items[0].ResourceID {
		t.Fatalf("备案初审语义或对象链接错误: %+v", filing)
	}
	alert := readAuditTest[testAuditPage](t, router, auditEndpoint+"?resource=alerts", 200)
	if len(alert.Items) != 1 || alert.Items[0].Href != "/alerts?alert="+alert.Items[0].ResourceID {
		t.Fatalf("预警事件详情链接不正确: %+v", alert)
	}
}

// readAuditTest 读取实际路由并核对响应状态及内容格式。
// 参数 t 为测试上下文，router、path、status 为目标路由、请求及期望状态。
// 返回值 已反序列化的 HTTP 响应。
func readAuditTest[T any](t *testing.T, router http.Handler, path string, status int) T {
	t.Helper()
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	var result T
	if recorder.Code != status {
		t.Fatalf("读取审计 %s 返回 %d，期望 %d: %s", path, recorder.Code, status, recorder.Body)
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// auditFixture 为审计读取和样例写入准备已迁移的隔离 schema。
// 参数 t 为测试上下文，测试数据库通过 GRADE_BUSINESS_TEST_DSN 显式配置。
// 返回值 独立数据模式中的真实数据库连接池。
func auditFixture(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if dsn == "" {
		t.Skip("GRADE_BUSINESS_TEST_DSN 未配置")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil || config.ConnConfig.Database != "grade_business_test" {
		t.Fatal("审计测试须使用隔离的 grade_business_test 数据库")
	}
	pool := auditSchema(t, config)
	if err := migrations.Apply(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

// auditSchema 建立当前用例专用 schema 并登记清理。
// 参数 t 为测试上下文，config 为已验证测试数据库配置。
// 返回值 当前测试 schema 的 PostgreSQL 连接池。
func auditSchema(t *testing.T, config *pgxpool.Config) *pgxpool.Pool {
	t.Helper()
	admin, err := pgxpool.NewWithConfig(t.Context(), config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("business_audit_%d_%d", os.Getpid(), time.Now().UnixNano())
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

const auditBusinessFixtures = `
WITH timer AS (SELECT '2026-06-01T02:00:00Z'::timestamptz AS stamp), filing AS (
	INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status)
	VALUES ('filing','审计备案-01','审计系统','隔离单位','隔离机构','2','approved') RETURNING id
), report AS (
	INSERT INTO business_cases(kind,number,assessment_org,filing_id,status,risk)
	SELECT 'report','测评-01','隔离机构',id,'returned','medium' FROM filing RETURNING id
), reviewed AS (
	INSERT INTO case_review_events(case_id,action,from_status,to_status,operator,version,occurred_at)
	SELECT id,'approve','pending_initial','pending_final','隔离办理席位',2,stamp FROM filing CROSS JOIN timer
	UNION ALL SELECT id,'return_for_correction','pending_final','returned','隔离办理席位',3,stamp FROM report CROSS JOIN timer
), application AS (
	INSERT INTO filing_applications(filing_id,application_type,filing_number,filing_version,reason,before_values,after_values)
	SELECT id,'change','审计备案-01',1,'审计示例','{"systemName":"旧"}','{"systemName":"新"}' FROM filing RETURNING id
), application_event AS (
	INSERT INTO filing_application_review_events(application_id,action,from_status,to_status,operator,version,occurred_at)
	SELECT id,'submit','draft','pending_initial','隔离办理席位',2,stamp FROM application CROSS JOIN timer
), implementation AS (
	INSERT INTO implementation_reports(filing_id,filing_number,system_name,organization_name,assessment_org,report_type,
		reporter,planned_start,planned_end,plan,created_by)
	SELECT id,'审计备案-01','审计系统','隔离单位','隔离机构','routine','隔离报备人',
		'2026-01-01','2026-01-02','隔离计划','隔离办理席位' FROM filing RETURNING id
), implementation_event AS (
	INSERT INTO implementation_events(report_id,version,action,from_status,to_status,operator,occurred_at)
	SELECT id,1,'create','','draft','隔离办理席位',stamp FROM implementation CROSS JOIN timer
), plan AS (
	INSERT INTO remediation_plans(report_id,deadline,risks,status,submission,submitted_at)
	SELECT id,'2026-03-01','[{}]','approved',1,stamp FROM report CROSS JOIN timer RETURNING id
), plan_event AS (
	INSERT INTO remediation_review_events(plan_id,action,from_status,to_status,operator,deadline,submission,version,occurred_at)
	SELECT id,'approve','pending_review','approved','隔离办理席位','2026-03-01',1,3,stamp FROM plan CROSS JOIN timer
), hazard AS (
	INSERT INTO hazard_items(plan_id,risk_index,severity,description,measure,responsibility,deadline,status)
	SELECT id,1,'medium','隔离业务说明','核对已有材料','隔离责任人','2026-03-01','pending_review'
	FROM plan RETURNING id
), hazard_event AS (
	INSERT INTO hazard_events(hazard_id,action,from_status,to_status,operator,comment,version,occurred_at)
	SELECT id,'submit','open','pending_review','隔离办理席位','隐患备注',2,stamp FROM hazard CROSS JOIN timer
), alert AS (
	INSERT INTO local_alerts(hazard_id,kind,severity,system_name,organization_name,deadline)
	SELECT id,'overdue','medium','审计系统','隔离单位','2026-03-01' FROM hazard RETURNING id
), alert_event AS (
	INSERT INTO local_alert_events(alert_id,version,action,to_status,operator,occurred_at)
	SELECT id,1,'create','pending','隔离办理席位',stamp FROM alert CROSS JOIN timer
), publication AS (
	INSERT INTO local_publications(status,published_version,published_at)
	SELECT 'published',1,stamp FROM timer RETURNING id
), publication_event AS (
	INSERT INTO publication_events(publication_id,version,action,status,category,title,publisher,body,pinned,operator,occurred_at)
	SELECT id,1,'publish','published','announcement','版本标题','隔离发布单位','私有模板内容',false,
		'隔离办理席位',stamp FROM publication CROSS JOIN timer
), file AS (
	INSERT INTO case_attachments(case_id,kind,file_name,file_key,size_bytes)
	SELECT id,'report_file','测试.docx',gen_random_uuid()::text,40 FROM report RETURNING id
), extraction AS (
	INSERT INTO report_extractions(attachment_id,fields,version)
	SELECT id,'[]',2 FROM file RETURNING attachment_id
), extraction_event AS (
	INSERT INTO report_extraction_reviews(attachment_id,version,changes,operator,confirmed_at)
	SELECT attachment_id,2,'[]','隔离办理席位',stamp FROM extraction CROSS JOIN timer RETURNING attachment_id
), knowledge AS (
	INSERT INTO knowledge_entries (title,category,source,body) VALUES ('依据','standard','隔离标准','内部依据') RETURNING id
), basis AS (
	INSERT INTO knowledge_revisions(entry_id,version,title,category,source,body,tags,updated_at,action)
	SELECT id,1,'依据','standard','隔离标准','内部依据','{}',stamp,'create' FROM knowledge CROSS JOIN timer RETURNING entry_id
)
INSERT INTO report_compliance_records
	(report_id,report_version,attachment_id,extraction_version,field,knowledge_id,knowledge_version,decision,comment,operator,created_at)
SELECT r.id,3,e.attachment_id,2,'{}',k.entry_id,1,'compliant','内部判断备注','隔离办理席位',stamp
FROM report r CROSS JOIN extraction_event e CROSS JOIN basis k CROSS JOIN timer
`
