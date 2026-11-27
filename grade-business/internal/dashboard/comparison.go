package dashboard

import (
	"context"
	"net/url"
	"strconv"
)

// 对比窗口长度：当前窗口、上期窗口与去年同期窗口使用同样的天数。
const comparisonWindowDays = 30

// 对比窗口允许的天数上限，避免超出数据保留范围后取到空窗口。
const comparisonMaxWindowDays = 365

// dashboardMetricDelta 描述一个指标在当前窗口与两个对比窗口的取值变化。
type dashboardMetricDelta struct {
	Current  int64  `json:"current"`
	Previous int64  `json:"previous"`
	YearAgo  int64  `json:"yearAgo"`
	Change   int64  `json:"change"`
	Rate     *int64 `json:"rate"`
	Worse    bool   `json:"worse"`
}

// dashboardCompletionDelta 描述一个比率指标的变化，单位为百分点。
type dashboardCompletionDelta struct {
	Current    int64 `json:"current"`
	Previous   int64 `json:"previous"`
	YearAgo    int64 `json:"yearAgo"`
	Change     int64 `json:"change"`
	HasCurrent bool  `json:"hasCurrent"`
}

// dashboardComparison 描述当前范围在当前窗口、上期窗口与去年同期窗口的对比。
type dashboardComparison struct {
	WindowDays      int                      `json:"windowDays"`
	HasYearAgo      bool                     `json:"hasYearAgo"`
	FilingsCreated  dashboardMetricDelta     `json:"filingsCreated"`
	ReportsCreated  dashboardMetricDelta     `json:"reportsCreated"`
	AlertsTriggered dashboardMetricDelta     `json:"alertsTriggered"`
	HazardsClosed   dashboardMetricDelta     `json:"hazardsClosed"`
	OpenHazards     dashboardMetricDelta     `json:"openHazards"`
	ActiveAlerts    dashboardMetricDelta     `json:"activeAlerts"`
	RiskIndex       dashboardMetricDelta     `json:"riskIndex"`
	CompletionRate  dashboardCompletionDelta `json:"completionRate"`
	ScopeNote       string                   `json:"scopeNote"`
}

// comparisonScopeNote 说明对比口径，避免把范围变化误读为业务变化。
const comparisonScopeNote = "按当前有效备案范围统计；上期为紧邻的等长窗口，去年同期为同一区间的去年同期。"

