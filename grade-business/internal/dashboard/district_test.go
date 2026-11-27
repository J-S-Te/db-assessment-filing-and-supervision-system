package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDashboardDistrictsUseDirectory 核对区县来自真实名录，缺失及待派发按业务语义处理。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDashboardDistrictsUseDirectory(t *testing.T) {
	db := dashboardTestPool(t)
	first := dashboardTestFiling(t, db, "区县一", "同名单位", "approved", false)
	second := dashboardTestFiling(t, db, "区县二", "历史单位", "approved", false)
	seedDashboardAssessments(t, db, first, second)
	dashboardExec(t, db, `INSERT INTO directory_parties (kind,scope_name,name,code,district)
		VALUES ('unit','同名单位','现单位名','123456789012345678','东城区')`)
	mux := http.NewServeMux()
	Register(mux, db)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/risk-by-district", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("区县统计响应 = %d %s", response.Code, response.Body)
	}
	var result struct {
		Items []struct {
			District                   string
			Systems, High, Medium, Low int64
		}
		WithoutDistrict struct{ Systems, High, Medium, Low int64 }
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].District != "东城区" ||
		result.Items[0].Systems != 1 || result.Items[0].High != 2 || result.WithoutDistrict.High != 1 {
		t.Fatalf("区县统计与备案归属不符：%+v", result)
	}
}

// TestDashboardDistrictUsesCanonicalNames 核对同一区县的编码与全称合并，点击全称读取完整明细。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDashboardDistrictUsesCanonicalNames(t *testing.T) {
	db := dashboardTestPool(t)
	coded := dashboardTestFiling(t, db, "编码备案", "编码单位", "approved", false)
	named := dashboardTestFiling(t, db, "名称备案", "名称单位", "approved", false)
	seedDashboardAssessments(t, db, coded, named)
	dashboardExec(t, db, `INSERT INTO directory_parties (kind,scope_name,name,code,district)
		VALUES ('unit','编码单位','编码单位','123456789012345678','330802'),
		       ('unit','名称单位','名称单位','223456789012345678','柯城区')`)
	mux := http.NewServeMux()
	Register(mux, db)
	var ranking districtRiskSummary
	readDistrictTest(t, mux, "/api/v1/dashboard/risk-by-district", 200, &ranking)
	if len(ranking.Items) != 1 || ranking.Items[0].District != "柯城区" || ranking.Items[0].Systems != 2 {
		t.Fatalf("同一区县的编码和全称应合并：%+v", ranking)
	}
	var detail districtResponse
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?district=柯城区", 200, &detail)
	if detail.District == nil || *detail.District != "柯城区" ||
		detail.Counts.Systems != ranking.Items[0].Systems || detail.Counts.High != ranking.Items[0].High {
		t.Fatalf("区县排行与选中区县明细不一致：%+v", detail)
	}
	var empty districtResponse
	readDistrictTest(t, mux, "/api/v1/dashboard/district-response?district=衢江区", 200, &empty)
	if empty.District == nil || *empty.District != "衢江区" || len(empty.Tasks) != 0 ||
		empty.Overview == nil || empty.Overview.FilingTotal != 0 || empty.Comparison.CompletionRate.HasCurrent {
		t.Fatalf("空区县应返回该区县的空记录：%+v", empty)
	}
}
