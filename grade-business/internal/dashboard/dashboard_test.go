package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/migrations"
)

// TestDashboardStatisticsUseRealBusinessRecords 验证归并同名系统时按备案标识区分并以实际首轮结论统计。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDashboardStatisticsUseRealBusinessRecords(t *testing.T) {
	db := dashboardTestPool(t)
	first := dashboardTestFiling(t, db, "有效备案一", "同名单位", "approved", false)
	second := dashboardTestFiling(t, db, "有效备案二", "同名单位", "approved", false)
	dashboardTestFiling(t, db, "已注销备案", "历史单位", "approved", true)
	dashboardTestFiling(t, db, "待审核备案", "其他单位", "pending_final", false)
	dashboardTestFiling(t, db, "退回备案", "其他单位", "returned", false)
	dashboardTestFiling(t, db, "草稿备案", "其他单位", "draft", false)
	seedDashboardAssessments(t, db, first, second)

	mux := http.NewServeMux()
	Register(mux, db)
	result := dashboardRequest(t, mux, "", 200)
	if result.Filings != (filingStatistics{Active: 2, Cancelled: 1, UnderReview: 1, Returned: 1, Draft: 1, ActiveOrganizations: 1}) ||
		result.Hazards != (hazardStatistics{High: 4, Medium: 1}) ||
		result.ReportFirstReview != (firstReviewStatistics{Approved: 1, Decided: 2}) {
		t.Fatalf("驾驶舱统计与事实不符：%+v", result)
	}
	if len(result.HighRiskSystems) != 2 || result.HighRiskSystems[0].FilingID != first ||
		result.HighRiskSystems[0].Count != 2 || result.HighRiskSystems[1].FilingID != second ||
		result.HighRiskSystems[1].Count != 1 || result.HighRiskSystems[0].SystemName != result.HighRiskSystems[1].SystemName {
		t.Fatalf("未销项高风险系统排名 = %+v", result.HighRiskSystems)
	}
}

// TestDashboardCountsDispatchedDirectHazards 验证已派发的直接登记隐患进入备案风险统计。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDashboardCountsDispatchedDirectHazards(t *testing.T) {
	db := dashboardTestPool(t)
	filing := dashboardTestFiling(t, db, "直接登记备案", "运营单位", "approved", false)
	dashboardExec(t, db, `INSERT INTO hazard_items
	  (registration_filing_id,ledger,title,source,discovered_on,severity,description,measure,responsibility,
	     deadline,status,dispatched_at,retest_institution)
	  VALUES ($1,'major_risk','已派发','manual','2026-11-22','high','配置异常','重新配置','整改组',
	      '2026-12-01','open',clock_timestamp(),'复测机构'),
	    ($1,'major_risk','待派发','scan','2026-11-22','high','权限异常','收回权限','整改组',
	      '2026-12-01','pending_dispatch',NULL,NULL)`, filing)
	mux := http.NewServeMux()
	Register(mux, db)
	result := dashboardRequest(t, mux, "", 200)
	if result.Hazards != (hazardStatistics{High: 1}) || result.OpenHazards != (hazardStatistics{High: 1}) ||
		len(result.HighRiskSystems) != 1 || result.HighRiskSystems[0].FilingID != filing ||
		result.HighRiskSystems[0].Count != 1 {
		t.Fatalf("已派发直接登记隐患统计 = %+v，风险系统 = %+v", result.Hazards, result.HighRiskSystems)
	}
}

// TestDashboardStatisticsKeepEmptyDataExplicit 验证空库保留零分母及空榜单，并拒绝未知筛选。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDashboardStatisticsKeepEmptyDataExplicit(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, dashboardTestPool(t))
	result := dashboardRequest(t, mux, "", 200)
	if result.Filings != (filingStatistics{}) || result.Hazards != (hazardStatistics{}) ||
		result.ReportFirstReview != (firstReviewStatistics{}) || result.HighRiskSystems == nil || len(result.HighRiskSystems) != 0 {
		t.Fatalf("空业务库返回 = %+v", result)
	}
	dashboardRequest(t, mux, "?q=unexpected", 400)
}

