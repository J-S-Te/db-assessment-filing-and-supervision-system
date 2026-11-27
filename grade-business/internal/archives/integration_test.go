package archives

import (
	"context"
	"encoding/base64"
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

const unitName = "江州医疗/中心"

// TestArchiveFindsRegisteredUnitBeforeFilingApproval 验证在册单位可建档并按信用代码查询。
// 参数 t 为真实隔离数据库和档案路由的测试上下文。
// 返回值 无。
func TestArchiveFindsRegisteredUnitBeforeFilingApproval(t *testing.T) {
	pool := archiveTestPool(t)
	const scope, currentName, code = "测试单位原名", "测试单位现名", "913310000000000018"
	archiveFixtureID(t, pool, `INSERT INTO directory_parties(kind,scope_name,name,code,district,classification)
	 VALUES ('unit',$1,'测试单位现名',$2,'椒江区','事业单位') RETURNING id::text`, scope, code)
	pending := insertArchiveFiling(t, pool, "F-ARCHIVE-PENDING", currentName, "2", "pending_initial", false)
	mux := http.NewServeMux()
	Register(mux, pool)
	path := "/api/v1/archives/units"
	for _, keyword := range []string{currentName, scope, code} {
		units := archiveGET[page[unitRow]](t, mux, path+"?q="+url.QueryEscape(keyword), 200)
		if len(units.Items) != 1 || units.Items[0].OrganizationName != scope || units.Items[0].SystemCount != 0 {
			t.Fatalf("搜索 %q 未返回唯一在册单位及零个已通过系统：%+v", keyword, units)
		}
	}
	key := base64.RawURLEncoding.EncodeToString([]byte(scope))
	detail := archiveGET[unitProfile](t, mux, path+"/"+key, 200)
	if len(detail.Systems.Items) != 0 || len(detail.Reports.Items) != 0 {
		t.Fatalf("待审备案提前进入关联档案：%+v", detail)
	}
	archiveGET[map[string]string](t, mux, "/api/v1/archives/systems/"+pending, 404)
	if _, err := pool.Exec(t.Context(), `UPDATE business_cases SET status='approved' WHERE id=$1`, pending); err != nil {
		t.Fatal(err)
	}
	report := insertArchiveReport(t, pool, pending, "R-ARCHIVE-ALIAS", "approved", "2026-04-01")
	units := archiveGET[page[unitRow]](t, mux, path+"?q="+url.QueryEscape("测试单位"), 200)
	if len(units.Items) != 1 || units.Items[0].SystemCount != 1 || units.Items[0].DirectoryName != currentName ||
		units.Items[0].CreditCode != code || units.Items[0].District != "椒江区" {
		t.Fatalf("历史备案使用当前名称时应合入唯一在册单位：%+v", units)
	}
	systems := archiveGET[page[systemRow]](t, mux, "/api/v1/archives/systems?q="+code, 200)
	if len(systems.Items) != 1 || systems.Items[0].ID != pending {
		t.Fatalf("按信用代码查询未找到终审通过的系统档案：%+v", systems)
	}
	detail = archiveGET[unitProfile](t, mux, path+"/"+key, 200)
	if detail.Unit.SystemCount != 1 || detail.Unit.ActiveSystemCount != 1 || len(detail.Systems.Items) != 1 ||
		len(detail.Reports.Items) != 1 || detail.Reports.Items[0].ID != report {
		t.Fatalf("终审通过后单位档案未同步系统及测评：%+v", detail)
	}
	oldKey := base64.RawURLEncoding.EncodeToString([]byte(currentName))
	legacy := archiveGET[unitProfile](t, mux, path+"/"+oldKey, 200)
	if legacy.Unit.Key != key || len(legacy.Systems.Items) != 1 {
		t.Fatalf("已使用历史名称形成的档案链接未解析到同一单位：%+v", legacy)
	}
}

type archiveFixtures struct {
	active, cancelled, other, pending string
	report, cancelledReport, plan     string
	open, closed                      string
}

// TestArchiveListsDirectHazardsWithoutAssessmentReport 验证已派发登记隐患归入唯一备案档案。
// 参数 t 为真实数据库集成测试上下文。
// 返回值 无。
func TestArchiveListsDirectHazardsWithoutAssessmentReport(t *testing.T) {
	pool := archiveTestPool(t)
	filing := insertArchiveFiling(t, pool, "F-DIRECT", unitName, "3", "approved", false)
	id := archiveFixtureID(t, pool, `INSERT INTO hazard_items
	 (registration_filing_id,ledger,title,source,discovered_on,severity,description,measure,responsibility,
	    deadline,dispatched_at,retest_institution,version)
	 VALUES ($1,'vulnerability','手工登记','scan','2026-11-22','high','配置异常','重新配置','整改组',
	    '2026-12-01',clock_timestamp(),'复测机构',2)
	 RETURNING id::text`, filing)
	archiveFixtureID(t, pool, `INSERT INTO hazard_items
	 (registration_filing_id,ledger,title,source,discovered_on,severity,description,measure,responsibility,deadline,status)
	 VALUES ($1,'major_risk','待派发','manual','2026-11-22','high','权限异常','收回权限','整改组','2026-12-01',
	    'pending_dispatch') RETURNING id::text`, filing)
	mux := http.NewServeMux()
	Register(mux, pool)
	profile := archiveGET[systemProfile](t, mux, "/api/v1/archives/systems/"+filing, 200)
	if len(profile.Hazards.Items) != 1 || profile.Hazards.Items[0].ID != id ||
		profile.Hazards.Items[0].PlanID != "" || profile.System.OpenHazardCount != 1 || len(profile.Reports.Items) != 0 {
		t.Fatalf("直接登记系统档案 = %+v", profile)
	}
	units := archiveGET[page[unitRow]](t, mux, "/api/v1/archives/units", 200)
	if len(units.Items) != 1 || units.Items[0].OpenHazardCount != 1 || units.Items[0].LatestReportAt != nil {
		t.Fatalf("直接登记单位档案 = %+v", units)
	}
}

// TestArchiveGroupingAndCurrentFilingProvenance 验证按原备案名称分组且不同单位及未通过备案的数据隔离。
// 参数 t 为真实数据库集成测试上下文。
// 返回值 无。
func TestArchiveGroupingAndCurrentFilingProvenance(t *testing.T) {
	pool := archiveTestPool(t)
	fixture := seedArchiveTestData(t, pool)
	mux := http.NewServeMux()
	Register(mux, pool)

	units := archiveGET[page[unitRow]](t, mux, "/api/v1/archives/units?q="+url.QueryEscape("江州医疗/"), 200)
	if len(units.Items) != 2 || units.Items[0].OrganizationName != unitName || units.Items[1].SystemCount != 1 {
		t.Fatalf("单位档案分组 = %+v", units.Items)
	}
	unit := units.Items[0]
	if unit.Key != base64.RawURLEncoding.EncodeToString([]byte(unitName)) || unit.SystemCount != 2 ||
		unit.ActiveSystemCount != 1 || unit.OpenHazardCount != 1 || unit.ClosedHazardCount != 1 ||
		unit.LatestReportAt == nil || unit.LatestReportAt.Format(time.DateOnly) != "2026-03-15" {
		t.Fatalf("单位档案摘要 = %+v", unit)
	}
	detail := archiveGET[unitProfile](t, mux, "/api/v1/archives/units/"+unit.Key, 200)
	assertUnitArchiveRelations(t, detail, fixture)
	other := archiveGET[systemProfile](t, mux, "/api/v1/archives/systems/"+fixture.other, 200)
	if other.System.OrganizationName == unitName || len(other.Reports.Items) != 1 ||
		len(other.Remediations.Items) != 0 || len(other.Hazards.Items) != 0 {
		t.Fatalf("同名系统档案串档 = %+v", other)
	}
	archiveGET[map[string]string](t, mux, "/api/v1/archives/systems/"+fixture.pending, 404)
	archiveGET[map[string]string](t, mux, "/api/v1/archives/units/"+base64.RawURLEncoding.EncodeToString([]byte("无备案单位")), 404)
}

// TestArchiveFiltersAndCursors 验证系统状态级别筛选、单位游标与每节独立翻页。
// 参数 t 为真实数据库集成测试上下文。
// 返回值 无。
func TestArchiveFiltersAndCursors(t *testing.T) {
	pool := archiveTestPool(t)
	fixture := seedArchiveTestData(t, pool)
	mux := http.NewServeMux()
	Register(mux, pool)
	units := archiveGET[page[unitRow]](t, mux, "/api/v1/archives/units?limit=1", 200)
	if len(units.Items) != 1 || units.NextCursor == "" {
		t.Fatalf("单位档案第一页 = %+v", units)
	}
	more := archiveGET[page[unitRow]](t, mux, "/api/v1/archives/units?limit=1&cursor="+units.NextCursor, 200)
	if len(more.Items) != 1 || more.Items[0].Key == units.Items[0].Key || more.NextCursor != "" {
		t.Fatalf("单位档案第二页 = %+v", more)
	}
	query := "?q=" + url.QueryEscape(unitName) + "&status=cancelled&level=3"
	systems := archiveGET[page[systemRow]](t, mux, "/api/v1/archives/systems"+query, 200)
	if len(systems.Items) != 1 || systems.Items[0].ID != fixture.cancelled {
		t.Fatalf("注销档案组合查询 = %+v", systems)
	}
	profile := archiveGET[systemProfile](t, mux, "/api/v1/archives/systems/"+fixture.cancelled, 200)
	if profile.System.Status != "cancelled" || profile.System.CancelledAt == nil || len(profile.Reports.Items) != 1 ||
		profile.Reports.Items[0].ID != fixture.cancelledReport {
		t.Fatalf("注销档案历史 = %+v", profile)
	}
	assertArchiveReportPages(t, mux, pool, fixture)
}

// TestArchiveRejectsInvalidParameters 验证档案接口拒绝重复参数、无效游标、未知字段及非法标识。
// 参数 t 为真实数据库集成测试上下文。
// 返回值 无。
func TestArchiveRejectsInvalidParameters(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, archiveTestPool(t))
	for _, query := range []string{"?q=a&q=b", "?cursor=%%%", "?limit=101", "?onlyDraft=true"} {
		archiveGET[map[string]string](t, mux, "/api/v1/archives/units"+query, 400)
	}
	for _, query := range []string{"?status=draft", "?level=6", "?cursor=not-a-page"} {
		archiveGET[map[string]string](t, mux, "/api/v1/archives/systems"+query, 400)
	}
	archiveGET[map[string]string](t, mux, "/api/v1/archives/systems/not-a-uuid", 404)
	archiveGET[map[string]string](t, mux, "/api/v1/archives/units/invalid-key", 404)
}

