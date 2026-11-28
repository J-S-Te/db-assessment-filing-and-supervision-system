package filingaudit

import (
	"context"
	"encoding/json"
	"strings"
)

// Usage 保存一次模型 HTTP 调用的状态和可核验计量。
// Reported 指供应商给出了可信的输入与输出总量，CacheReported 表示缓存细分也可用。
type Usage struct {
	Model, RequestID, Stage, Outcome  string
	Reported, CacheReported           bool
	PromptTokens, CacheHitTokens      int64
	CacheMissTokens, CompletionTokens int64
	ReasoningTokens                   int64
}

type usageContextKey uint8

const (
	usageObserverKey usageContextKey = iota
	usageStageKey
	maxUsageTokens = 10_000_000
)

// WithUsageObserver 注册当前审核范围内的模型调用计量观察器。
// 参数 ctx 为审核上下文，observe 为接收调用元数据的同步回调。
// 返回值 携带观察器的上下文。
func WithUsageObserver(ctx context.Context, observe func(context.Context, Usage)) context.Context {
	return context.WithValue(ctx, usageObserverKey, observe)
}

// withUsageStage 标记当前模型调用所属的审核阶段。
// 参数 ctx 为审核上下文，stage 为逐件或汇总阶段。
// 返回值 携带阶段标识的上下文。
func withUsageStage(ctx context.Context, stage string) context.Context {
	return context.WithValue(ctx, usageStageKey, stage)
}

// usageRequestID 选取格式可控的供应商请求标识。
// 参数 candidates 为按优先顺序排列的响应 ID 和请求头标识。
// 返回值 第一项格式有效的标识，无有效项时为空。
func usageRequestID(candidates ...string) string {
	for _, candidate := range candidates {
		if len(candidate) == 0 || len(candidate) > 128 {
			continue
		}
		if strings.IndexFunc(candidate, func(char rune) bool {
			return !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
				char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.')
		}) < 0 {
			return candidate
		}
	}
	return ""
}

type deepSeekUsagePayload struct {
	PromptTokens      *int64          `json:"prompt_tokens"`
	CompletionTokens  *int64          `json:"completion_tokens"`
	CacheHitTokens    json.RawMessage `json:"prompt_cache_hit_tokens"`
	CacheMissTokens   json.RawMessage `json:"prompt_cache_miss_tokens"`
	PromptDetails     json.RawMessage `json:"prompt_tokens_details"`
	CompletionDetails json.RawMessage `json:"completion_tokens_details"`
}

// validUsageCount 检查供应商返回的计数是否存在且位于有界范围内。
// 参数 count 是可缺省的计数字段。
// 返回值 计数存在、非负且未超出当前统计上限时为真。
func validUsageCount(count *int64) bool {
	return count != nil && *count >= 0 && *count <= maxUsageTokens
}

// optionalUsageCount 用途 解析一项可选计量，隔离单项类型异常。
// 参数 raw 为单项 JSON。
// 返回值 合法计数或空值。
func optionalUsageCount(raw json.RawMessage) *int64 {
	var count *int64
	if json.Unmarshal(raw, &count) != nil || !validUsageCount(count) {
		return nil
	}
	return count
}

// readCounts 独立解码计量数据，保留可信总量及能够核对的缓存细分。
// 参数 raw 为响应中独立的 usage JSON 字段。
// 返回值 无，无效的计量不改变审核正文。
func (usage *Usage) readCounts(raw json.RawMessage) {
	var value deepSeekUsagePayload
	if json.Unmarshal(raw, &value) != nil ||
		!validUsageCount(value.PromptTokens) || !validUsageCount(value.CompletionTokens) {
		return
	}
	usage.Reported = true
	usage.PromptTokens, usage.CompletionTokens = *value.PromptTokens, *value.CompletionTokens
	var completion struct {
		Reasoning json.RawMessage `json:"reasoning_tokens"`
	}
	if json.Unmarshal(value.CompletionDetails, &completion) == nil {
		if count := optionalUsageCount(completion.Reasoning); count != nil && *count <= usage.CompletionTokens {
			usage.ReasoningTokens = *count
		}
	}
	usage.readCacheCounts(value)
}

// readCacheCounts 用途 读取可与输入总量核对的缓存拆分。
// 参数 value 为已确认总量的供应商计量负载。
// 返回值 无，缺失或无效的拆分继续标记未知。
func (usage *Usage) readCacheCounts(value deepSeekUsagePayload) {
	hit, miss := optionalUsageCount(value.CacheHitTokens), optionalUsageCount(value.CacheMissTokens)
	if hit != nil && miss != nil && *hit+*miss == usage.PromptTokens {
		usage.CacheReported, usage.CacheHitTokens, usage.CacheMissTokens = true, *hit, *miss
		return
	}
	var prompt struct {
		Cached json.RawMessage `json:"cached_tokens"`
	}
	if value.CacheHitTokens == nil && value.CacheMissTokens == nil &&
		json.Unmarshal(value.PromptDetails, &prompt) == nil {
		if hit := optionalUsageCount(prompt.Cached); hit != nil && *hit <= usage.PromptTokens {
			usage.CacheReported, usage.CacheHitTokens, usage.CacheMissTokens = true, *hit, usage.PromptTokens-*hit
		}
	}
}
