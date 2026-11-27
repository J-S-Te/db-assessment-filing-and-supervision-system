package hazards

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
)

const (
	statusOpen         = "open"
	statusPending      = "pending_review"
	statusRetest       = "pending_retest"
	statusReturned     = "returned"
	statusClosed       = "closed"
	actionSubmit       = "submit"
	actionReturn       = "return_for_correction"
	actionClose        = "close"
	actionRetestPass   = "retest_pass"
	actionRetestReturn = "retest_return"
	kindEvidence       = "evidence"
	kindRetestReport   = "retest_report"
)

var (
	errInvalid         = errors.New("invalid hazard input")
	errNotFound        = errors.New("hazard record not found")
	errConflict        = errors.New("hazard version or state conflict")
	errEvidence        = errors.New("hazard evidence required for current review round")
	errRetestReport    = errors.New("hazard retest report required for current review round")
	errPlanNotApproved = errors.New("remediation plan not approved")
)

type hazardSummary struct {
	ID               string     `json:"id"`
	PlanID           string     `json:"planId,omitempty"`
	ReportID         string     `json:"reportId,omitempty"`
	FilingID         string     `json:"filingId"`
	FilingNumber     string     `json:"filingNumber"`
	ReportNumber     string     `json:"reportNumber,omitempty"`
	SystemName       string     `json:"systemName"`
	OrganizationName string     `json:"organizationName"`
	AssessmentOrg    string     `json:"assessmentOrg,omitempty"`
	Severity         string     `json:"severity"`
	Description      string     `json:"description"`
	Responsibility   string     `json:"responsibility"`
	Deadline         string     `json:"deadline"`
	Status           string     `json:"status"`
	CreatedAt        time.Time  `json:"createdAt"`
	Ledger           string     `json:"ledger"`
	Title            string     `json:"title,omitempty"`
	Source           string     `json:"source,omitempty"`
	DiscoveredOn     string     `json:"discoveredOn,omitempty"`
	DispatchedAt     *time.Time `json:"dispatchedAt,omitempty"`
}

type hazardDetail struct {
	hazardSummary
	Measure           string     `json:"measure"`
	Version           int64      `json:"version"`
	AllowedActions    []string   `json:"allowedActions"`
	ClosedAt          *time.Time `json:"closedAt,omitempty"`
	ReceiptAvailable  *bool      `json:"receiptAvailable,omitempty"`
	RetestReportReady *bool      `json:"retestReportReady,omitempty"`
}

