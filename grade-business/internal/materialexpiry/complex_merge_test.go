package materialexpiry

import (
	"net/http"
	"net/url"
	"testing"

	"grade-business/internal/directory"
)

// TestComplexExpiryHandoffAndOriginalScope 验证当前续办、提醒和未授权的原始状态均按归并范围生效。
// 参数 t 为隔离数据库和真实材料存储测试上下文。
// 返回值 无。
func TestComplexExpiryHandoffAndOriginalScope(t *testing.T) {
	db, files := expiryFixture(t)
	_, sourceFile, today := seedCaseMaterial(t, db, files)
	const targetName = "接续有效期主单位"
	router := http.NewServeMux()
	directory.Register(router, db)
	service := New(db, files, "有效期测试监管")
	service.Register(router)
	oldUnit := expiryCall(t, router, "POST", "/api/v1/directory/units",
		map[string]any{"name": testOrganization, "code": "913300001000000031"}, 201)
	newUnit := expiryCall(t, router, "POST", "/api/v1/directory/units",
		map[string]any{"name": targetName, "code": "913300001000000032"}, 201)
	entry := expiryCall(t, router, "POST", testRegulatorPath, map[string]any{
		"requestId": "90000000-0000-4000-8000-000000000030", "sourceAttachmentId": sourceFile,
		"validUntil": today, "evidence": "原单位核验登记材料",
	}, 201)
	id := entry["id"].(string)
	const endpoint = "/api/v1/directory/unit-merges"
	input := map[string]any{"targetId": newUnit["id"], "sourceIds": []any{oldUnit["id"]}, "mode": "complex"}
	first := expiryCall(t, router, "POST", endpoint+"/preview", input, 200)
	input["token"] = first["token"]
	expiryCall(t, router, "POST", endpoint, input, 200)
	verifyExpiryHandoff(t, router, service, id, targetName, today)
}

// verifyExpiryHandoff 检查原登记只读、主单位续办及提醒收件范围。
// 参数 t、router、service 提供隔离业务入口，id、targetName、today 为归并后的记录及收件情况。
// 返回值 无。
func verifyExpiryHandoff(t *testing.T, router http.Handler, service *Service, id, targetName, today string) {
	t.Helper()
	path := "/api/v1/operator/" + resource + "/" + id
	oldScope := "?organization=" + url.QueryEscape(testOrganization)
	newScope := "?organization=" + url.QueryEscape(targetName)
	next := expiryCall(t, router, "GET", path+newScope, nil, 200)
	old := expiryCall(t, router, "GET", path+oldScope, nil, 200)
	if next["dateStatus"] != "due" || next["sourceFileName"] != "" || next["evidence"] != "" ||
		len(next["history"].([]any)) != 0 || old["dateStatus"] != "retired" ||
		old["sourceFileName"] == "" || old["evidence"] == "" {
		t.Fatalf("归并后的办理和原件范围错误：%v / %v", next, old)
	}
	if count, err := service.Sweep(t.Context()); err != nil || count != 1 {
		t.Fatalf("应仅新增主单位一条提醒：%d / %v", count, err)
	}
	uploadRenewalTest(t, router, path+"/renewals"+oldScope, "新证据.pdf", testPDF, "2099-01-01", 2, 404)
	uploadRenewalTest(t, router, path+"/renewals"+newScope, "新证据.pdf", testPDF, "2099-01-01", 2, 201)
	admin := testRegulatorPath + "/" + id
	result := expiryCall(t, router, "POST", admin+"/actions",
		map[string]any{"action": "approve", "expectedVersion": 3}, 200)
	updated := expiryCall(t, router, "GET", path+newScope, nil, 200)
	original := expiryCall(t, router, "GET", path+oldScope, nil, 200)
	renewalID := updated["renewals"].([]any)[0].(map[string]any)["id"].(string)
	assertExpiryDownload(t, router, path+"/renewals/"+renewalID+"/download"+newScope, 200)
	assertExpiryDownload(t, router, path+"/renewals/"+renewalID+"/download"+oldScope, 404)
	oldRecord, err := loadRecord(t.Context(), service.db, scope{"operator", testOrganization}, id)
	if err != nil || oldRecord.cycle != 1 {
		t.Fatalf("原登记周期不应随后续审批推进：%d / %v", oldRecord.cycle, err)
	}
	if result["validUntil"] != "2099-01-01" || original["validUntil"] != today ||
		len(original["renewals"].([]any)) != 0 {
		t.Fatalf("当前续期或原周期错误：%v / %v", result, original)
	}
}
