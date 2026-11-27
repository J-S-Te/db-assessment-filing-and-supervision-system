package materialexpiry

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/internal/cases"
)

// TestReportExpiryOptionsUseExactReport 核对正式候选只属于指定已审批报告。
// 参数 t 为使用独立数据库模式的测试上下文。
// 返回值 无。
func TestReportExpiryOptionsUseExactReport(t *testing.T) {
	db, files := expiryFixture(t)
	filing, _, _ := seedCaseMaterial(t, db, files)
	report, materials := seedExpiryReport(t, db, files, filing, "EXPIRY-R1", "approved")
	seedExpiryReport(t, db, files, filing, "EXPIRY-R2", "approved")
	pending, _ := seedExpiryReport(t, db, files, filing, "EXPIRY-R3", "pending_final")
	router := http.NewServeMux()
	New(db, files, "有效期测试监管").Register(router)
	path := candidateURL + "?reportId=" + report
	items := expiryCall(t, router, "GET", path, nil, 200)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("报告原件候选应为结论和正文，实际为 %v", items)
	}
	for _, entry := range items {
		item := entry.(map[string]any)
		kind := item["attachmentKind"].(string)
		if item["caseId"] != report || kind == "remediation_plan" || item["attachmentId"] != materials[kind] {
			t.Fatalf("候选超出当前报告的测评材料范围：%v", item)
		}
	}
	for _, id := range []string{filing, pending} {
		wantEmptyExpiryPage(t, router, candidateURL+"?reportId="+id)
	}
	expiryCall(t, router, "GET", path+"&caseKind=filing", nil, 400)
	for _, invalid := range []string{"reportId=", "reportId=invalid",
		"reportId=" + strings.ReplaceAll(report, "-", "x"), "reportId=" + report + "&reportId=" + report} {
		expiryCall(t, router, "GET", candidateURL+"?"+invalid, nil, 400)
		expiryCall(t, router, "GET", testRegulatorPath+"?"+invalid, nil, 400)
	}
}

