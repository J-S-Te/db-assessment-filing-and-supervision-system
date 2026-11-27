package hazards

import (
	"net/http"
	"net/url"
	"testing"

	"grade-business/internal/cases"
	"grade-business/internal/directory"
)

// TestComplexMergeContinuesDispatchedHazard 验证已下发隐患的后续整改、历史和送达采用不同承办范围。
// 参数 t 为隔离 PostgreSQL 和真实证据目录的测试上下文。
// 返回值 无。
func TestComplexMergeContinuesDispatchedHazard(t *testing.T) {
	service, handler := ledgerFixture(t)
	mux := handler.(*http.ServeMux)
	directory.Register(mux, service.db)
	const source, target = "整改原收件单位", "整改接续主单位"
	const unitPath = "/api/v1/directory/units"
	type unit struct{ ID string }
	old := ledgerReply[unit](t, ledgerCall(mux, "POST", unitPath,
		map[string]string{"name": source, "code": "913300001000001061"}), 201)
	next := ledgerReply[unit](t, ledgerCall(mux, "POST", unitPath,
		map[string]string{"name": target, "code": "913300001000001062"}), 201)
	hazard := seedDispatchedHandoffHazard(t, service, mux, source)
	const mergePath = "/api/v1/directory/unit-merges"
	input := map[string]any{"mode": "complex", "targetId": next.ID, "sourceIds": []string{old.ID}}
	preview := ledgerReply[struct{ Token string }](t, ledgerCall(mux, "POST", mergePath+"/preview", input), 200)
	input["token"] = preview.Token
	ledgerReply[struct{ ID string }](t, ledgerCall(mux, "POST", mergePath, input), 200)
	assertHazardHandoffNotices(t, service, hazard.ID, source, target)
	assertHazardHandoffWork(t, mux, hazard, source, target)
}

// seedDispatchedHandoffHazard 通过监管入口真实派发并向原运营单位登记送达。
// 参数 t、service、mux 为隔离业务环境，source 是原单位范围。
// 返回值 已派发并处于整改阶段的隐患标识和版本。
func seedDispatchedHandoffHazard(t *testing.T, service *Service, mux http.Handler, source string) hazardMutation {
	t.Helper()
	var filing string
	err := service.db.QueryRow(t.Context(), `INSERT INTO business_cases
		(kind,number,system_name,organization_name,assessment_org,level,status)
		VALUES('filing','CX-HAZARD','接续整改系统',$1,'测评中心','3','approved') RETURNING id::text`, source).Scan(&filing)
	if err != nil {
		t.Fatal(err)
	}
	registered := ledgerReply[hazardMutation](t, ledgerCall(mux, "POST", testLedgerPath, map[string]string{
		"ledger": "vulnerability", "filingId": filing, "title": "归并整改验证", "severity": "low",
		"source": "manual", "discoveredOn": "2026-01-01", "deadline": "2099-01-01",
		"description": "隔离测试整改记录", "measure": "完成整改并提交", "responsibility": "值班室",
	}), 201)
	return ledgerReply[hazardMutation](t, ledgerCall(mux, "POST", testLedgerPath+"/"+registered.ID+"/actions",
		actionInput{actionDispatch, registered.Version, "原单位派发历史"}), 200)
}

// assertHazardHandoffNotices 核对一条原送达和一条面向接续方的新待办消息。
// 参数 t、service 为隔离测试环境，id、source、target 为隐患与两端收件范围。
// 返回值 无。
func assertHazardHandoffNotices(t *testing.T, service *Service, id, source, target string) {
	t.Helper()
	var original, assigned int
	err := service.db.QueryRow(t.Context(), `SELECT
		count(*) FILTER (WHERE recipient=$2 AND comment='原单位派发历史'),
		count(*) FILTER (WHERE recipient=$3 AND state='open' AND comment<>'原单位派发历史')
		FROM review_notices WHERE resource='hazards' AND resource_id=$1`, id, source, target).
		Scan(&original, &assigned)
	if err != nil || original != 1 || assigned != 1 {
		t.Fatalf("归并后的整改送达不正确：原 %d，新 %d，错误 %v", original, assigned, err)
	}
}

