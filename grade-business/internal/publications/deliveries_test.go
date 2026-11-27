package publications

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const institutionPath = "/api/v1/institution/publications"

type receiptTestState struct {
	ReadAt         *time.Time
	AcknowledgedAt *time.Time
}

type deliveryTestPage struct {
	Version int64
	Counts  struct{ Delivered, Read, Acknowledged int }
	Items   []struct {
		Audience, Recipient string
		DeliveredAt         time.Time
		receiptTestState
	}
	NextBefore string
}

// TestPublicationSelectedAudienceIsFixed 验证定向范围保存、有效名单解析和发布后收件范围固定。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestPublicationSelectedAudienceIsFixed(t *testing.T) {
	pool, router := publicationFixture(t)
	seedPublicationRecipient(t, pool, "BA-S1", "　甲单位 ", " 	甲单位", "approved", false)
	seedPublicationRecipient(t, pool, "BA-S2", "甲单位", "乙机构", "approved", false)
	seedPublicationRecipient(t, pool, "BA-S3", "已注销单位", "已注销机构", "approved", true)
	seedPublicationRecipient(t, pool, "BA-S4", "未生效单位", "未生效机构", "pending_final", false)
	seedPublicationRecipient(t, pool, "BA-S5", "含\n控制符", strings.Repeat("长", 201), "approved", false)

	roster := pubRequest[struct{ Items []map[string]string }](t, router, "GET",
		adminPath+"/recipients?audience=operator", nil, 200)
	if len(roster.Items) != 1 || roster.Items[0]["recipient"] != "甲单位" {
		t.Fatalf("scope roster contains invalid or duplicate entries: %+v", roster)
	}
	form := publicationDeliveryForm()
	form["audience"] = "selected"
	form["targets"] = []map[string]string{{"audience": "operator", "recipient": "未生效单位"}}
	pubRequest[map[string]any](t, router, "POST", adminPath, form, 400)
	form["targets"] = []map[string]string{
		{"audience": "operator", "recipient": "甲单位"},
		{"audience": "operator", "recipient": "　甲单位  "},
	}
	id := pubRequest[publicationFixtureItem](t, router, "POST", adminPath, form, 201).ID
	admin := adminPath + "/" + id
	published := pubAction(t, router, admin, "publish", 1, 200)
	detail := pubRequest[map[string]any](t, router, "GET", admin, nil, 200)
	if detail["audience"] != "selected" || len(detail["targets"].([]any)) != 1 {
		t.Fatalf("normalized policy missing: %+v", detail)
	}
	checkPublicationScope(t, pool, router, id, published.Version)
}

// checkPublicationScope 核对相同名称的不同角色隔离，以及新增备案不能补收旧版本。
// 参数 t 为测试上下文，pool 和 router 为隔离库和路由，id 为已发布公告，version 为发布版本。
// 返回值 无。
func checkPublicationScope(t *testing.T, pool *pgxpool.Pool, router http.Handler, id string, version int64) {
	t.Helper()
	scope := "?organization=" + url.QueryEscape("甲单位")
	reader := readerPath + "/" + id
	item := pubRequest[map[string]any](t, router, "GET", reader+scope, nil, 200)
	if item["deliveredAt"] == nil || item["targets"] != nil {
		t.Fatalf("invalid scoped response: %+v", item)
	}
	pubRequest[map[string]any](t, router, "GET", reader, nil, 404)
	pubRequest[map[string]any](t, router, "GET",
		institutionPath+"/"+id+"?institution="+url.QueryEscape("甲单位"), nil, 404)
	pubRequest[map[string]any](t, router, "POST", reader+"/read?organization=outside",
		map[string]any{"expectedVersion": version}, 404)
	seedPublicationRecipient(t, pool, "BA-NEW", "新增单位", "新增机构", "approved", false)
	pubRequest[map[string]any](t, router, "GET", reader+"?organization="+url.QueryEscape("新增单位"), nil, 404)
}

