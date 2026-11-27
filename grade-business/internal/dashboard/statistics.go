package dashboard

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/httpx"
	"grade-business/internal/workflowreminders"
)

const statisticsPath = "/api/v1/dashboard/statistics"

type filingStatistics struct {
	Active              int64 `json:"active"`
	Cancelled           int64 `json:"cancelled"`
	UnderReview         int64 `json:"underReview"`
	Returned            int64 `json:"returned"`
	Draft               int64 `json:"draft"`
	ActiveOrganizations int64 `json:"activeOrganizations"`
}

type hazardStatistics struct {
	High   int64 `json:"high"`
	Medium int64 `json:"medium"`
	Low    int64 `json:"low"`
}

type systemRisk struct {
	FilingID         string `json:"filingId"`
	SystemName       string `json:"systemName"`
	OrganizationName string `json:"organizationName"`
	Count            int64  `json:"count"`
}

type firstReviewStatistics struct {
	Approved int64 `json:"approved"`
	Decided  int64 `json:"decided"`
}

type dailyActivity struct {
	Date            string `json:"date"`
	FilingsCreated  int64  `json:"filingsCreated"`
	ReportsCreated  int64  `json:"reportsCreated"`
	AlertsTriggered int64  `json:"alertsTriggered"`
	HazardsClosed   int64  `json:"hazardsClosed"`
}

type statistics struct {
	dashboardStatistics
	HighRiskSystems []systemRisk `json:"highRiskSystems"`
}

type dashboardStatistics struct {
	UpdatedAt                time.Time             `json:"updatedAt"`
	Filings                  filingStatistics      `json:"filings"`
	Hazards                  hazardStatistics      `json:"hazards"`
	OpenHazards              hazardStatistics      `json:"openHazards"`
	ReportFirstReview        firstReviewStatistics `json:"reportFirstReview"`
	Trend                    []dailyActivity       `json:"trend"`
	FilingCoverage           filingCoverage        `json:"filingCoverage"`
	FilingCoverageByDistrict []districtCoverage    `json:"filingCoverageByDistrict"`
	RegisteredAssets         registeredAssets      `json:"registeredAssets"`
}

type dashboardQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Register 挂载指挥驾驶舱统计与归类维护接口。
// 参数 mux 为管理端路由表，pool 为业务数据库，reviewWindows 为可选的业务审核时限。
// 返回值 无。
func Register(mux *http.ServeMux, pool *pgxpool.Pool, reviewWindows ...workflowreminders.Windows) {
	var windows workflowreminders.Windows
	if len(reviewWindows) > 0 {
		windows = reviewWindows[0]
	}
	mux.HandleFunc("GET /api/v1/dashboard/risk-by-district", handleDistrictRisk(pool))
	mux.HandleFunc("GET /api/v1/dashboard/district-response", handleDistrictResponse(pool, windows))
	mux.HandleFunc("GET /api/v1/dashboard/analytics", handleAnalysis(pool))
	mux.HandleFunc("GET /api/v1/dashboard/classifications/{kind}", handleClassificationList(pool))
	mux.HandleFunc("PATCH /api/v1/dashboard/classifications/{kind}/{id}", handleClassificationPatch(pool))
	mux.HandleFunc("GET "+statisticsPath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			httpx.Error(w, http.StatusBadRequest, "invalid_input", "驾驶舱统计无需查询参数。")
			return
		}
		result, err := readStatistics(r.Context(), pool, nil)
		if err != nil {
			slog.Error("驾驶舱统计读取失败", "error_type", fmt.Sprintf("%T", err))
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "暂时无法读取驾驶舱统计。")
			return
		}
		httpx.JSON(w, http.StatusOK, result)
	})
}

