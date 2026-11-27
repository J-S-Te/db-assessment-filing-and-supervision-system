package cases

import (
	"net/http"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/migrations"
)

// TestInstitutionPrefillDoesNotReserveFilingNumber 验证预填号仅在终审通过时争夺正式编号。
// 参数 t 为独立 PostgreSQL 和正常备案路由的测试上下文。
// 返回值 无。
func TestInstitutionPrefillDoesNotReserveFilingNumber(t *testing.T) {
	_, handler := fixtureService(t)
	a := preparePrefilledFiling(t, handler, "机构甲", "BA-PREFILLED")
	b := preparePrefilledFiling(t, handler, "机构乙", "ba-prefilled")

	codes := make(chan int, 2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, item := range []mutation{a, b} {
		workers.Go(func() {
			<-start
			response := reviewRequest(handler, filingsURL+"/"+item.ID, approve, "", item.Version)
			codes <- response.Code
		})
	}
	close(start)
	workers.Wait()
	close(codes)
	statuses := map[int]int{}
	for code := range codes {
		statuses[code]++
	}
	if statuses[http.StatusOK] != 1 || statuses[http.StatusConflict] != 1 {
		t.Fatalf("同号终审应恰有一条通过和一条冲突：%v", statuses)
	}
	assertPrefilledFilingRetry(t, handler, a, b)
}

// TestInstitutionFilingOptionalNumber 用途为验证新建、编辑、复制和终审的编号约束。
// 参数 t 为独立 PostgreSQL 和正常备案路由的测试上下文。
// 返回值 无。
func TestInstitutionFilingOptionalNumber(t *testing.T) {
	_, handler := fixtureService(t)
	scope := firstSubmissionScope(bulkInstitution)
	official := approvedFiling(t, handler, "BA-ALREADY-FORMAL")
	item := createBulkFixture(t, handler, scope, "ba-already-formal")
	createBulkFixture(t, handler, scope, "BA-INSTITUTION-ONLY")
	createFiling(t, handler, "BA-INSTITUTION-ONLY")
	path := bulkEndpoint + "/" + item.ID
	for _, number := range []string{"BA-EDITED-PREFILL", ""} {
		item = readResponse[mutation](t, requestJSON(handler, "PATCH", path+scope, map[string]any{
			"number": number, "systemName": "批量测试系统", "organizationName": "批量测试单位",
			"level": "2", "expectedVersion": item.Version,
		}), 200)
		detail := readResponse[recordDetail](t, requestJSON(handler, "GET", path+scope, nil), 200)
		if detail.Number != number || !detail.FilingNumberProvisional {
			t.Fatalf("编辑后的预填编号不符：%q / %v", detail.Number, detail.FilingNumberProvisional)
		}
	}
	copied := readResponse[mutation](t, requestJSON(handler, "POST", path+"/copy"+scope, map[string]any{
		"requestId": "e81aba51-61ce-4b39-aa2e-30bb66f42a6c", "expectedVersion": item.Version,
	}), 201)
	pending := preparePrefilledFiling(t, handler, bulkInstitution, "")
	assertError(t, reviewRequest(handler, filingsURL+"/"+pending.ID, approve, "", pending.Version),
		400, "filing_number_required")
	duplicate := reviewInput{versionInput: versionInput{pending.Version}, Decision: approve,
		FilingNumber: "BA-ALREADY-FORMAL"}
	assertError(t, requestJSON(handler, "POST", filingsURL+"/"+pending.ID+"/review-actions", duplicate),
		409, "duplicate_number")
	assertFilingNumberDetail(t, handler, official.ID, "BA-ALREADY-FORMAL", approved, false)
	assertFilingNumberDetail(t, handler, copied.ID, "", draft, true)
}

// TestFilingNumberReservationMigration 用途为核对升级保留存量编号、状态以及历史导入留痕。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestFilingNumberReservationMigration(t *testing.T) {
	db := fixtureDB(t)
	// 此专用模式没有业务数据。恢复 080 的编号约束后构造待升级快照。
	_, err := db.Exec(t.Context(), `
		DROP INDEX business_cases_kind_number;
		ALTER TABLE business_cases DROP COLUMN filing_number_provisional;
		CREATE UNIQUE INDEX business_cases_kind_number ON business_cases(kind,lower(number))
			WHERE discarded_at IS NULL AND status<>'withdrawn' AND number<>'';
		DELETE FROM schema_migrations WHERE name='082_filing_number_reservation.sql';
		INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status,version)
		SELECT 'filing', 'OLD-' || status, '存量系统', '存量单位', '存量机构', '2', status, 4
		FROM unnest(ARRAY['draft','pending_initial','pending_final','returned','approved']) AS status;
		INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level)
		VALUES ('filing','OLD-IMPORTED','历史系统','存量单位','存量机构','2');
		INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,operator_origin)
		VALUES ('filing','','运营者系统','存量单位','存量机构','2',true);
		INSERT INTO filing_history_import_batches(digest,operator,result)
		SELECT repeat('a',64),'测试监管席位',
			jsonb_build_object('rows', jsonb_build_array(jsonb_build_object('status','imported','filingId',id)))
		FROM business_cases WHERE number='OLD-IMPORTED';`)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{draft, pendingInitial, pendingFinal, returned, approved} {
		assertMigratedFilingNumber(t, db, "OLD-"+status, status, 4, status != approved)
	}
	assertMigratedFilingNumber(t, db, "OLD-IMPORTED", draft, 1, false)
	assertMigratedFilingNumber(t, db, "", draft, 1, false)
}

