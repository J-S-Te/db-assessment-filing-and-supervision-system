package remediations

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestBulkRemindersRecheckEligibilityAndDeduplicate 验证催办边界、同日并发去重与站内留痕。
// 参数 t 为隔离数据库和 HTTP 测试上下文。
// 返回值 无。
func TestBulkRemindersRecheckEligibilityAndDeduplicate(t *testing.T) {
	db, router := remediationFixture(t)
	report, _ := insertReports(t, db)
	near := seedFollowupPlan(t, db, router, report, returned, 10)
	far := seedFollowupPlan(t, db, router, report, returned, 11)
	late := seedFollowupPlan(t, db, router, report, returned, -1)
	pending := seedFollowupPlan(t, db, router, report, pendingReview, 5)
	if _, err := db.Exec(t.Context(), `INSERT INTO review_notices
		(audience,resource,resource_id,event_version,state,recipient,reference,subject,comment)
		VALUES('operator','remediations',$1,1,'returned','测试单位','BG-QA','测试系统','待补正')`, near); err != nil {
		t.Fatal(err)
	}
	ids := []string{near, far, late, pending}
	request := map[string][]string{"planIds": ids}
	first, next := concurrentReminders(t, router, request)
	for row, id := range ids[1:] {
		if first[row+1].PlanID != id || first[row+1].Status != "ineligible" || next[row+1].Status != "ineligible" {
			t.Fatalf("ineligible plan was reminded: %+v %+v", first, next)
		}
	}
	if statuses := []string{first[0].Status, next[0].Status}; !slices.Contains(statuses, "sent") || !slices.Contains(statuses, "already_sent") ||
		first[0].NoticeID == "" || first[0].NoticeID != next[0].NoticeID {
		t.Fatalf("delivery did not deduplicate: %+v %+v", first, next)
	}
	var found int
	err := db.QueryRow(t.Context(), `SELECT count(*) FROM review_notices WHERE resource_id=$1
		AND audience='operator' AND resource='remediations' AND recipient='测试单位' AND state='reminder'`,
		near).Scan(&found)
	if err != nil || found != 1 || first[0].OrganizationName != "测试单位" {
		t.Fatalf("reminder receipt missing: count=%d, err=%v", found, err)
	}
	response[map[string]string](t, call(router, "POST", rootURL+"/reminders", map[string][]string{
		"planIds": {near, strings.ToUpper(near)},
	}), 400)
	assertRenewedReminder(t, db, router, near, first[0].NoticeID)
}

// TestReminderWriteFailureDoesNotLeavePartialBatch 验证任一通知写入失败会回滚全部已写催办。
// 参数 t 为隔离数据库和 HTTP 测试上下文。
// 返回值 无。
func TestReminderWriteFailureDoesNotLeavePartialBatch(t *testing.T) {
	db, router := remediationFixture(t)
	report, _ := insertReports(t, db)
	ids := []string{
		seedFollowupPlan(t, db, router, report, returned, 3),
		seedFollowupPlan(t, db, router, report, returned, 3),
	}
	slices.Sort(ids)
	constraint := `ALTER TABLE review_notices ADD CHECK (resource_id <> '` + ids[1] + `'::uuid)`
	if _, err := db.Exec(t.Context(), constraint); err != nil {
		t.Fatal(err)
	}
	response[map[string]string](t, call(router, "POST", rootURL+"/reminders", map[string][]string{"planIds": ids}), 500)
	var count int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM review_notices`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed batch left persisted deliveries: %d, %v", count, err)
	}
}

// assertRenewedReminder 核对去重按本地日期和接收方生效，并保留原审核及催办回执。
// 参数 t 为测试上下文，db 为隔离库，router 为业务路由，id 和 original 为规划及第一次催办标识。
// 返回值 无。
func assertRenewedReminder(t *testing.T, db *pgxpool.Pool, router http.Handler, id, original string) {
	t.Helper()
	if _, err := db.Exec(t.Context(), `UPDATE review_notices SET reminder_day=reminder_day-1 WHERE id=$1`, original); err != nil {
		t.Fatal(err)
	}
	request := map[string][]string{"planIds": {id}}
	fresh := response[collection[reminderResultFixture]](t, call(router, "POST", rootURL+"/reminders", request), 200)
	if len(fresh.Items) != 1 || fresh.Items[0].Status != "sent" || fresh.Items[0].NoticeID == original {
		t.Fatalf("yesterday's reminder prevented delivery: %+v", fresh)
	}
	if _, err := db.Exec(t.Context(), `UPDATE business_cases SET organization_name='接续整改单位' WHERE kind='filing'`); err != nil {
		t.Fatal(err)
	}
	transferred := response[collection[reminderResultFixture]](t, call(router, "POST", rootURL+"/reminders", request), 200)
	if len(transferred.Items) != 1 || transferred.Items[0].OrganizationName != "接续整改单位" ||
		transferred.Items[0].Status != "sent" || transferred.Items[0].NoticeID == fresh.Items[0].NoticeID {
		t.Fatalf("current owner did not receive a distinct reminder: %+v", transferred)
	}
	var count int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM review_notices WHERE resource_id=$1`, id).
		Scan(&count); err != nil || count != 4 {
		t.Fatalf("incorrect persisted review and reminder count: %d, %v", count, err)
	}
}

type reminderResultFixture struct {
	PlanID           string `json:"planId"`
	OrganizationName string `json:"organizationName"`
	NoticeID         string `json:"noticeId"`
	Status           string `json:"status"`
}

