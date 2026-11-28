package implementations

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/internal/access"
	"grade-business/migrations"
)

const fixtureReviewHeader = "X-Implementation-Fixture-Reviewer"

// TestConcurrentSubmissionsRespectChangeQuota 验证同备案的并发提交和撤销均遵守年度额度。
// 参数 t 为数据库集成测试上下文。
// 返回值 无。
func TestConcurrentSubmissionsRespectChangeQuota(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(t, pool)
	filing := implementationTestFiling(t, pool, "测试备案-限额", approved)
	item := testInput(filing, change)
	drafts := make([]mutation, 4)
	for i := range drafts {
		drafts[i] = decodeResponse[mutation](t, callImplementation(mux, http.MethodPost, path, item), 201)
	}
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, len(drafts))
	for _, item := range drafts {
		go func(record mutation) {
			<-start
			results <- callImplementation(mux, http.MethodPost, path+"/"+record.ID+"/actions",
				command{Action: "submit", ExpectedVersion: record.Version})
		}(item)
	}
	close(start)
	var accepted []mutation
	var rejected int
	for range drafts {
		response := <-results
		if response.Code == http.StatusConflict {
			rejected++
			message := decodeResponse[map[string]string](t, response, 409)
			if message["code"] != "change_quota" {
				t.Fatalf("unexpected refusal: %v", message)
			}
			continue
		}
		accepted = append(accepted, decodeResponse[mutation](t, response, 200))
	}
	if len(accepted) != 3 || rejected != 1 {
		t.Fatalf("submitted: %v, rejected: %d", accepted, rejected)
	}
	assertQuota(t, mux, filing, 3)
	withdrawnRecord := performAction(t, mux, accepted[0], "withdraw", "")
	if withdrawnRecord.Status != withdrawn {
		t.Fatalf("withdrawal = %+v", withdrawnRecord)
	}
	assertQuota(t, mux, filing, 2)
	for _, item := range drafts {
		if item.ID != accepted[0].ID && item.ID != accepted[1].ID && item.ID != accepted[2].ID {
			response := callImplementation(mux, http.MethodPost, path+"/"+item.ID+"/actions",
				command{Action: "submit", ExpectedVersion: item.Version})
			assertImplementationRuleFailure(t, response, "resubmission_cooldown")
		}
	}
	assertQuota(t, mux, filing, 2)
}

// TestImplementationReviewAndHistory 验证新增、补正、受理、三级审核及历史的 HTTP 持久化。
// 参数 t 为数据库集成测试上下文。
// 返回值 无。
func TestImplementationReviewAndHistory(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(t, pool)
	filing := implementationTestFiling(t, pool, "测试备案-评审", approved)
	draftFiling := implementationTestFiling(t, pool, "测试备案-未通过", draft)
	assertChoices(t, mux, filing)
	decodeResponse[map[string]string](t,
		callImplementation(mux, http.MethodPost, path, testInput(draftFiling, routine)), 400)

	item := testInput(filing, change)
	record := decodeResponse[mutation](t, callImplementation(mux, http.MethodPost, path, item), 201)
	detailPath := path + "/" + record.ID
	record = performAction(t, mux, record, "submit", "")
	submittedRecord := record
	record = performAction(t, mux, record, "accept", "")
	refused := callImplementation(mux, http.MethodPost, detailPath+"/actions",
		command{Action: "approve", ExpectedVersion: submittedRecord.Version})
	if decodeResponse[map[string]string](t, refused, 409)["code"] != "stale_version" {
		t.Fatal("旧版本审核未返回冲突")
	}
	record = performAction(t, mux, record, "return_for_correction", "补充停机时间窗口")
	assertQuota(t, mux, filing, 1)
	item.Plan = "按意见补充凌晨停机窗口。"
	record = decodeResponse[mutation](t, callImplementation(mux, http.MethodPatch, detailPath,
		patchInput{input: item, ExpectedVersion: record.Version}), 200)
	record = performAction(t, mux, record, "submit", "")
	assertQuota(t, mux, filing, 1)
	refused = callImplementation(mux, http.MethodPost, detailPath+"/actions",
		command{Action: "withdraw", ExpectedVersion: record.Version})
	if decodeResponse[map[string]string](t, refused, 409)["code"] != "withdraw_unavailable" {
		t.Fatal("既有受理历史的重新提交允准撤销")
	}
	record = performAction(t, mux, record, "accept", "")
	assertReviewFinishes(t, mux, record, item.Plan)
	if _, err := pool.Exec(t.Context(), "UPDATE business_cases SET cancelled_at=now() WHERE id=$1", filing); err != nil {
		t.Fatal(err)
	}
	assertChoices(t, mux, "")
	refused = callImplementation(mux, http.MethodPost, path, item)
	decodeResponse[map[string]string](t, refused, 400)
	decodeResponse[detail](t, callImplementation(mux, http.MethodGet, detailPath, nil), 200)
}

