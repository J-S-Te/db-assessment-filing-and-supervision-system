package app

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/internal/httpx"
	"grade-business/internal/reviewinbox"
)

const deadlinePageSize = 20

type deadlineFilter struct {
	scope   queueScope
	urgency string
	offset  int
}

type deadlinePage struct {
	Items      []deadlineTask `json:"items"`
	NextOffset int            `json:"nextOffset,omitempty"`
}

type deadlineTask struct {
	overviewTask
	WaitingFor string `json:"waitingFor,omitempty"`
}

type deadlineDetail struct {
	Deadline   string `json:"deadline,omitempty"`
	Label      string `json:"label,omitempty"`
	WaitingFor string `json:"waitingFor,omitempty"`
}

const applicantObserverSQL = `(w.audience<>$1 AND w.applicant_audience=$1
  AND w.window_kind<>''
  AND ($1='operator' AND w.organization COLLATE "C"=$2 OR
       $1='institution' AND w.institution COLLATE "C"=$2))`

const visibleDeadlineSQL = taskClockSQL + `
 SELECT w.*, ` + retestObserverSQL + ` AS monitor,
        CASE WHEN w.audience<>$1 THEN w.audience ELSE '' END AS waiting_for
 FROM workflow_tasks w
 WHERE ($1 <> 'operator' OR w.resource <> 'implementations')
 AND (` + currentAssigneeSQL + ` OR ` + retestObserverSQL + ` OR ` + applicantObserverSQL + `)` + taskDatesSQL

const deadlineLabelSQL = `CASE WHEN monitor THEN '待机构复测' ELSE label END`

const deadlineQueueSQL = visibleDeadlineSQL + `
SELECT id::text,resource,` + deadlineLabelSQL + `,
       system_name,number,to_char(deadline,'YYYY-MM-DD'),status,urgency,monitor,waiting_for
FROM marks
WHERE deadline IS NOT NULL AND ($8='' OR urgency=$8)
ORDER BY deadline,stage_at,resource,id LIMIT $9 OFFSET $10
`

const deadlineDetailSQL = visibleDeadlineSQL + `
 SELECT to_char(deadline,'YYYY-MM-DD'),` + deadlineLabelSQL + `,waiting_for
 FROM marks WHERE resource=$8 AND id=$9 AND deadline IS NOT NULL
 ORDER BY waiting_for,deadline,stage_at LIMIT 1
`

// registerDeadlines 提供三端预警页面共用的业务时限队列。
// 参数 mux 为应用路由，pool 为业务库，windows 为现用审核时限。
// 返回值 无。
func registerDeadlines(mux *http.ServeMux, pool *pgxpool.Pool, windows ReviewWindows) {
	mux.HandleFunc("GET /api/v1/workbench/deadlines", handleDeadlines(pool, windows, "regulator", ""))
	mux.HandleFunc("GET /api/v1/institution/workbench/deadlines",
		handleDeadlines(pool, windows, "institution", "institution"))
	mux.HandleFunc("GET /api/v1/operator/workbench/deadlines",
		handleDeadlines(pool, windows, "operator", "organization"))
	mux.HandleFunc("GET /api/v1/institution/workbench/deadlines/{resource}/{id}",
		handleDeadlineDetail(pool, windows, "institution", "institution"))
	mux.HandleFunc("GET /api/v1/operator/workbench/deadlines/{resource}/{id}",
		handleDeadlineDetail(pool, windows, "operator", "organization"))
}

