package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"grade-business/internal/access"
)

type isolatedWorkflow struct {
	hit                                     requestAccess
	handler                                 http.Handler
	admin, regulator, institution, operator *http.Cookie
	due                                     string
}

type workflowVersion struct {
	ID      string
	Status  string
	Version int64
}

// TestFourRoleWorkflowOnIsolatedDB 通过完整应用路由跑通同一备案的跨角色办理链。
// 参数 t 管理隔离数据库、合成账号和临时附件。
// 返回值 无。
func TestFourRoleWorkflowOnIsolatedDB(t *testing.T) {
	hit, db, handler := implementationAccessEnvironment(t)
	unit, _, org, _ := accessSubjects(t, db)
	w := isolatedWorkflow{
		hit: hit, handler: handler,
		admin:       implementationReviewer(t, hit, db, "workflow-admin", "", access.SystemAdmin),
		regulator:   implementationReviewer(t, hit, db, "workflow-regulator", "331002", access.Regulator),
		institution: loginImplementationSubject(t, hit, db, "workflow-institution", org, access.OrgAdmin),
		operator:    loginImplementationSubject(t, hit, db, "workflow-operator", unit, access.Operator),
	}
	var reporter string
	err := db.QueryRow(t.Context(), `INSERT INTO directory_people(institution_id,name,code)
		VALUES ($1,'联测报备人员','WORKFLOW-1') RETURNING id::text`, org).Scan(&reporter)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(t.Context(), `SELECT ((now() AT TIME ZONE 'Asia/Shanghai')::date + 8)::text`).Scan(&w.due); err != nil {
		t.Fatal(err)
	}
	assertOperatorFilingWorkflow(t, handler, w.admin, w.regulator, w.institution, w.operator)
	var filing string
	if err := db.QueryRow(t.Context(), "SELECT id::text FROM business_cases WHERE number='BA-ACCESS-001'").Scan(&filing); err != nil {
		t.Fatal(err)
	}
	w.implementation(t, filing, reporter)
	report := w.assessment(t, filing, reporter)
	plan := w.remediation(t, report)
	neighbor := implementationReviewer(t, hit, db, "workflow-neighbor", "331003", access.Regulator)
	w.remediationFiles(t, plan, neighbor)
	hazard := firstWorkflowItem(t, w.hit(w.regulator, "GET", "/api/v1/hazards?planId="+plan, nil, 200))
	w.alert(t, hazard.ID)
	w.closeHazard(t, hazard.ID)
	w.supervision(t, filing)
	w.cancelFiling(t, filing)
}

// decodeWorkflow 从正式的业务响应读取标识、版本和状态。
// 参数 t 为联测上下文，response 为实际响应，status 为预期业务状态。
// 返回值 已验证的记录标识和版本。
func decodeWorkflow(t *testing.T, response *httptest.ResponseRecorder, status string) workflowVersion {
	t.Helper()
	var value workflowVersion
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil ||
		value.ID == "" || value.Version < 1 || (status != "" && value.Status != status) {
		t.Fatalf("预期状态 %q 且有记录标识与版本，实际 %s", status, response.Body.String())
	}
	return value
}

// firstWorkflowItem 获取本轮操作唯一匹配的一条台账业务记录。
// 参数 t 为联测上下文，response 为按上游标识筛选的真实列表响应。
// 返回值 匹配记录，没有唯一记录时当前用例失败。
func firstWorkflowItem(t *testing.T, response *httptest.ResponseRecorder) workflowVersion {
	t.Helper()
	var list struct{ Items []workflowVersion }
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil ||
		len(list.Items) != 1 || list.Items[0].ID == "" {
		t.Fatalf("当前记录应有一个对应台账条目，实际 %s", response.Body.String())
	}
	return list.Items[0]
}

// implementation 核对机构首报、监管初审和管理员终审的真实会话与持久化结果。
// 参数 t 为联测上下文，filing、reporter 为已办结的备案和归属机构的报备人员。
// 返回值 无。
func (w isolatedWorkflow) implementation(t *testing.T, filing, reporter string) {
	t.Helper()
	const base = "/api/v1/institution/implementations"
	draft := decodeWorkflow(t, w.hit(w.institution, "POST", base, map[string]string{
		"filingId": filing, "reporterId": reporter, "reporter": "联测报备人员",
		"type": "routine", "plannedStart": w.due, "plannedEnd": w.due, "plan": "隔离联测实施计划",
	}, 201), "draft")
	local := base + "/" + draft.ID
	sent := w.runAction(t, w.institution, local, "submit", "", draft.Version, "submitted")
	reg := "/api/v1/implementations/" + draft.ID
	first := w.runAction(t, w.regulator, reg, "accept", "", sent.Version, "pending_initial")
	final := w.runAction(t, w.regulator, reg, "approve", "", first.Version, "pending_final")
	implementationAction(w.hit, w.regulator, reg, "approve", "", int(final.Version), 409)
	assertImplementationReviewActions(t, w.hit, w.regulator, reg, "return_for_correction", "approve")
	check := w.runAction(t, w.admin, reg, "approve", "", final.Version, "pending_supervision")
	w.runAction(t, w.regulator, reg, "approve", "", check.Version, "approved")
	decodeWorkflow(t, w.hit(w.institution, "GET", local, nil, 200), "approved")
	t.Log("机构实施报备完成，读取到监管初审与管理员终审后的通过状态")
}