// assertUnitArchiveRelations 核对单位画像保留注销历史及整改链路。
// 参数 t 为测试上下文，detail 为单位快照，fixture 为独立的预期关联记录。
// 返回值 无。
func assertUnitArchiveRelations(t *testing.T, detail unitProfile, fixture archiveFixtures) {
	t.Helper()
	if len(detail.Systems.Items) != 2 || detail.Unit.SystemCount != 2 || len(detail.Reports.Items) != 2 ||
		detail.Reports.Items[0].ID != fixture.report || detail.Reports.Items[1].ID != fixture.cancelledReport ||
		len(detail.Remediations.Items) != 1 || detail.Remediations.Items[0].ID != fixture.plan ||
		len(detail.Hazards.Items) != 2 || detail.Hazards.Items[0].ID != fixture.open ||
		detail.Hazards.Items[1].ID != fixture.closed || detail.Hazards.Items[1].ClosedAt == nil {
		t.Fatalf("画像跨模块归属 = %+v", detail)
	}
}

// assertArchiveReportPages 核对一组系统的测评历史使用独立游标，其他分区仍可查看。
// 参数 t 为测试上下文，mux 为真实业务路由，pool 与 fixture 确定隔离样本。
// 返回值 无。
func assertArchiveReportPages(t *testing.T, mux *http.ServeMux, pool *pgxpool.Pool, fixture archiveFixtures) {
	t.Helper()
	for i := range 20 {
		insertArchiveReport(t, pool, fixture.active, fmt.Sprintf("追加测评-%02d", i), "returned", "2026-04-01")
	}
	path := "/api/v1/archives/systems/" + fixture.active
	first := archiveGET[systemProfile](t, mux, path, 200)
	if len(first.Reports.Items) != 20 || first.Reports.NextCursor == "" {
		t.Fatalf("测评历史分页 = %+v", first.Reports)
	}
	second := archiveGET[systemProfile](t, mux, path+"?reportsCursor="+first.Reports.NextCursor, 200)
	if len(second.Reports.Items) != 1 || second.Reports.Items[0].ID != fixture.report ||
		len(second.Remediations.Items) != 1 || len(second.Hazards.Items) != 2 {
		t.Fatalf("测评历史第二页 = %+v", second)
	}
}

