package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/audittrail"
)

// TestCalendarRejectsMalformedRequestID 拒绝分隔符不是短横线的日历请求号。
// 参数 t 为日历输入验证测试。
// 返回值 无。
func TestCalendarRejectsMalformedRequestID(t *testing.T) {
	version := int64(0)
	input := calendarUpdate{RequestID: "bc0e1f00x9461-448b-915b-549757e58300",
		ExpectedVersion: &version, Mode: "workday", Reason: "补班验证"}
	if normalizeCalendarUpdate(&input) {
		t.Fatal("错误分隔符的请求号通过校验")
	}
}

// TestWorkdayCalendarChangesActualDeadline 核对时限同源、并发重放和恢复前后审计。
// 参数 t 为隔离 PostgreSQL 的工作日历测试。
// 返回值 无。
func TestWorkdayCalendarChangesActualDeadline(t *testing.T) {
	db := openWorkbenchTestDB(t, context.Background())
	mux := http.NewServeMux()
	RegisterWorkdayCalendar(mux, db, "监管台历测试")
	audittrail.New(db).Register(mux)
	const base = "/api/v1/workday-calendar"
	const specific = base + "/2026-02-07"
	month := readApplicantDeadline[struct{ Items []map[string]any }](t, mux, base+"?month=2026-02", 200)
	if len(month.Items) != 28 || month.Items[6]["isWorkday"] != false {
		t.Fatalf("二月默认日历不符合周末规则：%+v", month)
	}
	assertCalendarDeadline(t, db, "2026-02-09")
	body := `{"requestId":"bc0e1f00-9461-448b-915b-549757e58300","expectedVersion":0,"mode":"workday","reason":"隔离补班核验"}`
	runCalendarConcurrentSave(t, mux, specific, body)
	assertCalendarDeadline(t, db, "2026-02-07")
	for _, invalid := range []string{
		`{"requestId":"bc0e1f00-9461-448b-915b-549757e58301","expectedVersion":0,"mode":"default","reason":"旧版本"}`,
		`{"requestId":"bc0e1f00-9461-448b-915b-549757e58300","expectedVersion":0,"mode":"restday","reason":"请求号被复用"}`,
	} {
		writeCalendar(t, mux, specific, invalid, 409)
	}
	writeCalendar(t, mux, specific,
		`{"requestId":"bc0e1f00-9461-448b-915b-549757e58302","expectedVersion":1,"mode":"default","reason":"恢复周末默认"}`, 200)
	assertCalendarDeadline(t, db, "2026-02-09")
	assertCalendarAudits(t, mux, specific)
	readApplicantDeadline[map[string]any](t, mux, base+"?month=2026-02&month=2026-01", 400)
	readApplicantDeadline[map[string]any](t, mux, base+"/2026-02-30/history", 400)
	writeCalendar(t, mux, "/api/v1/operator/workday-calendar/2026-02-07", body, 404)
}

// runCalendarConcurrentSave 验证两个同时到达的相同请求返回同一回执。
// 参数 t 为测试，mux、address、body 指定写日历请求。
// 返回值 无。
func runCalendarConcurrentSave(t *testing.T, mux http.Handler, address, body string) {
	t.Helper()
	results := make(chan *httptest.ResponseRecorder, 2)
	for range cap(results) {
		go func() { results <- callCalendar(mux, address, body) }()
	}
	first, replay := <-results, <-results
	if first.Code != 200 || replay.Code != 200 || first.Body.String() != replay.Body.String() {
		t.Fatalf("并发保存未复用回执：%d %s 与 %d %s", first.Code, first.Body, replay.Code, replay.Body)
	}
}

// callCalendar 向实际路由发送工作日覆盖请求。
// 参数 mux 为测试路由，address 和 body 为路径和最小请求体。
// 返回值 HTTP 回执。
func callCalendar(mux http.Handler, address, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPatch, address, bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

// writeCalendar 核对输入、版本或重复请求实际返回的 HTTP 状态。
// 参数 t、mux 为测试和路由，address、body、status 为请求和期望结果。
// 返回值 无。
func writeCalendar(t *testing.T, mux http.Handler, address, body string, status int) {
	t.Helper()
	w := callCalendar(mux, address, body)
	if w.Code != status {
		t.Fatalf("日历保存返回 %d，应为 %d：%s", w.Code, status, w.Body)
	}
	if !json.Valid(w.Body.Bytes()) && status != 404 {
		t.Fatalf("日历保存响应不是 JSON：%s", w.Body)
	}
}

// assertCalendarDeadline 核对变更已由既有业务计算函数读取。
// 参数 t、db 为测试和连接池，expected 为预期截止日期。
// 返回值 无。
func assertCalendarDeadline(t *testing.T, db *pgxpool.Pool, expected string) {
	t.Helper()
	var due string
	err := db.QueryRow(t.Context(), "SELECT to_char(workflow_due('2026-02-06',1),'YYYY-MM-DD')").Scan(&due)
	if err != nil || due != expected {
		t.Fatalf("计算时限为 %q，期望 %q，错误 %v", due, expected, err)
	}
}

// assertCalendarAudits 核对日历页和集中操作审计均只出现实际发生的两次变更。
// 参数 t 为测试，mux 为路由，path 为选定自然日。
// 返回值 无。
func assertCalendarAudits(t *testing.T, mux http.Handler, path string) {
	t.Helper()
	type events struct{ Items []map[string]any }
	history := readApplicantDeadline[events](t, mux, path+"/history", 200)
	if len(history.Items) != 2 || history.Items[0]["mode"] != "default" ||
		history.Items[1]["mode"] != "workday" || history.Items[1]["operator"] != "监管台历测试" {
		t.Fatalf("工作日变更留痕异常：%+v", history)
	}
	audit := readApplicantDeadline[events](t, mux, "/api/v1/audit?resource=workday-calendar", 200)
	if len(audit.Items) != 2 || audit.Items[0]["href"] != "/workday-calendar?day=2026-02-07" {
		t.Fatalf("集中审计应显示实际变更及日期跳转：%+v", audit)
	}
}
