package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"grade-business/internal/workflowreminders"
)

// TestDistrictResponseRanksAndScopes 检查逾期边界、从完整数据取区县和事项联动。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDistrictResponseRanksAndScopes(t *testing.T) {
	db := dashboardTestPool(t)
	west := dashboardTestFiling(t, db, "西区备案", "西区单位", "approved", false)
	east := dashboardTestFiling(t, db, "东区备案", "东区单位", "approved", false)
	unknown := dashboardTestFiling(t, db, "未归区备案", "待核对单位", "approved", false)
	retired := dashboardTestFiling(t, db, "已注销备案", "西区单位", "approved", true)
	dashboardExec(t, db, `INSERT INTO directory_parties(kind,scope_name,name,code,district) VALUES
		('unit','西区单位','西区单位','111111111111111111','西区'),
		('unit','东区单位','东区单位','222222222222222222','东区')`)
	dashboardExec(t, db, `WITH today AS (SELECT (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date AS day)
		INSERT INTO hazard_items(registration_filing_id,ledger,title,source,discovered_on,severity,
			description,measure,responsibility,deadline,status,dispatched_at,retest_institution)
		SELECT filing,'major_risk',title,'manual',day-3,severity,'说明','措施','责任',day+delta,
			status,CASE WHEN status<>'pending_dispatch' THEN now() END,
			CASE WHEN status<>'pending_dispatch' THEN '复测机构' END
		FROM (VALUES ($1::uuid,'西区待复测','high',-1,'pending_retest'),
			($1,'西区低风险逾期','low',-2,'open'),($2,'东区当天到期','high',0,'pending_review'),
			($2,'东区一般待办','medium',1,'open'),($3,'未归区逾期','medium',-1,'open'),
			($4,'已注销系统事项','high',-1,'open'),($1,'尚未派发','high',-1,'pending_dispatch')
		) AS entries(filing,title,severity,delta,status) CROSS JOIN today`, west, east, unknown, retired)
	dashboardExec(t, db, `INSERT INTO local_alerts(hazard_id,kind,severity,system_name,organization_name,deadline)
		SELECT h.id,'high_risk',h.severity,'历史显示名','历史归属名',h.deadline
		FROM hazard_items h WHERE h.title IN ('西区待复测','东区当天到期')`)
	dashboardExec(t, db, `UPDATE local_alerts SET status='processing' WHERE hazard_id=
		(SELECT id FROM hazard_items WHERE title='东区当天到期')`)
	westPlan := dashboardTestPlan(t, db, dashboardTestReport(t, db, west, "西区测评", "approved"))
	_ = dashboardTestPlan(t, db, dashboardTestReport(t, db, east, "东区测评", "approved"))
	dashboardExec(t, db, `UPDATE remediation_plans SET status='pending_review' WHERE id=$1`, westPlan)

	mux := http.NewServeMux()
	Register(mux, db)
	var ranked struct {
		Items []struct {
			District      string
			High, Overdue int
		}
		WithoutDistrict struct{ Systems, Overdue int }
	}
	readDistrictTest(t, mux, "/api/v1/dashboard/risk-by-district?limit=2", 200, &ranked)
	if len(ranked.Items) != 2 || ranked.Items[0].District != "西区" || ranked.Items[0].High != 1 ||
		ranked.Items[0].Overdue != 2 || ranked.Items[1].Overdue != 0 ||
		ranked.WithoutDistrict.Systems != 1 || ranked.WithoutDistrict.Overdue != 1 {
		t.Fatalf("区县排序和逾期不符：%+v", ranked)
	}
	var district struct {
		District *string
		Counts   struct {
			High, Overdue, PendingRetest, PendingReview, PendingPlanReview, PendingAlerts int
		}
		Tasks []struct {
			Title, Status, OrganizationName string
			Overdue                         bool
		}
		HighRiskSystems []systemRisk
		Alerts          []struct{ ID, OrganizationName string }
	}
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?scope=all", 200, &district)
	if district.District != nil || district.Counts.High != 2 || district.Counts.Overdue != 3 ||
		district.Counts.PendingRetest != 1 || district.Counts.PendingReview != 1 ||
		district.Counts.PendingPlanReview != 1 || district.Counts.PendingAlerts != 1 ||
		len(district.Tasks) != 4 || len(district.Alerts) != 2 || len(district.HighRiskSystems) != 2 {
		t.Fatalf("全域应包含已归区及未归区的有效在办事项：%+v", district)
	}
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?district=%E8%A5%BF%E5%8C%BA", 200, &district)
	if district.District == nil || *district.District != "西区" || district.Counts.Overdue != 2 ||
		district.Counts.PendingRetest != 1 || district.Counts.PendingReview != 0 ||
		district.Counts.PendingPlanReview != 1 || district.Counts.PendingAlerts != 1 ||
		len(district.Tasks) != 2 || district.Tasks[0].Title != "西区待复测" ||
		district.Tasks[0].OrganizationName != "西区单位" ||
		district.Tasks[0].Status != "pending_retest" || !district.Tasks[0].Overdue ||
		len(district.HighRiskSystems) != 1 || district.HighRiskSystems[0].FilingID != west ||
		len(district.Alerts) != 1 || district.Alerts[0].OrganizationName != "西区单位" {
		t.Fatalf("区县详情与业务范围不符：%+v", district)
	}
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?district=%E4%B8%9C%E5%8C%BA", 200, &district)
	if district.Counts.PendingReview != 1 || district.Counts.PendingPlanReview != 0 ||
		district.Counts.PendingAlerts != 0 || len(district.Alerts) != 1 {
		t.Fatalf("东区待办应只统计待监管复核项，处理中预警仍应可见：%+v", district)
	}
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?district=", 200, &district)
	if district.District == nil || *district.District != "" || len(district.Tasks) != 1 ||
		district.Tasks[0].Title != "未归区逾期" || len(district.Alerts) != 0 ||
		district.Counts.PendingRetest != 0 || district.Counts.PendingReview != 0 ||
		district.Counts.PendingPlanReview != 0 || district.Counts.PendingAlerts != 0 {
		t.Fatalf("未归区事项查询不符：%+v", district)
	}
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?district=a&district=b", 400, nil)
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?district=%00", 400, nil)
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?district=&scope=all", 400, nil)
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?scope=all&scope=all", 400, nil)
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?scope=other", 400, nil)
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response", 400, nil)
}

