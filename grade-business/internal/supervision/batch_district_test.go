package supervision

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/access"
)

// TestDistrictBatchUsesCurrentFilingScope 核对预览、下发和重复回执都遵循真实会话当前辖区。
// 参数 t 提供隔离 PostgreSQL 和有效监管会话。
// 返回值 无。
func TestDistrictBatchUsesCurrentFilingScope(t *testing.T) {
	db, router := inspectionFixture(t)
	origin, due := seedDistrictInspectionFiling(t, db, "S-LOCAL-SOURCE", "330802", "S00000000000000001")
	own, _ := seedDistrictInspectionFiling(t, db, "S-LOCAL-RECIPIENT", "柯城区", "S00000000000000002")
	foreign, _ := seedDistrictInspectionFiling(t, db, "S-OTHER-RECIPIENT", "330803", "S00000000000000003")
	template := seedDistrictBatchTemplate(t, router, origin, due, sampleRequest)
	seedDistrictBatchTemplate(t, router, foreign, due, "90000000-0000-4000-8000-000000000002")
	api := access.New(db).Protect(router)
	regulator := districtBatchSession(t, db, api, "batch-test-regulator")
	base := testBase + "/batches"
	options := inspectionCall(t, regulator, "GET", base+"/template-options", nil, 200)
	templates := options["items"].([]any)
	if len(templates) != 1 || templates[0].(map[string]any)["id"] != template {
		t.Fatalf("辖区模板选项错误: %+v", options)
	}
	input := batchCriteria{Title: "辖区年度检查", Levels: []string{"3"}, DueOn: due,
		TemplateTaskID: template, TemplateVersion: 3}
	preview := inspectionCall(t, regulator, "POST", base+"/preview", input, 200)
	if preview["totalEligible"] != float64(1) || len(preview["items"].([]any)) != 2 {
		t.Fatalf("辖区批次预览错误: %+v", preview)
	}
	request := batchConfirm{batchCriteria: input, Token: preview["token"].(string)}
	result := inspectionCall(t, regulator, "POST", base, request, 201)
	if result["publishedCount"] != float64(1) {
		t.Fatalf("辖区批次下发错误: %+v", result)
	}
	other := districtBatchSession(t, db, api, "batch-second-regulator")
	assertDistrictBatchReceipt(t, db, regulator, other, own, request, result)
}

// seedDistrictBatchTemplate 给隔离备案准备一份完整可核验的原模板。
// 参数 t、router 为隔离测试网络，filingID、due、requestID 指模板绑定的备案与请求。
// 返回值 保存两份材料后的模板任务标识。
func seedDistrictBatchTemplate(t *testing.T, router http.Handler, filingID, due, requestID string) string {
	t.Helper()
	template := inspectionCall(t, router, "POST", testBase, map[string]any{
		"requestId": requestID, "filingId": filingID, "title": "样板任务", "dueOn": due,
	}, 201)["id"].(string)
	source := testBase + "/" + template
	inspectionUpload(t, router, source, "", inspectionTemplate, 1, testPDF, 201)
	inspectionUpload(t, router, source, "", summaryTemplate, 2, testPDF, 201)
	return template
}

// assertDistrictBatchReceipt 验证只下发给本辖区，且收件人迁出后不返回旧回执数据。
// 参数 t、db 为隔离测试环境，regulator、other 为辖区内的两个已验证账号，filingID、request、result 标记下发及回执。
// 返回值 无。
func assertDistrictBatchReceipt(t *testing.T, db *pgxpool.Pool, regulator, other http.Handler,
	filingID string, request batchConfirm, result map[string]any,
) {
	t.Helper()
	for _, raw := range result["items"].([]any) {
		item := raw.(map[string]any)
		if item["status"] == "published" && item["filingId"] != filingID {
			t.Fatalf("批次回执含非预期收件人: %+v", item)
		}
	}
	base := testBase + "/batches"
	replay := inspectionCall(t, regulator, "POST", base, request, 201)
	if replay["id"] != result["id"] {
		t.Fatalf("范围内回执无法重读: %+v", replay)
	}
	inspectionCall(t, other, "POST", base, request, 409)
	if _, err := db.Exec(t.Context(), `UPDATE directory_parties SET district='330803'
		WHERE id=(SELECT directory_unit_id FROM business_cases WHERE id=$1)`, filingID); err != nil {
		t.Fatal(err)
	}
	inspectionCall(t, regulator, "POST", base, request, 409)
}

// seedDistrictInspectionFiling 建立明确绑定可信名录的合成备案。
// 参数 t、db 为隔离测试环境，key、district、code 为样本的名称、辖区及固定编码。
// 返回值 备案标识及有效办理日期。
func seedDistrictInspectionFiling(t *testing.T, db *pgxpool.Pool, key, district, code string) (string, string) {
	t.Helper()
	id, due := seedInspectionFiling(t, db, key, key)
	_, err := db.Exec(t.Context(), `WITH unit AS (
		INSERT INTO directory_parties(kind,scope_name,name,code,district) VALUES('unit',$1,$1,$2,$3) RETURNING id
	) UPDATE business_cases f SET directory_unit_id=unit.id FROM unit WHERE f.id=$4`, key, code, district, id)
	if err != nil {
		t.Fatal(err)
	}
	return id, due
}

// districtBatchSession 通过真实鉴权网关取得隔离监管会话。
// 参数 t、db、api 为测试、数据库及实际请求路由，username 为合成监管账号。
// 返回值 绑定当前监管会话的业务路由。
func districtBatchSession(t *testing.T, db *pgxpool.Pool, api http.Handler, username string) http.Handler {
	t.Helper()
	const password = "Synthetic-Test_0831!"
	id, err := access.Provision(t.Context(), db, access.AccountInput{Username: username,
		Password: password, Name: "辖区批次经办", AreaCode: "330802", PersonKey: username, Role: access.Regulator})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(t.Context(), `UPDATE access_accounts SET must_change_password=false WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRequestWithContext(t.Context(), "POST", "/api/v1/auth/login",
		strings.NewReader(`{"username":"`+username+`","password":"`+password+`","areaCode":"330802"}`))
	login.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, login)
	cookies := response.Result().Cookies()
	if response.Code != 200 || len(cookies) != 1 {
		t.Fatalf("监管测试账号登录失败，状态 %d", response.Code)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(cookies[0])
		api.ServeHTTP(w, r)
	})
}
