package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestDashboardIndustryAndIssuesFollowBusinessRecords 核对独立行业维护、派发口径及问题归类排行。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDashboardIndustryAndIssuesFollowBusinessRecords(t *testing.T) {
	db := dashboardTestPool(t)
	active := dashboardTestFiling(t, db, "活动一", "测试单位", "approved", false)
	unclassified := dashboardTestFiling(t, db, "活动二", "测试单位", "approved", false)
	cancelled := dashboardTestFiling(t, db, "已注销", "测试单位", "approved", true)
	dashboardExec(t, db, `INSERT INTO directory_parties(kind,scope_name,name,code,classification)
		VALUES('unit','测试单位','测试单位','913300001000000011','企业')`)
	open := createIssueFixture(t, db, active, "有效的待整改隐患", "high", "open")
	closed := createIssueFixture(t, db, active, "有效的已销项隐患", "low", "closed")
	createIssueFixture(t, db, active, "待派发", "high", "pending_dispatch")
	createIssueFixture(t, db, unclassified, "未归类", "medium", "open")
	createIssueFixture(t, db, cancelled, "历史已销项", "low", "closed")

	mux := http.NewServeMux()
	Register(mux, db)
	analyticsCall(t, mux, http.MethodPatch, "/classifications/industry/"+active,
		`{"code":"22","expectedVersion":0}`, 200)
	analyticsCall(t, mux, http.MethodPatch, "/classifications/industry/"+cancelled,
		`{"code":"22","expectedVersion":0}`, 404)
	for _, id := range []string{open, closed} {
		analyticsCall(t, mux, http.MethodPatch, "/classifications/issue/"+id,
			`{"code":"access_control","expectedVersion":0}`, 200)
	}
	dashboardExec(t, db, "UPDATE hazard_items SET created_at=now()-interval '40 days' WHERE id=$1", closed)
	result := analyticsCall(t, mux, http.MethodGet, "/analytics", "", 200)
	var got struct {
		Industries struct {
			Items []struct {
				Code, Label                string
				Systems, High, Medium, Low int64
			}
			Unclassified districtCounts
		}
		RecentCommonIssues commonIssues
		CommonIssues       struct {
			Items []struct {
				Code, Label string
				Count       int64
			}
			Unclassified int64
		}
	}
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Industries.Items) != 1 || got.Industries.Items[0].Code != "22" ||
		got.Industries.Items[0].Label != "银行" || got.Industries.Items[0].Systems != 1 ||
		got.Industries.Items[0].High != 1 || got.Industries.Items[0].Low != 0 ||
		got.Industries.Unclassified != (districtCounts{Systems: 1, Medium: 1}) {
		t.Fatalf("行业归类及风险计数 = %+v", got.Industries)
	}
	if len(got.CommonIssues.Items) != 1 || got.CommonIssues.Items[0].Code != "access_control" ||
		got.CommonIssues.Items[0].Count != 2 || got.CommonIssues.Unclassified != 2 {
		t.Fatalf("常见问题归类 = %+v", got.CommonIssues)
	}
	if len(got.RecentCommonIssues.Items) != 1 || got.RecentCommonIssues.Items[0].Count != 1 ||
		got.RecentCommonIssues.Unclassified != 2 {
		t.Fatalf("近 30 日共性问题应排除历史记录：%+v", got.RecentCommonIssues)
	}
}

// TestDashboardClassificationsScopeAndVersions 核对搜索边界、清空归类和并发版本冲突的返回合同。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDashboardClassificationsScopeAndVersions(t *testing.T) {
	db := dashboardTestPool(t)
	active := dashboardTestFiling(t, db, "可搜索备案", "登记单位", "approved", false)
	dashboardTestFiling(t, db, "可搜索草稿", "登记单位", "draft", false)
	pending := createIssueFixture(t, db, active, "待派发隐患", "high", "pending_dispatch")
	mux := http.NewServeMux()
	Register(mux, db)
	table := analyticsCall(t, mux, http.MethodGet,
		"/classifications/industry?q=%E5%8F%AF%E6%90%9C%E7%B4%A2&page=1", "", 200)
	var list struct {
		Items []struct {
			ID, Number, Code string
			Version          int64
		}
		Options []struct{ Code, Label string }
		HasMore bool
	}
	if err := json.Unmarshal(table, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != active || list.Items[0].Number != "可搜索备案" ||
		list.Items[0].Version != 0 || list.HasMore || len(list.Options) == 0 {
		t.Fatalf("行业维护列表 = %+v", list)
	}
	analyticsCall(t, mux, http.MethodGet, "/classifications/industry?page=0", "", 400)
	analyticsCall(t, mux, http.MethodGet, "/classifications/industry?page=1&page=2", "", 400)
	analyticsCall(t, mux, http.MethodPatch, "/classifications/issue/"+pending,
		`{"code":"access_control","expectedVersion":0}`, 404)
	path := "/classifications/industry/" + active
	analyticsCall(t, mux, http.MethodPatch, path, `{"code":"企业","expectedVersion":0}`, 400)
	analyticsCall(t, mux, http.MethodPatch, path, `{"code":"22"}`, 400)
	saved := analyticsCall(t, mux, http.MethodPatch, path, `{"code":"22","expectedVersion":0}`, 200)
	assertClassification(t, saved, "22", 1)
	analyticsCall(t, mux, http.MethodPatch, path, `{"code":"27","expectedVersion":0}`, 409)
	cleared := analyticsCall(t, mux, http.MethodPatch, path, `{"code":"","expectedVersion":1}`, 200)
	assertClassification(t, cleared, "", 2)
	analyticsCall(t, mux, http.MethodPatch, path, `{"code":"22","expectedVersion":1}`, 409)
}