// isolatedImplementationPool 在隔离测试库的独立模式应用与应用相同的迁移。
// 参数 t 为测试生命周期与清理注册。
// 返回值 只访问当前实施集成测试模式的连接池。
func isolatedImplementationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if dsn == "" {
		t.Skip("未指定隔离的实施业务测试数据库")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil || config.ConnConfig.Database != "grade_business_test" {
		t.Fatal("GRADE_BUSINESS_TEST_DSN 必须使用隔离的 grade_business_test 数据库")
	}
	control, err := pgxpool.NewWithConfig(t.Context(), config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(control.Close)
	schema := fmt.Sprintf("implementation_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := control.Exec(t.Context(), "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := control.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Errorf("清理实施测试模式失败: %v", err)
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

// implementationTestMux 创建绑定测试库和固定业务时钟的路由。
// 参数 t 为测试生命周期，pool 为当前测试隔离连接池。
// 返回值 注册了真实 HTTP 处理逻辑的路由器。
func implementationTestMux(t *testing.T, pool *pgxpool.Pool) *http.ServeMux {
	service := New(pool, "测试办理人")
	service.now = func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, shanghai) }
	return implementationFixtureRoutes(t, pool, service)
}

// operatorImplementationTestMux 创建同时挂载运营者业务的隔离测试路由。
// 参数 t 为测试生命周期，pool 为当前测试模式的连接池。
// 返回值 监管、机构及运营者共享的业务路由。
func operatorImplementationTestMux(t *testing.T, pool *pgxpool.Pool) *http.ServeMux {
	service := New(pool, "测试办理人")
	service.now = func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, shanghai) }
	mux := implementationFixtureRoutes(t, pool, service)
	service.RegisterOperatorRoutes(mux)
	return mux
}

// implementationFixtureRoutes 挂载业务夹具，并让审核用例经真实会话权限网关办理。
// 参数 t、pool 为测试生命周期和隔离数据库，service 为已固定时钟的报备服务。
// 返回值 保留旧业务夹具入口并接受测试审核会话的路由。
func implementationFixtureRoutes(t *testing.T, pool *pgxpool.Pool, service *Service) *http.ServeMux {
	t.Helper()
	inner := http.NewServeMux()
	service.Register(inner)
	protected := access.New(pool).Protect(inner)
	initialReviewer := sync.OnceValue(func() *http.Cookie {
		return implementationFixtureSession(t, pool, "fixture-initial-reviewer")
	})
	finalReviewer := sync.OnceValue(func() *http.Cookie {
		return implementationFixtureSession(t, pool, "fixture-final-reviewer")
	})
	mux := http.NewServeMux()
	for _, prefix := range []string{path, institutionalPath} {
		mux.Handle(prefix, inner)
		mux.Handle(prefix+"/", inner)
	}
	mux.HandleFunc("POST "+path+"/{id}/actions", func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get(fixtureReviewHeader) {
		case "initial":
			r.AddCookie(initialReviewer())
		case "final":
			r.AddCookie(finalReviewer())
		default:
			inner.ServeHTTP(w, r)
			return
		}
		protected.ServeHTTP(w, r)
	})
	return mux
}