// comparisonSQL 在同一语句快照内统计三个窗口的流量与存量。
//
// 窗口包含起点并排除终点，历史窗口的终点是末日之后的北京时间零点。
// 流量型指标按事件发生时间落在窗口内计数；存量型指标按窗口末时点重放，
// 即"在该时点已创建且尚未销项"的隐患与"在该时点已触发且尚未关闭"的预警。
// 范围口径与驾驶舱其余面板一致，均为当前有效备案。
const comparisonSQL = dashboardScopeSQL + `, business_clock AS (
    SELECT statement_timestamp() AS as_of,
           (statement_timestamp() AT TIME ZONE 'Asia/Shanghai')::date AS today,
           ((statement_timestamp() AT TIME ZONE 'Asia/Shanghai')::date - interval '1 year')::date AS year_ago
), compare_windows AS (
    SELECT 'current'::text AS tag,
           b.as_of AS end_at, b.today AS end_day,
           (b.today - ($3::int - 1))::timestamp AT TIME ZONE 'Asia/Shanghai' AS start_at
    FROM business_clock b
    UNION ALL
    SELECT 'previous', (b.today - ($3::int - 1))::timestamp AT TIME ZONE 'Asia/Shanghai',
           b.today - $3::int,
           (b.today - ($3::int * 2 - 1))::timestamp AT TIME ZONE 'Asia/Shanghai'
    FROM business_clock b
    UNION ALL
    SELECT 'yearAgo', (b.year_ago + 1)::timestamp AT TIME ZONE 'Asia/Shanghai',
           b.year_ago,
           (b.year_ago - ($3::int - 1))::timestamp AT TIME ZONE 'Asia/Shanghai'
    FROM business_clock b
), compare_cases AS (
    SELECT
      count(*) FILTER (WHERE w.tag='current' AND c.kind='filing'
        AND c.created_at >= w.start_at AND c.created_at < w.end_at) AS cur_filings,
      count(*) FILTER (WHERE w.tag='previous' AND c.kind='filing'
        AND c.created_at >= w.start_at AND c.created_at < w.end_at) AS prev_filings,
      count(*) FILTER (WHERE w.tag='yearAgo' AND c.kind='filing'
        AND c.created_at >= w.start_at AND c.created_at < w.end_at) AS year_filings,
      count(*) FILTER (WHERE w.tag='current' AND c.kind='report'
        AND c.created_at >= w.start_at AND c.created_at < w.end_at) AS cur_reports,
      count(*) FILTER (WHERE w.tag='previous' AND c.kind='report'
        AND c.created_at >= w.start_at AND c.created_at < w.end_at) AS prev_reports,
      count(*) FILTER (WHERE w.tag='yearAgo' AND c.kind='report'
        AND c.created_at >= w.start_at AND c.created_at < w.end_at) AS year_reports
    FROM (SELECT kind,created_at,discarded_at FROM scope_filings
          UNION ALL SELECT kind,created_at,discarded_at FROM scope_reports) c
    CROSS JOIN compare_windows w
    WHERE c.discarded_at IS NULL
), compare_alerts AS (
    SELECT
      count(*) FILTER (WHERE w.tag='current'
        AND a.triggered_at >= w.start_at AND a.triggered_at < w.end_at) AS cur_triggered,
      count(*) FILTER (WHERE w.tag='previous'
        AND a.triggered_at >= w.start_at AND a.triggered_at < w.end_at) AS prev_triggered,
      count(*) FILTER (WHERE w.tag='yearAgo'
        AND a.triggered_at >= w.start_at AND a.triggered_at < w.end_at) AS year_triggered,
      count(*) FILTER (WHERE w.tag='current' AND a.triggered_at < w.end_at
        AND (a.closed_at IS NULL OR a.closed_at >= w.end_at)) AS cur_active,
      count(*) FILTER (WHERE w.tag='previous' AND a.triggered_at < w.end_at
        AND (a.closed_at IS NULL OR a.closed_at >= w.end_at)) AS prev_active,
      count(*) FILTER (WHERE w.tag='yearAgo' AND a.triggered_at < w.end_at
        AND (a.closed_at IS NULL OR a.closed_at >= w.end_at)) AS year_active
    FROM scope_alerts a CROSS JOIN compare_windows w
), compare_hazards AS (
    SELECT
      count(*) FILTER (WHERE w.tag='current' AND h.closed_at IS NOT NULL
        AND h.closed_at >= w.start_at AND h.closed_at < w.end_at) AS cur_closed,
      count(*) FILTER (WHERE w.tag='previous' AND h.closed_at IS NOT NULL
        AND h.closed_at >= w.start_at AND h.closed_at < w.end_at) AS prev_closed,
      count(*) FILTER (WHERE w.tag='yearAgo' AND h.closed_at IS NOT NULL
        AND h.closed_at >= w.start_at AND h.closed_at < w.end_at) AS year_closed,
      count(*) FILTER (WHERE w.tag='current' AND h.created_at < w.end_at
        AND (h.closed_at IS NULL OR h.closed_at >= w.end_at)) AS cur_open,
      count(*) FILTER (WHERE w.tag='previous' AND h.created_at < w.end_at
        AND (h.closed_at IS NULL OR h.closed_at >= w.end_at)) AS prev_open,
      count(*) FILTER (WHERE w.tag='yearAgo' AND h.created_at < w.end_at
        AND (h.closed_at IS NULL OR h.closed_at >= w.end_at)) AS year_open,
      count(*) FILTER (WHERE w.tag='current' AND h.created_at < w.end_at
        AND (h.closed_at IS NULL OR h.closed_at >= w.end_at)
        AND h.severity='high') AS cur_high,
      count(*) FILTER (WHERE w.tag='previous' AND h.created_at < w.end_at
        AND (h.closed_at IS NULL OR h.closed_at >= w.end_at)
        AND h.severity='high') AS prev_high,
      count(*) FILTER (WHERE w.tag='yearAgo' AND h.created_at < w.end_at
        AND (h.closed_at IS NULL OR h.closed_at >= w.end_at)
        AND h.severity='high') AS year_high,
      count(*) FILTER (WHERE w.tag='current' AND h.created_at < w.end_at
        AND (h.closed_at IS NULL OR h.closed_at >= w.end_at)
        AND h.status='pending_retest') AS cur_retest,
      count(*) FILTER (WHERE w.tag='previous' AND h.created_at < w.end_at
        AND (h.closed_at IS NULL OR h.closed_at >= w.end_at)
        AND h.status='pending_retest') AS prev_retest,
      count(*) FILTER (WHERE w.tag='yearAgo' AND h.created_at < w.end_at
        AND (h.closed_at IS NULL OR h.closed_at >= w.end_at)
        AND h.status='pending_retest') AS year_retest,
      count(*) FILTER (WHERE w.tag='current' AND h.created_at < w.end_at
        AND (h.closed_at IS NULL OR h.closed_at >= w.end_at)
        AND h.deadline < w.end_day) AS cur_overdue,
      count(*) FILTER (WHERE w.tag='previous' AND h.created_at < w.end_at
        AND (h.closed_at IS NULL OR h.closed_at >= w.end_at)
        AND h.deadline < w.end_day) AS prev_overdue,
      count(*) FILTER (WHERE w.tag='yearAgo' AND h.created_at < w.end_at
        AND (h.closed_at IS NULL OR h.closed_at >= w.end_at)
        AND h.deadline < w.end_day) AS year_overdue,
      count(*) FILTER (WHERE w.tag='current' AND h.created_at < w.end_at
        AND h.closed_at IS NOT NULL AND h.closed_at < w.end_at) AS cur_closed_total,
      count(*) FILTER (WHERE w.tag='previous' AND h.created_at < w.end_at
        AND h.closed_at IS NOT NULL AND h.closed_at < w.end_at) AS prev_closed_total,
      count(*) FILTER (WHERE w.tag='yearAgo' AND h.created_at < w.end_at
        AND h.closed_at IS NOT NULL AND h.closed_at < w.end_at) AS year_closed_total
    FROM scope_hazards h CROSS JOIN compare_windows w
    WHERE h.status <> 'pending_dispatch'
)
SELECT c.cur_filings,c.prev_filings,c.year_filings,
       c.cur_reports,c.prev_reports,c.year_reports,
       a.cur_triggered,a.prev_triggered,a.year_triggered,
       a.cur_active,a.prev_active,a.year_active,
       h.cur_closed,h.prev_closed,h.year_closed,
       h.cur_open,h.prev_open,h.year_open,
       h.cur_high,h.prev_high,h.year_high,
       h.cur_retest,h.prev_retest,h.year_retest,
       h.cur_overdue,h.prev_overdue,h.year_overdue,
       h.cur_closed_total,h.prev_closed_total,h.year_closed_total
FROM compare_cases c CROSS JOIN compare_alerts a CROSS JOIN compare_hazards h
`

