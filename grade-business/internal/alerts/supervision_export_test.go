package alerts

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestOverdueSupervisionAndOperatorExport 验证超期只登记一条监管线索且导出限制单位与关闭日期。
// 参数 t 为使用独立 PostgreSQL 的测试上下文。
// 返回值 无。
func TestOverdueSupervisionAndOperatorExport(t *testing.T) {
	pool, router := newAlertFixture(t)
	plan := seedAlertPlan(t, pool)
	addAlertHazard(t, pool, plan, 1, "medium", "open", -1)
	for range 2 {
		readAlertTest[syncResult](t, router, "POST", "/api/v1/alerts/sync", "", 200)
	}
	path := "/api/v1/alerts/supervision-leads"
	list := readAlertTest[struct {
		Items []struct{ AlertID, Status string }
	}](
		t, router, "GET", path, "", 200)
	if len(list.Items) != 1 || list.Items[0].Status != "pending" {
		t.Fatalf("监管线索数量或状态错误: %+v", list.Items)
	}
	id := list.Items[0].AlertID
	readAlertTest[actionResult](t, router, "POST", "/api/v1/alerts/"+id+"/actions",
		`{"action":"close","expectedVersion":1,"comment":"线索已核对"}`, 200)
	closed := readAlertTest[struct {
		Items []struct{ AlertID, Status string }
	}](
		t, router, "GET", path, "", 200)
	if len(closed.Items) != 1 || closed.Items[0].AlertID != id || closed.Items[0].Status != "closed" {
		t.Fatalf("已关闭线索的历史无法追溯: %+v", closed.Items)
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().In(location).Format(time.DateOnly)
	base := fmt.Sprintf("/api/v1/operator/alerts/export?from=%s&to=%s", today, today)
	assertScopedAlertExport(t, router, base)
}

// assertScopedAlertExport 核对导出按单位隔离并拒绝未声明的范围或倒序日期。
// 参数 t 为测试上下文，router 为业务路由，path 为同一合法日期区间。
// 返回值 无。
func assertScopedAlertExport(t *testing.T, router http.Handler, path string) {
	t.Helper()
	own := alarmRequest(router, "GET", path+"&organization=隔离单位", "")
	other := alarmRequest(router, "GET", path+"&organization=其他单位", "")
	invalid := alarmRequest(router, "GET", path, "")
	if own.Code != 200 || other.Code != 200 || invalid.Code != 400 ||
		!strings.Contains(own.Body.String(), "BA-01") || strings.Contains(other.Body.String(), "BA-01") ||
		!strings.Contains(own.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("导出范围或校验错误: %d,%d,%d %s", own.Code, other.Code, invalid.Code, own.Body.String())
	}
}
