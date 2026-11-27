package directory

import (
	"errors"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const mergePath = "/api/v1/directory/unit-merges"

type mergeTestPlan struct {
	Token    string
	Moves    []struct{ ID string }
	Blockers []struct{ Code string }
}

type mergeTestResult struct {
	ID         string
	MovedCount int
	Replayed   bool
}

// TestUnitMergeKeepsHistory 验证正式合并、幂等重放及历史归属不重写。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestUnitMergeKeepsHistory(t *testing.T) {
	pool := directoryTestPool(t)
	mux := http.NewServeMux()
	Register(mux, pool)
	main := mergeTestUnit(t, mux, "主单位", "913300001000001001")
	source := mergeTestUnit(t, mux, "原单位", "913300001000001002")
	id := seedMergeFiling(t, pool, source.Name, "BAM-1", "政务系统", "approved", false)
	cancelled := seedMergeFiling(t, pool, source.Name, "BAM-2", "退役系统", "approved", true)
	if _, err := pool.Exec(t.Context(), `INSERT INTO archive_coverage_targets(unit_id,filing_id,system_name,reference,source)
		VALUES($1,$2,'政务系统','MERGE-SCOPE','合成清册'),($1,NULL,'待备案系统','MERGE-PENDING','合成清册')`,
		source.ID, id); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"targetId": main.ID, "sourceIds": []string{source.ID}}
	plan := testJSON[mergeTestPlan](t, testRequest(t, mux, "POST", mergePath+"/preview", body, 200))
	if plan.Token == "" || len(plan.Moves) != 1 || plan.Moves[0].ID != id || len(plan.Blockers) != 0 {
		t.Fatalf("预览不正确: %+v", plan)
	}
	body["token"] = plan.Token
	out := testJSON[mergeTestResult](t, testRequest(t, mux, "POST", mergePath, body, 200))
	replay := testJSON[mergeTestResult](t, testRequest(t, mux, "POST", mergePath, body, 200))
	if out.ID == "" || out.MovedCount != 1 || out.Replayed || replay.ID != out.ID || !replay.Replayed {
		t.Fatalf("合并结果或重放不正确: %+v %+v", out, replay)
	}
	testRequest(t, mux, "GET", mergePath+"/"+out.ID, nil, 200)
	verifyMergeStoredHistory(t, pool, id, cancelled, main, source)
	assertMergedCoverage(t, pool, source.ID, main.ID)
	original := testJSON[testEntry](t,
		testRequest(t, mux, "GET", publicDirectory+"/units/"+source.ID, nil, 200))
	if original.Status != "inactive" || original.Usable || original.CanEdit {
		t.Fatalf("合并来源仍可继续办理: %+v", original)
	}
	testRequest(t, mux, "PATCH", publicDirectory+"/units/"+source.ID,
		map[string]any{"expectedVersion": original.Version, "status": "active"}, 403)
	_, err := pool.Exec(t.Context(), `INSERT INTO business_cases
		(kind,number,system_name,organization_name,assessment_org,level)
		VALUES('filing','BAM-3','新系统',$1,'测试机构','2')`, source.Name)
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.ConstraintName != "filing_unit_merged" {
		t.Fatalf("历史名称允许继续登记: %v", err)
	}
}

