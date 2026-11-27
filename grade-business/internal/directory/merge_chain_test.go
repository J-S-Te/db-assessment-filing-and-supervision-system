package directory

import (
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

type mergeChainPreview struct {
	mergeTestPlan
	Aliases []struct{ ID, MergedIntoID string }
}

// TestComplexUnitMergePreservesChain 验证后继主单位可再次归并，逐层保留原目录指针、回执和材料归属。
// 参数 t 提供隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestComplexUnitMergePreservesChain(t *testing.T) {
	pool := directoryTestPool(t)
	mux := http.NewServeMux()
	Register(mux, pool)
	units := []testEntry{
		mergeTestUnit(t, mux, "第一原单位", "913300001000001061"),
		mergeTestUnit(t, mux, "第一承接单位", "913300001000001062"),
		mergeTestUnit(t, mux, "第二承接单位", "913300001000001063"),
		mergeTestUnit(t, mux, "最终承接单位", "913300001000001064"),
	}
	filing := seedMergeFiling(t, pool, units[0].Name, "MERGE-CHAIN", "链式接续系统", "approved", false)
	var firstMerge mergeTestResult
	for step, source := range units[:3] {
		target := units[step+1]
		addChainAttachment(t, pool, filing, source.Name)
		body := map[string]any{"targetId": target.ID, "sourceIds": []string{source.ID}}
		if step > 0 {
			simple := testJSON[mergeTestPlan](t, testRequest(t, mux, "POST", mergePath+"/preview", body, 200))
			if simple.Token != "" || len(simple.Blockers) != 1 || simple.Blockers[0].Code != "merge_source_has_aliases" {
				t.Fatalf("简单模式未要求核对完整的原归并链：%+v", simple)
			}
		}
		body["mode"] = complexMerge
		plan := testJSON[mergeChainPreview](t, testRequest(t, mux, "POST", mergePath+"/preview", body, 200))
		verifyMergeChainPreview(t, plan, units[:step+1])
		body["token"] = plan.Token
		merged := testJSON[mergeTestResult](t, testRequest(t, mux, "POST", mergePath, body, 200))
		if step == 0 {
			firstMerge = merged
		}
	}
	verifyMergeChainHistory(t, pool, filing, units)
	stored := testJSON[mergeHistory](t, testRequest(t, mux, "GET", mergePath+"/"+firstMerge.ID, nil, 200))
	if stored.TargetID != units[1].ID || len(stored.SourceIDs) != 1 || stored.SourceIDs[0] != units[0].ID {
		t.Fatalf("首次归并回执被后继归并改写：%+v", stored)
	}
}

// verifyMergeChainPreview 核对当前来源项和逐层别名的原直接目标。
// 参数 t、plan 为测试上下文和本次预览，branch 为预览来源及其先前归并链。
// 返回值 无。
func verifyMergeChainPreview(t *testing.T, plan mergeChainPreview, branch []testEntry) {
	t.Helper()
	if plan.Token == "" || len(plan.Moves) != 1 || len(plan.Aliases) != len(branch)-1 {
		t.Fatalf("链式归并未给出可确认的范围及原目录别名：%+v", plan)
	}
	aliases := make(map[string]string, len(plan.Aliases))
	for _, alias := range plan.Aliases {
		aliases[alias.ID] = alias.MergedIntoID
	}
	for step, original := range branch[:len(branch)-1] {
		if aliases[original.ID] != branch[step+1].ID {
			t.Fatalf("预览未保留目录别名 %s 的原直接目标：%v", original.ID, aliases)
		}
	}
}

// addChainAttachment 为归并时的当前单位登记材料，使不同交接时点各有原始记录。
// 参数 t、pool 为测试上下文与隔离库，filing、owner 为备案及材料范围。
// 返回值 无。
func addChainAttachment(t *testing.T, pool *pgxpool.Pool, filing, owner string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `INSERT INTO case_attachments(case_id,kind,file_name,file_key,size_bytes)
		VALUES($1,'filing_form',$2,gen_random_uuid()::text,1)`, filing, owner); err != nil {
		t.Fatal(err)
	}
}

// verifyMergeChainHistory 核对最终承接归属与各次归并此前的历史材料和目录指针。
// 参数 t、pool 为测试上下文与隔离库，filing 为已归并备案，units 为按交接时间排序的单位。
// 返回值 无。
func verifyMergeChainHistory(t *testing.T, pool *pgxpool.Pool, filing string, units []testEntry) {
	t.Helper()
	for index, source := range units[:len(units)-1] {
		var target, scope string
		err := pool.QueryRow(t.Context(), `SELECT p.merged_into_id::text,a.merge_history_scope
			FROM directory_parties p JOIN case_attachments a ON a.case_id=$1 AND a.file_name=p.scope_name
			WHERE p.id=$2 AND p.status='inactive'`, filing, source.ID).Scan(&target, &scope)
		if err != nil || target != units[index+1].ID || scope != source.Name {
			t.Fatalf("目录或材料原始范围发生变化：%q、%q，错误 %v", target, scope, err)
		}
	}
	var owner string
	if err := pool.QueryRow(t.Context(), `SELECT organization_name FROM business_cases WHERE id=$1`,
		filing).Scan(&owner); err != nil || owner != units[len(units)-1].Name {
		t.Fatalf("当前备案未归最终主单位：%q，错误 %v", owner, err)
	}
}
