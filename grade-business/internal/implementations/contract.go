package implementations

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

	"grade-business/internal/directoryref"
)

const (
	path              = "/api/v1/implementations"
	institutionalPath = "/api/v1/institution/implementations"
	quotaLimit        = 3
	defaultLimit      = 20
	maxLimit          = 100
	routine           = "routine"
	change            = "change"
	draft             = "draft"
	submitted         = "submitted"
	initial           = "pending_initial"
	final             = "pending_final"
	supervision       = "pending_supervision"
	approved          = "approved"
	returned          = "returned"
	withdrawn         = "withdrawn"
	archived          = "archived"
	unarchived        = "unarchived"
	withdrawWindow    = 24 * time.Hour
)

var (
	errNotFound            = errors.New("实施报备不存在。")
	errFiling              = errors.New("请选择已通过且有效的备案。")
	errTarget              = errors.New("请选择与当前备案测评机构不同的拟换入机构。")
	errStale               = errors.New("记录已更新，请刷新后重试。")
	errConflict            = errors.New("当前状态不支持此操作。")
	errQuota               = errors.New("同一备案在本年度最多可提交三次有效变更报备。")
	errWithdrawal          = errors.New("当前报备不符合发起人撤销条件。")
	errPlanClosed          = errors.New("已到测评开始日零点，无法修改计划或重新提交补正。")
	errCooldown            = errors.New("该备案撤销后的 15 天冷却期尚未结束。")
	errComment             = errors.New("退回补正必须填写意见。")
	errInvalid             = errors.New("实施报备参数无效。")
	errPermission          = errors.New("当前账号无权执行此报备操作。")
	errSameReviewer        = errors.New("初审与终审必须由不同账号办理，请交由另一位审核人终审。")
	errUnknownReviewer     = errors.New("本轮缺少可核实的初审账号，请重新发起初审。")
	errUnverifiedReviewers = errors.New("本轮缺少可核实的异人初审、终审记录，请重新发起初审。")
	errRestartUnavailable  = errors.New("仅本轮缺少可核实审核身份的待终审或待复核报备可重新发起初审。")
	errRestartComment      = errors.New("重新发起初审必须填写原因。")
)

var listFilterKeys = []string{"q", "status", "type", "archive", "limit", "cursor"}

type input struct {
	FilingID      string  `json:"filingId"`
	Type          string  `json:"type"`
	Reporter      string  `json:"reporter"`
	ReporterID    *string `json:"reporterId,omitempty"`
	Contact       string  `json:"contact,omitempty"`
	AssessmentOrg string  `json:"assessmentOrg,omitempty"`
	PlannedStart  string  `json:"plannedStart"`
	PlannedEnd    string  `json:"plannedEnd"`
	Plan          string  `json:"plan"`
}

type patchInput struct {
	input
	ExpectedVersion int64 `json:"expectedVersion"`
}

type command struct {
	Action          string `json:"action"`
	ExpectedVersion int64  `json:"expectedVersion"`
	Comment         string `json:"comment,omitempty"`
}

type choice struct {
	ID               string `json:"id"`
	Number           string `json:"number"`
	SystemName       string `json:"systemName"`
	OrganizationName string `json:"organizationName"`
	AssessmentOrg    string `json:"assessmentOrg"`
}

type summary struct {
	ID               string     `json:"id"`
	FilingNumber     string     `json:"filingNumber"`
	SystemName       string     `json:"systemName"`
	OrganizationName string     `json:"organizationName"`
	AssessmentOrg    string     `json:"assessmentOrg"`
	Type             string     `json:"type"`
	Status           string     `json:"status"`
	PlannedStart     string     `json:"plannedStart"`
	PlannedEnd       string     `json:"plannedEnd"`
	SubmittedAt      *time.Time `json:"submittedAt,omitempty"`
	ArchivedAt       *time.Time `json:"archivedAt,omitempty"`
}