// assessment 由测评机构上传正式提交所需材料，再由不同管理端账号审核。
// 参数 t 为联测上下文，filing、reporter 为正式备案与负责人的标识。
// 返回值 已通过的测评报告标识。
func (w isolatedWorkflow) assessment(t *testing.T, filing, reporter string) string {
	t.Helper()
	const base = "/api/v1/institution/reports"
	draft := decodeWorkflow(t, w.hit(w.institution, "POST", base, map[string]string{
		"number": "BG-ISOLATED-001", "filingId": filing, "risk": "high", "assessmentPersonId": reporter,
	}, 201), "draft")
	local := base + "/" + draft.ID
	kinds := []string{"report_file", "remediation_plan", "assessment_conclusion", "issues_recommendations", "cover_basic_statement"}
	for _, kind := range kinds {
		uploadWorkflowMaterial(t, w.handler, w.institution, local+"/attachments", kind)
	}
	sent := decodeWorkflow(t, w.hit(w.institution, "POST", local+"/submit",
		map[string]int64{"expectedVersion": draft.Version + int64(len(kinds))}, 200), "pending_initial")
	path := "/api/v1/reports/" + draft.ID + "/review-actions"
	first := w.review(t, w.regulator, path, sent.Version, "approve", "pending_final", nil)
	w.review(t, w.admin, path, first.Version, "approve", "approved", nil)
	decodeWorkflow(t, w.hit(w.institution, "GET", local, nil, 200), "approved")
	t.Log("机构测评材料、监管初审与独立终审均已通过")
	return draft.ID
}

// review 提交一条带必需业务字段和乐观版本的审批决定。
// 参数 t、actor、path 为当前联测和办理会话地址，version、decision、status、fields 为业务表单和预期状态。
// 返回值 已更新的业务记录标识和版本。
func (w isolatedWorkflow) review(t *testing.T, actor *http.Cookie, path string,
	version int64, decision, status string, fields map[string]string,
) workflowVersion {
	t.Helper()
	input := map[string]any{"decision": decision, "expectedVersion": version}
	for key, value := range fields {
		input[key] = value
	}
	return decodeWorkflow(t, w.hit(actor, "POST", path, input, 200), status)
}

// remediation 核对运营者整改规划、补正与再次提交，确认监管通过后生成真实台账。
// 参数 t 为联测上下文，report 为同一备案下通过的报告。
// 返回值 已通过的整改规划标识。
func (w isolatedWorkflow) remediation(t *testing.T, report string) string {
	t.Helper()
	const base = "/api/v1/operator/remediations"
	draft := decodeWorkflow(t, w.hit(w.operator, "POST", base, map[string]any{
		"reportId": report, "deadline": w.due,
		"risks": []map[string]string{{"risk": "high", "description": "权限清理", "measure": "复核最小权限", "responsibility": "值守班组", "deadline": w.due}},
	}, 201), "draft")
	local := base + "/" + draft.ID
	for _, kind := range []string{"plan", "responsibility", "schedule"} {
		w.upload(t, w.operator, local, kind, map[string]string{"reportId": report}, 0)
	}
	sent := decodeWorkflow(t, w.hit(w.operator, "POST", local+"/submit",
		map[string]int64{"expectedVersion": draft.Version + 3}, 200), "pending_review")
	path := "/api/v1/remediations/" + draft.ID + "/review-actions"
	back := w.review(t, w.regulator, path, sent.Version, "return_for_correction", "returned",
		map[string]string{"deadline": w.due, "comment": "补充分工", "reason": "核对责任人"})
	resubmitted := decodeWorkflow(t, w.hit(w.operator, "POST", local+"/submit",
		map[string]int64{"expectedVersion": back.Version}, 200), "pending_review")
	w.review(t, w.regulator, path, resubmitted.Version, "approve", "approved", map[string]string{"deadline": w.due, "comment": "复核通过"})
	decodeWorkflow(t, w.hit(w.operator, "GET", local, nil, 200), "approved")
	t.Log("整改规划退回、重提、辖区批准和落库通过")
	return draft.ID
}

