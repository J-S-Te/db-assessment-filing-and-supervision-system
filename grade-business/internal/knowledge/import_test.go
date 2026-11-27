package knowledge

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestImportRejectsMalformedMultipart 验证 CSV 批量预检必须接收唯一上传文件。
// 参数 t 为测试上下文。
// 返回值 无。
func TestImportRejectsMalformedMultipart(t *testing.T) {
	mux := http.NewServeMux()
	New(nil).Register(mux)
	requireKnowledgeResponse(t, mux, http.MethodPost, collectionPath+"/import/preview",
		`{"file":"report.csv"}`, 400, "code", "message")
}

// TestKnowledgeImportIsAtomic 核对预检不写入、无效批次回滚及成功导入后历史可查。
// 参数 t 为测试上下文，仅使用隔离 PostgreSQL schema。
// 返回值 无。
func TestKnowledgeImportIsAtomic(t *testing.T) {
	mux := http.NewServeMux()
	New(isolatedKnowledgePool(t)).Register(mux)
	csv := "title,category,source,body,tags\n批量一,standard,独立测试,正文,\"" +
		"[\"\"标签\"\"]\"\n批量二,case,独立测试,\"跨行\n正文\",\n"
	invalid := csv + "批量一,standard,独立测试,重复正文,\n"
	preview := postCSV(t, mux, "/import/preview", invalid)
	var result struct {
		Total  int `json:"total"`
		Issues []struct {
			Row int `json:"row"`
		} `json:"issues"`
	}
	if preview.Code != 200 || json.Unmarshal(preview.Body.Bytes(), &result) != nil ||
		result.Total != 3 || len(result.Issues) != 1 || result.Issues[0].Row != 5 {
		t.Fatalf("预检应指出重复行: %d %s", preview.Code, preview.Body)
	}
	if rejected := postCSV(t, mux, "/import", invalid); rejected.Code != 400 {
		t.Fatalf("无效批次应拒绝: %d %s", rejected.Code, rejected.Body)
	}
	search := collectionPath + "?source=" + url.QueryEscape("独立测试")
	if items := getList(t, mux, search).Items; len(items) != 0 {
		t.Fatalf("无效批次发生部分写入: %+v", items)
	}
	assertImportedKnowledge(t, mux, csv, search)
}

// TestKnowledgeImportRollsBackStorageFailure 核对后续快照保存失败时前面条目无部分提交。
// 参数 t 为测试上下文，仅使用隔离 PostgreSQL schema。
// 返回值 无。
func TestKnowledgeImportRollsBackStorageFailure(t *testing.T) {
	pool := isolatedKnowledgePool(t)
	if _, err := pool.Exec(t.Context(), `ALTER TABLE knowledge_revisions
		ADD CONSTRAINT test_import_failure CHECK (title <> '写入异常')`); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	New(pool).Register(mux)
	csv := "title,category,source,body,tags\n首条写入,case,回滚测试,正文,\n写入异常,case,回滚测试,正文,\n"
	if response := postCSV(t, mux, "/import", csv); response.Code != 500 {
		t.Fatalf("数据库错误应拒绝完整批次: %d %s", response.Code, response.Body)
	}
	search := collectionPath + "?source=" + url.QueryEscape("回滚测试")
	if items := getList(t, mux, search).Items; len(items) != 0 {
		t.Fatalf("快照保存失败后仍有部分条目: %+v", items)
	}
}

// TestKnowledgeImportTemplateValid 核对下载的模板符合服务端预检格式。
// 参数 t 为测试上下文。
// 返回值 无。
func TestKnowledgeImportTemplateValid(t *testing.T) {
	mux := http.NewServeMux()
	New(nil).Register(mux)
	result := request(t, mux, http.MethodGet, collectionPath+"/import/template", nil)
	if result.Code != 200 || result.Header().Get("Content-Disposition") == "" ||
		result.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("导入模板下载响应无效: %d %+v", result.Code, result.Header())
	}
	preview := parseImport(result.Body.Bytes()).preview()
	if len(preview.Items) != 1 || len(preview.Issues) != 0 || preview.Items[0].Title != "示例知识标题" {
		t.Fatalf("模板无法通过预检: %+v", preview)
	}
}

// TestKnowledgeImportSerializesDuplicates 验证同样的两批并发提交只能保存一批。
// 参数 t 为测试上下文，仅使用独立 PostgreSQL schema。
// 返回值 无。
func TestKnowledgeImportSerializesDuplicates(t *testing.T) {
	mux := http.NewServeMux()
	New(isolatedKnowledgePool(t)).Register(mux)
	csv := "title,category,source,body,tags\n同一知识,case,并行测试,正文,\n"
	results := make(chan int, 2)
	for range 2 {
		go func() { results <- postCSV(t, mux, "/import", csv).Code }()
	}
	first, second := <-results, <-results
	if first != 201 && second != 201 || first != 400 && second != 400 {
		t.Fatalf("并发重复导入结果为 %d 和 %d，期望一批成功和一批重复", first, second)
	}
	search := collectionPath + "?source=" + url.QueryEscape("并行测试")
	if items := getList(t, mux, search).Items; len(items) != 1 {
		t.Fatalf("并发导入产生 %d 条业务数据", len(items))
	}
}

// assertImportedKnowledge 核对有效批次保存知识与初始版本，重复导入被拒绝。
// 参数 t 为测试上下文，mux 为实际处理器，csv 与 search 为测试文件和查询地址。
// 返回值 无。
func assertImportedKnowledge(t *testing.T, mux http.Handler, csv, search string) {
	t.Helper()
	saved := postCSV(t, mux, "/import", csv)
	var inserted struct {
		Count int      `json:"count"`
		IDs   []string `json:"ids"`
	}
	if saved.Code != 201 || json.Unmarshal(saved.Body.Bytes(), &inserted) != nil ||
		inserted.Count != 2 || len(inserted.IDs) != 2 {
		t.Fatalf("有效批次写入失败: %d %s", saved.Code, saved.Body)
	}
	if items := getList(t, mux, search).Items; len(items) != 2 {
		t.Fatalf("导入的条目应进入检索: %+v", items)
	}
	old := readKnowledgeJSON[struct {
		Body   string   `json:"body"`
		Action string   `json:"action"`
		Tags   []string `json:"tags"`
	}](t, mux, collectionPath+"/"+inserted.IDs[0]+"/versions/1")
	if old.Body != "正文" || old.Action != "import" || len(old.Tags) != 1 || old.Tags[0] != "标签" {
		t.Fatalf("初始导入快照不正确: %+v", old)
	}
	if again := postCSV(t, mux, "/import", csv); again.Code != 400 {
		t.Fatalf("重复导入应被拒绝: %d %s", again.Code, again.Body)
	}
}

// postCSV 发送等同于浏览器上传的 CSV 文件。
// 参数 t 为测试上下文，mux 为实际处理器，endpoint 和 csv 为实际上传目标和内容。
// 返回值 记录的 HTTP 响应。
func postCSV(t *testing.T, mux http.Handler, endpoint, csv string) *httptest.ResponseRecorder {
	t.Helper()
	var data bytes.Buffer
	form := multipart.NewWriter(&data)
	part, err := form.CreateFormFile("file", "knowledge.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(csv)); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, collectionPath+endpoint, &data)
	req.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	return response
}
