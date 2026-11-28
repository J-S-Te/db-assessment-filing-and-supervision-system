package filingaudit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type memoryReviewCheckpoints struct {
	results map[string]BatchResult
}

// Load 用途 从测试存储读取同一审核输入的已验证结果。
// 参数 key 为单件或者整批的输入指纹，ctx 为当前审核上下文。
// 返回值 已保存结果、命中状态及上下文取消错误。
func (store memoryReviewCheckpoints) Load(ctx context.Context, key string) (BatchResult, bool, error) {
	result, found := store.results[key]
	return result, found, ctx.Err()
}

// Save 用途 在测试存储中保留成功完成的检查点。
// 参数 ctx 为当前审核上下文，key 为输入指纹，result 为核验结论。
// 返回值 上下文取消错误。
func (store memoryReviewCheckpoints) Save(ctx context.Context, key string, result BatchResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.results[key] = result
	return nil
}

// BeforeRequest 用途 模拟调用前的有效任务检查。
// 参数 ctx 为本次审核上下文。
// 返回值 上下文取消错误。
func (store memoryReviewCheckpoints) BeforeRequest(ctx context.Context) error { return ctx.Err() }

// TestDeepSeekCheckpointParity 用途 验证整批复用、变更单件和失败续跑的调用次数及审核质量。
// 参数 t 提供同一组脱敏材料和模拟接口。
// 返回值 无。
func TestDeepSeekCheckpointParity(t *testing.T) {
	var calls, failSummary atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := writeCheckpointFixture(w, r, &failSummary); err != nil {
			t.Error(err)
			http.Error(w, "测试请求无效", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	model := newDeepSeek(defaultDeepSeekConfig(), "fixture-key")
	model.endpoint = server.URL
	unit := Unit{Name: "合成测试单位", Code: "TEST-CREDIT-1", District: "测试区"}
	docs := checkpointDocuments()
	var usage []Usage
	ctx := WithUsageObserver(t.Context(), func(_ context.Context, event Usage) {
		usage = append(usage, event)
	})
	baseline, err := model.AssessBatch(ctx, docs, unit, nil)
	if err != nil || calls.Load() != 4 {
		t.Fatalf("全量基线应调用四次，实际 %d，错误 %v", calls.Load(), err)
	}
	assertCheckpointQuality(t, baseline)
	logCheckpointCost(t, "全量基线", usage)
	store := memoryReviewCheckpoints{results: make(map[string]BatchResult)}
	usage = nil
	run := func(input []Document) (BatchResult, int32, error) {
		prior := calls.Load()
		result, err := model.AssessWithCheckpoints(ctx, input, unit, nil, store)
		return result, calls.Load() - prior, err
	}
	fresh, paid, err := run(docs)
	if err != nil || paid != 4 || !reflect.DeepEqual(fresh, baseline) {
		t.Fatalf("首次审核应等同基线，调用 %d，错误 %v", paid, err)
	}
	usage = nil
	cached, paid, err := run(docs)
	if err != nil || paid != 0 || !reflect.DeepEqual(cached, baseline) {
		t.Fatalf("整批命中应复用完整结论，调用 %d，错误 %v", paid, err)
	}
	logCheckpointCost(t, "相同材料重跑", usage)

	docs[1].Lines[0].Text = "系统名称：合成系统乙，定级报告补正"
	fullAfterEdit, err := model.AssessBatch(t.Context(), docs, unit, nil)
	if err != nil {
		t.Fatal(err)
	}
	incremental, paid, err := run(docs)
	if err != nil || paid != 2 || !reflect.DeepEqual(incremental, fullAfterEdit) {
		t.Fatalf("单件补正只需审核变更单件和汇总，调用 %d，错误 %v", paid, err)
	}
	logCheckpointCost(t, "单件补正", usage)
	assertCheckpointQuality(t, incremental)

	cold := memoryReviewCheckpoints{results: make(map[string]BatchResult)}
	failSummary.Store(1)
	usage = nil
	failed, err := model.AssessWithCheckpoints(ctx, docs, unit, nil, cold)
	if err == nil || len(failed.Materials) != 3 || len(failed.Checks) != 0 {
		t.Fatalf("汇总失败应保留逐件状态，批次 %+v，错误 %v", failed, err)
	}
	if len(usage) != 4 || usage[3].Stage != SummarizingReview || usage[3].Reported {
		t.Fatalf("失败的汇总仍应记录发起过请求且用量未知：%+v", usage)
	}
	usage = nil
	prior := calls.Load()
	resumed, err := model.AssessWithCheckpoints(ctx, docs, unit, nil, cold)
	if err != nil || calls.Load()-prior != 1 || !reflect.DeepEqual(resumed, fullAfterEdit) {
		t.Fatalf("续跑应只请求汇总，调用 %d，错误 %v", calls.Load()-prior, err)
	}
	logCheckpointCost(t, "汇总失败后的续跑", usage)
}

// logCheckpointCost 用途 以人民币闲时定价记录同一组合成用量的可重复对照。
// 参数 t 为测试上下文，scenario 为比较场景，usage 为本轮模拟供应商的实际回包用量。
// 返回值 无。
func logCheckpointCost(t *testing.T, scenario string, usage []Usage) {
	t.Helper()
	var hit, miss, output int64
	for _, request := range usage {
		if !request.Reported || !request.CacheReported {
			t.Fatalf("成功用量应能区分缓存命中：%+v", request)
		}
		hit += request.CacheHitTokens
		miss += request.CacheMissTokens
		output += request.CompletionTokens
	}
	cost := float64(hit+50*miss+200*output) / 50_000_000
	t.Logf("%s: %d 次，命中 %d，未命中 %d，输出 %d，闲时模拟 ¥%.6f",
		scenario, len(usage), hit, miss, output, cost)
}

// checkpointDocuments 用途 生成有跨材料矛盾、全部使用虚构标识的三份附件。
// 参数 无。
// 返回值 可重复用于基线及复用比较的材料。
func checkpointDocuments() []Document {
	docs := []Document{
		auditFixture("filing_form", "合成备案表.docx", "系统名称：合成系统甲"),
		auditFixture("classification_report", "合成定级报告.docx", "系统名称：合成系统乙"),
		auditFixture("expert_opinion", "合成专家意见.docx", "系统名称：合成系统甲"),
	}
	for index := range docs {
		docs[index].AttachmentID = fmt.Sprintf("test-attachment-%d", index)
		docs[index].Lines[0].Location = "第 1 段"
		docs[index].Lines = append(docs[index].Lines, Line{Location: "第 2 段", Text: "定级对象编号：TEST-001"})
	}
	return docs
}

// writeCheckpointFixture 用途 根据本轮收到的实际出处生成一致的模拟预审，并可让一次汇总失败。
// 参数 w、r 为本地测试模型的响应和请求，failSummary 表示需要模拟的汇总失败次数。
// 返回值 无法解码请求或输出结果时的错误。
func writeCheckpointFixture(w http.ResponseWriter, r *http.Request, failSummary *atomic.Int32) error {
	var input struct {
		Messages []struct{ Content json.RawMessage }
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		return err
	}
	if len(input.Messages) != 2 {
		return fmt.Errorf("消息数量错误")
	}
	body := input.Messages[1].Content
	summary := body[0] == '"'
	var text string
	if summary {
		if failSummary.CompareAndSwap(1, 0) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return nil
		}
		if err := json.Unmarshal(body, &text); err != nil {
			return err
		}
	} else {
		var parts []aiContent
		if err := json.Unmarshal(body, &parts); err != nil {
			return err
		}
		text = parts[0].Text
	}
	var payload struct{ Sources []promptSource }
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return err
	}
	items := make([]aiItem, 0, len(Rules()))
	for _, rule := range Rules() {
		item := aiItem{RuleID: rule.ID, Status: "manual", Message: "缺少可单独核对的证据"}
		if rule.ID == "object_number" {
			for _, source := range payload.Sources {
				if strings.Contains(source.Text, "定级对象编号") {
					item.Evidence = append(item.Evidence, aiCitation{source.ID, source.Text})
				}
			}
			if len(item.Evidence) != 0 {
				item.Status, item.Message = "pass", "对象编号一致"
			}
		}
		if rule.ID == "system_and_unit" {
			item.Message = "已核对材料内系统名称"
			for _, source := range payload.Sources {
				if strings.Contains(source.Text, "系统名称") {
					item.Evidence = append(item.Evidence, aiCitation{source.ID, source.Text})
				}
			}
			if summary && strings.Contains(text, "合成系统甲") && strings.Contains(text, "合成系统乙") {
				item.Status, item.Message = "fail", "备案表和定级报告的系统名称不一致"
			}
		}
		items = append(items, item)
	}
	raw, err := json.Marshal(map[string]any{"items": items})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, `{"choices":[{"finish_reason":"stop","message":{"content":%q}}],"usage":{"prompt_tokens":420,"prompt_cache_hit_tokens":100,"prompt_cache_miss_tokens":320,"completion_tokens":140,"completion_tokens_details":{"reasoning_tokens":50}}}`, raw)
	return err
}

