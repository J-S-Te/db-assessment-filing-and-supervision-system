package dashboard

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/httpx"
	"grade-business/internal/workflowreminders"
)

type districtTask struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	SystemName       string `json:"systemName"`
	OrganizationName string `json:"organizationName"`
	Status           string `json:"status"`
	Severity         string `json:"severity"`
	Deadline         string `json:"deadline"`
	Overdue          bool   `json:"overdue"`
}

type districtAlert struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	Severity         string `json:"severity"`
	SystemName       string `json:"systemName"`
	OrganizationName string `json:"organizationName"`
	Status           string `json:"status"`
	TriggeredAt      string `json:"triggeredAt"`
}

type districtResponse struct {
	District        *string              `json:"district"`
	Counts          districtWorkCounts   `json:"counts"`
	Tasks           []districtTask       `json:"tasks"`
	HighRiskSystems []systemRisk         `json:"highRiskSystems"`
	Alerts          []districtAlert      `json:"alerts"`
	Statistics      *dashboardStatistics `json:"statistics,omitempty"`
	Overview        *districtOverview    `json:"overview,omitempty"`
	Analytics       *dashboardAnalysis   `json:"analytics,omitempty"`
}

type districtWorkCounts struct {
	districtResponseCounts
	PendingRetest     int64 `json:"pendingRetest"`
	PendingReview     int64 `json:"pendingReview"`
	PendingPlanReview int64 `json:"pendingPlanReview"`
	PendingAlerts     int64 `json:"pendingAlerts"`
}

const districtResponseSQL = `
WITH scoped_filings AS (
	SELECT f.id,f.system_name,f.organization_name
	FROM business_cases f ` + filingDistrictJoinSQL + `
	WHERE ` + eligibleIndustrySQL + ` AND ($1::text IS NULL OR ` + filingDistrictSQL + `=$1)
), scoped_alerts AS (
	SELECT a.id,a.kind,a.severity,hc.system_name,hc.organization_name,a.status,a.triggered_at
	FROM scoped_filings f JOIN hazard_context hc ON hc.filing_id=f.id
	JOIN hazard_items h ON h.id=hc.id AND h.status<>'pending_dispatch'
	JOIN local_alerts a ON a.hazard_id=h.id
), open_items AS (
	SELECT h.id,hc.filing_id,f.system_name,f.organization_name,h.status,h.severity,h.deadline,
		h.deadline<` + districtBusinessDaySQL + ` AS overdue,
		CASE h.severity WHEN 'high' THEN 0 WHEN 'medium' THEN 1 ELSE 2 END AS severity_order,
		left(COALESCE(NULLIF(h.title,''),NULLIF(h.description,''),f.system_name),120) AS title
	FROM scoped_filings f JOIN hazard_context hc ON hc.filing_id=f.id
	JOIN hazard_items h ON h.id=hc.id
	WHERE h.status NOT IN ('pending_dispatch','closed')
), counts AS (
	SELECT count(DISTINCT filing_id) AS systems,
		count(*) FILTER (WHERE severity='high') AS high,
		count(*) FILTER (WHERE severity='medium') AS medium,
		count(*) FILTER (WHERE severity='low') AS low,
		count(*) FILTER (WHERE overdue) AS overdue,
		count(*) FILTER (WHERE status='pending_retest') AS "pendingRetest",
		count(*) FILTER (WHERE status='pending_review') AS "pendingReview",
		(SELECT count(*) FROM remediation_plans p
		 JOIN business_cases r ON r.id=p.report_id AND r.kind='report'
		 JOIN scoped_filings f ON f.id=r.filing_id
		 WHERE p.status='pending_review') AS "pendingPlanReview",
		(SELECT count(*) FROM scoped_alerts WHERE status='pending') AS "pendingAlerts"
	FROM open_items
), tasks AS (
	SELECT * FROM open_items WHERE overdue OR severity='high' OR status IN ('pending_retest','pending_review')
	ORDER BY overdue DESC,severity_order,deadline,id LIMIT 6
), ranked_systems AS (
	SELECT f.id,f.system_name,f.organization_name,count(*) AS amount
	FROM open_items h JOIN scoped_filings f ON f.id=h.filing_id
	WHERE h.severity='high'
	GROUP BY f.id,f.system_name,f.organization_name ORDER BY amount DESC,f.id LIMIT 10
), recent_alerts AS (
	SELECT * FROM scoped_alerts ORDER BY triggered_at DESC,id DESC LIMIT 20
)
SELECT to_jsonb(counts),
	COALESCE((SELECT jsonb_agg(jsonb_build_object('id',id,'title',title,'systemName',system_name,
		'organizationName',organization_name,'status',status,'severity',severity,
		'deadline',deadline,'overdue',overdue)
		ORDER BY overdue DESC,severity_order,deadline,id) FROM tasks),'[]'::jsonb),
	COALESCE((SELECT jsonb_agg(jsonb_build_object('filingId',id,'systemName',system_name,
		'organizationName',organization_name,'count',amount) ORDER BY amount DESC,id)
		FROM ranked_systems),'[]'::jsonb),
	COALESCE((SELECT jsonb_agg(jsonb_build_object('id',id,'type',kind,'severity',severity,
		'systemName',system_name,'organizationName',organization_name,'status',status,'triggeredAt',triggered_at)
		ORDER BY triggered_at DESC,id DESC) FROM recent_alerts),'[]'::jsonb)
FROM counts
`

