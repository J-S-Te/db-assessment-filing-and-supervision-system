package directory

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

type testEntry struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ScopeName       string `json:"scopeName"`
	InstitutionID   string `json:"institutionId"`
	InstitutionName string `json:"institutionName"`
	Status          string `json:"status"`
	Usable          bool   `json:"usable"`
	CanEdit         bool   `json:"canEdit"`
	Version         int64  `json:"version"`
}

type testPage[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor"`
}

// TestRegisterInstitutionWithoutQualification 用途 验证监管可在暂缺资质等级时登记和维护机构。
// 参数 t 为隔离 PostgreSQL 的测试上下文。
// 返回值 无。
func TestRegisterInstitutionWithoutQualification(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, directoryTestPool(t))
	const path = "/api/v1/directory/institutions"
	input := map[string]any{"name": "待补等级机构", "code": "CERT-OPTIONAL-LEVEL"}
	testRequest(t, mux, "POST", path, input, 400)
	input["validUntil"] = nextYear()
	item := testJSON[entry](t, testRequest(t, mux, "POST", path, input, 201))
	if item.Qualification != "" || item.ValidUntil != input["validUntil"] || !item.Usable {
		t.Fatalf("应保留有效期并允许登记空等级机构：%+v", item)
	}
	updated := testJSON[entry](t, testRequest(t, mux, "PATCH", path+"/"+item.ID, map[string]any{
		"expectedVersion": item.Version, "name": "待补等级机构新名称",
	}, 200))
	if updated.Qualification != "" || updated.Name == item.Name {
		t.Fatalf("未填写的等级应保持为空：%+v", updated)
	}
}

// TestDirectoryRegistryScope 验证重复登记、稳定归属、机构维护权限和共享单位停用。
// 参数 t 为隔离数据库的测试上下文。
// 返回值 无。
func TestDirectoryRegistryScope(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, directoryTestPool(t))
	north := registerTestInstitution(t, mux, "名录北机构", "CERT-N")
	south := registerTestInstitution(t, mux, "名录南机构", "CERT-S")
	testRequest(t, mux, "POST", "/api/v1/directory/institutions", map[string]any{
		"name": "另一个机构", "code": "cert-n", "qualification": "甲级", "validUntil": nextYear(),
	}, 409)
	scoped := scopedDirectory("units", "", north.ScopeName)
	unit := testJSON[testEntry](t, testRequest(t, mux, "POST", scoped, map[string]any{
		"name": "名录联测单位", "code": "913300001000000001", "classification": "企业", "district": "市本级",
	}, 201))
	itemPath := "/api/v1/directory/units/" + unit.ID
	if unit.Name != unit.ScopeName || !unit.CanEdit || !unit.Usable {
		t.Fatalf("登记后的单位缺少可用与范围: %+v", unit)
	}
	testRequest(t, mux, "PATCH", scopedDirectory("units", unit.ID, south.ScopeName),
		map[string]any{"expectedVersion": 1, "district": "南区"}, 404)
	link := "/api/v1/institution/directory/units/" + unit.ID + "/link?institution=" + url.QueryEscape(south.ScopeName)
	testRequest(t, mux, "POST", link, nil, 200)
	testRequest(t, mux, "POST", link, nil, 200)
	testRequest(t, mux, "PATCH", scopedDirectory("units", unit.ID, north.ScopeName),
		map[string]any{"expectedVersion": 1, "district": "北区"}, 403)
	renamed := testJSON[testEntry](t, testRequest(t, mux, "PATCH", itemPath,
		map[string]any{"expectedVersion": 1, "name": "名录联测单位新名称"}, 200))
	if renamed.ScopeName != unit.ScopeName || renamed.Name == unit.Name {
		t.Fatalf("显示名称更新破坏旧业务归属: %+v", renamed)
	}
	testRequest(t, mux, "POST", "/api/v1/directory/units", map[string]any{
		"name": unit.ScopeName, "code": "913300001000000005",
	}, 409)
	assertSharedUnitHistory(t, mux, itemPath, renamed.ScopeName, renamed.Name)
	testRequest(t, mux, "PATCH", itemPath,
		map[string]any{"expectedVersion": renamed.Version, "status": "inactive"}, 200)
	options := testJSON[testPage[testEntry]](t,
		testRequest(t, mux, "GET", "/api/v1/directory/options?kind=unit", nil, 200))
	if len(options.Items) != 0 {
		t.Fatalf("停用单位仍可选择: %+v", options)
	}
}

