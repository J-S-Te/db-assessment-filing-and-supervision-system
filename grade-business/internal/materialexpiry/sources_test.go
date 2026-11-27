package materialexpiry

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const candidateURL = testRegulatorPath + "/source-options"

// TestSourceOptionsContinueAtSameUploadTime 核对同一上传时刻的历史材料无遗漏和重复。
// 参数 t 为使用独立数据库模式的测试上下文。
// 返回值 无。
func TestSourceOptionsContinueAtSameUploadTime(t *testing.T) {
	db, files := expiryFixture(t)
	_, err := db.Exec(t.Context(), `WITH filings AS (
	 INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status)
	 SELECT 'filing','PAGE-'||n,'系统','单位','机构','3','approved' FROM generate_series(1,21) n
	 RETURNING id,number
	) INSERT INTO case_attachments(id,case_id,kind,file_name,file_key,size_bytes,uploaded_at)
	 SELECT ('10000000-0000-4000-8000-'||lpad(split_part(number,'-',2),12,'0'))::uuid,
	 id,'filing_form','分页原件.pdf',number,25,'2023-01-01T00:00:00Z' FROM filings`)
	if err != nil {
		t.Fatal(err)
	}
	router := http.NewServeMux()
	New(db, files, "有效期测试监管").Register(router)
	first := expiryCall(t, router, "GET", candidateURL, nil, 200)
	items, next := first["items"].([]any), first["nextBefore"]
	if len(items) != 20 || next == nil || items[19].(map[string]any)["number"] != "PAGE-2" {
		t.Fatalf("首屏缺少历史续页位置或顺序错误：%d %v", len(items), next)
	}
	second := expiryCall(t, router, "GET", candidateURL+"?before="+url.QueryEscape(next.(string)), nil, 200)
	last := second["items"].([]any)
	if len(last) != 1 || last[0].(map[string]any)["number"] != "PAGE-1" || second["nextBefore"] != nil {
		t.Fatalf("末页历史原件遗漏或重复：%v", second)
	}
	invalidQueries := []string{"before=invalid", "before=", "before=bad&before=bad", "caseKind=unknown", "caseKind="}
	for _, query := range invalidQueries {
		expiryCall(t, router, "GET", candidateURL+"?"+query, nil, 400)
	}
}

// TestSourceOptionsRequireCurrentApprovedOriginal 验证正式原件分类、最新版本和报告归属的边界。
// 参数 t 为使用独立数据库模式的测试上下文。
// 返回值 无。
func TestSourceOptionsRequireCurrentApprovedOriginal(t *testing.T) {
	db, files := expiryFixture(t)
	filing, previous, today := seedCaseMaterial(t, db, files)
	file, key, _, err := files.StagePublicationAttachment("现行原件.pdf", strings.NewReader(testPDF))
	if err != nil {
		t.Fatal(err)
	}
	var current, zipped, foreign string
	err = db.QueryRow(t.Context(), `WITH report AS (
	 INSERT INTO business_cases(kind,number,organization_name,assessment_org,filing_id,risk,status)
	 VALUES('report','R-FOREIGN','不一致报告单位',$2,$1,'low','approved') RETURNING id
	), attached AS (
	 INSERT INTO case_attachments(case_id,kind,file_name,file_key,size_bytes)
	 VALUES($1,'filing_form',$3,$4,$5),($1,'filing_package','汇总包.zip','zip-fixture',25),
	 ((SELECT id FROM report),'report_file','归属不一致报告.pdf','report-fixture',25)
	 RETURNING id,kind
	) SELECT (SELECT id::text FROM attached WHERE kind='filing_form'),
	 (SELECT id::text FROM attached WHERE kind='filing_package'),
	 (SELECT id::text FROM attached WHERE kind='report_file')`,
		filing, testInstitution, file.FileName, key, file.Size).Scan(&current, &zipped, &foreign)
	if err != nil {
		t.Fatal(err)
	}
	router := http.NewServeMux()
	New(db, files, "有效期测试监管").Register(router)
	items := expiryCall(t, router, "GET", candidateURL, nil, 200)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["attachmentId"] != current {
		t.Fatalf("候选包含已替换、不可登记分类或错属材料：%v", items)
	}
	for _, invalid := range []string{previous, zipped, foreign} {
		response := expiryCall(t, router, "POST", testRegulatorPath, map[string]any{
			"requestId": "90000000-0000-4000-8000-000000000046", "sourceAttachmentId": invalid,
			"validUntil": today, "evidence": "用于核对不适用原件的测试输入",
		}, 409)
		if response["code"] != "material_expiry_scope_changed" {
			t.Fatalf("错误来源未被材料范围规则拒绝：%v", response)
		}
	}
}

// TestSourceOptionsFilterAndHideRegisteredMaterial 核对候选检索和登记后去重。
// 参数 t 为使用独立数据库模式的测试上下文。
// 返回值 无。
func TestSourceOptionsFilterAndHideRegisteredMaterial(t *testing.T) {
	db, files := expiryFixture(t)
	filing, source, today := seedCaseMaterial(t, db, files)
	router := http.NewServeMux()
	New(db, files, "有效期测试监管").Register(router)
	reports := expiryCall(t, router, "GET", candidateURL+"?caseKind=report", nil, 200)
	if len(reports["items"].([]any)) != 0 {
		t.Fatalf("报告筛选暴露备案原件：%v", reports)
	}
	path := candidateURL + "?caseKind=filing&q=" + url.QueryEscape("备案表.pdf")
	items := expiryCall(t, router, "GET", path, nil, 200)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["attachmentId"] != source ||
		items[0].(map[string]any)["caseId"] != filing {
		t.Fatalf("未按实际原件文件名定位唯一来源：%v", items)
	}
	files.Register(router)
	assertExpiryDownload(t, router, "/api/v1/filings/"+filing+"/attachments/"+source, 200)
	payload := map[string]any{"requestId": "90000000-0000-4000-8000-000000000041",
		"sourceAttachmentId": source, "validUntil": today, "evidence": "文件所载核定日期"}
	expiryCall(t, router, "POST", testRegulatorPath, payload, 201)
	if rows := expiryCall(t, router, "GET", path, nil, 200)["items"].([]any); len(rows) != 0 {
		t.Fatalf("已登记材料仍被列为候选：%v", rows)
	}
}
