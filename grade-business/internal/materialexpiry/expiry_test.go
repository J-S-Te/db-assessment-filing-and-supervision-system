package materialexpiry

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/internal/cases"
	"grade-business/migrations"
)

const testPDF = "%PDF-1.4\nLocal material expiry checked document.\n%%EOF\n"
const testRegulatorPath = "/api/v1/material-expiries"
const testOrganization = "有效期原运营单位"
const testInstitution = "有效期原测评机构"

// TestRegisterEvidenceRequiresActualAttachment 验证监管可按真实材料和明确证据登记。
// 参数 t 为隔离的测试上下文。
// 返回值 无。
func TestRegisterEvidenceRequiresActualAttachment(t *testing.T) {
	db, files := expiryFixture(t)
	_, source, today := seedCaseMaterial(t, db, files)
	router := http.NewServeMux()
	New(db, files, "有效期测试监管").Register(router)
	payload := map[string]any{
		"requestId":          "90000000-0000-4000-8000-000000000010",
		"sourceAttachmentId": source, "validUntil": today,
		"evidence": "监管依据材料原件实际记录的截止日期",
	}
	first := expiryCall(t, router, http.MethodPost, testRegulatorPath, payload, http.StatusCreated)
	again := expiryCall(t, router, http.MethodPost, testRegulatorPath, payload, http.StatusCreated)
	if first["id"] != again["id"] || first["dateStatus"] != "due" {
		t.Fatalf("登记及重试口径错误：%v %v", first, again)
	}
	payload["evidence"] = "以相同发起标识替换依据"
	expiryCall(t, router, http.MethodPost, testRegulatorPath, payload, http.StatusConflict)
}

// TestSweepDoesNotSendAfterSourceCancellation 验证日期扫提醒受实际归属和生效状态保护。
// 参数 t 为隔离真库的测试上下文。
// 返回值 无。
func TestSweepDoesNotSendAfterSourceCancellation(t *testing.T) {
	db, files := expiryFixture(t)
	filing, source, today := seedCaseMaterial(t, db, files)
	service := New(db, files, "有效期测试监管")
	router := http.NewServeMux()
	service.Register(router)
	result := expiryCall(t, router, "POST", testRegulatorPath, map[string]any{
		"requestId":          "90000000-0000-4000-8000-000000000099",
		"sourceAttachmentId": source, "validUntil": today, "evidence": "核定日期供专用测试",
	}, 201)
	id := result["id"].(string)
	if _, err := db.Exec(t.Context(), "DELETE FROM review_notices WHERE resource_id=$1", id); err != nil {
		t.Fatal(err)
	}
	if count, err := service.Sweep(t.Context()); err != nil || count != 2 {
		t.Fatalf("扫临期消息失败：%d %v", count, err)
	}
	assertNoticeCount(t, db, id, "material_due", 2)
	if _, err := db.Exec(t.Context(), "UPDATE business_cases SET cancelled_at=clock_timestamp() WHERE id=$1", filing); err != nil {
		t.Fatal(err)
	}
	if state := expiryCall(t, router, "GET", testRegulatorPath+"/"+id, nil, 200)["dateStatus"]; state != "suspended" {
		t.Fatalf("已经注销的材料仍可新办：%v", state)
	}
	if count, err := service.Sweep(t.Context()); err != nil || count != 0 {
		t.Fatalf("给已失效备案续发提醒：%d %v", count, err)
	}
}

// expiryFixture 在指定测试库创建本测试独立 schema 及附件目录。
// 参数 t 为测试上下文。
// 返回值 隔离的 PostgreSQL 连接池和材料服务。
func expiryFixture(t *testing.T) (*pgxpool.Pool, *cases.Service) {
	t.Helper()
	raw := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if raw == "" {
		t.Skip("未配置专用隔离 PostgreSQL")
	}
	config, err := pgxpool.ParseConfig(raw)
	if err != nil || config.ConnConfig.Database != "grade_business_test" {
		t.Fatal("材料到期测试需要 grade_business_test 专用数据库")
	}
	admin, err := pgxpool.NewWithConfig(t.Context(), config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	var schema string
	if err = admin.QueryRow(t.Context(), "SELECT 'expiry_'||replace(gen_random_uuid()::text,'-','')").Scan(&schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if _, err = admin.Exec(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer admin.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
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
	files, err := cases.New(pool, t.TempDir(), "有效期测试监管")
	if err != nil {
		t.Fatal(err)
	}
	return pool, files
}

// seedCaseMaterial 建立已核验、归属已知的材料原件。
// 参数 t、db、files 分别为隔离测试、数据库和已校验材料存储。
// 返回值 备案、附件标识和上海今日。
func seedCaseMaterial(t *testing.T, db *pgxpool.Pool, files *cases.Service) (string, string, string) {
	t.Helper()
	file, key, _, err := files.StagePublicationAttachment("备案表.pdf", strings.NewReader(testPDF))
	if err != nil {
		t.Fatal(err)
	}
	var filing, attachment, today string
	err = db.QueryRow(t.Context(), `WITH f AS (
	  INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status)
	  VALUES('filing','EXPIRY-F1','有效期样例系统',$1,$2,'3','approved') RETURNING id
	), a AS (
	  INSERT INTO case_attachments(case_id,kind,file_name,file_key,size_bytes)
	  SELECT id,'filing_form',$3,$4,$5 FROM f RETURNING id
	)
	SELECT f.id::text,a.id::text,(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date::text FROM f,a`,
		testOrganization, testInstitution, file.FileName, key, file.Size).Scan(&filing, &attachment, &today)
	if err != nil {
		t.Fatal(err)
	}
	return filing, attachment, today
}

// expiryCall 执行实际路由，核对状态码并读取 JSON 返回。
// 参数 t、mux、method、path、payload、status 为测试、接口及预期状态。
// 返回值 JSON 响应解码。
func expiryCall(t *testing.T, mux http.Handler, method, path string, payload any, status int) map[string]any {
	t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if payload != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s：HTTP %d，期望 %d：%s", method, path, w.Code, status, w.Body.String())
	}
	var item map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	return item
}
