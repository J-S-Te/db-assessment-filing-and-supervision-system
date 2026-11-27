package implementations

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/reviewinbox"
)

type implementationNotice struct {
	Version   int64
	State     string
	Recipient string
	Reference string
	Subject   string
	Comment   string
}

type implementationInboxMessage struct {
	ID             string     `json:"id"`
	Status         *string    `json:"status,omitempty"`
	State          string     `json:"state"`
	Comment        string     `json:"comment"`
	AcknowledgedAt *time.Time `json:"acknowledgedAt,omitempty"`
}

// TestImplementationReviewNotices 验证补正意见和监管复核通过按其实际审核事件送达登记机构。
// 参数 t 为隔离数据库集成测试上下文。
// 返回值 无。
func TestImplementationReviewNotices(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(pool)
	filing := implementationTestFiling(t, pool, "测试备案-审核回传", approved)
	item := testInput(filing, routine)
	item.AssessmentOrg = "实际实施机构"
	correction := returnedImplementation(t, mux, item)
	record := performAction(t, mux, correction, "submit", "")
	record = performAction(t, mux, record, "accept", "")
	record = performAction(t, mux, record, "approve", "初审通过。")
	record = performAction(t, mux, record, "approve", "终审通过。")
	accepted := performAction(t, mux, record, "approve", "监管复核通过。")
	rows, err := pool.Query(t.Context(), `SELECT event_version,state,recipient,reference,subject,comment
	 FROM review_notices WHERE audience='institution' AND resource='implementations' AND resource_id=$1
	 ORDER BY event_version`, accepted.ID)
	if err != nil {
		t.Fatal(err)
	}
	notices, err := pgx.CollectRows(rows, pgx.RowToStructByPos[implementationNotice])
	if err != nil {
		t.Fatal(err)
	}
	want := []implementationNotice{
		{correction.Version, returned, item.AssessmentOrg, "测试备案-审核回传", "测试系统", "补充割接和回退方案。"},
		{accepted.Version, approved, item.AssessmentOrg, "测试备案-审核回传", "测试系统", "监管复核通过。"},
	}
	if len(notices) != 2 || notices[0] != want[0] || notices[1] != want[1] {
		t.Fatalf("实施审核回传 = %+v, 预期 %+v", notices, want)
	}
}

// TestImplementationReviewNoticeFailureRollsBack 验证回传写入失败会回滚审核状态和留痕。
// 参数 t 为隔离数据库集成测试上下文。
// 返回值 无。
func TestImplementationReviewNoticeFailureRollsBack(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(pool)
	filing := implementationTestFiling(t, pool, "测试备案-回传失败", approved)
	item := testInput(filing, routine)
	item.AssessmentOrg = "实际实施机构"
	record := decodeResponse[mutation](t, callImplementation(mux, "POST", path, item), 201)
	record = performAction(t, mux, record, "submit", "")
	record = performAction(t, mux, record, "accept", "")
	if _, err := pool.Exec(t.Context(), `ALTER TABLE review_notices ADD CHECK (resource <> 'implementations')`); err != nil {
		t.Fatal(err)
	}
	resource := path + "/" + record.ID
	decodeResponse[map[string]string](t, callImplementation(mux, "POST", resource+"/actions",
		command{Action: "return_for_correction", ExpectedVersion: record.Version, Comment: "补齐割接方案。"}), 500)
	detail := decodeResponse[detail](t, callImplementation(mux, "GET", resource, nil), 200)
	history := decodeResponse[eventPage](t, callImplementation(mux, "GET", resource+"/history", nil), 200)
	if detail.Status != initial || detail.Version != record.Version || len(history.Items) != 3 || history.Items[0].Action != "accept" {
		t.Fatalf("回传失败后实际状态为 %+v，留痕为 %+v", detail, history)
	}
}

