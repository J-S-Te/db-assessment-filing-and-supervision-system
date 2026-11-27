package compliance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/migrations"
)

// TestRecordPinsVerifiedSourcesAndRespectsReviewState 验证登记绑定真实来源，且审结后仅可读留痕。
// 参数 t 为真实隔离库测试上下文。
// 返回值 无。
func TestRecordPinsVerifiedSourcesAndRespectsReviewState(t *testing.T) {
	pool, router := complianceFixture(t)
	source := seedComplianceSources(t, pool)
	path := "/api/v1/reports/" + source.reportID + "/compliance"
	input := map[string]any{"expectedReportVersion": 3, "attachmentId": source.attachmentID,
		"extractionVersion": 2, "fieldKey": "risk", "knowledgeId": source.knowledgeID,
		"knowledgeVersion": 1, "decision": "needs_remediation", "comment": "逐段核对后补充责任说明。"}
	input["attachmentId"] = source.otherAttachmentID
	complianceRequest[map[string]any](t, router, "POST", path, input, 409)
	input["attachmentId"] = source.attachmentID
	input["fieldKey"] = "assets"
	complianceRequest[map[string]any](t, router, "POST", path, input, 409)
	input["fieldKey"] = "risk"
	input["extractionVersion"] = 1
	complianceRequest[map[string]any](t, router, "POST", path, input, 409)
	input["extractionVersion"] = 2
	result := complianceRequest[auditFixture](t, router, "POST", path, input, 201)
	if result.ID == "" || result.Field.Original != "原始证据" || result.Field.Value != "已核对证据" ||
		result.Field.Start != 4 || result.AttachmentName != "测评正文.docx" || result.KnowledgeVersion != 1 ||
		result.Decision != "needs_remediation" || result.Operator != "合规测试席位" || result.ReportVersion != 3 {
		t.Fatalf("incorrect audit evidence: %+v", result)
	}
	var status string
	var version int64
	err := pool.QueryRow(t.Context(), `SELECT status,version FROM business_cases WHERE id=$1`, source.reportID).
		Scan(&status, &version)
	if err != nil || status != "pending_initial" || version != 3 {
		t.Fatalf("recording evidence changed report approval: %q %d, %v", status, version, err)
	}
	assertFrozenCompliance(t, pool, router, source, path, result, input)
}

// TestFinalReviewCompliancePagination 验证终审仍可登记，且历史分页无丢失或重复。
// 参数 t 为隔离库测试上下文。
// 返回值 无。
func TestFinalReviewCompliancePagination(t *testing.T) {
	pool, router := complianceFixture(t)
	source := seedComplianceSources(t, pool)
	path := "/api/v1/reports/" + source.reportID + "/compliance"
	if _, err := pool.Exec(t.Context(), `UPDATE business_cases SET status='pending_final',version=4
		WHERE id=$1`, source.reportID); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"expectedReportVersion": 3, "attachmentId": source.attachmentID,
		"extractionVersion": 2, "fieldKey": "risk", "knowledgeId": source.knowledgeID,
		"knowledgeVersion": 1, "decision": "insufficient_evidence", "comment": "请补充证据。"}
	complianceRequest[map[string]any](t, router, "POST", path, input, 409)
	input["expectedReportVersion"] = 4
	for i := range 21 {
		input["comment"] = fmt.Sprintf("第 %d 项缺少说明。", i+1)
		complianceRequest[auditFixture](t, router, "POST", path, input, 201)
	}
	first := complianceRequest[auditPage](t, router, "GET", path, nil, 200)
	if !first.CanRecord || len(first.Items) != 20 || first.NextBefore == "" || first.Items[0].Comment != "第 21 项缺少说明。" {
		t.Fatalf("unexpected first page summary: count=%d, cursor=%q", len(first.Items), first.NextBefore)
	}
	second := complianceRequest[auditPage](t, router, "GET", path+"?before="+first.NextBefore, nil, 200)
	if len(second.Items) != 1 || second.NextBefore != "" || second.Items[0].Comment != "第 1 项缺少说明。" {
		t.Fatalf("unexpected older page: %+v", second)
	}
	complianceRequest[map[string]any](t, router, "GET", path+"?before=1&before=2", nil, 400)
}

type auditFixture struct {
	ID, AttachmentName, KnowledgeTitle, Decision, Operator string
	ReportVersion, KnowledgeVersion                        int64
	Field                                                  struct {
		Original, Value string
		Start           int `json:"startParagraph"`
	}
}

