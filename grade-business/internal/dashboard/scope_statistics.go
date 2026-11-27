package dashboard

import (
	"context"

	"grade-business/internal/workflowreminders"
)

// 备案使用当前单位名录归属，测评、隐患和预警使用各表已登记的备案关联。
// 区县风险沿用处置面板的有效备案范围，备案自身统计保留历史状态。
const dashboardScopeSQL = `
WITH scope_filings AS (
	SELECT f.* FROM business_cases f ` + filingDistrictJoinSQL + `
	WHERE f.kind='filing' AND ` + scopedFilingDistrictSQL + `
), scope_reports AS (
	SELECT r.* FROM business_cases r JOIN scope_filings f ON f.id=r.filing_id WHERE r.kind='report'
), scope_hazards AS (
	SELECT h.*,f.id AS scope_filing_id
	FROM hazard_items h JOIN hazard_context hc ON hc.id=h.id
	JOIN scope_filings f ON f.id=hc.filing_id
	WHERE $1::jsonb IS NULL OR (` + eligibleIndustrySQL + `)
), scope_alerts AS (
	SELECT a.* FROM local_alerts a JOIN scope_hazards h ON h.id=a.hazard_id
	WHERE $1::jsonb IS NULL OR h.status<>'pending_dispatch'
)`

type districtAlertTotals struct {
	Total      int64 `json:"total"`
	Pending    int64 `json:"pending"`
	Processing int64 `json:"processing"`
	Closed     int64 `json:"closed"`
}

type districtDeadlines struct {
	Overdue int64 `json:"overdue"`
}

type districtOverview struct {
	FilingTotal        int64               `json:"filingTotal"`
	FilingsThisYear    int64               `json:"filingsThisYear"`
	ImplementationOpen int64               `json:"implementationOpen"`
	ReportInitial      int64               `json:"reportInitial"`
	ReportFinal        int64               `json:"reportFinal"`
	HazardTotal        int64               `json:"hazardTotal"`
	HazardClosed       int64               `json:"hazardClosed"`
	AlertCounts        districtAlertTotals `json:"alertCounts"`
	ReviewDeadlines    *districtDeadlines  `json:"reviewDeadlines,omitempty"`
}

const districtOverviewSQL = dashboardScopeSQL + `,
scope_implementations AS (
	SELECT i.id,i.status FROM implementation_reports i JOIN scope_filings f ON f.id=i.filing_id
), review_records AS (
	SELECT 'filings' AS resource,id FROM scope_filings
	UNION ALL SELECT 'reports',id FROM scope_reports
	UNION ALL SELECT 'implementations',id FROM scope_implementations
	UNION ALL SELECT 'filing-applications',a.id FROM filing_applications a
		JOIN scope_filings f ON f.id=a.filing_id
	UNION ALL SELECT 'remediations',p.id FROM remediation_plans p JOIN scope_reports r ON r.id=p.report_id
), review_overdue AS (
	SELECT count(*) AS overdue
	FROM workflow_tasks w JOIN review_records r ON r.id=w.id AND r.resource=w.resource
	WHERE w.audience='regulator' AND w.recipient='' AND w.window_kind<>''
	 AND COALESCE(w.explicit_deadline,workflow_due((w.stage_at AT TIME ZONE 'Asia/Shanghai')::date,
		CASE w.window_kind WHEN 'filing_initial' THEN $3::int WHEN 'filing_final' THEN $4::int
		     WHEN 'report' THEN $5::int WHEN 'implementation' THEN $6::int
		     WHEN 'remediation' THEN $7::int END)) < ` + districtBusinessDaySQL + `
), filings AS (
	SELECT count(*) AS total,
	       count(*) FILTER (WHERE created_at>=date_trunc('year',CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')
	           AT TIME ZONE 'Asia/Shanghai') AS this_year
	FROM scope_filings WHERE status<>'withdrawn' AND discarded_at IS NULL
), reports AS (
	SELECT count(*) FILTER (WHERE status='pending_initial') AS initial,
	       count(*) FILTER (WHERE status='pending_final') AS final FROM scope_reports
), hazards AS (
	SELECT count(*) AS total,count(*) FILTER (WHERE status='closed') AS closed FROM scope_hazards
), alerts AS (
	SELECT count(*) AS total,count(*) FILTER (WHERE status='pending') AS pending,
	       count(*) FILTER (WHERE status='processing') AS processing,
	       count(*) FILTER (WHERE status='closed') AS closed
	FROM scope_alerts
)
SELECT f.total,f.this_year,
	(SELECT count(*) FROM scope_implementations
	 WHERE status IN ('submitted','pending_initial','pending_final','pending_supervision','returned')),
	r.initial,r.final,h.total,h.closed,a.total,a.pending,a.processing,a.closed,d.overdue
FROM filings f CROSS JOIN reports r CROSS JOIN hazards h CROSS JOIN alerts a CROSS JOIN review_overdue d
`

// readDistrictOverview 汇总所选区县的业务总量、预警和审核逾期指标。
// 参数 ctx 为查询上下文，db 为读取事务，scope 为授权区县，windows 为业务审核时限。
// 返回值 区县概览及查询错误，未启用审核时限时省略该指标。
func readDistrictOverview(ctx context.Context, db dashboardQueryer, scope districtScope,
	windows workflowreminders.Windows,
) (districtOverview, error) {
	var result districtOverview
	var overdue districtDeadlines
	alerts := &result.AlertCounts
	err := db.QueryRow(ctx, districtOverviewSQL, scope.aliases, scope.requireUnit, windows.FilingInitial, windows.FilingFinal,
		windows.Report, windows.Implementation, windows.Remediation).
		Scan(&result.FilingTotal, &result.FilingsThisYear, &result.ImplementationOpen,
			&result.ReportInitial, &result.ReportFinal, &result.HazardTotal, &result.HazardClosed,
			&alerts.Total, &alerts.Pending, &alerts.Processing, &alerts.Closed, &overdue.Overdue)
	if windows != (workflowreminders.Windows{}) {
		result.ReviewDeadlines = &overdue
	}
	return result, err
}
