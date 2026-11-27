package reportextract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/cases"
	"grade-business/migrations"
)

// TestPDFToolOutputLimit 验证使用进程标准输出复制时也会执行结果上限。
// 参数 t 为测试上下文。
// 返回值 无。
func TestPDFToolOutputLimit(t *testing.T) {
	output := &pdfOutput{remaining: 4}
	source := struct{ io.Reader }{strings.NewReader("超过限制")}
	if n, err := io.Copy(output, source); !errors.Is(err, errDocument) || n != 0 {
		t.Fatalf("copied %d bytes, got %v, want output limit error", n, err)
	}
}

// TestFilingPDFEvidence 验证 PDF 文字层和用于视觉预审的实际页面图片。
// 参数 t 为本地 PDF 工具和合成 PDF 的检查上下文。
// 返回值 无。
func TestFilingPDFEvidence(t *testing.T) {
	requireTestPDFTools(t, "pdfinfo", "pdftotext", "pdftoppm")
	raw, err := os.ReadFile(filepath.Join("testdata", "text.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	reader := bytes.NewReader(raw)
	pages, err := FilingPDFText(t.Context(), reader)
	if err != nil || len(pages) != 1 || !strings.Contains(pages[0], "中风险") {
		t.Fatalf("未读取真实 PDF 文字层: %v", err)
	}
	images := FilingPDFImages(t.Context(), reader)
	if len(images) != 1 || images[0].MIME != "image/jpeg" || images[0].Location != "第 1 页" {
		t.Fatal("未渲染可定位的真实 PDF JPEG")
	}
}

type pdfEvidence struct {
	Key       string `json:"key"`
	Original  string `json:"original"`
	Value     string `json:"value"`
	Start     int    `json:"startParagraph"`
	End       int    `json:"endParagraph"`
	StartPage int    `json:"startPage"`
	EndPage   int    `json:"endPage"`
	Source    string `json:"source"`
}

// TestPDFExtractionWithPostgres 验证真实 PDF 上传、本地 OCR、归属、证据校对及版本持久化。
// 参数 t 为提供隔离测试连接与临时附件目录的测试上下文。
// 返回值 无。
func TestPDFExtractionWithPostgres(t *testing.T) {
	pool, handler := pdfTestService(t)
	path, other := pdfTestReportPaths(t, pool)
	text := uploadPDF(t, handler, path, "report_file", "text.pdf")
	pdfRequest(t, handler, http.MethodPost, other+text, nil, 404)
	pdfRequest(t, handler, http.MethodPost, path+text+"?extra=1", nil, 400)
	malformed := strings.Replace(text, "-", "x", 1)
	pdfRequest(t, handler, http.MethodPost, path+malformed, nil, 404)
	nonBody := uploadPDF(t, handler, path, "supporting", "text.pdf")
	pdfRequest(t, handler, http.MethodPost, path+nonBody, nil, 415)
	t.Run("missing PDF tools", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		failure := pdfRequest(t, handler, http.MethodPost, path+text, nil, 503)
		if !bytes.Contains(failure, []byte(`"code":"pdf_extraction_unavailable"`)) {
			t.Fatalf("expected missing PDF tools, got %s", failure)
		}
		pdfRequest(t, handler, http.MethodGet, path+text, nil, 404)
	})

	requireTestPDFTools(t, "pdfinfo", "pdftotext")
	initial := pdfRequest(t, handler, http.MethodPost, path+text, nil, 201)
	assertEvidence(t, initial, pdfEvidence{
		Key: "risk", Original: "中风险", Start: 4, End: 4,
		StartPage: 1, EndPage: 1, Source: "pdf_text",
	}, 1)
	if !bytes.Equal(initial, pdfRequest(t, handler, http.MethodPost, path+text, nil, 200)) {
		t.Fatal("repeated extraction changed the saved result")
	}
	t.Run("scan and review", func(t *testing.T) {
		testPDFScanReview(t, handler, path)
	})
	reportID := path[strings.LastIndex(path, "/")+1:]
	if _, err := pool.Exec(t.Context(), `UPDATE business_cases SET discarded_at=now() WHERE id=$1`, reportID); err != nil {
		t.Fatal(err)
	}
	pdfRequest(t, handler, http.MethodGet, path+text, nil, 404)
	pdfRequest(t, handler, http.MethodPost, path+text, nil, 404)
	pdfRequest(t, handler, http.MethodPatch, path+text, strings.NewReader(`{"expectedVersion":1,"changes":[]}`), 404)
}

// testPDFScanReview 验证混合正文的扫描来源、缺少 OCR 能力和人工校对。
// 参数 t 为测试上下文，handler 为业务路由，path 为报告地址。
// 返回值 无。
func testPDFScanReview(t *testing.T, handler http.Handler, path string) {
	t.Helper()
	scanned := uploadPDF(t, handler, path, "report_file", "mixed.pdf")
	t.Run("missing OCR languages", func(t *testing.T) {
		t.Setenv("TESSDATA_PREFIX", t.TempDir())
		failure := pdfRequest(t, handler, http.MethodPost, path+scanned, nil, 503)
		if !bytes.Contains(failure, []byte(`"code":"ocr_unavailable"`)) {
			t.Fatalf("expected missing OCR languages, got %s", failure)
		}
		pdfRequest(t, handler, http.MethodGet, path+scanned, nil, 404)
	})
	requireTestPDFTools(t, "pdftoppm", "tesseract")
	if err := requireOCRLanguages(t.Context()); err != nil {
		t.Skipf("需要本地中英文 OCR 语言包: %v", err)
	}
	draft := pdfRequest(t, handler, http.MethodPost, path+scanned, nil, 201)
	assertEvidence(t, draft, pdfEvidence{
		Key: "findings", Original: "日志留存不足\n应检查日志留存期限",
		Start: 3, End: 4, StartPage: 1, EndPage: 2, Source: "pdf_mixed",
	}, 1)
	assertEvidence(t, draft, pdfEvidence{
		Key: "risk", Original: "中风险", Start: 5, End: 5,
		StartPage: 2, EndPage: 2, Source: "pdf_ocr",
	}, 1)
	correction := strings.NewReader(`{"expectedVersion":1,"changes":[{"key":"findings","value":"人工校对"}]}`)
	reviewed := pdfRequest(t, handler, http.MethodPatch, path+scanned, correction, 200)
	assertEvidence(t, reviewed, pdfEvidence{
		Key: "findings", Original: "日志留存不足\n应检查日志留存期限", Value: "人工校对",
		Start: 3, End: 4, StartPage: 1, EndPage: 2, Source: "pdf_mixed",
	}, 2)
	if !bytes.Equal(reviewed, pdfRequest(t, handler, http.MethodGet, path+scanned, nil, 200)) {
		t.Fatal("saved correction did not preserve PDF evidence")
	}
}

// requireTestPDFTools 判断当前隔离测试环境能否执行真实格式提取用例。
// 参数 t 为测试上下文，names 为该用例依赖的本地工具。
// 返回值 无，缺少工具时跳过依赖该工具的用例。
func requireTestPDFTools(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("需要本地 PDF 提取工具 %s", name)
		}
	}
}

