package precheck

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"grade-business/internal/filingaudit"
)

type assessmentStub struct {
	profile string
	fail    bool
	count   int
	observe func()
}

// Profile 返回隔离测试使用的模型配置版本。
// 参数 无。
// 返回值 随测试阶段变化的标识。
func (stub *assessmentStub) Profile() string { return stub.profile }

// Timeout 返回合成任务的短时请求期限。
// 参数 无。
// 返回值 测试允许的请求期限。
func (stub *assessmentStub) Timeout() time.Duration { return 2 * time.Second }

// Assess 模拟模型服务成功及网络失败。
// 参数 ctx、docs、unit 分别为请求期、合成材料和测试名录。
// 返回值 完整合成结果或用于检查回退路径的错误。
func (stub *assessmentStub) Assess(_ context.Context, docs []filingaudit.Document,
	unit filingaudit.Unit,
) ([]filingaudit.Result, error) {
	stub.count++
	if stub.observe != nil {
		stub.observe()
	}
	if stub.fail {
		return nil, errors.New("fixture network failure")
	}
	return filingaudit.Evaluate(docs, unit, time.Now()), nil
}

// TestPrecheckReportsRunningProgress 验证轮询可读到后端正在执行的真实阶段。
// 参数 t 提供隔离 PostgreSQL 及可观测的材料读取和模型替身。
// 返回值 无。
func TestPrecheckReportsRunningProgress(t *testing.T) {
	pool := isolatedPool(t)
	const id = "00000000-0000-4000-8000-000000000021"
	source := func(context.Context, string) (Snapshot, error) { return Snapshot{Revision: 1}, nil }
	var service *Service
	content := func(context.Context, string, []Material) ([]filingaudit.Document, filingaudit.Unit, error) {
		assertProgressStage(t, service, id, "running", "materials")
		return nil, filingaudit.Unit{}, nil
	}
	service = New(pool, source, content)
	service.WithAssessor(&assessmentStub{
		profile: "deepseek-high",
		observe: func() { assertProgressStage(t, service, id, "running", "deepseek") },
	})
	if _, err := service.Start(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	assertProgressStage(t, service, id, "queued", "")
	if err := service.runOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertProgressStage(t, service, id, "succeeded", "")
}

// assertProgressStage 通过轮询接口的数据格式核对队列和阶段。
// 参数 t、service、id、status、stage 分别为测试、服务、备案和两个期望状态值。
// 返回值 无。
func assertProgressStage(t *testing.T, service *Service, id, status, stage string) {
	t.Helper()
	job, err := service.Get(t.Context(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Status string `json:"status"`
		Stage  string `json:"progressStage"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != status || result.Stage != stage {
		t.Fatalf("预期 %s/%s，实际 %s/%s", status, stage, result.Status, result.Stage)
	}
}

// TestDeepSeekJobProfileAndRetry 核对优先 AI、网络失败自动回退和结果复用。
// 参数 t 提供显式隔离的 PostgreSQL 和任务上下文。
// 返回值 无。
func TestDeepSeekJobProfileAndRetry(t *testing.T) {
	pool := isolatedPool(t)
	const id = "00000000-0000-4000-8000-000000000011"
	source := func(context.Context, string) (Snapshot, error) { return Snapshot{Revision: 1}, nil }
	content := func(context.Context, string, []Material) ([]filingaudit.Document, filingaudit.Unit, error) {
		return []filingaudit.Document{{AttachmentID: id, FileName: "定级报告.docx",
				Kind: "classification_report", Lines: []filingaudit.Line{{Text: "盖章状态：未盖章", Location: "第 1 段"}}}},
			filingaudit.Unit{}, nil
	}
	service := New(pool, source, content)
	local := assertQueuedResult(t, service, id, "local_rules")
	model := &assessmentStub{profile: "deepseek-high", fail: true}
	service.WithAssessor(model)
	previous, err := service.Get(t.Context(), id, local.ID)
	if err != nil || !previous.Stale {
		t.Fatalf("启用 DeepSeek 后仍复用了原本地结果: %v", err)
	}
	fallback := assertQueuedResult(t, service, id, "local_rules")
	if fallback.Failure != "" || fallback.Fallback == "" || fallback.ID != local.ID || model.count != 1 {
		t.Fatal("模型网络失败后未自动完成规则审核、保留原因和任务")
	}
	model.fail = false
	finished := assertQueuedResult(t, service, id, "deepseek")
	same, err := service.Start(t.Context(), id)
	if err != nil || same.ID != finished.ID || same.Status != "succeeded" ||
		same.Fallback != "" || model.count != 2 {
		t.Fatalf("成功后未清除回退提示或复用模型结果: %v", err)
	}
	model.profile = "deepseek-max"
	current := assertQueuedResult(t, service, id, "deepseek")
	if current.Stale || model.count != 3 {
		t.Fatal("思考参数变化没有更新结论")
	}
}

// assertQueuedResult 通过真实队列发起并读取一次已完成的预审。
// 参数 t、service、id、source 分别为测试、任务服务、备案和期望来源。
// 返回值 已持久化的完成任务。
func assertQueuedResult(t *testing.T, service *Service, id, source string) Job {
	t.Helper()
	job, err := service.Start(t.Context(), id)
	if err != nil || job.Status != "queued" {
		t.Fatalf("应排队处理得到 %s，错误 %v", job.Status, err)
	}
	if err := service.runOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	complete, err := service.Get(t.Context(), id, job.ID)
	if err != nil || complete.Status != "succeeded" || complete.Source != source || len(complete.Checks) != 11 {
		t.Fatalf("预期 succeeded/%s，得到 %s/%s，错误 %v", source, complete.Status, complete.Source, err)
	}
	return complete
}
