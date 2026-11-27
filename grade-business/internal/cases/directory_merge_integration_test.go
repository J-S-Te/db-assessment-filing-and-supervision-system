package cases

import (
	"net/http"
	"testing"

	"grade-business/internal/directory"
)

// TestMergedFilingShowsCurrentOwner 核对合并备案的当前归属、运营者权限以及旧目录快照。
// 参数 t 为共用实际合并与备案 HTTP 路由的隔离数据库测试上下文。
// 返回值 无。
func TestMergedFilingShowsCurrentOwner(t *testing.T) {
	service, handler := operatorFilingFixture(t)
	mux := handler.(*http.ServeMux)
	directory.Register(mux, service.db)
	const source, target = "旧归属单位", "新归属单位"
	sourceID := createMergeCaseUnit(t, mux, source, "913300001000002001")
	targetID := createMergeCaseUnit(t, mux, target, "913300001000002002")
	payload := mergeHistoryImportZip(t, source, "BA-MERGE-OWNER")
	created := readResponse[historyImportTestResponse](t, historyImportUpload(t, mux, "/commit", payload), 200)
	if len(created.Rows) != 1 || created.Rows[0].FilingID == "" {
		t.Fatalf("历史备案导入失败: %+v", created)
	}
	filingID := created.Rows[0].FilingID
	path := filingsURL + "/" + filingID
	submitted := readResponse[mutation](t, requestJSON(mux, "POST", path+"/submit", versionInput{3}), 200)
	initial := readResponse[mutation](t, reviewRequest(mux, path, approve, "", submitted.Version), 200)
	readResponse[mutation](t, reviewRequest(mux, path, approve, "", initial.Version), 200)
	mergeCaseOwner(t, mux, sourceID, targetID)
	assertMergedFilingOwner(t, mux, filingID, source, target)
	input := sampleFiling("BA-MERGE-REJECT")
	input.OrganizationName = source
	assertError(t, requestJSON(mux, "POST", filingsURL, input), 409, "filing_unit_merged")
	assertMergedHistoryImportRejected(t, mux, source)
}

// createMergeCaseUnit 从监管名录入口创建实际测试单位。
// 参数 t、mux 为测试上下文与业务路由，name、code 为测试单位名称和编码。
// 返回值 业务目录存储的单位标识。
func createMergeCaseUnit(t *testing.T, mux http.Handler, name, code string) string {
	t.Helper()
	return readResponse[struct{ ID string }](t, requestJSON(mux, "POST", "/api/v1/directory/units",
		map[string]string{"name": name, "code": code}), 201).ID
}

// mergeCaseOwner 按真实监管操作先预览再合并两个单位。
// 参数 t、mux 为测试上下文与业务路由，sourceID、targetID 为有效单位标识。
// 返回值 无。
func mergeCaseOwner(t *testing.T, mux http.Handler, sourceID, targetID string) {
	t.Helper()
	const endpoint = "/api/v1/directory/unit-merges"
	input := map[string]any{"sourceIds": []string{sourceID}, "targetId": targetID}
	plan := readResponse[struct {
		Token    string
		Blockers []struct{ Code string }
	}](t, requestJSON(mux, "POST", endpoint+"/preview", input), 200)
	if plan.Token == "" {
		t.Fatalf("测试前置条件阻止合并: %+v", plan.Blockers)
	}
	input["token"] = plan.Token
	result := readResponse[struct{ MovedCount int }](t, requestJSON(mux, "POST", endpoint, input), 200)
	if result.MovedCount != 1 {
		t.Fatalf("预期合并一条备案: %+v", result)
	}
}

// assertMergedFilingOwner 同时核对监管详情和运营者列表中的新归属及原始留痕。
// 参数 t、mux 为测试上下文与路由，id、source、target 为备案及合并前后单位名称。
// 返回值 无。
func assertMergedFilingOwner(t *testing.T, mux http.Handler, id, source, target string) {
	t.Helper()
	detail := readResponse[recordDetail](t, requestJSON(mux, "GET", filingsURL+"/"+id, nil), 200)
	if detail.OrganizationName != target || detail.DirectorySnapshot == nil ||
		detail.DirectorySnapshot.Unit == nil || detail.DirectorySnapshot.Unit.ScopeName != source {
		t.Fatalf("当前归属或原始名录快照发生偏差: %+v", detail)
	}
	list := readResponse[listResponse](t, requestJSON(mux, "GET", operatorFilingsURL+operatorFilingQuery(target), nil), 200)
	if len(list.Items) != 1 || list.Items[0].ID != id || list.Items[0].OrganizationName != target {
		t.Fatalf("新归属列表和备案详情未同步: %+v", list)
	}
	assertError(t, requestJSON(mux, "GET", operatorFilingsURL+"/"+id+operatorFilingQuery(source), nil), 404, "not_found")
}