// handleDeadlineDetail 查询归属范围内单据的当前期限与办理方。
// 参数 pool、windows 为业务库和审核时限，audience、key 为办理端及归属查询字段。
// 返回值 未到审核时返回空对象，范围外同样不返回单据字段。
func handleDeadlineDetail(pool *pgxpool.Pool, windows ReviewWindows, audience, key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, err := reviewinbox.Scope(r, key)
		resource, rawID := r.PathValue("resource"), r.PathValue("id")
		var id pgtype.UUID
		if err != nil || !hasDeadlineDetails(resource) || len(rawID) != 36 || id.Scan(rawID) != nil || !id.Valid {
			httpx.Error(w, 400, "invalid_input", "请核对单据类型、标识和办理归属。")
			return
		}
		var result deadlineDetail
		err = pool.QueryRow(r.Context(), deadlineDetailSQL, audience, scope,
			windows.FilingDays, windows.FilingFinalDays, windows.ReportDays,
			windows.ImplementationDays, windows.RemediationDays, resource, id).
			Scan(&result.Deadline, &result.Label, &result.WaitingFor)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, 500, "internal_error", "暂时无法读取该业务的时限。")
			return
		}
		httpx.JSON(w, 200, result)
	}
}

// hasDeadlineDetails 限制详情查询只接收实际具有截止日期的业务类型。
// 参数 resource 为页面的业务资源名。
// 返回值 受支持时返回真。
func hasDeadlineDetails(resource string) bool {
	switch resource {
	case "filings", "filing-applications", "implementations", "reports", "remediations",
		"hazards", "supervision-site":
		return true
	default:
		return false
	}
}

// handleDeadlines 读取真实在办任务并沿用工作台对各角色的范围和期限定义。
// 参数 pool 为业务数据库，windows、audience、key 分别为审核期限、席位和范围参数。
// 返回值 业务时限查询路由。
func handleDeadlines(pool *pgxpool.Pool, windows ReviewWindows, audience, key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter, ok := parseDeadlineFilter(r, audience, key)
		if !ok {
			httpx.Error(w, 400, "invalid_input", "请核对办理归属、时限状态和分页。")
			return
		}
		page, err := queryDeadlines(r, pool, windows, filter)
		if err != nil {
			httpx.Error(w, 500, "internal_error", "暂时无法读取业务时限。")
			return
		}
		httpx.JSON(w, 200, page)
	}
}

// parseDeadlineFilter 核对此页面接受的范围、紧迫程度和偏移位置。
// 参数 r 为查询请求，audience 和 key 为所选角色及范围字段。
// 返回值 已校验的筛选条件及合法标记。
func parseDeadlineFilter(r *http.Request, audience, key string) (deadlineFilter, bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	filter := deadlineFilter{scope: queueScope{audience: audience}, urgency: values.Get("urgency")}
	if err != nil {
		return filter, false
	}
	for name, fields := range values {
		if len(fields) != 1 || (name != key && name != "urgency" && name != "offset") {
			return filter, false
		}
	}
	if key != "" {
		scoped := r.Clone(r.Context())
		scoped.URL.RawQuery = url.Values{key: values[key]}.Encode()
		if filter.scope.recipient, err = reviewinbox.Scope(scoped, key); err != nil {
			return filter, false
		}
	}
	if values.Has("offset") {
		filter.offset, err = strconv.Atoi(values.Get("offset"))
		if err != nil || filter.offset < 0 || filter.offset > 10000 {
			return filter, false
		}
	}
	switch filter.urgency {
	case "", "overdue", "near", "upcoming":
		return filter, true
	default:
		return filter, false
	}
}

// queryDeadlines 返回命中当前归属的一页有期限任务。
// 参数 r 为读取上下文，pool、windows、filter 为数据源、时限口径和查询条件。
// 返回值 业务时限页或查询失败。
func queryDeadlines(r *http.Request, pool *pgxpool.Pool, windows ReviewWindows,
	filter deadlineFilter,
) (deadlinePage, error) {
	rows, err := pool.Query(r.Context(), deadlineQueueSQL, filter.scope.audience,
		filter.scope.recipient, windows.FilingDays, windows.FilingFinalDays,
		windows.ReportDays, windows.ImplementationDays, windows.RemediationDays,
		filter.urgency, deadlinePageSize+1, filter.offset)
	if err != nil {
		return deadlinePage{}, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[deadlineTask])
	page := deadlinePage{Items: items}
	if len(items) > deadlinePageSize {
		page.Items = items[:deadlinePageSize]
		if filter.offset+deadlinePageSize <= 10000 {
			page.NextOffset = filter.offset + deadlinePageSize
		}
	}
	return page, err
}
