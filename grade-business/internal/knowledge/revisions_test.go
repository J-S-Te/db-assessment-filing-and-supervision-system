package knowledge

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

// TestVersionQueryRejectsInvalidCursor 验证无效版本游标在读取数据库前返回输入错误。
// 参数 t 为测试上下文。
// 返回值 无。
func TestVersionQueryRejectsInvalidCursor(t *testing.T) {
	mux := http.NewServeMux()
	New(nil).Register(mux)
	path := collectionPath + "/00000000-0000-4000-8000-000000000001/versions?before=0"
	requireKnowledgeResponse(t, mux, http.MethodGet, path, "", 400, "code", "message")
}

// TestKnowledgeVersionHistory 验证版本快照、标签、停用记录与分页随业务维护持久化。
// 参数 t 为测试上下文，仅使用隔离 PostgreSQL schema。
// 返回值 无。
func TestKnowledgeVersionHistory(t *testing.T) {
	mux := http.NewServeMux()
	New(isolatedKnowledgePool(t)).Register(mux)
	id := insertEntry(t, mux, "初始标题", "standard", "隔离来源", "初始正文")
	path := collectionPath + "/" + id
	requireKnowledgeResponse(t, mux, http.MethodPatch, path+"/tags",
		`{"expectedVersion":1,"tags":["依据"]}`, 200, "version")
	requireKnowledgeResponse(t, mux, http.MethodPatch, path,
		`{"expectedVersion":2,"title":"更新标题","category":"standard","source":"隔离来源","body":"更新正文"}`, 200, "version")
	requireKnowledgeResponse(t, mux, http.MethodPatch, path+"/status",
		`{"expectedVersion":3,"active":false}`, 200, "version")
	requireKnowledgeResponse(t, mux, http.MethodPatch, path+"/tags",
		`{"expectedVersion":3,"tags":[]}`, 409, "code", "message")
	assertKnowledgeVersions(t, mux, path)

	filter := "?q=" + url.QueryEscape("更新标题")
	if items := getList(t, mux, collectionPath+filter).Items; len(items) != 0 {
		t.Fatalf("停用条目仍出现在普通检索结果: %+v", items)
	}
	items := readKnowledgeJSON[page](t, mux, collectionPath+filter+"&status=inactive").Items
	if len(items) != 1 || items[0].ID != id || items[0].InactiveAt == nil {
		t.Fatalf("停用筛选未返回正确条目: %+v", items)
	}
	requireKnowledgeResponse(t, mux, http.MethodPatch, path+"/status",
		`{"expectedVersion":4,"active":true}`, 200, "version")
	if items := getList(t, mux, collectionPath+filter).Items; len(items) != 1 || items[0].ID != id {
		t.Fatalf("重新启用后未恢复检索: %+v", items)
	}
}

// TestKnowledgeVersionBaseline 核对迁移保留升级前的当前版本，而不会伪造更早历史。
// 参数 t 为测试上下文，仅使用隔离数据库中的独立旧版 schema。
// 返回值 无。
func TestKnowledgeVersionBaseline(t *testing.T) {
	pool := knowledgeFixture(t, "002_knowledge.sql", "010_knowledge_maintenance.sql")
	var id string
	err := pool.QueryRow(t.Context(), `INSERT INTO knowledge_entries (title, category, source, body, tags, version)
		VALUES ('既有知识', 'standard', '历史来源', '迁移前正文', '{既有标签}', 7) RETURNING id::text`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	migrateKnowledgeFixture(t, pool, "015_knowledge_revisions.sql")
	mux := http.NewServeMux()
	New(pool).Register(mux)
	path := collectionPath + "/" + id + "/versions"
	version := readKnowledgeJSON[revisionDetail](t, mux, path+"/7")
	if version.Version != 7 || version.Action != "baseline" || version.Body != "迁移前正文" ||
		len(version.Tags) != 1 || version.Tags[0] != "既有标签" {
		t.Fatalf("旧条目的基线缺少有效信息: %+v", version)
	}
	requireKnowledgeResponse(t, mux, http.MethodGet, path+"/6", "", 404, "code", "message")
}

// assertKnowledgeVersions 核对停用条目的倒序分页和历次正文及标签。
// 参数 t 为测试上下文，mux 为实际处理器，path 为条目详情地址。
// 返回值 无。
func assertKnowledgeVersions(t *testing.T, mux http.Handler, path string) {
	t.Helper()
	history := readKnowledgeJSON[struct {
		Items []struct {
			Version int64  `json:"version"`
			Action  string `json:"action"`
		} `json:"items"`
		NextCursor string `json:"nextCursor"`
	}](t, mux, path+"/versions?limit=2")
	if len(history.Items) != 2 || history.Items[0].Version != 4 ||
		history.Items[0].Action != "disable" || history.Items[1].Action != "edit" || history.NextCursor != "3" {
		t.Fatalf("历史分页与操作来源不匹配: %+v", history)
	}
	old := readKnowledgeJSON[struct {
		Body   string   `json:"body"`
		Title  string   `json:"title"`
		Tags   []string `json:"tags"`
		Action string   `json:"action"`
	}](t, mux, path+"/versions/2")
	if old.Body != "初始正文" || old.Title != "初始标题" || len(old.Tags) != 1 ||
		old.Tags[0] != "依据" || old.Action != "tags" {
		t.Fatalf("历史版本未保留旧正文和当时标签: %+v", old)
	}
}

// readKnowledgeJSON 读取并解码知识接口的成功响应。
// 参数 t 为测试上下文，mux 为实际处理器，path 为请求地址。
// 返回值 已解码的知识业务响应。
func readKnowledgeJSON[T any](t *testing.T, mux http.Handler, path string) T {
	t.Helper()
	response := request(t, mux, http.MethodGet, path, nil)
	var result T
	if response.Code != http.StatusOK {
		t.Fatalf("读取 %s 返回 %d: %s", path, response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
