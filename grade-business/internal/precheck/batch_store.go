package precheck

import (
	"context"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/filingpolicy"
)

const readBatchFields = `id,filing_id,revision,policy_version,check_profile,created_at,status,findings,
	check_results,check_source,fallback_reason,failure,progress_stage,material_reviews,material_review_total,
	unit_fingerprint`

const archiveBatchFields = readBatchFields + `,materials,policy_rules,attempt,lease_until,claim_token,force_ai`

const insertCurrentBatch = `INSERT INTO filing_prechecks AS current
	(filing_id,revision,materials,policy_version,policy_rules,check_profile,force_ai) VALUES($1,$2,$3,$4,$5,$6,$7)
	ON CONFLICT (filing_id,revision,policy_version) DO UPDATE SET force_ai=
		CASE WHEN current.status='queued' THEN current.force_ai OR EXCLUDED.force_ai ELSE current.force_ai END
	RETURNING id::text,status,findings,check_results,check_source,fallback_reason,failure,
		progress_stage,material_reviews,material_review_total,force_ai`

// startBatch 锁定当前版本的批次，完成或失败后原子归档并创建新批次。
// 参数 ctx、filing、snapshot、policy 为请求上下文、备案、材料快照与规则版本，force 表示显式重审。
// 返回值 本版本进行中的批次、刚创建的批次，或数据库错误。
func (s *Service) startBatch(ctx context.Context, filing string, snapshot Snapshot,
	policy filingpolicy.Policy, force bool,
) (Job, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback(ctx)
	args := []any{filing, snapshot.Revision, snapshot.Materials, policy.Version,
		policy.For(filingpolicy.Initial), s.profile(), force}
	job, activeForce, err := scanBatch(tx.QueryRow(ctx, insertCurrentBatch, args...))
	if err != nil {
		return Job{}, err
	}
	if job.Status == "succeeded" || job.Status == "failed" {
		if err := archiveBatch(ctx, tx, job.ID); err != nil {
			return Job{}, err
		}
		job, _, err = scanBatch(tx.QueryRow(ctx, insertCurrentBatch, args...))
		if err != nil {
			return Job{}, err
		}
	}
	if job.Status == "running" && force && !activeForce {
		return Job{}, errPrecheckInProgress
	}
	return job, tx.Commit(ctx)
}

// archiveBatch 在事务内把已锁定的旧批次移动到只读历史表。
// 参数 ctx、tx、id 为事务上下文、已持有行锁的事务和批次号。
// 返回值 归档写入错误，事务由发起方统一提交。
func archiveBatch(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `WITH moved AS (
		DELETE FROM filing_prechecks WHERE id=$1 RETURNING `+archiveBatchFields+`
	) INSERT INTO filing_precheck_history (`+archiveBatchFields+`) SELECT * FROM moved`, id)
	return err
}

// scanBatch 读取提交审核接口返回的当前批次信息。
// 参数 row 为已执行的批次提交查询。
// 返回值 批次的状态、是否为强制重审，或数据库扫描错误。
func scanBatch(row pgx.Row) (Job, bool, error) {
	var job Job
	var force bool
	err := row.Scan(&job.ID, &job.Status, &job.Findings, &job.Checks, &job.Source, &job.Fallback,
		&job.Failure, &job.Stage, &job.MaterialReviews, &job.MaterialReviewTotal, &force)
	return job, force, err
}
