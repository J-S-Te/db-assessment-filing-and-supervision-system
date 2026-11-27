package knowledge

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

// TestKnowledgeMaintenanceWithPostgres 核对正文编辑、标签维护和版本冲突。
// 参数 t 为测试上下文，仅使用隔离 PostgreSQL schema。
// 返回值 无。
func TestKnowledgeMaintenanceWithPostgres(t *testing.T) {
	mux := http.NewServeMux()
	New(isolatedKnowledgePool(t)).Register(mux)
	id := insertEntry(t, mux, "备案示例", "template", "纸质材料", "旧正文")
	path := collectionPath + "/" + id

	tags := `{"expectedVersion":1,"tags":[" 复核依据 ","等保 2.0"]}`
	requireKnowledgeResponse(t, mux, http.MethodPatch, path+"/tags", tags, 200, "version")
	edit := `{"expectedVersion":2,"title":"备案复核","category":"standard","source":"隔离测试标准","body":"新正文"}`
	requireKnowledgeResponse(t, mux, http.MethodPatch, path, edit, 200, "version")
	requireKnowledgeResponse(t, mux, http.MethodPatch, path+"/tags", tags, 409, "code", "message")
	requireKnowledgeResponse(t, mux, http.MethodPatch, path+"/tags",
		`{"expectedVersion":3,"tags":["ISO","iso"]}`, 400, "code", "message")
	requireKnowledgeResponse(t, mux, http.MethodPatch, path+"/tags",
		`{"expectedVersion":3,"tags":null}`, 400, "code", "message")
	requireKnowledgeResponse(t, mux, http.MethodPatch, path+"/tags",
		`{"expectedVersion":3,"tags":[],"body":"不适用字段"}`, 400, "code", "message")

	query := url.Values{"q": {"复核依据 新正文"}, "source": {"测试标准"}, "category": {"standard"}}
	items := getList(t, mux, collectionPath+"?"+query.Encode()).Items
	if len(items) != 1 || items[0].ID != id {
		t.Fatalf("维护后的知识检索结果不匹配: %+v", items)
	}
	detail := request(t, mux, http.MethodGet, path, nil)
	var result map[string]any
	if err := json.Unmarshal(detail.Body.Bytes(), &result); err != nil ||
		result["body"] != "新正文" || result["version"] != float64(3) {
		t.Fatalf("条目维护结果不匹配: %s", detail.Body.String())
	}
	requireKnowledgeResponse(t, mux, http.MethodPatch, path+"/tags",
		`{"expectedVersion":3,"tags":[]}`, 200, "version")
	if results := getList(t, mux, collectionPath+"?q="+url.QueryEscape("复核依据")); len(results.Items) != 0 {
		t.Fatalf("移除标签后仍能命中: %+v", results)
	}
}

// requireKnowledgeResponse 验证知识维护请求的状态及精简响应字段。
// 参数 t 为测试上下文，mux 为实际处理器，method、path、body 为请求，status、keys 为预期。
// 返回值 无。
func requireKnowledgeResponse(t *testing.T, mux http.Handler, method, path, body string, status int, keys ...string) {
	t.Helper()
	response := request(t, mux, method, path, []byte(body))
	if response.Code != status {
		t.Fatalf("%s %s 返回 %d，预期 %d: %s", method, path, response.Code, status, response.Body.String())
	}
	assertKeys(t, response.Body.Bytes(), keys...)
}