// TestDashboardTrendUsesCalendarDays 验证趋势按北京时间归档并保留无事件日期。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDashboardTrendUsesCalendarDays(t *testing.T) {
	db := dashboardTestPool(t)
	var today string
	if err := db.QueryRow(t.Context(), `SELECT ((statement_timestamp() AT TIME ZONE 'Asia/Shanghai')::date)::text`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	day, err := time.Parse(time.DateOnly, today)
	if err != nil {
		t.Fatal(err)
	}
	seedDashboardTrend(t, db, today)
	mux := http.NewServeMux()
	Register(mux, db)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, statisticsPath, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("驾驶舱返回 %d：%s", response.Code, response.Body.String())
	}
	type sample struct {
		Date            string `json:"date"`
		FilingsCreated  int64  `json:"filingsCreated"`
		ReportsCreated  int64  `json:"reportsCreated"`
		AlertsTriggered int64  `json:"alertsTriggered"`
		HazardsClosed   int64  `json:"hazardsClosed"`
	}
	var body struct {
		UpdatedAt   time.Time        `json:"updatedAt"`
		OpenHazards hazardStatistics `json:"openHazards"`
		Trend       []sample         `json:"trend"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Trend) != 30 || body.UpdatedAt.IsZero() || body.OpenHazards != (hazardStatistics{Low: 1}) {
		t.Fatalf("当前风险和趋势基线 = %+v", body)
	}
	for index, got := range body.Trend {
		want := sample{Date: day.AddDate(0, 0, index-29).Format(time.DateOnly)}
		switch index {
		case 0:
			want.FilingsCreated = 1
		case 19:
			want.ReportsCreated, want.AlertsTriggered, want.HazardsClosed = 1, 1, 1
		}
		if got != want {
			t.Fatalf("第 %d 天实际 = %+v，期望 = %+v", index, got, want)
		}
	}
}

// seedDashboardTrend 保存跨时区日期边界、窗口边界及风险状态的测试事实。
// 参数 t 为测试上下文，db 为隔离库，today 为数据库的北京时间日期。
// 返回值 无。
func seedDashboardTrend(t *testing.T, db *pgxpool.Pool, today string) {
	t.Helper()
	first := dashboardTestFiling(t, db, "窗口内备案", "测试单位", "approved", false)
	old := dashboardTestFiling(t, db, "窗口外备案", "测试单位", "approved", false)
	report := dashboardTestReport(t, db, first, "窗口内报告", "approved")
	future := dashboardTestReport(t, db, first, "当前时刻以后", "approved")
	plan := dashboardTestPlan(t, db, report)
	dashboardExec(t, db, `UPDATE business_cases SET created_at = (CASE
	  WHEN id=$2 THEN ($1::text::date-29)::timestamp WHEN id=$3 THEN ($1::text::date-30)::timestamp
	  WHEN id=$4 THEN ($1::text::date-10)+time '00:30' ELSE ($1::text::date+1)::timestamp
	  END) AT TIME ZONE 'Asia/Shanghai' WHERE id IN ($2,$3,$4,$5)`, today, first, old, report, future)
	closed := dashboardRowID(t, db, `INSERT INTO hazard_items
	  (plan_id,risk_index,severity,description,measure,responsibility,deadline,status,closed_at) VALUES
	  ($1,1,'high','已销项','整改','测试','2026-12-01','closed',
	   (($2::text::date-10)+time '00:30') AT TIME ZONE 'Asia/Shanghai') RETURNING id::text`, plan, today)
	dashboardExec(t, db, `INSERT INTO hazard_items
	  (plan_id,risk_index,severity,description,measure,responsibility,deadline) VALUES
	  ($1,2,'low','在办隐患','整改','测试','2026-12-01')`, plan)
	dashboardExec(t, db, `INSERT INTO local_alerts
	  (hazard_id,kind,severity,system_name,organization_name,deadline,triggered_at) VALUES
	  ($1,'high_risk','high','测试系统','测试单位','2026-12-01',
	   (($2::text::date-10)+time '00:30') AT TIME ZONE 'Asia/Shanghai')`, closed, today)
}

// dashboardRequest 调用注册后的驾驶舱读取接口并解码统计结构。
// 参数 t 为测试上下文，mux 为路由，query 为原始查询参数，status 为期望状态码。
// 返回值 接口统计数据。
func dashboardRequest(t *testing.T, mux *http.ServeMux, query string, status int) statistics {
	t.Helper()
	var result statistics
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, statisticsPath+query, nil))
	if response.Code != status {
		t.Fatalf("驾驶舱返回 %d，预期 %d：%s", response.Code, status, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// seedDashboardAssessments 保存首轮通过、返工后通过、在审及多类隐患测试数据。
// 参数 t 为测试上下文，db 为隔离库，first 和 second 是两个独立备案标识。
// 返回值 无。
func seedDashboardAssessments(t *testing.T, db *pgxpool.Pool, first, second string) {
	t.Helper()
	passed := dashboardTestReport(t, db, first, "首轮通过", "approved")
	corrected := dashboardTestReport(t, db, second, "补正后通过", "approved")
	reviewing := dashboardTestReport(t, db, second, "仍待终审", "pending_final")
	dashboardExec(t, db, `INSERT INTO case_review_events(case_id,action,from_status,to_status,operator,version) VALUES
	  ($1,'approve','pending_initial','pending_final','测试',2),($1,'approve','pending_final','approved','测试',3),
	  ($2,'return_for_correction','pending_initial','returned','测试',2),
	  ($2,'approve','pending_final','approved','测试',5),($3,'approve','pending_initial','pending_final','测试',2)`,
		passed, corrected, reviewing)
	plan := dashboardTestPlan(t, db, passed)
	otherPlan := dashboardTestPlan(t, db, corrected)
	dashboardExec(t, db, `INSERT INTO hazard_items
	  (plan_id,risk_index,severity,description,measure,responsibility,deadline,status,closed_at) VALUES
	  ($1,1,'high','高风险一','整改','测试','2026-12-01','open',NULL),
	  ($1,2,'high','高风险二','整改','测试','2026-12-01','pending_review',NULL),
	  ($1,3,'high','已销项','整改','测试','2026-12-01','closed',clock_timestamp()),
	  ($1,4,'medium','中风险','整改','测试','2026-12-01','open',NULL),
	  ($2,1,'high','另一系统','整改','测试','2026-12-01','returned',NULL)`, plan, otherPlan)
}

// dashboardTestFiling 保存需要单独统计的备案测试件。
// 参数 t 为测试上下文，db 为隔离库，number、organization、status、cancelled 为备案业务字段。
// 返回值 新建备案标识。
func dashboardTestFiling(t *testing.T, db *pgxpool.Pool, number, organization, status string, cancelled bool) string {
	t.Helper()
	return dashboardRowID(t, db, `INSERT INTO business_cases
	  (kind,number,system_name,organization_name,assessment_org,level,status,cancelled_at)
	  VALUES('filing',$1,'同名系统',$2,'测试机构','3',$3,CASE WHEN $4 THEN clock_timestamp() END)
	  RETURNING id::text`, number, organization, status, cancelled)
}

// dashboardTestReport 保存关联备案的测评测试件。
// 参数 t 为测试上下文，db 为隔离库，filing、number、status 为测评报告业务字段。
// 返回值 新建测评报告标识。
func dashboardTestReport(t *testing.T, db *pgxpool.Pool, filing, number, status string) string {
	t.Helper()
	return dashboardRowID(t, db, `INSERT INTO business_cases
	  (kind,number,assessment_org,filing_id,risk,status) VALUES ('report',$1,'测试机构',$2,'high',$3)
	  RETURNING id::text`, number, filing, status)
}

// dashboardTestPlan 保存用于区分隐患系统归属的已批准整改计划。
// 参数 t 为测试上下文，db 为隔离库，report 为关联测评标识。
// 返回值 新建计划标识。
func dashboardTestPlan(t *testing.T, db *pgxpool.Pool, report string) string {
	t.Helper()
	return dashboardRowID(t, db, `INSERT INTO remediation_plans(report_id,deadline,risks,status,submission,submitted_at)
	  VALUES($1,'2026-12-01','[{}]'::jsonb,'approved',1,clock_timestamp()) RETURNING id::text`, report)
}

// dashboardRowID 插入隔离测试记录并返回标识。
// 参数 t 为测试上下文，db 为隔离库，query 和 args 为测试输入。
// 返回值 已保存记录的公开标识。
func dashboardRowID(t *testing.T, db *pgxpool.Pool, query string, args ...any) string {
	t.Helper()
	var id string
	if err := db.QueryRow(t.Context(), query, args...).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// dashboardExec 在隔离库插入与测试案例相关的业务记录。
// 参数 t 为测试上下文，db 为隔离库，query 和 args 为测试输入。
// 返回值 无。
func dashboardExec(t *testing.T, db *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

// dashboardTestPool 为驾驶舱集成测试创建独立数据库模式。
// 参数 t 为持有模式及连接生命周期的测试上下文。
// 返回值 只访问当前测试模式的连接池。
func dashboardTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if dsn == "" {
		t.Skip("未配置驾驶舱隔离测试数据库")
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
	schema := fmt.Sprintf("dashboard_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	dashboardExec(t, control, "CREATE SCHEMA "+identifier)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := control.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Errorf("清理驾驶舱测试模式失败：%v", err)
		}
	})
	config.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	return db
}
