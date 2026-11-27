package materialexpiry

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestExpiryRenewalClosesCurrentWarning 验证过期不因待审而关闭，通过后只保留原提醒的历史。
// 参数 t 为隔离真库测试。
// 返回值 无。
func TestExpiryRenewalClosesCurrentWarning(t *testing.T) {
	db, files := expiryFixture(t)
	_, source, _ := seedCaseMaterial(t, db, files)
	var elapsed, future string
	err := db.QueryRow(t.Context(), "SELECT ("+todaySQL+"-1)::text,("+todaySQL+"+60)::text").Scan(&elapsed, &future)
	if err != nil {
		t.Fatal(err)
	}
	router := http.NewServeMux()
	service := New(db, files, "有效期测试监管")
	service.Register(router)
	record := expiryCall(t, router, "POST", testRegulatorPath, map[string]any{
		"requestId": "90000000-0000-4000-8000-000000000020", "sourceAttachmentId": source,
		"validUntil": elapsed, "evidence": "源文件注明旧日期",
	}, 201)
	if record["dateStatus"] != "expired" {
		t.Fatalf("旧材料应过期：%v", record)
	}
	id := record["id"].(string)
	operator := "/api/v1/operator/" + resource + "/" + id
	partner := "/api/v1/institution/" + resource + "/" + id
	own := "?" + url.Values{"organization": {testOrganization}}.Encode()
	agency := "?" + url.Values{"institution": {testInstitution}}.Encode()
	other := "?" + url.Values{"organization": {"另一无关单位"}}.Encode()
	admin := testRegulatorPath + "/" + id
	expiryCall(t, router, "GET", operator+other, nil, 404)
	expiryCall(t, router, "GET", operator+own+"&injected=1", nil, 400)
	expiryCall(t, router, "GET", partner+agency, nil, 200)
	assertNoticeCount(t, db, id, "material_expired", 2)
	if count, err := service.Sweep(t.Context()); err != nil || count != 0 {
		t.Fatalf("重复扫描登记了提醒：%d %v", count, err)
	}

	path := operator + "/renewals"
	resp := uploadRenewalTest(t, router, path+own, "续期材料.pdf", "无效字节", future, 1, 415)
	if !strings.Contains(resp.Body.String(), "unsupported_attachment") {
		t.Fatal(resp.Body.String())
	}
	uploadRenewalTest(t, router, path+own, "续期材料.pdf", testPDF, elapsed, 1, 422)
	uploadRenewalTest(t, router, path+other, "续期材料.pdf", testPDF, future, 1, 404)
	uploadRenewalTest(t, router, path+own, "续期材料.pdf", testPDF, future, 1, 201)
	uploadRenewalTest(t, router, path+own, "续期材料.pdf", testPDF, future, 1, 201)
	uploadRenewalTest(t, router, path+own, "不同文件.pdf", testPDF, future, 1, 409)
	item := expiryCall(t, router, "GET", admin, nil, 200)
	if item["dateStatus"] != "expired" || item["renewalPending"] != true || item["version"] != float64(2) {
		t.Fatalf("待审续传错误地关闭旧预警：%v", item)
	}
	renewalID := item["renewals"].([]any)[0].(map[string]any)["id"].(string)
	filePath := "/renewals/" + renewalID + "/download"
	assertExpiryDownload(t, router, operator+filePath+own, 200)
	assertExpiryDownload(t, router, partner+filePath+agency, 404)
	assertExpiryDownload(t, router, operator+filePath+other, 404)
	verifyExpiryApproval(t, router, db, admin, partner+filePath+agency, id, future)
}

// verifyExpiryApproval 核对批准后同一份文件可追溯并为原收件方刷新当前日期。
// 参数 t、router、db 为隔离环境，admin、agencyDownload、id、future 为当前办理位置和期望日期。
// 返回值 无。
func verifyExpiryApproval(t *testing.T, router http.Handler, db *pgxpool.Pool, admin, agencyDownload, id, future string) {
	t.Helper()
	body := map[string]any{"action": "approve", "expectedVersion": 2, "comment": "已核验新期证明"}
	active := expiryCall(t, router, "POST", admin+"/actions", body, 200)
	again := expiryCall(t, router, "POST", admin+"/actions", body, 200)
	if active["validUntil"] != future || active["dateStatus"] != "valid" || active["renewalPending"] != false ||
		active["version"] != again["version"] {
		t.Fatalf("续传通过未切换周期：%v %v", active, again)
	}
	body["comment"] = "不一致的审核决定"
	expiryCall(t, router, "POST", admin+"/actions", body, 409)
	assertExpiryDownload(t, router, agencyDownload, 200)
	assertNoticeCount(t, db, id, "material_expired", 2)
	assertNoticeCount(t, db, id, "material_renewal_approved", 2)
	closed := expiryCall(t, router, "GET", testRegulatorPath+"?status=expired", nil, 200)
	if len(closed["items"].([]any)) != 0 {
		t.Fatalf("已续期还出现在过期台账：%v", closed)
	}
}

// uploadRenewalTest 将测试文件和明确依据提交到受控真实表单。
// 参数 t、router、path、name、document、date、version、status 为测试所需请求及期望状态。
// 返回值 可供调用方断言错误码的响应。
func uploadRenewalTest(t *testing.T, router http.Handler, path, name, document, date string,
	version int64, status int,
) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range map[string]string{"validUntil": date, "evidence": "续期文书的明确注明日期",
		"expectedVersion": strconv.FormatInt(version, 10)} {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(part, document); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf("%s: HTTP %d，期望 %d：%s", path, response.Code, status, response.Body)
	}
	return response
}

// assertExpiryDownload 校验取回的是已经接收的字节，或当前归属无权下载。
// 参数 t、router、path、status 指定测试和期望状态。
// 返回值 无。
func assertExpiryDownload(t *testing.T, router http.Handler, path string, status int) {
	t.Helper()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
	if response.Code != status || (status == 200 && response.Body.String() != testPDF) {
		t.Fatalf("续传下载响应 %d，期望 %d：%s", response.Code, status, response.Body)
	}
}

// assertNoticeCount 核对同一周期的接收范围实际条数。
// 参数 t、db、id、state、want 为测试、数据库、业务标识、消息类型和预期条数。
// 返回值 无。
func assertNoticeCount(t *testing.T, db *pgxpool.Pool, id, state string, want int) {
	t.Helper()
	var count, scoped int
	err := db.QueryRow(t.Context(), `SELECT COUNT(*),count(*) FILTER (WHERE recipient IN ($3,$4))
	  FROM review_notices WHERE resource='material-expiries' AND resource_id=$1 AND state=$2`,
		id, state, testOrganization, testInstitution).Scan(&count, &scoped)
	if err != nil || count != want || scoped != want {
		t.Fatalf("%s: 消息 %d，其中正确范围 %d，期望 %d：%v", state, count, scoped, want, err)
	}
}