// assertFilingNumberDetail 用途为核对详情返回的办结状态与预填标记。
// 参数 t、handler 为测试及路由，id、number、status、provisional 为记录及预期值。
// 返回值 无。
func assertFilingNumberDetail(t *testing.T, handler http.Handler, id, number, status string, provisional bool) {
	t.Helper()
	detail := readResponse[recordDetail](t, requestJSON(handler, "GET", filingsURL+"/"+id, nil), 200)
	if detail.Number != number || detail.Status != status || detail.FilingNumberProvisional != provisional {
		t.Fatalf("详情中的编号状态不符：%q / %s / %v", detail.Number, detail.Status, detail.FilingNumberProvisional)
	}
}

// assertMigratedFilingNumber 用途为检查迁移保留已有记录，并区分正式号和预填号。
// 参数 t、db 为测试及隔离数据库，number、status、version、provisional 为旧号和预期状态。
// 返回值 无。
func assertMigratedFilingNumber(t *testing.T, db *pgxpool.Pool, number, status string, version int64, provisional bool) {
	t.Helper()
	var gotStatus string
	var gotVersion int64
	var gotProvisional bool
	err := db.QueryRow(t.Context(), `SELECT status,version,filing_number_provisional
		FROM business_cases WHERE number=$1`, number).Scan(&gotStatus, &gotVersion, &gotProvisional)
	if err != nil || gotStatus != status || gotVersion != version || gotProvisional != provisional {
		t.Fatalf("升级后的 %s 状态不符：%s/%d/%v，错误 %v", number, gotStatus, gotVersion, gotProvisional, err)
	}
}

// preparePrefilledFiling 通过机构新建、上传、送审和管理端初审生成待终审记录。
// 参数 t、handler 为测试和路由，institution、number 为机构范围及预填编号。
// 返回值 待终审记录的标识和版本。
func preparePrefilledFiling(t *testing.T, handler http.Handler, institution, number string) mutation {
	t.Helper()
	scope := firstSubmissionScope(institution)
	item := createBulkFixture(t, handler, scope, number)
	path := bulkEndpoint + "/" + item.ID
	for _, material := range []string{"filing_form", "classification_report"} {
		postMaterialAt(t, handler, path+"/attachments"+scope, material)
	}
	detail := readResponse[recordDetail](t, requestJSON(handler, "GET", path+scope, nil), 200)
	sent := readResponse[mutation](t, requestJSON(handler, "POST", path+"/submit"+scope,
		versionInput{detail.Version}), 200)
	return readResponse[mutation](t, reviewRequest(handler, filingsURL+"/"+item.ID, approve, "", sent.Version), 200)
}

// assertPrefilledFilingRetry 核对编号冲突未推进版本，并可另填正式编号重新终审。
// 参数 t、handler 为测试和路由，a、b 为参与并发审核的记录。
// 返回值 无。
func assertPrefilledFilingRetry(t *testing.T, handler http.Handler, a, b mutation) {
	t.Helper()
	for _, item := range []mutation{a, b} {
		path := filingsURL + "/" + item.ID
		detail := readResponse[recordDetail](t, requestJSON(handler, "GET", path, nil), 200)
		if detail.Status == approved && !detail.FilingNumberProvisional {
			continue
		}
		if detail.Status != pendingFinal || detail.Version != item.Version || !detail.FilingNumberProvisional {
			t.Fatalf("冲突后状态或版本变动：%s/%d", detail.Status, detail.Version)
		}
		result := readResponse[mutation](t, requestJSON(handler, "POST", path+"/review-actions", reviewInput{
			versionInput: versionInput{item.Version}, Decision: approve, FilingNumber: "BA-PREFILLED-OTHER",
		}), 200)
		if result.Status != approved {
			t.Fatal("改用正式编号后未通过终审")
		}
		assertFilingNumberDetail(t, handler, item.ID, "BA-PREFILLED-OTHER", approved, false)
	}
}
