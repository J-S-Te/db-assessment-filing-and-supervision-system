package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"
)

const maxBody = 256 << 10

var invalidObject = errors.New("invalid JSON request object")

type problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Decode 读取一个符合接口字段声明的 JSON 请求对象。
// 参数 w 为限制请求体的响应对象，r 为业务请求，dst 为目标输入结构。
// 返回值 请求超限、格式异常或包含额外字段时返回错误。
func Decode(w http.ResponseWriter, r *http.Request, dst any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return invalidObject
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		return err
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' || !utf8.Valid(body) {
		return invalidObject
	}
	if err := checkFieldKeys(body); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}

// checkFieldKeys 验证对象结构和字段键唯一性。
// 参数 body 为已限制长度的 JSON 字节。
// 返回值 重复字段、无效语法或第二个 JSON 值时返回错误。
func checkFieldKeys(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if _, err := decoder.Token(); err != nil {
		return invalidObject
	}
	used := map[string]struct{}{}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return invalidObject
		}
		key := strings.ToLower(name)
		if _, duplicate := used[key]; duplicate {
			return invalidObject
		}
		used[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return invalidObject
		}
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
		return invalidObject
	}
	if _, err := decoder.Token(); err != io.EOF {
		return invalidObject
	}
	return nil
}

// JSON 返回经过业务接口筛选的 JSON 数据。
// 参数 w 为响应对象，status 为状态码，value 为声明的响应结构。
// 返回值 无。
func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// Error 输出不携带内部数据的业务错误。
// 参数 w 为响应对象，status 为状态码，code 和 message 为对外错误信息。
// 返回值 无。
func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, problem{Code: code, Message: message})
}
