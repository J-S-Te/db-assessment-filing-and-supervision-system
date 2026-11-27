package precheck

import (
	"context"
	"errors"
	"testing"
	"time"

	"grade-business/internal/filingaudit"
)

type isolatedAssessment struct {
	started chan string
	resume  chan struct{}
}

// Profile 返回测试审核版本。
// 参数 无。
// 返回值 固定的模型配置标识。
func (stub *isolatedAssessment) Profile() string { return "independent-batch" }

// Timeout 返回合成任务的最长等待时间。
// 参数 无。
// 返回值 测试允许的等待时间。
func (stub *isolatedAssessment) Timeout() time.Duration { return 10 * time.Second }

// Assess 阻塞同一批次的模型调用，直到测试允许完成。
// 参数 ctx、docs、unit 为模型上下文、材料快照及关联名录。
// 返回值 当前备案独有的结果或模型上下文错误。
func (stub *isolatedAssessment) Assess(ctx context.Context, docs []filingaudit.Document,
	unit filingaudit.Unit,
) ([]filingaudit.Result, error) {
	stub.started <- docs[0].FileName
	select {
	case <-stub.resume:
		return []filingaudit.Result{{RuleID: "object_number", Message: docs[0].FileName}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestIndependentPrecheckBatches 验证不同备案同时执行，且历史审核可单独读取。
// 参数 t 提供隔离 PostgreSQL 和合成模型请求。
// 返回值 无。
func TestIndependentPrecheckBatches(t *testing.T) {
	pool := isolatedPool(t)
	const systemC = "00000000-0000-4000-8000-000000000031"
	const systemB = "00000000-0000-4000-8000-000000000032"
	source := func(context.Context, string) (Snapshot, error) { return Snapshot{Revision: 1}, nil }
	content := func(_ context.Context, id string, _ []Material) ([]filingaudit.Document, filingaudit.Unit, error) {
		return []filingaudit.Document{{FileName: id}}, filingaudit.Unit{}, nil
	}
	model := &isolatedAssessment{started: make(chan string, 4), resume: make(chan struct{})}
	service := New(pool, source, content)
	service.WithAssessor(model)
	c, err := service.Start(t.Context(), systemC)
	if err != nil {
		t.Fatal(err)
	}
	b, err := service.Start(t.Context(), systemB)
	if err != nil || c.ID == b.ID {
		t.Fatalf("两个备案应有各自的审核批次：%v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		service.Run(ctx)
	}()
	defer func() { cancel(); <-stopped }()
	started := map[string]bool{}
	for range 2 {
		select {
		case id := <-model.started:
			started[id] = true
		case <-time.After(2 * time.Second):
			t.Fatal("另一个备案应在首个模型调用阻塞时进入自己的审核")
		}
	}
	if !started[systemC] || !started[systemB] {
		t.Fatal("执行中的审核批次归属不正确")
	}
	active, err := service.Start(t.Context(), systemC)
	if err != nil || active.ID != c.ID || active.Status != "running" {
		t.Fatalf("执行中的重复点击应继续使用原批次：%v", err)
	}
	close(model.resume)
	waitBatchCheck(t, service, systemC, c.ID)
	waitBatchCheck(t, service, systemB, b.ID)
	cancel()
	<-stopped
	if _, err := service.Get(t.Context(), systemB, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("读取其他备案的批次应返回未找到：%v", err)
	}
	repeat, err := service.Start(t.Context(), systemC)
	if err != nil || repeat.ID == c.ID {
		t.Fatalf("再次发起应产生新批次并保留旧结果：%v", err)
	}
	active, err = service.Start(t.Context(), systemC)
	if err != nil || active.ID != repeat.ID || active.Status != "queued" {
		t.Fatalf("新批次排队时应复用同一批次：%v", err)
	}
	waitBatchCheck(t, service, systemC, c.ID)
}

// waitBatchCheck 等待指定批次完成，并验证结果只属于该备案。
// 参数 t、service、filing、job 分别为验证上下文、服务、备案和批次号。
// 返回值 无。
func waitBatchCheck(t *testing.T, service *Service, filing, job string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		result, err := service.Get(t.Context(), filing, job)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status == "succeeded" {
			if len(result.Checks) != 1 || result.Checks[0].Message != filing {
				t.Fatalf("批次结果与备案不符：%+v", result)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("等待批次结果超时")
}

type batchProgressProbe struct {
	assessmentStub
	t       *testing.T
	service *Service
	filing  string
}

// AssessBatch 模拟逐份审核后进入终审，验证回调提交的中间记录可单独读取。
// 参数 ctx、docs、unit、progress 为任务上下文、材料、名录和持久化回调。
// 返回值 合成终审结论，或进度记录错误。
func (probe *batchProgressProbe) AssessBatch(ctx context.Context, docs []filingaudit.Document,
	unit filingaudit.Unit, progress filingaudit.BatchProgress,
) (filingaudit.BatchResult, error) {
	reviews := []filingaudit.MaterialReview{{AttachmentID: docs[0].AttachmentID,
		FileName: docs[0].FileName, Status: "reviewed", ManualChecks: 1}}
	if err := progress(filingaudit.ReviewingMaterials, reviews, 2); err != nil {
		return filingaudit.BatchResult{}, err
	}
	assertStoredMaterials(probe.t, probe.service, probe.filing, filingaudit.ReviewingMaterials, 1)
	reviews = append(reviews, filingaudit.MaterialReview{AttachmentID: docs[1].AttachmentID,
		FileName: docs[1].FileName, Status: "reviewed"})
	if err := progress(filingaudit.SummarizingReview, reviews, 2); err != nil {
		return filingaudit.BatchResult{}, err
	}
	assertStoredMaterials(probe.t, probe.service, probe.filing, filingaudit.SummarizingReview, 2)
	return filingaudit.BatchResult{Materials: reviews, Checks: filingaudit.Evaluate(docs, unit, time.Now())}, nil
}

// TestBatchProgressPersistsMaterialResults 验证逐件处理和终审进度可轮询且保留于本批次。
// 参数 t 提供隔离 PostgreSQL、两个合成附件和模型阶段探针。
// 返回值 无。
func TestBatchProgressPersistsMaterialResults(t *testing.T) {
	pool := isolatedPool(t)
	const filing = "00000000-0000-4000-8000-000000000033"
	source := func(context.Context, string) (Snapshot, error) { return Snapshot{Revision: 1}, nil }
	content := func(context.Context, string, []Material) ([]filingaudit.Document, filingaudit.Unit, error) {
		return []filingaudit.Document{{AttachmentID: "form-id", FileName: "备案表.docx"},
			{AttachmentID: "report-id", FileName: "定级报告.docx"}}, filingaudit.Unit{}, nil
	}
	service := New(pool, source, content)
	service.WithAssessor(&batchProgressProbe{
		assessmentStub: assessmentStub{profile: "batch-test"},
		t:              t, service: service, filing: filing,
	})
	job := assertQueuedResult(t, service, filing, "deepseek")
	if job.MaterialReviewTotal != 2 || len(job.MaterialReviews) != 2 ||
		job.MaterialReviews[0].AttachmentID != "form-id" || job.MaterialReviews[1].AttachmentID != "report-id" {
		t.Fatalf("终审完成后未保留本批次的两份材料记录：%+v", job.MaterialReviews)
	}
}

// assertStoredMaterials 核对可供前端轮询的阶段、总数与已处理数。
// 参数 t、service、filing、stage、count 分别为测试、服务、归属、期望阶段和处理数量。
// 返回值 无。
func assertStoredMaterials(t *testing.T, service *Service, filing, stage string, count int) {
	t.Helper()
	job, err := service.Get(t.Context(), filing, "")
	if err != nil || job.Stage != stage || job.MaterialReviewTotal != 2 || len(job.MaterialReviews) != count {
		t.Fatalf("预审进度未按归属保存：%+v，错误 %v", job, err)
	}
}