// assertMergedCoverage 核对覆盖对象随简单单位归并变更归属，且原归属保留在档案事件中。
// 参数 t 为测试上下文，pool 为隔离库，sourceID 和 targetID 为归并前后单位标识。
// 返回值 无。
func assertMergedCoverage(t *testing.T, pool *pgxpool.Pool, sourceID, targetID string) {
	t.Helper()
	var targets, covered, events int
	err := pool.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM archive_coverage_status WHERE unit_id=$1 AND version=2),
		(SELECT count(*) FROM archive_coverage_status WHERE unit_id=$1 AND covered),
		(SELECT count(*) FROM archive_registration_events WHERE resource='coverage-targets' AND version=2
		 AND before_values->>'unit_id'=$2 AND after_values->>'unit_id'=($1::uuid)::text)`,
		targetID, sourceID).Scan(&targets, &covered, &events)
	if err != nil || targets != 2 || covered != 1 || events != 2 {
		t.Fatalf("归并后的清册、覆盖状态及原归属留痕错误：%d/%d/%d, %v", targets, covered, events, err)
	}
}

// TestUnitMergeRejectsStalePreviewAndDependencies 验证确认前范围变化、在审及同名系统拒绝迁移。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestUnitMergeRejectsStalePreviewAndDependencies(t *testing.T) {
	pool := directoryTestPool(t)
	mux := http.NewServeMux()
	Register(mux, pool)
	main := mergeTestUnit(t, mux, "主单位", "913300001000001011")
	source := mergeTestUnit(t, mux, "原单位", "913300001000001012")
	approved := seedMergeFiling(t, pool, source.Name, "BMB-1", "系统 A", "approved", false)
	body := map[string]any{"targetId": main.ID, "sourceIds": []string{source.ID}}
	plan := testJSON[mergeTestPlan](t, testRequest(t, mux, "POST", mergePath+"/preview", body, 200))
	seedMergeFiling(t, pool, source.Name, "BMB-2", "系统 B", "approved", false)
	body["token"] = plan.Token
	testRequest(t, mux, "POST", mergePath, body, 409)
	delete(body, "token")
	seedMergeFiling(t, pool, source.Name, "BMB-3", "系统 C", "pending_initial", false)
	seedMergeFiling(t, pool, main.Name, "BMB-4", "系统 A", "approved", false)
	_, err := pool.Exec(t.Context(), `INSERT INTO implementation_reports
		(filing_id,filing_number,system_name,organization_name,assessment_org,report_type,
		 reporter,planned_start,planned_end,plan,created_by)
		VALUES ($1,'BMB-1','系统 A',$2,'测试机构','routine','原单位联系人',
		        CURRENT_DATE,CURRENT_DATE+2,'隔离测试安排','机构端')`, approved, source.Name)
	if err != nil {
		t.Fatal(err)
	}
	plan = testJSON[mergeTestPlan](t, testRequest(t, mux, "POST", mergePath+"/preview", body, 200))
	codes := map[string]bool{}
	for _, blocker := range plan.Blockers {
		codes[blocker.Code] = true
	}
	if plan.Token != "" || !codes["filing_open"] || !codes["system_name_conflict"] ||
		!codes["implementation_history"] {
		t.Fatalf("预览未阻止业务冲突: %+v", plan)
	}
	testRequest(t, mux, "POST", mergePath+"/preview",
		map[string]any{"targetId": main.ID, "sourceIds": []string{main.ID}}, 400)
}

// TestUnitMergeRejectsConcurrentBusinessWrite 验证相关写入未提交时不执行归并，随后可安全重试。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestUnitMergeRejectsConcurrentBusinessWrite(t *testing.T) {
	pool := directoryTestPool(t)
	mux := http.NewServeMux()
	Register(mux, pool)
	main := mergeTestUnit(t, mux, "主单位", "913300001000001021")
	source := mergeTestUnit(t, mux, "原单位", "913300001000001022")
	id := seedMergeFiling(t, pool, source.Name, "BMC-1", "系统 A", "approved", false)
	body := map[string]any{"targetId": main.ID, "sourceIds": []string{source.ID}}
	plan := testJSON[mergeTestPlan](t, testRequest(t, mux, "POST", mergePath+"/preview", body, 200))
	body["token"] = plan.Token
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), `UPDATE business_cases SET version=version+1 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	type failure struct{ Code string }
	blocked := testJSON[failure](t, testRequest(t, mux, "POST", mergePath, body, 409))
	if blocked.Code != "unit_merge_busy" {
		t.Fatalf("并发写入没有阻止归并: %+v", blocked)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	testRequest(t, mux, "POST", mergePath, body, 200)
}

// mergeTestUnit 登记真实的测试名录单位。
// 参数 t、mux 为测试上下文和路由，name、code 为单位的测试名称和编号。
// 返回值 保存的单位条目。
func mergeTestUnit(t *testing.T, mux *http.ServeMux, name, code string) testEntry {
	t.Helper()
	return testJSON[testEntry](t, testRequest(t, mux, "POST", publicDirectory+"/units",
		map[string]any{"name": name, "code": code}, 201))
}

// seedMergeFiling 在隔离模式登记某种状态的备案以及不可覆盖的归属快照。
// 参数 t、pool 为测试上下文与隔离库，organization、number、system、status、cancelled 为备案属性。
// 返回值 实际备案 ID。
func seedMergeFiling(t *testing.T, pool *pgxpool.Pool, organization, number, system, status string,
	cancelled bool,
) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(), `INSERT INTO business_cases
		(kind,number,system_name,organization_name,assessment_org,level,status,cancelled_at,directory_snapshot)
		VALUES('filing',$1,$2,$3,'测试机构','2',$4,CASE WHEN $5 THEN now() END,
		  jsonb_build_object('unit',jsonb_build_object('name',$3::text))) RETURNING id::text`,
		number, system, organization, status, cancelled).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// verifyMergeStoredHistory 核对实际迁移的当前指针、原快照及注销保留。
// 参数 t、pool 为测试上下文和隔离模式，id、cancelled 为两条备案，main、source 为两端归属。
// 返回值 无。
func verifyMergeStoredHistory(t *testing.T, pool *pgxpool.Pool, id, cancelled string, main, source testEntry) {
	t.Helper()
	var org, newID, historical, retired string
	err := pool.QueryRow(t.Context(), `SELECT organization_name,directory_unit_id::text,
		directory_snapshot->'unit'->>'name',
		(SELECT organization_name FROM business_cases WHERE id=$2)
		FROM business_cases WHERE id=$1`, id, cancelled).Scan(&org, &newID, &historical, &retired)
	if err != nil || org != main.Name || newID != main.ID || historical != source.Name || retired != source.Name {
		t.Fatalf("实际归属或历史不符: %q %q %q %q %v", org, newID, historical, retired, err)
	}
}
