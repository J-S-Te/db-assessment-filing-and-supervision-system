package access

import (
	"net/http/httptest"
	"net/netip"
	"strconv"
	"testing"
	"time"
)

// TestLoginLimitsAreSeparatedBySource 验证同一账号在不同来源拥有独立额度。
// 参数 t 为登录限流测试上下文。
// 返回值 无。
func TestLoginLimitsAreSeparatedBySource(t *testing.T) {
	service := New(nil)
	for range 12 {
		if allowed, _ := service.limitLogin("192.0.2.1:1000", "fixture"); !allowed {
			t.Fatal("合法窗口额度未能使用")
		}
	}
	firstSource, firstReport := service.limitLogin("192.0.2.1:1001", "fixture")
	retry, retryReport := service.limitLogin("192.0.2.1:1002", "fixture")
	secondSource, _ := service.limitLogin("192.0.2.2:1000", "fixture")
	if firstSource || retry || !secondSource {
		t.Fatal("账号额度必须按来源隔离，同一来源不能靠端口换桶")
	}
	if !firstReport || retryReport {
		t.Fatal("同一来源窗口仅首次限流应记录安全审计")
	}
}

// TestFullLimiterKeepsExistingSources 验证达到来源容量后已有来源的额度仍可使用。
// 参数 t 为限流容量测试上下文。
// 返回值 无。
func TestFullLimiterKeepsExistingSources(t *testing.T) {
	service := New(nil)
	for index := range 4096 {
		service.limitLogin("source-"+strconv.Itoa(index), "fixture-"+strconv.Itoa(index))
	}
	if allowed, _ := service.limitLogin("source-0", "another-fixture"); !allowed {
		t.Fatal("已有来源被卡在表容量限制")
	}
	if allowed, _ := service.limitLogin("new-source", "fixture"); allowed {
		t.Fatal("新增来源不应超出内存容量")
	}
}

// TestExpiredLimiterEntriesReleaseCapacity 验证过期匿名来源释放容量。
// 参数 t 为限流窗口测试上下文。
// 返回值 无。
func TestExpiredLimiterEntriesReleaseCapacity(t *testing.T) {
	service := New(nil)
	for index := range 4096 {
		service.registrationRates[strconv.Itoa(index)] = loginWindow{until: time.Now().Add(-time.Minute), count: 15}
	}
	if !service.limitRegistration("192.0.2.1:1000", "submit", 15) {
		t.Fatal("过期窗口没有释放限流容量")
	}
}

// TestSessionCookieTransport 验证反代部署和直接 TLS 的 Cookie 安全属性。
// 参数 t 为模拟请求及 Cookie 测试上下文。
// 返回值 无。
func TestSessionCookieTransport(t *testing.T) {
	force := New(nil, Options{SecureCookies: true})
	for _, item := range []struct {
		service *Service
		url     string
		secure  bool
	}{
		{force, "http://internal.example/", true},
		{New(nil), "https://test.example/", true},
		{New(nil), "http://localhost/", false},
	} {
		request := httptest.NewRequest("GET", item.url, nil)
		request.Header.Set("X-Forwarded-Proto", "https")
		response := httptest.NewRecorder()
		item.service.clearCookie(response, request)
		if cookie := response.Result().Cookies()[0]; cookie.Secure != item.secure || !cookie.HttpOnly {
			t.Errorf("地址 %q 的 Cookie 安全属性不正确", item.url)
		}
	}
}

// TestAuthSourceUsesOnlyTrustedProxy 验证直连、受信代理和无效转发头的来源规则。
// 参数 t 为反代信任规则的测试上下文。
// 返回值 无。
func TestAuthSourceUsesOnlyTrustedProxy(t *testing.T) {
	service := New(nil, Options{TrustedProxy: netip.MustParseAddr("192.0.2.10")})
	for _, item := range []struct {
		peer   string
		header []string
		want   string
	}{
		{"192.0.2.10:1000", []string{"198.51.100.10"}, "198.51.100.10"},
		{"192.0.2.10:1000", []string{"198.51.100.11"}, "198.51.100.11"},
		{"192.0.2.11:1000", []string{"198.51.100.10"}, "192.0.2.11"},
		{"192.0.2.10:1000", []string{"198.51.100.10", "198.51.100.11"}, "192.0.2.10"},
		{"192.0.2.10:1000", []string{"invalid"}, "192.0.2.10"},
	} {
		request := httptest.NewRequest("POST", "/", nil)
		request.RemoteAddr = item.peer
		for _, header := range item.header {
			request.Header.Add("X-Real-IP", header)
		}
		if got := service.authSource(request); got != item.want {
			t.Errorf("限流来源 %q，预期 %q", got, item.want)
		}
	}
}
