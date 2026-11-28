package reviewinbox

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/httpx"
)

const inboxPrefix = "/api/v1/"

// DistrictScope 根据当前监管会话生成回执查询的辖区条件。
// 参数 ctx 为认证上下文，filing 为固定查询表达式，param 为辖区数组占位符。
// 返回值 SQL 条件及授权区县，非监管席位使用 nil。
type DistrictScope func(ctx context.Context, filing, param string) (string, []string)

type inboxEndpoint struct {
	pool          *pgxpool.Pool
	audience, key string
	districtScope DistrictScope
}

// Register 挂载机构及运营者收件箱，并提供监管端的按业务回执查询。
// 参数 mux 为本地路由，pool 为业务库，scope 可提供监管回执的辖区条件。
// 返回值 无。
func Register(mux *http.ServeMux, pool *pgxpool.Pool, scope ...DistrictScope) {
	for _, endpoint := range []inboxEndpoint{
		{pool: pool, audience: "institution", key: "institution"},
		{pool: pool, audience: "operator", key: "organization"},
	} {
		path := inboxPrefix + endpoint.audience + "/feedback"
		mux.HandleFunc("GET "+path, endpoint.list)
		mux.HandleFunc("POST "+path+"/{id}/acknowledge", endpoint.acknowledge)
		mux.HandleFunc("GET "+path+"/unread-count", endpoint.unreadCount)
	}
	admin := inboxEndpoint{pool: pool}
	if len(scope) > 0 {
		admin.districtScope = scope[0]
	}
	mux.HandleFunc("GET /api/v1/review-feedback/{resource}/{id}", admin.deliveries)
}

// list 查询当前机构或运营单位的一页真实意见。
// 参数 w 为响应，r 携带本地收件范围和分页位置。
// 返回值 无。
func (e inboxEndpoint) list(w http.ResponseWriter, r *http.Request) {
	name, before, err := scopeQuery(r.URL.RawQuery, e.key, true)
	if err != nil {
		httpx.Error(w, 400, "invalid_input", "请核对收件方和分页条件。")
		return
	}
	result, err := listInbox(r.Context(), e.pool, e.audience, name, before)
	if err != nil {
		httpx.Error(w, 500, "internal_error", "暂时无法读取站内意见。")
		return
	}
	httpx.JSON(w, 200, result)
}

// unreadCount 读取办理端尚未签收的站内消息数量。
// 参数 w 为响应，r 包含当前本地机构或运营单位范围。
// 返回值 无。
func (e inboxEndpoint) unreadCount(w http.ResponseWriter, r *http.Request) {
	name, err := Scope(r, e.key)
	if err != nil {
		httpx.Error(w, 400, "invalid_input", "请选择办理范围。")
		return
	}
	var result struct {
		Count int64 `json:"count"`
	}
	err = e.pool.QueryRow(r.Context(), `SELECT count(*) FROM review_notices n
		WHERE audience=$1 AND recipient=$2 AND acknowledged_at IS NULL
		AND `+trustedFilingNotice, e.audience, name).Scan(&result.Count)
	if err != nil {
		httpx.Error(w, 500, "internal_error", "暂时无法读取待签收消息数量。")
		return
	}
	httpx.JSON(w, 200, result)
}

// acknowledge 首次签收意见，并在重复请求中返回同一已保存时间。
// 参数 w 为响应，r 指定当前收件方和意见标识。
// 返回值 无。
func (e inboxEndpoint) acknowledge(w http.ResponseWriter, r *http.Request) {
	name, err := Scope(r, e.key)
	if err != nil || r.ContentLength != 0 {
		httpx.Error(w, 400, "invalid_input", "签收仅需指定收件方与意见，不接收正文。")
		return
	}
	id, valid := parseUUID(r.PathValue("id"))
	if !valid {
		httpx.Error(w, 404, "not_found", "未找到该意见。")
		return
	}
	var result receipt
	err = e.pool.QueryRow(r.Context(), `UPDATE review_notices n
		SET acknowledged_at=COALESCE(acknowledged_at,clock_timestamp())
		WHERE id=$1 AND audience=$2 AND recipient=$3
		AND `+trustedFilingNotice+` RETURNING acknowledged_at`,
		id, e.audience, name).Scan(&result.AcknowledgedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, 404, "not_found", "未找到该意见。")
	case err != nil:
		httpx.Error(w, 500, "internal_error", "暂时无法登记签收。")
	default:
		httpx.JSON(w, 200, result)
	}
}

