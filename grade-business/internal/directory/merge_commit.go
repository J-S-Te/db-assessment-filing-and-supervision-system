package directory

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// commitMergePlan 保存本次合并的原始证据、主从状态和实际备案归属。
// 参数 ctx、tx 为确认事务，plan 为重新核验的计划，sourceIDs 为规范化来源列表。
// 返回值 持久化合并回执，或整批回滚错误。
func commitMergePlan(ctx context.Context, tx pgx.Tx, plan mergePlan, sourceIDs []string) (mergeResult, error) {
	result := mergeResult{TargetID: plan.Target.ID, SourceIDs: sourceIDs, MovedCount: len(plan.Moves)}
	token := plan.Token
	plan.Token = ""
	err := tx.QueryRow(ctx, `INSERT INTO directory_unit_merges(target_id,preview_token,plan,actor)
		VALUES($1,$2,$3,'监管端') RETURNING id::text,merged_at`, plan.Target.ID, token, plan).
		Scan(&result.ID, &result.MergedAt)
	if err != nil {
		return mergeResult{}, err
	}
	before, err := mergeDirectorySnapshots(ctx, tx, plan)
	if err != nil {
		return mergeResult{}, err
	}
	if plan.Mode == complexMerge {
		if err := recordComplexMergeImpact(ctx, tx, plan, result.ID); err != nil {
			return mergeResult{}, err
		}
	}
	if err := migrateMergeFilings(ctx, tx, plan); err != nil {
		return mergeResult{}, err
	}
	if err := migrateCoverageTargets(ctx, tx, plan.Target.ID, sourceIDs); err != nil {
		return mergeResult{}, err
	}
	if err := updateMergedDirectories(ctx, tx, plan, result); err != nil {
		return mergeResult{}, err
	}
	return result, saveMergedDirectoryEvents(ctx, tx, before)
}

// mergeDirectorySnapshots 保存变更前的主从目录数据。
// 参数 ctx、tx 为当前合并事务，plan 为重新确认的目标。
// 返回值 实际变更前的目录值或读库错误。
func mergeDirectorySnapshots(ctx context.Context, tx pgx.Tx, plan mergePlan) ([]entry, error) {
	all := append([]mergeUnit{plan.Target}, plan.Sources...)
	items := make([]entry, 0, len(all))
	for _, item := range all {
		value, err := readEntry(ctx, tx, unit, "", item.ID)
		if err != nil {
			return nil, err
		}
		items = append(items, value)
	}
	return items, nil
}

// migrateMergeFilings 移交有效备案及复杂模式核准的在审首报，保留原附件、状态与审核证据。
// 参数 ctx、tx 为确认事务，plan 包含准确的备案范围与主单位。
// 返回值 实际影响等于预览时为空，或业务范围变化及数据库错误。
func migrateMergeFilings(ctx context.Context, tx pgx.Tx, plan mergePlan) error {
	tag, err := tx.Exec(ctx, `UPDATE business_cases f SET organization_name=$1,
		directory_unit_id=$2,version=version+1
		WHERE f.id=ANY($3::uuid[]) AND f.kind='filing' AND f.cancelled_at IS NULL AND f.discarded_at IS NULL
		AND (f.status='approved' OR ($4 AND `+mergeOperatorFilingReview+`))`,
		plan.Target.ScopeName, plan.Target.ID, moveIDs(plan.Moves), plan.Mode == complexMerge)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != int64(len(plan.Moves)) {
		return errVersion
	}
	return nil
}

// migrateCoverageTargets 在已确认归并的同一事务中移交在册对象并保留版本事件。
// 参数 ctx 和 tx 为归并事务，targetID 和 sourceIDs 为已锁定的主单位与来源单位。
// 返回值 已迁移时为 nil，写入异常时整笔归并回滚。
func migrateCoverageTargets(ctx context.Context, tx pgx.Tx, targetID string, sourceIDs []string) error {
	_, err := tx.Exec(ctx, `UPDATE archive_coverage_targets
		SET unit_id=$1,version=version+1,updated_at=clock_timestamp()
		WHERE active AND unit_id=ANY($2::uuid[])`, targetID, sourceIDs)
	return err
}

// updateMergedDirectories 复制现有机构关系，并永久冻结已合并主档。
// 参数 ctx、tx 为确认事务，plan 及 result 提供主从范围和合并依据。
// 返回值 写入错误或当前事务内更新成功。
func updateMergedDirectories(ctx context.Context, tx pgx.Tx, plan mergePlan, result mergeResult) error {
	if _, err := tx.Exec(ctx, `INSERT INTO directory_unit_memberships(institution_id,unit_id)
		SELECT DISTINCT institution_id,$1::uuid FROM directory_unit_memberships
		WHERE unit_id::text=ANY($2::text[]) ON CONFLICT DO NOTHING`, plan.Target.ID, result.SourceIDs); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE directory_parties SET merged_into_id=$1,
		merge_id=$2,merged_at=$3,status='inactive',version=version+1
		WHERE id::text=ANY($4::text[])`, plan.Target.ID, result.ID, result.MergedAt, result.SourceIDs); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE directory_parties SET version=version+1 WHERE id=$1`, plan.Target.ID)
	return err
}

// saveMergedDirectoryEvents 为主单位和来源单位记录归属前后快照。
// 参数 ctx、tx 为确认事务，previous 为刚发生变更前的所有主档。
// 返回值 留痕写入结果。
func saveMergedDirectoryEvents(ctx context.Context, tx pgx.Tx, previous []entry) error {
	for _, before := range previous {
		after, err := readEntry(ctx, tx, unit, "", before.ID)
		if err != nil {
			return err
		}
		if err := recordEvent(ctx, tx, unit, "", "merge", &before, after); err != nil {
			return err
		}
	}
	return nil
}
