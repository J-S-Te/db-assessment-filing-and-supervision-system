package publications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/migrations"
)

const adminPath = "/api/v1/publications"
const readerPath = "/api/v1/operator/publications"

// TestPublicationPublicSnapshotLifecycle 验证草稿隔离、版本确认发布、撤回及历史留痕。
// 参数 t 为真实隔离数据库测试上下文。
// 返回值 无。
func TestPublicationPublicSnapshotLifecycle(t *testing.T) {
	pool, router := publicationFixture(t)
	form := map[string]any{"category": "bulletin", "title": "初次发布", "publisher": "测试发布单位",
		"body": "原始通报内容", "pinned": true}
	created := pubRequest[publicationFixtureItem](t, router, "POST", adminPath, form, 201)
	if created.ID == "" || created.Version != 1 || created.Status != "draft" {
		t.Fatalf("unexpected draft: %+v", created)
	}
	admin := adminPath + "/" + created.ID
	reader := readerPath + "/" + created.ID
	pubRequest[map[string]string](t, router, "GET", reader, nil, 404)
	pubRequest[map[string]string](t, router, "PATCH", reader, form, 405)
	published := pubAction(t, router, admin, "publish", 1, 200)
	public := pubRequest[publicationFixtureItem](t, router, "GET", reader, nil, 200)
	if published.Version != 2 || public.Body != "原始通报内容" || public.Version != 2 || !public.Pinned {
		t.Fatalf("initial publication not visible: %+v, %+v", published, public)
	}
	form["expectedVersion"], form["title"], form["body"] = 2, "修订版标题", "修订后内容"
	updated := pubRequest[publicationFixtureItem](t, router, "PATCH", admin, form, 200)
	public = pubRequest[publicationFixtureItem](t, router, "GET", reader, nil, 200)
	if updated.Version != 3 || public.Title != "初次发布" || public.Version != 2 {
		t.Fatalf("unpublished revision leaked: %+v, %+v", updated, public)
	}
	pubAction(t, router, admin, "publish", 2, 409)
	assertPublicationRollback(t, pool, router, admin, reader)
	pubAction(t, router, admin, "publish", 3, 200)
	public = pubRequest[publicationFixtureItem](t, router, "GET", reader, nil, 200)
	if public.Body != "修订后内容" || public.Version != 4 {
		t.Fatalf("published revision not visible: %+v", public)
	}
	pubAction(t, router, admin, "withdraw", 4, 200)
	pubRequest[map[string]string](t, router, "GET", reader, nil, 404)
	assertPublicationHistory(t, router, admin)
}

type publicationFixtureItem struct {
	ID, Category, Title, Publisher, Body, Status string
	Version                                      int64
	Pinned                                       bool
}