// deliveries 返回监管详情所需的最近回执摘要。
// 参数 w 为响应，r 指定受支持业务的公开类型和记录标识。
// 返回值 无。
func (e inboxEndpoint) deliveries(w http.ResponseWriter, r *http.Request) {
	id, valid := parseUUID(r.PathValue("id"))
	resource := r.PathValue("resource")
	if !valid || !knownResource(resource) {
		httpx.Error(w, 404, "not_found", "未找到该业务回执。")
		return
	}
	if r.URL.RawQuery != "" {
		httpx.Error(w, 400, "invalid_input", "回执查询不接收额外参数。")
		return
	}
	query := `SELECT id::text,recipient,state,sent_at,acknowledged_at FROM review_notices
		WHERE resource=$1 AND resource_id=$2`
	args := []any{resource, id}
	if e.districtScope != nil {
		filing := receiptFiling(resource)
		condition, districts := e.districtScope(r.Context(), filing, "$3")
		if districts != nil {
			if condition == "" {
				httpx.Error(w, 404, "not_found", "未找到该业务回执。")
				return
			}
			query += " AND " + condition
			args = append(args, districts)
		}
	}
	rows, err := e.pool.Query(r.Context(), query+` ORDER BY sequence DESC LIMIT 50`, args...)
	if err != nil {
		httpx.Error(w, 500, "internal_error", "暂时无法读取业务回执。")
		return
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[delivery])
	if err != nil {
		httpx.Error(w, 500, "internal_error", "暂时无法读取业务回执。")
		return
	}
	httpx.JSON(w, 200, struct {
		Items []delivery `json:"items"`
	}{Items: items})
}

// receiptFiling 返回已接通监管辖区检查的回执所属备案。
// 参数 resource 为受控业务类型，$2 绑定该业务的标识。
// 返回值 固定 SQL 查询表达式，未完成范围接入的类型返回空值。
func receiptFiling(resource string) string {
	switch resource {
	case "filings":
		return "$2::uuid"
	case "reports":
		return "(SELECT filing_id FROM business_cases WHERE id=$2 AND kind='report')"
	case "filing-applications":
		return "(SELECT filing_id FROM filing_applications WHERE id=$2)"
	case "implementations":
		return "(SELECT filing_id FROM implementation_reports WHERE id=$2)"
	case "remediations":
		return `(SELECT r.filing_id FROM remediation_plans p
			JOIN business_cases r ON r.id=p.report_id WHERE p.id=$2)`
	case "hazards":
		return "(SELECT filing_id FROM hazard_context WHERE id=$2)"
	case "alerts":
		return `(SELECT h.filing_id FROM local_alerts a
			JOIN hazard_context h ON h.id=a.hazard_id WHERE a.id=$2)`
	case "supervisions", "supervision-site":
		return "(SELECT filing_id FROM supervision_tasks WHERE id=$2)"
	default:
		return ""
	}
}

// knownResource 核对当前共享意见服务支持的业务模块。
// 参数 value 为公开路由的业务类型。
// 返回值 受支持时为真。
func knownResource(value string) bool {
	switch value {
	case "filings", "filing-applications", "reports", "implementations", "remediations", "hazards", "alerts",
		"material-issues", "supervisions", "material-expiries", "supervision-site", "assessment-dues":
		return true
	default:
		return false
	}
}

// parseUUID 解析请求中的公开消息或业务标识。
// 参数 raw 为原始路径值。
// 返回值 PostgreSQL UUID 与格式是否有效。
func parseUUID(raw string) (pgtype.UUID, bool) {
	var id pgtype.UUID
	valid := len(raw) == 36 && id.Scan(raw) == nil && id.Valid
	return id, valid
}
