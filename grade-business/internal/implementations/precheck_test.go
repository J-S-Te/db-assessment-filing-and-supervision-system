package implementations

import (
	"archive/zip"
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/internal/cases"
)

type checkedImplementation struct {
	ReportVersion    int64
	FilingVersion    *int64
	MaterialRevision *int64
	Findings         []struct {
		RuleID   string
		Status   string
		Evidence *struct{ AttachmentID, FileName string }
	}
}

// TestImplementationMaterialPrecheck 验证预检读取当前备案材料、按分类去重并拒绝旧报备版本。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestImplementationMaterialPrecheck(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(pool)
	filing := implementationTestFiling(t, pool, "PRECHECK-MATERIAL", approved)
	other := implementationTestFiling(t, pool, "PRECHECK-OTHER", approved)
	form := testInput(filing, routine)
	record := decodeResponse[mutation](t, callImplementation(mux, "POST", path, form), 201)
	target := fmt.Sprintf("%s/%s/precheck?expectedVersion=%d", path, record.ID, record.Version)
	addImplementationPrecheckMaterial(t, pool, filing, "filing_form", "旧备案表.pdf")
	selected := addImplementationPrecheckMaterial(t, pool, filing, "filing_form", "新备案表.pdf")
	addImplementationPrecheckMaterial(t, pool, other, "classification_report", "他方定级报告.pdf")
	result := decodeResponse[checkedImplementation](t, callImplementationPrecheck(mux, target, nil), 200)
	if result.ReportVersion != record.Version || result.FilingVersion == nil || *result.FilingVersion != 1 ||
		result.MaterialRevision == nil || *result.MaterialRevision != 2 {
		t.Fatalf("预检未绑定当前版本：%+v", result)
	}
	assertImplementationFinding(t, result, "filing_form", "pass", selected)
	assertImplementationFinding(t, result, "classification_report", "warning", "")
	latest := addImplementationPrecheckMaterial(t, pool, filing, "classification_report", "本案定级报告.pdf")
	result = decodeResponse[checkedImplementation](t, callImplementationPrecheck(mux, target, nil), 200)
	assertImplementationFinding(t, result, "classification_report", "pass", latest)
	if *result.MaterialRevision != 3 {
		t.Fatalf("材料更新后仍出现旧结果：%+v", result)
	}
	current := decodeResponse[detail](t, callImplementation(mux, "GET", path+"/"+record.ID, nil), 200)
	history := decodeResponse[eventPage](t, callImplementation(mux, "GET", path+"/"+record.ID+"/history", nil), 200)
	if current.Version != record.Version || current.Status != record.Status || len(history.Items) != 1 {
		t.Fatalf("预检写入了状态或历史：%+v %+v", current, history)
	}
	if !current.CanPrecheck {
		t.Fatal("监管详情应提供预检入口")
	}
	form.Plan = "补充实施计划"
	decodeResponse[mutation](t, callImplementation(mux, "PATCH", path+"/"+record.ID,
		patchInput{input: form, ExpectedVersion: record.Version}), 200)
	assertImplementationPrecheckError(t, mux, target, nil, 409, "stale_version")
}

const implementationPrecheckPDF = "%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n%%EOF\n"

// TestImplementationPrecheckAcceptsValidatedFilingPackage 验证正式上传的备案包可作为两类必传材料的证据。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestImplementationPrecheckAcceptsValidatedFilingPackage(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationPrecheckMaterialMux(t, pool)
	filing := implementationTestFiling(t, pool, "PRECHECK-PACKAGE", draft)
	filingPath := "/api/v1/institution/filings/" + filing + "/attachments"
	archive := uploadImplementationPrecheckMaterial(t, mux, filingPath+firstImplementationScope(),
		"filing_package", "备案材料包.zip", implementationFilingPackage(t))
	form := uploadImplementationPrecheckMaterial(t, mux, filingPath+firstImplementationScope(),
		"filing_form", "独立备案表.pdf", []byte(implementationPrecheckPDF))
	setOperatorFilingField(t, pool, `UPDATE business_cases SET status='approved' WHERE id=$1`, filing)
	record := createInstitutionImplementation(t, mux, testInput(filing, routine))
	target := institutionalPath + "/" + record.ID + "/precheck" + firstImplementationScope() + "&expectedVersion=1"
	result := decodeResponse[checkedImplementation](t, callImplementationPrecheck(mux, target, nil), 200)
	assertImplementationFinding(t, result, "filing_form", "pass", form.ID)
	assertImplementationFinding(t, result, "classification_report", "pass", archive.ID)
	response := callImplementationPrecheck(mux, filingPath+"/"+archive.ID+firstImplementationScope(), nil)
	if response.Code != http.StatusOK || response.Body.Len() != int(archive.Size) {
		t.Fatalf("引用备案包无法下载：%d", response.Code)
	}
}

