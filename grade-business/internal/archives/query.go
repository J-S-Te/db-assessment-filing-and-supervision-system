package archives

import (
	"encoding/base64"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
)

const defaultPageSize = 20
const organizationNameLimit = 200

var errInvalid = errors.New("invalid archive parameters")

type archiveScope struct {
	organization string
	filingID     pgtype.UUID
}

type timedPosition struct {
	stamp pgtype.Timestamptz
	id    pgtype.UUID
}

type listOptions struct {
	keyword, status, level string
	unitAfter              string
	after                  timedPosition
	limit                  int
}

type detailOptions struct {
	systems, reports, remediations, hazards, assetRecords timedPosition
}

// listParameters 校验列表从关键词到分页位置的组合条件。
// 参数 raw 为 HTTP 查询字符串，systems 表示查询系统档案。
// 返回值 列表选项或非法输入错误。
func listParameters(raw string, systems bool) (listOptions, error) {
	keys := []string{"q", "cursor", "limit"}
	if systems {
		keys = append(keys, "status", "level")
	}
	values, err := strictQuery(raw, keys...)
	if err != nil {
		return listOptions{}, err
	}
	input := listOptions{keyword: strings.TrimSpace(values.Get("q")), status: values.Get("status"),
		level: values.Get("level"), limit: defaultPageSize}
	if !boundedName(input.keyword, 120) || (input.status != "" && input.status != "active" && input.status != "cancelled") ||
		(input.level != "" && !slices.Contains([]string{"1", "2", "3", "4", "5"}, input.level)) {
		return listOptions{}, errInvalid
	}
	if values.Has("limit") {
		input.limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil || input.limit < 1 || input.limit > 100 {
			return listOptions{}, errInvalid
		}
	}
	if cursor := values.Get("cursor"); cursor != "" {
		if systems {
			input.after, err = readTimedPosition(cursor)
		} else {
			input.unitAfter, err = decodeGroupKey(cursor)
		}
	}
	return input, err
}

// detailParameters 读取详情各分区的独立游标。
// 参数 raw 为 HTTP 查询字符串，unit 表示是否有所属系统分页。
// 返回值 各分区位置或非法输入错误。
func detailParameters(raw string, unit bool) (detailOptions, error) {
	var result detailOptions
	keys := []string{"reportsCursor", "remediationsCursor", "hazardsCursor"}
	positions := []*timedPosition{&result.reports, &result.remediations, &result.hazards}
	if unit {
		keys = append(keys, "systemsCursor", "assetRecordsCursor")
		positions = append(positions, &result.systems, &result.assetRecords)
	}
	values, err := strictQuery(raw, keys...)
	if err != nil {
		return detailOptions{}, err
	}
	for index, key := range keys {
		*positions[index], err = readTimedPosition(values.Get(key))
		if err != nil {
			return detailOptions{}, err
		}
	}
	return result, nil
}

// strictQuery 解析白名单内的唯一查询参数。
// 参数 raw 为原始查询，keys 为该接口实际接受的字段。
// 返回值 已校验的查询或参数错误。
func strictQuery(raw string, keys ...string) (url.Values, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, errInvalid
	}
	for key, entries := range values {
		if len(entries) != 1 || !slices.Contains(keys, key) {
			return nil, errInvalid
		}
	}
	return values, nil
}

// boundedName 检查名称或关键词的编码、长度和数据库可用性。
// 参数 name 为待检查文字，limit 为字符上限。
// 返回值 文字可作为查询条件时为真。
func boundedName(name string, limit int) bool {
	return utf8.ValidString(name) && utf8.RuneCountInString(name) <= limit && !strings.ContainsRune(name, 0)
}

// decodeGroupKey 从 URL 安全的路由键还原原始备案名称。
// 参数 raw 为客户端携带的名称键或单位列表游标。
// 返回值 原样名称或非法编码错误。
func decodeGroupKey(raw string) (string, error) {
	if len(raw) > base64.RawURLEncoding.EncodedLen(organizationNameLimit*utf8.UTFMax) {
		return "", errInvalid
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	name := string(decoded)
	if err != nil || name == "" || !boundedName(name, organizationNameLimit) || groupKey(name) != raw {
		return "", errInvalid
	}
	return name, nil
}

// groupKey 生成可在 URL 单个路径片段安全传递的备案名称键。
// 参数 name 为备案中保存的精确单位名称。
// 返回值 可逆的无填充 URL 安全编码。
func groupKey(name string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(name))
}

// parseArchiveID 解析档案及其关联业务记录使用的 UUID。
// 参数 raw 为请求路径或游标中的公开标识。
// 返回值 已验证的 UUID 或参数错误。
func parseArchiveID(raw string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if len(raw) != 36 || raw[8] != '-' || raw[13] != '-' || raw[18] != '-' || raw[23] != '-' || id.Scan(raw) != nil {
		return id, errInvalid
	}
	return id, nil
}

// readTimedPosition 还原按时间和标识排序的下一页位置。
// 参数 raw 为档案接口返回的游标，缺省读取首页。
// 返回值 分页位置或非法编码错误。
func readTimedPosition(raw string) (timedPosition, error) {
	if raw == "" {
		return timedPosition{}, nil
	}
	if len(raw) > 120 {
		return timedPosition{}, errInvalid
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	stamp, key, ok := strings.Cut(string(decoded), "|")
	if err != nil || !ok {
		return timedPosition{}, errInvalid
	}
	at, timeError := time.Parse(time.RFC3339Nano, stamp)
	id, idError := parseArchiveID(key)
	if timeError != nil || idError != nil {
		return timedPosition{}, errInvalid
	}
	return timedPosition{stamp: pgtype.Timestamptz{Time: at, Valid: true}, id: id}, nil
}

// cursor 生成当前记录的时间和标识组成的分页游标。
// 参数 无，使用记录查询时的排序时间和标识。
// 返回值 可用作下页位置的 URL 安全游标。
func (row rowOrder) cursor() string {
	return base64.RawURLEncoding.EncodeToString([]byte(row.SortAt.UTC().Format(time.RFC3339Nano) + "|" + row.ID))
}
