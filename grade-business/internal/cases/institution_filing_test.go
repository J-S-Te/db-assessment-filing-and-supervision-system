package cases

import (
	"net/url"
	"testing"
)

// TestInstitutionFilingCorrection 验证机构接续退回备案并在重提时真实推进状态。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestInstitutionFilingCorrection(t *testing.T) {
	service, handler := fixtureService(t)
	filing := createFiling(t, handler, "QA-机构备案")
	path := filingsURL + "/" + filing.ID
	postMaterial(t, handler, path, "filing_form")
	postMaterial(t, handler, path, "classification_report")
	started := readResponse[mutation](t, requestJSON(handler, "POST", path+"/submit", versionInput{3}), 200)
	readResponse[mutation](t, reviewRequest(handler, path, returnForFix, "机构需补传文件", started.Version), 200)
	source := sampleFiling("QA-机构备案")
	scope := "?institution=" + url.QueryEscape(source.AssessmentOrg)
	other := "?institution=" + url.QueryEscape("其他测试机构")
	base := "/api/v1/institution/filings/" + filing.ID
	assertError(t, requestJSON(handler, "GET", base+other, nil), 404, "not_found")
	current := readResponse[recordDetail](t, requestJSON(handler, "GET", base+scope, nil), 200)
	if current.Status != returned || len(current.AllowedActions) != 2 {
		t.Fatalf("机构未取得补正办理动作: %+v", current)
	}
	postMaterialAt(t, handler, base+"/attachments"+scope, "supporting")
	current = readResponse[recordDetail](t, requestJSON(handler, "GET", base+scope, nil), 200)
	assertError(t, requestJSON(handler, "POST", base+"/submit"+other, versionInput{current.Version}), 404, "not_found")
	readResponse[mutation](t, requestJSON(handler, "POST", base+"/submit"+scope, versionInput{current.Version}), 200)
	updated := readResponse[recordDetail](t, requestJSON(handler, "GET", path, nil), 200)
	if updated.Status != pendingInitial {
		t.Fatalf("机构重提后状态不正确: %+v", updated)
	}
	checkFilingNotice(t, service, filing.ID, "filings", source.AssessmentOrg)
}

// checkFilingNotice 核对实际送达记录属于该业务的登记机构。
// 参数 t 为测试上下文，s 为隔离业务服务，id、resource、recipient 为期望范围。
// 返回值 无。
func checkFilingNotice(t *testing.T, s *Service, id, resource, recipient string) {
	t.Helper()
	var sent int
	err := s.db.QueryRow(t.Context(), `SELECT count(*) FROM review_notices
		WHERE resource=$1 AND resource_id=$2 AND recipient=$3 AND state='returned'`, resource, id, recipient).Scan(&sent)
	if err != nil || sent != 1 {
		t.Fatalf("退回未正确投递: %d %v", sent, err)
	}
}
