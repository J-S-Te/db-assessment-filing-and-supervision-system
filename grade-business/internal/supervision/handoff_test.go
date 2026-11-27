package supervision

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/internal/directory"
)

const nextInspectionOwner = "归并承接单位"

// TestSupervisionHandoffKeepsAnnualEvidenceScoped 核对年度补正可承接且旧资料隔离。
// 参数 t 为隔离数据库、文件和 HTTP 的测试上下文。
// 返回值 无。
func TestSupervisionHandoffKeepsAnnualEvidenceScoped(t *testing.T) {
	db, router := inspectionFixture(t)
	reg, op := openInspectionForHandoff(t, db, router)
	original, successor := inspectionOwnerQuery(testOperator), inspectionOwnerQuery(nextInspectionOwner)
	old := inspectionUpload(t, router, op, original, inspectionForm, 4, testPDF, 201)
	inspectionUpload(t, router, op, original, rectificationForm, 5, testPDF, 201)
	inspectionAction(t, router, op, original, "submit", 6, 200)
	inspectionCall(t, router, "POST", reg+"/actions", map[string]any{
		"action": "return", "expectedVersion": 7, "comment": "原单位补正说明", "correctionKind": rectificationForm,
	}, 200)
	mergeInspectionOwner(t, db, router)
	wantReadOnlyInspection(t, router, op+original, "returned", 8)
	assertFileStatus(t, router, attachedFile(op, old)+original, 200)
	assertFileStatus(t, router, attachedFile(op, old)+successor, 404)
	inspectionUpload(t, router, op, original, inspectionForm, 8, testPDF, 404)
	inspectionAction(t, router, op, original, "submit", 8, 404)
	inspectionAction(t, router, op, successor, "submit", 9, 422)
	newFile := inspectionUpload(t, router, op, successor, inspectionForm, 9, testPDF, 201)
	inspectionUpload(t, router, op, successor, rectificationForm, 10, testPDF, 201)
	inspectionAction(t, router, op, successor, "submit", 11, 200)
	assertFileStatus(t, router, attachedFile(op, newFile)+successor, 200)
	assertFileStatus(t, router, attachedFile(op, newFile)+original, 404)
	wantReadOnlyInspection(t, router, op+original, "returned", 8)
	list := inspectionCall(t, router, "GET", testOperatorBase+original+"&status=returned", nil, 200)
	if len(list["items"].([]any)) != 1 {
		t.Fatalf("按历史阶段检索遗漏原单位任务：%v", list)
	}
	oldHistory := inspectionCall(t, router, "GET", op+"/history"+original, nil, 200)["items"].([]any)
	newHistory := inspectionCall(t, router, "GET", op+"/history"+successor, nil, 200)["items"].([]any)
	if len(oldHistory) != 3 || len(newHistory) != 1 || newHistory[0].(map[string]any)["actor"] != nextInspectionOwner {
		t.Fatalf("年度监督证据历史越过归属：%v / %v", oldHistory, newHistory)
	}
}

// TestSupervisionHandoffKeepsSiteEvidenceScoped 核对现场跟进独立承接且同摘要附件不能跨归属复用。
// 参数 t 为隔离数据库、文件和 HTTP 的测试上下文。
// 返回值 无。
func TestSupervisionHandoffKeepsSiteEvidenceScoped(t *testing.T) {
	db, router := inspectionFixture(t)
	reg, op := openInspectionForHandoff(t, db, router)
	reg, op = reg+"/site-inspection", op+"/site-inspection"
	original, successor := inspectionOwnerQuery(testOperator), inspectionOwnerQuery(nextInspectionOwner)
	prepareFollowingUpForHandoff(t, router, reg)
	inspectionUpload(t, router, op, original, siteFollowUpEvidence, 4, testPDF, 201)
	inspectionAction(t, router, op, original, "submit_follow_up", 5, 200)
	inspectionCall(t, router, "POST", reg+"/actions", map[string]any{
		"action": "return_follow_up", "expectedVersion": 6, "comment": "补充本轮现场证据",
	}, 200)
	old := inspectionUpload(t, router, op, original, siteFollowUpEvidence, 7, testPDF+"\n", 201)
	mergeInspectionOwner(t, db, router)
	wantReadOnlyInspection(t, router, op+original, "following_up", 8)
	assertFileStatus(t, router, attachedFile(op, old)+original, 200)
	assertFileStatus(t, router, attachedFile(op, old)+successor, 404)
	inspectionUpload(t, router, op, original, siteFollowUpEvidence, 8, testPDF, 404)
	inspectionAction(t, router, op, successor, "submit_follow_up", 5, 409)
	inspectionAction(t, router, op, successor, "submit_follow_up", 9, 422)
	newFile := inspectionUpload(t, router, op, successor, siteFollowUpEvidence, 9, testPDF+"\n", 201)
	inspectionAction(t, router, op, successor, "submit_follow_up", 10, 200)
	assertFileStatus(t, router, attachedFile(op, newFile)+successor, 200)
	assertFileStatus(t, router, attachedFile(op, newFile)+original, 404)
	wantReadOnlyInspection(t, router, op+original, "following_up", 8)
	current := inspectionCall(t, router, "GET", op+successor, nil, 200)
	events := current["events"].([]any)
	if current["status"] != "follow_up_submitted" || len(events) != 1 ||
		events[0].(map[string]any)["actor"] != nextInspectionOwner {
		t.Fatalf("现场检查证据范围或续办状态错误：%v", current)
	}
}