// archiveGET 通过真实路由读取接口并核对响应。
// 参数 t 为测试上下文，mux 为接口路由，path 和 status 为请求及预期状态码。
// 返回值 已解码的公开响应数据。
func archiveGET[T any](t *testing.T, mux *http.ServeMux, path string, status int) T {
	t.Helper()
	var result T
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != status {
		t.Fatalf("%s 返回 %d，预期 %d：%s", path, response.Code, status, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// archiveTestPool 为档案测试建立真实 PostgreSQL 隔离模式并应用现有迁移。
// 参数 t 为管理隔离连接和模式清理的测试上下文。
// 返回值 仅读写当前档案测试模式的连接池。
func archiveTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if dsn == "" {
		t.Skip("未配置隔离的档案测试数据库")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil || config.ConnConfig.Database != "grade_business_test" {
		t.Fatal("GRADE_BUSINESS_TEST_DSN 须使用独立 grade_business_test 数据库")
	}
	control, err := pgxpool.NewWithConfig(t.Context(), config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(control.Close)
	schema := fmt.Sprintf("archive_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := control.Exec(t.Context(), "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := control.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Errorf("清理档案测试模式失败: %v", err)
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

// seedArchiveTestData 保存多单位、同名系统、注销和整改链路的独立测试记录。
// 参数 t 为测试上下文，pool 为专用模式连接池。
// 返回值 可独立核对的来源记录标识。
func seedArchiveTestData(t *testing.T, pool *pgxpool.Pool) archiveFixtures {
	t.Helper()
	var fixture archiveFixtures
	fixture.active = insertArchiveFiling(t, pool, "当前备案", unitName, "3", "approved", false)
	fixture.cancelled = insertArchiveFiling(t, pool, "历史备案", unitName, "3", "approved", true)
	fixture.other = insertArchiveFiling(t, pool, "相似单位备案", "江州医疗/中芯", "2", "approved", false)
	fixture.pending = insertArchiveFiling(t, pool, "待初审备案", unitName, "2", "pending_initial", false)
	fixture.report = insertArchiveReport(t, pool, fixture.active, "当前测评", "approved", "2026-03-15")
	fixture.cancelledReport = insertArchiveReport(t, pool, fixture.cancelled, "历史测评", "approved", "2026-01-01")
	insertArchiveReport(t, pool, fixture.active, "草稿测评", "draft", "")
	insertArchiveReport(t, pool, fixture.other, "他组测评", "approved", "2026-09-01")
	insertArchiveReport(t, pool, fixture.pending, "待初审备案测试测评", "approved", "2026-10-01")
	fixture.plan = insertArchivePlan(t, pool, fixture.report, "approved")
	insertArchivePlan(t, pool, fixture.report, "draft")
	fixture.closed = insertArchiveHazard(t, pool, fixture.plan, "closed", 1)
	fixture.open = insertArchiveHazard(t, pool, fixture.plan, "open", 2)
	return fixture
}

// insertArchiveFiling 插入同名系统的备案测试记录，按入参保留归属及生命周期。
// 参数 t 为测试上下文，pool 为隔离库，number、organization、level、status、cancelled 为备案条件。
// 返回值 测试备案公开标识。
func insertArchiveFiling(t *testing.T, pool *pgxpool.Pool, number, organization, level, status string, cancelled bool) string {
	t.Helper()
	return archiveFixtureID(t, pool, `INSERT INTO business_cases
	 (kind,number,system_name,organization_name,assessment_org,level,status,cancelled_at)
	 VALUES('filing',$1,'同名业务系统',$2,'测试机构',$3,$4,CASE WHEN $5 THEN clock_timestamp() END)
	 RETURNING id::text`, number, organization, level, status, cancelled)
}

// insertArchiveReport 保存按备案标识关联的真实格式测评测试记录。
// 参数 t 为测试上下文，pool 为隔离库，filing、number、status、submitted 为报告字段。
// 返回值 测评报告公开标识。
func insertArchiveReport(t *testing.T, pool *pgxpool.Pool, filing, number, status, submitted string) string {
	t.Helper()
	return archiveFixtureID(t, pool, `INSERT INTO business_cases
	 (kind,number,filing_id,assessment_org,risk,status,submitted_at)
	 VALUES('report',$1,$2,'报告机构','medium',$3,NULLIF($4,'')::timestamptz) RETURNING id::text`,
		number, filing, status, submitted)
}

// insertArchivePlan 保存测评关联的整改规划测试记录。
// 参数 t 为测试上下文，pool 为隔离库，report 为报告标识，status 为规划审核状态。
// 返回值 整改规划公开标识。
func insertArchivePlan(t *testing.T, pool *pgxpool.Pool, report, status string) string {
	t.Helper()
	return archiveFixtureID(t, pool, `INSERT INTO remediation_plans(report_id,deadline,risks,status,submission,submitted_at)
	 VALUES($1,'2026-12-01','[{}]'::jsonb,$2,CASE WHEN $2='draft' THEN 0 ELSE 1 END,
	   CASE WHEN $2<>'draft' THEN clock_timestamp() END) RETURNING id::text`, report, status)
}

// insertArchiveHazard 保存规划风险对应的待整改或已销项隐患。
// 参数 t 为测试上下文，pool 为隔离库，plan 为规划标识，status 与 index 为隐患状态和顺序。
// 返回值 隐患公开标识。
func insertArchiveHazard(t *testing.T, pool *pgxpool.Pool, plan, status string, index int) string {
	t.Helper()
	return archiveFixtureID(t, pool, `INSERT INTO hazard_items
	 (plan_id,risk_index,severity,description,measure,responsibility,deadline,status,closed_at)
	 VALUES($1,$2,'medium','核对现有权限配置','核对并验证','整改组','2026-12-01',$3,
	   CASE WHEN $3='closed' THEN clock_timestamp() END) RETURNING id::text`, plan, index, status)
}

// archiveFixtureID 执行隔离模式的测试插入语句并读取标识。
// 参数 t 为测试上下文，pool 为隔离库，query 与 args 为测试输入。
// 返回值 已保存测试记录的标识。
func archiveFixtureID(t *testing.T, pool *pgxpool.Pool, query string, args ...any) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(), query, args...).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
