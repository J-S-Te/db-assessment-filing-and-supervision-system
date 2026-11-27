package supervision

import (
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"
)

// TestBatchSendPinsScopeAndTemplates 核对分批范围、重复投递、模板版本与接收权限。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestBatchSendPinsScopeAndTemplates(t *testing.T) {
	db, router := inspectionFixture(t)
	origin, due := seedInspectionFiling(t, db, "模板来源单位", "S-BATCH-0")
	_, _ = seedInspectionFiling(t, db, "批量接收一", "S-BATCH-1")
	_, _ = seedInspectionFiling(t, db, "已下发单位", "S-BATCH-2")
	_, _ = seedInspectionFiling(t, db, "无关单位", "S-BATCH-3")
	_, err := db.Exec(t.Context(), `INSERT INTO directory_parties(kind,scope_name,name,code,district)
		VALUES('unit','批量接收一','正式目录名称','N00000000000000001','南区'),
		('unit','已下发单位','已下发单位','N00000000000000002','南区'),
		('unit','无关单位','无关单位','N00000000000000003','北区');
		INSERT INTO supervision_tasks(request_id,filing_id,filing_number,system_name,organization_name,
			title,due_on,cycle_year) SELECT gen_random_uuid(),id,number,system_name,organization_name,
			'年度已创建',CURRENT_DATE+10,extract(year from CURRENT_DATE+10)::int
			FROM business_cases WHERE number='S-BATCH-2'`)
	if err != nil {
		t.Fatal(err)
	}
	template := inspectionCall(t, router, "POST", testBase, map[string]any{
		"requestId": sampleRequest, "filingId": origin, "title": "模板来源", "dueOn": due,
	}, 201)
	source := testBase + "/" + template["id"].(string)
	first := inspectionUpload(t, router, source, "", inspectionTemplate, 1, testPDF, 201)
	inspectionUpload(t, router, source, "", summaryTemplate, 2, testPDF, 201)
	input := map[string]any{"district": "南区", "levels": []string{"3"}, "title": "南区年度检查",
		"dueOn": due, "templateTaskId": template["id"], "templateVersion": 3}
	preview := inspectionCall(t, router, "POST", testBase+"/batches/preview", input, 200)
	if preview["totalEligible"] != float64(1) || len(preview["items"].([]any)) != 2 {
		t.Fatalf("批次预览范围错误：%+v", preview)
	}
	input["token"] = preview["token"]
	_, err = db.Exec(t.Context(), `UPDATE directory_parties SET district='南区' WHERE scope_name='无关单位'`)
	if err != nil {
		t.Fatal(err)
	}
	inspectionCall(t, router, "POST", testBase+"/batches", input, 409)
	_, err = db.Exec(t.Context(), `UPDATE directory_parties SET district='北区' WHERE scope_name='无关单位'`)
	if err != nil {
		t.Fatal(err)
	}
	output := inspectionCall(t, router, "POST", testBase+"/batches", input, 201)
	replayed := inspectionCall(t, router, "POST", testBase+"/batches", input, 201)
	if output["id"] != replayed["id"] || output["publishedCount"] != float64(1) {
		t.Fatalf("重复确认发生多次下发：%+v %+v", output, replayed)
	}
	checkBatchPublished(t, router, output, first)
}

// checkBatchPublished 核对实际下发任务的模板和原接收范围。
// 参数 t、router 为测试环境，output 为批次结果，first 为模板文件。
// 返回值 无。
func checkBatchPublished(t *testing.T, router http.Handler, output, first map[string]any) {
	t.Helper()
	for _, raw := range output["items"].([]any) {
		row := raw.(map[string]any)
		if row["status"] != "published" {
			continue
		}
		child := testOperatorBase + "/" + row["taskId"].(string)
		audience := "?organization=" + url.QueryEscape("批量接收一")
		out := inspectionCall(t, router, "GET", child+audience, nil, 200)
		if out["status"] != "open" || len(out["attachments"].([]any)) != 2 {
			t.Fatalf("批量下发模板不可查：%+v", out)
		}
		assertFileStatus(t, router, attachedFile(child, first)+audience, 200)
		assertFileStatus(t, router, attachedFile(child, first)+"?organization="+url.QueryEscape("无关单位"), 404)
		return
	}
	t.Fatal("未找到已发布的子任务")
}

// TestFieldInspectionRequiresEvidenceAndFollowsScope 核对现场计划、实际材料与跟进权限。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestFieldInspectionRequiresEvidenceAndFollowsScope(t *testing.T) {
	db, router := inspectionFixture(t)
	filing, due := seedInspectionFiling(t, db, testOperator, "S-SITE-1")
	item := inspectionCall(t, router, "POST", testBase, map[string]any{
		"requestId": sampleRequest, "filingId": filing, "title": "现场检查验证", "dueOn": due,
	}, 201)
	id := item["id"].(string)
	reg := testBase + "/" + id
	op := testOperatorBase + "/" + id
	scope := "?organization=" + url.QueryEscape(testOperator)
	inspectionUpload(t, router, reg, "", inspectionTemplate, 1, testPDF, 201)
	inspectionUpload(t, router, reg, "", summaryTemplate, 2, testPDF, 201)
	inspectionAction(t, router, reg, "", "publish", 3, 200)
	reg += "/site-inspection"
	op += "/site-inspection"
	scheduled := map[string]any{"action": "schedule", "expectedVersion": 0,
		"visitAt": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339), "location": "登记现场",
		"inspectors": "检查一组"}
	inspectionCall(t, router, "POST", reg+"/actions", scheduled, 200)
	inspectionCall(t, router, "POST", reg+"/actions", scheduled, 200)
	parent := inspectionCall(t, router, "GET", testBase+"/"+id, nil, 200)
	if slices.Contains(parent["allowedActions"].([]any), any("cancel")) {
		t.Fatalf("办理现场检查期间不能提供取消年度自查的入口：%+v", parent)
	}
	inspectionCall(t, router, "GET", op+"?organization="+url.QueryEscape("其他单位"), nil, 404)
	current := inspectionCall(t, router, "GET", op+scope, nil, 200)
	if current["status"] != "scheduled" || current["version"] != float64(1) {
		t.Fatalf("现场检查任务未送达：%+v", current)
	}
	inspectionCall(t, router, "POST", reg+"/actions", map[string]any{
		"action": "record", "expectedVersion": 1, "outcome": "pass", "comment": "现场已检查",
	}, 422)
	result := inspectionUpload(t, router, reg, "", "inspection_evidence", 1, testPDF, 201)
	assertFileStatus(t, router, attachedFile(op, result)+scope, 404)
	inspectionCall(t, router, "POST", reg+"/actions", map[string]any{
		"action": "record", "expectedVersion": 2, "outcome": "pass", "comment": "现场已检查",
	}, 200)
	assertFileStatus(t, router, attachedFile(op, result)+scope, 200)
	closed := inspectionCall(t, router, "POST", reg+"/actions", map[string]any{
		"action": "close", "expectedVersion": 3, "comment": "检查记录已核对",
	}, 200)
	if closed["status"] != "closed" {
		t.Fatalf("检查未办结：%+v", closed)
	}
	parent = inspectionCall(t, router, "GET", testBase+"/"+id, nil, 200)
	if !slices.Contains(parent["allowedActions"].([]any), any("cancel")) {
		t.Fatalf("现场检查办结后取消年度自查入口未恢复：%+v", parent)
	}
}
