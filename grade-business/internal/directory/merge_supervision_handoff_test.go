package directory

import (
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestComplexMergeTransfersSupervisionSite 验证年度和现场任务同时接续且完整冻结原收件范围。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestComplexMergeTransfersSupervisionSite(t *testing.T) {
	pool := directoryTestPool(t)
	mux := http.NewServeMux()
	Register(mux, pool)
	original := mergeTestUnit(t, mux, "监督现场原单位", "913300001000001071")
	current := mergeTestUnit(t, mux, "监督现场承接单位", "913300001000001072")
	next := mergeTestUnit(t, mux, "监督现场后续单位", "913300001000001073")
	filing := seedMergeFiling(t, pool, original.Name, "MERGE-SITE", "现场接续系统", "approved", false)
	task := seedSupervisionHandoff(t, pool, filing, original.Name)
	body := map[string]any{"targetId": current.ID, "sourceIds": []string{original.ID}, "mode": complexMerge}
	plan := testJSON[mergePlan](t, testRequest(t, mux, "POST", mergePath+"/preview", body, 200))
	if plan.Token == "" || len(plan.Impacts) != 2 || plan.Impacts[0].Disposition != "transfer" ||
		plan.Impacts[1].Kind != "supervision-site" || plan.Impacts[1].Disposition != "transfer" {
		t.Fatalf("发出后的自查和现场任务未一并交接：%+v", plan)
	}
	body["token"] = plan.Token
	testRequest(t, mux, "POST", mergePath, body, 200)
	verifySupervisionHandoff(t, pool, task, original.Name, current.Name)
	body = map[string]any{"targetId": next.ID, "sourceIds": []string{current.ID}, "mode": complexMerge}
	plan = testJSON[mergePlan](t, testRequest(t, mux, "POST", mergePath+"/preview", body, 200))
	if plan.Token != "" || len(plan.Blockers) != 1 || plan.Blockers[0].Code != "supervision_already_handed_off" {
		t.Fatalf("在办任务可覆盖已有原范围快照：%+v", plan)
	}
}

// seedSupervisionHandoff 登记包含待整改现场检查的已下发年度任务。
// 参数 t、pool 为测试上下文与隔离库，filing、owner 为备案及原收件范围。
// 返回值 年度监督与现场检查共用的任务标识。
func seedSupervisionHandoff(t *testing.T, pool *pgxpool.Pool, filing, owner string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(), `WITH task AS (
		INSERT INTO supervision_tasks(request_id,filing_id,filing_number,system_name,organization_name,
			title,due_on,cycle_year,status,version,published_at)
		VALUES(gen_random_uuid(),$1,'MERGE-SITE','现场接续系统',$2,'原现场任务',DATE '2030-05-01',2030,'open',2,now())
		RETURNING id
	)
	INSERT INTO supervision_site_inspections(task_id,status,version,visit_at,location,inspectors,outcome,follow_up_due_on)
	SELECT id,'following_up',3,now(),'原检查地点','原检查组','follow_up_required',DATE '2030-05-02' FROM task
	RETURNING task_id::text`, filing, owner).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// verifySupervisionHandoff 核对现场和年度版本、旧快照及当前承办单位。
// 参数 t、pool 为测试上下文与隔离库，id、original、current 为任务及交接前后的单位范围。
// 返回值 无。
func verifySupervisionHandoff(t *testing.T, pool *pgxpool.Pool, id, original, current string) {
	t.Helper()
	var preserved bool
	err := pool.QueryRow(t.Context(), `SELECT t.organization_name=$2 AND t.version=3
		AND t.status='open' AND t.merge_history_scope=$3 AND t.merge_history_version=2
		AND t.merge_history_snapshot->>'status'='open'
		AND s.version=4 AND s.status='following_up' AND s.merge_history_version=3
		AND s.merge_history_snapshot->>'location'='原检查地点'
		AND s.merge_history_snapshot->>'status'='following_up'
		FROM supervision_tasks t JOIN supervision_site_inspections s ON s.task_id=t.id
		WHERE t.id=$1`, id, current, original).Scan(&preserved)
	if err != nil || !preserved {
		t.Fatalf("年度或现场归属和原快照未原子生效：%v，错误 %v", preserved, err)
	}
}