const statisticsSQL = dashboardScopeSQL + `, business_clock AS (
    SELECT statement_timestamp() AS as_of,
           (statement_timestamp() AT TIME ZONE 'Asia/Shanghai')::date AS today
), trend_bounds AS (
    SELECT as_of,today,((today-29)::timestamp AT TIME ZONE 'Asia/Shanghai') AS first_instant
    FROM business_clock
), case_activity AS (
    SELECT (c.created_at AT TIME ZONE 'Asia/Shanghai')::date AS day,
           count(*) FILTER (WHERE c.kind='filing') AS filings_created,
           count(*) FILTER (WHERE c.kind='report') AS reports_created
    FROM (SELECT kind,created_at,discarded_at FROM scope_filings
          UNION ALL SELECT kind,created_at,discarded_at FROM scope_reports) c CROSS JOIN trend_bounds b
    WHERE c.discarded_at IS NULL
      AND c.created_at BETWEEN b.first_instant AND b.as_of
    GROUP BY 1
), alert_activity AS (
    SELECT (a.triggered_at AT TIME ZONE 'Asia/Shanghai')::date AS day,count(*) AS alerts_triggered
    FROM scope_alerts a CROSS JOIN trend_bounds b
    WHERE a.triggered_at BETWEEN b.first_instant AND b.as_of
    GROUP BY 1
), hazard_activity AS (
    SELECT (h.closed_at AT TIME ZONE 'Asia/Shanghai')::date AS day,count(*) AS hazards_closed
    FROM scope_hazards h CROSS JOIN trend_bounds b
    WHERE h.status='closed' AND h.closed_at BETWEEN b.first_instant AND b.as_of
    GROUP BY 1
), recent_activity AS (
    SELECT jsonb_agg(jsonb_build_object('date',d.day,
        'filingsCreated',COALESCE(c.filings_created,0),'reportsCreated',COALESCE(c.reports_created,0),
        'alertsTriggered',COALESCE(a.alerts_triggered,0),'hazardsClosed',COALESCE(h.hazards_closed,0)
    ) ORDER BY d.day) AS trend
    FROM (SELECT today-series.age AS day FROM trend_bounds CROSS JOIN generate_series(0,29) AS series(age)) d
    LEFT JOIN case_activity c USING(day) LEFT JOIN alert_activity a USING(day) LEFT JOIN hazard_activity h USING(day)
), filing_counts AS (
    SELECT count(*) FILTER (WHERE status='approved' AND cancelled_at IS NULL) AS active,
           count(*) FILTER (WHERE cancelled_at IS NOT NULL) AS cancelled,
           count(*) FILTER (WHERE status IN ('pending_initial','pending_final')) AS under_review,
           count(*) FILTER (WHERE status='returned') AS returned,
           count(*) FILTER (WHERE status='draft') AS draft,
           count(DISTINCT organization_name COLLATE "C") FILTER (
               WHERE status='approved' AND cancelled_at IS NULL AND organization_name<>'') AS active_orgs
    FROM scope_filings WHERE discarded_at IS NULL
), hazard_counts AS (
    SELECT count(*) FILTER (WHERE severity='high') AS high,
           count(*) FILTER (WHERE severity='medium') AS medium,
           count(*) FILTER (WHERE severity='low') AS low,
           count(*) FILTER (WHERE severity='high' AND status<>'closed') AS open_high,
           count(*) FILTER (WHERE severity='medium' AND status<>'closed') AS open_medium,
           count(*) FILTER (WHERE severity='low' AND status<>'closed') AS open_low
    FROM scope_hazards WHERE status<>'pending_dispatch'
), high_risk_systems AS (
    SELECT f.id,f.system_name,f.organization_name,count(*) AS amount
    FROM scope_hazards h
    JOIN scope_filings f ON f.id=h.scope_filing_id AND f.status='approved'
    WHERE h.severity='high' AND h.status NOT IN ('pending_dispatch','closed')
    GROUP BY f.id,f.system_name,f.organization_name ORDER BY amount DESC,f.id LIMIT 10
), first_outcomes AS (
    SELECT DISTINCT ON (e.case_id) e.to_status
    FROM case_review_events e JOIN scope_reports r ON r.id=e.case_id
    WHERE (e.action='approve' AND e.to_status='approved')
       OR (e.action='return_for_correction' AND e.to_status='returned')
    ORDER BY e.case_id,e.version
), report_counts AS (
    SELECT count(*) FILTER (WHERE to_status='approved') AS approved,count(*) AS decided FROM first_outcomes
), ` + coverageStatisticsSQL + `
SELECT b.as_of,f.active,f.cancelled,f.under_review,f.returned,f.draft,f.active_orgs,h.high,h.medium,h.low,
       h.open_high,h.open_medium,h.open_low,
       COALESCE((SELECT jsonb_agg(jsonb_build_object(
           'filingId',id,'systemName',system_name,'organizationName',organization_name,'count',amount
       ) ORDER BY amount DESC,id) FROM high_risk_systems),'[]'::jsonb),
       r.approved,r.decided,a.trend,c.covered,c.eligible,
       COALESCE((SELECT jsonb_agg(jsonb_build_object('district',district,'covered',covered,'eligible',eligible)
         ORDER BY eligible DESC,district) FROM district_coverage),'[]'::jsonb),
       assets.ip,assets.domain,assets.topology,assets.supplier,assets.security_event,assets.cloud,assets.big_data
FROM business_clock b CROSS JOIN filing_counts f CROSS JOIN hazard_counts h
CROSS JOIN report_counts r CROSS JOIN recent_activity a CROSS JOIN coverage_counts c CROSS JOIN asset_counts assets
`

// readStatistics 在同一语句快照中读取备案、风险、测评首轮结论、资产、覆盖清册和每日趋势。
// 参数 ctx 控制读取，db 为数据库或读取事务，district 为空指针时统计全域。
// 返回值 仅用于大屏显示的统计字段，或查询错误。
func readStatistics(ctx context.Context, db dashboardQueryer, district *string) (statistics, error) {
	var result statistics
	f, h, open, review := &result.Filings, &result.Hazards, &result.OpenHazards, &result.ReportFirstReview
	coverage, assets := &result.FilingCoverage, &result.RegisteredAssets
	err := db.QueryRow(ctx, statisticsSQL, district).Scan(&result.UpdatedAt,
		&f.Active, &f.Cancelled, &f.UnderReview, &f.Returned, &f.Draft, &f.ActiveOrganizations,
		&h.High, &h.Medium, &h.Low, &open.High, &open.Medium, &open.Low,
		&result.HighRiskSystems, &review.Approved, &review.Decided, &result.Trend,
		&coverage.Covered, &coverage.Eligible, &result.FilingCoverageByDistrict,
		&assets.IP, &assets.Domain, &assets.Topology, &assets.Supplier, &assets.SecurityEvent, &assets.Cloud, &assets.BigData)
	return result, err
}
