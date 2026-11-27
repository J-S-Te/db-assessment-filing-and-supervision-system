package filingaudit

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestDeepSeekReviewsFilesBeforeSummary 验证原件分别进入独立请求，终审只接收核验后的依据。
// 参数 t 提供本地模型替身和两份合成材料。
// 返回值 无。
func TestDeepSeekReviewsFilesBeforeSummary(t *testing.T) {
	const formText = "备案表独有字段"
	const reportText = "定级报告独有字段"
	const unused = "从未被引用的正文"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []struct {
				Role    string
				Content json.RawMessage
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		n := calls.Add(1)
		if len(input.Messages) != 2 || input.Messages[0].Role != "system" || input.Messages[1].Role != "user" {
			t.Error("单次请求应只有本轮指令和输入")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		payload := string(input.Messages[1].Content)
		switch n {
		case 1:
			if !strings.Contains(payload, formText) || strings.Contains(payload, reportText) {
				t.Error("备案表请求混入另一份原件")
			}
			writeBatchCompletion(w, "S1", formText)
		case 2:
			if !strings.Contains(payload, reportText) || strings.Contains(payload, formText) {
				t.Error("定级报告请求混入另一份原件")
			}
			writeBatchCompletion(w, "S1", reportText)
		case 3:
			if !strings.Contains(payload, formText) || !strings.Contains(payload, reportText) ||
				strings.Contains(payload, unused) || !strings.Contains(payload, "fileReviews") {
				t.Error("终审输入缺少逐件核验结果或包含未引用的原件正文")
			}
			writeBatchCompletion(w, "S2", reportText)
		default:
			t.Errorf("不应调用模型第 %d 次", n)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	model := newDeepSeek(DeepSeekConfig{Model: "deepseek-flash", MaxTokens: 4096, TimeoutSeconds: 5}, "fixture-key")
	model.endpoint = server.URL
	form := auditFixture("filing_form", "备案表.docx", formText, unused)
	report := auditFixture("classification_report", "定级报告.docx", reportText)
	form.AttachmentID, report.AttachmentID = "form-id", "report-id"
	results, err := model.Assess(t.Context(), []Document{form, report}, Unit{})
	if err != nil || calls.Load() != 3 || len(results) != 11 {
		t.Fatalf("需要两次单件审核及一次终审，实际调用 %d 次，结果 %d，错误 %v", calls.Load(), len(results), err)
	}
	if evidence := results[0].Evidence; len(evidence) != 1 || evidence[0].AttachmentID != "report-id" {
		t.Fatalf("终审引用未还原到正确附件：%+v", evidence)
	}
}

// TestDeepSeekRequiresCompleteFileReviews 验证未读原件阻止终审并保留已完成的逐件记录。
// 参数 t 提供合成原件和本地 HTTP 模型替身。
// 返回值 无。
func TestDeepSeekRequiresCompleteFileReviews(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeBatchCompletion(w, "S1", "独立原件")
	}))
	defer server.Close()
	model := newDeepSeek(DeepSeekConfig{Model: "deepseek-flash", MaxTokens: 4096, TimeoutSeconds: 5}, "fixture-key")
	model.endpoint = server.URL
	batch, err := model.AssessBatch(t.Context(), []Document{
		auditFixture("filing_form", "备案表.docx", "独立原件"),
		{FileName: "损坏的定级报告.docx", Kind: "classification_report"},
	}, Unit{}, nil)
	if !errors.Is(err, ErrNoEvidence) || calls.Load() != 1 || len(batch.Checks) != 0 ||
		len(batch.Materials) != 2 || batch.Materials[0].Status != "reviewed" ||
		batch.Materials[1].Status != "unreadable" {
		t.Fatalf("不完整批次不能给出 AI 终审结论：调用 %d 次，批次 %+v，错误 %v", calls.Load(), batch, err)
	}
}

// TestDeepSeekSummarizesManualFindings 验证原件都已审核但依据不足时仍完成 AI 终审。
// 参数 t 提供合成原件和两轮模型替身。
// 返回值 无。
func TestDeepSeekSummarizesManualFindings(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeBatchCompletion(w, "", "")
	}))
	defer server.Close()
	model := newDeepSeek(DeepSeekConfig{Model: "deepseek-flash", MaxTokens: 4096, TimeoutSeconds: 5}, "fixture-key")
	model.endpoint = server.URL
	results, err := model.Assess(t.Context(), []Document{
		auditFixture("filing_form", "备案表.docx", "备案表原文"),
	}, Unit{})
	if err != nil || calls.Load() != 2 || len(results) != 11 || results[0].Status != "manual" {
		t.Fatalf("原件都已审核时应汇总为待核实结论：调用 %d 次、结果 %d 项、错误 %v", calls.Load(), len(results), err)
	}
}

// writeBatchCompletion 以合成证据写入一份正式规则响应。
// 参数 w、source、quote 分别为响应、出处标识和本次核对的摘录。
// 返回值 无。
func writeBatchCompletion(w http.ResponseWriter, source, quote string) {
	items := make([]aiItem, 0, len(Rules()))
	for _, rule := range Rules() {
		item := aiItem{RuleID: rule.ID, Status: "manual", Message: "需人工核实"}
		if rule.ID == "object_number" {
			item.Status, item.Message, item.Evidence = "pass", "原件已核对", []aiCitation{{SourceID: source, Quote: quote}}
		}
		items = append(items, item)
	}
	body, _ := json.Marshal(map[string]any{"items": items})
	fmt.Fprintf(w, `{"choices":[{"finish_reason":"stop","message":{"content":%q}}]}`, body)
}