// concurrentReminders 模拟同一组规划的两个竞争催办请求。
// 参数 t 为测试上下文，router 为业务路由，request 为相同的已选规划标识。
// 返回值 两个请求的逐项反馈。
func concurrentReminders(t *testing.T, router http.Handler, request map[string][]string) (
	[]reminderResultFixture, []reminderResultFixture,
) {
	t.Helper()
	var workers sync.WaitGroup
	var result [2]collection[reminderResultFixture]
	var status [2]int
	var decode [2]error
	for i := range result {
		workers.Go(func() {
			recorder := call(router, "POST", rootURL+"/reminders", request)
			status[i] = recorder.Code
			decode[i] = json.Unmarshal(recorder.Body.Bytes(), &result[i])
		})
	}
	workers.Wait()
	if status != [2]int{200, 200} || decode[0] != nil || decode[1] != nil ||
		len(result[0].Items) != len(request["planIds"]) || len(result[1].Items) != len(request["planIds"]) {
		t.Fatalf("invalid reminder response: status=%v bodies=%+v decode=%v", status, result, decode)
	}
	return result[0].Items, result[1].Items
}

// TestRemediationExportMatchesFiltersAndEscapesCells 验证实际过滤、生效列和电子表格注入防护。
// 参数 t 为隔离数据库和 HTTP 测试上下文。
// 返回值 无。
func TestRemediationExportMatchesFiltersAndEscapesCells(t *testing.T) {
	db, router := remediationFixture(t)
	report, _ := insertReports(t, db)
	system := " \t=SUM(1,2)\n测试系统"
	if _, err := db.Exec(t.Context(), `UPDATE business_cases SET system_name=$1,organization_name='@测试单位'
		WHERE kind='filing'`, system); err != nil {
		t.Fatal(err)
	}
	matching := seedFollowupPlan(t, db, router, report, returned, 5)
	seedFollowupPlan(t, db, router, report, returned, 20)
	seedFollowupPlan(t, db, router, report, pendingReview, 5)
	query := "?q=" + url.QueryEscape("测试系统") + "&status=returned&due=within_10_days"
	export := call(router, "GET", rootURL+"/export"+query, nil)
	if export.Code != 200 || !strings.HasPrefix(export.Header().Get("Content-Type"), "text/csv") ||
		!strings.HasPrefix(export.Header().Get("Content-Disposition"), "attachment") ||
		!strings.HasPrefix(export.Body.String(), "\ufeff") {
		t.Fatalf("invalid CSV headers or status: %d %v %q", export.Code, export.Header(), export.Body.String())
	}
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(export.Body.String(), "\ufeff"))).ReadAll()
	if err != nil || len(records) != 2 || len(records[0]) != 10 || records[0][0] != "备案编号" ||
		records[1][0] != "BA-QA" || records[1][1] != "'"+system || records[1][2] != "'@测试单位" ||
		records[1][8] != businessDate(5) || records[1][9] != "退回补正" {
		t.Fatalf("incorrect export contents: %+v, %v", records, err)
	}
	response[map[string]string](t, call(router, "GET", rootURL+"/export"+query+"&limit=1", nil), 400)
	assertExportIncludesLaterPages(t, db, router, matching, query)
}

// assertExportIncludesLaterPages 验证导出覆盖筛选后超出默认页限制的记录。
// 参数 t 为测试上下文，db 为隔离库，router 为业务路由，id 为匹配快照，query 为同一筛选条件。
// 返回值 无。
func assertExportIncludesLaterPages(t *testing.T, db *pgxpool.Pool, router http.Handler, id, query string) {
	t.Helper()
	if _, err := db.Exec(t.Context(), `INSERT INTO remediation_plans(report_id,deadline,risks,status,submission,submitted_at)
		SELECT report_id,deadline,risks,status,submission,submitted_at FROM remediation_plans
		CROSS JOIN generate_series(1,20) WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	list := response[page](t, call(router, "GET", rootURL+query, nil), 200)
	file := call(router, "GET", rootURL+"/export"+query, nil)
	data := strings.TrimPrefix(file.Body.String(), "\ufeff")
	rows, err := csv.NewReader(strings.NewReader(data)).ReadAll()
	if file.Code != 200 || err != nil || len(list.Items) != 20 || list.NextCursor == "" || len(rows) != 22 {
		t.Fatalf("export did not cover all matching plans: HTTP %d, page=%d, CSV=%d, %v",
			file.Code, len(list.Items), len(rows), err)
	}
}

// seedFollowupPlan 构造具有真实风险、期限和指定状态的可查询业务条目。
// 参数 t 为测试上下文，db 为隔离数据库，router 为备案整改路由，report、status 和 offset 指定关联和期限。
// 返回值 规划标识。
func seedFollowupPlan(t *testing.T, db *pgxpool.Pool, router http.Handler, report, status string, offset int) string {
	t.Helper()
	input := newPlanInput(report)
	input.Deadline, input.Risks[0].Deadline = businessDate(offset), businessDate(offset)
	created := response[mutation](t, call(router, "POST", rootURL, input), 201)
	if _, err := db.Exec(t.Context(), `UPDATE remediation_plans SET status=$2,submission=1,
		submitted_at=transaction_timestamp() WHERE id=$1`, created.ID, status); err != nil {
		t.Fatal(err)
	}
	return created.ID
}
