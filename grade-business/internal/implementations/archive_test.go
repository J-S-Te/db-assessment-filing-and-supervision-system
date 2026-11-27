package implementations

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestImplementationArchivePreservesApprovedReview 验证归档只更新归档时间及版本并保留原审核状态。
// 参数 t 为隔离数据库集成测试上下文。
// 返回值 无。
func TestImplementationArchivePreservesApprovedReview(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(pool)
	filing := implementationTestFiling(t, pool, "测试备案-实施归档", approved)
	record := approvedImplementation(t, mux, testInput(filing, change))
	resource := path + "/" + record.ID
	before := decodeResponse[detail](t, callImplementation(mux, http.MethodGet, resource, nil), 200)
	if !slices.Equal(before.AllowedActions, []string{"archive"}) || before.ArchivedAt != nil {
		t.Fatalf("已通过未归档报备 = %+v", before)
	}
	result := decodeResponse[mutation](t, callImplementation(mux, http.MethodPost, resource+"/actions",
		command{Action: "archive", ExpectedVersion: record.Version, Comment: "归档核对完成。"}), 200)
	if result.ID != record.ID || result.Status != approved || result.Version != record.Version+1 ||
		result.ArchivedAt == nil || !result.ArchivedAt.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, shanghai)) {
		t.Fatalf("归档结果 = %+v", result)
	}
	assertImplementationArchiveHistory(t, mux, resource, &before, result)
	assertQuota(t, mux, filing, 1)
	var notices int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM review_notices
	 WHERE resource='implementations' AND resource_id=$1`, record.ID).Scan(&notices); err != nil || notices != 1 {
		t.Fatalf("归档后实施审核意见数 = %d，错误 = %v", notices, err)
	}
}

// TestImplementationArchiveRejectsIneligibleAndRepeatedActions 验证草稿不可归档且重复操作只写入一条历史。
// 参数 t 为隔离数据库集成测试上下文。
// 返回值 无。
func TestImplementationArchiveRejectsIneligibleAndRepeatedActions(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(pool)
	filing := implementationTestFiling(t, pool, "测试备案-归档边界", approved)
	item := testInput(filing, routine)
	pending := decodeResponse[mutation](t, callImplementation(mux, http.MethodPost, path, item), 201)
	assertArchiveFailure(t, mux, path+"/"+pending.ID+"/actions", pending.Version, 409, "invalid_state")
	record := approvedImplementation(t, mux, item)
	resource := path + "/" + record.ID
	scopedActions := "/api/v1/institution/implementations/" + record.ID +
		"/actions?institution=" + url.QueryEscape("备案测评机构")
	assertArchiveFailure(t, mux, scopedActions, record.Version, 400, "validation_error")
	next := concurrentImplementationArchive(t, mux, resource+"/actions", record.Version)
	assertArchiveFailure(t, mux, resource+"/actions", next.Version, 409, "invalid_state")
	history := decodeResponse[eventPage](t, callImplementation(mux, http.MethodGet, resource+"/history", nil), 200)
	if len(history.Items) < 2 || history.Items[0].Action != "archive" || history.Items[1].Action != "approve" {
		t.Fatalf("重复提交归档后的流转记录 = %+v", history.Items)
	}
}

// TestImplementationArchiveListFilters 验证归档和原筛选条件可组合，同时保持默认列表的数据范围。
// 参数 t 为隔离数据库集成测试上下文。
// 返回值 无。
func TestImplementationArchiveListFilters(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(pool)
	filing := implementationTestFiling(t, pool, "测试备案-归档列表", approved)
	archived := approvedImplementation(t, mux, testInput(filing, change))
	archivedSecond := approvedImplementation(t, mux, testInput(filing, routine))
	current := approvedImplementation(t, mux, testInput(filing, routine))
	latest := decodeResponse[mutation](t, callImplementation(mux, http.MethodPost, path, testInput(filing, routine)), 201)
	performAction(t, mux, archived, "archive", "")
	performAction(t, mux, archivedSecond, "archive", "")
	assertArchivePage(t, mux, "", latest.ID, current.ID, archivedSecond.ID, archived.ID)
	assertArchivePage(t, mux, "?archive=archived&status=approved&type=change", archived.ID)
	assertArchivePage(t, mux, "?archive=unarchived&status=approved", current.ID)
	first := assertArchivePage(t, mux, "?archive=archived&limit=1", archivedSecond.ID)
	if first == "" {
		t.Fatal("归档列表缺少下一页游标")
	}
	assertArchivePage(t, mux, "?archive=archived&limit=1&cursor="+first, archived.ID)
	decodeResponse[map[string]string](t, callImplementation(mux, http.MethodGet, path+"?archive=unknown", nil), 400)
	decodeResponse[map[string]string](t, callImplementation(mux, http.MethodGet, path+"?archive=archived&archive=unarchived", nil), 400)
}

// TestImplementationArchiveRollsBackHistoryFailure 验证归档留痕写入失败时归档状态与版本均回滚。
// 参数 t 为隔离数据库集成测试上下文。
// 返回值 无。
func TestImplementationArchiveRollsBackHistoryFailure(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(pool)
	filing := implementationTestFiling(t, pool, "测试备案-归档回滚", approved)
	record := approvedImplementation(t, mux, testInput(filing, routine))
	if _, err := pool.Exec(t.Context(), `ALTER TABLE implementation_events ADD CHECK (action <> 'archive')`); err != nil {
		t.Fatal(err)
	}
	resource := path + "/" + record.ID
	assertArchiveFailure(t, mux, resource+"/actions", record.Version, 500, "internal_error")
	detail := decodeResponse[detail](t, callImplementation(mux, http.MethodGet, resource, nil), 200)
	if detail.Status != approved || detail.Version != record.Version || detail.ArchivedAt != nil ||
		!slices.Equal(detail.AllowedActions, []string{"archive"}) {
		t.Fatalf("归档留痕失败后的报备 = %+v", detail)
	}
}

// approvedImplementation 通过监管接口生成已完成监管复核的实施报备。
// 参数 t 为测试上下文，mux 为真实业务路由，item 为报备表单。
// 返回值 已通过的报备版本。
func approvedImplementation(t *testing.T, mux *http.ServeMux, item input) mutation {
	t.Helper()
	record := decodeResponse[mutation](t, callImplementation(mux, http.MethodPost, path, item), 201)
	for _, action := range []string{"submit", "accept", "approve", "approve", "approve"} {
		record = performAction(t, mux, record, action, "")
	}
	return record
}

// assertImplementationArchiveHistory 核对归档详情保留原记录且最新留痕记录归档。
// 参数 t 为测试上下文，mux 为真实路由，resource 为详情路径，before、result 为归档前后数据。
// 返回值 无。
func assertImplementationArchiveHistory(t *testing.T, mux *http.ServeMux, resource string,
	before *detail, result mutation) {
	t.Helper()
	current := decodeResponse[detail](t, callImplementation(mux, http.MethodGet, resource, nil), 200)
	if current.ArchivedAt == nil || !current.ArchivedAt.Equal(*result.ArchivedAt) || len(current.AllowedActions) != 0 ||
		current.Version != result.Version || current.Plan != before.Plan || current.Type != before.Type ||
		current.FilingID != before.FilingID || current.Status != approved {
		t.Fatalf("归档后详情 = %+v", current)
	}
	history := decodeResponse[eventPage](t, callImplementation(mux, http.MethodGet, resource+"/history", nil), 200)
	if len(history.Items) < 1 {
		t.Fatal("缺少归档操作历史")
	}
	entry := history.Items[0]
	if entry.Action != "archive" || entry.FromStatus != approved || entry.ToStatus != approved ||
		entry.Comment != "归档核对完成。" || entry.Operator != "测试办理人" || !entry.OccurredAt.Equal(*result.ArchivedAt) {
		t.Fatalf("归档留痕 = %+v", entry)
	}
}

// assertArchiveFailure 核对归档拒绝原因及状态码。
// 参数 t 为测试上下文，mux 为真实业务路由，endpoint 为动作路径，version 为提交版本，
// status 和 code 为期望的 HTTP 状态和业务错误。
// 返回值 无。
func assertArchiveFailure(t *testing.T, mux *http.ServeMux, endpoint string, version int64, status int, code string) {
	t.Helper()
	response := callImplementation(mux, http.MethodPost, endpoint, command{Action: "archive", ExpectedVersion: version})
	if failure := decodeResponse[map[string]string](t, response, status); failure["code"] != code {
		t.Fatalf("归档拒绝结果 = %+v，预期 %s", failure, code)
	}
}

// concurrentImplementationArchive 验证同一版本并发归档只允许一次通过。
// 参数 t 为测试上下文，mux 为业务路由，endpoint 为动作路径，version 为提交版本。
// 返回值 唯一成功的归档结果。
func concurrentImplementationArchive(t *testing.T, mux *http.ServeMux, endpoint string, version int64) mutation {
	t.Helper()
	responses := make(chan *httptest.ResponseRecorder, 2)
	request := command{Action: "archive", ExpectedVersion: version}
	for range 2 {
		go func() { responses <- callImplementation(mux, http.MethodPost, endpoint, request) }()
	}
	first, second := <-responses, <-responses
	if first.Code != http.StatusOK {
		first, second = second, first
	}
	result := decodeResponse[mutation](t, first, 200)
	failure := decodeResponse[map[string]string](t, second, 409)
	if result.Version != version+1 || failure["code"] != "stale_version" {
		t.Fatalf("并发归档结果 = %+v, %+v", result, failure)
	}
	return result
}

// assertArchivePage 核对归档列表按条件返回正确顺序和归档时间。
// 参数 t 为测试上下文，mux 为真实业务路由，query 为查询条件，ids 为预期返回的记录标识。
// 返回值 服务端给出的下一页游标。
func assertArchivePage(t *testing.T, mux *http.ServeMux, query string, ids ...string) string {
	t.Helper()
	result := decodeResponse[page](t, callImplementation(mux, http.MethodGet, path+query, nil), 200)
	got := make([]string, 0, len(result.Items))
	for _, row := range result.Items {
		got = append(got, row.ID)
		if query != "" && (row.ArchivedAt != nil) != strings.HasPrefix(query, "?archive=archived") {
			t.Fatalf("条件 %s 返回不一致的归档时间：%+v", query, row)
		}
	}
	if !slices.Equal(got, ids) {
		t.Fatalf("条件 %s 返回 %v, 预期 %v", query, got, ids)
	}
	return result.NextCursor
}
