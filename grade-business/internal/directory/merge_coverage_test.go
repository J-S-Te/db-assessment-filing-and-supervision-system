package directory

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestUnitMergeRejectsConcurrentCoverageRegistration 验证归并拒绝与来源清册登记交错，重试时移交登记结果。
// 参数 t 提供隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestUnitMergeRejectsConcurrentCoverageRegistration(t *testing.T) {
	for name, mode := range map[string]string{"simple": "", "complex": complexMerge} {
		t.Run(name, func(t *testing.T) {
			pool := directoryTestPool(t)
			mux := http.NewServeMux()
			Register(mux, pool)
			main := mergeTestUnit(t, mux, "清册接续单位", "913300001000001051")
			source := mergeTestUnit(t, mux, "清册原单位", "913300001000001052")
			svc := service{pool: pool}
			input := mergeInput{TargetID: main.ID, SourceIDs: []string{source.ID}, Mode: mode}
			tx := lockCoverageRegistration(t, pool, source.ID)
			defer tx.Rollback(t.Context())
			confirmCoverageMerge(t, svc, input, errMergeBusy)
			registerLockedCoverage(t, tx, source.ID)
			confirmCoverageMerge(t, svc, input, nil)
			verifyCoverageMerge(t, pool, source.ID, main.ID)
		})
	}
}

// lockCoverageRegistration 模拟清册写入检查启用单位后持有真实共享行锁的时点。
// 参数 t、pool 为测试上下文与隔离库，sourceID 为即将登记的原单位。
// 返回值 已取得源目录共享锁、尚未插入清册的事务。
func lockCoverageRegistration(t *testing.T, pool *pgxpool.Pool, sourceID string) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var id string
	err = tx.QueryRow(t.Context(), `SELECT id::text FROM directory_parties
		WHERE id=$1 AND kind='unit' AND status='active' AND merged_into_id IS NULL FOR SHARE`, sourceID).Scan(&id)
	if err != nil {
		_ = tx.Rollback(t.Context())
		t.Fatal(err)
	}
	return tx
}

// confirmCoverageMerge 复核数据库计划并在限定时间内确认预期的归并结果。
// 参数 t、svc 为测试上下文及真实归并服务，input 为主从单位，want 为预期错误。
// 返回值 无。
func confirmCoverageMerge(t *testing.T, svc service, input mergeInput, want error) {
	t.Helper()
	plan, err := svc.previewMerge(t.Context(), input)
	if err != nil || plan.Token == "" {
		t.Fatalf("清册归并预览失败：%+v，错误 %v", plan, err)
	}
	input.Token = plan.Token
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, err := svc.confirmMerge(ctx, input); !errors.Is(err, want) {
		t.Fatalf("清册登记期间归并结果为 %v，预期 %v", err, want)
	}
}

// registerLockedCoverage 完成已核对原单位归属的在册对象登记。
// 参数 t、tx 为测试上下文与登记事务，sourceID 为原单位。
// 返回值 无。
func registerLockedCoverage(t *testing.T, tx pgx.Tx, sourceID string) {
	t.Helper()
	if _, err := tx.Exec(t.Context(), `INSERT INTO archive_coverage_targets
		(unit_id,system_name,reference,source) VALUES($1,'待备案系统','MERGE-CONCURRENT','合成清册')`,
		sourceID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// verifyCoverageMerge 检查重试之后承办方的在册对象与原归属留痕。
// 参数 t、pool 为测试上下文与隔离库，sourceID、targetID 为归并前后单位。
// 返回值 无。
func verifyCoverageMerge(t *testing.T, pool *pgxpool.Pool, sourceID, targetID string) {
	t.Helper()
	var moved bool
	err := pool.QueryRow(t.Context(), `SELECT c.unit_id=$2 AND c.active AND c.version=2 AND EXISTS (
		SELECT 1 FROM archive_registration_events e WHERE e.resource='coverage-targets' AND e.record_id=c.id
		AND e.version=2 AND e.before_values->>'unit_id'=$1 AND e.after_values->>'unit_id'=($2::uuid)::text)
		FROM archive_coverage_targets c WHERE c.reference='MERGE-CONCURRENT'`, sourceID, targetID).Scan(&moved)
	if err != nil || !moved {
		t.Fatalf("重试未移交并保留清册登记历史：%v，错误 %v", moved, err)
	}
}
