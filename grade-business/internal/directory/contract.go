package directory

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
)

const (
	unit        = "unit"
	institution = "institution"
	person      = "person"
	active      = "active"
	inactive    = "inactive"
	pageSize    = 30
	// businessToday 统一按业务发生地计算证书有效期，包含有效期当日。
	businessToday = "(clock_timestamp() AT TIME ZONE 'Asia/Shanghai')::date"
)

var (
	errInvalid         = errors.New("invalid directory request")
	errForbidden       = errors.New("directory entry cannot be maintained in this scope")
	errDistrictManaged = errors.New("unit district assignment is managed by the system administrator")
	errUnitCodeManaged = errors.New("verified unit credit code is managed by the system administrator")
	errVersion         = errors.New("directory version changed")
	errDuplicate       = errors.New("duplicate directory entry")
	errUnavailable     = errors.New("directory institution or unit unavailable")
	errInFlight        = errors.New("directory entry has in-flight business")
)

type entry struct {
	ID              string     `json:"id,omitempty"`
	Name            string     `json:"name"`
	ScopeName       string     `json:"scopeName,omitempty"`
	Code            string     `json:"code,omitempty"`
	CodeManaged     bool       `json:"codeManaged,omitempty"`
	Classification  string     `json:"classification,omitempty"`
	District        string     `json:"district,omitempty"`
	Qualification   string     `json:"qualification,omitempty"`
	ValidUntil      string     `json:"validUntil,omitempty"`
	InstitutionID   string     `json:"institutionId,omitempty"`
	InstitutionName string     `json:"institutionName,omitempty"`
	CertificateNo   string     `json:"certificateNo,omitempty"`
	Status          string     `json:"status"`
	Usable          bool       `json:"usable"`
	CanEdit         bool       `json:"canEdit"`
	Version         int64      `json:"version,omitempty"`
	SystemCount     int64      `json:"systemCount,omitempty"`
	InReviewCount   int64      `json:"inReviewCount,omitempty"`
	MergedIntoID    string     `json:"mergedIntoId,omitempty"`
	MergeID         string     `json:"mergeId,omitempty"`
	MergedAt        *time.Time `json:"mergedAt,omitempty"`
	ownerID         string
}

type option struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	ScopeName string `json:"scopeName,omitempty"`
	Code      string `json:"code,omitempty"`
	Usable    bool   `json:"usable"`
	Status    string `json:"status"`
}

type page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

type event struct {
	Sequence  int64     `json:"sequence"`
	Version   int64     `json:"version"`
	Action    string    `json:"action"`
	Actor     string    `json:"actor"`
	ChangedAt time.Time `json:"changedAt"`
	Before    *entry    `json:"before,omitempty"`
	After     entry     `json:"after"`
}

type change struct {
	ExpectedVersion int64   `json:"expectedVersion"`
	Name            *string `json:"name"`
	Code            *string `json:"code"`
	Classification  *string `json:"classification"`
	District        *string `json:"district"`
	Qualification   *string `json:"qualification"`
	ValidUntil      *string `json:"validUntil"`
	InstitutionID   *string `json:"institutionId"`
	CertificateNo   *string `json:"certificateNo"`
	Status          *string `json:"status"`
}

type listFilter struct {
	kind, scope, query, status, institutionID string
	cursorName, cursorKey                     string
	options                                   bool
}

// kindOfCollection 将公开复数路径映射为内部名录对象类型。
// 参数 collection 为 URL 枚举段。
// 返回值 规范类型，未知时为空。
func kindOfCollection(collection string) string {
	switch collection {
	case "units":
		return unit
	case "institutions":
		return institution
	case "people":
		return person
	default:
		return ""
	}
}

// validID 验证具有标准格式的 UUID。
// 参数 raw 为公开条目的路径标识。
// 返回值 格式合法且不是空引用时为真。
func validID(raw string) bool {
	if len(raw) != 36 || raw[8] != '-' || raw[13] != '-' || raw[18] != '-' || raw[23] != '-' {
		return false
	}
	var id pgtype.UUID
	return id.Scan(raw) == nil && id.Valid
}

// textAllowed 核对名录内容的编码、长度、控制字符和是否允许为空。
// 参数 raw 为已去首尾空白的文字，limit 为字符上限，optional 表示允许空字符串。
// 返回值 文字能够安全登记及展示时为真。
func textAllowed(raw string, limit int, optional bool) bool {
	return (optional || raw != "") && utf8.ValidString(raw) &&
		utf8.RuneCountInString(raw) <= limit && strings.IndexFunc(raw, unicode.IsControl) < 0
}

// validateChangeKind 禁止提交其他目录才有的字段及超出角色的归属变更。
// 参数 kind 为目标类型，scope 为机构范围，input 为请求修改。
// 返回值 字段集合及角色范围合法时为真。
func validateChangeKind(kind, scope string, input change) bool {
	switch kind {
	case unit:
		return input.Qualification == nil && input.ValidUntil == nil &&
			input.CertificateNo == nil && input.InstitutionID == nil
	case institution:
		return scope == "" && input.CertificateNo == nil && input.InstitutionID == nil
	case person:
		return input.Classification == nil && input.District == nil &&
			(scope == "" || input.InstitutionID == nil)
	default:
		return false
	}
}