// TestImplementationPrecheckRespectsAttachmentHistoryScope 验证归并历史材料证据遵循原件下载归属。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestImplementationPrecheckRespectsAttachmentHistoryScope(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationPrecheckMaterialMux(t, pool)
	filing := implementationTestFiling(t, pool, "PRECHECK-MERGED-MATERIAL", draft)
	filingPath := "/api/v1/filings/" + filing + "/attachments"
	sourceReport, sourceForm, currentForm := implementationPrecheckSourceMaterials(t, mux, filingPath)
	setOperatorFilingField(t, pool, `UPDATE business_cases SET status='approved' WHERE id=$1`, filing)
	if _, err := pool.Exec(t.Context(), `UPDATE case_attachments SET merge_history_scope='原单位'
		WHERE id IN ($1,$2)`, sourceReport, sourceForm); err != nil {
		t.Fatal(err)
	}
	form := testInput(filing, change)
	form.AssessmentOrg = replacementInstitution
	record := createOperatorImplementation(t, mux, form)
	scope := operatorImplementationScope(operatorTestOrganization)
	target := operatorPath + "/" + record.ID + "/precheck" + scope + "&expectedVersion=1"
	result := decodeResponse[checkedImplementation](t, callImplementationPrecheck(mux, target, nil), 200)
	assertImplementationFinding(t, result, "classification_report", "warning", "")
	operatorMaterial := "/api/v1/operator/filings/" + filing + "/attachments/" + sourceReport
	if response := callImplementationPrecheck(mux, operatorMaterial+scope, nil); response.Code != http.StatusNotFound {
		t.Fatalf("归并目标单位访问历史原件的返回状态 = %d", response.Code)
	}
	if response := callImplementationPrecheck(mux, operatorMaterial+operatorImplementationScope("原单位"), nil); response.Code != http.StatusOK {
		t.Fatalf("归并来源单位访问历史原件的返回状态 = %d", response.Code)
	}
	assertImplementationFinding(t, result, "filing_form", "pass", currentForm)
}

// implementationPrecheckSourceMaterials 上传当前归属和来源历史的合成原件。
// 参数 t、mux 为测试上下文和真实路由，filingPath 为备案材料地址。
// 返回值 按历史定级报告、历史备案表、当前备案表排序的附件标识。
func implementationPrecheckSourceMaterials(t *testing.T, mux *http.ServeMux, filingPath string) (string, string, string) {
	t.Helper()
	pdf := []byte(implementationPrecheckPDF)
	current := uploadImplementationPrecheckMaterial(t, mux, filingPath, "filing_form", "当前备案表.pdf", pdf)
	sourceForm := uploadImplementationPrecheckMaterial(t, mux, filingPath, "filing_form", "原单位备案表.pdf", pdf)
	sourceReport := uploadImplementationPrecheckMaterial(t, mux, filingPath, "classification_report", "原单位定级报告.pdf", pdf)
	return sourceReport.ID, sourceForm.ID, current.ID
}

// implementationPrecheckMaterialMux 注册实施预检和正式附件的隔离测试路由。
// 参数 t 为测试生命周期，pool 为隔离的数据库连接池。
// 返回值 共用同一测试库的实施报备、备案上传和下载路由。
func implementationPrecheckMaterialMux(t *testing.T, pool *pgxpool.Pool) *http.ServeMux {
	t.Helper()
	mux := operatorImplementationTestMux(pool)
	filings, err := cases.New(pool, t.TempDir(), "测试办理人")
	if err != nil {
		t.Fatal(err)
	}
	filings.Register(mux)
	filings.RegisterOperatorFilingRoutes(mux)
	return mux
}

// implementationFilingPackage 构造可由实际上传路由校验的最小备案 ZIP。
// 参数 t 为测试上下文。
// 返回值 包含备案表与定级报告的 ZIP 内容。
func implementationFilingPackage(t *testing.T) []byte {
	t.Helper()
	var content bytes.Buffer
	archive := zip.NewWriter(&content)
	for _, name := range []string{"备案表.pdf", "定级报告.pdf"} {
		file, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(implementationPrecheckPDF)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return content.Bytes()
}

// uploadImplementationPrecheckMaterial 通过正式备案材料接口上传证据。
// 参数 t、mux 为测试上下文和路由，target、kind、name、payload 指定上传对象及内容。
// 返回值 接口确认登记后的备案附件。
func uploadImplementationPrecheckMaterial(t *testing.T, mux *http.ServeMux, target, kind, name string,
	payload []byte,
) cases.Attachment {
	t.Helper()
	var content bytes.Buffer
	form := multipart.NewWriter(&content)
	if err := form.WriteField("kind", kind); err != nil {
		t.Fatal(err)
	}
	part, err := form.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, target, &content)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return decodeResponse[cases.Attachment](t, response, http.StatusCreated)
}