// implementationFixtureSession 在隔离库中建立供会话网关验证的一次性审核身份。
// 参数 t、pool 为测试环境，username 区分两位审核人。
// 返回值 与库中会话摘要对应的测试 Cookie。
func implementationFixtureSession(t *testing.T, pool *pgxpool.Pool, username string) *http.Cookie {
	t.Helper()
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw[:])
	if _, err := pool.Exec(t.Context(), `WITH account AS (
	  INSERT INTO access_accounts (username,display_name,role_code,person_key,password_salt,password_hash,must_change_password)
	  VALUES ($1,'测试审核人','ROLE_SYS_ADMIN',$1,decode(repeat('00',16),'hex'),decode(repeat('00',32),'hex'),false)
	  RETURNING id
	) INSERT INTO access_sessions (account_id,token_hash,expires_at)
	  SELECT id,$2,clock_timestamp()+interval '1 hour' FROM account`, username, hash[:]); err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: "grade_business_session", Value: base64.RawURLEncoding.EncodeToString(raw[:])}
}

// implementationTestFiling 创建给定状态的关联备案测试件。
// 参数 t 为测试上下文，pool 为隔离库，number 和 status 为测试件内容。
// 返回值 新建测试备案的公开标识。
func implementationTestFiling(t *testing.T, pool *pgxpool.Pool, number, status string) string {
	t.Helper()
	var filing string
	err := pool.QueryRow(t.Context(), `INSERT INTO business_cases
	    (kind, number, system_name, organization_name, assessment_org, level, status)
	    VALUES ('filing', $1, '测试系统', '测试单位', '备案测评机构', '2', $2) RETURNING id::text`,
		number, status).Scan(&filing)
	if err != nil {
		t.Fatal(err)
	}
	return filing
}

// testInput 创建可提交的实际报备表单。
// 参数 filing 和 reportType 为报备选中的备案和类型。
// 返回值 完整的有效表单。
func testInput(filing, reportType string) input {
	return input{
		FilingID: filing, Type: reportType, Reporter: "测试人员", PlannedStart: "2027-01-04",
		PlannedEnd: "2027-01-07", Plan: "系统迁移及影响范围。",
	}
}

// callImplementation 用真实 HTTP 路由发送业务请求。
// 参数 mux 为路由，method 和 endpoint 为请求目标，body 为可选 JSON 数据，reviewer 为测试审核身份。
// 返回值 HTTP 响应记录。
func callImplementation(mux *http.ServeMux, method, endpoint string, body any, reviewer ...string) *httptest.ResponseRecorder {
	payload, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	request := httptest.NewRequest(method, endpoint, bytes.NewReader(payload))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if len(reviewer) != 0 {
		request.Header.Set(fixtureReviewHeader, reviewer[0])
	}
	output := httptest.NewRecorder()
	mux.ServeHTTP(output, request)
	return output
}