// remediationFiles 验证监管员只能读取自己辖区的整改规划附件与下载内容。
// 参数 t、plan 为联测和计划标识，neighbor 为不负责该记录的已认证监管员。
// 返回值 无。
func (w isolatedWorkflow) remediationFiles(t *testing.T, plan string, neighbor *http.Cookie) {
	t.Helper()
	path := "/api/v1/remediations/" + plan + "/attachments"
	result := w.hit(w.regulator, "GET", path, nil, 200)
	var files struct{ Items []struct{ ID string } }
	if err := json.Unmarshal(result.Body.Bytes(), &files); err != nil || len(files.Items) != 3 {
		t.Fatalf("辖区规划未返回三类已登记材料: %s", result.Body.String())
	}
	download := path + "/" + files.Items[0].ID
	if file := w.hit(w.regulator, "GET", download, nil, 200); !strings.HasPrefix(file.Body.String(), "%PDF-") {
		t.Fatal("辖区整改规划下载未返回实际材料")
	}
	w.hit(neighbor, "GET", path, nil, 404)
	w.hit(neighbor, "GET", download, nil, 404)
}

// upload 通过实际附件路由上传合成 PDF，按业务需要携带报告关联或当前版本。
// 参数 t、actor、path、kind 为会话、记录及材料种类，fields 为附加表单字段，version 为可选强版本条件。
// 返回值 无。
func (w isolatedWorkflow) upload(t *testing.T, actor *http.Cookie, path, kind string,
	fields map[string]string, version int64,
) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	write := func() error {
		if err := form.WriteField("kind", kind); err != nil {
			return err
		}
		for key, value := range fields {
			if err := form.WriteField(key, value); err != nil {
				return err
			}
		}
		file, err := form.CreateFormFile("file", "联测材料.pdf")
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(file, "%%PDF-1.7\n%% isolated role workflow %s\n%%%%EOF\n", kind); err != nil {
			return err
		}
		return form.Close()
	}
	if err := write(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18549"+path+"/attachments", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	if version != 0 {
		req.Header.Set("If-Match", strconv.Quote(strconv.FormatInt(version, 10)))
	}
	req.AddCookie(actor)
	res := httptest.NewRecorder()
	w.handler.ServeHTTP(res, req)
	if res.Code != 201 {
		t.Fatalf("上传 %s 到 %s，实际 %d: %s", kind, path, res.Code, res.Body.String())
	}
}

// runAction 执行带版本条件的报备以外业务动作。
// 参数 t、actor、path、action、note、version 为办理上下文和请求，status 为成功后的业务状态。
// 返回值 可用于下一步办理的持久化版本。
func (w isolatedWorkflow) runAction(t *testing.T, actor *http.Cookie, path, action, note string,
	version int64, status string,
) workflowVersion {
	t.Helper()
	return decodeWorkflow(t, w.hit(actor, "POST", path+"/actions",
		map[string]any{"action": action, "comment": note, "expectedVersion": version}, 200), status)
}

// alert 从已入账的高风险隐患生成联动预警并办理运营者延期申请。
// 参数 t 为联测上下文，hazard 为当前整改规划生成的隐患标识。
// 返回值 无。
func (w isolatedWorkflow) alert(t *testing.T, hazard string) {
	t.Helper()
	const base = "/api/v1/alerts"
	w.hit(w.admin, "POST", base+"/sync", nil, 200)
	response := w.hit(w.regulator, "GET", base, nil, 200)
	var list struct {
		Items []struct {
			ID, HazardID string
			Version      int64
		}
	}
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil ||
		len(list.Items) != 1 || list.Items[0].HazardID != hazard {
		t.Fatalf("本区高风险预警未与台账一致: %s", response.Body.String())
	}
	item := list.Items[0]
	operatorPath := "/api/v1/operator/alerts/" + item.ID
	currentAlert := decodeWorkflow(t, w.hit(w.operator, "GET", operatorPath, nil, 200), "pending")
	due, err := time.Parse(time.DateOnly, w.due)
	if err != nil {
		t.Fatal(err)
	}
	requested := w.hit(w.operator, "POST", operatorPath+"/actions", map[string]any{
		"action": "request_extension", "expectedVersion": currentAlert.Version, "comment": "申请重新安排工期",
		"requestedDeadline": due.AddDate(0, 0, 10).Format(time.DateOnly),
	}, 200)
	current := decodeWorkflow(t, requested, "pending")
	approved := w.hit(w.regulator, "POST", base+"/"+item.ID+"/actions", map[string]any{
		"action": "approve", "expectedVersion": current.Version, "comment": "确认延期计划",
	}, 200)
	var result struct{ Disposition struct{ Status string } }
	if err := json.Unmarshal(approved.Body.Bytes(), &result); err != nil || result.Disposition.Status != "approved" {
		t.Fatalf("预警延期审核未落库: %s", approved.Body.String())
	}
	w.hit(w.regulator, "GET", base+"/summary", nil, 200)
	t.Log("高风险预警被真实生成，运营者延期由辖区监管审核")
}

