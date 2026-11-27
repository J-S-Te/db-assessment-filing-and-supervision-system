package cases

import (
	"net/http"
	"net/url"
	"sync"
	"testing"
)

const feedbackURL = "/api/v1/institution/report-feedback"
const institutionReportsURL = "/api/v1/institution/reports/"

// TestInstitutionReportKeepsOriginalAmongManyFiles 验证历史报告正文不会被后续配套材料挤出详情。
// 参数 t 为仅使用隔离 PostgreSQL 的测试上下文。
// 返回值 无。
func TestInstitutionReportKeepsOriginalAmongManyFiles(t *testing.T) {
	service, handler := fixtureService(t)
	id, version := pendingFeedbackReport(t, handler)
	path := reportsURL + "/" + id
	readResponse[mutation](t, reviewRequest(handler, path, returnForFix, "补齐佐证", version), 200)
	_, err := service.db.Exec(t.Context(), `
		INSERT INTO case_attachments(case_id,kind,file_name,file_key,size_bytes,uploaded_at)
		SELECT $1,'supporting','配套.pdf',gen_random_uuid()::text,64,clock_timestamp()
		FROM generate_series(1,30)`, id)
	if err != nil {
		t.Fatal(err)
	}
	scope := "?institution=" + url.QueryEscape("测试测评机构")
	item := readResponse[institutionReport](t,
		requestJSON(handler, "GET", institutionReportsURL+id+scope, nil), 200)
	for _, file := range item.Attachments {
		if file.Kind == "report_file" && item.CanCorrect && len(item.Attachments) == 30 {
			return
		}
	}
	t.Fatal("符合旧提交规则的报告详情必须保留正文")
}

// TestReportFeedbackAndCorrection 验证意见投递、签收、归属及补正重提。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestReportFeedbackAndCorrection(t *testing.T) {
	_, handler := fixtureService(t)
	id, version := pendingFeedbackReport(t, handler)
	reportPath := reportsURL + "/" + id
	own := "?institution=" + url.QueryEscape("测试测评机构")
	other := "?institution=" + url.QueryEscape("另一家机构")
	returnedRow := readResponse[mutation](t, reviewRequest(handler, reportPath, returnForFix, "补传测评佐证", version), 200)
	inbox := readResponse[struct {
		Items []map[string]any `json:"items"`
	}](t, requestJSON(handler, "GET", feedbackURL+own, nil), 200)
	if len(inbox.Items) != 1 || inbox.Items[0]["decision"] != "returned" ||
		inbox.Items[0]["comment"] != "补传测评佐证" || inbox.Items[0]["status"] != "returned" {
		t.Fatalf("机构未收到退回意见: %+v", inbox)
	}
	message := inbox.Items[0]["id"].(string)
	assertFeedbackReceipt(t, handler, message, own, other, reportPath)
	assertError(t, requestJSON(handler, "GET", feedbackURL, nil), 400, "invalid_input")
	assertError(t, requestJSON(handler, "GET", feedbackURL+own+"&before=-1", nil), 400, "invalid_input")
	assertError(t, requestJSON(handler, "GET", institutionReportsURL+id+other, nil), 404, "not_found")
	assertError(t, requestJSON(handler, "POST", institutionReportsURL+id+"/submit"+other,
		versionInput{returnedRow.Version}), 404, "not_found")
	material := postMaterialAt(t, handler, institutionReportsURL+id+"/attachments"+own, "supporting")
	assertError(t, requestJSON(handler, "GET", institutionReportsURL+id+"/attachments/"+material.ID+other, nil),
		404, "not_found")
	readResponse[mutation](t, requestJSON(handler, "POST", institutionReportsURL+id+"/submit"+own,
		versionInput{returnedRow.Version + 1}), 200)
	assertFeedbackApproval(t, handler, reportPath, message, own)
}

// TestFeedbackFailureRollsBackReportReview 验证消息写入失败不会产生孤立审核状态及留痕。
// 参数 t 为只使用隔离 PostgreSQL schema 的测试上下文。
// 返回值 无。
func TestFeedbackFailureRollsBackReportReview(t *testing.T) {
	service, handler := fixtureService(t)
	id, version := pendingFeedbackReport(t, handler)
	if _, err := service.db.Exec(t.Context(), "ALTER TABLE report_feedback ADD CHECK (false)"); err != nil {
		t.Fatal(err)
	}
	path := reportsURL + "/" + id
	assertError(t, reviewRequest(handler, path, returnForFix, "等待重新审核", version), 500, "internal_error")
	result := readResponse[recordDetail](t, requestJSON(handler, "GET", path, nil), 200)
	events := readResponse[itemResponse[reviewEvent]](t, requestJSON(handler, "GET", path+"/review-history", nil), 200)
	if result.Status != pendingInitial || result.Version != version || len(events.Items) != 1 {
		t.Fatalf("审核写入回传失败后状态或留痕未回滚: %+v %+v", result, events)
	}
}

