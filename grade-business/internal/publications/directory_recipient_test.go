package publications

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestDirectoryRecipientsRetainPublishedAttribution 验证名录候选、停用检查和发布版本名称留痕。
// 参数 t 为隔离 PostgreSQL 与实际公告接口的测试上下文。
// 返回值 无。
func TestDirectoryRecipientsRetainPublishedAttribution(t *testing.T) {
	pool, router := publicationFixture(t)
	insertManagedRecipients(t, pool)
	seedPublicationRecipient(t, pool, "DIR-PUB-F", "名录单位", "名录机构", "approved", false)
	picks := pubRequest[map[string]any](t, router, "GET", adminPath+"/recipients?audience=operator", nil, 200)
	if items := picks["items"].([]any); len(items) != 2 {
		t.Fatalf("独立登记单位和备案存量单位应同时可选: %+v", picks)
	}
	assertVersionedDirectoryLabels(t, pool, router)
}

// insertManagedRecipients 准备同一有效范围的机构和两家已登记单位。
// 参数 t 为测试上下文，pool 为仅用于本用例的业务库。
// 返回值 无。
func insertManagedRecipients(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `INSERT INTO directory_parties(kind,scope_name,name,code)
		VALUES('unit','名录单位','新展示名','913300000000000001'),('unit','单独登记','单独登记','913300000000000002');
		INSERT INTO directory_parties(kind,scope_name,name,code,qualification,valid_until)
		VALUES('institution','名录机构','名录机构','DJCP-PUB','甲级',CURRENT_DATE+365)`)
	if err != nil {
		t.Fatal(err)
	}
}

// assertVersionedDirectoryLabels 核对更名只影响后续版本，停用阻止待发布对象。
// 参数 t 为测试上下文，pool 与 router 为隔离数据库、真实路由。
// 返回值 无。
func assertVersionedDirectoryLabels(t *testing.T, pool *pgxpool.Pool, router http.Handler) {
	t.Helper()
	form := map[string]any{"category": "announcement", "title": "名录公告", "publisher": "测试发布人",
		"body": "按名录投递", "pinned": false, "audience": "selected",
		"targets": []map[string]string{{"audience": "operator", "recipient": "名录单位"}}}
	draft := pubRequest[publicationFixtureItem](t, router, "POST", adminPath, form, 201)
	path := adminPath + "/" + draft.ID
	first := pubAction(t, router, path, "publish", draft.Version, 200)
	assertReceiptLabel(t, router, path+"/deliveries", "新展示名")
	_, err := pool.Exec(t.Context(), `UPDATE directory_parties SET name='再次更名'
		WHERE kind='unit' AND scope_name='名录单位'`)
	if err != nil {
		t.Fatal(err)
	}
	form["expectedVersion"], form["body"] = first.Version, "更新内容"
	revision := pubRequest[publicationFixtureItem](t, router, "PATCH", path, form, 200)
	second := pubAction(t, router, path, "publish", revision.Version, 200)
	assertReceiptLabel(t, router, path+"/deliveries?version=2", "新展示名")
	assertReceiptLabel(t, router, path+"/deliveries", "再次更名")
	query := "?organization=" + url.QueryEscape("再次更名")
	reader := readerPath + "/" + draft.ID
	pubRequest[publicationFixtureItem](t, router, "GET", reader+query, nil, 200)
	pubRequest[map[string]any](t, router, "POST", reader+"/read"+query,
		map[string]any{"expectedVersion": second.Version}, 200)
	form["expectedVersion"], form["body"] = second.Version, "待发布的修订"
	revision = pubRequest[publicationFixtureItem](t, router, "PATCH", path, form, 200)
	_, err = pool.Exec(t.Context(), `UPDATE directory_parties SET status='inactive'
		WHERE kind='unit' AND scope_name='名录单位'`)
	if err != nil {
		t.Fatal(err)
	}
	pubAction(t, router, path, "publish", revision.Version, 400)
	assertReceiptLabel(t, router, path+"/deliveries", "再次更名")
	pubRequest[publicationFixtureItem](t, router, "GET", reader+"?organization=名录单位", nil, 200)
}

// assertReceiptLabel 核对回执保留当前读取发布版本的收件名称和原范围名称。
// 参数 t 为测试上下文，router 为实际路由，path 为回执地址，label 为发布时展示名称。
// 返回值 无。
func assertReceiptLabel(t *testing.T, router http.Handler, path, label string) {
	t.Helper()
	page := pubRequest[struct {
		Items []struct{ Recipient, DisplayName string }
	}](t, router, "GET", path, nil, 200)
	if len(page.Items) != 1 || page.Items[0].Recipient != "名录单位" || page.Items[0].DisplayName != label {
		t.Fatalf("公告回执名称与发布时的快照不符: %+v", page)
	}
}
