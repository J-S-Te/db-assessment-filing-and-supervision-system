package archives

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestInventoryRejectsInvalidAddress 验证 IP 登记只接受单个规范 IP 地址。
// 参数 t 为不访问数据库的 HTTP 测试上下文。
// 返回值 无。
func TestInventoryRejectsInvalidAddress(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, nil)
	inventoryRequest(t, mux, http.MethodPost,
		"/api/v1/archives/systems/00000000-0000-0000-0000-000000000001/assets",
		`{"kind":"ip","identifier":"192.0.2.0/24","source":"台账-1"}`, "", 400)
}

// TestInventoryUsesFilingIdentityAndVersions 验证资产登记的系统归属、规范化去重及停用版本。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestInventoryUsesFilingIdentityAndVersions(t *testing.T) {
	db := archiveTestPool(t)
	active := insertArchiveFiling(t, db, "F-IP", "资产单位", "3", "approved", false)
	other := insertArchiveFiling(t, db, "F-OTHER", "其他单位", "3", "approved", false)
	cancelled := insertArchiveFiling(t, db, "F-PAST", "资产单位", "3", "approved", true)
	mux := http.NewServeMux()
	Register(mux, db)
	path := "/api/v1/archives/systems/" + active + "/assets"

	first := inventoryRequest(t, mux, http.MethodPost, path,
		`{"kind":"domain","identifier":"Example.Test.","source":"资产台账-1"}`, "", 201)
	id, _ := first["id"].(string)
	if id == "" || first["identifier"] != "example.test" || first["version"] != float64(1) {
		t.Fatalf("域名登记 = %+v", first)
	}
	retry := inventoryRequest(t, mux, http.MethodPost, path,
		`{"kind":"domain","identifier":"example.test","source":"资产台账-1"}`, "", 200)
	if retry["id"] != id || retry["version"] != float64(1) {
		t.Fatalf("规范化重试 = %+v，原记录 = %+v", retry, first)
	}
	inventoryRequest(t, mux, http.MethodPost, path,
		`{"kind":"domain","identifier":"example.test","source":"不同台账"}`, "", 409)
	inventoryRequest(t, mux, http.MethodPatch, "/api/v1/archives/systems/"+other+"/assets/"+id,
		`{"source":"资产台账-1","active":false}`, `"1"`, 404)
	updated := inventoryRequest(t, mux, http.MethodPatch, path+"/"+id,
		`{"source":"资产台账-1","active":false}`, `"1"`, 200)
	if updated["active"] != false || updated["version"] != float64(2) {
		t.Fatalf("登记停用 = %+v", updated)
	}
	inventoryRequest(t, mux, http.MethodPatch, path+"/"+id,
		`{"source":"资产台账-2","active":true}`, `"1"`, 409)
	inventoryRequest(t, mux, http.MethodPost, "/api/v1/archives/systems/"+cancelled+"/assets",
		`{"kind":"ip","identifier":"192.0.2.1","source":"资产台账-1"}`, "", 409)
}

// inventoryRequest 执行档案读写接口并核对实际 HTTP 状态。
// 参数 t 为测试上下文，mux 为档案路由，method、path、body、version 为请求，status 为预期状态。
// 返回值 解码后的业务响应对象。
func inventoryRequest(t *testing.T, mux http.Handler, method, path, body, version string, status int) map[string]any {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if version != "" {
		req.Header.Set("If-Match", version)
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	if response.Code != status {
		t.Fatalf("%s %s = %d，期望 %d，响应 %s", method, path, response.Code, status, response.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// TestUnitArchivesExposeOnlyTheirOwnEvidence 验证供应商、安全事件与资产计数按备案归属读取。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestUnitArchivesExposeOnlyTheirOwnEvidence(t *testing.T) {
	db := archiveTestPool(t)
	active := insertArchiveFiling(t, db, "F-UNIT-ASSET", unitName, "3", "approved", false)
	other := insertArchiveFiling(t, db, "F-OTHER-ASSET", "其他单位", "3", "approved", false)
	mux := http.NewServeMux()
	Register(mux, db)
	prefix := "/api/v1/archives/systems/"
	inventoryRequest(t, mux, http.MethodPost, prefix+active+"/assets",
		`{"kind":"ip","identifier":"::ffff:192.0.2.1","source":"资产台账-1"}`, "", 201)
	inventoryRequest(t, mux, http.MethodPost, prefix+active+"/assets",
		`{"kind":"supplier","identifier":"SUP-001","label":"维护服务方","source":"服务合同-1"}`, "", 201)
	inventoryRequest(t, mux, http.MethodPost, prefix+active+"/assets",
		`{"kind":"security_event","identifier":"EVT-001","source":"处置单-1","occurredOn":"2026-01-01"}`, "", 201)
	inventoryRequest(t, mux, http.MethodPost, prefix+other+"/assets",
		`{"kind":"security_event","identifier":"EVT-002","source":"处置单-2","occurredOn":"2026-01-01"}`, "", 201)
	result := archiveGET[unitProfile](t, mux, "/api/v1/archives/units/"+groupKey(unitName), 200)
	if len(result.Systems.Items) != 1 || result.Systems.Items[0].AssetCount != 1 ||
		len(result.AssetRecords.Items) != 2 || result.AssetRecords.Items[0].FilingID != active ||
		result.AssetRecords.Items[0].OccurredOn != "2026-01-01" || result.AssetRecords.Items[1].Identifier != "SUP-001" {
		t.Fatalf("单位资产与证据范围 = %+v", result)
	}
}

// TestRegistrationWritesRejectQueryParameters 验证写入接口只通过已公开的路径、请求体和版本字段接收参数。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestRegistrationWritesRejectQueryParameters(t *testing.T) {
	db := archiveTestPool(t)
	id := insertArchiveFiling(t, db, "F-WRITE-QUERY", "资产单位", "3", "approved", false)
	mux := http.NewServeMux()
	Register(mux, db)
	inventoryRequest(t, mux, http.MethodPost, "/api/v1/archives/systems/"+id+"/assets?active=false",
		`{"kind":"ip","identifier":"192.0.2.1","source":"资产台账"}`, "", 400)
	inventoryRequest(t, mux, http.MethodPatch, "/api/v1/archives/systems/"+id+"/assets/"+id+"?unitId="+id,
		`{"source":"资产台账","active":true}`, `"1"`, 400)
	inventoryRequest(t, mux, http.MethodPost, "/api/v1/archives/coverage-targets?active=false",
		`{"unitId":"`+id+`","systemName":"系统","reference":"范围-01","source":"清册"}`, "", 400)
	inventoryRequest(t, mux, http.MethodPatch, "/api/v1/archives/coverage-targets/"+id+"?unitId="+id,
		`{"active":true}`, `"1"`, 400)
}