// closeHazard 执行运营者整改、机构复测及监管销号并核对站内回执。
// 参数 t、id 为联测上下文和当前关联隐患标识。
// 返回值 无。
func (w isolatedWorkflow) closeHazard(t *testing.T, id string) {
	t.Helper()
	operator := "/api/v1/operator/hazards/" + id
	org := "/api/v1/institution/hazards/" + id
	reg := "/api/v1/hazards/" + id
	current := decodeWorkflow(t, w.hit(w.operator, "GET", operator, nil, 200), "open")
	w.upload(t, w.operator, operator, "evidence", nil, current.Version)
	sent := w.runAction(t, w.operator, operator, "submit", "完成整改", current.Version+1, "pending_retest")
	w.upload(t, w.institution, org, "retest_report", nil, sent.Version)
	reviewed := w.runAction(t, w.institution, org, "retest_pass", "复测通过", sent.Version+1, "pending_review")
	w.runAction(t, w.regulator, reg, "close", "核对证据后销号", reviewed.Version, "closed")
	decodeWorkflow(t, w.hit(w.operator, "GET", operator, nil, 200), "closed")
	feedback := w.hit(w.regulator, "GET", "/api/v1/review-feedback/hazards/"+id, nil, 200).Body.String()
	if !strings.Contains(feedback, "closed") {
		t.Fatalf("未发现销号站内回执: %s", feedback)
	}
	t.Log("运营者提交证据、机构复测、辖区监管销号及站内回执通过")
}

// supervision 核对监管创建和下发监督任务、运营者回传材料和监管归档。
// 参数 t、filing 为联测上下文和同一业务备案标识。
// 返回值 无。
func (w isolatedWorkflow) supervision(t *testing.T, filing string) {
	t.Helper()
	const base = "/api/v1/supervisions"
	item := decodeWorkflow(t, w.hit(w.regulator, "POST", base, map[string]string{
		"requestId": "3dc7fa32-5a68-4a86-a9b4-9824d9064e4f", "filingId": filing,
		"title": "辖区年度自查", "dueOn": w.due,
	}, 201), "draft")
	reg := base + "/" + item.ID
	op := "/api/v1/operator/supervisions/" + item.ID
	w.upload(t, w.regulator, reg, "inspection_template", map[string]string{"expectedVersion": "1"}, 0)
	w.upload(t, w.regulator, reg, "summary_template", map[string]string{"expectedVersion": "2"}, 0)
	open := w.runAction(t, w.regulator, reg, "publish", "", 3, "open")
	w.upload(t, w.operator, op, "inspection_form", map[string]string{"expectedVersion": strconv.FormatInt(open.Version, 10)}, 0)
	w.upload(t, w.operator, op, "rectification_summary", map[string]string{"expectedVersion": strconv.FormatInt(open.Version+1, 10)}, 0)
	sent := w.runAction(t, w.operator, op, "submit", "", open.Version+2, "submitted")
	w.runAction(t, w.regulator, reg, "approve", "辖区自查通过", sent.Version, "approved")
	decodeWorkflow(t, w.hit(w.operator, "GET", op, nil, 200), "approved")
	t.Log("辖区监督任务下发、运营端回传附件和审核通过")
}

// cancelFiling 核对同一备案的注销申请在两级审批完成后向运营者反馈。
// 参数 t、filing 为联测上下文和已经完成业务办理的备案标识。
// 返回值 无。
func (w isolatedWorkflow) cancelFiling(t *testing.T, filing string) {
	t.Helper()
	const base = "/api/v1/operator/filing-applications"
	item := decodeWorkflow(t, w.hit(w.operator, "POST", base, map[string]string{
		"filingId": filing, "type": "cancel", "reason": "联测系统已下线",
	}, 201), "draft")
	local := base + "/" + item.ID
	uploadWorkflowMaterial(t, w.handler, w.operator, local+"/attachments", "cancellation_statement")
	sent := decodeWorkflow(t, w.hit(w.operator, "POST", local+"/submit",
		map[string]int64{"expectedVersion": item.Version + 1}, 200), "pending_initial")
	path := "/api/v1/filing-applications/" + item.ID + "/review-actions"
	first := w.review(t, w.regulator, path, sent.Version, "approve", "pending_final", nil)
	w.review(t, w.admin, path, first.Version, "approve", "approved", nil)
	decodeWorkflow(t, w.hit(w.operator, "GET", local, nil, 200), "approved")
	t.Log("同备案的注销申请通过、办理结果站内可见")
}
