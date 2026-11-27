package workflowreminders

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const retestReminderURL = "/api/v1/operator/workbench/retest-reminders"

// TestRetestReminderRejectsInvalidRequest 验证空范围、重复范围和非精确请求体均在查库前拒绝。
// 参数 t 为 HTTP 契约测试上下文。
// 返回值 无。
func TestRetestReminderRejectsInvalidRequest(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, nil)
	valid := `{"id":"00000000-0000-4000-8000-000000000001"}`
	for _, test := range []struct{ query, body string }{
		{"", valid},
		{"?organization=甲&organization=乙", valid},
		{"?organization=甲&institution=乙", valid},
		{"?organization=甲", `{"id":"invalid"}`},
		{"?organization=甲", `{"id":"00000000_0000-4000-8000-000000000001"}`},
		{"?organization=甲", `{"ID":"00000000-0000-4000-8000-000000000001"}`},
		{"?organization=甲", `{"id":"00000000-0000-4000-8000-000000000001","recipient":"乙"}`},
		{"?organization=甲", valid + ` {}`},
	} {
		response := postReminder(mux, test.query, test.body)
		if response.Code != http.StatusBadRequest {
			t.Errorf("query=%q body=%q: status=%d, body=%s",
				test.query, test.body, response.Code, response.Body)
		}
	}
}

// postReminder 调用公开的运营者复测催办接口。
// 参数 mux 为被测业务路由，query 和 body 分别为范围查询和原始正文。
// 返回值 捕获的响应。
func postReminder(mux http.Handler, query, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, retestReminderURL+query, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}