// TestDistrictSnapshotScopesAllDashboardPanels 校验不同区县的统计在同一范围内。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestDistrictSnapshotScopesAllDashboardPanels(t *testing.T) {
	db := dashboardTestPool(t)
	west := dashboardTestFiling(t, db, "西区备案", "西区单位", "approved", false)
	east := dashboardTestFiling(t, db, "东区备案", "东区单位", "approved", false)
	dashboardTestFiling(t, db, "待映射备案", "待归区单位", "draft", false)
	report := dashboardTestReport(t, db, west, "西区待审", "pending_final")
	dashboardExec(t, db, "UPDATE business_cases SET created_at=now()-interval '40 days' WHERE id=$1", report)
	dashboardExec(t, db, `INSERT INTO directory_parties(kind,scope_name,name,code,district) VALUES
		('unit','西区单位','西区单位','111111111111111111','西区'),
		('unit','东区单位','东区单位','222222222222222222','东区')`)
	dashboardExec(t, db, `INSERT INTO archive_system_assets(filing_id,kind,identifier,source)
		VALUES ($1,'ip','192.0.2.7','测试'),($2,'domain','east.example','测试')`, west, east)
	dashboardExec(t, db, `INSERT INTO archive_coverage_targets(unit_id,filing_id,system_name,reference,source)
		SELECT id,$1::uuid,'同名系统','西区在册','测试' FROM directory_parties WHERE district='西区'
		UNION ALL SELECT id,NULL,'待备案','东区在册','测试' FROM directory_parties WHERE district='东区'`, west)
	dashboardExec(t, db, `INSERT INTO hazard_items(registration_filing_id,ledger,title,source,discovered_on,severity,
		description,measure,responsibility,deadline,status,dispatched_at,retest_institution,closed_at)
		VALUES ($1,'major_risk','西区留存风险','manual',current_date,'medium','说明','措施','责任',
		   current_date+3,'open',now(),'测试机构',NULL),
		($1,'major_risk','西区历史销项','manual',current_date,'low','说明','措施','责任',
		   current_date+3,'closed',now(),'测试机构',now()),
		($2,'major_risk','东区未销项','manual',current_date,'high','说明','措施','责任',
		   current_date+3,'open',now(),'测试机构',NULL)`, west, east)
	dashboardExec(t, db, `INSERT INTO local_alerts(hazard_id,kind,severity,system_name,organization_name,deadline,status)
		SELECT id,'high_risk',severity,'东区系统','东区单位',deadline,'processing'
		FROM hazard_items WHERE title='东区未销项'`)
	mux := http.NewServeMux()
	Register(mux, db, workflowreminders.Windows{Report: 3})
	var response districtResponse
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?district=%E8%A5%BF%E5%8C%BA", 200, &response)
	if response.Statistics == nil || response.Overview == nil || response.Analytics == nil {
		t.Fatal("区县响应未提供整屏统计")
	}
	s, o := response.Statistics, response.Overview
	if s.Filings.Active != 1 || s.OpenHazards != (hazardStatistics{Medium: 1}) ||
		s.FilingCoverage != (filingCoverage{Eligible: 1, Covered: 1}) ||
		s.RegisteredAssets.IP != 1 || s.RegisteredAssets.Domain != 0 || len(s.Trend) != 30 ||
		o.FilingTotal != 1 || o.ReportFinal != 1 || o.HazardTotal != 2 || o.HazardClosed != 1 ||
		o.AlertCounts.Total != 0 || o.ReviewDeadlines == nil || o.ReviewDeadlines.Overdue != 1 ||
		response.Analytics.CommonIssues.Unclassified != 2 {
		t.Fatalf("西区统计混入其他范围或缺少数据：统计 %+v，概览 %+v", s, o)
	}
	var unknown districtResponse
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?district=", 200, &unknown)
	if unknown.Overview == nil || unknown.Overview.FilingTotal != 1 || unknown.Overview.HazardTotal != 0 {
		t.Fatalf("未归区统计不符：%+v", unknown.Overview)
	}
	var all districtResponse
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?scope=all", 200, &all)
	if all.Statistics != nil || all.Overview != nil || all.Analytics != nil {
		t.Fatal("全域处置接口应继续只返回处置数据")
	}
}

// readDistrictTest 核对区县读取的状态并解码成功响应。
// 参数 t 为测试上下文，mux 为真实业务路由，path 和 status 为预期请求及状态，target 为接收结构。
// 返回值 无。
func readDistrictTest(t *testing.T, mux http.Handler, path string, status int, target any) {
	t.Helper()
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
	if res.Code != status {
		t.Fatalf("%s 状态 = %d，正文 = %s", path, res.Code, res.Body)
	}
	if target != nil {
		if err := json.Unmarshal(res.Body.Bytes(), target); err != nil {
			t.Fatal(err)
		}
	}
}