// assertHazardHandoffWork 核对隔离历史、新承办单位上传原件并正式提交本轮整改。
// 参数 t、mux 为隔离测试路由，hazard 为待整改隐患，source、target 为两端权限范围。
// 返回值 无。
func assertHazardHandoffWork(t *testing.T, mux http.Handler, hazard hazardMutation, source, target string) {
	t.Helper()
	path := operatorBase + "/" + hazard.ID
	oldScope, newScope := "?organization="+url.QueryEscape(source), "?organization="+url.QueryEscape(target)
	current := ledgerReply[hazardDetail](t, ledgerCall(mux, "GET", path+newScope, nil), 200)
	if current.Status != statusOpen || current.Version != hazard.Version+1 {
		t.Fatalf("主单位没有可办理的正确版本：%+v", current)
	}
	assertLedgerError(t, ledgerCall(mux, "GET", path+oldScope, nil), 404, "not_found")
	original := ledgerReply[hazardCollection[hazardEvent]](t, ledgerCall(mux, "GET", path+"/history"+oldScope, nil), 200)
	history := ledgerReply[hazardCollection[hazardEvent]](t, ledgerCall(mux, "GET", path+"/history"+newScope, nil), 200)
	if len(original.Items) != 2 || len(history.Items) != 0 {
		t.Fatalf("历史办理范围发生扩张，原有 %v，新有 %v", original, history)
	}
	file := ledgerReply[cases.Attachment](t, ledgerUpload(t, mux, path+newScope, kindEvidence, fixturePDF, current.Version), 201)
	assertLedgerError(t, ledgerCall(mux, "GET", path+"/attachments/"+file.ID+oldScope, nil), 404, "not_found")
	submitted := ledgerReply[hazardMutation](t, ledgerCall(mux, "POST", path+"/actions"+newScope,
		actionInput{actionSubmit, current.Version + 1, "主单位完成整改申请复测"}), 200)
	if submitted.Status != statusRetest {
		t.Fatalf("主单位整改没有进入复测：%+v", submitted)
	}
}

// TestComplexMergeKeepsClosedHazardReference 验证归并后继续修改母备案不改变旧销项详情及回执。
// 参数 t 为隔离 PostgreSQL 和真实路由测试上下文。
// 返回值 无。
func TestComplexMergeKeepsClosedHazardReference(t *testing.T) {
	service, handler := ledgerFixture(t)
	mux := handler.(*http.ServeMux)
	directory.Register(mux, service.db)
	const source, target = "销项原收件单位", "销项归并主单位"
	const unitPath = "/api/v1/directory/units"
	type unit struct{ ID string }
	old := ledgerReply[unit](t, ledgerCall(mux, "POST", unitPath,
		map[string]string{"name": source, "code": "913300001000001063"}), 201)
	next := ledgerReply[unit](t, ledgerCall(mux, "POST", unitPath,
		map[string]string{"name": target, "code": "913300001000001064"}), 201)
	hazard := seedDispatchedHandoffHazard(t, service, mux, source)
	closeHazardForHandoff(t, mux, hazard, source)
	const mergePath = "/api/v1/directory/unit-merges"
	input := map[string]any{"mode": "complex", "targetId": next.ID, "sourceIds": []string{old.ID}}
	preview := ledgerReply[struct{ Token string }](t, ledgerCall(mux, "POST", mergePath+"/preview", input), 200)
	input["token"] = preview.Token
	ledgerReply[struct{ ID string }](t, ledgerCall(mux, "POST", mergePath, input), 200)
	if _, err := service.db.Exec(t.Context(), `UPDATE business_cases
		SET number='后续备案编号',system_name='后续系统名称'
		WHERE kind='filing' AND organization_name=$1`, target); err != nil {
		t.Fatal(err)
	}
	path := operatorBase + "/" + hazard.ID
	oldScope := "?organization=" + url.QueryEscape(source)
	record := ledgerReply[hazardDetail](t, ledgerCall(mux, "GET", path+oldScope, nil), 200)
	if record.FilingNumber != "CX-HAZARD" || record.SystemName != "接续整改系统" {
		t.Fatalf("旧销项详情出现后续备案内容：%+v", record)
	}
	assertHazardReceipt(t, mux, path+"/receipt"+oldScope, "隐患销项回执", "CX-HAZARD")
}

// closeHazardForHandoff 通过原运营者、复测机构和监管端实际办结本项隐患。
// 参数 t、mux、hazard、source 为隔离测试上下文、业务路由、隐患标识与运营单位。
// 返回值 无。
func closeHazardForHandoff(t *testing.T, mux http.Handler, hazard hazardMutation, source string) {
	t.Helper()
	path := operatorBase + "/" + hazard.ID
	owner := "?organization=" + url.QueryEscape(source)
	ledgerReply[cases.Attachment](t, ledgerUpload(t, mux, path+owner, kindEvidence, fixturePDF, hazard.Version), 201)
	submitted := ledgerReply[hazardMutation](t, ledgerCall(mux, "POST", path+"/actions"+owner,
		actionInput{actionSubmit, hazard.Version + 1, "已落实整改"}), 200)
	passed := passInstitutionRetest(t, mux, submitted.ID, submitted.Version)
	ledgerReply[hazardMutation](t, ledgerCall(mux, "POST", testLedgerPath+"/"+passed.ID+"/actions",
		actionInput{actionClose, passed.Version, "监管复核销项"}), 200)
}
