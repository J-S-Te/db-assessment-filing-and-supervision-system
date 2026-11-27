package knowledge

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"grade-business/internal/tagcatalog"
)

const catalogURL = "/api/v1/tag-catalog"

type testCatalogTag struct {
	ID, Name, Color string
	Active          bool
	UsageCount      int
	Version         int
}

// TestKnowledgeTagCatalogBackfillsWithoutChangingVersions 验证存量大小写别名合并并保存历史标签原文。
// 参数 t 为隔离业务数据库测试上下文。
// 返回值 无。
func TestKnowledgeTagCatalogBackfillsWithoutChangingVersions(t *testing.T) {
	pool := knowledgeFixture(t, "002_knowledge.sql", "010_knowledge_maintenance.sql", "015_knowledge_revisions.sql")
	mux := http.NewServeMux()
	New(pool).Register(mux)
	first := insertEntry(t, mux, "历史知识一", "standard", "隔离测试", "历史正文")
	second := insertEntry(t, mux, "历史知识二", "standard", "隔离测试", "历史正文")
	assertCatalogAssignment(t, mux, first, `{"tags":["API","已移除旧标签"],"expectedVersion":1}`, 200)
	assertCatalogAssignment(t, mux, second, `{"tags":["api"],"expectedVersion":1}`, 200)
	assertCatalogAssignment(t, mux, first, `{"tags":["API"],"expectedVersion":2}`, 200)
	if _, err := pool.Exec(t.Context(), `UPDATE knowledge_entries SET tags=ARRAY['API','api'] WHERE id=$1`, first); err != nil {
		t.Fatal(err)
	}
	migrateKnowledgeFixture(t, pool, "019_knowledge_tag_catalog.sql")
	tagcatalog.Register(mux, pool)
	active := catalogTagByName(t, mux, "api")
	old := catalogTagByName(t, mux, "已移除旧标签")
	version := readKnowledgeJSON[revisionDetail](t, mux, collectionPath+"/"+first+"/versions/2")
	current := readKnowledgeJSON[entry](t, mux, collectionPath+"/"+first)
	if active.UsageCount != 2 || !active.Active || old.UsageCount != 0 ||
		version.Body != "历史正文" || !slices.Equal(version.Tags, []string{"API", "已移除旧标签"}) ||
		!slices.Equal(current.Tags, []string{"API", "api"}) {
		t.Fatalf("存量目录=%+v / %+v，历史=%+v", active, old, version.Tags)
	}
}

// TestKnowledgeTagCatalogLifecycle 验证颜色和停用带版本保护，停用仅限制新关联。
// 参数 t 为隔离业务数据库测试上下文。
// 返回值 无。
func TestKnowledgeTagCatalogLifecycle(t *testing.T) {
	pool := isolatedKnowledgePool(t)
	mux := http.NewServeMux()
	New(pool).Register(mux)
	tagcatalog.Register(mux, pool)
	item := callCatalog[testCatalogTag](t, mux, "POST", catalogURL, `{"name":"Scope","color":"#FF002A"}`, 201)
	if item.Name != "Scope" || item.Color != "#ff002a" || !item.Active || item.Version != 1 || item.UsageCount != 0 {
		t.Fatalf("新建目录标签 = %+v", item)
	}
	duplicate := callCatalog[map[string]string](t, mux, "POST", catalogURL, `{"name":"scope","color":"#000000"}`, 409)
	if duplicate["code"] != "duplicate_tag" {
		t.Fatalf("重复标签错误 = %+v", duplicate)
	}
	first := insertEntry(t, mux, "已引用", "case", "隔离测试", "正文")
	second := insertEntry(t, mux, "未引用", "case", "隔离测试", "正文")
	assertCatalogAssignment(t, mux, first, `{"tags":["scope"],"expectedVersion":1}`, 200)
	assertCatalogDisable(t, mux, item, first, second)
	assertCatalogPages(t, mux)
}

