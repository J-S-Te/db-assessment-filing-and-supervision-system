package implementations

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestImplementationRejectsUnknownDirectoryReporter 核对报备拒绝无归属的人员标识。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestImplementationRejectsUnknownDirectoryReporter(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(pool)
	filing := implementationTestFiling(t, pool, "BA-DIRECTORY-REPORTER", approved)
	raw, err := json.Marshal(testInput(filing, routine))
	if err != nil {
		t.Fatal(err)
	}
	var form map[string]any
	if err := json.Unmarshal(raw, &form); err != nil {
		t.Fatal(err)
	}
	form["reporterId"] = "00000000-0000-0000-0000-000000000001"
	response := callImplementation(mux, http.MethodPost,
		firstImplementationPath+firstImplementationScope(), form)
	result := decodeResponse[map[string]string](t, response, http.StatusBadRequest)
	if result["code"] != "directory_person_invalid" {
		t.Fatalf("无归属人员响应 = %+v", result)
	}
}

// TestImplementationRetainsDirectoryAttribution 核对实施报备引用名录，停用人员关联的草稿不可送审并保留历史。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestImplementationRetainsDirectoryAttribution(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(pool)
	filing := implementationTestFiling(t, pool, "BA-DIRECTORY-RETAIN", approved)
	_, err := pool.Exec(t.Context(), `INSERT INTO directory_parties
		(kind,scope_name,name,code,qualification,valid_until)
		SELECT 'institution',assessment_org,'当前机构名','DIR-Q','甲级','2099-12-31' FROM business_cases WHERE id=$1`,
		filing)
	if err != nil {
		t.Fatal(err)
	}
	var person string
	err = pool.QueryRow(t.Context(), `INSERT INTO directory_people (institution_id,name,code)
	    SELECT id,'经办人','T-01' FROM directory_parties RETURNING id::text`).Scan(&person)
	if err != nil {
		t.Fatal(err)
	}
	form := testInput(filing, routine)
	response := callImplementation(mux, http.MethodPost, firstImplementationPath+firstImplementationScope(), form)
	rejection := decodeResponse[map[string]string](t, response, 400)
	if rejection["code"] != "directory_person_required" {
		t.Fatalf("机构使用自由文字绕过名录: %+v", rejection)
	}
	form.Reporter, form.ReporterID = "", &person
	created := createInstitutionImplementation(t, mux, form)
	if _, err := pool.Exec(t.Context(), `UPDATE directory_people SET status='inactive' WHERE id=$1`, person); err != nil {
		t.Fatal(err)
	}
	expectImplementationDirectoryBlocked(t, mux, created)
	if _, err := pool.Exec(t.Context(), `UPDATE directory_people SET status='active' WHERE id=$1`, person); err != nil {
		t.Fatal(err)
	}
	sent := institutionImplementationAction(t, mux, created, "submit", 200)
	if sent.Status != submitted {
		t.Fatalf("恢复有效名录后未送审: %+v", sent)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE directory_people SET name='名录后来改名' WHERE id=$1`, person); err != nil {
		t.Fatal(err)
	}
	assertImplementationDirectoryHistory(t, mux, sent, person)
}

// expectImplementationDirectoryBlocked 核对已停用人员不能提交更早创建的实施草稿。
// 参数 t、mux 为测试上下文及路由，record 为停用之前创建的草稿。
// 返回值 无。
func expectImplementationDirectoryBlocked(t *testing.T, mux *http.ServeMux, record mutation) {
	t.Helper()
	response := callImplementation(mux, http.MethodPost,
		firstImplementationPath+"/"+record.ID+"/actions"+firstImplementationScope(),
		command{Action: "submit", ExpectedVersion: record.Version})
	result := decodeResponse[map[string]string](t, response, 409)
	if result["code"] != "directory_unavailable" {
		t.Fatalf("停用后仍可提交或拒绝原因错误: %+v", result)
	}
}

// assertImplementationDirectoryHistory 核对历史报名人员读取的是报备时快照。
// 参数 t、mux 为测试上下文和路由，item 为已提交的报备，id 为原始人员标识。
// 返回值 无。
func assertImplementationDirectoryHistory(t *testing.T, mux *http.ServeMux, item mutation, id string) {
	t.Helper()
	detailResponse := decodeResponse[detail](t, callImplementation(mux, http.MethodGet, path+"/"+item.ID, nil), 200)
	if detailResponse.ReporterID == nil || *detailResponse.ReporterID != id ||
		detailResponse.Reporter != "经办人" || detailResponse.DirectorySnapshot == nil ||
		detailResponse.DirectorySnapshot.Institution == nil || detailResponse.DirectorySnapshot.Institution.Name != "当前机构名" {
		t.Fatalf("报备未保留实际人员和当时机构数据: %+v", detailResponse)
	}
	history := decodeResponse[eventPage](t,
		callImplementation(mux, http.MethodGet, path+"/"+item.ID+"/history", nil), 200)
	if len(history.Items) != 2 || history.Items[0].DirectorySnapshot == nil ||
		history.Items[0].DirectorySnapshot.Person.Name != "经办人" {
		t.Fatalf("提交事件未保留原人员: %+v", history.Items)
	}
}
