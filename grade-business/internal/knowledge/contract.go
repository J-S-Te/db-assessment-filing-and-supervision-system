package knowledge

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"grade-business/internal/tagcatalog"
)

const (
	maxBodySize   = 32 << 10
	defaultLimit  = 20
	maxLimit      = 100
	maxQueryRunes = 120
	maxCursorSize = 128
	maxTags       = 12
)

type createInput struct {
	Title    string `json:"title"`
	Category string `json:"category"`
	Source   string `json:"source"`
	Body     string `json:"body"`
}

type createResult struct {
	ID string `json:"id"`
}

type updateInput struct {
	createInput
	ExpectedVersion int64 `json:"expectedVersion"`
}

type tagsInput struct {
	Tags            []string `json:"tags"`
	ExpectedVersion int64    `json:"expectedVersion"`
}

type updateResult struct {
	Version int64 `json:"version"`
}

type summary struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Category   string     `json:"category"`
	Source     string     `json:"source"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	Tags       []string   `json:"tags"`
	InactiveAt *time.Time `json:"inactiveAt,omitempty"`
}

type entry struct {
	summary
	Body    string `json:"body"`
	Version int64  `json:"version"`
}

type page struct {
	Items      []summary `json:"items"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

type pageAnchor struct {
	at time.Time
	id pgtype.UUID
}

type listQuery struct {
	category string
	source   string
	status   string
	terms    []string
	anchor   pageAnchor
	limit    int
}

// normalizeTags 清理标签两端空白并拒绝同一条目中的重复标签。
// 参数 tags 为维护页完整提交的标签列表，空数组表示清除已有标签。
// 返回值 规范化的标签列表和可展示的校验错误。
func normalizeTags(tags []string) ([]string, error) {
	if tags == nil || len(tags) > maxTags {
		return nil, errors.New("请提交有效的标签列表，最多 12 个标签。")
	}
	used := make(map[string]bool, len(tags))
	for i, tag := range tags {
		value, valid := tagcatalog.NormalizeName(tag)
		if !valid {
			return nil, errors.New("每个标签须为 1 到 32 字符的单行文本。")
		}
		key := strings.ToLower(value)
		if used[key] {
			return nil, errors.New("同一条目不能添加重复标签。")
		}
		tags[i] = value
		used[key] = true
	}
	return tags, nil
}

// validateCreate 清理标题与来源并校验分类及纯文本正文。
// 参数 input 为新增条目的输入字段。
// 返回值 清理后的条目和可直接展示的字段错误。
func validateCreate(input createInput) (createInput, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Source = strings.TrimSpace(input.Source)
	if !validLabel(input.Title, 200) {
		return createInput{}, errors.New("标题必填且须为 200 字符以内的单行文本。")
	}
	if !validCategory(input.Category) {
		return createInput{}, errors.New("请选择有效的知识库分类。")
	}
	if !validLabel(input.Source, 500) {
		return createInput{}, errors.New("来源必填且须为 500 字符以内的单行文本。")
	}
	if len(input.Body) > maxBodySize || strings.TrimSpace(input.Body) == "" || !validText(input.Body) {
		return createInput{}, errors.New("正文必填，须为 32 KiB 以内的 UTF-8 文本。")
	}
	return input, nil
}

// validLabel 检查单行标题或来源是否可保存。
// 参数 value 为业务标签，maximum 为字符数上限。
// 返回值 标签满足非空、有效 UTF-8 且无控制字符时为真。
func validLabel(value string, maximum int) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= maximum &&
		!strings.ContainsFunc(value, unicode.IsControl)
}

// validText 检查正文字符，同时允许文本文件中的换行和制表符。
// 参数 value 为待保存的正文原文。
// 返回值 正文使用有效 UTF-8 且不存在不支持的控制字符时为真。
func validText(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
	})
}

