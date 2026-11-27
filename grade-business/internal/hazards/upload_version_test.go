package hazards

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"grade-business/internal/cases"
)

type interruptedUpload struct {
	io.ReadCloser
	advance func()
}

// Read 在读取上传流之前通过实际 API 推进进度，确定性复现并发上传。
// 参数 data 为上传读取缓冲区。
// 返回值 已读取长度以及底层读取错误。
func (body *interruptedUpload) Read(data []byte) (int, error) {
	if body.advance != nil {
		advance := body.advance
		body.advance = nil
		advance()
	}
	return body.ReadCloser.Read(data)
}

// TestDelayedHazardAttachmentsCannotEnterNewRound 证明迟到材料无法登记到后续办理轮次。
// 参数 t 为隔离真实业务测试上下文。
// 返回值 无。
func TestDelayedHazardAttachmentsCannotEnterNewRound(t *testing.T) {
	for _, role := range []string{"operator", "institution", "regulator"} {
		t.Run(role, func(t *testing.T) {
			directory := t.TempDir()
			service, router := ledgerFixtureWithMaterials(t, directory)
			admin, _, _ := syncedPaths(t, service, router)
			id := strings.TrimPrefix(admin, testLedgerPath+"/")
			operator := operatorBase + "/" + id + "?organization=" + url.QueryEscape("试点单位")
			institution := institutionBase + "/" + id + "?institution=" + url.QueryEscape("测评中心")
			ret := institutionBase + "/" + id + "/actions?institution=" + url.QueryEscape("测评中心")
			sub := operatorBase + "/" + id + "/actions?organization=" + url.QueryEscape("试点单位")

			path, kind, header := operator, kindEvidence, `"1"`
			if role == "regulator" {
				path, header = admin, ""
			}
			if role == "institution" {
				submitHazardEvidenceForUploadRace(t, router, operator, sub, 1)
				path, kind, header = institution, kindRetestReport, `"3"`
			}
			request := ledgerUploadRequest(t, path, kind, fixturePDF)
			if header != "" {
				request.Header.Set("If-Match", header)
			}
			advance := func() {
				if role != "institution" {
					submitHazardEvidenceForUploadRace(t, router, operator, sub, 1)
				}
				ledgerReply[hazardMutation](t, ledgerCall(router, http.MethodPost, ret,
					actionInput{actionRetestReturn, 3, "补充整改证据"}), 200)
				if role == "institution" {
					submitHazardEvidenceForUploadRace(t, router, operator, sub, 4)
				}
			}
			request.Body = &interruptedUpload{ReadCloser: request.Body, advance: advance}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assertLedgerError(t, response, 409, "conflict")

			count, version, status := 1, int64(4), statusReturned
			if role == "institution" {
				count, version, status = 2, 6, statusRetest
			}
			if header != "" {
				retry := ledgerUploadRequest(t, path, kind, fixturePDF)
				retry.Header.Set("If-Match", header)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, retry)
				assertLedgerError(t, response, 409, "conflict")
			}
			assertLateUploadLeavesNoMaterial(t, router, admin, institution, directory, count, version, status)
		})
	}
}

// assertLateUploadLeavesNoMaterial 检查拒绝旧上传后持久化记录、实体文件和新轮状态。
// 参数 t、router 为验证上下文，admin、institution 为两个详情地址，directory 为测试存储目录，
// count、version 和 status 为新轮登记前允许保留的文件数、版本和状态。
// 返回值 无。
func assertLateUploadLeavesNoMaterial(t *testing.T, router http.Handler, admin, institution, directory string,
	count int, version int64, status string) {
	t.Helper()
	attachments := ledgerReply[hazardCollection[cases.Attachment]](t,
		ledgerCall(router, http.MethodGet, admin+"/attachments", nil), 200)
	current := ledgerReply[hazardDetail](t, ledgerCall(router, http.MethodGet, institution, nil), 200)
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != count || len(attachments.Items) != count || current.Version != version ||
		current.Status != status || current.RetestReportReady == nil || *current.RetestReportReady {
		t.Fatalf("late upload left data or changed the round: files=%v, attachments=%+v, current=%+v, err=%v",
			files, attachments, current, err)
	}
}

// submitHazardEvidenceForUploadRace 通过运营者接口上传新材料并正式送测。
// 参数 t 和 router 为验证上下文，path、actions 为同一隐患的详情和办理地址，version 为上传前版本。
// 返回值 无。
func submitHazardEvidenceForUploadRace(t *testing.T, router http.Handler, path, actions string, version int64) {
	t.Helper()
	ledgerReply[cases.Attachment](t, ledgerUpload(t, router, path, kindEvidence, fixturePDF, version), 201)
	ledgerReply[hazardMutation](t, ledgerCall(router, http.MethodPost, actions,
		actionInput{actionSubmit, version + 1, "提交整改证据"}), 200)
}

// TestHazardUploadRequiresStrongVersion 验证可写阶段的外端上传拒绝缺失和非法条件。
// 参数 t 为隔离真实业务测试上下文。
// 返回值 无。
func TestHazardUploadRequiresStrongVersion(t *testing.T) {
	service, router := ledgerFixture(t)
	admin, _, _ := syncedPaths(t, service, router)
	id := strings.TrimPrefix(admin, testLedgerPath+"/")
	operator := operatorBase + "/" + id + "?organization=" + url.QueryEscape("试点单位")
	for _, values := range [][]string{nil, {""}, {"1"}, {"*"}, {`W/"1"`}, {`"1", "2"`}, {`"1"`, `"1"`}, {`"01"`}, {`"+1"`}, {`"0"`}} {
		request := ledgerUploadRequest(t, operator, kindEvidence, fixturePDF)
		for _, value := range values {
			request.Header.Add("If-Match", value)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		assertLedgerError(t, response, 400, "invalid_input")
	}
	assertLedgerError(t, ledgerUpload(t, router, operator, kindEvidence, fixturePDF, 2), 409, "conflict")
	actions := operatorBase + "/" + id + "/actions?organization=" + url.QueryEscape("试点单位")
	submitHazardEvidenceForUploadRace(t, router, operator, actions, 1)
	institution := institutionBase + "/" + id + "?institution=" + url.QueryEscape("测评中心")
	assertLedgerError(t, ledgerUpload(t, router, institution, kindRetestReport, fixturePDF), 400, "invalid_input")
}
