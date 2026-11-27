package hazards

import (
	"net/http"
	"net/url"
	"testing"

	"grade-business/internal/cases"
	"grade-business/internal/directory"
)

const hazardOldUnit = "隐患证据原收件单位"
const hazardNewUnit = "隐患证据承接单位"

// TestComplexMergeSeparatesHazardEvidence 验证旧证据留原范围，承接方必须登记自己的本轮材料。
// 参数 t 为隔离数据库和真实证据目录的测试上下文。
// 返回值 无。
func TestComplexMergeSeparatesHazardEvidence(t *testing.T) {
	service, mux, hazard, input := hazardEvidenceHandoffFixture(t)
	path := operatorBase + "/" + hazard.ID
	oldScope := "?organization=" + url.QueryEscape(hazardOldUnit)
	newScope := "?organization=" + url.QueryEscape(hazardNewUnit)
	evidence := ledgerReply[cases.Attachment](t, ledgerUpload(t, mux, path+oldScope, kindEvidence, fixturePDF, hazard.Version), 201)
	mergeEvidenceHazard(t, mux, input)
	assertTransferredEvidenceScope(t, mux, path, evidence.ID, oldScope, newScope)
	current := ledgerReply[hazardDetail](t, ledgerCall(mux, "GET", path+newScope, nil), 200)
	assertLedgerError(t, ledgerCall(mux, "POST", path+"/actions"+oldScope,
		actionInput{actionSubmit, current.Version, "原单位已失去办理权"}), 404, "not_found")
	assertLedgerError(t, ledgerCall(mux, "POST", path+"/actions"+newScope,
		actionInput{actionSubmit, current.Version, "不得借用旧范围证据"}), 400, "evidence_required")
	ledgerReply[cases.Attachment](t, ledgerUpload(t, mux, path+newScope, kindEvidence, fixturePDF, current.Version), 201)
	result := ledgerReply[hazardMutation](t, ledgerCall(mux, "POST", path+"/actions"+newScope,
		actionInput{actionSubmit, current.Version + 1, "承接单位提交本轮证据"}), 200)
	if result.Status != statusRetest {
		t.Fatalf("承接单位未能正常提交整改：%+v", result)
	}
	assertHazardHandoffNotices(t, service, hazard.ID, hazardOldUnit, hazardNewUnit)
}

// TestComplexMergeKeepsHazardInRetest 验证已送复测事项继续原审核，旧回执保持原备案信息。
// 参数 t 为隔离数据库和真实路由的测试上下文。
// 返回值 无。
func TestComplexMergeKeepsHazardInRetest(t *testing.T) {
	service, mux, hazard, input := hazardEvidenceHandoffFixture(t)
	path := operatorBase + "/" + hazard.ID
	oldScope := "?organization=" + url.QueryEscape(hazardOldUnit)
	newScope := "?organization=" + url.QueryEscape(hazardNewUnit)
	evidence := ledgerReply[cases.Attachment](t, ledgerUpload(t, mux, path+oldScope, kindEvidence, fixturePDF, hazard.Version), 201)
	ledgerReply[hazardMutation](t, ledgerCall(mux, "POST", path+"/actions"+oldScope,
		actionInput{actionSubmit, hazard.Version + 1, "原单位提交有效整改证据"}), 200)
	mergeEvidenceHazard(t, mux, input)
	current := ledgerReply[hazardDetail](t, ledgerCall(mux, "GET", path+newScope, nil), 200)
	if current.Status != statusRetest || current.ReceiptAvailable == nil || *current.ReceiptAvailable {
		t.Fatalf("复测阶段或原收件回执串入新承接单位：%+v", current)
	}
	assertRetestHandoff(t, mux, hazard.ID, evidence.ID, current.Version)
	if _, err := service.db.Exec(t.Context(), `UPDATE business_cases SET number='后续隐患备案编号',
		system_name='后续隐患系统名称' WHERE kind='filing' AND number='CX-HAZARD'`); err != nil {
		t.Fatal(err)
	}
	assertHazardReceipt(t, mux, path+"/receipt"+oldScope, "整改提交回执", "CX-HAZARD")
	assertLedgerError(t, ledgerCall(mux, "GET", path+"/receipt"+newScope, nil), 409, "receipt_unavailable")
}

