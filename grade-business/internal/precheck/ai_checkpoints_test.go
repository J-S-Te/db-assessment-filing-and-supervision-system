package precheck

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"grade-business/internal/filingaudit"
)

type checkpointAssessorProbe struct {
	assessmentStub
	calls         int
	stopAfterSave context.CancelFunc
	afterSave     func()
}

// AssessBatch 用途 为不支持检查点的旧入口返回错误，避免测试误走该路径。
// 参数 ctx、docs、unit、progress 为旧接口的执行参数。
// 返回值 明确指出调用路径错误的结果。
func (model *checkpointAssessorProbe) AssessBatch(context.Context, []filingaudit.Document,
	filingaudit.Unit, filingaudit.BatchProgress,
) (filingaudit.BatchResult, error) {
	return filingaudit.BatchResult{}, errors.New("测试应使用可恢复审核")
}

// AssessWithCheckpoints 用途 模拟模型完成一次请求，并通过实际 worker 保存检查点与用量。
// 参数 ctx、docs、unit、progress、store 为执行上下文、合成材料、名录、进度和持久化接口。
// 返回值 当前素材的核验结果或持久化错误。
func (model *checkpointAssessorProbe) AssessWithCheckpoints(ctx context.Context,
	docs []filingaudit.Document, _ filingaudit.Unit, progress filingaudit.BatchProgress,
	store filingaudit.ReviewCheckpoints,
) (filingaudit.BatchResult, error) {
	key := strings.Repeat("a", 64)
	result, hit, err := store.Load(ctx, key)
	if err != nil {
		return result, err
	}
	if !hit {
		if err := store.BeforeRequest(ctx); err != nil {
			return result, err
		}
		model.calls++
		run := store.(*aiExecution)
		run.recordUsage(ctx, filingaudit.Usage{Model: "deepseek-flash", Stage: filingaudit.ReviewingMaterials,
			Outcome: "success", Reported: true, CacheReported: true, PromptTokens: 420,
			CacheHitTokens: 100, CacheMissTokens: 320, CompletionTokens: 140, ReasoningTokens: 50})
		result.Checks = []filingaudit.Result{{RuleID: "object_number", Status: "manual",
			Message: docs[0].FileName}}
		result.Materials = []filingaudit.MaterialReview{{FileName: docs[0].FileName, Status: "reviewed"}}
		if err := store.Save(ctx, key, result); err != nil {
			return result, err
		}
		if model.stopAfterSave != nil {
			model.stopAfterSave()
			return result, ctx.Err()
		}
		if model.afterSave != nil {
			model.afterSave()
		}
	}
	return result, progress(filingaudit.ReviewingMaterials, result.Materials, len(docs))
}