// mergeChange 将请求中真正提交的字段更新到目录行，再统一本地命名和编码。
// 参数 current 为原值，input 为本次修改。
// 返回值 将要进行后续验证的目录记录。
func mergeChange(current entry, input change) entry {
	for _, update := range []struct{ newValue, oldValue *string }{
		{input.Name, &current.Name}, {input.Code, &current.Code}, {input.Classification, &current.Classification},
		{input.District, &current.District}, {input.Qualification, &current.Qualification},
		{input.ValidUntil, &current.ValidUntil}, {input.InstitutionID, &current.InstitutionID},
		{input.CertificateNo, &current.CertificateNo}, {input.Status, &current.Status},
	} {
		if update.newValue != nil {
			*update.oldValue = strings.TrimSpace(*update.newValue)
		}
	}
	current.Code = strings.ToUpper(current.Code)
	current.CertificateNo = strings.ToUpper(current.CertificateNo)
	return current
}

// validEntry 对照目录类型校验必填字段、属性和日期格式。
// 参数 kind 为记录类型，item 为规范化后的拟保存行。
// 返回值 记录的数据有效时为真。
func validEntry(kind string, item entry) bool {
	if !textAllowed(item.Name, 200, false) || !textAllowed(item.Code, 80, false) ||
		!textAllowed(item.Classification, 80, true) || !textAllowed(item.District, 80, true) ||
		!textAllowed(item.Qualification, 100, true) || !textAllowed(item.CertificateNo, 100, true) ||
		(item.Status != active && item.Status != inactive) {
		return false
	}
	if item.ValidUntil != "" {
		parsed, err := time.Parse(time.DateOnly, item.ValidUntil)
		if err != nil || parsed.Year() < 1 || parsed.Format(time.DateOnly) != item.ValidUntil {
			return false
		}
	}
	switch kind {
	case unit:
		return unitCodeAllowed(item.Code)
	case institution:
		return item.ValidUntil != ""
	case person:
		return validID(item.InstitutionID)
	default:
		return false
	}
}

// unitCodeAllowed 核对统一社会信用代码的输入长度及字符形状。
// 参数 code 为转为大写后的信用代码。
// 返回值 输入由 18 位大写英数字组成时为真。
func unitCodeAllowed(code string) bool {
	if len(code) != 18 {
		return false
	}
	for _, char := range code {
		if !('0' <= char && char <= '9' || 'A' <= char && char <= 'Z') {
			return false
		}
	}
	return true
}

// parseQuery 严格读取每个接口允许出现的查询字段。
// 参数 raw 为原始查询字符串，allowed 为允许的字段。
// 返回值 经去重核对的参数，或请求错误。
func parseQuery(raw string, allowed ...string) (url.Values, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, errInvalid
	}
	for key, list := range values {
		ok := false
		for _, field := range allowed {
			ok = ok || key == field
		}
		if !ok || len(list) != 1 {
			return nil, errInvalid
		}
	}
	return values, nil
}

// readScope 解析用于机构端隔离的稳定机构名称。
// 参数 values 为已做名称检查的查询参数，required 指该接口是否必须提供范围。
// 返回值 稳定机构业务范围，或请求错误。
func readScope(values url.Values, required bool) (string, error) {
	scope := strings.TrimSpace(values.Get("institution"))
	if !textAllowed(scope, 200, !required && !values.Has("institution")) {
		return "", errInvalid
	}
	return scope, nil
}

// prepareList 解析目录类型、范围、检索条件、状态及稳定翻页游标。
// 参数 kind 为条目类型，scope 为机构范围，values 为查询字段。
// 返回值 正规化列表条件或验证错误。
func prepareList(kind, scope string, values url.Values) (listFilter, error) {
	filter := listFilter{kind: kind, scope: scope, query: strings.TrimSpace(values.Get("q")),
		status: values.Get("status"), institutionID: values.Get("institutionId")}
	if filter.status == "" {
		filter.status = "all"
	}
	if !textAllowed(filter.query, 120, true) ||
		(filter.status != "all" && filter.status != active && filter.status != inactive) ||
		(values.Has("institutionId") && (kind != person || !validID(filter.institutionID))) {
		return listFilter{}, errInvalid
	}
	filter.cursorName, filter.cursorKey = decodeCursor(values.Get("before"))
	if values.Has("before") && filter.cursorKey == "" {
		return listFilter{}, errInvalid
	}
	return filter, nil
}

// decodeCursor 解码目录名称与同名行的稳定排序键。
// 参数 raw 为前一页提供的 URL 安全游标。
// 返回值 名称与排序键，无效时返回空值。
func decodeCursor(raw string) (string, string) {
	if raw == "" || len(raw) > 2200 {
		return "", ""
	}
	value, err := base64.RawURLEncoding.DecodeString(raw)
	name, key, found := strings.Cut(string(value), "\x00")
	if err != nil || !found || !textAllowed(name, 200, false) || !textAllowed(key, 200, false) {
		return "", ""
	}
	return name, key
}

// encodeCursor 对末条结果的名称与身份进行可逆编码。
// 参数 item 为本页末条目录记录。
// 返回值 用于读取下一页的游标。
func encodeCursor(item entry) string {
	key := item.ID
	if key == "" {
		key = item.ScopeName
	}
	return base64.RawURLEncoding.EncodeToString([]byte(item.Name + "\x00" + key))
}

// historyBefore 解析从新到旧的历史顺序游标。
// 参数 values 为验证过的查询字段。
// 返回值 序号，缺省为零，非法时返回错误。
func historyBefore(values url.Values) (int64, error) {
	if !values.Has("before") {
		return 0, nil
	}
	before, err := strconv.ParseInt(values.Get("before"), 10, 64)
	if err != nil || before < 1 {
		return 0, errInvalid
	}
	return before, nil
}
