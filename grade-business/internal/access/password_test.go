package access

import (
	"testing"
	"unicode"
)

// TestStrongPassword 核对新设口令的长度与字符类别复杂度要求。
// 参数 t 用于报告不符合复杂度要求的判定偏差。
// 返回值 无。
func TestStrongPassword(t *testing.T) {
	for _, item := range []struct {
		password string
		want     bool
	}{
		{"Abcdefghij12", true},
		{"Abcdefghij12!@#", true},
		{"密码Abcdefghij12", true},
		{"abcdefghij12", false},
		{"Abcdefghijkl", false},
		{"Abc12!", false},
		{"", false},
	} {
		if got := strongPassword(item.password); got != item.want {
			t.Errorf("strongPassword(%q)=%v，应为 %v", item.password, got, item.want)
		}
	}
}

// TestTemporaryPassword 验证系统临时口令满足新口令复杂度且逐次随机。
// 参数 t 用于报告临时口令类别缺失或重复生成。
// 返回值 无。
func TestTemporaryPassword(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		password, err := temporaryPassword()
		if err != nil {
			t.Fatal(err)
		}
		if !strongPassword(password) {
			t.Fatalf("临时口令 %q 未满足复杂度要求", password)
		}
		var upper, lower, digit bool
		for _, r := range password {
			switch {
			case unicode.IsUpper(r):
				upper = true
			case unicode.IsLower(r):
				lower = true
			case unicode.IsDigit(r):
				digit = true
			}
		}
		if !upper || !lower || !digit {
			t.Fatalf("临时口令 %q 缺少必需字符类别", password)
		}
		if seen[password] {
			t.Fatalf("临时口令 %q 重复生成", password)
		}
		seen[password] = true
	}
}
