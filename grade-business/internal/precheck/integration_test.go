package precheck

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/filingpolicy"
	"grade-business/migrations"
)

// TestStoredJobRevision 验证真实 PostgreSQL 中的预审队列、去重和版本变化。
// 参数 t 为隔离测试上下文。
// 返回值 无。
func TestStoredJobRevision(t *testing.T) {
	pool := isolatedPool(t)
	const filing = "00000000-0000-4000-8000-000000000001"
	const other = "00000000-0000-4000-8000-000000000002"
	revision := int64(1)
	service := New(pool, func(_ context.Context, id string) (Snapshot, error) {
		if id != filing && id != other {
			return Snapshot{}, ErrNotFound
		}
		return Snapshot{Revision: revision}, nil
	})
	first, err := service.Start(t.Context(), filing)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := service.Start(t.Context(), filing)
	if err != nil || repeated.ID != first.ID || repeated.Status != "queued" {
		t.Fatalf("unexpected duplicate %+v %v", repeated, err)
	}
	if err := service.runOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	complete, err := service.Get(t.Context(), filing, first.ID)
	if err != nil || complete.Status != "succeeded" || len(complete.Findings) != 2 || complete.Stale {
		t.Fatalf("run result %+v %v", complete, err)
	}
	_, err = filingpolicy.Replace(t.Context(), pool, 1, []filingpolicy.Rule{{
		Stage: filingpolicy.Initial, Kind: "supporting", Label: "单位说明", FilenameContains: "单位说明",
	}})
	if err != nil {
		t.Fatal(err)
	}
	previous, err := service.Get(t.Context(), filing, first.ID)
	if err != nil || !previous.Stale {
		t.Fatalf("policy update did not expire the old job: %+v %v", previous, err)
	}
	updated, err := service.Start(t.Context(), filing)
	if err != nil || updated.ID == first.ID || updated.Stale {
		t.Fatalf("policy update did not create a new job: %+v %v", updated, err)
	}
	if err := service.runOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := service.Get(t.Context(), filing, updated.ID)
	if err != nil || current.Stale || len(current.Findings) != 3 {
		t.Fatalf("current job missing configured rule: %+v %v", current, err)
	}
	revision++
	outdated, err := service.Get(t.Context(), filing, updated.ID)
	if err != nil || !outdated.Stale {
		t.Fatalf("stale result %+v %v", outdated, err)
	}
	if _, err := service.Get(t.Context(), other, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-record read got %v", err)
	}
}

// isolatedPool 在专用业务测试库建立独立 schema 并应用迁移。
// 参数 t 控制访问和清理期限。
// 返回值 指向本测试 schema 的连接池。
func isolatedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if dsn == "" {
		t.Skip("GRADE_BUSINESS_TEST_DSN is not configured")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if config.ConnConfig.Database != "grade_business_test" {
		t.Fatal("expected isolated database grade_business_test")
	}
	admin, err := pgxpool.NewWithConfig(t.Context(), config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("pc_%d", time.Now().UnixNano())
	name := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+name+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrations.Apply(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}
