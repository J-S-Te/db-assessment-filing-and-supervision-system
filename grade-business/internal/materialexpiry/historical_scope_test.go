package materialexpiry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestHistoricalFilingSourceRejectsTransferredRecipients 验证备案转单位、换机构和归并后不纳入已失去原归属的材料。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestHistoricalFilingSourceRejectsTransferredRecipients(t *testing.T) {
	for _, changed := range []string{"unit", "institution", "merge"} {
		t.Run(changed, func(t *testing.T) {
			db, files := expiryFixture(t)
			filing, source, today := seedCaseMaterial(t, db, files)
			router := http.NewServeMux()
			New(db, files, "有效期测试监管").Register(router)
			if changed == "merge" {
				insertHistoricalMerge(t, db, filing)
			} else {
				recordRecipientChange(t, db, filing, changed)
			}
			page := expiryCall(t, router, "GET", candidateURL+"?caseKind=filing", nil, 200)
			if len(page["items"].([]any)) != 0 {
				t.Errorf("不再属于当前收件范围的原件仍被列入候选：%v", page)
			}
			expiryCall(t, router, "POST", testRegulatorPath, map[string]any{
				"requestId": "90000000-0000-4000-8000-000000000051", "sourceAttachmentId": source,
				"validUntil": today, "evidence": "旧原件日期核对",
			}, 409)
		})
	}
}

// TestHistoricalFilingRegistrationRechecksConcurrentTransfer 验证补录等待已在途的单位变更提交后重新核验归属。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestHistoricalFilingRegistrationRechecksConcurrentTransfer(t *testing.T) {
	db, files := expiryFixture(t)
	filing, source, today := seedCaseMaterial(t, db, files)
	router := http.NewServeMux()
	New(db, files, "有效期测试监管").Register(router)
	tx, err := db.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	recordRecipientChange(t, tx, filing, "unit")
	responses := make(chan *httptest.ResponseRecorder, 1)
	payload := `{"requestId":"90000000-0000-4000-8000-000000000052","sourceAttachmentId":"` + source +
		`","validUntil":"` + today + `","evidence":"补录必须重验归属"}`
	go func() {
		w := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, testRegulatorPath, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		responses <- w
	}()
	waitForRecipientLock(t, db, tx.Conn().PgConn().PID())
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	response := <-responses
	if response.Code != 409 || !strings.Contains(response.Body.String(), "material_expiry_scope_changed") {
		t.Fatalf("并发变更后的历史原件未被拒绝: %d %s", response.Code, response.Body)
	}
}

// waitForRecipientLock 等待 PostgreSQL 观测到补录正在等待变更事务持有的备案锁。
// 参数 t、db、pid 为测试、隔离库和变更连接标识。
// 返回值 无。
func waitForRecipientLock(t *testing.T, db *pgxpool.Pool, pid uint32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	timer := time.NewTicker(20 * time.Millisecond)
	defer timer.Stop()
	for {
		var waiting bool
		err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
			WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)))`, int32(pid)).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("补录请求未等待备案变更锁")
		case <-timer.C:
		}
	}
}

// recordRecipientChange 登记已经生效且带新旧收件方轨迹的备案变更。
// 参数 t、db、filing 代表测试、事务和主备案，changed 指定更换单位或机构。
// 返回值 无。
func recordRecipientChange(t *testing.T, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, filing, changed string) {
	t.Helper()
	var current string
	err := db.QueryRow(t.Context(), `WITH old AS (
		SELECT id,number,organization_name,assessment_org FROM business_cases WHERE id=$1
	), updated AS (
		UPDATE business_cases c SET
		  organization_name=CASE WHEN $2='unit' THEN '变更后单位' ELSE old.organization_name END,
		  assessment_org=CASE WHEN $2='institution' THEN '变更后机构' ELSE old.assessment_org END
		FROM old WHERE c.id=old.id RETURNING c.*,old.organization_name AS old_unit,old.assessment_org AS old_agency
	)
	INSERT INTO filing_applications(filing_id,application_type,filing_number,filing_version,reason,
	  before_values,after_values,status,applied_at)
	SELECT id,'change',number,version,'正式变更',jsonb_build_object('organizationName',old_unit,'assessmentOrg',old_agency),
	  jsonb_build_object('organizationName',organization_name,'assessmentOrg',assessment_org),
	  'approved',clock_timestamp() FROM updated RETURNING id::text`, filing, changed).Scan(&current)
	if err != nil {
		t.Fatal(err)
	}
}

// insertHistoricalMerge 模拟已归并备案保留原附件与独立归并历史的实际表结构。
// 参数 t、db、filing 为测试、数据库及被迁移备案。
// 返回值 无。
func insertHistoricalMerge(t *testing.T, db *pgxpool.Pool, filing string) {
	t.Helper()
	_, err := db.Exec(t.Context(), `WITH target AS (
		INSERT INTO directory_parties(kind,scope_name,name,code)
		VALUES('unit','并入后单位','并入后单位','123456789012345678') RETURNING id,scope_name
	), move AS (
		UPDATE business_cases f SET organization_name=target.scope_name,directory_unit_id=target.id
		FROM target WHERE f.id=$1 RETURNING f.id,f.directory_unit_id
	)
	INSERT INTO directory_unit_merges(target_id,preview_token,plan,actor)
	SELECT directory_unit_id,repeat('a',64),
	  jsonb_build_object('moves',jsonb_build_array(jsonb_build_object('id',id::text))),'监管' FROM move`, filing)
	if err != nil {
		t.Fatal(err)
	}
}
