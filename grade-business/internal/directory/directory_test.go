package directory

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/migrations"
)

// TestDirectoryMigration 核对名录和业务引用随真实迁移一并落地。
// 参数 t 为隔离测试的上下文。
// 返回值 无。
func TestDirectoryMigration(t *testing.T) {
	pool := directoryTestPool(t)
	for _, table := range []string{"directory_parties", "directory_people", "directory_events",
		"directory_unit_memberships"} {
		var found bool
		if err := pool.QueryRow(t.Context(),
			"SELECT to_regclass($1) IS NOT NULL", table).Scan(&found); err != nil || !found {
			t.Fatalf("名录表 %s 未随迁移建立: %v", table, err)
		}
	}
	for _, table := range []string{"business_cases", "implementation_reports",
		"case_review_events", "implementation_events"} {
		var count int
		err := pool.QueryRow(t.Context(), `SELECT count(*) FROM information_schema.columns
			WHERE table_schema=current_schema() AND table_name=$1 AND column_name='directory_snapshot'`,
			table).Scan(&count)
		if err != nil || count != 1 {
			t.Fatalf("业务表 %s 未包含历史名录快照: %v", table, err)
		}
	}
}

// directoryTestPool 仅连接明确指定的测试数据库，并在独立模式内执行生产迁移。
// 参数 t 提供运行及清理上下文。
// 返回值 隔离模式的连接池。
func directoryTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if dsn == "" {
		t.Skip("GRADE_BUSINESS_TEST_DSN 未配置")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil || config.ConnConfig.Database != "grade_business_test" {
		t.Fatal("名录测试仅支持明确指定的 grade_business_test 数据库")
	}
	control, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(control.Close)
	schema := fmt.Sprintf("directory_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := control.Exec(t.Context(), "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := control.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Errorf("清理名录测试模式失败: %v", err)
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