type detail struct {
	summary
	FilingID          string                 `json:"filingId"`
	Reporter          string                 `json:"reporter"`
	Contact           string                 `json:"contact,omitempty"`
	Plan              string                 `json:"plan"`
	Version           int64                  `json:"version"`
	AcceptedAt        *time.Time             `json:"acceptedAt,omitempty"`
	PlanEditDeadline  *time.Time             `json:"planEditDeadline,omitempty"`
	ResubmitAt        *time.Time             `json:"resubmitAvailableAt,omitempty"`
	AllowedActions    []string               `json:"allowedActions"`
	ReviewBlockReason string                 `json:"reviewBlockReason,omitempty"`
	CanPrecheck       bool                   `json:"canPrecheck"`
	ReporterID        *string                `json:"reporterId,omitempty"`
	DirectorySnapshot *directoryref.Snapshot `json:"directorySnapshot,omitempty"`
}

type storedRecord struct {
	detail
	CreatedBy       string
	QuotaYear       *int
	FirstReviewerID *string
	FinalReviewerID *string
}

type mutation struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	Version    int64      `json:"version"`
	ArchivedAt *time.Time `json:"archivedAt,omitempty"`
}

type event struct {
	Action            string                 `json:"action"`
	FromStatus        string                 `json:"fromStatus,omitempty"`
	ToStatus          string                 `json:"toStatus"`
	Operator          string                 `json:"operator"`
	OperatorID        *string                `json:"operatorId,omitempty"`
	OperatorRole      *string                `json:"operatorRole,omitempty"`
	Comment           string                 `json:"comment,omitempty"`
	OccurredAt        time.Time              `json:"occurredAt"`
	DirectorySnapshot *directoryref.Snapshot `json:"directorySnapshot,omitempty"`
}

type eventPage struct {
	Items             []event `json:"items"`
	NextBeforeVersion int64   `json:"nextBeforeVersion,omitempty"`
}

type quota struct {
	Year       int        `json:"year"`
	Used       int        `json:"used"`
	Limit      int        `json:"limit"`
	ResubmitAt *time.Time `json:"resubmitAvailableAt,omitempty"`
}

type listOptions struct {
	q       string
	status  string
	kind    string
	archive string
	limit   int
	after   pgtype.Timestamptz
	afterID pgtype.UUID
}

