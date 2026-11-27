package dashboard

import (
	"net/http"
	"testing"
)

// TestDashboardComparisonUsesRealWindows 核对同比环比按三个真实窗口统计，不使用估算值。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDashboardComparisonUsesRealWindows(t *testing.T) {
	db := dashboardTestPool(t)
	current := dashboardTestFiling(t, db, "本期备案", "本期单位", "approved", false)
	previous := dashboardTestFiling(t, db, "上期备案", "上期单位", "approved", false)
	yearAgo := dashboardTestFiling(t, db, "去年备案", "去年单位", "approved", false)
	// 三个备案分别落在当前窗口、上期窗口和去年同期窗口。
	dashboardExec(t, db, `UPDATE business_cases SET created_at=clock_timestamp() - $2::int * interval '1 day'
		WHERE id=$1::uuid`, current, 5)
	dashboardExec(t, db, `UPDATE business_cases SET created_at=clock_timestamp() - $2::int * interval '1 day'
		WHERE id=$1::uuid`, previous, 40)
	dashboardExec(t, db, `UPDATE business_cases SET created_at=clock_timestamp() - $2::int * interval '1 day'
		WHERE id=$1::uuid`, yearAgo, 380)

	// 隐患在三个时点的状态不同，用于验证存量重放。
	dashboardExec(t, db, `WITH today AS (SELECT (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date AS day)
		INSERT INTO hazard_items(registration_filing_id,ledger,title,source,discovered_on,severity,
			description,measure,responsibility,deadline,status,dispatched_at,retest_institution,
			created_at,closed_at)
		SELECT filing,'major_risk',title,'manual',day-3,severity,'说明','措施','责任',day+delta,
			status,clock_timestamp() - age * interval '1 day','复测机构',
			clock_timestamp() - age * interval '1 day',closed_at
		FROM (VALUES ($1::uuid,'本期未销项','high',5,'open',NULL::timestamptz,5),
			($1::uuid,'本期已销项','medium',8,'closed',clock_timestamp() - 3 * interval '1 day',5),
			($2::uuid,'上期未销项','high',40,'open',NULL::timestamptz,40),
			($3::uuid,'去年未销项','high',380,'open',NULL::timestamptz,380)
		) AS entries(filing,title,severity,delta,status,closed_at,age) CROSS JOIN today`,
		current, previous, yearAgo)

	// 预警同样分布在三个窗口。
	dashboardExec(t, db, `INSERT INTO local_alerts(hazard_id,kind,severity,system_name,organization_name,deadline,triggered_at)
		SELECT h.id,'high_risk',h.severity,'系统名','单位名',h.deadline,
			clock_timestamp() - CASE h.title WHEN '本期未销项' THEN 5 WHEN '上期未销项' THEN 40 ELSE 380 END
			* interval '1 day'
		FROM hazard_items h WHERE h.title IN ('本期未销项','上期未销项','去年未销项')`)

	mux := http.NewServeMux()
	Register(mux, db)
	var result struct {
		Comparison struct {
			WindowDays     int
			HasYearAgo     bool
			FilingsCreated struct {
				Current, Previous, YearAgo, Change int64
				Rate                               *int64
				Worse                              bool
			}
			OpenHazards struct {
				Current, Previous, YearAgo int64
				Worse                      bool
			}
			AlertsTriggered struct {
				Current, Previous, YearAgo int64
			}
			RiskIndex struct {
				Current, Previous, YearAgo int64
			}
			CompletionRate struct {
				Current, Previous, Change int64
				HasCurrent                bool
			}
			ScopeNote string
		}
	}
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?scope=all", 200, &result)
	comparison := result.Comparison
	if comparison.WindowDays != 30 {
		t.Fatalf("对比窗口天数 = %d，期望 30", comparison.WindowDays)
	}
	// 三个窗口各有一个新增备案，变化率为零。
	if comparison.FilingsCreated.Current != 1 || comparison.FilingsCreated.Previous != 1 ||
		comparison.FilingsCreated.YearAgo != 1 || comparison.FilingsCreated.Change != 0 {
		t.Fatalf("新增备案对比不符：%+v", comparison.FilingsCreated)
	}
	if comparison.FilingsCreated.Rate == nil || *comparison.FilingsCreated.Rate != 0 {
		t.Fatalf("新增备案变化率不符：%+v", comparison.FilingsCreated)
	}
	// 存量重放：较早登记的未销项会延续到更晚的时点。
	// 本期时点有 3 项未销项，上期时点 2 项，去年同期时点 1 项。
	if comparison.OpenHazards.Current != 3 || comparison.OpenHazards.Previous != 2 ||
		comparison.OpenHazards.YearAgo != 1 {
		t.Fatalf("未销项存量重放不符：%+v", comparison.OpenHazards)
	}
	// 本期预警 1 条，上期 1 条，去年 1 条。
	if comparison.AlertsTriggered.Current != 1 || comparison.AlertsTriggered.Previous != 1 ||
		comparison.AlertsTriggered.YearAgo != 1 {
		t.Fatalf("触发预警对比不符：%+v", comparison.AlertsTriggered)
	}
	// 综合风险指数按各时点的高风险数重算：本期 3 项为 120 封顶 100，上期 2 项为 80，去年 1 项为 40。
	if comparison.RiskIndex.Current != 100 || comparison.RiskIndex.Previous != 80 ||
		comparison.RiskIndex.YearAgo != 40 {
		t.Fatalf("综合风险指数对比不符：%+v", comparison.RiskIndex)
	}
	// 本期 1 销 3 未销 = 25%，上期与去年同期均无销项记录。
	if comparison.CompletionRate.Current != 25 || comparison.CompletionRate.Previous != 0 ||
		comparison.CompletionRate.Change != 25 || !comparison.CompletionRate.HasCurrent {
		t.Fatalf("整改完成率对比不符：%+v", comparison.CompletionRate)
	}
	if !comparison.HasYearAgo || comparison.ScopeNote == "" {
		t.Fatalf("去年同期可用性或口径说明缺失：%+v", comparison)
	}
}

