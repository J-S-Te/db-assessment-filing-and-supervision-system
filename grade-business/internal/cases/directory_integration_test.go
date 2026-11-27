package cases

import (
	"net/http"
	"testing"
)

// TestReportRejectsUnknownDirectoryPerson 核对报告表单不会接受无归属的测评人员标识。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestReportRejectsUnknownDirectoryPerson(t *testing.T) {
	_, mux := fixtureService(t)
	source := approvedFiling(t, mux, "BA-DIRECTORY-PERSON")
	input := map[string]any{
		"filingId": source.ID, "risk": "medium", "number": "BG-DIRECTORY-PERSON",
		"assessmentPersonId": "00000000-0000-0000-0000-000000000001",
	}
	assertError(t, requestJSON(mux, http.MethodPost, institutionBase+"/reports"+firstSubmissionScope("测评中心"), input),
		http.StatusBadRequest, "directory_person_invalid")
}

// TestReportRetainsDirectoryAttribution 核对名录停用、补正重新选择及已经送审的历史归属。
// 参数 t 为隔离数据库和附件测试上下文。
// 返回值 无。
func TestReportRetainsDirectoryAttribution(t *testing.T) {
	service, mux := fixtureService(t)
	original, replacement := reportDirectoryFixture(t, service)
	source := approvedFiling(t, mux, "BA-DIRECTORY-HISTORY")
	own := firstSubmissionScope("测评中心")
	collection := institutionBase + "/reports"
	form := map[string]any{"filingId": source.ID, "number": "BG-DIR-HISTORY", "risk": "medium"}
	assertError(t, requestJSON(mux, http.MethodPost, collection+own, form), 400, "directory_person_required")
	form["assessmentPersonId"] = original
	created := readResponse[mutation](t, requestJSON(mux, http.MethodPost, collection+own, form), 201)
	scoped, admin := collection+"/"+created.ID, reportsURL+"/"+created.ID
	for _, material := range institutionReportMaterials {
		postMaterialAt(t, mux, scoped+"/attachments"+own, material)
	}
	current := readResponse[recordDetail](t, requestJSON(mux, http.MethodGet, admin, nil), 200)
	if current.DirectorySnapshot == nil || current.DirectorySnapshot.Person == nil ||
		current.DirectorySnapshot.Person.ID != original ||
		current.DirectorySnapshot.Unit == nil || current.DirectorySnapshot.Unit.Name != "备案被测单位" ||
		current.DirectorySnapshot.Institution == nil || current.DirectorySnapshot.Institution.Qualification != "甲级" {
		t.Fatalf("报告未保存真实名录和人员归属: %+v", current.DirectorySnapshot)
	}
	submitted := readResponse[mutation](t, requestJSON(mux, http.MethodPost, scoped+"/submit"+own,
		versionInput{current.Version}), 200)
	returned := readResponse[mutation](t, reviewRequest(mux, admin, returnForFix, "核对测评人", submitted.Version), 200)
	disableReportAssessor(t, service, original)
	assertError(t, requestJSON(mux, http.MethodPost, scoped+"/submit"+own, versionInput{returned.Version}),
		409, "directory_unavailable")
	form["expectedVersion"] = returned.Version
	form["assessmentPersonId"] = replacement
	edited := readResponse[mutation](t, requestJSON(mux, http.MethodPatch, scoped+own, form), 200)
	readResponse[mutation](t, requestJSON(mux, http.MethodPost, scoped+"/submit"+own, versionInput{edited.Version}), 200)
	verifyReportedDirectoryHistory(t, mux, admin, original, replacement)
}

// reportDirectoryFixture 建立本用例逐步变更的单位、机构和两名人员。
// 参数 t、service 为共享隔离数据库和测试上下文。
// 返回值 原始和补正后的人员标识。
func reportDirectoryFixture(t *testing.T, service *Service) (string, string) {
	t.Helper()
	_, err := service.db.Exec(t.Context(), `INSERT INTO directory_parties
	   (kind,scope_name,name,code,qualification,valid_until) VALUES
	   ('unit','测试单位','备案被测单位','91330100MA27Y5QX49','',NULL),
	   ('institution','测评中心','机构显示名称','ORG-DIR-01','甲级','2099-12-31')`)
	if err != nil {
		t.Fatal(err)
	}
	type staff struct{ name, code, certificate string }
	insert := func(entry staff) string {
		var id string
		err := service.db.QueryRow(t.Context(), `INSERT INTO directory_people
		    (institution_id,name,code,certificate_no,qualification)
		    SELECT id,$1,$2,$3,'测评师' FROM directory_parties WHERE scope_name='测评中心'
		    RETURNING id::text`, entry.name, entry.code, entry.certificate).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	return insert(staff{"原始测评人", "T001", "CERT-OLD"}), insert(staff{"补正测评人", "T002", "CERT-NEW"})
}

// disableReportAssessor 模拟报告退回后该人员的资质或可用状态发生变化。
// 参数 t、service 为测试环境，personID 是原报告名录引用。
// 返回值 无。
func disableReportAssessor(t *testing.T, service *Service, personID string) {
	t.Helper()
	if _, err := service.db.Exec(t.Context(), `UPDATE directory_people
	    SET status='inactive',name='名录更名' WHERE id=$1`, personID); err != nil {
		t.Fatal(err)
	}
}

// verifyReportedDirectoryHistory 核对再送审不覆盖上一轮选择的实际人员及显示名称。
// 参数 t、mux 为测试上下文及实际路由，path、prior、current 为报告地址和本轮、上一轮人员标识。
// 返回值 无。
func verifyReportedDirectoryHistory(t *testing.T, mux http.Handler, path, prior, current string) {
	t.Helper()
	history := readResponse[itemResponse[reviewEvent]](t, requestJSON(mux, http.MethodGet, path+"/review-history", nil), 200)
	if len(history.Items) != 3 {
		t.Fatalf("报告轮次不完整: %+v", history.Items)
	}
	latest, first := history.Items[0].DirectorySnapshot, history.Items[2].DirectorySnapshot
	if latest == nil || latest.Person == nil || latest.Person.ID != current ||
		first == nil || first.Person == nil || first.Person.ID != prior || first.Person.Name != "原始测评人" {
		t.Fatalf("旧审核轮次的人员发生漂移: %+v", history.Items)
	}
}