type page struct {
	Items      []summary `json:"items"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

// validateInput 检查并整理报备输入，保留实施计划中的换行。
// 参数 item 为报备表单输入。
// 返回值 清理后的输入和无效时可公开展示的错误。
func validateInput(item input) (input, error) {
	if !validID(item.FilingID) || !validType(item.Type) {
		return input{}, errInvalid
	}
	item.Reporter = strings.TrimSpace(item.Reporter)
	item.Contact = strings.TrimSpace(item.Contact)
	item.AssessmentOrg = strings.TrimSpace(item.AssessmentOrg)
	item.Plan = strings.TrimSpace(item.Plan)
	usesDirectory := item.ReporterID != nil && *item.ReporterID != ""
	if usesDirectory && !validID(*item.ReporterID) {
		return input{}, directoryref.ErrPersonInvalid
	}
	if (!usesDirectory && item.Reporter == "") || item.Plan == "" || !boundedText(item.Reporter, 120, false) ||
		!boundedText(item.Contact, 60, false) || !boundedText(item.AssessmentOrg, 200, false) || !boundedText(item.Plan, 5000, true) {
		return input{}, errors.New("请输入报备人员和实施计划，检查字段长度与格式。")
	}
	if !validPlannedDates(item.PlannedStart, item.PlannedEnd) {
		return input{}, errors.New("请输入有效的计划日期，完成日期不能早于开始日期。")
	}
	return item, nil
}

// validPlannedDates 核对实施计划起止日期且允许同日开始和完成。
// 参数 plannedStart、plannedEnd 为报备记录的计划日期。
// 返回值 两个日期有效且完成日期不早于开始日期时为真。
func validPlannedDates(plannedStart, plannedEnd string) bool {
	start, startErr := time.Parse(time.DateOnly, plannedStart)
	end, endErr := time.Parse(time.DateOnly, plannedEnd)
	return startErr == nil && endErr == nil && start.Year() >= 1 && !end.Before(start)
}

// boundedText 限制字符长度并过滤不可见控制字符。
// 参数 value 为待检查的文字，size 为字符上限，multi 为是否允许换行与制表符。
// 返回值 文本满足范围和编码规则时为真。
func boundedText(value string, size int, multi bool) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= size && !strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsControl(r) && (!multi || (r != '\n' && r != '\r' && r != '\t'))
	})
}

// validID 检查公开 UUID 格式。
// 参数 raw 为路径或表单中的公开标识。
// 返回值 标识可用于数据库 UUID 查询时为真。
func validID(raw string) bool {
	var id pgtype.UUID
	return len(raw) == 36 && raw[8] == '-' && raw[13] == '-' && raw[18] == '-' && raw[23] == '-' && id.Scan(raw) == nil
}

// validType 判断报备类别是否有效。
// 参数 kind 为业务类别。
// 返回值 常规或变更类别时为真。
func validType(kind string) bool { return kind == routine || kind == change }

// validStatus 判断列表筛选状态是否有效。
// 参数 status 为用户指定的状态代码。
// 返回值 状态已定义时为真。
func validStatus(status string) bool {
	switch status {
	case draft, submitted, initial, final, supervision, approved, returned, withdrawn:
		return true
	}
	return false
}

// filterValues 解析指定白名单中的查询参数。
// 参数 raw 为原始查询字符串，keys 为允许字段。
// 返回值 唯一字段的查询值和非法参数错误。
func filterValues(raw string, keys ...string) (url.Values, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, errInvalid
	}
	for key, entries := range values {
		if len(entries) != 1 || !contains(keys, key) {
			return nil, errInvalid
		}
	}
	return values, nil
}

// contains 检查字段名是否属于允许集合。
// 参数 values 为已知字段，item 为待验证字段。
// 返回值 包含该字段时为真。
func contains(values []string, item string) bool {
	for _, value := range values {
		if value == item {
			return true
		}
	}
	return false
}

// parseList 解析实施报备状态、归档筛选与分页位置。
// 参数 raw 为请求查询字段。
// 返回值 已验证的筛选条件和输入错误。
func parseList(raw string) (listOptions, error) {
	values, err := filterValues(raw, listFilterKeys...)
	if err != nil {
		return listOptions{}, err
	}
	q, status, kind := strings.TrimSpace(values.Get("q")), values.Get("status"), values.Get("type")
	archive := values.Get("archive")
	if !boundedText(q, 120, false) || (status != "" && !validStatus(status)) ||
		(kind != "" && !validType(kind)) || (archive != "" && archive != archived && archive != unarchived) {
		return listOptions{}, errInvalid
	}
	limit, err := parseLimit(values.Get("limit"))
	if err != nil {
		return listOptions{}, err
	}
	options := listOptions{q: q, status: status, kind: kind, archive: archive, limit: limit}
	if raw := values.Get("cursor"); raw != "" {
		options.after, options.afterID, err = decodeCursor(raw)
	}
	return options, err
}

// parseLimit 解析请求页大小。
// 参数 raw 为字符串表示的条数，空值采用默认值。
// 返回值 条数和超出范围时的错误。
func parseLimit(raw string) (int, error) {
	if raw == "" {
		return defaultLimit, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > maxLimit {
		return 0, errInvalid
	}
	return value, nil
}

// decodeCursor 读取更新时间和 UUID 组成的游标。
// 参数 raw 为服务端上一页返回的游标。
// 返回值 排序位置和无效输入错误。
func decodeCursor(raw string) (pgtype.Timestamptz, pgtype.UUID, error) {
	var date pgtype.Timestamptz
	var id pgtype.UUID
	if len(raw) > 128 {
		return date, id, errInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return date, id, errInvalid
	}
	stamp, key, ok := strings.Cut(string(decoded), "|")
	value, err := time.Parse(time.RFC3339Nano, stamp)
	if !ok || err != nil || !validID(key) || id.Scan(key) != nil {
		return date, id, errInvalid
	}
	return pgtype.Timestamptz{Time: value, Valid: true}, id, nil
}

// encodeCursor 生成可用于下一页请求的游标。
// 参数 at 和 id 为当前页最后一项的排序字段。
// 返回值 URL 安全的游标。
func encodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))
}
