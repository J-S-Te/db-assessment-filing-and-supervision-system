package main

import "testing"

// TestDeploymentListener 验证内网部署使用服务器地址并保留已有服务端口。
// 参数 t 为测试上下文。
// 返回值 无。
func TestDeploymentListener(t *testing.T) {
	t.Setenv("GRADE_BUSINESS_DSN", "postgres://test")
	t.Setenv("GRADE_BUSINESS_ADDR", "127.0.0.1:18470")
	for _, row := range []struct {
		cidr    string
		address string
	}{
		{"", "127.0.0.1:18470"},
		{"192.168.3.22/24", "192.168.3.22:18470"},
		{"192.168.3.22/8", ""},
		{"8.8.8.8/24", ""},
		{"fd00::1/64", ""},
	} {
		t.Setenv("GRADE_BUSINESS_LAN_CIDR", row.cidr)
		cfg, err := loadSettings()
		if row.address == "" {
			if err == nil {
				t.Errorf("接受了无效的内网范围 %q", row.cidr)
			}
		} else if err != nil || cfg.address != row.address {
			t.Errorf("范围 %q 监听于 %q，错误 %v", row.cidr, cfg.address, err)
		}
	}
}

// TestSecureCookieDeploymentSetting 验证受控 HTTPS 反代可显式启用安全 Cookie。
// 参数 t 为启动配置的测试上下文。
// 返回值 无。
func TestSecureCookieDeploymentSetting(t *testing.T) {
	t.Setenv("GRADE_BUSINESS_DSN", "postgres://fixture")
	t.Setenv("GRADE_BUSINESS_COOKIE_SECURE", "true")
	t.Setenv("GRADE_BUSINESS_AUTH_TRUSTED_PROXY", "127.0.0.1")
	cfg, err := loadSettings()
	if err != nil || !cfg.accessOptions.SecureCookies || cfg.accessOptions.TrustedProxy.String() != "127.0.0.1" {
		t.Fatalf("安全 Cookie 配置未生效：%v", err)
	}
	t.Setenv("GRADE_BUSINESS_COOKIE_SECURE", "invalid")
	if _, err := loadSettings(); err == nil {
		t.Fatal("启动配置接受了无效的 Cookie 安全值")
	}
	t.Setenv("GRADE_BUSINESS_COOKIE_SECURE", "true")
	t.Setenv("GRADE_BUSINESS_AUTH_TRUSTED_PROXY", "0.0.0.0")
	if _, err := loadSettings(); err == nil {
		t.Fatal("启动配置接受了泛地址作为可信代理")
	}
}
