package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestCreateValidatesInput 检查条目校验并保持正文原有排版。
// 参数 t 为测试上下文。
// 返回值 无。
func TestCreateValidatesInput(t *testing.T) {
	original := "  ## 正文\n\n  保留前导空格\t\n"
	input, err := validateCreate(createInput{Title: " 标准示例 ", Category: "standard", Source: " 公开材料 ", Body: original})
	if err != nil || input.Title != "标准示例" || input.Source != "公开材料" || input.Body != original {
		t.Fatalf("unexpected result %+v: %v", input, err)
	}
	for _, item := range []createInput{
		{Title: "A", Category: "unknown", Source: "S", Body: "B"},
		{Title: " ", Category: "standard", Source: "S", Body: "B"},
		{Title: "A\nB", Category: "standard", Source: "S", Body: "B"},
		{Title: "A", Category: "standard", Source: "", Body: "B"},
		{Title: "A", Category: "standard", Source: "S", Body: " \n\t"},
		{Title: "A", Category: "standard", Source: "S", Body: "B\x00"},
		{Title: "A", Category: "standard", Source: "S", Body: strings.Repeat("中", 10923)},
	} {
		if _, err := validateCreate(item); err == nil {
			t.Errorf("invalid input accepted: category=%q title=%q body length=%d", item.Category, item.Title, len(item.Body))
		}
	}
}

// TestListQueryBounds 检查合法中文查询以及分页参数约束。
// 参数 t 为测试上下文。
// 返回值 无。
func TestListQueryBounds(t *testing.T) {
	q, err := parseListQuery(url.Values{"q": {"  网络安全\t企业% "}, "category": {"regulation"}})
	if err != nil || q.limit != 20 || q.category != "regulation" || !reflect.DeepEqual(q.terms, []string{"网络安全", "企业%"}) {
		t.Fatalf("unexpected query: %+v, %v", q, err)
	}
	for _, query := range []url.Values{
		{"limit": {"0"}}, {"limit": {"101"}}, {"limit": {"x"}},
		{"q": {strings.Repeat("界", 121)}}, {"cursor": {"garbage"}},
		{"q": {"one", "two"}}, {"category": {"other"}}, {"internalIP": {"invalid"}},
	} {
		if _, err := parseListQuery(query); err == nil {
			t.Errorf("invalid query accepted: %v", query)
		}
	}
}

// TestCursorRoundTrip 检查排序位置在编码和解码间保持精度。
// 参数 t 为测试上下文。
// 返回值 无。
func TestCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 11, 23, 12, 30, 1, 999999000, time.UTC)
	id := "00000000-0000-4000-8000-000000000002"
	cursor, err := decodeCursor(encodeCursor(at, id))
	if err != nil || !cursor.at.Equal(at) || cursor.id.String() != id {
		t.Fatalf("cursor = %+v, %v", cursor, err)
	}
}

// TestHTTPRejectsBadRequests 检查非法字段和对象标识在访问数据库前被拦截。
// 参数 t 为测试上下文。
// 返回值 无。
func TestHTTPRejectsBadRequests(t *testing.T) {
	mux := http.NewServeMux()
	New(nil).Register(mux)
	for _, item := range []struct {
		method string
		path   string
		body   string
		want   int
	}{
		{http.MethodGet, "/api/v1/knowledge?limit=999", "", http.StatusBadRequest},
		{http.MethodGet, "/api/v1/knowledge?q=%xG", "", http.StatusBadRequest},
		{http.MethodGet, "/api/v1/knowledge/invalid-id", "", http.StatusNotFound},
		{http.MethodPost, "/api/v1/knowledge", `{"title":"标题","category":"standard","source":"材料","body":"正文","internalIP":"192.0.2.1"}`, http.StatusBadRequest},
		{http.MethodPost, "/api/v1/knowledge", `{"title":"标题","category":"standard","source":"材料","body":""}`, http.StatusBadRequest},
	} {
		resp := request(t, mux, item.method, item.path, []byte(item.body))
		if resp.Code != item.want {
			t.Errorf("%s %s = %d, want %d", item.method, item.path, resp.Code, item.want)
		}
		assertKeys(t, resp.Body.Bytes(), "code", "message")
	}
}