// TestPublicationReceiptFollowsPublishedVersion 验证签收隐含已读，重复提交保留时间，新版重新计算状态。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestPublicationReceiptFollowsPublishedVersion(t *testing.T) {
	pool, router := publicationFixture(t)
	seedPublicationRecipient(t, pool, "BA-R1", "Operator", "Institution", "approved", false)
	form := publicationDeliveryForm()
	id := pubRequest[publicationFixtureItem](t, router, "POST", adminPath, form, 201).ID
	admin := adminPath + "/" + id
	reader := institutionPath + "/" + id
	scope := "?institution=Institution"
	pubAction(t, router, admin, "publish", 1, 200)
	receipt := pubRequest[receiptTestState](t, router, "POST", reader+"/acknowledge"+scope,
		map[string]any{"expectedVersion": 2}, 200)
	retried := pubRequest[receiptTestState](t, router, "POST", reader+"/acknowledge"+scope,
		map[string]any{"expectedVersion": 2}, 200)
	if receipt.ReadAt == nil || receipt.AcknowledgedAt == nil ||
		!receipt.ReadAt.Equal(*retried.ReadAt) || !receipt.AcknowledgedAt.Equal(*retried.AcknowledgedAt) {
		t.Fatalf("receipt timestamps changed or missing: %+v / %+v", receipt, retried)
	}
	form["expectedVersion"] = 2
	form["body"] = "重新发布的正文"
	pubRequest[publicationFixtureItem](t, router, "PATCH", admin, form, 200)
	pubRequest[receiptTestState](t, router, "POST", reader+"/read"+scope, map[string]any{"expectedVersion": 2}, 200)
	pubAction(t, router, admin, "publish", 3, 200)
	pubRequest[map[string]any](t, router, "POST", reader+"/read"+scope, map[string]any{"expectedVersion": 2}, 409)
	current := pubRequest[map[string]any](t, router, "GET", reader+scope, nil, 200)
	if current["deliveredAt"] == nil || current["readAt"] != nil || current["acknowledgedAt"] != nil {
		t.Fatalf("old receipt carried into revision: %+v", current)
	}
	pubAction(t, router, admin, "withdraw", 4, 200)
	pubRequest[map[string]any](t, router, "POST", reader+"/acknowledge"+scope, map[string]any{"expectedVersion": 4}, 404)
	checkPublicationReceiptHistory(t, router, admin)
}

// checkPublicationReceiptHistory 核对撤回后默认统计末次发布，并可选择历史发布版本查询。
// 参数 t 为测试上下文，router 和 admin 为监管真实路由和单条公告路径。
// 返回值 无。
func checkPublicationReceiptHistory(t *testing.T, router http.Handler, admin string) {
	t.Helper()
	current := pubRequest[deliveryTestPage](t, router, "GET", admin+"/deliveries", nil, 200)
	if current.Version != 4 || current.Counts.Delivered != 2 || current.Counts.Read != 0 || current.Counts.Acknowledged != 0 {
		t.Fatalf("withdrawn publication lost or mixed its deliveries: %+v", current)
	}
	old := pubRequest[deliveryTestPage](t, router, "GET",
		admin+"/deliveries?version=2&audience=institution&q=stitut", nil, 200)
	if old.Version != 2 || old.Counts.Delivered != 2 || old.Counts.Read != 1 ||
		old.Counts.Acknowledged != 1 || len(old.Items) != 1 || old.Items[0].Recipient != "Institution" ||
		old.Items[0].AcknowledgedAt == nil || old.Items[0].ReadAt.Before(old.Items[0].DeliveredAt) {
		t.Fatalf("historical receipt unavailable or filtered counts: %+v", old)
	}
	pubRequest[map[string]any](t, router, "GET", admin+"/deliveries?version=3", nil, 404)
}