// readComparison 读取当前范围在三个窗口内的真实对比数据。
// 参数 ctx 控制读取，db 为数据库或读取事务，scope 为授权范围，windowDays 为窗口天数。
// 返回值 对比结果或查询错误。
func readComparison(ctx context.Context, db dashboardQueryer, scope districtScope,
	windowDays int,
) (dashboardComparison, error) {
	if windowDays <= 0 {
		windowDays = comparisonWindowDays
	}
	if windowDays > comparisonMaxWindowDays {
		windowDays = comparisonMaxWindowDays
	}
	var curFilings, prevFilings, yearFilings int64
	var curReports, prevReports, yearReports int64
	var curTriggered, prevTriggered, yearTriggered int64
	var curActive, prevActive, yearActive int64
	var curClosed, prevClosed, yearClosed int64
	var curOpen, prevOpen, yearOpen int64
	var curHigh, prevHigh, yearHigh int64
	var curRetest, prevRetest, yearRetest int64
	var curOverdue, prevOverdue, yearOverdue int64
	var curClosedTotal, prevClosedTotal, yearClosedTotal int64
	err := db.QueryRow(ctx, comparisonSQL, scope.aliases, scope.requireUnit, windowDays).Scan(
		&curFilings, &prevFilings, &yearFilings,
		&curReports, &prevReports, &yearReports,
		&curTriggered, &prevTriggered, &yearTriggered,
		&curActive, &prevActive, &yearActive,
		&curClosed, &prevClosed, &yearClosed,
		&curOpen, &prevOpen, &yearOpen,
		&curHigh, &prevHigh, &yearHigh,
		&curRetest, &prevRetest, &yearRetest,
		&curOverdue, &prevOverdue, &yearOverdue,
		&curClosedTotal, &prevClosedTotal, &yearClosedTotal,
	)
	if err != nil {
		return dashboardComparison{}, err
	}

	result := dashboardComparison{
		WindowDays:      windowDays,
		ScopeNote:       comparisonScopeNote,
		FilingsCreated:  metricDelta(curFilings, prevFilings, yearFilings, directionNeutral),
		ReportsCreated:  metricDelta(curReports, prevReports, yearReports, directionNeutral),
		AlertsTriggered: metricDelta(curTriggered, prevTriggered, yearTriggered, directionWorseWhenRising),
		HazardsClosed:   metricDelta(curClosed, prevClosed, yearClosed, directionWorseWhenFalling),
		OpenHazards:     metricDelta(curOpen, prevOpen, yearOpen, directionWorseWhenRising),
		ActiveAlerts:    metricDelta(curActive, prevActive, yearActive, directionWorseWhenRising),
		RiskIndex: metricDelta(
			comparisonRiskIndex(curOverdue, curHigh, curRetest),
			comparisonRiskIndex(prevOverdue, prevHigh, prevRetest),
			comparisonRiskIndex(yearOverdue, yearHigh, yearRetest),
			directionWorseWhenRising,
		),
	}
	result.HasYearAgo = yearFilings+yearReports+yearTriggered+yearClosed+yearOpen > 0
	result.CompletionRate = comparisonCompletion(
		completionRate(curClosedTotal, curOpen),
		completionRate(prevClosedTotal, prevOpen),
		completionRate(yearClosedTotal, yearOpen),
	)
	return result, nil
}

