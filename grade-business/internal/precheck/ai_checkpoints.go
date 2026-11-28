package precheck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/filingaudit"
)

var errAIInputSuperseded = errors.New("备案预审输入版本已更新")
var errAILeaseLost = errors.New("备案预审任务租约已失效")

type checkpointedAssessor interface {
	AssessWithCheckpoints(context.Context, []filingaudit.Document, filingaudit.Unit,
		filingaudit.BatchProgress, filingaudit.ReviewCheckpoints) (filingaudit.BatchResult, error)
}

type aiExecution struct {
	service            *Service
	job, filing, claim string
	revision, policy   int64
	unit               filingaudit.Unit
	unitKey            string
	force              bool
	err                error
}

// Load 用途 读取同一备案和规则下已核验的输入结果。
// 参数 ctx 为当前执行上下文，key 为材料或批次指纹。
// 返回值 已保存的结论、是否命中及查询错误。
func (run *aiExecution) Load(ctx context.Context, key string) (filingaudit.BatchResult, bool, error) {
	var result filingaudit.BatchResult
	err := run.service.pool.QueryRow(ctx, `SELECT result FROM filing_precheck_ai_checkpoints
		WHERE filing_id=$1 AND policy_version=$2 AND input_key=$3 AND (NOT $4 OR job_id=$5)`,
		run.filing, run.policy, key, run.force, run.job).Scan(&result)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, false, nil
	}
	run.err = err
	return result, err == nil, err
}

// Save 用途 在逐件进度写入前持久化核验后的结论，供任务租约恢复时复用。
// 参数 ctx、key、result 为执行上下文、输入指纹和核验结论。
// 返回值 写入失败或当前租约失效时的错误。
func (run *aiExecution) Save(ctx context.Context, key string, result filingaudit.BatchResult) error {
	// 已完成的模型响应仍可在短暂的独立写入期保存，写入资格由数据库租约决定。
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	tag, err := run.service.pool.Exec(saveCtx, `INSERT INTO filing_precheck_ai_checkpoints
		(filing_id,policy_version,input_key,job_id,result)
		SELECT filing_id,policy_version,$3,id,$4 FROM filing_prechecks
		WHERE id=$1 AND claim_token=$2 AND status='running' AND lease_until>now()
		FOR UPDATE
		ON CONFLICT (filing_id,policy_version,input_key)
		DO UPDATE SET job_id=EXCLUDED.job_id,result=EXCLUDED.result`, run.job, run.claim, key, result)
	if err == nil && tag.RowsAffected() == 0 {
		err = errAILeaseLost
	}
	run.err = err
	return err
}

// BeforeRequest 用途 在复用批次和发出付费请求前核对材料版本、规则版本与执行租约。
// 参数 ctx 为当前执行上下文。
// 返回值 已过期、租约失效或查询错误。
func (run *aiExecution) BeforeRequest(ctx context.Context) error {
	snapshot, err := run.service.source(ctx, run.filing)
	if errors.Is(err, ErrNotFound) || (err == nil && snapshot.Revision != run.revision) {
		run.err = errAIInputSuperseded
		return run.err
	}
	if err != nil {
		run.err = err
		return err
	}
	if run.service.unit != nil {
		currentUnit, err := run.service.unit(ctx, run.filing)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && currentUnit != run.unit) {
			err = errAIInputSuperseded
		}
		if err != nil {
			run.err = err
			return err
		}
	}
	run.err = run.checkLease(ctx)
	return run.err
}

// unitFingerprint 用途 为预审结果标记参与模型或本地规则判断的单位名录事实。
// 参数 unit 为已读取的单位名、信用代码和行政区划。
// 返回值 名录事实的 SHA-256 指纹。
func unitFingerprint(unit filingaudit.Unit) string {
	raw, _ := json.Marshal(unit)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// checkLease 用途 确认当前规则版本和执行者持有有效租约。
// 参数 ctx 为本次检查上下文。
// 返回值 查询失败、规则更新或租约失效错误。
func (run *aiExecution) checkLease(ctx context.Context) error {
	var leaseValid, policyValid bool
	err := run.service.pool.QueryRow(ctx, `SELECT
		EXISTS (SELECT 1 FROM filing_prechecks WHERE id=$1 AND claim_token=$2
			AND status='running' AND lease_until>now()),
		EXISTS (SELECT 1 FROM filing_material_policy WHERE singleton AND version=$3)`,
		run.job, run.claim, run.policy).Scan(&leaseValid, &policyValid)
	if err != nil {
		return err
	}
	if !leaseValid {
		return errAILeaseLost
	}
	if !policyValid {
		return errAIInputSuperseded
	}
	return nil
}

// recordUsage 用途 独立保存本次模型请求的计量结果，包括模型未返回用量的调用。
// 参数 ctx 为请求上下文，usage 为供应商返回的计数及已归类的请求结果。
// 返回值 无，写入失败记录运营日志。
func (run *aiExecution) recordUsage(ctx context.Context, usage filingaudit.Usage) {
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_, err := run.service.pool.Exec(recordCtx, `INSERT INTO filing_precheck_ai_usage
		(filing_id,job_id,claim_token,stage,model,provider_request_id,outcome,usage_reported,cache_reported,
		 prompt_tokens,prompt_cache_hit_tokens,prompt_cache_miss_tokens,completion_tokens,reasoning_tokens)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		run.filing, run.job, run.claim, usage.Stage, usage.Model, usage.RequestID, usage.Outcome,
		usage.Reported, usage.CacheReported, usage.PromptTokens, usage.CacheHitTokens,
		usage.CacheMissTokens, usage.CompletionTokens, usage.ReasoningTokens)
	if err != nil {
		slog.Warn("备案预审模型用量暂时无法写入", "jobId", run.job,
			"stage", usage.Stage, "model", usage.Model, "outcome", usage.Outcome,
			"usageReported", usage.Reported, "cacheReported", usage.CacheReported,
			"promptTokens", usage.PromptTokens, "cacheHitTokens", usage.CacheHitTokens,
			"cacheMissTokens", usage.CacheMissTokens, "completionTokens", usage.CompletionTokens,
			"reasoningTokens", usage.ReasoningTokens)
	}
}