// hazardEvidenceHandoffFixture 创建两端单位、已派发隐患及复杂归并参数。
// 参数 t 为隔离测试上下文。
// 返回值 完整隐患服务、同源路由、待整改隐患及归并参数。
func hazardEvidenceHandoffFixture(t *testing.T) (*Service, *http.ServeMux, hazardMutation, map[string]string) {
	t.Helper()
	service, handler := ledgerFixture(t)
	mux := handler.(*http.ServeMux)
	directory.Register(mux, service.db)
	type unit struct{ ID string }
	const path = "/api/v1/directory/units"
	old := ledgerReply[unit](t, ledgerCall(mux, "POST", path,
		map[string]string{"name": hazardOldUnit, "code": "913300001000001065"}), 201)
	next := ledgerReply[unit](t, ledgerCall(mux, "POST", path,
		map[string]string{"name": hazardNewUnit, "code": "913300001000001066"}), 201)
	hazard := seedDispatchedHandoffHazard(t, service, mux, hazardOldUnit)
	return service, mux, hazard, map[string]string{"source": old.ID, "target": next.ID}
}

// mergeEvidenceHazard 通过监管预览和确认完成包含隐患证据的复杂归并。
// 参数 t、mux 为测试上下文与真实路由，units 为主从目录标识。
// 返回值 无。
func mergeEvidenceHazard(t *testing.T, mux http.Handler, units map[string]string) {
	t.Helper()
	const path = "/api/v1/directory/unit-merges"
	input := map[string]any{"mode": "complex", "targetId": units["target"], "sourceIds": []string{units["source"]}}
	plan := ledgerReply[struct {
		Token    string
		Blockers []struct{ Code string }
	}](t, ledgerCall(mux, "POST", path+"/preview", input), 200)
	if plan.Token == "" {
		t.Fatalf("隐患证据不能按原范围与新职责接续：%+v", plan)
	}
	input["token"] = plan.Token
	ledgerReply[struct{ ID string }](t, ledgerCall(mux, "POST", path, input), 200)
}

// assertTransferredEvidenceScope 核对原证据的元数据、原件访问范围及第三方拒绝。
// 参数 t、mux 为测试上下文与真实路由，path、id、oldScope、newScope 为隐患、文件及两端范围。
// 返回值 无。
func assertTransferredEvidenceScope(t *testing.T, mux http.Handler, path, id, oldScope, newScope string) {
	t.Helper()
	attachments := path + "/attachments"
	old := ledgerReply[hazardCollection[cases.Attachment]](t, ledgerCall(mux, "GET", attachments+oldScope, nil), 200)
	next := ledgerReply[hazardCollection[cases.Attachment]](t, ledgerCall(mux, "GET", attachments+newScope, nil), 200)
	if len(old.Items) != 1 || old.Items[0].ID != id || len(next.Items) != 0 {
		t.Fatalf("归并改变了原件可见范围：原单位 %+v，承接 %+v", old, next)
	}
	file := attachments + "/" + id
	resp := ledgerCall(mux, "GET", file+oldScope, nil)
	if resp.Code != 200 || resp.Body.String() != fixturePDF {
		t.Fatalf("原单位无法下载原件：%d", resp.Code)
	}
	assertLedgerError(t, ledgerCall(mux, "GET", file+newScope, nil), 404, "not_found")
	assertLedgerError(t, ledgerCall(mux, "GET", file+"?organization="+url.QueryEscape("无关单位"), nil), 404, "not_found")
}

// assertRetestHandoff 核对原复测机构继续读取材料并完成审核。
// 参数 t、mux 为测试上下文及路由，id、attachmentID、version 为隐患、历史证据及交接后版本。
// 返回值 无。
func assertRetestHandoff(t *testing.T, mux http.Handler, id, attachmentID string, version int64) {
	t.Helper()
	path := institutionBase + "/" + id + "/attachments/" + attachmentID
	r := ledgerCall(mux, "GET", path+"?institution="+url.QueryEscape("测评中心"), nil)
	if r.Code != 200 || r.Body.String() != fixturePDF {
		t.Fatalf("原复测机构无法继续查阅既有证据：%d", r.Code)
	}
	if result := passInstitutionRetest(t, mux, id, version); result.Status != statusPending {
		t.Fatalf("原复测机构没有继续送监管审核：%+v", result)
	}
}