// TestKnowledgeHTTPWithPostgres 验证条目录入、分类检索、分页和详情的数据库闭环。
// 参数 t 为测试上下文，仅使用名称匹配的隔离 PostgreSQL 数据库。
// 返回值 无。
func TestKnowledgeHTTPWithPostgres(t *testing.T) {
	pool := isolatedKnowledgePool(t)
	mux := http.NewServeMux()
	New(pool).Register(mux)
	first := insertEntry(t, mux, "备案材料标准", "standard", "业务来源", "入口必须附上示例登记表。")
	second := insertEntry(t, mux, "办理案例", "case", "备案材料目录", "缺少登记表的处理示例。")
	insertEntry(t, mux, "测评清单", "template", "样本", "其他内容。")
	assertSearchAndPagination(t, pool, mux, first, second)
	assertDetail(t, mux, first)
}

// isolatedKnowledgePool 创建并迁移本测试专用的 PostgreSQL schema。
// 参数 t 为测试上下文，GRADE_BUSINESS_TEST_DSN 由测试环境提供。
// 返回值 当前 schema 的连接池。
func isolatedKnowledgePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return knowledgeFixture(t, "002_knowledge.sql", "010_knowledge_maintenance.sql",
		"015_knowledge_revisions.sql", "019_knowledge_tag_catalog.sql")
}

// knowledgeFixture 准备指定迁移状态的知识库测试 schema。
// 参数 t 为测试上下文，files 为需要安装的迁移文件。
// 返回值 隔离的 PostgreSQL 连接池。
func knowledgeFixture(t *testing.T, files ...string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if dsn == "" {
		t.Skip("GRADE_BUSINESS_TEST_DSN 未配置")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil || config.ConnConfig.Database != "grade_business_test" {
		t.Fatal("测试 DSN 必须指向 grade_business_test")
	}
	fixture := openSchemaPool(t, config)
	migrateKnowledgeFixture(t, fixture, files...)
	return fixture
}

// migrateKnowledgeFixture 按传入顺序在测试 schema 内安装知识库迁移。
// 参数 t 为测试上下文，pool 为已隔离连接池，files 为选定迁移文件名。
// 返回值 无。
func migrateKnowledgeFixture(t *testing.T, pool *pgxpool.Pool, files ...string) {
	t.Helper()
	for _, path := range files {
		ddl, err := os.ReadFile("../../migrations/" + path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), string(ddl)); err != nil {
			t.Fatal(err)
		}
	}
}

// openSchemaPool 创建带临时搜索路径的连接池并登记清理。
// 参数 t 为测试上下文，config 为已验证的隔离库连接配置。
// 返回值 仅用于当前知识库测试的连接池。
func openSchemaPool(t *testing.T, config *pgxpool.Config) *pgxpool.Pool {
	t.Helper()
	control, err := pgxpool.NewWithConfig(t.Context(), config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(control.Close)
	schema := fmt.Sprintf("knowledge_%d_%d", os.Getpid(), time.Now().UnixNano())
	name := pgx.Identifier{schema}.Sanitize()
	if _, err := control.Exec(t.Context(), "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := control.Exec(ctx, "DROP SCHEMA "+name+" CASCADE"); err != nil {
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

// assertSearchAndPagination 检查中文正文、来源和标题联合检索与同时间分页。
// 参数 t 为测试上下文，pool 为隔离池，mux 为实际处理器，first 和 second 为已插入的 ID。
// 返回值 无。
func assertSearchAndPagination(t *testing.T, pool *pgxpool.Pool, mux http.Handler, first, second string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), "UPDATE knowledge_entries SET updated_at = '2026-11-23T08:00:00Z'"); err != nil {
		t.Fatal(err)
	}
	query := "/api/v1/knowledge?q=" + url.QueryEscape("备案材料 登记表")
	filtered := getList(t, mux, query+"&category=standard")
	if len(filtered.Items) != 1 || filtered.Items[0].ID != first {
		t.Fatalf("classified full text results: %+v", filtered)
	}
	page := getList(t, mux, query+"&limit=1")
	if len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("first page: %+v", page)
	}
	next := getList(t, mux, query+"&limit=1&cursor="+url.QueryEscape(page.NextCursor))
	if len(next.Items) != 1 || next.NextCursor != "" {
		t.Fatalf("second page: %+v", next)
	}
	wantIDs := []string{first, second}
	sort.Sort(sort.Reverse(sort.StringSlice(wantIDs)))
	if page.Items[0].ID != wantIDs[0] || next.Items[0].ID != wantIDs[1] {
		t.Fatalf("unstable order: %s, %s", page.Items[0].ID, next.Items[0].ID)
	}
	literal := getList(t, mux, "/api/v1/knowledge?q=%25")
	if literal.Items == nil || len(literal.Items) != 0 {
		t.Fatalf("wildcard must be literal, empty items must be an array: %+v", literal)
	}
}

