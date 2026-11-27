package compliance

import (
	"net/http"
	"testing"
)

// TestClauseComparisonPinsVersionAndPreventsDuplicates 验证精确引用、人工比对及版本留痕。
// 参数 t 为隔离业务库和 HTTP 测试上下文。
// 返回值 无。
func TestClauseComparisonPinsVersionAndPreventsDuplicates(t *testing.T) {
	pool, router := complianceFixture(t)
	source := seedComplianceSources(t, pool)
	catalog := "/api/v1/knowledge/" + source.knowledgeID + "/clauses"
	first := map[string]any{"requestId": "a950bce5-3925-4aa0-9579-db466c032f0b", "knowledgeVersion": 1,
		"number": "3.1.2", "expectedVersion": 0, "text": "合成条款甲", "active": true}
	clause := complianceRequest[struct{ Version int }](t, router, http.MethodPost, catalog, first, 201)
	if clause.Version != 1 {
		t.Fatalf("首个条款版本错误：%+v", clause)
	}
	complianceRequest[map[string]any](t, router, http.MethodPost, catalog, first, 201)
	catalogPage := complianceRequest[struct{ Items []map[string]any }](t, router, http.MethodGet, catalog, nil, 200)
	if len(catalogPage.Items) != 1 || catalogPage.Items[0]["number"] != "3.1.2" {
		t.Fatalf("目录应有一个条款：%+v", catalogPage)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE report_extractions SET fields=jsonb_build_array(
		jsonb_build_object('key','risk','original',$1::text,'value',$2::text,'startParagraph',4,'endParagraph',6,
		'startPage',2,'endPage',3,'source','pdf_mixed')) WHERE attachment_id=$3`,
		"《固定条款》第3.1.2条，另有《无关》三。", "《固定条款》第3.1.20条", source.attachmentID); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/reports/" + source.reportID + "/compliance"
	query := "?attachmentId=" + source.attachmentID + "&extractionVersion=2&fieldKey=risk"
	type matches struct {
		Items []struct {
			InOriginal, InCorrected bool
			Clause                  struct{ Number, Text string }
		}
	}
	got := complianceRequest[matches](t, router, http.MethodGet, path+"/clause-candidates"+query, nil, 200)
	if len(got.Items) != 1 || !got.Items[0].InOriginal || got.Items[0].InCorrected ||
		got.Items[0].Clause.Number != "3.1.2" || got.Items[0].Clause.Text != "合成条款甲" {
		t.Fatalf("候选应精确匹配原文编号：%+v", got)
	}
	input := map[string]any{"requestId": "dd82cb56-14d2-4fa7-b46d-018d2dc21cb1", "expectedReportVersion": 3,
		"attachmentId": source.attachmentID, "extractionVersion": 2, "fieldKey": "risk",
		"knowledgeId": source.knowledgeID, "knowledgeVersion": 1, "clauseNumber": "3.1.2", "clauseVersion": 1,
		"decision": "insufficient_evidence", "comment": "人工比对合成证据。"}
	type recorded struct {
		ID     string
		Field  struct{ StartPage, EndPage int }
		Clause struct{ Number, Text string }
	}
	audit := complianceRequest[recorded](t, router, http.MethodPost, path, input, 201)
	if audit.ID == "" || audit.Clause.Text != "合成条款甲" || audit.Field.StartPage != 2 || audit.Field.EndPage != 3 {
		t.Fatalf("条文及 PDF 证据定位应一同固定：%+v", audit)
	}
	first["requestId"], first["expectedVersion"], first["text"], first["active"] =
		"5d3b719e-d67d-4bd1-b816-52083cd2883e", 1, "合成条款乙", false
	complianceRequest[map[string]any](t, router, http.MethodPost, catalog, first, 201)
	got = complianceRequest[matches](t, router, http.MethodGet, path+"/clause-candidates"+query, nil, 200)
	if len(got.Items) != 0 {
		t.Fatalf("停用条款仍成为新候选：%+v", got)
	}
	replay := complianceRequest[recorded](t, router, http.MethodPost, path, input, 201)
	if replay != audit {
		t.Fatalf("重试应返回原记录：%+v != %+v", replay, audit)
	}
	input["comment"] = "重复键用于不同内容"
	complianceRequest[map[string]any](t, router, http.MethodPost, path, input, 409)
	input["requestId"] = "25b685da-3078-49a0-88bc-af744679320e"
	complianceRequest[map[string]any](t, router, http.MethodPost, path, input, 409)
	history := complianceRequest[struct{ Items []recorded }](t, router, http.MethodGet, path, nil, 200)
	if len(history.Items) != 1 || history.Items[0] != audit {
		t.Fatalf("条款变更后历史快照应完整保留：%+v", history)
	}
}
