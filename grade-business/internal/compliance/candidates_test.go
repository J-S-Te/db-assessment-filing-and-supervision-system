package compliance

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

type candidateFixture struct {
	ID, Title, Source string
	Version           int64
	InOriginal        bool
	InCorrected       bool
}

// TestComplianceRejectsMalformedUUID 拒绝错误分隔符并保持合法大写标识兼容。
// 参数 t 为合规证据标识格式测试。
// 返回值 无。
func TestComplianceRejectsMalformedUUID(t *testing.T) {
	if _, ok := parseUUID("bc0e1f00x9461-448b-915b-549757e58300"); ok {
		t.Fatal("错误分隔符的附件或报告标识通过校验")
	}
	const canonical = "bc0e1f00-9461-448b-915b-549757e58300"
	if value, ok := parseUUID("BC0E1F00-9461-448B-915B-549757E58300"); !ok || value != canonical {
		t.Fatalf("合法大写标识未规范化：%q %t", value, ok)
	}
}

// TestCandidatesLimitReportsAdditionalSources 验证同名法规来源不合并，且结果为有上限的稳定顺序。
// 参数 t 为隔离 PostgreSQL 及 HTTP 验证上下文。
// 返回值 无。
func TestCandidatesLimitReportsAdditionalSources(t *testing.T) {
	pool, router := complianceFixture(t)
	source := seedComplianceSources(t, pool)
	const citation = "《固定条款》"
	_, err := pool.Exec(t.Context(), `UPDATE report_extractions SET fields=jsonb_set(
		jsonb_set(fields,'{0,original}',to_jsonb($1::text)),'{0,value}',to_jsonb($1::text))
		WHERE attachment_id=$2`, citation, source.attachmentID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), `WITH entries AS (
		INSERT INTO knowledge_entries(title,category,source,body)
		SELECT '固定条款','standard','source-' || lpad(i::text,2,'0'),'正文'
		FROM generate_series(1,22) AS i RETURNING *)
		INSERT INTO knowledge_revisions(entry_id,version,title,category,source,body,tags,updated_at,action)
		SELECT id,version,title,category,source,body,tags,updated_at,'create' FROM entries`)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/reports/" + source.reportID + "/compliance/candidates?attachmentId=" +
		source.attachmentID + "&extractionVersion=2&fieldKey=risk"
	page := complianceRequest[candidatePageFixture](t, router, http.MethodGet, path, nil, 200)
	if !page.HasMore || len(page.Items) != 20 {
		t.Fatalf("expected the first 20 matches and a hasMore indication, got %+v", page)
	}
	for index, item := range page.Items {
		want := fmt.Sprintf("source-%02d", index+1)
		if item.Source != want || !item.InOriginal || !item.InCorrected {
			t.Fatalf("wrong candidate at position %d: %+v", index, item)
		}
	}
}

type candidatePageFixture struct {
	Items   []candidateFixture
	HasMore bool
}

// TestCandidatesReturnExactCurrentDocumentReferences 验证候选只关联当前启用的正式知识条目并指明出处。
// 参数 t 为隔离 PostgreSQL 及 HTTP 验证上下文。
// 返回值 无。
func TestCandidatesReturnExactCurrentDocumentReferences(t *testing.T) {
	pool, router := complianceFixture(t)
	source := seedComplianceSources(t, pool)
	seedCandidateReferences(t, pool, source)
	path := "/api/v1/reports/" + source.reportID + "/compliance/candidates?attachmentId="
	query := source.attachmentID + "&extractionVersion=2&fieldKey=risk"
	page := complianceRequest[candidatePageFixture](t, router, http.MethodGet, path+query, nil, 200)
	if page.HasMore || len(page.Items) != 2 {
		t.Fatalf("expected exactly two current candidates, got %+v", page)
	}
	items := make(map[string]candidateFixture, len(page.Items))
	for _, item := range page.Items {
		items[item.Title] = item
	}
	if got := items["固定条款"]; got.ID != source.knowledgeID || got.Version != 1 ||
		got.Source != "内部测试依据" || !got.InOriginal || got.InCorrected {
		t.Fatalf("wrong original reference: %+v", got)
	}
	if got := items["校对标准"]; got.ID == "" || got.Version != 2 ||
		got.Source != "合成标准目录" || got.InOriginal || !got.InCorrected {
		t.Fatalf("wrong corrected reference: %+v", got)
	}
	var count int
	err := pool.QueryRow(t.Context(), `SELECT count(*) FROM report_compliance_records`).Scan(&count)
	if err != nil || count != 0 {
		t.Fatalf("candidate lookup must leave the human audit trail untouched: count=%d, error=%v", count, err)
	}
}

