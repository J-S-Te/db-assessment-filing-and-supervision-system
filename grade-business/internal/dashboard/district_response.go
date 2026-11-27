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
	Risk            districtRiskIndex    `json:"risk"`
	Duty            districtDutySummary  `json:"duty"`
	Comparison      dashboardComparison  `json:"comparison"`
	Tasks           []districtTask       `json:"tasks"`
	HighRiskSystems []systemRisk         `json:"highRiskSystems"`
	Alerts          []districtAlert      `json:"alerts"`
	Statistics      *dashboardStatistics `json:"statistics,omitempty"`
	Overview        *districtOverview    `json:"overview,omitempty"`
	Analytics       *dashboardAnalysis   `json:"analytics,omitempty"`
}

type districtWorkCounts struct {
	districtResponseCounts
	PendingReview     int64 `json:"pendingReview"`
	PendingPlanReview int64 `json:"pendingPlanReview"`
	PendingAlerts     int64 `json:"pendingAlerts"`
}

var districtResponseSQL = `
WITH scoped_filings AS (
	SELECT f.id,f.system_name,f.organization_name
	FROM business_cases f ` + filingDistrictJoinSQL + `
	WHERE ` + eligibleIndustrySQL + ` AND ` + scopedFilingDistrictSQL + `
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
), district_scores AS (
	SELECT COALESCE($1::jsonb ->> (` + filingDistrictSQL + `),
	       $3::jsonb ->> (` + filingDistrictSQL + `), ` + filingDistrictSQL + `) AS district,
	       count(*) FILTER (WHERE h.deadline<` + districtBusinessDaySQL + `) AS overdue,
	       count(*) FILTER (WHERE h.severity='high') AS high,
	       count(*) FILTER (WHERE h.status='pending_retest') AS "pendingRetest"
	FROM hazard_items h JOIN hazard_context hc ON hc.id=h.id
	JOIN business_cases f ON f.id=hc.filing_id AND f.kind='filing'
	` + filingDistrictJoinSQL + `
	WHERE h.status NOT IN ('pending_dispatch','closed')
	  AND ` + eligibleIndustrySQL + ` AND ` + scopedFilingDistrictSQL + `
	GROUP BY 1
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
		ORDER BY triggered_at DESC,id DESC) FROM recent_alerts),'[]'::jsonb),
	(SELECT district FROM district_scores WHERE district<>''
		ORDER BY ` + riskWeightedSQL("") + ` DESC,district LIMIT 1),
	COALESCE((SELECT LEAST(100,` + riskWeightedSQL("") + `) FROM district_scores WHERE district<>''
		ORDER BY ` + riskWeightedSQL("") + ` DESC,district LIMIT 1),0)
FROM counts
`

// handleDistrictResponse 在同一查询快照内读取所选范围的处置和联动内容。
// 参数 pool 为业务数据库，windows 为审核阶段的工作日配置。
// 返回值 校验范围后查询区县数据的 HTTP 处理器。
func handleDistrictResponse(pool *pgxpool.Pool, windows workflowreminders.Windows) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_input", "请选择全域或一个有效的区县范围。")
			return
		}
		district, valid := responseDistrict(values)
		if !valid {
			httpx.Error(w, http.StatusBadRequest, "invalid_input", "请选择全域或一个有效的区县范围。")
			return
		}
		windowDays, valid := comparisonWindowFromValues(values)
		if !valid {
			httpx.Error(w, http.StatusBadRequest, "invalid_input", "对比窗口天数须为 1 至 365。")
			return
		}
		district, scope, authorized := resolveDistrictScope(r.Context(), district)
		if !authorized {
			httpx.Error(w, http.StatusForbidden, "permission_denied", "当前账号无权查看该区县。")
			return
		}
		result := districtResponse{District: district}
		if err := readDistrictResponse(r.Context(), pool, &result, scope, windows, windowDays); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "暂时无法读取区县处置态势。")
			return
		}
		httpx.JSON(w, http.StatusOK, result)
	}
}

// readDistrictResponse 用同一个只读快照查询区县处置和全屏指标。
// 参数 ctx 控制读取，pool 为数据库，result 为响应，scope 为授权区县，windows 为审核时限。
// 返回值 任一查询失败时的错误。
func readDistrictResponse(ctx context.Context, pool *pgxpool.Pool, result *districtResponse,
	scope districtScope, windows workflowreminders.Windows, windowDays int,
) error {
	if scope.aliases == nil {
		if err := scanDistrictDetails(ctx, pool, result, scope); err != nil {
			return err
		}
		comparison, err := readComparison(ctx, pool, scope, windowDays)
		if err != nil {
			return err
		}
		result.Comparison = comparison
		return nil
	}
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	return pgx.BeginTxFunc(ctx, pool, options, func(tx pgx.Tx) error {
		if err := scanDistrictDetails(ctx, tx, result, scope); err != nil {
			return err
		}
		stats, err := readStatistics(ctx, tx, scope)
		if err != nil {
			return err
		}
		overview, err := readDistrictOverview(ctx, tx, scope, windows)
		if err != nil {
			return err
		}
		analysis, err := readAnalysis(ctx, tx, scope)
		if err != nil {
			return err
		}
		comparison, err := readComparison(ctx, tx, scope, windowDays)
		if err != nil {
			return err
		}
		result.Statistics, result.Overview, result.Analytics = &stats.dashboardStatistics, &overview, &analysis
		result.Comparison = comparison
		return nil
	})
}

// scanDistrictDetails 读取当前范围的重点隐患、排行和预警。
// 参数 ctx 控制读取，db 为数据库或事务，result 保存查询结果，scope 为授权区县。
// 返回值 查询或解码错误。
func scanDistrictDetails(ctx context.Context, db dashboardQueryer, result *districtResponse, scope districtScope) error {
	var topDistrict *string
	var topIndex int64
	err := db.QueryRow(ctx, districtResponseSQL, scope.aliases, scope.requireUnit, dashboardDistrictNames).
		Scan(&result.Counts, &result.Tasks, &result.HighRiskSystems, &result.Alerts, &topDistrict, &topIndex)
	if err != nil {
		return err
	}
	result.Risk = buildRiskIndex(result.Counts.Overdue, result.Counts.High, result.Counts.PendingRetest)
	top := ""
	if topDistrict != nil {
		top = *topDistrict
	}
	result.Duty = buildDutySummary(result.Counts, result.Risk, top, topIndex)
	return nil
}

// responseDistrict 校验互斥的全域与区县参数，区县空值表示未归区。
// 参数 values 为已解析的请求查询参数，对比窗口参数不计入范围校验。
// 返回值 全域为 nil，其他有效范围为区县名指针，同时返回查询有效性。
func responseDistrict(values url.Values) (*string, bool) {
	scoped := url.Values{}
	for key, list := range values {
		if key == comparisonWindowParam {
			continue
		}
		scoped[key] = list
	}
	if len(scoped) != 1 {
		return nil, false
	}
	if scope := scoped["scope"]; len(scope) == 1 && scope[0] == "all" {
		return nil, true
	}
	if len(scoped["district"]) != 1 {
		return nil, false
	}
	district := scoped["district"][0]
	if !validClassificationText(district, 200) {
		return nil, false
	}
	district = strings.TrimSpace(district)
	return &district, true
}