// assertSharedUnitHistory 核对重复关联不会产生重复历史以及原始归属仍可回溯。
// 参数 t 为测试上下文，mux 为测试路由，path、scope 和 name 为单位路径、旧范围和新名称。
// 返回值 无。
func assertSharedUnitHistory(t *testing.T, mux *http.ServeMux, path, scope, name string) {
	t.Helper()
	type testEvent struct {
		Action string     `json:"action"`
		Before *testEntry `json:"before"`
		After  testEntry  `json:"after"`
	}
	events := testJSON[testPage[testEvent]](t, testRequest(t, mux, "GET", path+"/history", nil, 200))
	if len(events.Items) != 3 || events.Items[0].Before == nil ||
		events.Items[0].Before.Name != scope || events.Items[0].After.Name != name ||
		events.Items[1].Action != "link" || events.Items[2].Action != "create" {
		t.Fatalf("单位归属历史 = %+v", events)
	}
}

// registerTestInstitution 快速通过机构接口登记一条有效机构。
// 参数 t、mux 为测试上下文及路由，name、code 为测试机构名称及资质编号。
// 返回值 新登记的机构条目。
func registerTestInstitution(t *testing.T, mux *http.ServeMux, name, code string) testEntry {
	t.Helper()
	return testJSON[testEntry](t, testRequest(t, mux, "POST", "/api/v1/directory/institutions", map[string]any{
		"name": name, "code": code, "qualification": "甲级", "validUntil": nextYear(),
	}, 201))
}

// nextYear 返回测试长期有效的资格日期。
// 参数 无。
// 返回值 ISO 日期。
func nextYear() string { return time.Now().AddDate(1, 0, 0).Format(time.DateOnly) }

// scopedDirectory 形成机构端目录路径及稳定范围条件。
// 参数 collection、id 和 scope 分别为目录、条目标识和机构范围。
// 返回值 可传给请求的 URL。
func scopedDirectory(collection, id, scope string) string {
	path := "/api/v1/institution/directory/" + collection
	if id != "" {
		path += "/" + id
	}
	return path + "?institution=" + url.QueryEscape(scope)
}