// TestReturnedFeedbackFollowsReportReassignment 验证改派后新机构收到当前意见，原回执保留。
// 参数 t 为只使用隔离 PostgreSQL schema 的测试上下文。
// 返回值 无。
func TestReturnedFeedbackFollowsReportReassignment(t *testing.T) {
	_, handler := fixtureService(t)
	id, version := pendingFeedbackReport(t, handler)
	path := reportsURL + "/" + id
	const reason = "核对补正附件"
	readResponse[mutation](t, reviewRequest(handler, path, returnForFix, reason, version), 200)
	oldScope := "?institution=" + url.QueryEscape("测试测评机构")
	old := readResponse[itemResponse[feedbackInboxItem]](t, requestJSON(handler, "GET", feedbackURL+oldScope, nil), 200)
	oldMessage := old.Items[0].ID
	receiptPath := feedbackURL + "/" + oldMessage + "/acknowledge" + oldScope
	oldReceipt := readResponse[feedbackReceipt](t, requestJSON(handler, "POST", receiptPath, nil), 200)
	current := readResponse[recordDetail](t, requestJSON(handler, "GET", path, nil), 200)
	newInstitution := "新测评\t机构"
	translated := reportInput{Number: current.Number, FilingID: current.FilingID,
		AssessmentOrg: newInstitution, Risk: current.Risk}
	input := struct {
		reportInput
		versionInput
	}{translated, versionInput{current.Version}}
	readResponse[mutation](t, requestJSON(handler, "PATCH", path, input), 200)
	newScope := "?institution=" + url.QueryEscape(newInstitution)
	next := readResponse[itemResponse[feedbackInboxItem]](t,
		requestJSON(handler, "GET", feedbackURL+newScope, nil), 200)
	if len(next.Items) != 1 || next.Items[0].Comment != reason || next.Items[0].AcknowledgedAt != nil {
		t.Fatalf("新机构没有收到待签收的当前补正意见: %+v", next)
	}
	newDetail := readResponse[institutionReport](t, requestJSON(handler, "GET", institutionReportsURL+id+newScope, nil), 200)
	if !newDetail.CanCorrect || newDetail.ReturnComment == nil || *newDetail.ReturnComment != reason {
		t.Fatalf("新机构不能进入有当前意见的补正流程: %+v", newDetail)
	}
	old = readResponse[itemResponse[feedbackInboxItem]](t, requestJSON(handler, "GET", feedbackURL+oldScope, nil), 200)
	if len(old.Items) != 1 || old.Items[0].Status != nil || old.Items[0].AcknowledgedAt == nil ||
		!oldReceipt.AcknowledgedAt.Equal(*old.Items[0].AcknowledgedAt) {
		t.Fatalf("原回执或改派隔离不正确: %+v", old)
	}
	assertError(t, requestJSON(handler, "GET", institutionReportsURL+id+oldScope, nil), 404, "not_found")
}

// pendingFeedbackReport 创建已送初审的测评测试报告。
// 参数 t 为测试上下文，handler 为已装配的业务路由。
// 返回值 报告标识和已提交的版本。
func pendingFeedbackReport(t *testing.T, handler http.Handler) (string, int64) {
	t.Helper()
	filing := createFiling(t, handler, "BA-FEEDBACK")
	row := readResponse[mutation](t, requestJSON(handler, "POST", reportsURL,
		reportInput{Number: "BG-FEEDBACK", FilingID: filing.ID, AssessmentOrg: "测试测评机构", Risk: "high"}), 201)
	path := reportsURL + "/" + row.ID
	postMaterial(t, handler, path, "report_file")
	result := readResponse[mutation](t, requestJSON(handler, "POST", path+"/submit", versionInput{2}), 200)
	return result.ID, result.Version
}

// assertFeedbackReceipt 验证不同机构的消息范围和并发重复签收。
// 参数 t 为测试上下文，handler 为业务路由，message、own、other 与 path 指定消息和收件范围。
// 返回值 无。
func assertFeedbackReceipt(t *testing.T, handler http.Handler, message, own, other, path string) {
	t.Helper()
	ack := feedbackURL + "/" + message + "/acknowledge"
	assertError(t, requestJSON(handler, "POST", ack+other, nil), 404, "not_found")
	assertError(t, requestJSON(handler, "POST", ack+own, map[string]string{"note": "额外字段"}), 400, "invalid_input")
	var waits sync.WaitGroup
	results := make(chan string, 2)
	for range 2 {
		waits.Go(func() {
			response := requestJSON(handler, "POST", ack+own, nil)
			if response.Code != 200 {
				t.Errorf("签收返回 %d: %s", response.Code, response.Body.String())
			}
			results <- response.Body.String()
		})
	}
	waits.Wait()
	if first, second := <-results, <-results; first != second {
		t.Fatalf("重复签收变更了回执: %s / %s", first, second)
	}
	deliveries := readResponse[struct {
		Items []map[string]any `json:"items"`
	}](t, requestJSON(handler, "GET", path+"/feedback", nil), 200)
	if len(deliveries.Items) != 1 || deliveries.Items[0]["acknowledgedAt"] == nil {
		t.Fatalf("监管端无法确认签收: %+v", deliveries)
	}
}

// assertFeedbackApproval 核对补正重提后的状态和最终意见。
// 参数 t 为测试上下文，handler 为业务路由，path、firstMessage、own 指定报告及收件范围。
// 返回值 无。
func assertFeedbackApproval(t *testing.T, handler http.Handler, path, firstMessage, own string) {
	t.Helper()
	row := readResponse[recordDetail](t, requestJSON(handler, "GET", path, nil), 200)
	if row.Status != pendingInitial {
		t.Fatalf("补正未回到初审: %+v", row)
	}
	final := readResponse[mutation](t, reviewRequest(handler, path, approve, "", row.Version), 200)
	readResponse[mutation](t, reviewRequest(handler, path, approve, "确认通过", final.Version), 200)
	result := readResponse[struct {
		Items []map[string]any `json:"items"`
	}](t, requestJSON(handler, "GET", feedbackURL+own, nil), 200)
	if len(result.Items) != 2 || result.Items[0]["decision"] != "approved" ||
		result.Items[1]["id"] != firstMessage || result.Items[1]["status"] != "approved" {
		t.Fatalf("终审结果与历史意见不同步: %+v", result)
	}
}