// TestKnowledgeTagCatalogImportValidatesAndRollsBack 验证批导识别已停用标签及目录随批次回滚。
// 参数 t 为隔离业务数据库测试上下文。
// 返回值 无。
func TestKnowledgeTagCatalogImportValidatesAndRollsBack(t *testing.T) {
	pool := isolatedKnowledgePool(t)
	mux := http.NewServeMux()
	New(pool).Register(mux)
	tagcatalog.Register(mux, pool)
	item := callCatalog[testCatalogTag](t, mux, "POST", catalogURL, `{"name":"Dormant","color":"#123456"}`, 201)
	callCatalog[testCatalogTag](t, mux, "PATCH", catalogURL+"/"+item.ID, `{"color":"#123456","active":false,"expectedVersion":1}`, 200)
	valid := "title,category,source,body,tags\n目录批导甲,case,隔离导入,正文,\"[\"\"BatchTag\"\"]\"\n"
	invalid := valid + "目录批导乙,case,隔离导入,正文,\"[\"\"dormant\"\"]\"\n"
	preview := postCSV(t, mux, "/import/preview", invalid)
	var result struct{ Issues []importIssue }
	if preview.Code != 200 || json.Unmarshal(preview.Body.Bytes(), &result) != nil ||
		len(result.Issues) != 1 || result.Issues[0].Row != 3 {
		t.Fatalf("停用标签预检结果 = %d %s", preview.Code, preview.Body)
	}
	if failed := postCSV(t, mux, "/import", invalid); failed.Code != 400 {
		t.Fatalf("带停用标签的提交 = %d %s", failed.Code, failed.Body)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE knowledge_revisions ADD CHECK (title <> '目录批导丙')`); err != nil {
		t.Fatal(err)
	}
	if failed := postCSV(t, mux, "/import", valid+"目录批导丙,case,隔离导入,正文,\n"); failed.Code != 500 {
		t.Fatalf("模拟快照失败 = %d %s", failed.Code, failed.Body)
	}
	items := callCatalog[struct{ Items []testCatalogTag }](t, mux, "GET", catalogURL+"?q=BatchTag", "", 200)
	if len(items.Items) != 0 {
		t.Fatalf("导入失败后目录仍有部分写入：%+v", items)
	}
	if saved := postCSV(t, mux, "/import", valid); saved.Code != 201 || catalogTagByName(t, mux, "BatchTag").UsageCount != 1 {
		t.Fatalf("批导目录未同步 = %d %s", saved.Code, saved.Body)
	}
}

// TestKnowledgeTagCatalogConcurrentAssignments 验证并发的同名标签及不同大小写只登记一份目录。
// 参数 t 为隔离业务数据库测试上下文。
// 返回值 无。
func TestKnowledgeTagCatalogConcurrentAssignments(t *testing.T) {
	pool := isolatedKnowledgePool(t)
	mux := http.NewServeMux()
	New(pool).Register(mux)
	tagcatalog.Register(mux, pool)
	first := insertEntry(t, mux, "并发标签一", "case", "隔离测试", "正文")
	second := insertEntry(t, mux, "并发标签二", "case", "隔离测试", "正文")
	result := make(chan int, 2)
	for id, payload := range map[string]string{
		first:  `{"tags":["Parallel"],"expectedVersion":1}`,
		second: `{"tags":["parallel"],"expectedVersion":1}`,
	} {
		go func() { result <- request(t, mux, "PATCH", collectionPath+"/"+id+"/tags", []byte(payload)).Code }()
	}
	if one, two := <-result, <-result; one != 200 || two != 200 {
		t.Fatalf("并发标签维护 = %d / %d", one, two)
	}
	if tag := catalogTagByName(t, mux, "Parallel"); tag.UsageCount != 2 || tag.Version != 1 {
		t.Fatalf("并发写入后的目录记录 = %+v", tag)
	}
}

// assertCatalogDisable 核对停用后旧引用仍可维护和历史可查，新关联拒绝。
// 参数 t 为测试上下文，mux 为业务路由，item 为目录行，first、second 为已引用和未引用条目。
// 返回值 无。
func assertCatalogDisable(t *testing.T, mux http.Handler, item testCatalogTag, first, second string) {
	t.Helper()
	path := catalogURL + "/" + item.ID
	disabled := callCatalog[testCatalogTag](t, mux, "PATCH", path,
		`{"color":"#abcdef","active":false,"expectedVersion":1}`, 200)
	if disabled.Active || disabled.UsageCount != 1 || disabled.Color != "#abcdef" || disabled.Version != 2 {
		t.Fatalf("停用后的实际目录 = %+v", disabled)
	}
	filtered := callCatalog[struct{ Items []testCatalogTag }](t, mux, "GET", catalogURL+"?q=scope&status=disabled", "", 200)
	if len(filtered.Items) != 1 || filtered.Items[0].ID != item.ID || filtered.Items[0].Active {
		t.Fatalf("停用标签筛选结果 = %+v", filtered)
	}
	stale := callCatalog[map[string]string](t, mux, "PATCH", path,
		`{"color":"#abcdef","active":true,"expectedVersion":1}`, 409)
	if stale["code"] != "version_conflict" {
		t.Fatalf("旧目录版本错误 = %+v", stale)
	}
	assertCatalogAssignment(t, mux, first, `{"tags":["scope","追加标签"],"expectedVersion":2}`, 200)
	blocked := assertCatalogAssignment(t, mux, second, `{"tags":["SCOPE"],"expectedVersion":1}`, 409)
	if blocked["code"] != "tag_disabled" {
		t.Fatalf("停用标签新增错误 = %+v", blocked)
	}
	history := readKnowledgeJSON[revisionDetail](t, mux, collectionPath+"/"+first+"/versions/2")
	if !slices.Equal(history.Tags, []string{"scope"}) || catalogTagByName(t, mux, "追加标签").UsageCount != 1 {
		t.Fatalf("目录维护影响知识快照：%+v", history.Tags)
	}
	callCatalog[testCatalogTag](t, mux, "PATCH", path, `{"color":"#abcdef","active":true,"expectedVersion":2}`, 200)
	assertCatalogAssignment(t, mux, second, `{"tags":["SCOPE"],"expectedVersion":1}`, 200)
}

// assertCatalogPages 校验目录游标跨页无重叠并拒绝重复筛选参数。
// 参数 t 为测试上下文，mux 为目录业务路由。
// 返回值 无。
func assertCatalogPages(t *testing.T, mux http.Handler) {
	t.Helper()
	type catalogList struct {
		Items      []testCatalogTag
		NextCursor string
	}
	path := catalogURL + "?status=active&limit=1"
	first := callCatalog[catalogList](t, mux, "GET", path, "", 200)
	if len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("标签目录第一页 = %+v", first)
	}
	next := callCatalog[catalogList](t, mux, "GET", path+"&cursor="+url.QueryEscape(first.NextCursor), "", 200)
	if len(next.Items) != 1 || first.Items[0].ID == next.Items[0].ID || next.NextCursor != "" {
		t.Fatalf("标签目录第二页 = %+v", next)
	}
	callCatalog[map[string]string](t, mux, "GET", path+"&status=disabled", "", 400)
}

// assertCatalogAssignment 写入一条知识的标签并核对接口状态。
// 参数 t 为测试上下文，mux 为业务路由，id、body、status 指定请求字段及预期状态。
// 返回值 成功版本或可展示错误。
func assertCatalogAssignment(t *testing.T, mux http.Handler, id, body string, status int) map[string]any {
	t.Helper()
	return callCatalog[map[string]any](t, mux, "PATCH", collectionPath+"/"+id+"/tags", body, status)
}

// catalogTagByName 查询测试中名称唯一的目录行。
// 参数 t 为测试上下文，mux 为业务路由，name 为预期名称的检索词。
// 返回值 唯一匹配的公开标签。
func catalogTagByName(t *testing.T, mux http.Handler, name string) testCatalogTag {
	t.Helper()
	path := catalogURL + "?q=" + url.QueryEscape(name)
	result := callCatalog[struct{ Items []testCatalogTag }](t, mux, "GET", path, "", 200)
	if len(result.Items) != 1 {
		t.Fatalf("检索 %q 应返回唯一标签：%+v", name, result.Items)
	}
	return result.Items[0]
}

// callCatalog 调用同源业务路由并解码真实响应。
// 参数 t 为测试上下文，mux 为路由，method、path、body、status 为请求与预期状态。
// 返回值 公开接口数据。
func callCatalog[T any](t *testing.T, mux http.Handler, method, path, body string, status int) T {
	t.Helper()
	var result T
	response := request(t, mux, method, path, []byte(body))
	if response.Code != status {
		t.Fatalf("%s %s = %d，预期 %d：%s", method, path, response.Code, status, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
