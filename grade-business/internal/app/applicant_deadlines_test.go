package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestApplicantCanFollowReviewDeadline 验证办理方和申请方看到同一期限且不跨归属。
// 参数 t 为使用隔离 PostgreSQL 的测试上下文。
// 返回值 无。
func TestApplicantCanFollowReviewDeadline(t *testing.T) {
	ctx := t.Context()
	pool := openWorkbenchTestDB(t, context.Background())
	var id string
	err := pool.QueryRow(ctx, `INSERT INTO business_cases
	 (kind,number,system_name,organization_name,assessment_org,level,status,version,operator_origin)
	 VALUES ('filing','FOLLOW-1','时限核对系统','归属单位','处理机构','3','pending_initial',2,true)
	 RETURNING id::text`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	RegisterWorkbenchOverview(mux, pool, ReviewWindows{FilingDays: 3, FilingFinalDays: 5})
	operator := "/api/v1/operator/workbench/deadlines"
	partner := "/api/v1/institution/workbench/deadlines"
	org := "?" + url.Values{"organization": {"归属单位"}}.Encode()
	agency := "?" + url.Values{"institution": {"处理机构"}}.Encode()
	other := "?" + url.Values{"organization": {"其他单位"}}.Encode()
	target := "/filings/" + id
	mine := readApplicantDeadline[map[string]string](t, mux, operator+target+org, 200)
	actual := readApplicantDeadline[map[string]string](t, mux, partner+target+agency, 200)
	if mine["deadline"] == "" || mine["deadline"] != actual["deadline"] ||
		mine["waitingFor"] != "institution" || actual["waitingFor"] != "" {
		t.Fatalf("申请人与机构时限不一致：%v %v", mine, actual)
	}
	if hidden := readApplicantDeadline[map[string]string](t, mux, operator+target+other, 200); len(hidden) != 0 {
		t.Fatalf("范围外查询返回了期限：%v", hidden)
	}
	readApplicantDeadline[map[string]string](t, mux, operator+"/alerts/"+id+org, 400)
	readApplicantDeadline[map[string]string](t, mux, operator+target+org+"&offset=1", 400)
	assertApplicantDeadlineList(t, mux, operator+org, id)
	if _, err := pool.Exec(ctx, `UPDATE business_cases SET status='approved' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if ended := readApplicantDeadline[map[string]string](t, mux, operator+target+org, 200); len(ended) != 0 {
		t.Fatalf("办结后期限仍留在待办：%v", ended)
	}
}

// assertApplicantDeadlineList 核对报送方的关注时限仅展示状态，不增加实际工作台待办。
// 参数 t、mux 为测试及路由，path 为范围列表地址，id 为此记录标识。
// 返回值 无。
func assertApplicantDeadlineList(t *testing.T, mux http.Handler, path, id string) {
	t.Helper()
	result := readApplicantDeadline[struct {
		Items []struct {
			ID         string
			WaitingFor string
			CanRemind  bool
		}
	}](t, mux, path, 200)
	if len(result.Items) != 1 || result.Items[0].ID != id ||
		result.Items[0].WaitingFor != "institution" || result.Items[0].CanRemind {
		t.Fatalf("报送方时限列表 = %+v", result)
	}
}

// TestSupervisionTaskAppearsForCurrentSeat 验证自查待办进入唯一办理方的同源队列。
// 参数 t 为使用隔离 PostgreSQL 的测试上下文。
// 返回值 无。
func TestSupervisionTaskAppearsForCurrentSeat(t *testing.T) {
	ctx := t.Context()
	pool := openWorkbenchTestDB(t, context.Background())
	var id string
	err := pool.QueryRow(ctx, `WITH filing AS (
	 INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status)
	 VALUES ('filing','SELF-1','自查测试系统','自查归属','测试机构','3','approved')
	 RETURNING id,number,system_name,organization_name
	), today AS (SELECT (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date AS day)
	 INSERT INTO supervision_tasks(request_id,filing_id,filing_number,system_name,
	   organization_name,title,due_on,cycle_year,status,version,published_at)
	 SELECT gen_random_uuid(),id,number,system_name,organization_name,'年度自查',
	   day,EXTRACT(year FROM day),'open',2,clock_timestamp() FROM filing CROSS JOIN today
	 RETURNING id::text`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	RegisterWorkbenchOverview(mux, pool, ReviewWindows{FilingDays: 3})
	org := "?" + url.Values{"organization": {"自查归属"}}.Encode()
	wrong := "?" + url.Values{"organization": {"其他单位"}}.Encode()
	base := "/api/v1/operator/workbench"
	own := readApplicantDeadline[deadlinePage](t, mux, base+"/deadlines"+org, 200)
	if len(own.Items) != 1 || own.Items[0].ID != id ||
		own.Items[0].Resource != "supervisions" || own.Items[0].Deadline == "" {
		t.Fatalf("自查未进入本单位限时待办：%v", own)
	}
	other := readApplicantDeadline[deadlinePage](t, mux, base+"/deadlines"+wrong, 200)
	if len(other.Items) != 0 {
		t.Fatalf("其他单位读到自查截止日期：%v", other)
	}
	if _, err := pool.Exec(ctx, `UPDATE supervision_tasks
		SET status='submitted',version=3,submitted_version=3,submitted_at=clock_timestamp()
		WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	admin := readApplicantDeadline[struct{ Tasks []overviewTask }](t, mux, "/api/v1/workbench/overview", 200)
	if len(admin.Tasks) != 1 || admin.Tasks[0].ID != id ||
		admin.Tasks[0].Status != "submitted" {
		t.Fatalf("已报送自查未进入监管待办：%v", admin)
	}
}

// readApplicantDeadline 对受控路由执行 GET 并校验预期状态。
// 参数 t、mux、path、status 为测试、路由、地址及期望状态码。
// 返回值 成功响应的解码结果。
func readApplicantDeadline[T any](t *testing.T, mux http.Handler, path string, status int) T {
	t.Helper()
	var result T
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code != status {
		t.Fatalf("%s 返回 %d，期望 %d：%s", path, w.Code, status, w.Body)
	}
	if status == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}
