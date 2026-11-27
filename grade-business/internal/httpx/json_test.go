package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDecodeRejectsFieldsAndRepeatedValues 检查接口只接收声明的单个 JSON 对象。
// 参数 t 为测试上下文。
// 返回值 无。
func TestDecodeRejectsFieldsAndRepeatedValues(t *testing.T) {
	for _, row := range []struct {
		input string
		valid bool
	}{
		{`{"decision":"approve"}`, true},
		{`{"decision":"approve","clientIp":"192.0.2.10"}`, false},
		{`{"decision":"approve","decision":"return"}`, false},
		{`{"decision":"approve"} {}`, false},
		{`null`, false},
	} {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(row.input))
		r.Header.Set("Content-Type", "application/json")
		var input struct {
			Decision string `json:"decision"`
		}
		err := Decode(httptest.NewRecorder(), r, &input)
		if (err == nil) != row.valid {
			t.Errorf("input=%q error=%v", row.input, err)
		}
	}
}
