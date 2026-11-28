package filingaudit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDeepSeekUsage 验证计量只读取响应元数据，保留模型正文和完成校验。
// 参数 t 提供脱敏的请求、响应和用量观察器。
// 返回值 无。
func TestDeepSeekUsage(t *testing.T) {
	const choices = `"choices":[{"finish_reason":"stop","message":{"content":"审核结论",` +
		`"reasoning_content":"内部推理"}}]`
	const fullUsage = `"usage":{"prompt_tokens":120,"prompt_cache_hit_tokens":80,` +
		`"prompt_cache_miss_tokens":40,"completion_tokens":13,` +
		`"completion_tokens_details":{"reasoning_tokens":8}}`

	for _, row := range []struct {
		name string
		code int
		body string
		want Usage
	}{
		{
			name: "返回实际用量",
			code: http.StatusOK,
			body: `{"id":"synthetic-1",` + choices + "," + fullUsage + "}",
			want: Usage{RequestID: "synthetic-1", Outcome: "success", Reported: true,
				CacheReported: true, PromptTokens: 120, CacheHitTokens: 80,
				CacheMissTokens: 40, CompletionTokens: 13, ReasoningTokens: 8},
		},
		{
			name: "使用提示词细分兼容字段",
			code: http.StatusOK,
			body: "{" + choices + `,"usage":{"prompt_tokens":9,"completion_tokens":3,` +
				`"prompt_tokens_details":{"cached_tokens":5}}}`,
			want: Usage{RequestID: "synthetic-header", Outcome: "success", Reported: true,
				CacheReported: true, PromptTokens: 9, CacheHitTokens: 5,
				CacheMissTokens: 4, CompletionTokens: 3},
		},
		{
			name: "缓存细分缺失不可用于估价",
			code: http.StatusOK,
			body: "{" + choices + `,"usage":{"prompt_tokens":9,"completion_tokens":3}}`,
			want: Usage{RequestID: "synthetic-header", Outcome: "success", Reported: true,
				PromptTokens: 9, CompletionTokens: 3},
		},
		{
			name: "缓存数量失配保留可信总量",
			code: http.StatusOK,
			body: "{" + choices + `,"usage":{"prompt_tokens":9,"completion_tokens":3,` +
				`"prompt_cache_hit_tokens":7,"prompt_cache_miss_tokens":7,` +
				`"completion_tokens_details":{"reasoning_tokens":99}}}`,
			want: Usage{RequestID: "synthetic-header", Outcome: "success", Reported: true,
				PromptTokens: 9, CompletionTokens: 3},
		},
		{
			name: "可选细分字段类型异常仍记录总量",
			code: http.StatusOK,
			body: "{" + choices + `,"usage":{"prompt_tokens":9,"completion_tokens":3,` +
				`"prompt_cache_hit_tokens":"异常","prompt_cache_miss_tokens":7,` +
				`"completion_tokens_details":{"reasoning_tokens":"异常"}}}`,
			want: Usage{RequestID: "synthetic-header", Outcome: "success", Reported: true,
				PromptTokens: 9, CompletionTokens: 3},
		},
		{
			name: "无效统计不影响正文",
			code: http.StatusOK,
			body: `{"id":"有敏感内容的非标准ID",` + choices + `,"usage":{"prompt_tokens":-1,` +
				`"completion_tokens":99999999999999}}`,
			want: Usage{RequestID: "synthetic-header", Outcome: "success"},
		},
		{
			name: "非对象计量不影响正文",
			code: http.StatusOK,
			body: "{" + choices + `,"usage":"服务方异常计量值"}`,
			want: Usage{RequestID: "synthetic-header", Outcome: "success"},
		},
		{
			name: "过滤被供应商意外回显的密钥",
			code: http.StatusOK,
			body: `{"id":"trace-synthetic-key",` + choices + "}",
			want: Usage{Outcome: "success"},
		},
		{
			name: "截断响应仍记录已报告用量",
			code: http.StatusOK,
			body: `{"choices":[{"finish_reason":"length","message":{"content":"截断"}}],` +
				fullUsage + "}",
			want: Usage{RequestID: "synthetic-header", Outcome: "incomplete_response", Reported: true,
				CacheReported: true, PromptTokens: 120, CacheHitTokens: 80,
				CacheMissTokens: 40, CompletionTokens: 13, ReasoningTokens: 8},
		},
		{
			name: "正文结构异常保留已报告用量",
			code: http.StatusOK,
			body: `{"choices":null,` + fullUsage + "}",
			want: Usage{RequestID: "synthetic-header", Outcome: "invalid_response", Reported: true,
				CacheReported: true, PromptTokens: 120, CacheHitTokens: 80,
				CacheMissTokens: 40, CompletionTokens: 13, ReasoningTokens: 8},
		},
		{
			name: "HTTP错误无可核验用量",
			code: http.StatusTooManyRequests,
			body: `{"error":{"message":"服务方异常正文"}}`,
			want: Usage{RequestID: "synthetic-header", Outcome: "http_error"},
		},
		{
			name: "无效响应无用量",
			code: http.StatusOK,
			body: "服务方异常正文",
			want: Usage{RequestID: "synthetic-header", Outcome: "invalid_response"},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Request-ID", "synthetic-header")
				w.WriteHeader(row.code)
				_, _ = w.Write([]byte(row.body))
			}))
			defer server.Close()
			model := newDeepSeek(defaultDeepSeekConfig(), "synthetic-key")
			model.endpoint = server.URL
			var got []Usage
			ctx := WithUsageObserver(t.Context(), func(_ context.Context, usage Usage) {
				got = append(got, usage)
			})
			result, err := model.complete(withUsageStage(ctx, "document"), []byte("{}"))
			if row.want.Outcome == "success" {
				if err != nil || string(result) != "审核结论" {
					t.Fatalf("审核正文异常: %q, %v", result, err)
				}
			} else if err == nil {
				t.Fatal("不完整响应被当作成功")
			}
			row.want.Model = "deepseek-flash"
			row.want.Stage = "document"
			if len(got) != 1 || got[0] != row.want {
				t.Fatalf("计量不符合预期，实际 %+v，预期 %+v", got, row.want)
			}
		})
	}
}

// TestDeepSeekUsageTransportError 验证无响应时也计入一次调用。
// 参数 t 提供已关闭的本地服务和计量断言。
// 返回值 无。
func TestDeepSeekUsageTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(http.NotFound))
	server.Close()
	model := newDeepSeek(defaultDeepSeekConfig(), "synthetic-key")
	model.endpoint = server.URL
	var got []Usage
	ctx := WithUsageObserver(t.Context(), func(_ context.Context, usage Usage) {
		got = append(got, usage)
	})
	if _, err := model.complete(withUsageStage(ctx, "summary"), []byte("{}")); err == nil {
		t.Fatal("应返回连接错误")
	}
	want := Usage{Model: "deepseek-flash", Stage: "summary", Outcome: "transport_error"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("传输错误计量不符合预期，实际 %+v，预期 %+v", got, want)
	}
}