// testRequest 发送 JSON 或空体业务请求并核对状态。
// 参数 t、mux 为测试上下文及路由，method、path、input、status 指示请求与预期。
// 返回值 可解析的状态响应。
func testRequest(t *testing.T, mux *http.ServeMux, method, path string, input map[string]any, status int) []byte {
	t.Helper()
	var payload []byte
	if input != nil {
		var err error
		payload, err = json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != status {
		t.Fatalf("%s %s 响应 %d，预期 %d: %s", method, path, w.Code, status, w.Body)
	}
	return w.Body.Bytes()
}

// testJSON 解码测试需要的公开字段。
// 参数 t 为测试上下文，body 为接口响应体。
// 返回值 对应类型的结果。
func testJSON[T any](t *testing.T, body []byte) T {
	t.Helper()
	var result T
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// TestDirectoryRenameCannotHideLegacyUnit 验证管理端改名不会把另一旧备案单位覆盖在同一名录记录下。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDirectoryRenameCannotHideLegacyUnit(t *testing.T) {
	pool := directoryTestPool(t)
	mux := http.NewServeMux()
	Register(mux, pool)
	if _, err := pool.Exec(t.Context(), `INSERT INTO business_cases
		(kind,number,system_name,organization_name,assessment_org,level,status)
		VALUES('filing','LEGACY-001','旧备案系统','既有备案单位','旧机构','2','approved')`); err != nil {
		t.Fatal(err)
	}
	registered := testJSON[testEntry](t, testRequest(t, mux, "POST", "/api/v1/directory/units",
		map[string]any{"name": "新登记单位", "code": "913300001000000207"}, 201))
	testRequest(t, mux, "PATCH", "/api/v1/directory/units/"+registered.ID,
		map[string]any{"expectedVersion": registered.Version, "name": "既有备案单位"}, 409)
	items := testJSON[testPage[testEntry]](t,
		testRequest(t, mux, "GET", "/api/v1/directory/options?kind=unit", nil, 200)).Items
	if len(items) != 2 {
		t.Fatalf("旧备案选择范围受损: %+v", items)
	}
}

// TestDirectoryPersonnelHistoryAndReviewGuard 核对在审阻止停用及调离、办结后保留原机构记录。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDirectoryPersonnelHistoryAndReviewGuard(t *testing.T) {
	pool := directoryTestPool(t)
	mux := http.NewServeMux()
	Register(mux, pool)
	old := registerTestInstitution(t, mux, "原测评机构", "CERT-OLD")
	next := registerTestInstitution(t, mux, "新测评机构", "CERT-NEXT")
	peoplePath := scopedDirectory("people", "", old.ScopeName)
	personInput := map[string]any{"name": "在审人员", "certificateNo": "T-TEST-202"}
	staff := testJSON[testEntry](t, testRequest(t, mux, "POST", peoplePath, personInput, 201))
	testRequest(t, mux, "POST", peoplePath, personInput, 409)
	testRequest(t, mux, "POST", scopedDirectory("people", "", next.ScopeName),
		map[string]any{"name": "证书重复", "certificateNo": "t-test-202"}, 409)
	if staff.InstitutionID != old.ID || !staff.CanEdit {
		t.Fatalf("机构登记人员归属错误: %+v", staff)
	}
	var reportID string
	err := pool.QueryRow(t.Context(), `WITH source AS (INSERT INTO business_cases
	    (kind,number,system_name,organization_name,assessment_org,level,status)
	    VALUES('filing','REVIEW-001','测试系统','测试单位',$1,'2','approved') RETURNING id)
	  INSERT INTO business_cases(kind,number,assessment_org,filing_id,risk,status,directory_person_id)
	  SELECT 'report','REVIEW-002',$1,id,'low','pending_initial',$2 FROM source RETURNING id::text`,
		old.ScopeName, staff.ID).Scan(&reportID)
	if err != nil {
		t.Fatal(err)
	}
	assertDirectoryReviewGuard(t, mux, staff, old, next)
	if _, err := pool.Exec(t.Context(), "UPDATE business_cases SET status='approved' WHERE id=$1", reportID); err != nil {
		t.Fatal(err)
	}
	assertDirectoryTransfer(t, mux, staff, old, next)
}

// TestRegisterAssessorsWithBlankCode 验证监管和机构可连续登记无工号测评师。
// 参数 t 为隔离 PostgreSQL 与实际名录路由的测试上下文。
// 返回值 无。
func TestRegisterAssessorsWithBlankCode(t *testing.T) {
	pool := directoryTestPool(t)
	mux := http.NewServeMux()
	Register(mux, pool)
	org := registerTestInstitution(t, mux, "免工号登记测试机构", "ORG-NO-STAFF-CODE")
	registry := "/api/v1/directory/people"
	first := testJSON[entry](t, testRequest(t, mux, "POST", registry, map[string]any{
		"name": "监管登记测评师", "institutionId": org.ID, "certificateNo": "cert-1128",
	}, 201))
	people := scopedDirectory("people", "", org.ScopeName)
	second := testJSON[entry](t, testRequest(t, mux, "POST", people, map[string]any{
		"name": "机构登记测评师",
	}, 201))
	if first.ID == second.ID || first.CertificateNo != "CERT-1128" || second.InstitutionID != org.ID {
		t.Fatalf("登记结果不正确：%+v，%+v", first, second)
	}
	updated := testJSON[entry](t, testRequest(t, mux, "PATCH", registry+"/"+first.ID, map[string]any{
		"expectedVersion": first.Version, "certificateNo": "cert-1129",
	}, 200))
	if updated.CertificateNo != "CERT-1129" {
		t.Fatalf("证书编号未更新：%+v", updated)
	}
	var count int
	err := pool.QueryRow(t.Context(),
		"SELECT count(*) FROM directory_people WHERE institution_id=$1 AND code=''", org.ID,
	).Scan(&count)
	if err != nil || count != 2 {
		t.Fatalf("两条新档案的数据库工号应为空，实际记录数 %d，错误 %v", count, err)
	}
}