// assertEvidence 核对返回的有效版本及原文定位。
// 参数 t 为测试上下文，body 为 HTTP 响应，want 和 version 为独立预期。
// 返回值 无。
func assertEvidence(t *testing.T, body []byte, want pdfEvidence, version int64) {
	t.Helper()
	var response struct {
		Fields  []pdfEvidence `json:"fields"`
		Version int64         `json:"version"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	for _, item := range response.Fields {
		if item == want && response.Version == version {
			return
		}
	}
	t.Fatalf("evidence %+v and version %d missing from %s", want, version, body)
}

// pdfTestService 为 PDF API 测试启动隔离模式与真实业务路由。
// 参数 t 为测试上下文，使用已显式配置的本地测试库。
// 返回值 测试模式的连接池和 HTTP 路由。
func pdfTestService(t *testing.T) (*pgxpool.Pool, http.Handler) {
	t.Helper()
	dsn := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if dsn == "" {
		t.Skip("需要显式设置隔离测试库 GRADE_BUSINESS_TEST_DSN")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Database != "grade_business_test" ||
		cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 15444 {
		t.Fatal("限定本地隔离库 grade_business_test 127.0.0.1:15444")
	}
	admin, err := pgxpool.NewWithConfig(t.Context(), cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("pdf_extract_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrations.Apply(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	records, err := cases.New(pool, t.TempDir(), "报告提取测试席位")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	records.Register(mux)
	New(pool, records, "报告提取测试席位").Register(mux)
	return pool, mux
}

// pdfTestReportPaths 准备同一备案下的两份测试报告以核对资料归属。
// 参数 t 为测试上下文，pool 为隔离测试库连接池。
// 返回值 主报告和另一报告的 HTTP 路径。
func pdfTestReportPaths(t *testing.T, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	var first, other string
	err := pool.QueryRow(t.Context(), `WITH filing AS (
		INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status)
		VALUES('filing','BA-PDF','测试系统','测试单位','测试机构','3','approved') RETURNING id
	), reports AS (
		INSERT INTO business_cases(kind,number,filing_id,assessment_org,risk,status)
		SELECT 'report',n,filing.id,'测试机构','low','draft'
		FROM filing CROSS JOIN (VALUES('BG-PDF'),('BG-OTHER')) AS nums(n) RETURNING id,number
	) SELECT (SELECT id::text FROM reports WHERE number='BG-PDF'),
		(SELECT id::text FROM reports WHERE number='BG-OTHER')`).Scan(&first, &other)
	if err != nil {
		t.Fatal(err)
	}
	const reports = "/api/v1/reports/"
	return reports + first, reports + other
}

// uploadPDF 通过实际报告上传接口登记指定的 PDF 测试样本。
// 参数 t 为测试上下文，handler 为实际业务路由，path 为报告地址，kind 为材料类别，name 为样本名。
// 返回值 附件的提取接口路径后缀。
func uploadPDF(t *testing.T, handler http.Handler, path, kind, name string) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	multi := multipart.NewWriter(&body)
	if err := multi.WriteField("kind", kind); err != nil {
		t.Fatal(err)
	}
	part, err := multi.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(source); err != nil {
		t.Fatal(err)
	}
	if err := multi.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path+"/attachments", &body)
	req.Header.Set("Content-Type", multi.FormDataContentType())
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != 201 {
		t.Fatalf("upload status %d, body %s", resp.Code, resp.Body)
	}
	var attachment cases.Attachment
	if err := json.Unmarshal(resp.Body.Bytes(), &attachment); err != nil {
		t.Fatal(err)
	}
	return "/attachments/" + attachment.ID + "/extraction"
}

// pdfRequest 调用实际提取路由并断言业务响应状态。
// 参数 t 提供测试上下文，handler、method、path、body 构成请求，want 为预期 HTTP 状态。
// 返回值 响应体。
func pdfRequest(t *testing.T, handler http.Handler, method, path string, body io.Reader, want int) []byte {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != want {
		t.Fatalf("%s %s: status %d, want %d, body %s", method, path, resp.Code, want, resp.Body)
	}
	return resp.Body.Bytes()
}
