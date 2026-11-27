package remediations

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"grade-business/internal/cases"
	"grade-business/internal/directory"
)

// TestComplexMergeKeepsCompletedRemediationEvidence 测试已审材料留在原范围，未提交草稿可由主单位继续编辑。
// 参数 t 为隔离 PostgreSQL、真实路由和材料存储测试上下文。
// 返回值 无。
func TestComplexMergeKeepsCompletedRemediationEvidence(t *testing.T) {
	pool, mux := remediationFixture(t)
	directory.Register(mux.(*http.ServeMux), pool)
	report, draftReport := insertReports(t, pool)
	if _, err := pool.Exec(t.Context(), `UPDATE business_cases SET discarded_at=now() WHERE id=$1`, draftReport); err != nil {
		t.Fatal(err)
	}
	const original, successor = "测试单位", "接续整改主单位"
	const unitsPath = "/api/v1/directory/units"
	type savedUnit struct{ ID string }
	old := response[savedUnit](t, call(mux, "POST", unitsPath,
		map[string]string{"name": original, "code": "913300001000001091"}), 201)
	next := response[savedUnit](t, call(mux, "POST", unitsPath,
		map[string]string{"name": successor, "code": "913300001000001092"}), 201)
	const merges = "/api/v1/directory/unit-merges"
	item := newPlanInput(report)
	approvedPlan := createCompletedPlanForHandoff(t, mux, item)
	draftPlan := response[mutation](t, call(mux, "POST", rootURL, item), 201)
	mergeBody := map[string]any{"targetId": next.ID, "sourceIds": []string{old.ID}, "mode": "complex"}
	preview := response[struct {
		Token    string
		Blockers []struct{ Code, Message string }
	}](t, call(mux, "POST", merges+"/preview", mergeBody), 200)
	if preview.Token == "" {
		t.Fatalf("完成整改及空白草稿阻止合并：%+v", preview.Blockers)
	}
	mergeBody["token"] = preview.Token
	response[struct{ ID string }](t, call(mux, "POST", merges, mergeBody), 200)
	if _, err := pool.Exec(t.Context(), `UPDATE business_cases SET
		number='后续备案编号',system_name='后续系统名称'
		WHERE kind='filing' AND organization_name=$1`, successor); err != nil {
		t.Fatal(err)
	}
	assertRemediationHandoff(t, mux, approvedPlan, draftPlan.ID, successor, item)
}

// createCompletedPlanForHandoff 通过常规审核路由办结含真实存储原件的整改规划。
// 参数 t、mux、item 为测试环境、办理路由、已关联批准报告的输入。
// 返回值 已办结的整改规划标识。
func createCompletedPlanForHandoff(t *testing.T, mux http.Handler, item planInput) string {
	t.Helper()
	created := response[mutation](t, call(mux, "POST", rootURL, item), 201)
	path := rootURL + "/" + created.ID
	for _, kind := range []string{"plan", "responsibility", "schedule"} {
		response[cases.Attachment](t, upload(mux, path, kind, samplePDF, item.ReportID), 201)
	}
	submitted := response[mutation](t, call(mux, "POST", path+"/submit", submitInput{4}), 200)
	response[mutation](t, call(mux, "POST", path+"/review-actions", reviewInput{
		Decision: "approve", Deadline: businessDate(8), Comment: "隔离测试审定",
		ExpectedVersion: submitted.Version,
	}), 200)
	return created.ID
}

// assertRemediationHandoff 核对已办规划材料、原收件范围及新承办草稿的真实可编辑版本。
// 参数 t、mux、approvedID、draftID、successor、item 为权限前置、两个工作事项与续编内容。
// 返回值 无。
func assertRemediationHandoff(t *testing.T, mux http.Handler, approvedID, draftID, successor string, item planInput) {
	t.Helper()
	oldScope, newScope := "?organization="+url.QueryEscape("测试单位"), "?organization="+url.QueryEscape(successor)
	path := operatorRoot + "/" + approvedID
	old := response[detail](t, call(mux, "GET", path+oldScope, nil), 200)
	receipt := call(mux, "GET", path+"/receipt"+oldScope, nil)
	if old.FilingNumber != "BA-QA" || old.SystemName != "测试系统" || receipt.Code != 200 ||
		!strings.Contains(receipt.Body.String(), "备案编号：BA-QA") ||
		!strings.Contains(receipt.Body.String(), "系统名称：测试系统") {
		t.Fatalf("原已办详情或回执出现变更后的编号、名称：%+v / %q", old, receipt.Body.String())
	}
	oldFiles := response[collection[cases.Attachment]](t, call(mux, "GET", path+"/attachments"+oldScope, nil), 200)
	if len(oldFiles.Items) != 3 {
		t.Fatalf("原单位失去整改文件：%+v", oldFiles)
	}
	fileURL := path + "/attachments/" + oldFiles.Items[0].ID
	if file := call(mux, "GET", fileURL+oldScope, nil); file.Code != 200 || file.Body.String() != samplePDF {
		t.Fatalf("原整改文件无法下载：%d %q", file.Code, file.Body.String())
	}
	response[map[string]string](t, call(mux, "GET", fileURL+newScope, nil), 404)
	draftPath := operatorRoot + "/" + draftID
	current := response[detail](t, call(mux, "GET", draftPath+newScope, nil), 200)
	item.ExpectedVersion = current.Version
	item.Risks[0].Responsibility = "主单位承接组"
	response[mutation](t, call(mux, "PATCH", draftPath+newScope, item), 200)
	response[map[string]string](t, call(mux, "GET", draftPath+oldScope, nil), 404)
}