type hazardPage struct {
	Items      []hazardSummary `json:"items"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type hazardCollection[T any] struct {
	Items []T `json:"items"`
}

type actionInput struct {
	Action          string `json:"action"`
	ExpectedVersion int64  `json:"expectedVersion"`
	Comment         string `json:"comment"`
}

type hazardMutation struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Version int64  `json:"version"`
}

type hazardEvent struct {
	Action     string    `json:"action"`
	FromStatus string    `json:"fromStatus"`
	ToStatus   string    `json:"toStatus"`
	Operator   string    `json:"operator"`
	Comment    string    `json:"comment,omitempty"`
	OccurredAt time.Time `json:"occurredAt"`
}

type hazardCursor struct {
	CreatedAt time.Time
	ID        string
}

type hazardFilter struct {
	query, status, severity, due, planID, filingID, ledger string
	cursor                                                 hazardCursor
	limit                                                  int
}

// validID 验证台账、整改规划和材料通用标识格式。
// 参数 id 为地址参数中的业务标识。
// 返回值 可作为完整 UUID 使用时为真。
func validID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	var parsed pgtype.UUID
	return parsed.Scan(id) == nil && parsed.Valid
}

// validText 检查可展示文字的长度、编码和特殊控制字符边界。
// 参数 value 为业务文字，limit 为最大 Unicode 字符数。
// 返回值 文字符合边界时为真，是否必填由调用方决定。
func validText(value string, limit int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit && !strings.ContainsRune(value, 0)
}

// decodeFilter 解析允许的列表查询并拒绝重复键、未知选项和超限分页。
// 参数 query 为已完成 URL 解码的筛选参数。
// 返回值 规范化筛选参数，或参数无效错误。
func decodeFilter(query url.Values) (hazardFilter, error) {
	for key, values := range query {
		switch key {
		case "q", "status", "severity", "due", "planId", "filingId", "ledger", "cursor", "limit":
		default:
			return hazardFilter{}, errInvalid
		}
		if len(values) != 1 {
			return hazardFilter{}, errInvalid
		}
	}
	filter := hazardFilter{
		query: strings.TrimSpace(query.Get("q")), status: query.Get("status"), severity: query.Get("severity"),
		due: query.Get("due"), planID: query.Get("planId"), filingID: query.Get("filingId"),
		ledger: query.Get("ledger"), limit: 20,
	}
	if !validFilter(filter) {
		return hazardFilter{}, errInvalid
	}
	if limit, supplied := query["limit"]; supplied {
		value, err := strconv.Atoi(limit[0])
		if err != nil || value < 1 || value > 100 {
			return hazardFilter{}, errInvalid
		}
		filter.limit = value
	}
	cursor, err := decodeCursor(query.Get("cursor"))
	filter.cursor = cursor
	return filter, err
}

// validFilter 检查筛选文本与台账业务枚举是否有效。
// 参数 filter 为已完成字段提取的筛选对象。
// 返回值 状态、严重性、期限和规划条件均有效时为真。
func validFilter(filter hazardFilter) bool {
	switch filter.status {
	case "", statusDispatch, statusOpen, statusRetest, statusPending, statusReturned, statusClosed:
	default:
		return false
	}
	validDue := filter.due == "" || filter.due == "overdue" || filter.due == "within_10_days"
	return validSeverity(filter.severity) && validDue && validLedger(filter.ledger) &&
		validText(filter.query, 120) && (filter.planID == "" || validID(filter.planID)) &&
		(filter.filingID == "" || validID(filter.filingID))
}

// validLedger 检查台账分类是否为系统漏洞或重大风险。
// 参数 ledger 为精确分类，列表允许空值代表全部。
// 返回值 分类在声明范围内时为真。
func validLedger(ledger string) bool {
	return ledger == "" || ledger == ledgerVulnerability || ledger == ledgerMajorRisk
}

// validSeverity 检查登记接口及筛选共同支持的风险级别。
// 参数 severity 为等级编码，筛选允许空值。
// 返回值 属于可用等级时为真。
func validSeverity(severity string) bool {
	return severity == "" || severity == "low" || severity == "medium" || severity == "high"
}

// decodeCursor 读取列表游标中的创建时间与尾条标识。
// 参数 raw 为上页返回的已编码游标，空值表示首页。
// 返回值 分页位置，或格式、长度无效的错误。
func decodeCursor(raw string) (hazardCursor, error) {
	if raw == "" {
		return hazardCursor{}, nil
	}
	if len(raw) > 110 {
		return hazardCursor{}, errInvalid
	}
	body, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return hazardCursor{}, errInvalid
	}
	stamp, id, ok := strings.Cut(string(body), ",")
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if !ok || err != nil || at.IsZero() || !validID(id) {
		return hazardCursor{}, errInvalid
	}
	return hazardCursor{CreatedAt: at, ID: id}, nil
}

// encodeCursor 生成对应台账稳定排序的下一页游标。
// 参数 at 与 id 为当前页最后一条记录的排序位置。
// 返回值 URL 安全的分页游标。
func encodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "," + id))
}

// validateAction 验证闭环操作、预期版本和状态变化所需的业务说明。
// 参数 input 指向可规范化的操作请求体。
// 返回值 输入有效时返回 nil，否则返回校验错误。
func validateAction(input *actionInput) error {
	input.Comment = strings.TrimSpace(input.Comment)
	if input.ExpectedVersion < 1 || !validText(input.Comment, 1000) {
		return errInvalid
	}
	switch input.Action {
	case actionSubmit, actionReturn, actionRetestReturn, actionSupervise:
		if input.Comment != "" {
			return nil
		}
	case actionClose, actionRetestPass, actionDispatch:
		return nil
	}
	return errInvalid
}