// assertDetail 检查单条记录字段及不存在时的状态。
// 参数 t 为测试上下文，mux 为实际处理器，id 为已有条目标识。
// 返回值 无。
func assertDetail(t *testing.T, mux http.Handler, id string) {
	t.Helper()
	resp := request(t, mux, http.MethodGet, "/api/v1/knowledge/"+id, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("detail: %d %s", resp.Code, resp.Body.String())
	}
	assertKeys(t, resp.Body.Bytes(), "id", "title", "category", "source", "body", "updatedAt", "tags", "version")
	var value entry
	if err := json.Unmarshal(resp.Body.Bytes(), &value); err != nil || value.Body != "入口必须附上示例登记表。" || value.ID != id {
		t.Fatalf("detail data = %+v, %v", value, err)
	}
	missing := request(t, mux, http.MethodGet, "/api/v1/knowledge/00000000-0000-4000-8000-000000000000", nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing detail = %d", missing.Code)
	}
}

// insertEntry 通过 HTTP 新增测试记录。
// 参数 t 为测试上下文，mux 为实际处理器，title、category、source、body 为业务字段。
// 返回值 新记录 ID。
func insertEntry(t *testing.T, mux http.Handler, title, category, source, body string) string {
	t.Helper()
	payload, err := json.Marshal(createInput{Title: title, Category: category, Source: source, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	resp := request(t, mux, http.MethodPost, "/api/v1/knowledge", payload)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.Code, resp.Body.String())
	}
	assertKeys(t, resp.Body.Bytes(), "id")
	var result createResult
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil || result.ID == "" {
		t.Fatalf("invalid result: %+v, %v", result, err)
	}
	return result.ID
}

// getList 读取列表并确认摘要未包含正文或额外字段。
// 参数 t 为测试上下文，mux 为实际处理器，path 为列表请求地址。
// 返回值 已解码的分页结果。
func getList(t *testing.T, mux http.Handler, path string) page {
	t.Helper()
	resp := request(t, mux, http.MethodGet, path, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list: %d %s", resp.Code, resp.Body.String())
	}
	var result page
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, value := range raw.Items {
		assertKeys(t, value, "id", "title", "category", "source", "updatedAt", "tags")
	}
	return result
}

// request 在进程内运行指定业务请求。
// 参数 t 为测试上下文，handler 为实际处理器，method、path、body 定义请求。
// 返回值 HTTP 响应记录器。
func request(t *testing.T, handler http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}

// assertKeys 校验响应只包含约定的 JSON 字段。
// 参数 t 为测试上下文，raw 为响应，want 为期望字段集合。
// 返回值 无。
func assertKeys(t *testing.T, raw []byte, want ...string) {
	t.Helper()
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(fields))
	for key := range fields {
		got = append(got, key)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("response fields = %v, want %v", got, want)
	}
}