// 指标变化的方向语义：上升为恶化、下降为恶化以及与方向无关的业务量。
const (
	directionWorseWhenRising  = 1
	directionWorseWhenFalling = -1
	directionNeutral          = 0
)

// metricDelta 计算一个指标相对上期窗口的变化。
// 参数 current、previous、yearAgo 为三个窗口的取值，direction 表示变化方向的业务含义。
// 返回值 指标变化，previous 为零时变化率为空，业务量指标恒不判定为恶化。
func metricDelta(current, previous, yearAgo int64, direction int) dashboardMetricDelta {
	delta := dashboardMetricDelta{
		Current: current, Previous: previous, YearAgo: yearAgo, Change: current - previous,
	}
	if previous > 0 {
		rate := (current - previous) * 100 / previous
		delta.Rate = &rate
	}
	switch direction {
	case directionWorseWhenRising:
		delta.Worse = current > previous
	case directionWorseWhenFalling:
		delta.Worse = current < previous
	default:
		delta.Worse = false
	}
	return delta
}

// comparisonRiskIndex 按统一口径计算某一时点的综合风险指数。
// 参数 overdue、high、pendingRetest 为该时点的逾期、高风险与待复测隐患数。
// 返回值 0 至 100 的综合风险指数。
func comparisonRiskIndex(overdue, high, pendingRetest int64) int64 {
	score := overdue*riskWeightOverdue + high*riskWeightHigh + pendingRetest*riskWeightRetest
	if score > riskIndexCeiling {
		return riskIndexCeiling
	}
	return score
}

// completionRate 计算某一时点的整改完成率。
// 参数 closed 为该时点已销项数，open 为该时点未销项数。
// 返回值 0 至 100 的完成率，无可用分母时返回零。
func completionRate(closed, open int64) int64 {
	total := closed + open
	if total <= 0 {
		return 0
	}
	return closed * 100 / total
}

// comparisonCompletion 计算整改完成率在三个窗口的变化，单位为百分点。
// 参数 current、previous、yearAgo 为三个窗口的完成率。
// 返回值 完成率变化，当前窗口无数据时标记不可用。
func comparisonCompletion(current, previous, yearAgo int64) dashboardCompletionDelta {
	return dashboardCompletionDelta{
		Current: current, Previous: previous, YearAgo: yearAgo,
		Change: current - previous, HasCurrent: current > 0 || previous > 0 || yearAgo > 0,
	}
}

// comparisonWindowParam 是请求中指定对比窗口天数的参数名。
const comparisonWindowParam = "window"

// comparisonWindowFromValues 读取请求指定的对比窗口天数。
// 参数 values 为已解析的请求查询参数。
// 返回值 通过验证的窗口天数和参数是否有效。
func comparisonWindowFromValues(values url.Values) (int, bool) {
	raw := values[comparisonWindowParam]
	if len(raw) == 0 {
		return comparisonWindowDays, true
	}
	if len(raw) != 1 {
		return 0, false
	}
	days, err := strconv.Atoi(raw[0])
	if err != nil || days < 1 || days > comparisonMaxWindowDays {
		return 0, false
	}
	return days, true
}