// TestReturnedImplementationReassignment 验证报备改派使新机构收到原意见并保留旧签收。
// 参数 t 为隔离数据库集成测试上下文。
// 返回值 无。
func TestReturnedImplementationReassignment(t *testing.T) {
	pool := isolatedImplementationPool(t)
	mux := implementationTestMux(pool)
	reviewinbox.Register(mux, pool)
	filing := implementationTestFiling(t, pool, "测试备案-改派回传", approved)
	item := testInput(filing, routine)
	item.AssessmentOrg = "原实施机构"
	record := returnedImplementation(t, mux, item)
	oldScope := "?institution=" + url.QueryEscape(item.AssessmentOrg)
	old := implementationInbox(t, mux, oldScope)
	acknowledge := httptest.NewRequest(http.MethodPost,
		"/api/v1/institution/feedback/"+old.ID+"/acknowledge"+oldScope, nil)
	receipt := httptest.NewRecorder()
	mux.ServeHTTP(receipt, acknowledge)
	decodeResponse[map[string]time.Time](t, receipt, 200)

	item.AssessmentOrg = "改派实施机构"
	newScope := "?institution=" + url.QueryEscape(item.AssessmentOrg)
	updated := decodeResponse[mutation](t, callImplementation(mux, "PATCH", path+"/"+record.ID,
		patchInput{input: item, ExpectedVersion: record.Version}), 200)
	assertImplementationReassignment(t, mux, record, updated, oldScope, newScope, old.ID)
	var version int64
	err := pool.QueryRow(t.Context(), `SELECT event_version FROM review_notices
	 WHERE resource='implementations' AND resource_id=$1 AND recipient=$2`, record.ID, item.AssessmentOrg).Scan(&version)
	if err != nil || version != record.Version {
		t.Fatalf("改派送达原意见版本 = %d, err = %v", version, err)
	}
	item.AssessmentOrg = "原实施机构"
	decodeResponse[mutation](t, callImplementation(mux, "PATCH", path+"/"+record.ID,
		patchInput{input: item, ExpectedVersion: updated.Version}), 200)
	restored := implementationInbox(t, mux, oldScope)
	if restored.ID != old.ID || restored.AcknowledgedAt == nil {
		t.Fatalf("改派回原机构中的签收记录 = %+v", restored)
	}
}

// implementationInbox 从共享收件箱读取当前测试唯一的实施意见。
// 参数 t 为测试上下文，mux 为业务路由，scope 为机构条件。
// 返回值 按公开接口返回的意见。
func implementationInbox(t *testing.T, mux *http.ServeMux, scope string) implementationInboxMessage {
	t.Helper()
	response := callImplementation(mux, "GET", "/api/v1/institution/feedback"+scope, nil)
	inbox := decodeResponse[struct{ Items []implementationInboxMessage }](t, response, 200)
	if len(inbox.Items) != 1 {
		t.Fatalf("预期一条实施意见，收到 %+v", inbox.Items)
	}
	return inbox.Items[0]
}

// assertImplementationReassignment 核对新旧收件人的意见状态、签收和详情访问范围。
// 参数 t 为测试上下文，mux 为业务路由，original、updated 为改派前后版本，
// oldScope、newScope 为机构条件，oldID 为原机构收到的意见标识。
// 返回值 无。
func assertImplementationReassignment(t *testing.T, mux *http.ServeMux, original, updated mutation,
	oldScope, newScope, oldID string) {
	t.Helper()
	old := implementationInbox(t, mux, oldScope)
	current := implementationInbox(t, mux, newScope)
	if old.ID != oldID || old.Status != nil || old.AcknowledgedAt == nil || current.ID == oldID ||
		current.Status == nil || *current.Status != returned || current.State != returned ||
		current.Comment != old.Comment || current.AcknowledgedAt != nil {
		t.Fatalf("改派后的站内意见，旧机构 = %+v，新机构 = %+v", old, current)
	}
	resource := "/api/v1/institution/implementations/" + original.ID
	decodeResponse[map[string]string](t, callImplementation(mux, "GET", resource+oldScope, nil), 404)
	decodeResponse[map[string]string](t, callImplementation(mux, "POST", resource+"/actions"+oldScope,
		command{Action: "submit", ExpectedVersion: updated.Version}), 404)
	detail := decodeResponse[detail](t, callImplementation(mux, "GET", resource+newScope, nil), 200)
	if detail.Version != updated.Version {
		t.Fatalf("新机构获取的实施详情 = %+v", detail)
	}
}