// TestDecodeAssessorWriteRejectsCode 验证工号字段无论大小写和取值均不在请求白名单内。
// 参数 t 为名录请求解码测试上下文。
// 返回值 无。
func TestDecodeAssessorWriteRejectsCode(t *testing.T) {
	for _, body := range []string{
		`{"name":"测评师","code":"INJECTED"}`,
		`{"name":"测评师","Code":""}`,
		`{"name":"测评师","CODE":null}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/directory/people", bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		if err := (endpoint{}).decodeWrite(httptest.NewRecorder(), request, person, &change{}); err != errInvalid {
			t.Errorf("测评师请求包含工号，应在读写数据库前拒绝：%s，错误 %v", body, err)
		}
	}
}

// TestAssessorWritesRejectCode 验证两个入口拒收工号且保留已有值。
// 参数 t 为隔离 PostgreSQL 与实际名录路由的测试上下文。
// 返回值 无。
func TestAssessorWritesRejectCode(t *testing.T) {
	pool := directoryTestPool(t)
	mux := http.NewServeMux()
	Register(mux, pool)
	org := registerTestInstitution(t, mux, "工号防写测试机构", "ORG-CODE-READONLY")
	var id string
	err := pool.QueryRow(t.Context(), `INSERT INTO directory_people(institution_id,name,code)
		VALUES ($1,'存量测评师','HISTORICAL') RETURNING id::text`, org.ID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	registry := "/api/v1/directory/people"
	for _, paths := range [][2]string{
		{registry, registry + "/" + id},
		{scopedDirectory("people", "", org.ScopeName), scopedDirectory("people", id, org.ScopeName)},
	} {
		for key, value := range map[string]any{"code": "INJECTED", "Code": "", "CODE": nil} {
			create := map[string]any{"name": "带工号的测评师", key: value}
			if paths[0] == registry {
				create["institutionId"] = org.ID
			}
			testRequest(t, mux, "POST", paths[0], create, 400)
			testRequest(t, mux, "PATCH", paths[1], map[string]any{
				"expectedVersion": 1, "name": "未经允许的修改", key: value,
			}, 400)
		}
	}
	testRequest(t, mux, "PATCH", registry+"/"+id,
		map[string]any{"expectedVersion": 1, "name": "正常修改姓名"}, 200)
	var code string
	err = pool.QueryRow(t.Context(), "SELECT code FROM directory_people WHERE id=$1", id).Scan(&code)
	if err != nil || code != "HISTORICAL" {
		t.Fatalf("存量工号未保留：%q，错误 %v", code, err)
	}
}

// assertDirectoryReviewGuard 验证一条未结业务就能阻止名录停用或人员调离。
// 参数 t、mux 为测试上下文及路由，staff、old、next 为人员和原、新归属。
// 返回值 无。
func assertDirectoryReviewGuard(t *testing.T, mux *http.ServeMux, staff, old, next testEntry) {
	t.Helper()
	institutionPath := "/api/v1/directory/institutions/" + old.ID
	personPath := "/api/v1/directory/people/" + staff.ID
	for _, assignment := range []struct {
		path, key, value string
	}{
		{personPath, "status", "inactive"},
		{personPath, "institutionId", next.ID},
		{institutionPath, "validUntil", "2000-01-01"},
		{institutionPath, "status", "inactive"},
	} {
		response := testJSON[map[string]string](t, testRequest(t, mux, "PATCH", assignment.path,
			map[string]any{"expectedVersion": 1, assignment.key: assignment.value}, 409))
		if response["code"] != "directory_in_review" {
			t.Fatalf("未按在审门禁阻止数据库变更: %v", response)
		}
	}
}

// assertDirectoryTransfer 核对调动留痕以及旧归属和新归属的目录隔离。
// 参数 t、mux 为测试上下文及路由，staff、old、next 为人员和原、新归属。
// 返回值 无。
func assertDirectoryTransfer(t *testing.T, mux *http.ServeMux, staff, old, next testEntry) {
	t.Helper()
	admin := "/api/v1/directory/people/" + staff.ID
	moved := testJSON[testEntry](t, testRequest(t, mux, "PATCH", admin,
		map[string]any{"expectedVersion": staff.Version, "institutionId": next.ID}, 200))
	if moved.InstitutionID != next.ID || moved.Version != staff.Version+1 {
		t.Fatalf("人员调动归属错误: %+v", moved)
	}
	testRequest(t, mux, "GET", scopedDirectory("people", staff.ID, old.ScopeName), nil, 404)
	testRequest(t, mux, "PATCH", admin, map[string]any{"expectedVersion": staff.Version, "status": "inactive"}, 409)
	history := testJSON[testPage[event]](t, testRequest(t, mux, "GET", admin+"/history", nil, 200)).Items
	if len(history) != 2 || history[0].Action != "transfer" || history[0].Before == nil ||
		history[0].Before.InstitutionID != old.ID || history[0].After.InstitutionID != next.ID {
		t.Fatalf("监管没有完整人员归属历史: %+v", history)
	}
	scopedPath := "/api/v1/institution/directory/people/" + staff.ID + "/history?institution=" +
		url.QueryEscape(next.ScopeName)
	scoped := testJSON[testPage[event]](t, testRequest(t, mux, "GET", scopedPath, nil, 200)).Items
	if len(scoped) != 1 || scoped[0].Action != "transfer" || scoped[0].Before != nil {
		t.Fatalf("新机构取得了旧机构的人员登记明细: %+v", scoped)
	}
}

// TestDirectoryPersonPickerHidesCertificateSearch 验证业务选择器兼容按历史编号辨识且不暴露证书。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDirectoryPersonPickerHidesCertificateSearch(t *testing.T) {
	mux := http.NewServeMux()
	pool := directoryTestPool(t)
	Register(mux, pool)
	org := registerTestInstitution(t, mux, "人员选项测试机构", "CERT-CHOICE")
	_, err := pool.Exec(t.Context(),
		`INSERT INTO directory_people(institution_id,name,code,certificate_no) VALUES
			($1,'同名测评师','WORK-ONE','PRIVATE-CERTIFICATE'),
			($1,'同名测评师','WORK-TWO','OTHER-CERTIFICATE')`, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/directory/options?kind=person&institution=" + url.QueryEscape(org.ScopeName)
	body := testRequest(t, mux, "GET", path+"&q=work-two", nil, 200)
	result := testJSON[testPage[map[string]any]](t, body).Items
	if len(result) != 1 || result[0]["code"] != "WORK-TWO" || result[0]["certificateNo"] != nil {
		t.Fatalf("人员选项不能用工号最小辨识: %v", result)
	}
	result = testJSON[testPage[map[string]any]](t,
		testRequest(t, mux, "GET", path+"&q=private-certificate", nil, 200)).Items
	if len(result) != 0 {
		t.Fatalf("人员选项泄露了未公开证书匹配结果: %v", result)
	}
}