// TestPublicationBulkReadOnlyChangesActiveScope 验证跨页全部已读、筛选与不同范围独立，且已读不作签收。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestPublicationBulkReadOnlyChangesActiveScope(t *testing.T) {
	pool, router := publicationFixture(t)
	seedPublicationRecipient(t, pool, "BA-B1", "Operator", "Institution", "approved", false)
	seedPublicationRecipient(t, pool, "BA-B2", "Other", "Institution", "approved", false)
	paths := make([]string, 22)
	for index := range paths {
		id := pubRequest[publicationFixtureItem](t, router, "POST", adminPath, publicationDeliveryForm(), 201).ID
		paths[index] = adminPath + "/" + id
		pubAction(t, router, paths[index], "publish", 1, 200)
	}
	pubAction(t, router, paths[0], "withdraw", 2, 200)
	scope := "?organization=Operator"
	list := pubRequest[struct {
		Items      []publicationFixtureItem
		NextBefore string
	}](t, router, "GET", readerPath+scope+"&read=unread", nil, 200)
	if len(list.Items) != 20 || list.NextBefore == "" {
		t.Fatalf("unexpected first unread page: %+v", list)
	}
	count := pubRequest[map[string]int](t, router, "GET", readerPath+"/unread-count"+scope, nil, 200)
	result := pubRequest[map[string]int](t, router, "POST", readerPath+"/read-all"+scope, map[string]any{}, 200)
	if count["count"] != 21 || result["updated"] != 21 {
		t.Fatalf("bulk action failed to cover exactly the active deliveries: %v / %v", count, result)
	}
	verifyBulkReadScope(t, router, paths, scope)
}

// verifyBulkReadScope 核对重复批量已读、签收状态及其他收件范围。
// 参数 t 为测试上下文，router 为真实路由，paths 为公告监管路径，scope 为受影响运营者。
// 返回值 无。
func verifyBulkReadScope(t *testing.T, router http.Handler, paths []string, scope string) {
	t.Helper()
	count := pubRequest[map[string]int](t, router, "GET", readerPath+"/unread-count"+scope, nil, 200)
	other := pubRequest[map[string]int](t, router, "GET", readerPath+"/unread-count?organization=Other", nil, 200)
	repeat := pubRequest[map[string]int](t, router, "POST", readerPath+"/read-all"+scope, nil, 200)
	active := pubRequest[deliveryTestPage](t, router, "GET", paths[1]+"/deliveries", nil, 200)
	withdrawn := pubRequest[deliveryTestPage](t, router, "GET", paths[0]+"/deliveries", nil, 200)
	read := pubRequest[struct{ Items []publicationFixtureItem }](t, router, "GET", readerPath+scope+"&read=read", nil, 200)
	if count["count"] != 0 || other["count"] != 21 || repeat["updated"] != 0 || len(read.Items) != 20 ||
		active.Counts.Read != 1 || active.Counts.Acknowledged != 0 || withdrawn.Counts.Read != 0 {
		t.Fatalf("bulk read side effects: count=%v other=%v repeat=%v active=%+v withdrawn=%+v",
			count, other, repeat, active, withdrawn)
	}
	pubRequest[map[string]any](t, router, "GET", readerPath+"?read=read", nil, 400)
	pubRequest[map[string]any](t, router, "GET", institutionPath, nil, 400)
}

// TestPublicationDeliveryFailureRollsBackPublish 验证送达写入失败时公告版本和历史均回滚。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestPublicationDeliveryFailureRollsBackPublish(t *testing.T) {
	pool, router := publicationFixture(t)
	seedPublicationRecipient(t, pool, "BA-T1", "Operator", "Institution", "approved", false)
	id := pubRequest[publicationFixtureItem](t, router, "POST", adminPath, publicationDeliveryForm(), 201).ID
	admin := adminPath + "/" + id
	_, err := pool.Exec(t.Context(), `ALTER TABLE publication_deliveries
		ADD CONSTRAINT test_stop_delivery CHECK (recipient<>'Institution')`)
	if err != nil {
		t.Fatal(err)
	}
	pubAction(t, router, admin, "publish", 1, 500)
	item := pubRequest[publicationFixtureItem](t, router, "GET", admin, nil, 200)
	if item.Version != 1 || item.Status != "draft" {
		t.Fatalf("delivery insertion failure published an incomplete revision: %+v", item)
	}
	pubRequest[map[string]any](t, router, "GET", admin+"/deliveries", nil, 404)
	pubRequest[map[string]any](t, router, "GET", readerPath+"/"+id+"?organization=Operator", nil, 404)
}

// publicationDeliveryForm 提供可发布的公告表单用于接口测试。
// 参数 无。
// 返回值 每次独立的公告字段集合。
func publicationDeliveryForm() map[string]any {
	return map[string]any{"category": "announcement", "title": "通告标题", "publisher": "发布单位", "body": "通告正文"}
}