// assertMergedHistoryImportRejected 核对导入预检和确认均阻止已归并单位新增备案。
// 参数 t、mux 为测试上下文与路由，source 为已经归并的单位历史范围。
// 返回值 无。
func assertMergedHistoryImportRejected(t *testing.T, mux http.Handler, source string) {
	t.Helper()
	payload := mergeHistoryImportZip(t, source, "BA-MERGED-HISTORY")
	for _, operation := range []string{"/preview", "/commit"} {
		result := readResponse[historyImportTestResponse](t, historyImportUpload(t, mux, operation, payload), 200)
		if result.Counts["conflict"] != 1 || len(result.Rows) != 1 || result.Rows[0].FilingID != "" {
			t.Fatalf("%s 未阻止使用已归并的历史单位: %+v", operation, result)
		}
	}
}

// mergeHistoryImportZip 为归并回归构造能保存原始归属快照的真实导入包。
// 参数 t 为测试上下文，source、number 为拟导入的源单位及备案号。
// 返回值 包含两种业务材料的完整 ZIP。
func mergeHistoryImportZip(t *testing.T, source, number string) []byte {
	t.Helper()
	return historyImportTestZip(t, map[string]string{
		"manifest.csv": "number,systemName,organizationName,assessmentOrg,level,filingForm,classificationReport\n" +
			number + ",旧单位系统," + source + ",联验机构,2,a.pdf,b.pdf\n",
		"a.pdf": filingPackagePDF, "b.pdf": filingPackagePDF,
	})
}

// TestComplexMergePreservesFilingOriginals 核对复杂归并后旧材料、审核和回执仅留原收件范围。
// 参数 t 为隔离 PostgreSQL 和附件存储测试上下文。
// 返回值 无。
func TestComplexMergePreservesFilingOriginals(t *testing.T) {
	service, handler := operatorFilingFixture(t)
	mux := handler.(*http.ServeMux)
	directory.Register(mux, service.db)
	const source, target = "复杂归并文件原单位", "复杂归并文件接续单位"
	sourceID := createMergeCaseUnit(t, mux, source, "913300001000002011")
	targetID := createMergeCaseUnit(t, mux, target, "913300001000002012")
	payload := mergeHistoryImportZip(t, source, "BA-COMPLEX-ORIGINAL")
	created := readResponse[historyImportTestResponse](t, historyImportUpload(t, mux, "/commit", payload), 200)
	filingID := created.Rows[0].FilingID
	path := filingsURL + "/" + filingID
	submitted := readResponse[mutation](t, requestJSON(mux, "POST", path+"/submit", versionInput{3}), 200)
	initial := readResponse[mutation](t, reviewRequest(mux, path, approve, "", submitted.Version), 200)
	readResponse[mutation](t, reviewRequest(mux, path, approve, "", initial.Version), 200)
	const endpoint = "/api/v1/directory/unit-merges"
	input := map[string]any{"sourceIds": []string{sourceID}, "targetId": targetID, "mode": "complex"}
	plan := readResponse[struct{ Token string }](t, requestJSON(mux, "POST", endpoint+"/preview", input), 200)
	input["token"] = plan.Token
	readResponse[struct{ ID string }](t, requestJSON(mux, "POST", endpoint, input), 200)
	assertComplexFilingHistory(t, mux, filingID, source, target)
}

// assertComplexFilingHistory 核对旧单位能够读取原件、回执与审核，新单位仅能读取当前备案。
// 参数 t、mux 提供测试路由，filingID、source、target 为已完成归并的体系。
// 返回值 无。
func assertComplexFilingHistory(t *testing.T, mux http.Handler, filingID, source, target string) {
	t.Helper()
	path := operatorFilingsURL + "/" + filingID
	oldQuery, newQuery := operatorFilingQuery(source), operatorFilingQuery(target)
	old := readResponse[itemResponse[Attachment]](t, requestJSON(mux, "GET", path+"/attachments"+oldQuery, nil), 200)
	current := readResponse[itemResponse[Attachment]](t, requestJSON(mux, "GET", path+"/attachments"+newQuery, nil), 200)
	if len(old.Items) != 2 || len(current.Items) != 0 {
		t.Fatalf("原件范围改变：原单位 %v，主单位 %v", old, current)
	}
	itemURL := path + "/attachments/" + old.Items[0].ID
	if response := requestJSON(mux, "GET", itemURL+oldQuery, nil); response.Code != 200 ||
		response.Body.String() != filingPackagePDF {
		t.Fatalf("原单位真实材料无法打开：%d / %q", response.Code, response.Body.String())
	}
	assertError(t, requestJSON(mux, "GET", itemURL+newQuery, nil), 404, "not_found")
	history := path + "/review-history"
	oldHistory := readResponse[itemResponse[reviewEvent]](t, requestJSON(mux, "GET", history+oldQuery, nil), 200)
	newHistory := readResponse[itemResponse[reviewEvent]](t, requestJSON(mux, "GET", history+newQuery, nil), 200)
	if len(oldHistory.Items) != 3 || len(newHistory.Items) != 0 {
		t.Fatalf("审核范围改变：原单位 %v，主单位 %v", oldHistory, newHistory)
	}
	if response := requestJSON(mux, "GET", path+"/receipt"+oldQuery, nil); response.Code != 200 {
		t.Fatalf("原收件单位无法下载原回执：%d", response.Code)
	}
	assertError(t, requestJSON(mux, "GET", path+"/receipt"+newQuery, nil), 409, "receipt_unavailable")
}