// openInspectionForHandoff 创建正式下发的自查检查任务及真实模板。
// 参数 t、db、router 分别为当前测试、专用库及 HTTP 路由。
// 返回值 监管和运营者任务地址。
func openInspectionForHandoff(t *testing.T, db *pgxpool.Pool, router http.Handler) (string, string) {
	t.Helper()
	filing, due := seedInspectionFiling(t, db, testOperator, "S-HANDOFF-1")
	item := inspectionCall(t, router, "POST", testBase, map[string]any{
		"requestId": sampleRequest, "filingId": filing, "title": "归并监督任务", "dueOn": due,
	}, 201)
	id := item["id"].(string)
	reg := testBase + "/" + id
	inspectionUpload(t, router, reg, "", inspectionTemplate, 1, testPDF, 201)
	inspectionUpload(t, router, reg, "", summaryTemplate, 2, testPDF, 201)
	inspectionAction(t, router, reg, "", "publish", 3, 200)
	return reg, testOperatorBase + "/" + id
}

// prepareFollowingUpForHandoff 按真实 HTTP 建立有待提交证据的现场任务。
// 参数 t、router、reg 分别为测试、路由和监管现场检查地址。
// 返回值 无。
func prepareFollowingUpForHandoff(t *testing.T, router http.Handler, reg string) {
	t.Helper()
	inspectionCall(t, router, "POST", reg+"/actions", map[string]any{
		"action": "schedule", "expectedVersion": 0,
		"visitAt": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339), "location": "登记现场", "inspectors": "检查组",
	}, 200)
	inspectionUpload(t, router, reg, "", siteEvidence, 1, testPDF, 201)
	inspectionCall(t, router, "POST", reg+"/actions", map[string]any{
		"action": "record", "expectedVersion": 2, "outcome": "follow_up_required", "comment": "现场原意见",
	}, 200)
	future := time.Now().Add(72 * time.Hour).UTC().Format(time.DateOnly)
	inspectionCall(t, router, "POST", reg+"/actions", map[string]any{
		"action": "follow_up", "expectedVersion": 3, "followUpDueOn": future, "comment": "限期跟进",
	}, 200)
}

// mergeInspectionOwner 通过实际名录路由预览并确认监督任务交接。
// 参数 t、db、router 为测试上下文、专用库和共用业务路由。
// 返回值 无。
func mergeInspectionOwner(t *testing.T, db *pgxpool.Pool, router *http.ServeMux) {
	t.Helper()
	directory.Register(router, db)
	const unitPath = "/api/v1/directory/units"
	const mergePath = "/api/v1/directory/unit-merges"
	original := inspectionCall(t, router, "POST", unitPath, map[string]any{
		"name": testOperator, "code": "913300001000001081",
	}, 201)
	current := inspectionCall(t, router, "POST", unitPath, map[string]any{
		"name": nextInspectionOwner, "code": "913300001000001082",
	}, 201)
	input := map[string]any{"targetId": current["id"], "sourceIds": []any{original["id"]}, "mode": "complex"}
	plan := inspectionCall(t, router, "POST", mergePath+"/preview", input, 200)
	if token, _ := plan["token"].(string); token == "" {
		t.Fatalf("正在办理的监督任务未能进入交接：%v", plan)
	}
	input["token"] = plan["token"]
	inspectionCall(t, router, "POST", mergePath, input, 200)
}

// inspectionOwnerQuery 生成可安全用于测试路径的运营范围查询。
// 参数 owner 为测试任务的原单位或承接单位。
// 返回值 HTTP 范围筛选查询串。
func inspectionOwnerQuery(owner string) string {
	return "?" + url.Values{"organization": {owner}}.Encode()
}

// wantReadOnlyInspection 验证原单位只有移交前的任务快照和读取权限。
// 参数 t、router 是测试上下文，path、status、version 是访问地址及冻结状态版本。
// 返回值 无。
func wantReadOnlyInspection(t *testing.T, router http.Handler, path, status string, version int) {
	t.Helper()
	item := inspectionCall(t, router, "GET", path, nil, 200)
	if item["status"] != status || item["version"] != float64(version) || len(item["allowedActions"].([]any)) != 0 {
		t.Fatalf("历史任务的状态或办理权限错误：%v", item)
	}
}
