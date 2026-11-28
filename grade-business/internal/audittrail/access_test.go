package audittrail

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"grade-business/internal/access"
)

// TestAuditShowsSanitizedAccessRejections 验证真实网关留痕只向管理员公开安全摘要。
// 参数 t 为隔离数据库中的请求和验证上下文。
// 返回值 无。
func TestAuditShowsSanitizedAccessRejections(t *testing.T) {
	pool := auditFixture(t)
	mux := http.NewServeMux()
	New(pool).Register(mux)
	handler := access.New(pool).Protect(mux)
	const username, password = "audit-reviewer", "Audit-test-8952!"
	id, err := access.Provision(t.Context(), pool, access.AccountInput{
		Username: username, Name: "审计测试管理员", Role: access.SystemAdmin, PersonKey: username, Password: password,
	})
	if err != nil {
		t.Fatal(err)
	}
	loginBody := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	for range 14 {
		request := accessTestRequest(handler, "POST", "/api/v1/auth/login",
			fmt.Sprintf(`{"username":%q,"password":"x"}`, username), nil)
		if request.Code != 401 && request.Code != 429 {
			t.Fatalf("登录拒绝状态 = %d", request.Code)
		}
	}
	valid := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
	valid.RemoteAddr = "192.0.2.20:1234"
	valid.Header.Set("Content-Type", "application/json")
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, valid)
	if login.Code != 200 || len(login.Result().Cookies()) != 1 {
		t.Fatalf("登录状态 = %d", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	if denial := accessTestRequest(handler, "GET", auditEndpoint, "", cookie); denial.Code != 403 {
		t.Fatalf("未完成首次改密时读取审计状态 = %d", denial.Code)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE access_accounts SET must_change_password=false WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	const sensitive = "SYNTHETIC_PRIVATE_TARGET"
	if _, err := pool.Exec(t.Context(), `INSERT INTO access_events(event,target) VALUES ('denied',$1),('unclassified',$1)`,
		sensitive); err != nil {
		t.Fatal(err)
	}
	result := readAuditTestWithCookie(t, handler, auditEndpoint+"?resource=access&limit=100", cookie)
	for _, fragment := range []string{`"action":"login_failed"`, `"action":"denied"`, `"operator":"审计测试管理员"`} {
		if !strings.Contains(result, fragment) {
			t.Errorf("审计缺少必要摘要 %s", fragment)
		}
	}
	if got := strings.Count(result, `"action":"login_throttled"`); got != 1 {
		t.Errorf("限流窗口内安全审计数量 = %d，预期 1", got)
	}
	csv := readAuditTestWithCookie(t, handler, auditEndpoint+"/export?resource=access", cookie)
	if !strings.Contains(csv, "账号与权限") || !strings.Contains(csv, "登录尝试限流") {
		t.Error("安全审计导出缺少业务类型和限流动作")
	}
	search := readAuditTestWithCookie(t, handler, auditEndpoint+"?resource=access&q="+sensitive, cookie)
	if !strings.Contains(search, `"items":[]`) {
		t.Error("安全事件不得使用内部目标参与检索")
	}
	for _, secret := range []string{username, password, sensitive, "unclassified", cookie.Value, id} {
		if strings.Contains(result, secret) || strings.Contains(csv, secret) {
			t.Error("安全事件的内部字段出现在列表或导出")
		}
	}
}

// TestSessionTracksLatestActivityWithinConfiguredIdle 验证已接受请求从真实活跃时间重新计算空闲期。
// 参数 t 为独立库中的登录和会话边界测试。
// 返回值 无。
func TestSessionTracksLatestActivityWithinConfiguredIdle(t *testing.T) {
	pool := auditFixture(t)
	const username, password = "session-reviewer", "Session-test-8952!"
	id, err := access.Provision(t.Context(), pool, access.AccountInput{
		Username: username, Name: "会话测试管理员", Role: access.SystemAdmin, PersonKey: username, Password: password,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, minutes := range []int{1, 30, 720} {
		t.Run(fmt.Sprintf("%d分钟", minutes), func(t *testing.T) {
			handler := access.New(pool, access.Options{IdleTimeout: time.Duration(minutes) * time.Minute}).
				Protect(http.NewServeMux())
			login := accessTestRequest(handler, "POST", "/api/v1/auth/login",
				fmt.Sprintf(`{"username":%q,"password":%q}`, username, password), nil)
			if login.Code != 200 || len(login.Result().Cookies()) != 1 {
				t.Fatalf("登录状态 = %d", login.Code)
			}
			cookie := login.Result().Cookies()[0]
			if _, err := pool.Exec(t.Context(), `UPDATE access_sessions
				SET last_seen_at=statement_timestamp()-interval '30 seconds' WHERE account_id=$1`, id); err != nil {
				t.Fatal(err)
			}
			if active := accessTestRequest(handler, "GET", "/api/v1/auth/session", "", cookie); active.Code != 200 {
				t.Fatalf("配置空闲期内的会话状态 = %d", active.Code)
			}
			var refreshed bool
			if err := pool.QueryRow(t.Context(), `SELECT max(last_seen_at)>statement_timestamp()-interval '10 seconds'
				FROM access_sessions WHERE account_id=$1`, id).Scan(&refreshed); err != nil || !refreshed {
				t.Errorf("短间隔内的实际请求没有刷新活跃时间，查询错误 %v", err)
			}
			if _, err := pool.Exec(t.Context(), `UPDATE access_sessions
				SET last_seen_at=statement_timestamp()-make_interval(mins => $2) WHERE account_id=$1`,
				id, minutes); err != nil {
				t.Fatal(err)
			}
			if idle := accessTestRequest(handler, "GET", "/api/v1/auth/session", "", cookie); idle.Code != 401 {
				t.Fatalf("空闲期到达后的会话状态 = %d", idle.Code)
			}
		})
	}
}

// accessTestRequest 将安全测试请求直接交给实际认证及审计路由。
// 参数 handler 为受保护的路由，method、path、body 为请求字段，cookie 为可选的测试会话。
// 返回值 响应记录。
func accessTestRequest(handler http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// readAuditTestWithCookie 读取经认证的安全审计响应。
// 参数 t 为验证上下文，handler、path 为受保护的列表或下载地址，cookie 为管理员会话。
// 返回值 响应文本。
func readAuditTestWithCookie(t *testing.T, handler http.Handler, path string, cookie *http.Cookie) string {
	t.Helper()
	response := accessTestRequest(handler, "GET", path, "", cookie)
	if response.Code != 200 {
		t.Fatalf("安全审计 %s 状态 = %d", path, response.Code)
	}
	return response.Body.String()
}
