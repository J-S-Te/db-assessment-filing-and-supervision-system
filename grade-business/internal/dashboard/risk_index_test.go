package dashboard

import (
	"net/http"
	"testing"
)

// TestDistrictRiskIndexAndDutySummary 核对综合风险指数口径与当前范围待办汇总。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDistrictRiskIndexAndDutySummary(t *testing.T) {
	db := dashboardTestPool(t)
	west := dashboardTestFiling(t, db, "西区备案", "西区单位", "approved", false)
	east := dashboardTestFiling(t, db, "东区备案", "东区单位", "approved", false)
	dashboardExec(t, db, `INSERT INTO directory_parties(kind,scope_name,name,code,district) VALUES
		('unit','西区单位','西区单位','111111111111111111','西区'),
		('unit','东区单位','东区单位','222222222222222222','东区')`)
	// 西区：1 项高风险待复测且逾期 1 项，加 1 项逾期中等风险。
	// 东区：1 项高风险待监管复核，不逾期。
	dashboardExec(t, db, `WITH today AS (SELECT (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date AS day)
		INSERT INTO hazard_items(registration_filing_id,ledger,title,source,discovered_on,severity,
			description,measure,responsibility,deadline,status,dispatched_at,retest_institution)
		SELECT filing,'major_risk',title,'manual',day-3,severity,'说明','措施','责任',day+delta,
			status,CASE WHEN status<>'pending_dispatch' THEN now() END,
			CASE WHEN status<>'pending_dispatch' THEN '复测机构' END
		FROM (VALUES ($1::uuid,'西区高风险待复测','high',-1,'pending_retest'),
			($1,'西区逾期中风险','medium',-1,'open'),
			($2,'东区高风险待复核','high',3,'pending_review')
		) AS entries(filing,title,severity,delta,status) CROSS JOIN today`, west, east)
	dashboardExec(t, db, `INSERT INTO local_alerts(hazard_id,kind,severity,system_name,organization_name,deadline)
		SELECT h.id,'high_risk',h.severity,'系统名','单位名',h.deadline FROM hazard_items h WHERE h.title='西区高风险待复测'`)
	westPlan := dashboardTestPlan(t, db, dashboardTestReport(t, db, west, "西区测评", "approved"))
	dashboardExec(t, db, `UPDATE remediation_plans SET status='pending_review' WHERE id=$1`, westPlan)

	mux := http.NewServeMux()
	Register(mux, db)

	var ranked struct {
		Items []struct {
			District                     string
			Overdue, High, PendingRetest int
			RiskIndex                    int
			RiskLevel                    string
		}
	}
	readDistrictTest(t, mux, "/api/v1/dashboard/risk-by-district?limit=10", 200, &ranked)
	if len(ranked.Items) != 2 {
		t.Fatalf("区县数量 = %d，期望 2：%+v", len(ranked.Items), ranked)
	}
	// 西区加权分 = 1×40 + 1×40 + 1×20 = 100，指数 100，分级 high。
	if ranked.Items[0].District != "西区" || ranked.Items[0].RiskIndex != 100 ||
		ranked.Items[0].RiskLevel != "high" || ranked.Items[0].PendingRetest != 1 {
		t.Fatalf("西区综合风险指数不符：%+v", ranked.Items[0])
	}
	// 东区加权分 = 0×40 + 1×40 + 0×20 = 40，指数 40，分级 mid。
	if ranked.Items[1].District != "东区" || ranked.Items[1].RiskIndex != 40 ||
		ranked.Items[1].RiskLevel != "mid" {
		t.Fatalf("东区综合风险指数不符：%+v", ranked.Items[1])
	}

	var district struct {
		Risk struct {
			Index, Score int
			Level        string
			Weights      string
		}
		Duty struct {
			Total, OverdueHazards, PendingReview, PendingAlerts, PendingPlanReview int
			TopDistrict                                                            string
			TopDistrictIndex                                                       int
			Conclusion                                                             string
		}
	}
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?scope=all", 200, &district)
	// 全域加权分 = 2×40 + 2×40 + 1×20 = 180，指数封顶 100。
	if district.Risk.Index != 100 || district.Risk.Score != 180 || district.Risk.Level != "high" ||
		district.Risk.Weights == "" {
		t.Fatalf("全域综合风险指数不符：%+v", district.Risk)
	}
	// 待办 = 2 项逾期 + 1 项待复核 + 1 项待处理预警 + 1 项待审核整改规划 = 5。
	if district.Duty.Total != 5 || district.Duty.OverdueHazards != 2 || district.Duty.PendingReview != 1 ||
		district.Duty.PendingAlerts != 1 || district.Duty.PendingPlanReview != 1 {
		t.Fatalf("当前范围待办汇总不符：%+v", district.Duty)
	}
	if district.Duty.TopDistrict != "西区" || district.Duty.TopDistrictIndex != 100 ||
		district.Duty.Conclusion == "" {
		t.Fatalf("最重区县与结论不符：%+v", district.Duty)
	}
}