// decodeResponse 核对实际响应状态并读取响应 JSON。
// 参数 t 为测试上下文，response 为真实响应，status 为预期状态码。
// 返回值 解码后的响应数据。
func decodeResponse[T any](t *testing.T, response *httptest.ResponseRecorder, status int) T {
	t.Helper()
	var result T
	if response.Code != status {
		t.Fatalf("status %d, want %d: %s", response.Code, status, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// performAction 执行业务动作并验证同一记录获得唯一后续版本。
// 参数 t 为测试上下文，mux 为路由，record、action 和 note 为命令参数。
// 返回值 已持久化的报备版本。
func performAction(t *testing.T, mux *http.ServeMux, record mutation, action, note string) mutation {
	t.Helper()
	var reviewers []string
	if action == "approve" {
		reviewer := "initial"
		if record.Status == final {
			reviewer = "final"
		}
		reviewers = append(reviewers, reviewer)
	}
	response := callImplementation(mux, http.MethodPost, path+"/"+record.ID+"/actions",
		command{Action: action, ExpectedVersion: record.Version, Comment: note}, reviewers...)
	result := decodeResponse[mutation](t, response, 200)
	if result.ID != record.ID || result.Version != record.Version+1 {
		t.Fatalf("record progression %+v to %+v", record, result)
	}
	return result
}

// assertQuota 检查上海年度与当前备案实际额度。
// 参数 t 为测试上下文，mux 为路由，filing 为备案标识，used 为预期已用次数。
// 返回值 无。
func assertQuota(t *testing.T, mux *http.ServeMux, filing string, used int) {
	t.Helper()
	actual := decodeResponse[quota](t, callImplementation(mux, http.MethodGet, path+"/quota?filingId="+filing, nil), 200)
	if actual.Year != 2027 || actual.Used != used || actual.Limit != 3 {
		t.Fatalf("额度 = %+v, 已用预期 %d", actual, used)
	}
}

// assertChoices 检查可关联选项只包含有效备案。
// 参数 t 为测试上下文，mux 为路由，expected 为唯一预期可选标识，空值表示没有选项。
// 返回值 无。
func assertChoices(t *testing.T, mux *http.ServeMux, expected string) {
	t.Helper()
	result := decodeResponse[struct{ Items []choice }](t,
		callImplementation(mux, http.MethodGet, path+"/filing-options", nil), 200)
	if (expected == "" && len(result.Items) != 0) ||
		(expected != "" && (len(result.Items) != 1 || result.Items[0].ID != expected)) {
		t.Fatalf("unexpected choices: %+v", result)
	}
}

// assertReviewFinishes 验证剩余三级审核、列表和审核意见历史持久化。
// 参数 t 为测试上下文，mux 为路由，record 为已受理报备，plan 为已补正的实施计划。
// 返回值 无。
func assertReviewFinishes(t *testing.T, mux *http.ServeMux, record mutation, plan string) {
	t.Helper()
	for _, next := range []string{final, supervision, approved} {
		record = performAction(t, mux, record, "approve", "审核通过")
		if record.Status != next {
			t.Fatalf("审核进度 = %+v, want %s", record, next)
		}
	}
	detailPath := path + "/" + record.ID
	result := decodeResponse[detail](t, callImplementation(mux, http.MethodGet, detailPath, nil), 200)
	if result.Plan != plan || result.AcceptedAt == nil || result.AssessmentOrg != "备案测评机构" ||
		result.Status != approved || !slices.Equal(result.AllowedActions, []string{"archive"}) {
		t.Fatalf("detail = %+v", result)
	}
	list := decodeResponse[page](t,
		callImplementation(mux, http.MethodGet, path+"?status=approved&type=change&limit=1", nil), 200)
	if len(list.Items) != 1 || list.Items[0].ID != record.ID {
		t.Fatalf("list = %+v", list)
	}
	latest := decodeResponse[eventPage](t, callImplementation(mux, http.MethodGet, detailPath+"/history?limit=2", nil), 200)
	if len(latest.Items) != 2 || latest.Items[0].ToStatus != approved || latest.NextBeforeVersion == 0 {
		t.Fatalf("history page = %+v", latest)
	}
	query := fmt.Sprintf("%s/history?beforeVersion=%d", detailPath, latest.NextBeforeVersion)
	older := decodeResponse[eventPage](t, callImplementation(mux, http.MethodGet, query, nil), 200)
	for _, event := range older.Items {
		if event.Action == "return_for_correction" && event.Comment == "补充停机时间窗口" {
			return
		}
	}
	t.Fatalf("未持久化补正意见: %+v", older.Items)
}
