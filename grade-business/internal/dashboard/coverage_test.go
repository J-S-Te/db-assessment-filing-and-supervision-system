package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDashboardReportsOnlyRegisteredCoverage 验证分母来自明确在册对象，注销及无关联对象均不算覆盖。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestDashboardReportsOnlyRegisteredCoverage(t *testing.T) {
	db := dashboardTestPool(t)
	mux := http.NewServeMux()
	Register(mux, db)
	covered := dashboardTestFiling(t, db, "F-COVERED", "登记单位", "approved", false)
	cancelled := dashboardTestFiling(t, db, "F-HISTORY", "登记单位", "approved", true)
	dashboardTestFiling(t, db, "F-OUTSIDE", "登记单位", "approved", false)
	assertDashboardCoverage(t, mux, 0, 0, 0)
	dashboardExec(t, db, `INSERT INTO directory_parties(kind,scope_name,name,code,district)
	 VALUES ('unit','登记单位','登记单位','913501001234567890','测试区')`)
	dashboardExec(t, db, `INSERT INTO archive_coverage_targets(unit_id,filing_id,system_name,reference,source,active)
	 SELECT id,$1::uuid,'同名系统','scope-1','普查清册',true FROM directory_parties
	 UNION ALL SELECT id,NULL,'待备案','scope-2','普查清册',true FROM directory_parties
	 UNION ALL SELECT id,$2::uuid,'同名系统','scope-3','普查清册',true FROM directory_parties
	 UNION ALL SELECT id,NULL,'已移除','scope-4','普查清册',false FROM directory_parties`, covered, cancelled)
	dashboardExec(t, db, `INSERT INTO archive_system_assets(filing_id,kind,identifier,source,active)
	 VALUES ($1,'ip','192.0.2.10','盘点表',true),($1,'ip','192.0.2.11','盘点表',false),
	        ($2,'ip','192.0.2.12','历史盘点表',true)`, covered, cancelled)
	assertDashboardCoverage(t, mux, 1, 3, 1)
}

// assertDashboardCoverage 通过真实 HTTP 读取覆盖口径与资产数量并核对空样本。
// 参数 t 为测试上下文，mux 为大屏 HTTP 路由，covered、eligible 和 ip 为应有的业务计数。
// 返回值 无。
func assertDashboardCoverage(t *testing.T, mux http.Handler, covered, eligible, ip int) {
	t.Helper()
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, statisticsPath, nil))
	var result struct {
		Coverage *struct {
			Covered  int
			Eligible int
		} `json:"filingCoverage"`
		Districts []struct {
			District string
			Covered  int
			Eligible int
		} `json:"filingCoverageByDistrict"`
		Assets *struct{ IP int } `json:"registeredAssets"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil {
		t.Fatalf("驾驶舱响应 = %d %s", response.Code, response.Body.String())
	}
	if result.Coverage == nil || result.Coverage.Covered != covered || result.Coverage.Eligible != eligible ||
		result.Assets == nil || result.Assets.IP != ip {
		t.Fatalf("覆盖和资产口径 = %+v，期望已覆盖 %d、应备案 %d、IP %d", result, covered, eligible, ip)
	}
	if result.Districts == nil || (eligible == 0 && len(result.Districts) != 0) ||
		(eligible > 0 && (len(result.Districts) != 1 || result.Districts[0].District != "测试区" ||
			result.Districts[0].Covered != covered || result.Districts[0].Eligible != eligible)) {
		t.Fatalf("区县覆盖口径 = %+v", result.Districts)
	}
}