// TestDashboardComparisonWindowParameter 核对对比窗口可按请求指定，且不破坏范围校验。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDashboardComparisonWindowParameter(t *testing.T) {
	db := dashboardTestPool(t)
	mux := http.NewServeMux()
	Register(mux, db)

	var result struct {
		Comparison struct{ WindowDays int }
	}
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?scope=all&window=7", 200, &result)
	if result.Comparison.WindowDays != 7 {
		t.Fatalf("指定窗口天数 = %d，期望 7", result.Comparison.WindowDays)
	}
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?scope=all&window=400", 400, &result)
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?scope=all&district=柯城区", 400, &result)
}

// TestDashboardComparisonReplaysHazardEvents 核对跨窗口复测、延期和派发记录。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDashboardComparisonReplaysHazardEvents(t *testing.T) {
	db := dashboardTestPool(t)
	filing := dashboardTestFiling(t, db, "复测系统", "复测单位", "approved", false)
	dashboardExec(t, db, `WITH clock AS (
		SELECT now() AS ts,(now() AT TIME ZONE 'Asia/Shanghai')::date AS day
	), registered AS (
		INSERT INTO hazard_items(registration_filing_id,ledger,title,source,discovered_on,severity,
			description,measure,responsibility,deadline,status,dispatched_at,retest_institution,
			created_at,version)
		SELECT $1::uuid,'major_risk',title,'manual',day-60,'medium',
			'说明','措施','责任',day+20,status,ts-dispatch_age*interval '1 day',
			'复测机构',ts-60*interval '1 day',version
		FROM clock CROSS JOIN (VALUES ('完成复测和延期','returned',60,4),
			('之后才派发','open',3,2),('本期才进入复测','pending_retest',60,2))
			AS cases(title,status,dispatch_age,version)
		RETURNING id,title
	)
	INSERT INTO hazard_events(hazard_id,action,from_status,to_status,operator,comment,version,occurred_at)
	SELECT r.id,e.action,e.before,e.after,'测试账号',
		CASE WHEN e.action='extend_deadline'
			THEN format('整改期限由 %s 延至 %s。延期说明',day-40,day+20) ELSE '处理说明' END,
		e.version,ts-e.age*interval '1 day'
	FROM clock CROSS JOIN registered r CROSS JOIN (
		VALUES ('完成复测和延期','submit','open','pending_retest',2,45),
		       ('完成复测和延期','retest_return','pending_retest','returned',3,10),
		       ('完成复测和延期','extend_deadline','returned','returned',4,5),
		       ('本期才进入复测','submit','open','pending_retest',2,8)
	) e(title,action,before,after,version,age)
	WHERE r.title=e.title`, filing)
	mux := http.NewServeMux()
	Register(mux, db)
	var result struct{ Comparison dashboardComparison }
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?scope=all", 200, &result)
	comparison := result.Comparison
	if comparison.RiskIndex.Current != 20 || comparison.RiskIndex.Previous != 60 ||
		comparison.RiskIndex.PreviousUnavailable ||
		comparison.OpenHazards.Current != 3 || comparison.OpenHazards.Previous != 2 {
		t.Fatalf("历史风险应按当时复测、期限与派发记录计算：%+v", comparison)
	}
}

// TestDashboardComparisonIncludesLastCalendarDay 验证结束日白天归入对应窗口。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDashboardComparisonIncludesLastCalendarDay(t *testing.T) {
	db := dashboardTestPool(t)
	previous := dashboardTestFiling(t, db, "上期末日备案", "上期单位", "approved", false)
	yearAgo := dashboardTestFiling(t, db, "同期末日备案", "去年单位", "approved", false)
	dashboardExec(t, db, `WITH calendar AS (
		SELECT (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date AS today
	) UPDATE business_cases SET created_at=(
		CASE WHEN id=$1::uuid THEN today - 7
			ELSE (today - interval '1 year')::date END + TIME '12:00'
		) AT TIME ZONE 'Asia/Shanghai'
	FROM calendar WHERE id IN ($1::uuid,$2::uuid)`, previous, yearAgo)

	mux := http.NewServeMux()
	Register(mux, db)
	var result struct{ Comparison dashboardComparison }
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?scope=all&window=7", 200, &result)
	filings := result.Comparison.FilingsCreated
	if filings.Current != 0 || filings.Previous != 1 || filings.YearAgo != 1 {
		t.Fatalf("窗口结束日的备案未正确统计：%+v", filings)
	}
}
