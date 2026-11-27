package reviewinbox

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

var errInvalid = errors.New("invalid review inbox scope")

// Scope 核对单个本地机构或运营单位的访问范围。
// 参数 r 为业务请求，key 为该端接受的机构或单位名称参数。
// 返回值 规范化名称或无效输入错误。
func Scope(r *http.Request, key string) (string, error) {
	name, _, err := scopeQuery(r.URL.RawQuery, key, false)
	return name, err
}

// scopeQuery 校验归属与可选分页条件，拒绝重复字段和未知条件。
// 参数 raw 为原始查询字符串，key 为范围字段，allowCursor 表示是否为收件箱列表。
// 返回值 归属、游标或参数错误。
func scopeQuery(raw, key string, allowCursor bool) (string, int64, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "", 0, errInvalid
	}
	for field, items := range values {
		if len(items) != 1 || (field != key && !(allowCursor && field == "before")) {
			return "", 0, errInvalid
		}
	}
	name := strings.TrimSpace(values.Get(key))
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 200 ||
		strings.ContainsRune(name, 0) {
		return "", 0, errInvalid
	}
	rawCursor := values.Get("before")
	if rawCursor == "" {
		return name, 0, nil
	}
	cursor, err := strconv.ParseInt(rawCursor, 10, 64)
	if err != nil || cursor < 1 || rawCursor != strconv.FormatInt(cursor, 10) {
		return "", 0, errInvalid
	}
	return name, cursor, nil
}
