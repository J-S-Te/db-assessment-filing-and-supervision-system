package cases

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const historyImportTestBase = "/api/v1/filing-history-import"

// TestFilingHistoryImportTemplate 核对监管导入入口提供实际可使用的清单表头。
// 参数 t 为当前测试上下文。
// 返回值 无。
func TestFilingHistoryImportTemplate(t *testing.T) {
	router := http.NewServeMux()
	(&Service{}).Register(router)
	response := requestJSON(router, "GET", historyImportTestBase+"/template", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(),
		"number,systemName,organizationName,assessmentOrg,level,filingForm,classificationReport") {
		t.Fatalf("未取得历史备案清单模板: %d %s", response.Code, response.Body)
	}
}

type historyImportTestResponse struct {
	BatchID, Operator, ImportedAt string
	Rows                          []struct{ Status, FilingID string }
	Counts                        map[string]int
}

// TestFilingHistoryImportWorkflow 核对预检、草稿材料、编号冲突、重复提交及批次留痕。
// 参数 t 为使用独立 PostgreSQL schema 和文件目录的测试上下文。
// 返回值 无。
func TestFilingHistoryImportWorkflow(t *testing.T) {
	service, router := fixtureService(t)
	createHistoryImportExisting(t, router)
	createBulkFixture(t, router, firstSubmissionScope("联验机构"), "BA-IMP-001")
	payload := historyImportTestZip(t, map[string]string{
		"manifest.csv": "number,systemName,organizationName,assessmentOrg,level,filingForm,classificationReport\n" +
			"BA-IMP-001,联验系统,联验单位,联验机构,2,a.pdf,b.pdf\n" +
			"BA-IMP-001,联验系统,联验单位,联验机构,2,a.pdf,b.pdf\n" +
			"BA-IMP-002,不同系统,联验单位,联验机构,2,a.pdf,b.pdf\n" +
			"BA-IMP-003,漏件系统,联验单位,联验机构,2,a.pdf,missing.pdf\n" +
			"BA-IMP-004,损坏系统,联验单位,联验机构,2,bad.pdf,b.pdf\n",
		"a.pdf": filingPackagePDF, "b.pdf": filingPackagePDF, "bad.pdf": "%PDF-1.4\n不完整",
	})
	preview := readResponse[historyImportTestResponse](t,
		historyImportUpload(t, router, "/preview", payload), 200)
	if len(preview.Rows) != 5 || preview.Counts["ready"] != 1 || preview.Counts["duplicate"] != 1 ||
		preview.Counts["conflict"] != 1 || preview.Counts["invalid"] != 2 {
		t.Fatalf("预检分类不符: %+v", preview)
	}
	files, err := os.ReadDir(service.attachmentDir)
	if err != nil || len(files) != 0 {
		t.Fatalf("预检不应写入材料: %v %v", files, err)
	}
	assertHistoryImportCommit(t, router, payload)
}

// assertHistoryImportCommit 核对确认导入、历史收据和普通备案详情使用同一数据。
// 参数 t 为断言上下文，router 为实际 API，payload 是同批 ZIP 包。
// 返回值 无。
func assertHistoryImportCommit(t *testing.T, router http.Handler, payload []byte) {
	t.Helper()
	imported := readResponse[historyImportTestResponse](t,
		historyImportUpload(t, router, "/commit", payload), 200)
	if imported.BatchID == "" || imported.Counts["imported"] != 1 || imported.Rows[0].FilingID == "" ||
		imported.Operator != "测试监管席位" || imported.ImportedAt == "" {
		t.Fatalf("历史导入结果不符: %+v", imported)
	}
	if imported.Rows[1].FilingID != imported.Rows[0].FilingID {
		t.Fatalf("同批跳过行的回执应指向实际备案: %+v", imported.Rows)
	}
	filingPath := "/api/v1/filings/" + imported.Rows[0].FilingID
	detail := requestJSON(router, "GET", filingPath, nil)
	if !strings.Contains(detail.Body.String(), `"status":"draft"`) {
		t.Fatalf("导入应保存为草稿: %s", detail.Body)
	}
	assertHistoryImportMaterials(t, router, filingPath)
	replayed := historyImportUpload(t, router, "/commit", payload)
	prior := requestJSON(router, "GET", historyImportTestBase+"/batches/"+imported.BatchID, nil)
	var replayData, priorData historyImportTestResponse
	if replayed.Code != 200 || prior.Code != 200 ||
		json.Unmarshal(replayed.Body.Bytes(), &replayData) != nil ||
		json.Unmarshal(prior.Body.Bytes(), &priorData) != nil ||
		replayData.BatchID != imported.BatchID || priorData.BatchID != imported.BatchID {
		t.Fatalf("重放或批次追溯失败: %d %s / %d %s", replayed.Code, replayed.Body, prior.Code, prior.Body)
	}
}