// TestCandidatesRequireCurrentReviewedEvidence 验证历史、其他报告的来源及已办结报告无法查询候选。
// 参数 t 为隔离 PostgreSQL 及 HTTP 验证上下文。
// 返回值 无。
func TestCandidatesRequireCurrentReviewedEvidence(t *testing.T) {
	pool, router := complianceFixture(t)
	source := seedComplianceSources(t, pool)
	path := "/api/v1/reports/" + source.reportID + "/compliance/candidates?attachmentId="
	query := source.attachmentID + "&extractionVersion=2&fieldKey=risk"
	page := complianceRequest[candidatePageFixture](t, router, http.MethodGet, path+query, nil, 200)
	if page.Items == nil || len(page.Items) != 0 || page.HasMore {
		t.Fatalf("evidence without a marked reference must return an empty array: %+v", page)
	}
	for _, bad := range []string{
		source.otherAttachmentID + "&extractionVersion=2&fieldKey=risk",
		source.attachmentID + "&extractionVersion=1&fieldKey=risk",
		source.attachmentID + "&extractionVersion=2&fieldKey=assets",
	} {
		complianceRequest[map[string]any](t, router, http.MethodGet, path+bad, nil, 409)
	}
	complianceRequest[map[string]any](t, router, http.MethodGet, path+query+"&fieldKey=risk", nil, 400)
	if _, err := pool.Exec(t.Context(), `UPDATE report_extractions SET version=3 WHERE attachment_id=$1`,
		source.attachmentID); err != nil {
		t.Fatal(err)
	}
	unsigned := source.attachmentID + "&extractionVersion=3&fieldKey=risk"
	complianceRequest[map[string]any](t, router, http.MethodGet, path+unsigned, nil, 409)
	if _, err := pool.Exec(t.Context(), `UPDATE business_cases SET status='approved' WHERE id=$1`,
		source.reportID); err != nil {
		t.Fatal(err)
	}
	complianceRequest[map[string]any](t, router, http.MethodGet, path+query, nil, 409)
}

// seedCandidateReferences 为报告构造可区分的原文、校对值、部分标题、停用和文本模板引用。
// 参数 t 为测试上下文，pool 为隔离库，source 为报告及来源。
// 返回值 无。
func seedCandidateReferences(t *testing.T, pool *pgxpool.Pool, source complianceSource) {
	t.Helper()
	const original = "原文引用《固定条款》；《停用依据》；《测试模板》；《部分标题》；旧版《旧标准》。" +
		"裸标题，外层书名《国标《内层同名》问答》。"
	const corrected = "校对后引用《校对标准》《校对标准》。"
	if _, err := pool.Exec(t.Context(), `UPDATE report_extractions SET fields=jsonb_build_array(
		jsonb_build_object('key','risk','original',$1::text,'value',$2::text,
		'startParagraph',4,'endParagraph',6)) WHERE attachment_id=$3`, original, corrected, source.attachmentID); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(t.Context(), `WITH entries AS (
		INSERT INTO knowledge_entries(title,category,source,body,version,inactive_at) VALUES
		('校对标准','regulation','合成标准目录','正文',2,NULL),
		('停用依据','standard','合成标准目录','正文',1,now()),
		('测试模板','template','合成标准目录','正文',1,NULL),
		('裸标题','standard','合成标准目录','正文',1,NULL),
		('内层同名','standard','合成标准目录','正文',1,NULL),
		('部分标题完整名称','regulation','合成标准目录','正文',1,NULL)
		RETURNING *)
		INSERT INTO knowledge_revisions(entry_id,version,title,category,source,body,tags,updated_at,inactive_at,action)
		SELECT id,version,title,category,source,body,tags,updated_at,inactive_at,'create' FROM entries
		UNION ALL SELECT id,1,'旧标准',category,source,body,tags,updated_at,NULL,'create'
		FROM entries WHERE version=2`)
	if err != nil {
		t.Fatal(err)
	}
}
