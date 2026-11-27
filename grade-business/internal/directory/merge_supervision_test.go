package directory

import (
	"net/http"
	"testing"
)

// TestUnitMergePreservesActiveSupervisionScope 验证合并前先办结自查，并保留原单位已撤回备案。
// 参数 t 为独立 PostgreSQL 测试上下文。
// 返回值 无。
func TestUnitMergePreservesActiveSupervisionScope(t *testing.T) {
	pool := directoryTestPool(t)
	mux := http.NewServeMux()
	Register(mux, pool)
	main := mergeTestUnit(t, mux, "归并保留单位", "913300001000001021")
	source := mergeTestUnit(t, mux, "自查原收件单位", "913300001000001022")
	id := seedMergeFiling(t, pool, source.Name, "SUP-MERGE", "自查关联系统", "approved", false)
	_, err := pool.Exec(t.Context(), `INSERT INTO business_cases
		(kind,number,system_name,organization_name,assessment_org,level,status,operator_origin)
		VALUES('filing','SUP-OLD','自查关联系统',$1,'原机构','3','withdrawn',true)`, source.Name)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), `INSERT INTO supervision_tasks(request_id,filing_id,filing_number,
	    system_name,organization_name,title,due_on,cycle_year)
	    VALUES(gen_random_uuid(),$1,'SUP-MERGE','自查关联系统',$2,'等待下发',DATE '2030-05-01',2030)`, id, source.Name)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"targetId": main.ID, "sourceIds": []string{source.ID}}
	initial := testJSON[mergeTestPlan](t, testRequest(t, mux, "POST", mergePath+"/preview", body, 200))
	if initial.Token != "" || len(initial.Blockers) != 1 || initial.Blockers[0].Code != "supervision_open" {
		t.Fatalf("待下发任务未阻止归并：%+v", initial)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE supervision_tasks SET status='cancelled' WHERE filing_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	ready := testJSON[mergeTestPlan](t, testRequest(t, mux, "POST", mergePath+"/preview", body, 200))
	if ready.Token == "" || len(ready.Moves) != 1 || ready.Moves[0].ID != id {
		t.Fatalf("办结后的待移归属有误：%+v", ready)
	}
}
