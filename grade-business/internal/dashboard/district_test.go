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
