package reviewinbox

import (
	"net/http/httptest"
	"testing"
)

// TestScope 拒绝缺失、重复和无关的本地业务归属参数。
// 参数 t 为测试上下文。
// 返回值 无。
func TestScope(t *testing.T) {
	request := httptest.NewRequest("GET", "/?institution=%20%E6%9C%BA%E6%9E%84%20", nil)
	if scope, err := Scope(request, "institution"); err != nil || scope != "机构" {
		t.Fatalf("机构范围不匹配: %q %v", scope, err)
	}
	for _, query := range []string{"", "?institution=x&institution=y", "?institution=x&before=10"} {
		if _, err := Scope(httptest.NewRequest("GET", "/"+query, nil), "institution"); err == nil {
			t.Errorf("错误范围通过了校验: %q", query)
		}
	}
}
