package directory

import (
	"net/http"
	"testing"
)

// TestComplexUnitMergePreviewAndRevalidation 核对未下发监督与已派发无证据隐患的明确移交，并拒绝陈旧或跨模式复用。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestComplexUnitMergePreviewAndRevalidation(t *testing.T) {
	pool := directoryTestPool(t)
	mux := http.NewServeMux()
	Register(mux, pool)
	main := mergeTestUnit(t, mux, "复杂归并主单位", "913300001000001031")
	source := mergeTestUnit(t, mux, "复杂归并原单位", "913300001000001032")
	filing := seedMergeFiling(t, pool, source.Name, "CX-001", "业务接续系统", "approved", false)
	var task, hazard string
	err := pool.QueryRow(t.Context(), `INSERT INTO supervision_tasks(request_id,filing_id,filing_number,
	    system_name,organization_name,title,due_on,cycle_year)
	    VALUES(gen_random_uuid(),$1,'CX-001','业务接续系统',$2,'待下发监督',DATE '2030-05-01',2030)
	    RETURNING id::text`, filing, source.Name).Scan(&task)
	if err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(t.Context(), `INSERT INTO hazard_items(registration_filing_id,ledger,title,severity,
		source,discovered_on,description,measure,responsibility,deadline,status,dispatched_at,retest_institution)
		VALUES($1,'vulnerability','待整改隐患','medium','manual',CURRENT_DATE,'核对风险','迁移后处理',
		'原整改人',CURRENT_DATE+30,'open',now(),'原测评机构') RETURNING id::text`, filing).Scan(&hazard)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"targetId": main.ID, "sourceIds": []string{source.ID}, "mode": "complex"}
	type impact struct{ ID, Kind, Status, Disposition string }
	type complexPreview struct {
		Token   string
		Mode    string
		Impacts []impact
	}
	preview := testJSON[complexPreview](t, testRequest(t, mux, "POST", mergePath+"/preview", body, 200))
	if preview.Token == "" || preview.Mode != "complex" || len(preview.Impacts) != 2 ||
		preview.Impacts[0] != (impact{hazard, "hazard", "open", "transfer"}) ||
		preview.Impacts[1] != (impact{task, "supervision", "draft", "transfer"}) {
		t.Fatalf("复杂预览未逐笔说明工作责任: %+v", preview)
	}
	body["token"] = preview.Token
	body["mode"] = "simple"
	testRequest(t, mux, "POST", mergePath, body, 409)
	body["mode"] = "complex"
	if _, err := pool.Exec(t.Context(), `UPDATE hazard_items SET version=version+1 WHERE id=$1`, hazard); err != nil {
		t.Fatal(err)
	}
	testRequest(t, mux, "POST", mergePath, body, 409)
	delete(body, "token")
	preview = testJSON[complexPreview](t, testRequest(t, mux, "POST", mergePath+"/preview", body, 200))
	body["token"] = preview.Token
	result := testJSON[mergeTestResult](t, testRequest(t, mux, "POST", mergePath, body, 200))
	replay := testJSON[mergeTestResult](t, testRequest(t, mux, "POST", mergePath, body, 200))
	if !replay.Replayed || result.ID != replay.ID || result.MovedCount != 1 {
		t.Fatalf("复杂归并重放不一致：%+v %+v", result, replay)
	}
	body["mode"] = "simple"
	testRequest(t, mux, "POST", mergePath, body, 409)
	var newHazard, newTask string
	err = pool.QueryRow(t.Context(), `SELECT h.organization_name,t.organization_name
		FROM hazard_context h JOIN supervision_tasks t ON t.id=$2 WHERE h.id=$1`, hazard, task).
		Scan(&newHazard, &newTask)
	if err != nil || newHazard != main.Name || newTask != main.Name {
		t.Fatalf("移交后未连接实际承办席位：%q / %q / %v", newHazard, newTask, err)
	}
}

// TestComplexUnitMergeRevalidatesIssuedSupervision 验证任务下发后要求重新预览，后续按已送达范围接续。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestComplexUnitMergeRevalidatesIssuedSupervision(t *testing.T) {
	pool := directoryTestPool(t)
	mux := http.NewServeMux()
	Register(mux, pool)
	main := mergeTestUnit(t, mux, "归并监督承接单位", "913300001000001033")
	source := mergeTestUnit(t, mux, "归并监督原收件单位", "913300001000001034")
	filing := seedMergeFiling(t, pool, source.Name, "CX-ISSUED", "受监督系统", "approved", false)
	var task string
	err := pool.QueryRow(t.Context(), `INSERT INTO supervision_tasks(request_id,filing_id,filing_number,
	    system_name,organization_name,title,due_on,cycle_year)
	    VALUES(gen_random_uuid(),$1,'CX-ISSUED','受监督系统',$2,'已经在办的任务',DATE '2030-05-01',2030)
	    RETURNING id::text`, filing, source.Name).Scan(&task)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"targetId": main.ID, "sourceIds": []string{source.ID}, "mode": "complex"}
	preview := testJSON[mergePlan](t, testRequest(t, mux, "POST", mergePath+"/preview", body, 200))
	if preview.Token == "" {
		t.Fatalf("未下发的监督草稿无法移交：%+v", preview)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE supervision_tasks
		SET status='open',version=version+1,published_at=clock_timestamp() WHERE id=$1`, task); err != nil {
		t.Fatal(err)
	}
	body["token"] = preview.Token
	testRequest(t, mux, "POST", mergePath, body, 409)
	delete(body, "token")
	preview = testJSON[mergePlan](t, testRequest(t, mux, "POST", mergePath+"/preview", body, 200))
	if preview.Token == "" || len(preview.Impacts) != 1 || len(preview.Blockers) != 0 ||
		preview.Impacts[0].ID != task || preview.Impacts[0].Disposition != "transfer" ||
		preview.Impacts[0].Status != "open" {
		t.Fatalf("已下发事项未重新显示接续与原收件条件：%+v", preview)
	}
	var current string
	if err := pool.QueryRow(t.Context(), `SELECT organization_name FROM business_cases WHERE id=$1`, filing).
		Scan(&current); err != nil || current != source.Name {
		t.Fatalf("陈旧预览改写了备案归属：%q / %v", current, err)
	}
}
