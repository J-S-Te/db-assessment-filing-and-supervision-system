package publications

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPublicationMigrationBackfillsOnlyLiveDeliveries 验证旧表升级只为当前公告生成可追溯的实际送达。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestPublicationMigrationBackfillsOnlyLiveDeliveries(t *testing.T) {
	pool := publicationTestDatabase(t)
	scripts, err := filepath.Glob("../../migrations/*.sql")
	if err != nil || len(scripts) < 33 {
		t.Fatalf("migrations are missing: %v", err)
	}
	applyPublicationScripts(t, pool, scripts[:32]...)
	seedPublicationRecipient(t, pool, "BA-MIG-1", "CurrentOperator", "CurrentInstitution", "approved", false)
	seedPublicationRecipient(t, pool, "BA-MIG-2", "CancelledOperator", "CancelledInstitution", "approved", true)
	live := seedLegacyPublication(t, pool, "published", 4)
	withdrawn := seedLegacyPublication(t, pool, "withdrawn", 5)
	var before time.Time
	if err := pool.QueryRow(t.Context(), `SELECT clock_timestamp()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	applyPublicationScripts(t, pool, scripts[32:]...)
	router := publicationTestRouter(pool)
	assertMigratedPublicationDeliveries(t, router, live, withdrawn, before)
}

// assertMigratedPublicationDeliveries 核对现行公告补送时间和历史记录未被推断填充。
// 参数 t 为测试上下文，router 为实际路由，live 和 withdrawn 为存量公告标识，before 为迁移前数据库时间。
// 返回值 无。
func assertMigratedPublicationDeliveries(t *testing.T, router http.Handler, live, withdrawn string, before time.Time) {
	t.Helper()
	base := adminPath + "/" + live + "/deliveries"
	current := pubRequest[deliveryTestPage](t, router, "GET", base, nil, 200)
	if current.Version != 4 || current.Counts.Delivered != 2 || len(current.Items) != 2 {
		t.Fatalf("unexpected live legacy deliveries: %+v", current)
	}
	for _, receipt := range current.Items {
		if receipt.DeliveredAt.Before(before) || receipt.ReadAt != nil || receipt.AcknowledgedAt != nil {
			t.Fatalf("migration reports a synthetic past action: %+v", receipt)
		}
	}
	old := pubRequest[deliveryTestPage](t, router, "GET", base+"?version=2", nil, 200)
	closed := pubRequest[deliveryTestPage](t, router, "GET", adminPath+"/"+withdrawn+"/deliveries", nil, 200)
	if old.Version != 2 || old.Counts.Delivered != 0 || len(old.Items) != 0 ||
		closed.Version != 4 || closed.Counts.Delivered != 0 || len(closed.Items) != 0 {
		t.Fatalf("migration synthesized historical delivery: old=%+v withdrawn=%+v", old, closed)
	}
	pubRequest[map[string]any](t, router, "GET", readerPath+"/"+live+"?organization=CurrentOperator", nil, 200)
	pubRequest[map[string]any](t, router, "GET", readerPath+"/"+live+"?organization=CancelledOperator", nil, 404)
}

// seedLegacyPublication 构造有两次发布的迁移前快照，按需保留撤回事件。
// 参数 t 和 pool 为测试上下文及隔离连接，status 和 version 指定旧公告最新状态。
// 返回值 旧公告标识。
func seedLegacyPublication(t *testing.T, pool *pgxpool.Pool, status string, version int64) string {
	t.Helper()
	var id string
	err := pgx.BeginFunc(t.Context(), pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(t.Context(), `INSERT INTO local_publications(status,version,published_version,published_at)
			VALUES($1,$2::bigint,CASE WHEN $1='published' THEN $2::bigint END,now()-interval '1 day') RETURNING id::text`,
			status, version).Scan(&id)
		if err != nil {
			return err
		}
		_, err = tx.Exec(t.Context(), `INSERT INTO publication_events
			(publication_id,version,action,status,category,title,publisher,body,pinned,operator,occurred_at)
			SELECT $1,v.version,v.action,v.status,'announcement','旧公告','原发布单位','有效正文',false,
				'测试经办人',now()-interval '1 day'
			FROM (VALUES (1,'create','draft'),(2,'publish','published'),(3,'edit','published'),
				(4,'publish','published'),(5,'withdraw','withdrawn')) v(version,action,status)
			WHERE v.version<=$2`, id, version)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// applyPublicationScripts 在隔离测试模式按顺序运行真实迁移文件。
// 参数 t 和 pool 为测试上下文及隔离连接，files 为本测试使用的迁移文件名。
// 返回值 无。
func applyPublicationScripts(t *testing.T, pool *pgxpool.Pool, files ...string) {
	t.Helper()
	for _, name := range files {
		script, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), string(script)); err != nil {
			t.Fatalf("migration %s: %v", name, err)
		}
	}
}