// assertCheckpointQuality 用途 检查跨材料冲突、证据映射和证据不足项的基线质量。
// 参数 t 为测试上下文，batch 为使用合成材料的审核结果。
// 返回值 无。
func assertCheckpointQuality(t *testing.T, batch BatchResult) {
	t.Helper()
	if len(batch.Checks) != 11 || batch.Checks[0].Status != "pass" || batch.Checks[1].Status != "fail" ||
		len(batch.Checks[1].Evidence) != 3 || batch.Checks[1].Evidence[1].AttachmentID != "test-attachment-1" ||
		batch.Checks[1].Evidence[1].Location != "第 1 段" || batch.Checks[3].Status != "manual" {
		t.Fatalf("跨材料冲突、定位或待核验状态异常：%+v", batch)
	}
}

// TestCheckpointIdentity 用途 检查材料改动只使对应单件和整批失效，名录变更使全部失效。
// 参数 t 为测试上下文。
// 返回值 无。
func TestCheckpointIdentity(t *testing.T) {
	model := newDeepSeek(defaultDeepSeekConfig(), "fixture-key")
	docs := checkpointDocuments()
	before, original, err := model.checkpointKeys(docs, Unit{Name: "测试单位甲"})
	if err != nil {
		t.Fatal(err)
	}
	docs[1].FileName = "合成附件.zip / 新目录/定级报告.docx"
	changed, singleEdit, err := model.checkpointKeys(docs, Unit{Name: "测试单位甲"})
	if err != nil || changed == before || singleEdit[0] != original[0] ||
		singleEdit[1] == original[1] || singleEdit[2] != original[2] {
		t.Fatal("归档成员路径变更后应重新核验对应的原文位置")
	}
	directory, unitEdit, err := model.checkpointKeys(docs, Unit{Name: "测试单位乙"})
	if err != nil || directory == changed {
		t.Fatal("名录变更应使整批失效")
	}
	for index, key := range unitEdit {
		if key == singleEdit[index] {
			t.Fatal("名录变更不应命中旧的单件结论")
		}
	}
}

// TestCheckpointRejectsInvalidEvidence 用途 确认模型的无效引文会降级且不会被缓存。
// 参数 t 为测试上下文。
// 返回值 无。
func TestCheckpointRejectsInvalidEvidence(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeBatchCompletion(w, "S1", "这段文字不在原件中")
	}))
	defer server.Close()
	model := newDeepSeek(defaultDeepSeekConfig(), "fixture-key")
	model.endpoint = server.URL
	store := memoryReviewCheckpoints{results: make(map[string]BatchResult)}
	for range 2 {
		result, err := model.AssessWithCheckpoints(t.Context(), checkpointDocuments()[:1], Unit{}, nil, store)
		if err != nil || len(result.Checks) != 11 || result.Checks[0].Status != "manual" {
			t.Fatalf("无效引文应转人工核验：%+v，%v", result, err)
		}
	}
	if calls.Load() != 4 || len(store.results) != 0 {
		t.Fatalf("无效引文不应写入复用结果：调用 %d，缓存 %d", calls.Load(), len(store.results))
	}
}