// assertHistoryImportMaterials 核对导入的两类材料可通过原业务路由原样下载。
// 参数 t、router 为上下文与实际路由，filingPath 为已导入备案的地址。
// 返回值 无。
func assertHistoryImportMaterials(t *testing.T, router http.Handler, filingPath string) {
	t.Helper()
	path := filingPath + "/attachments"
	response := readResponse[itemResponse[Attachment]](t, requestJSON(router, "GET", path, nil), 200)
	if len(response.Items) != 2 || response.Items[0].Kind == response.Items[1].Kind {
		t.Fatalf("材料未登记在普通备案: %+v", response)
	}
	for _, item := range response.Items {
		file := requestJSON(router, "GET", path+"/"+item.ID, nil)
		if file.Code != 200 || file.Body.String() != filingPackagePDF ||
			(item.Kind != "filing_form" && item.Kind != "classification_report") {
			t.Fatalf("材料类别或实际下载不同: %+v, HTTP %d", item, file.Code)
		}
	}
}

// TestFilingHistoryImportConcurrentReplay 核对同一 ZIP 并发导入只落库一次，且重复包返回原回执。
// 参数 t 为隔离 PostgreSQL 和材料目录的测试上下文。
// 返回值 无。
func TestFilingHistoryImportConcurrentReplay(t *testing.T) {
	service, router := fixtureService(t)
	payload := mergeHistoryImportZip(t, "并发联验单位", "BA-HISTORY-CONCURRENT")
	var requests sync.WaitGroup
	var responses [2]*httptest.ResponseRecorder
	for i := range responses {
		requests.Go(func() { responses[i] = historyImportUpload(t, router, "/commit", payload) })
	}
	requests.Wait()
	first := readResponse[historyImportTestResponse](t, responses[0], 200)
	second := readResponse[historyImportTestResponse](t, responses[1], 200)
	if first.BatchID == "" || first.BatchID != second.BatchID || first.Counts["imported"] != 1 {
		t.Fatalf("并发提交返回不同批次: %+v %+v", first, second)
	}
	files, err := os.ReadDir(service.attachmentDir)
	if err != nil || len(files) != 2 {
		t.Fatalf("重复提交写入了重复文件或落档不完整: %v %v", files, err)
	}
}

// TestFilingHistoryImportRollback 验证第二项材料落库失败时不会遗留半条备案、文件或成功回执。
// 参数 t 为隔离 PostgreSQL 及材料路径的测试上下文。
// 返回值 无。
func TestFilingHistoryImportRollback(t *testing.T) {
	service, router := fixtureService(t)
	const number = "BA-HISTORY-ROLLBACK"
	payload := mergeHistoryImportZip(t, "回滚联验单位", number)
	_, err := service.db.Exec(t.Context(), `ALTER TABLE case_attachments
		ADD CONSTRAINT test_history_material_failure CHECK (kind<>'classification_report')`)
	if err != nil {
		t.Fatal(err)
	}
	assertError(t, historyImportUpload(t, router, "/commit", payload), 500, "internal_error")
	var filingExists, batchExists bool
	err = service.db.QueryRow(t.Context(), `SELECT
		EXISTS(SELECT 1 FROM business_cases WHERE number=$1),
		EXISTS(SELECT 1 FROM filing_history_import_batches)`, number).Scan(&filingExists, &batchExists)
	if err != nil || filingExists || batchExists {
		t.Fatalf("失败时残留备案或批次: %v %v %v", filingExists, batchExists, err)
	}
	files, err := os.ReadDir(service.attachmentDir)
	if err != nil || len(files) != 0 {
		t.Fatalf("导入回滚后残留暂存材料: %v %v", files, err)
	}
	if _, err := service.db.Exec(t.Context(),
		`ALTER TABLE case_attachments DROP CONSTRAINT test_history_material_failure`); err != nil {
		t.Fatal(err)
	}
	result := readResponse[historyImportTestResponse](t, historyImportUpload(t, router, "/commit", payload), 200)
	if result.Counts["imported"] != 1 {
		t.Fatalf("导入未能安全重试: %+v", result)
	}
}