// TestImplementationPrecheckScopes 验证预检只向发起方和监管开放，并校验备案材料的独立归属。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestImplementationPrecheckScopes(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := operatorImplementationTestMux(pool)
	filing := implementationTestFiling(t, pool, "PRECHECK-SCOPE", approved)
	evidence := addImplementationPrecheckMaterial(t, pool, filing, "filing_form", "备案表.pdf")
	form := testInput(filing, routine)
	institutionReport := createInstitutionImplementation(t, mux, form)
	institutionTarget := institutionalPath + "/" + institutionReport.ID + "/precheck" + firstImplementationScope()
	institutionTarget += "&expectedVersion=1"
	operatorScope := operatorImplementationScope(operatorTestOrganization) + "&expectedVersion=1"
	visible := decodeResponse[detail](t, callImplementation(mux, "GET", operatorPath+"/"+institutionReport.ID+
		operatorImplementationScope(operatorTestOrganization), nil), 200)
	if visible.CanPrecheck {
		t.Fatalf("非发起方详情展示了预检入口：%+v", visible)
	}
	assertImplementationPrecheckError(t, mux, operatorPath+"/"+institutionReport.ID+"/precheck"+operatorScope,
		nil, 404, "not_found")
	result := decodeResponse[checkedImplementation](t, callImplementationPrecheck(mux, institutionTarget, nil), 200)
	assertImplementationFinding(t, result, "filing_form", "pass", evidence)
	form.Type, form.AssessmentOrg = change, replacementInstitution
	operatorReport := createOperatorImplementation(t, mux, form)
	operatorTarget := operatorPath + "/" + operatorReport.ID + "/precheck" + operatorScope
	result = decodeResponse[checkedImplementation](t, callImplementationPrecheck(mux, operatorTarget, nil), 200)
	assertImplementationFinding(t, result, "filing_form", "pass", evidence)
	assertImplementationPrecheckError(t, mux, institutionalPath+"/"+operatorReport.ID+"/precheck?expectedVersion=1&institution="+replacementInstitution,
		nil, 404, "not_found")
	setOperatorFilingField(t, pool, "UPDATE business_cases SET assessment_org='其他测评机构' WHERE id=$1", filing)
	result = decodeResponse[checkedImplementation](t, callImplementationPrecheck(mux, institutionTarget, nil), 200)
	if result.FilingVersion != nil || result.MaterialRevision != nil {
		t.Fatalf("返回了外部机构备案版本：%+v", result)
	}
	assertImplementationFinding(t, result, "filing_materials", "unavailable", "")
	assertImplementationFinding(t, result, "filing_registration", "unavailable", "")
	for _, finding := range result.Findings {
		if finding.Evidence != nil {
			t.Fatalf("泄漏了外部机构备案证据：%+v", finding)
		}
	}
	assertImplementationPrecheckError(t, mux, operatorPath+"/"+operatorReport.ID+"/precheck"+
		operatorImplementationScope("其他单位")+"&expectedVersion=1", nil, 404, "not_found")
}

// TestImplementationPrecheckInputAndFindings 验证严格查询和历史记录的表单、备案及资质问题。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestImplementationPrecheckInputAndFindings(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(pool)
	filing := implementationTestFiling(t, pool, "PRECHECK-VALIDATION", approved)
	record := decodeResponse[mutation](t, callImplementation(mux, "POST", path, testInput(filing, routine)), 201)
	target := path + "/" + record.ID + "/precheck"
	for _, query := range []string{"", "?expectedVersion=0", "?expectedVersion=xyz",
		"?expectedVersion=1&expectedVersion=1", "?expectedVersion=1&filingId=" + filing} {
		assertImplementationPrecheckError(t, mux, target+query, nil, 400, "validation_error")
	}
	target += "?expectedVersion=1"
	assertImplementationPrecheckError(t, mux, target, map[string]string{"plan": "伪造计划"}, 400, "validation_error")
	setOperatorFilingField(t, pool, "UPDATE business_cases SET system_name='新版系统名',cancelled_at=now(),version=2 WHERE id=$1", filing)
	setOperatorFilingField(t, pool, "UPDATE implementation_reports SET plan='   ',planned_start='10000-01-01',planned_end='10000-01-02' WHERE id=$1", record.ID)
	_, err := pool.Exec(t.Context(), `INSERT INTO directory_parties
		(kind,scope_name,name,code,qualification,valid_until)
		VALUES ('institution','备案测评机构','备案测评机构','DIR-PRECHECK','甲级','2000-01-01')`)
	if err != nil {
		t.Fatal(err)
	}
	result := decodeResponse[checkedImplementation](t, callImplementationPrecheck(mux, target, nil), 200)
	assertImplementationFinding(t, result, "report_fields", "warning", "")
	assertImplementationFinding(t, result, "planned_dates", "warning", "")
	assertImplementationFinding(t, result, "filing_registration", "warning", "")
	assertImplementationFinding(t, result, "filing_identity", "warning", "")
	assertImplementationFinding(t, result, "directory", "warning", "")
}

