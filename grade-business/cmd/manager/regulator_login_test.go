package main

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"grade-business/internal/access"
	"grade-business/migrations"
)

// TestRegulatorLoginAreaBinding 验证共享登录入口按监管账号绑定的区划签发会话。
// 参数 t 为测试上下文，使用 GRADE_ACCESS_TEST_DSN 指向隔离库。
// 返回值 无。
func TestRegulatorLoginAreaBinding(t *testing.T) {
	dsn := os.Getenv("GRADE_ACCESS_TEST_DSN")
	if dsn == "" {
		t.Skip("需要单独隔离的 GRADE_ACCESS_TEST_DSN")
	}
	db := accessTestSchema(t, dsn)
	if err := migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	accounts := []access.AccountInput{
		{Username: "login-city", Name: "市本级", Role: access.Regulator, AreaCode: "330800"},
		{Username: "login-county", Name: "柯城区", Role: access.Regulator, AreaCode: "330802"},
		{Username: "login-admin", Name: "平台管理员", Role: access.SystemAdmin},
	}
	for _, account := range accounts {
		account.PersonKey, account.Password = account.Username, testInitialPassword
		if _, err := access.Provision(t.Context(), db, account); err != nil {
			t.Fatal(err)
		}
	}
	handler := access.New(db).Protect(http.NewServeMux())
	for _, scenario := range []struct {
		name, username, area, password string
		want                           int
	}{
		{"市本级不能选下辖区", "login-city", "330802", testInitialPassword, 401},
		{"市本级正确", "login-city", "330800", testInitialPassword, 200},
		{"同城不同区不能登录", "login-county", "330803", testInitialPassword, 401},
		{"缺少辖区不能登录", "login-county", "", testInitialPassword, 401},
		{"辖区正确但密码错误", "login-county", "330802", "synthetic-wrong-secret", 401},
		{"区县正确", "login-county", "330802", testInitialPassword, 200},
		{"管理员不能选择区县", "login-admin", "330802", testInitialPassword, 401},
		{"管理员仍走普通入口", "login-admin", "", testInitialPassword, 200},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			hit := accessRequest(t, handler)
			body := map[string]string{"username": scenario.username, "password": scenario.password}
			if scenario.area != "" {
				body["areaCode"] = scenario.area
			}
			result := hit(nil, "POST", "/api/v1/auth/login", body, scenario.want)
			if got := len(result.Result().Cookies()); (got == 1) != (scenario.want == 200) {
				t.Fatalf("会话签发不符：%d 张 Cookie", got)
			}
		})
	}
}

// TestPublicLoginAreasMatchDashboard 验证匿名登录选项与发布的驾驶舱地图保持一致。
// 参数 t 提供匿名 HTTP 路由和发布地图的测试上下文。
// 返回值 无。
func TestPublicLoginAreasMatchDashboard(t *testing.T) {
	mapData, err := os.ReadFile("../../web/src/assets/district-boundaries.geojson")
	if err != nil {
		t.Fatal(err)
	}
	var boundary struct {
		Features []struct {
			Properties struct{ Code, Name string }
		}
	}
	if err := json.Unmarshal(mapData, &boundary); err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{"330800": "衢州市本级"}
	for _, feature := range boundary.Features {
		expected[feature.Properties.Code] = feature.Properties.Name
	}

	hit := accessRequest(t, access.New(nil).Protect(http.NewServeMux()))
	var response struct {
		Items []struct{ Code, Label string }
	}
	body := hit(nil, "GET", "/api/v1/auth/login-areas", nil, 200).Body.Bytes()
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != len(expected) {
		t.Fatalf("登录辖区有 %d 项，地图范围应有 %d 项", len(response.Items), len(expected))
	}
	for _, option := range response.Items {
		if want, ok := expected[option.Code]; !ok || option.Label != want {
			t.Errorf("登录选项 %s %q 与地图范围不符", option.Code, option.Label)
		}
		delete(expected, option.Code)
	}
}
