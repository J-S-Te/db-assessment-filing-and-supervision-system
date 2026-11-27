package reviewinbox

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/httpx"
)

const inboxPrefix = "/api/v1/"

type inboxEndpoint struct {
	pool          *pgxpool.Pool
	audience, key string
}

// Register 挂载机构及运营者收件箱，并提供监管端的按业务回执查询。
// 参数 mux 为本地路由，pool 为业务库。
// 返回值 无。
func Register(mux *http.ServeMux, pool *pgxpool.Pool) {
	for _, endpoint := range []inboxEndpoint{{pool, "institution", "institution"}, {pool, "operator", "organization"}} {
		path := inboxPrefix + endpoint.audience + "/feedback"
		mux.HandleFunc("GET "+path, endpoint.list)
		mux.HandleFunc("POST "+path+"/{id}/acknowledge", endpoint.acknowledge)
		mux.HandleFunc("GET "+path+"/unread-count", endpoint.unreadCount)
	}
	admin := inboxEndpoint{pool: pool}
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
	err = e.pool.QueryRow(r.Context(), `SELECT count(*) FROM review_notices
		WHERE audience=$1 AND recipient=$2 AND acknowledged_at IS NULL`, e.audience, name).Scan(&result.Count)
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
	err = e.pool.QueryRow(r.Context(), `UPDATE review_notices
		SET acknowledged_at=COALESCE(acknowledged_at,clock_timestamp())
		WHERE id=$1 AND audience=$2 AND recipient=$3 RETURNING acknowledged_at`,
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
	rows, err := e.pool.Query(r.Context(), `SELECT id::text,recipient,state,sent_at,acknowledged_at FROM review_notices
		WHERE resource=$1 AND resource_id=$2 ORDER BY sequence DESC LIMIT 50`, resource, id)
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