// TestImplementationPrecheckQuota 验证预检不会消耗额度，已经占用的报备仍使用原份额。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestImplementationPrecheckQuota(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(pool)
	filing := implementationTestFiling(t, pool, "PRECHECK-QUOTA", approved)
	form := testInput(filing, change)
	first := decodeResponse[mutation](t, callImplementation(mux, "POST", path, form), 201)
	second := decodeResponse[mutation](t, callImplementation(mux, "POST", path, form), 201)
	third := decodeResponse[mutation](t, callImplementation(mux, "POST", path, form), 201)
	inspection := decodeResponse[mutation](t, callImplementation(mux, "POST", path, form), 201)
	first = performAction(t, mux, first, "submit", "")
	target := path + "/" + inspection.ID + "/precheck?expectedVersion=1"
	result := decodeResponse[checkedImplementation](t, callImplementationPrecheck(mux, target, nil), 200)
	assertImplementationFinding(t, result, "change_quota", "pass", "")
	assertQuota(t, mux, filing, 1)
	performAction(t, mux, second, "submit", "")
	performAction(t, mux, third, "submit", "")
	result = decodeResponse[checkedImplementation](t, callImplementationPrecheck(mux, target, nil), 200)
	assertImplementationFinding(t, result, "change_quota", "warning", "")
	first = performAction(t, mux, first, "accept", "")
	first = performAction(t, mux, first, "return_for_correction", "请补充计划。")
	target = fmt.Sprintf("%s/%s/precheck?expectedVersion=%d", path, first.ID, first.Version)
	result = decodeResponse[checkedImplementation](t, callImplementationPrecheck(mux, target, nil), 200)
	assertImplementationFinding(t, result, "change_quota", "pass", "")
	assertQuota(t, mux, filing, 3)
}

// addImplementationPrecheckMaterial 在隔离库保存真实附件元数据及相应材料版本。
// 参数 t、pool 为隔离测试上下文，filing、kind、name 为附件归属、材料分类和文件名。
// 返回值 附件公开标识。
func addImplementationPrecheckMaterial(t *testing.T, pool *pgxpool.Pool, filing, kind, name string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(), `WITH revision AS (
		UPDATE business_cases SET material_revision=material_revision+1 WHERE id=$1::uuid RETURNING id
	)
	INSERT INTO case_attachments (case_id,kind,file_name,file_key,size_bytes)
	SELECT id,$2,$3,gen_random_uuid()::text,100 FROM revision RETURNING id::text`, filing, kind, name).
		Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// assertImplementationFinding 核对固定规则的状态及至多一份正确归属的证据。
// 参数 t 为测试上下文，result 为预检结果，rule、status、evidence 为预期规则状态和证据标识。
// 返回值 无。
func assertImplementationFinding(t *testing.T, result checkedImplementation, rule, status, evidence string) {
	t.Helper()
	count := 0
	for _, finding := range result.Findings {
		if finding.RuleID != rule {
			continue
		}
		count++
		if finding.Status != status || (evidence == "" && finding.Evidence != nil) ||
			(evidence != "" && (finding.Evidence == nil || finding.Evidence.AttachmentID != evidence)) {
			t.Fatalf("规则 %s 状态或证据不符：%+v", rule, finding)
		}
	}
	if count != 1 {
		t.Fatalf("规则 %s 出现次数 = %d，结果 %+v", rule, count, result)
	}
}

// assertImplementationPrecheckError 核对无效请求的业务错误，而非路由器缺省错误。
// 参数 t、mux 为测试运行上下文，target、body 为请求，status、code 为预期错误。
// 返回值 无。
func assertImplementationPrecheckError(t *testing.T, mux *http.ServeMux, target string, body any, status int, code string) {
	t.Helper()
	actual := decodeResponse[map[string]string](t, callImplementationPrecheck(mux, target, body), status)
	if actual["code"] != code {
		t.Fatalf("预检错误 = %+v，预期 %s", actual, code)
	}
}

// callImplementationPrecheck 发送正常无请求体查询或专门校验是否拒绝多余请求体。
// 参数 mux 为真实路由，target 为预检路径，body 为可选的无效请求体。
// 返回值 HTTP 响应。
func callImplementationPrecheck(mux *http.ServeMux, target string, body any) *httptest.ResponseRecorder {
	if body != nil {
		return callImplementation(mux, http.MethodGet, target, body)
	}
	output := httptest.NewRecorder()
	mux.ServeHTTP(output, httptest.NewRequest(http.MethodGet, target, nil))
	return output
}