// TestPrecheckAIStoreIntegration 用途 验证同案复用、旧批次可查、显式重审和付费用量持久化。
// 参数 t 提供隔离的 PostgreSQL schema 和合成备案。
// 返回值 无。
func TestPrecheckAIStoreIntegration(t *testing.T) {
	pool := isolatedPool(t)
	var filing, other string
	for index, id := range []*string{&filing, &other} {
		err := pool.QueryRow(t.Context(), `INSERT INTO business_cases
			(kind,number,system_name,organization_name,assessment_org,level)
			VALUES('filing',$1,'测试系统','测试单位','测试机构','2') RETURNING id::text`,
			[]string{"AI-TEST-001", "AI-TEST-002"}[index]).Scan(id)
		if err != nil {
			t.Fatal(err)
		}
	}
	currentUnit := filingaudit.Unit{Name: "合成单位甲"}
	service := New(pool, func(context.Context, string) (Snapshot, error) {
		return Snapshot{Revision: 1}, nil
	}, func(_ context.Context, id string, _ []Material) ([]filingaudit.Document, filingaudit.Unit, error) {
		return []filingaudit.Document{{FileName: id}}, currentUnit, nil
	})
	service.WithUnitSource(func(context.Context, string) (filingaudit.Unit, error) { return currentUnit, nil })
	model := &checkpointAssessorProbe{}
	service.WithAssessor(model)
	run := func(id string, force bool) Job {
		job, err := service.start(t.Context(), id, force)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.runOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		job, err = service.Get(t.Context(), id, job.ID)
		if err != nil || job.Stale || job.Status != "succeeded" || job.Source != "deepseek" ||
			len(job.Checks) != 1 || job.Checks[0].Message != id {
			t.Fatalf("工作进程写入的结论不正确：%+v，%v", job, err)
		}
		return job
	}
	first := run(filing, false)
	repeated := run(filing, false)
	if model.calls != 1 || repeated.ID == first.ID || !reflect.DeepEqual(first.Checks, repeated.Checks) {
		t.Fatal("重复发起应复用已核验结论并保留独立批次")
	}
	if _, err := service.Get(t.Context(), filing, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(t.Context(), other, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("其他备案不应读到同案缓存或批次：%v", err)
	}
	run(other, false)
	forced := run(filing, true)
	var records int
	var allOutput int64
	err := pool.QueryRow(t.Context(), `SELECT count(*),sum(completion_tokens)
		FROM filing_precheck_ai_usage WHERE usage_reported AND cache_reported`).Scan(&records, &allOutput)
	if err != nil || model.calls != 3 || records != 3 || allOutput != 420 {
		t.Fatalf("只应记录真实请求并按备案隔离，调用 %d，记录 %d，输出 %d，错误 %v",
			model.calls, records, allOutput, err)
	}
	err = pool.QueryRow(t.Context(), `SELECT count(*) FROM filing_precheck_ai_usage
		WHERE job_id=$1 AND stage='deepseek' AND reasoning_tokens=50`, forced.ID).Scan(&records)
	if err != nil || records != 1 {
		t.Fatalf("强制重审的新调用应单独计量，记录 %d，错误 %v", records, err)
	}
	resumeAfterWorkerStop(t, service, model, filing)
	assertDirectoryChange(t, service, model, filing, &currentUnit)
}

// assertDirectoryChange 用途 验证模型处理期间名录发生变化时原结论标记过时。
// 参数 t、service、model、filing、current 为隔离测试、预审服务、模拟模型、备案和可修改名录。
// 返回值 无。
func assertDirectoryChange(t *testing.T, service *Service, model *checkpointAssessorProbe,
	filing string, current *filingaudit.Unit,
) {
	t.Helper()
	job, err := service.start(t.Context(), filing, true)
	if err != nil {
		t.Fatal(err)
	}
	model.afterSave = func() { current.Name = "合成单位乙" }
	if err := service.runOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	model.afterSave = nil
	result, err := service.Get(t.Context(), filing, job.ID)
	if err != nil || !result.Stale || result.Source != "deepseek" {
		t.Fatalf("模型运行期间变化的名录应使旧结论过时：%+v，%v", result, err)
	}
}

// resumeAfterWorkerStop 用途 验证强制重审任务换领后只继续缺失步骤。
// 参数 t、service、model、filing 为测试环境、队列服务、模拟模型和备案标识。
// 返回值 无。
func resumeAfterWorkerStop(t *testing.T, service *Service, model *checkpointAssessorProbe, filing string) {
	t.Helper()
	job, err := service.start(t.Context(), filing, true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	model.stopAfterSave = cancel
	if err := service.runOne(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("第一个 worker 应在保存检查点后退出：%v", err)
	}
	model.stopAfterSave = nil
	result, err := service.Get(t.Context(), filing, job.ID)
	if err != nil || result.Status != "running" {
		t.Fatalf("中断任务应保持等待恢复：%+v，%v", result, err)
	}
	if _, err := service.pool.Exec(t.Context(), `UPDATE filing_prechecks
		SET lease_until=now()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	calls := model.calls
	if err := service.runOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err = service.Get(t.Context(), filing, job.ID)
	if err != nil || result.Status != "succeeded" || calls != model.calls {
		t.Fatalf("重新领取应复用同任务已保存的结论：%+v，%v，新增调用 %d",
			result, err, model.calls-calls)
	}
}

// TestPrecheckRejectsInvalidForce 用途 验证明示重审只接受唯一的布尔值。
// 参数 t 提供 HTTP 路由测试上下文。
// 返回值 无。
func TestPrecheckRejectsInvalidForce(t *testing.T) {
	mux := http.NewServeMux()
	New(nil, nil).Register(mux)
	for _, query := range []string{"?force=", "?force=1", "?force=false&force=true"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/filings/fixture/prechecks"+query, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("非法重审参数 %q 得到状态 %d", query, response.Code)
		}
	}
}

// TestForcePrecheckClaimConflict 用途 验证普通任务可在领取前升级为强制，但运行后返回冲突。
// 参数 t 提供隔离 PostgreSQL 与 HTTP 路由。
// 返回值 无。
func TestForcePrecheckClaimConflict(t *testing.T) {
	pool := isolatedPool(t)
	service := New(pool, func(context.Context, string) (Snapshot, error) { return Snapshot{Revision: 1}, nil })
	const queued = "00000000-0000-4000-8000-100000000001"
	const running = "00000000-0000-4000-8000-100000000002"
	first, err := service.Start(t.Context(), queued)
	if err != nil {
		t.Fatal(err)
	}
	same, err := service.start(t.Context(), queued, true)
	if err != nil || same.ID != first.ID {
		t.Fatalf("排队中的任务应原子提升优先复算设置：%+v，%v", same, err)
	}
	var force bool
	if err := pool.QueryRow(t.Context(), `SELECT force_ai FROM filing_prechecks WHERE id=$1`, first.ID).Scan(&force); err != nil || !force {
		t.Fatalf("已排队任务未被标记强制重审：%v", err)
	}
	active, err := service.Start(t.Context(), running)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE filing_prechecks
		SET status='running',lease_until=now()+interval '1 minute' WHERE id=$1`, active.ID); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	service.Register(mux)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/filings/"+running+"/prechecks?force=true", nil)
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("普通任务已领取时显式重审应返回冲突，实际 %d", response.Code)
	}
}

type checkpointTimeoutProbe struct{ checkpointAssessorProbe }

// Timeout 用途 模拟模型总期限在查询检查点前到期。
// 参数 无。
// 返回值 立即到期的测试期限。
func (model *checkpointTimeoutProbe) Timeout() time.Duration { return 0 }

// TestCheckpointTimeoutFallsBack 用途 验证模型子期限到期仍会完成本地规则审核。
// 参数 t 提供隔离 PostgreSQL 与合成材料。
// 返回值 无。
func TestCheckpointTimeoutFallsBack(t *testing.T) {
	pool := isolatedPool(t)
	const filing = "00000000-0000-4000-8000-100000000003"
	service := New(pool, func(ctx context.Context, _ string) (Snapshot, error) {
		return Snapshot{Revision: 1}, ctx.Err()
	}, func(context.Context, string, []Material) ([]filingaudit.Document, filingaudit.Unit, error) {
		return []filingaudit.Document{{Kind: "filing_form"}}, filingaudit.Unit{}, nil
	})
	model := &checkpointTimeoutProbe{}
	service.WithAssessor(model)
	job, err := service.Start(t.Context(), filing)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.runOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err := service.Get(t.Context(), filing, job.ID)
	if err != nil || result.Status != "succeeded" || result.Source != "local_rules" ||
		result.Fallback == "" || model.calls != 0 {
		t.Fatalf("模型期限到期应完成本地回退：%+v，%v", result, err)
	}
}