// TestFilingHistoryImportLockOrder 验证导入等待名录时已经取得与归并互斥的备案表锁。
// 参数 t 为真实 PostgreSQL 隔离测试上下文。
// 返回值 无。
func TestFilingHistoryImportLockOrder(t *testing.T) {
	service, router := fixtureService(t)
	const unit = "锁序联验单位"
	_, err := service.db.Exec(t.Context(), `INSERT INTO directory_parties(kind,scope_name,name,code)
		VALUES('unit',$1,$1,'913300001000003001')`, unit)
	if err != nil {
		t.Fatal(err)
	}
	payload := mergeHistoryImportZip(t, unit, "BA-HISTORY-LOCK-ORDER")
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	tx, err := service.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	var blocker int32
	err = tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM directory_parties
		WHERE scope_name=$1 FOR UPDATE`, unit).Scan(&blocker)
	if err != nil {
		t.Fatal(err)
	}
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() { response <- historyImportUpload(t, router, "/commit", payload) }()
	awaitHistoryLock(t, ctx, service, blocker)
	_, lockErr := tx.Exec(ctx, "LOCK TABLE business_cases IN EXCLUSIVE MODE NOWAIT")
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	assertHistoryLockResult(t, ctx, response, lockErr)
}

// awaitHistoryLock 等待另一条实际 PostgreSQL 会话进入预设的名录行锁。
// 参数 t、ctx、service 控制隔离查询，blocker 为名录锁持有者的后端序号。
// 返回值 无，超时或数据库错误时终止测试。
func awaitHistoryLock(t *testing.T, ctx context.Context, service *Service, blocker int32) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err := service.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE $1=ANY(pg_blocking_pids(pid)))`, blocker).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("导入未进入预设的名录锁等待")
		}
	}
}

// assertHistoryLockResult 核对归并可立即返回忙碌，且释放名录锁后导入能够完成。
// 参数 t、ctx 为测试上下文，response 为导入回包，lockErr 为归并模拟取得表锁的结果。
// 返回值 无。
func assertHistoryLockResult(t *testing.T, ctx context.Context,
	response <-chan *httptest.ResponseRecorder, lockErr error,
) {
	t.Helper()
	select {
	case next := <-response:
		result := readResponse[historyImportTestResponse](t, next, 200)
		if result.Counts["imported"] != 1 {
			t.Fatalf("名录锁释放后没有完成导入: %+v", result)
		}
	case <-ctx.Done():
		t.Fatal("名录锁释放后导入超时")
	}
	var dbErr *pgconn.PgError
	if !errors.As(lockErr, &dbErr) || dbErr.Code != "55P03" {
		t.Fatalf("导入持有名录范围前未建立备案锁顺序: %v", lockErr)
	}
}

// createHistoryImportExisting 保存同号但所属系统不同的先存备案。
// 参数 t 为测试上下文，router 为真实服务。
// 返回值 无。
func createHistoryImportExisting(t *testing.T, router http.Handler) {
	t.Helper()
	prior := requestJSON(router, "POST", "/api/v1/filings", map[string]string{
		"number": "BA-IMP-002", "organizationName": "联验单位", "systemName": "先存系统",
		"assessmentOrg": "联验机构", "level": "2",
	})
	if prior.Code != 201 {
		t.Fatalf("无法准备已有备案: %d %s", prior.Code, prior.Body)
	}
}

// historyImportTestZip 将测试记录及材料写入内存中的真实 ZIP。
// 参数 t 为测试上下文，files 为包内路径及内容。
// 返回值 已压缩的文件字节。
func historyImportTestZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var out bytes.Buffer
	archive := zip.NewWriter(&out)
	for name, contents := range files {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// historyImportUpload 构造监管批量导入的 multipart 调用。
// 参数 t 为测试上下文，router 为服务路由，suffix 为操作路径，payload 为 ZIP。
// 返回值 HTTP 回包。
func historyImportUpload(t *testing.T, router http.Handler, suffix string, payload []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", "备案历史.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", historyImportTestBase+suffix, &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res
}
