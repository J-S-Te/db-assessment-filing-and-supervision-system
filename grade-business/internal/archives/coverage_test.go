package archives

import (
	"fmt"
	"net/http"
	"testing"
)

// TestCoverageRetainsUnfiledTargetsAndChecksFilingOwnership 验证分母可先登记，挂接须满足真实单位与系统归属。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestCoverageRetainsUnfiledTargetsAndChecksFilingOwnership(t *testing.T) {
	db := archiveTestPool(t)
	unit := archiveFixtureID(t, db, `INSERT INTO directory_parties(kind,scope_name,name,code,district)
	 VALUES('unit','范围甲','范围甲','913501001234567890','测试区') RETURNING id::text`)
	filing := insertArchiveFiling(t, db, "F-SCOPE", "范围甲", "3", "approved", false)
	unrelated := insertArchiveFiling(t, db, "F-OTHER", "范围乙", "3", "approved", false)
	mux := http.NewServeMux()
	Register(mux, db)
	path := "/api/v1/archives/coverage-targets"
	result := inventoryRequest(t, mux, http.MethodPost, path, fmt.Sprintf(
		`{"unitId":%q,"systemName":"同名业务系统","reference":"范围-001","source":"普查清册"}`, unit), "", 201)
	id, _ := result["id"].(string)
	if id == "" || result["covered"] != false || result["filingId"] != nil {
		t.Fatalf("未备案分母对象 = %+v", result)
	}
	inventoryRequest(t, mux, http.MethodPatch, path+"/"+id,
		fmt.Sprintf(`{"filingId":%q,"active":true}`, unrelated), `"1"`, 409)
	updated := inventoryRequest(t, mux, http.MethodPatch, path+"/"+id,
		fmt.Sprintf(`{"filingId":%q,"active":true}`, filing), `"1"`, 200)
	if updated["covered"] != true || updated["version"] != float64(2) {
		t.Fatalf("覆盖登记挂接 = %+v", updated)
	}
	inventoryRequest(t, mux, http.MethodPost, path, fmt.Sprintf(
		`{"unitId":%q,"systemName":"同名业务系统","reference":"范围-002","source":"普查清册","filingId":%q}`,
		unit, filing), "", 409)
	if _, err := db.Exec(t.Context(), "UPDATE business_cases SET system_name='备案系统已更名' WHERE id=$1", filing); err != nil {
		t.Fatal(err)
	}
	assertCoverageTargetUncovered(t, mux, path)
	if _, err := db.Exec(t.Context(), "UPDATE business_cases SET system_name='同名业务系统',cancelled_at=clock_timestamp() WHERE id=$1",
		filing); err != nil {
		t.Fatal(err)
	}
	assertCoverageTargetUncovered(t, mux, path)
}

// assertCoverageTargetUncovered 核对变化后的备案仍可追溯，但已不能覆盖原登记对象。
// 参数 t 为测试上下文，mux 为档案接口，path 为覆盖清册接口地址。
// 返回值 无。
func assertCoverageTargetUncovered(t *testing.T, mux http.Handler, path string) {
	t.Helper()
	list := inventoryRequest(t, mux, http.MethodGet, path, "", "", 200)
	items := list["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["covered"] != false {
		t.Fatalf("原清册对象应保留但不再算已覆盖：%+v", list)
	}
}