// seedPublicationRecipient 构造指定状态的备案及其本地收件范围。
// 参数 t 和 pool 为隔离测试上下文和连接池，number、operator、institution 为备案字段，status 和 cancelled 为其状态。
// 返回值 无。
func seedPublicationRecipient(t *testing.T, pool *pgxpool.Pool, number, operator, institution, status string, cancelled bool) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `INSERT INTO business_cases
		(kind,number,system_name,organization_name,assessment_org,level,status,cancelled_at)
		VALUES('filing',$1,'送达测试系统',$2,$3,'3',$4,CASE WHEN $5 THEN clock_timestamp() END)`,
		number, operator, institution, status, cancelled)
	if err != nil {
		t.Fatal(err)
	}
}

// TestPublicationLegacyEditPreservesRecipientPolicy 验证旧编辑表单不会扩大定向范围，显式切换范围才生效。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestPublicationLegacyEditPreservesRecipientPolicy(t *testing.T) {
	pool, router := publicationFixture(t)
	seedPublicationRecipient(t, pool, "BA-LEGACY", "NamedOperator", "NamedInstitution", "approved", false)
	form := publicationDeliveryForm()
	form["audience"] = "selected"
	form["targets"] = []map[string]string{{"audience": "operator", "recipient": "NamedOperator"}}
	id := pubRequest[publicationFixtureItem](t, router, "POST", adminPath, form, 201).ID
	admin := adminPath + "/" + id

	legacyEdit := publicationDeliveryForm()
	legacyEdit["expectedVersion"] = 1
	edited := pubRequest[map[string]any](t, router, "PATCH", admin, legacyEdit, 200)
	if edited["audience"] != "selected" || len(edited["targets"].([]any)) != 1 {
		t.Fatalf("legacy edit changed the selected delivery policy: %+v", edited)
	}
	pubAction(t, router, admin, "publish", 2, 200)
	pubRequest[map[string]any](t, router, "GET", readerPath+"/"+id, nil, 404)
	legacyEdit["expectedVersion"], legacyEdit["audience"] = 3, "all"
	pubRequest[map[string]any](t, router, "PATCH", admin, legacyEdit, 200)
	pubAction(t, router, admin, "publish", 4, 200)
	pubRequest[map[string]any](t, router, "GET", readerPath+"/"+id, nil, 200)
}

// TestPublicationReceiptCursorSurvivesRepublish 验证监管翻页游标始终绑定首次查询所用的发布版本。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestPublicationReceiptCursorSurvivesRepublish(t *testing.T) {
	pool, router := publicationFixture(t)
	for i := range 21 {
		seedPublicationRecipient(t, pool, fmt.Sprintf("BA-PAGE-%d", i),
			fmt.Sprintf("Operator-%02d", i), "Institution", "approved", false)
	}
	form := publicationDeliveryForm()
	id := pubRequest[publicationFixtureItem](t, router, "POST", adminPath, form, 201).ID
	admin := adminPath + "/" + id
	pubAction(t, router, admin, "publish", 1, 200)
	base := admin + "/deliveries"
	first := pubRequest[deliveryTestPage](t, router, "GET", base+"?audience=operator", nil, 200)
	if first.Version != 2 || len(first.Items) != 20 || first.NextBefore == "" {
		t.Fatalf("unexpected first delivery page: %+v", first)
	}
	form["expectedVersion"], form["audience"] = 2, "institution"
	pubRequest[publicationFixtureItem](t, router, "PATCH", admin, form, 200)
	pubAction(t, router, admin, "publish", 3, 200)
	cursor := url.QueryEscape(first.NextBefore)
	next := pubRequest[deliveryTestPage](t, router, "GET", base+"?audience=operator&before="+cursor, nil, 200)
	if next.Version != 2 || next.Counts.Delivered != 22 || len(next.Items) != 1 || next.NextBefore != "" {
		t.Fatalf("cursor mixed deliveries across versions: %+v", next)
	}
	for _, item := range first.Items {
		if item.Recipient == next.Items[0].Recipient {
			t.Fatalf("cursor repeated recipient: %+v", item)
		}
	}
	pubRequest[map[string]any](t, router, "GET", base+"?version=4&before="+cursor, nil, 400)
}