// TestDashboardDistrictLimitReportsOmittedGroups 核对有界区县风险统计与无名称数据。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestDashboardDistrictLimitReportsOmittedGroups(t *testing.T) {
	db := dashboardTestPool(t)
	f1 := dashboardTestFiling(t, db, "一区备案", "一区单位", "approved", false)
	f2 := dashboardTestFiling(t, db, "二区备案", "二区单位", "approved", false)
	unknown := dashboardTestFiling(t, db, "未知区备案", "未知区单位", "approved", false)
	dashboardExec(t, db, `INSERT INTO directory_parties (kind,scope_name,name,code,district) VALUES
		('unit','一区单位','一区单位','913300001000000011','一区'),
		('unit','二区单位','二区单位','913300001000000012','二区')`)
	createIssueFixture(t, db, f1, "一区高", "high", "open")
	createIssueFixture(t, db, f2, "二区中", "medium", "open")
	createIssueFixture(t, db, unknown, "未登记低", "low", "open")
	mux := http.NewServeMux()
	Register(mux, db)
	result := analyticsCall(t, mux, http.MethodGet, "/risk-by-district?limit=1", "", 200)
	var got struct {
		Items            []districtRisk
		WithoutDistrict  districtCounts
		OmittedDistricts int
	}
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].District != "一区" || got.Items[0].High != 1 ||
		got.WithoutDistrict.Low != 1 || got.OmittedDistricts != 1 {
		t.Fatalf("区县风险超限标记 = %+v", got)
	}
	analyticsCall(t, mux, http.MethodGet, "/risk-by-district?limit=201", "", 400)
}

// createIssueFixture 保存直接登记隐患及相应状态的业务数据。
// 参数 t 为测试上下文，db 为隔离库，filing、title、severity、status 为隐患业务字段。
// 返回值 已保存记录的公开标识。
func createIssueFixture(t *testing.T, db *pgxpool.Pool, filing, title, severity, status string) string {
	t.Helper()
	return dashboardRowID(t, db, `INSERT INTO hazard_items
	  (registration_filing_id,ledger,title,source,discovered_on,severity,description,measure,responsibility,
	     deadline,status,dispatched_at,retest_institution,closed_at)
	  VALUES ($1,'major_risk',$2,'manual','2026-11-22',$3,'隔离测试','重新配置','整改组',
	      '2026-12-01',$4,CASE WHEN $4<>'pending_dispatch' THEN clock_timestamp() END,
	      CASE WHEN $4<>'pending_dispatch' THEN '复测机构' END,
	      CASE WHEN $4='closed' THEN clock_timestamp() END) RETURNING id::text`, filing, title, severity, status)
}

// analyticsCall 请求驾驶舱接口并核对响应状态。
// 参数 t 为测试上下文，mux 为实际路由，method、path、body、status 为请求和预期状态。
// 返回值 成功读取的响应 JSON 字节。
func analyticsCall(t *testing.T, mux *http.ServeMux, method, path, body string, status int) []byte {
	t.Helper()
	res := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/api/v1/dashboard"+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	mux.ServeHTTP(res, req)
	if res.Code != status {
		t.Fatalf("%s %s = %d，期望 %d：%s", method, path, res.Code, status, res.Body)
	}
	return res.Body.Bytes()
}

// assertClassification 核对已持久归类的编码及版本。
// 参数 t 为测试上下文，payload 为响应 JSON，code 和 version 为预期值。
// 返回值 无。
func assertClassification(t *testing.T, payload []byte, code string, version int64) {
	t.Helper()
	var got struct {
		Code    string
		Version int64
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.Code != code || got.Version != version {
		t.Fatalf("维护响应 = %+v，期望 %s/%d", got, code, version)
	}
}
