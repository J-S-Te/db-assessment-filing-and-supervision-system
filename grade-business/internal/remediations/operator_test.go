package remediations

import (
	"net/http"
	"net/url"
	"slices"
	"testing"

	"grade-business/internal/cases"
)

// TestOperatorRemediationRequiresOrganization 验证企业端详情拒绝缺失、重复或多余的范围参数。
// 参数 t 为 HTTP 合同测试上下文。
// 返回值 无。
func TestOperatorRemediationRequiresOrganization(t *testing.T) {
	mux := http.NewServeMux()
	New(nil, nil, "测试席位").RegisterOperator(mux)
	path := "/api/v1/operator/remediations/a550b84f-a8ec-4f2e-9dfa-49ecb7bc681b"
	for _, query := range []string{"", "?organization=", "?organization=甲&organization=乙", "?organization=甲&other=x"} {
		response[map[string]string](t, call(mux, "GET", path+query, nil), 400)
	}
}

// TestOperatorCorrectsReturnedRemediation 验证企业只可在自己的退回计划中补充材料、编辑和重提。
// 参数 t 为隔离数据库和文件存储测试上下文。
// 返回值 无。
func TestOperatorCorrectsReturnedRemediation(t *testing.T) {
	db, router := remediationFixture(t)
	report, _ := insertReports(t, db)
	input := newPlanInput(report)
	created := response[mutation](t, call(router, "POST", rootURL, input), 201)
	regulator := rootURL + "/" + created.ID
	operator := "/api/v1/operator/remediations/" + created.ID
	owner := "?organization=" + url.QueryEscape("测试单位")
	other := "?organization=" + url.QueryEscape("测试单位分部")
	response[map[string]string](t, call(router, "POST", operator+"/withdraw"+owner, submitInput{1}), 409)
	for _, kind := range []string{"plan", "responsibility", "schedule"} {
		response[cases.Attachment](t, upload(router, regulator, kind, samplePDF, report), 201)
	}
	submitted := response[mutation](t, call(router, "POST", regulator+"/submit", submitInput{4}), 200)
	pending := response[detail](t, call(router, "GET", operator+owner, nil), 200)
	if !slices.Equal(pending.AllowedActions, []string{"withdraw"}) {
		t.Fatalf("pending remediation offered operator actions: %v", pending.AllowedActions)
	}
	returned := response[mutation](t, call(router, "POST", regulator+"/review-actions", reviewInput{
		Decision: "return_for_correction", Deadline: input.Deadline,
		Comment: "核对责任组", Reason: "待补充职责", ExpectedVersion: submitted.Version,
	}), 200)
	var comment string
	err := db.QueryRow(t.Context(), `SELECT comment FROM review_notices
		WHERE audience='operator' AND resource='remediations' AND resource_id=$1
		AND recipient='测试单位' AND state='returned' AND event_version=$2 AND reference='BG-QA'`,
		created.ID, returned.Version).Scan(&comment)
	if err != nil || comment != "待补充职责\n核对责任组" {
		t.Fatalf("incorrect return notice: %q, %v", comment, err)
	}
	insertLaterReport(t, db, report)
	assertOperatorRemediationScope(t, router, operator, owner, other, input, returned.Version)
	if _, err := db.Exec(t.Context(), `UPDATE business_cases SET organization_name='测试单位分部'
		WHERE kind='filing' AND number='BA-QA'`); err != nil {
		t.Fatal(err)
	}
	response[map[string]string](t, call(router, "GET", operator+owner, nil), 404)
	response[detail](t, call(router, "GET", operator+other, nil), 200)
}

// assertOperatorRemediationScope 核对附件、计划变更和重提都受当前备案单位限制。
// 参数 t 为测试上下文，router 为业务路由，path 为企业地址，owner 和 other 为范围查询串，input 和 version 为计划和版本。
// 返回值 无。
func assertOperatorRemediationScope(t *testing.T, router http.Handler, path, owner, other string,
	input planInput, version int64) {
	t.Helper()
	returned := response[detail](t, call(router, "GET", path+owner, nil), 200)
	for _, action := range []string{"edit", "submit"} {
		if !slices.Contains(returned.AllowedActions, action) {
			t.Fatalf("returned remediation omitted %s: %v", action, returned.AllowedActions)
		}
	}
	input.ExpectedVersion = version
	response[map[string]string](t, call(router, "GET", path+other, nil), 404)
	response[map[string]string](t, call(router, "PATCH", path+other, input), 404)
	response[map[string]string](t, call(router, "POST", path+"/submit"+other, submitInput{version}), 404)
	response[map[string]string](t, call(router, "GET", path+"/attachments"+other, nil), 404)
	response[map[string]string](t, upload(router, path+other, "plan", samplePDF, input.ReportID), 404)
	input.Risks[0].Responsibility = "企业安全责任组"
	edited := response[mutation](t, call(router, "PATCH", path+owner, input), 200)
	file := response[cases.Attachment](t, upload(router, path+owner, "plan", samplePDF, input.ReportID), 201)
	files := response[collection[cases.Attachment]](t, call(router, "GET", path+"/attachments"+owner, nil), 200)
	if len(files.Items) != 4 || files.Items[0].ID != file.ID {
		t.Fatalf("incorrect scoped attachments: %+v", files)
	}
	response[map[string]string](t, call(router, "GET", path+"/attachments/"+file.ID+other, nil), 404)
	download := call(router, "GET", path+"/attachments/"+file.ID+owner, nil)
	if download.Code != 200 || download.Body.String() != samplePDF {
		t.Fatalf("wrong scoped download: %d %q", download.Code, download.Body.String())
	}
	response[map[string]string](t, call(router, "POST", path+"/submit"+owner, submitInput{edited.Version}), 409)
	resubmitted := response[mutation](t, call(router, "POST", path+"/submit"+owner, submitInput{edited.Version + 1}), 200)
	if resubmitted.Status != pendingReview || resubmitted.Version != edited.Version+2 {
		t.Fatalf("operator did not resubmit remediation: %+v", resubmitted)
	}
}