// assertFrozenCompliance 修改当前数据后检验留痕仍指向登记当时的固定版本。
// 参数 t 为测试上下文，pool 为隔离库，router 为真实路由，source、path、result、input 为登记上下文。
// 返回值 无。
func assertFrozenCompliance(t *testing.T, pool *pgxpool.Pool, router http.Handler, source complianceSource,
	path string, result auditFixture, input map[string]any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE knowledge_entries
		SET title='后续版本标题',inactive_at=now(),version=2 WHERE id=$1`, source.knowledgeID); err != nil {
		t.Fatal(err)
	}
	complianceRequest[map[string]any](t, router, "POST", path, input, 409)
	if _, err := pool.Exec(t.Context(), `UPDATE report_extractions
		SET fields=jsonb_set(fields,'{0,value}','"后续校对"'),version=3 WHERE attachment_id=$1`, source.attachmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE business_cases SET status='approved',version=4 WHERE id=$1`, source.reportID); err != nil {
		t.Fatal(err)
	}
	input["expectedReportVersion"] = 4
	complianceRequest[map[string]any](t, router, "POST", path, input, 409)
	page := complianceRequest[struct {
		CanRecord bool
		Items     []auditFixture
	}](t, router, "GET", path, nil, 200)
	if page.CanRecord || len(page.Items) != 1 || page.Items[0] != result || page.Items[0].KnowledgeTitle != "固定条款" {
		t.Fatalf("audit trail changed with current sources: %+v", page)
	}
}

type complianceSource struct {
	reportID, attachmentID, otherAttachmentID, knowledgeID string
}

// seedComplianceSources 建立真实报告、审核确认及知识版本之间的引用关系。
// 参数 t 为测试上下文，pool 为隔离库连接池。
// 返回值 审核目标以及用于验证归属的另一报告正文标识。
func seedComplianceSources(t *testing.T, pool *pgxpool.Pool) complianceSource {
	t.Helper()
	var source complianceSource
	err := pool.QueryRow(t.Context(), `WITH filing AS (
		INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status)
		VALUES('filing','BA-COMP','测试系统','测试单位','测试机构','3','approved') RETURNING id
	), reports AS (
		INSERT INTO business_cases(kind,number,filing_id,assessment_org,risk,status,version)
		SELECT 'report',n,filing.id,'测试机构','low','pending_initial',3
		FROM filing CROSS JOIN (VALUES('BG-COMP'),('BG-OTHER')) AS nums(n) RETURNING id,number
	), attachments AS (
		INSERT INTO case_attachments(case_id,kind,file_name,file_key,size_bytes)
		SELECT id,'report_file','测评正文.docx',number,100 FROM reports RETURNING id,case_id
	), extracts AS (
		INSERT INTO report_extractions(attachment_id,fields,version,confirmed_at,confirmed_by)
		SELECT id,'[{"key":"risk","original":"原始证据","value":"已核对证据","startParagraph":4,"endParagraph":6}]',
		2,now(),'测试校对席位' FROM attachments RETURNING attachment_id
	), confirmations AS (
		INSERT INTO report_extraction_reviews(attachment_id,version,changes,operator,confirmed_at)
		SELECT attachment_id,2,'[]','测试校对席位',now() FROM extracts
	), knowledge AS (
		INSERT INTO knowledge_entries(title,category,source,body)
		VALUES('固定条款','standard','内部测试依据','测试条款文本。') RETURNING *
	), revisions AS (
		INSERT INTO knowledge_revisions(entry_id,version,title,category,source,body,tags,updated_at,action)
		SELECT id,version,title,category,source,body,tags,updated_at,'create' FROM knowledge
	) SELECT r.id::text,a.id::text,
		(SELECT b.id::text FROM attachments b WHERE b.case_id<>r.id),knowledge.id::text
	FROM reports r JOIN attachments a ON a.case_id=r.id CROSS JOIN knowledge WHERE r.number='BG-COMP'`).
		Scan(&source.reportID, &source.attachmentID, &source.otherAttachmentID, &source.knowledgeID)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

// complianceRequest 通过实际业务路由发送请求并核对 HTTP 状态。
// 参数 t 为测试上下文，router、method、path 和 body 组成业务请求，want 为预期状态码。
// 返回值 解码的业务响应。
func complianceRequest[T any](t *testing.T, router http.Handler, method, path string, body map[string]any, want int) T {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	result := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, &payload)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(result, request)
	if result.Code != want {
		t.Fatalf("HTTP %d, want %d: %s", result.Code, want, result.Body.String())
	}
	var value T
	if err := json.Unmarshal(result.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

// complianceFixture 只在专用测试数据库中应用全量迁移并注册真实 HTTP 接口。
// 参数 t 为测试上下文，分配独立模式并注册清理。
// 返回值 隔离连接池和实际业务路由。
func complianceFixture(t *testing.T) (*pgxpool.Pool, http.Handler) {
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
	schema := fmt.Sprintf("compliance_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
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
	mux := http.NewServeMux()
	New(pool, "合规测试席位").Register(mux)
	return pool, mux
}