// validCategory 判断分类是否属于本期知识库分类。
// 参数 value 为前端提交的分类代码。
// 返回值 分类已定义时为真。
func validCategory(value string) bool {
	switch value {
	case "regulation", "standard", "vulnerability", "case", "template":
		return true
	default:
		return false
	}
}

// parseListQuery 校验检索、分类、来源和分页字段并准备数据库查询条件。
// 参数 values 为请求中解码后的查询参数。
// 返回值 可执行的筛选条件和输入不合法时的错误。
func parseListQuery(values url.Values) (listQuery, error) {
	for key, entries := range values {
		if len(entries) != 1 {
			return listQuery{}, errors.New("列表筛选参数无效。")
		}
		switch key {
		case "q", "category", "source", "status", "cursor", "limit":
		default:
			return listQuery{}, errors.New("列表筛选参数无效。")
		}
	}
	q := values.Get("q")
	if utf8.RuneCountInString(q) > maxQueryRunes || !validText(q) {
		return listQuery{}, errors.New("关键词须为 120 字符以内的文本。")
	}
	category := values.Get("category")
	if category != "" && !validCategory(category) {
		return listQuery{}, errors.New("请选择有效的知识库分类。")
	}
	source := strings.TrimSpace(values.Get("source"))
	if source != "" && !validLabel(source, 500) {
		return listQuery{}, errors.New("来源须为 500 字符以内的单行文本。")
	}
	status := values.Get("status")
	switch status {
	case "":
		status = "active"
	case "active", "inactive", "all":
	default:
		return listQuery{}, errors.New("请选择有效的知识条目状态。")
	}
	limit, err := parseLimit(values.Get("limit"))
	if err != nil {
		return listQuery{}, err
	}
	anchor, err := decodeCursor(values.Get("cursor"))
	if err != nil {
		return listQuery{}, err
	}
	return listQuery{category: category, source: source, status: status, terms: strings.Fields(q), anchor: anchor, limit: limit}, nil
}

// parseLimit 解析列表页大小。
// 参数 raw 为原始记录数，空值使用默认配置。
// 返回值 每页条数和超出有效范围时的错误。
func parseLimit(raw string) (int, error) {
	if raw == "" {
		return defaultLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > maxLimit {
		return 0, errors.New("每页条数须为 1 到 100 之间的整数。")
	}
	return limit, nil
}

// parseID 将路径或分页中的 UUID 转为数据库参数。
// 参数 raw 为公开条目标识。
// 返回值 数据库 UUID 和公开标识是否采用有效格式。
func parseID(raw string) (pgtype.UUID, bool) {
	var id pgtype.UUID
	if len(raw) != 36 || raw[8] != '-' || raw[13] != '-' || raw[18] != '-' || raw[23] != '-' {
		return id, false
	}
	err := id.Scan(raw)
	return id, err == nil && id.Valid
}

// encodeCursor 生成列表继续读取的位置。
// 参数 at 与 id 为当前页最后一项的更新时间和标识。
// 返回值 可用于 URL 查询字段的分页值。
func encodeCursor(at time.Time, id string) string {
	value := at.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

// decodeCursor 读取客户端返回的排序位置。
// 参数 raw 为列表提供的分页值，空值代表从首页开始。
// 返回值 排序位置和无效分页值错误。
func decodeCursor(raw string) (pageAnchor, error) {
	if raw == "" {
		return pageAnchor{}, nil
	}
	invalid := errors.New("分页游标无效，请重新查询。")
	if len(raw) > maxCursorSize {
		return pageAnchor{}, invalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return pageAnchor{}, invalid
	}
	date, key, found := strings.Cut(string(decoded), "|")
	at, dateErr := time.Parse(time.RFC3339Nano, date)
	id, validID := parseID(key)
	if !found || dateErr != nil || at.IsZero() || !validID {
		return pageAnchor{}, invalid
	}
	return pageAnchor{at: at, id: id}, nil
}
