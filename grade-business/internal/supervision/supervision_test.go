package supervision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/internal/cases"
	"grade-business/migrations"
)

const sampleRequest = "90000000-0000-4000-8000-000000000001"
const testOperator = "监督本地运营者"
const testBase = "/api/v1/supervisions"
const testOperatorBase = "/api/v1/operator/supervisions"

// TestInspectionDraftScope 验证待下发任务只对监管可见，同请求重试复用同一任务。
// 参数 t 提供隔离真库及请求上下文。
// 返回值 无。
func TestInspectionDraftScope(t *testing.T) {
	db, router := inspectionFixture(t)
	filing, due := seedInspectionFiling(t, db, testOperator, "BA-001")
	payload := map[string]any{
		"requestId": sampleRequest, "filingId": filing, "title": "年度自查", "dueOn": due,
	}
	first := inspectionCall(t, router, "POST", testBase, payload, 201)
	second := inspectionCall(t, router, "POST", testBase, payload, 201)
	if first["id"] != second["id"] || first["status"] != "draft" || first["version"] != float64(1) {
		t.Fatalf("预期重复创建只保留一个草稿，实际 %+v、%+v", first, second)
	}
	path := testOperatorBase + "/" + first["id"].(string) + "?organization=" + url.QueryEscape(testOperator)
	inspectionCall(t, router, "GET", path, nil, 404)
	payload["title"] = "冲突的同请求"
	inspectionCall(t, router, "POST", testBase, payload, 409)
	payload["requestId"] = "90000000-0000-4000-8000-000000000002"
	inspectionCall(t, router, "POST", testBase, payload, 409)
}

// inspectionFixture 为每套监督检查测试建立独立 PostgreSQL schema 和附件目录。
// 参数 t 为测试上下文，要求隔离数据库配置。
// 返回值 连接池和实际业务路由。
func inspectionFixture(t *testing.T) (*pgxpool.Pool, *http.ServeMux) {
	t.Helper()
	raw := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if raw == "" {
		t.Skip("未配置隔离 PostgreSQL")
	}
	config, err := pgxpool.ParseConfig(raw)
	if err != nil || config.ConnConfig.Database != "grade_business_test" {
		t.Fatal("监督检查测试需要 grade_business_test 专用数据库")
	}
	ctx := t.Context()
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	var schema string
	if err := admin.QueryRow(ctx, "SELECT 'sup_'||replace(gen_random_uuid()::text,'-','')").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer admin.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	recordFiles, err := cases.New(pool, t.TempDir(), "隔离测试监管")
	if err != nil {
		t.Fatal(err)
	}
	router := http.NewServeMux()
	New(pool, recordFiles, "隔离测试监管").Register(router)
	return pool, router
}

// seedInspectionFiling 建立带有效归属和未来期限的隔离备案样本。
// 参数 t、db 为测试环境，name 和 number 为单位名称与备案编号。
// 返回值 备案标识及可用于任务的未来日期。
func seedInspectionFiling(t *testing.T, db *pgxpool.Pool, name, number string) (string, string) {
	t.Helper()
	var id, due string
	err := db.QueryRow(t.Context(), `INSERT INTO business_cases(kind,number,system_name,organization_name,
		assessment_org,level,status) VALUES('filing',$1,'自查测试系统',$2,'隔离机构','3','approved')
		RETURNING id::text,((CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date+10)::text`, number, name).Scan(&id, &due)
	if err != nil {
		t.Fatal(err)
	}
	return id, due
}

// inspectionCall 发起真 HTTP handler 请求并断言响应，仅处理 JSON 响应。
// 参数 t、router、method、path、payload、status 为测试环境和输入。
// 返回值 已解码的响应对象。
func inspectionCall(t *testing.T, router http.Handler, method, path string, payload any, status int) map[string]any {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatal(fmt.Sprintf("%s %s = %d，期望 %d，%s", method, path, w.Code, status, w.Body.String()))
	}
	var object map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &object); err != nil {
		t.Fatal(err)
	}
	return object
}
