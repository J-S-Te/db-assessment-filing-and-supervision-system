package tagcatalog

import (
	"encoding/base64"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
)

const maxNameRunes = 32

var colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
var errInvalid = errors.New("invalid tag catalog input")
var errVersion = errors.New("tag catalog version was changed")

type catalogEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Color      string `json:"color"`
	Active     bool   `json:"active"`
	UsageCount int64  `json:"usageCount"`
	Version    int64  `json:"version"`
	key        string
}

type createInput struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type patchInput struct {
	Color           string `json:"color"`
	Active          *bool  `json:"active"`
	ExpectedVersion int64  `json:"expectedVersion"`
}

type listOptions struct {
	keyword string
	status  string
	after   string
	limit   int
}

type catalogPage struct {
	Items      []catalogEntry `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

// NormalizeName 按现有知识标签规范清理并校验共享目录名称。
// 参数 raw 为表单或导入使用的原始标签文本。
// 返回值 去掉首尾空白的原名称与名称是否合法。
func NormalizeName(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	return name, name != "" && validTerm(name, maxNameRunes)
}

// IsDisabledAssignment 判断知识写入是否尝试新关联已停用的标签。
// 参数 err 为知识维护或导入事务的错误。
// 返回值 命中标签目录约束时为真。
func IsDisabledAssignment(err error) bool {
	return constraint(err, "23514", "knowledge_tag_active_assignment")
}

// constraint 识别目录数据库约束返回的稳定错误。
// 参数 err 为数据库错误，code 及 name 为对应 SQLSTATE 与约束。
// 返回值 属于指定约束错误时为真。
func constraint(err error, code, name string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code && pgErr.ConstraintName == name
}

// validTerm 限制单行搜索词和标签的编码及字符数。
// 参数 term 为文字，maximum 为字符数限制。
// 返回值 文字符合单行和长度规范时为真。
func validTerm(term string, maximum int) bool {
	return utf8.ValidString(term) && utf8.RuneCountInString(term) <= maximum && !strings.ContainsFunc(term, unicode.IsControl)
}

// parseList 校验目录的筛选字段、每页数量和名称游标。
// 参数 raw 为 HTTP 原始查询字符串。
// 返回值 已校验的查询条件或输入不合法错误。
func parseList(raw string) (listOptions, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return listOptions{}, errInvalid
	}
	for key, items := range values {
		if len(items) != 1 || (key != "q" && key != "status" && key != "cursor" && key != "limit") {
			return listOptions{}, errInvalid
		}
	}
	input := listOptions{keyword: strings.TrimSpace(values.Get("q")), status: values.Get("status"), limit: 20}
	if !validTerm(input.keyword, 120) || (input.status != "" && input.status != "active" && input.status != "disabled") {
		return listOptions{}, errInvalid
	}
	if values.Has("limit") {
		input.limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil || input.limit < 1 || input.limit > 100 {
			return listOptions{}, errInvalid
		}
	}
	input.after, err = decodeKey(values.Get("cursor"))
	return input, err
}

// decodeKey 还原目录上页的名称排序位置。
// 参数 raw 为 URL 安全的游标，空值读取首屏。
// 返回值 排序条件或非法游标错误。
func decodeKey(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if len(raw) > base64.RawURLEncoding.EncodedLen(maxNameRunes*utf8.UTFMax) {
		return "", errInvalid
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	value := string(decoded)
	name, valid := NormalizeName(value)
	if err != nil || !valid || name != value || base64.RawURLEncoding.EncodeToString(decoded) != raw {
		return "", errInvalid
	}
	return value, nil
}