// TestReportExpiryListPreservesRecipients 核对报告筛选、日期登记和原有收件范围。
// 参数 t 为使用独立数据库模式的测试上下文。
// 返回值 无。
func TestReportExpiryListPreservesRecipients(t *testing.T) {
	db, files := expiryFixture(t)
	filing, _, today := seedCaseMaterial(t, db, files)
	report, first := seedExpiryReport(t, db, files, filing, "EXPIRY-R1", "approved")
	_, second := seedExpiryReport(t, db, files, filing, "EXPIRY-R2", "approved")
	service := New(db, files, "有效期测试监管")
	router := http.NewServeMux()
	service.Register(router)
	result := registerExpiryReportMaterial(t, router, first["assessment_conclusion"], today, "021")
	registerExpiryReportMaterial(t, router, first["remediation_plan"], today, "022")
	registerExpiryReportMaterial(t, router, second["report_file"], today, "023")
	id := result["id"].(string)
	query := "?reportId=" + report
	wantEmptyExpiryPage(t, router, candidateURL+query+"&q="+url.QueryEscape("等级测评结论"))
	for _, path := range []string{
		testRegulatorPath + query,
		"/api/v1/operator/material-expiries" + query + "&organization=" + url.QueryEscape(testOrganization),
		"/api/v1/institution/material-expiries" + query + "&institution=" + url.QueryEscape(testInstitution),
	} {
		items := expiryCall(t, router, "GET", path+"&status=due", nil, 200)["items"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["id"] != id {
			t.Fatalf("台账超出终审报告的测评材料范围：%v", items)
		}
	}
	wantEmptyExpiryPage(t, router, "/api/v1/operator/material-expiries"+query+"&organization=other")
	wantEmptyExpiryPage(t, router, "/api/v1/institution/material-expiries"+query+"&institution=other")
	assertNoticeCount(t, db, id, "material_due", 2)
	if count, err := service.Sweep(t.Context()); err != nil || count != 0 {
		t.Fatalf("已发送的测评材料临期消息被重复投递：%d %v", count, err)
	}
}

// TestReportExpirySweepSendsExpiredAfterDue 验证同一周期内两次提醒可分别送达。
// 参数 t 为使用独立数据库模式的测试上下文。
// 返回值 无。
func TestReportExpirySweepSendsExpiredAfterDue(t *testing.T) {
	db, files := expiryFixture(t)
	filing, _, today := seedCaseMaterial(t, db, files)
	_, materials := seedExpiryReport(t, db, files, filing, "EXPIRY-R1", "approved")
	service := New(db, files, "有效期测试监管")
	router := http.NewServeMux()
	service.Register(router)
	result := registerExpiryReportMaterial(t, router, materials["assessment_conclusion"], today, "024")
	id := result["id"].(string)
	assertAssessmentNotices(t, db, id, "material_due", "测评材料有效期即将结束")
	if _, err := db.Exec(t.Context(), `UPDATE material_expiry_records
		SET valid_until=valid_until-1 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if count, err := service.Sweep(t.Context()); err != nil || count != 2 {
		t.Fatalf("临期通知之后未发送两条实际到期通知：%d %v", count, err)
	}
	assertAssessmentNotices(t, db, id, "material_expired", "测评材料有效期已届满")
	if count, err := service.Sweep(t.Context()); err != nil || count != 0 {
		t.Fatalf("重复扫描产生已到期消息：%d %v", count, err)
	}
}

// assertAssessmentNotices 验证测评站内通知的两端接收方和业务文案。
// 参数 t、db 为测试上下文和专用库，id、state、prefix 为消息来源、类型和文案起始文本。
// 返回值 无。
func assertAssessmentNotices(t *testing.T, db *pgxpool.Pool, id, state, prefix string) {
	t.Helper()
	assertNoticeCount(t, db, id, state, 2)
	var count int
	err := db.QueryRow(t.Context(), `SELECT count(*) FROM review_notices
		WHERE resource='material-expiries' AND resource_id=$1 AND state=$2 AND starts_with(comment,$3)
		AND ((audience='operator' AND recipient=$4) OR (audience='institution' AND recipient=$5))`,
		id, state, prefix, testOrganization, testInstitution).Scan(&count)
	if err != nil || count != 2 {
		t.Errorf("%s 的文案或收件人不符合测评业务：%d %v", state, count, err)
	}
}

// seedExpiryReport 为同一备案建立指定状态的报告和真实附件文件。
// 参数 t、db、files 为测试依赖，filing、number、status 为备案、报告号和审核状态。
// 返回值 报告标识及按原件类别索引的附件标识。
func seedExpiryReport(t *testing.T, db *pgxpool.Pool, files *cases.Service,
	filing, number, status string,
) (string, map[string]string) {
	t.Helper()
	var report string
	err := db.QueryRow(t.Context(), `INSERT INTO business_cases
		(kind,number,organization_name,assessment_org,filing_id,risk,status)
		VALUES('report',$1,$2,$3,$4,'low',$5) RETURNING id::text`,
		number, testOrganization, testInstitution, filing, status).Scan(&report)
	if err != nil {
		t.Fatal(err)
	}
	attachments := make(map[string]string)
	for kind, label := range map[string]string{
		"assessment_conclusion": "等级测评结论", "report_file": "测评报告正文", "remediation_plan": "整改规划",
	} {
		file, key, _, err := files.StagePublicationAttachment(label+".pdf", strings.NewReader(testPDF))
		if err != nil {
			t.Fatal(err)
		}
		var id string
		err = db.QueryRow(t.Context(), `INSERT INTO case_attachments
			(case_id,kind,file_name,file_key,size_bytes) VALUES($1,$2,$3,$4,$5) RETURNING id::text`,
			report, kind, file.FileName, key, file.Size).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		attachments[kind] = id
	}
	return report, attachments
}

// registerExpiryReportMaterial 按明确原件和实际证据登记测试报告材料。
// 参数 t、router 为测试依赖，source、today、suffix 为附件、上海当日和请求号后缀。
// 返回值 登记日期和归属在内的接口响应。
func registerExpiryReportMaterial(t *testing.T, router http.Handler, source, today, suffix string) map[string]any {
	t.Helper()
	return expiryCall(t, router, "POST", testRegulatorPath, map[string]any{
		"requestId": "90000000-0000-4000-8000-000000000" + suffix, "sourceAttachmentId": source,
		"validUntil": today, "evidence": "用于核验的报告明确日期",
	}, 201)
}

// wantEmptyExpiryPage 验证台账和候选筛选响应没有越界的结果。
// 参数 t、router 为测试依赖，path 为待核验读接口。
// 返回值 无。
func wantEmptyExpiryPage(t *testing.T, router http.Handler, path string) {
	t.Helper()
	if items := expiryCall(t, router, "GET", path, nil, 200)["items"].([]any); len(items) != 0 {
		t.Fatalf("%s 应为空，实际为 %v", path, items)
	}
}