// handleDistrictResponse 在同一查询快照内读取所选范围的处置和联动内容。
// 参数 pool 为业务数据库，windows 为审核阶段的工作日配置。
// 返回值 校验范围后查询区县数据的 HTTP 处理器。
func handleDistrictResponse(pool *pgxpool.Pool, windows workflowreminders.Windows) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		district, valid := responseDistrict(r.URL.RawQuery)
		if !valid {
			httpx.Error(w, http.StatusBadRequest, "invalid_input", "请选择全域或一个有效的区县范围。")
			return
		}
		result := districtResponse{District: district}
		err := readDistrictResponse(r.Context(), pool, &result, windows)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "暂时无法读取区县处置态势。")
			return
		}
		httpx.JSON(w, http.StatusOK, result)
	}
}

// readDistrictResponse 用同一个只读快照查询区县处置和全屏指标。
// 参数 ctx 控制读取，pool 为数据库，result 为选定范围及输出，windows 为审核时限。
// 返回值 任一查询失败时的错误。
func readDistrictResponse(ctx context.Context, pool *pgxpool.Pool, result *districtResponse,
	windows workflowreminders.Windows,
) error {
	if result.District == nil {
		return scanDistrictDetails(ctx, pool, result)
	}
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	return pgx.BeginTxFunc(ctx, pool, options, func(tx pgx.Tx) error {
		if err := scanDistrictDetails(ctx, tx, result); err != nil {
			return err
		}
		stats, err := readStatistics(ctx, tx, result.District)
		if err != nil {
			return err
		}
		overview, err := readDistrictOverview(ctx, tx, result.District, windows)
		if err != nil {
			return err
		}
		analysis, err := readAnalysis(ctx, tx, result.District)
		if err != nil {
			return err
		}
		result.Statistics, result.Overview, result.Analytics = &stats.dashboardStatistics, &overview, &analysis
		return nil
	})
}

// scanDistrictDetails 读取当前范围的重点隐患、排行和预警。
// 参数 ctx 控制读取，db 为数据库或事务，result 保存范围和查询结果。
// 返回值 查询或解码错误。
func scanDistrictDetails(ctx context.Context, db dashboardQueryer, result *districtResponse) error {
	return db.QueryRow(ctx, districtResponseSQL, result.District).
		Scan(&result.Counts, &result.Tasks, &result.HighRiskSystems, &result.Alerts)
}

// responseDistrict 校验互斥的全域与区县参数，区县空值表示未归区。
// 参数 raw 为未经解码的 URL 查询串。
// 返回值 全域为 nil，其他有效范围为区县名指针，同时返回查询有效性。
func responseDistrict(raw string) (*string, bool) {
	values, err := url.ParseQuery(raw)
	if err != nil || len(values) != 1 {
		return nil, false
	}
	if scope := values["scope"]; len(scope) == 1 && scope[0] == "all" {
		return nil, true
	}
	if len(values["district"]) != 1 {
		return nil, false
	}
	district := values["district"][0]
	if !validClassificationText(district, 200) {
		return nil, false
	}
	district = strings.TrimSpace(district)
	return &district, true
}