// assertPublicationRollback 核对留痕失败会保留原线上版本和待发布草稿。
// 参数 t 为测试上下文，pool 为隔离库，router、admin 与 reader 为业务访问路径。
// 返回值 无。
func assertPublicationRollback(t *testing.T, pool *pgxpool.Pool, router http.Handler, admin, reader string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `ALTER TABLE publication_events
		ADD CONSTRAINT test_stop_publish CHECK (version<>4)`); err != nil {
		t.Fatal(err)
	}
	pubAction(t, router, admin, "publish", 3, 500)
	current := pubRequest[publicationFixtureItem](t, router, "GET", admin, nil, 200)
	online := pubRequest[publicationFixtureItem](t, router, "GET", reader, nil, 200)
	if current.Version != 3 || online.Version != 2 {
		t.Fatalf("failed publish altered pointer: draft=%+v online=%+v", current, online)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE publication_events DROP CONSTRAINT test_stop_publish`); err != nil {
		t.Fatal(err)
	}
}

// assertPublicationHistory 核对原文、动作及版本信息仍保存在监管留痕中。
// 参数 t 为测试上下文，router 为真实路由，admin 为监管单条公告路径。
// 返回值 无。
func assertPublicationHistory(t *testing.T, router http.Handler, admin string) {
	t.Helper()
	trail := pubRequest[struct {
		Items []struct {
			Version                int64
			Action, Body, Operator string
		}
	}](t, router, "GET", admin+"/history", nil, 200)
	if len(trail.Items) != 5 || trail.Items[0].Action != "withdraw" || trail.Items[2].Action != "edit" ||
		trail.Items[3].Body != "原始通报内容" || trail.Items[0].Operator != "公告测试席位" {
		t.Fatalf("incomplete historical versions: %+v", trail)
	}
}

// TestOperatorPublicationFiltersUsePublishedVersion 验证门户筛选只采用已发布快照并遵从置顶顺序。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestOperatorPublicationFiltersUsePublishedVersion(t *testing.T) {
	_, router := publicationFixture(t)
	form := map[string]any{"category": "announcement", "title": "置顶信息", "publisher": "本地测试席位",
		"body": "有效正文", "pinned": true}
	first := pubRequest[publicationFixtureItem](t, router, "POST", adminPath, form, 201)
	pubAction(t, router, adminPath+"/"+first.ID, "publish", 1, 200)
	form["pinned"], form["title"] = false, "后发布信息"
	second := pubRequest[publicationFixtureItem](t, router, "POST", adminPath, form, 201)
	pubAction(t, router, adminPath+"/"+second.ID, "publish", 1, 200)
	form["category"], form["title"], form["expectedVersion"] = "security_advisory", "不可公开查到", 2
	pubRequest[publicationFixtureItem](t, router, "PATCH", adminPath+"/"+second.ID, form, 200)
	list := pubRequest[struct{ Items []publicationFixtureItem }](t, router, "GET", readerPath+"?category=announcement", nil, 200)
	if len(list.Items) != 2 || list.Items[0].ID != first.ID || list.Items[1].Title != "后发布信息" {
		t.Fatalf("published snapshot list mismatch: %+v", list)
	}
	list = pubRequest[struct{ Items []publicationFixtureItem }](t, router, "GET", readerPath+"?category=security_advisory", nil, 200)
	if len(list.Items) != 0 {
		t.Fatalf("private revision leaked into operator search: %+v", list)
	}
	pubRequest[map[string]string](t, router, "GET", readerPath+"?status=draft", nil, 400)
}

// TestPublicationConcurrentActionsAndValidation 验证并发确认只创建一期公告，并严格拒绝无效字段。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestPublicationConcurrentActionsAndValidation(t *testing.T) {
	_, router := publicationFixture(t)
	form := map[string]any{"category": "security_advisory", "title": "安全提示", "publisher": "本地测试席位",
		"body": strings.Repeat("文", 8001), "pinned": false}
	pubRequest[map[string]string](t, router, "POST", adminPath, form, 400)
	form["body"], form["publisher"] = "核对已登记的整改任务。", "测试\n单位"
	pubRequest[map[string]string](t, router, "POST", adminPath, form, 400)
	form["publisher"] = "有效单位"
	item := pubRequest[publicationFixtureItem](t, router, "POST", adminPath, form, 201)
	path := adminPath + "/" + item.ID + "/actions"
	var workers sync.WaitGroup
	var statuses [2]int
	for i := range statuses {
		workers.Go(func() {
			input := strings.NewReader(`{"action":"publish","expectedVersion":1}`)
			request := httptest.NewRequest("POST", path, input)
			request.Header.Set("Content-Type", "application/json")
			reply := httptest.NewRecorder()
			router.ServeHTTP(reply, request)
			statuses[i] = reply.Code
		})
	}
	workers.Wait()
	if min(statuses[0], statuses[1]) != 200 || max(statuses[0], statuses[1]) != 409 {
		t.Fatalf("concurrent publish attempts were not serialized: %v", statuses)
	}
}

// TestAdvisoryReferencesOnlyActualAlert 验证预警来源真实、仅适用于安全预警公告且只向监管端展示。
// 参数 t 为隔离数据库及路由测试上下文。
// 返回值 无。
func TestAdvisoryReferencesOnlyActualAlert(t *testing.T) {
	pool, router := publicationFixture(t)
	form := map[string]any{"category": "security_advisory", "title": "安全预警公告", "publisher": "测试单位",
		"body": "请检查登记的责任分工。", "pinned": false, "alertId": "00000000-0000-4000-8000-000000000001"}
	pubRequest[map[string]string](t, router, "POST", adminPath, form, 400)
	alert := seedPublicationAlert(t, pool)
	form["alertId"], form["category"] = alert, "announcement"
	pubRequest[map[string]string](t, router, "POST", adminPath, form, 400)
	form["category"] = "security_advisory"
	created := pubRequest[map[string]any](t, router, "POST", adminPath, form, 201)
	if created["alertId"] != alert {
		t.Fatalf("linked alert missing from regulator result: %+v", created)
	}
	id := created["id"].(string)
	path := adminPath + "/" + id
	pubAction(t, router, path, "publish", 1, 200)
	public := pubRequest[map[string]any](t, router, "GET", readerPath+"/"+id, nil, 200)
	if _, leaked := public["alertId"]; leaked {
		t.Fatal("publication disclosed the internal alert identifier")
	}
	history := pubRequest[struct{ Items []map[string]any }](t, router, "GET", path+"/history", nil, 200)
	if len(history.Items) != 2 || history.Items[0]["alertId"] != alert || history.Items[1]["alertId"] != alert {
		t.Fatalf("alert omitted from publication history: %+v", history)
	}
}

// seedPublicationAlert 建立用于发布来源校验的真实隐患和站内预警。
// 参数 t 为测试上下文，pool 为隔离业务库。
// 返回值 实际预警标识。
func seedPublicationAlert(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(), `WITH filing AS (
		INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status)
		VALUES('filing','BA-PUB','测试系统','测试单位','测试机构','3','approved') RETURNING id
	), report AS (
		INSERT INTO business_cases(kind,number,filing_id,assessment_org,risk,status)
		SELECT 'report','BG-PUB',id,'测试机构','high','approved' FROM filing RETURNING id
	), plan AS (
		INSERT INTO remediation_plans(report_id,deadline,risks)
		SELECT id,CURRENT_DATE,'[{"risk":"high"}]' FROM report RETURNING id
	), hazard AS (
		INSERT INTO hazard_items(plan_id,risk_index,severity,description,measure,responsibility,deadline)
		SELECT id,1,'high','测试事项','核对责任分工','测试单位',CURRENT_DATE FROM plan RETURNING id
	) INSERT INTO local_alerts(hazard_id,kind,severity,system_name,organization_name,deadline)
	SELECT id,'high_risk','high','测试系统','测试单位',CURRENT_DATE FROM hazard RETURNING id::text`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// pubAction 以标准 JSON 提交一期发布操作。
// 参数 t 为测试上下文，router 为路由，path、action、version 和 want 指定行为及预期状态。
// 返回值 解码后的公告或错误响应。
func pubAction(t *testing.T, router http.Handler, path, action string, version int64, want int) publicationFixtureItem {
	t.Helper()
	return pubRequest[publicationFixtureItem](t, router, "POST", path+"/actions",
		map[string]any{"expectedVersion": version, "action": action}, want)
}

// pubRequest 向真实路由发送请求并核对 JSON 接口结果。
// 参数 t 为测试上下文，router、method、path、body 定义请求，want 为预期状态码。
// 返回值 响应 JSON 对象。
func pubRequest[T any](t *testing.T, router http.Handler, method, path string, body map[string]any, want int) T {
	t.Helper()
	var input bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&input).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, &input)
	request.Header.Set("Content-Type", "application/json")
	reply := httptest.NewRecorder()
	router.ServeHTTP(reply, request)
	if reply.Code != want {
		t.Fatalf("HTTP %d, want %d: %s", reply.Code, want, reply.Body.String())
	}
	var out T
	if want != 405 {
		if err := json.Unmarshal(reply.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// publicationFixture 在明确隔离的专用库中创建独立模式并应用迁移。
// 参数 t 为测试上下文，负责隔离资源生命周期。
// 返回值 业务连接池与实际 HTTP 路由。
func publicationFixture(t *testing.T) (*pgxpool.Pool, http.Handler) {
	t.Helper()
	pool := publicationTestDatabase(t)
	if err := migrations.Apply(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return pool, publicationTestRouter(pool)
}

// publicationTestRouter 构建使用当前隔离模式的完整公告接口。
// 参数 pool 为隔离测试库的连接池。
// 返回值 实际发布与收件路由。
func publicationTestRouter(pool *pgxpool.Pool) http.Handler {
	mux := http.NewServeMux()
	New(pool, "公告测试席位").Register(mux)
	return mux
}

// publicationTestDatabase 在指定隔离测试库准备空白数据库模式。
// 参数 t 为管理数据库模式生命周期的测试上下文。
// 返回值 使用独立模式的数据库池。
func publicationTestDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if dsn == "" {
		t.Skip("GRADE_BUSINESS_TEST_DSN not set")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil || config.ConnConfig.Database != "grade_business_test" {
		t.Fatal("requires explicitly isolated grade_business_test database")
	}
	admin, err := pgxpool.NewWithConfig(t.Context(), config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("publications_%d", time.Now().UnixNano())
	name := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+name+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
