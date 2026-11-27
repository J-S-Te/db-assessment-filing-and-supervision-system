package dashboard

import (
	"fmt"
	"strings"
)

// 综合风险指数口径：逾期隐患与高风险隐患各占 40%，待机构复测占 20%。
const (
	riskWeightOverdue   int64 = 40
	riskWeightHigh      int64 = 40
	riskWeightRetest    int64 = 20
	riskIndexCeiling    int64 = 100
	riskLevelHighCutoff int64 = 66
	riskLevelMidCutoff  int64 = 34
)

// riskIndexWeightsText 说明综合风险指数的计算口径，供界面直接展示。
const riskIndexWeightsText = "逾期 40% / 高风险 40% / 待复测 20%"

// riskWeightedSQL 按统一口径生成未封顶加权分的 SQL 表达式。
// 参数 prefix 为列所属的表或子查询别名，留空表示不带前缀。
// 返回值 可直接用于排序或计算的加权分表达式。
func riskWeightedSQL(prefix string) string {
	head := ""
	if prefix != "" {
		head = prefix + "."
	}
	return fmt.Sprintf("(%soverdue*%d+%shigh*%d+%s\"pendingRetest\"*%d)",
		head, riskWeightOverdue, head, riskWeightHigh, head, riskWeightRetest)
}

// riskIndexSQL 按统一口径生成加权分、封顶指数与分级的 SQL 表达式。
// 参数 prefix 为列所属的表或子查询别名，留空表示不带前缀。
// 返回值 可直接拼进查询语句的三段表达式。
func riskIndexSQL(prefix string) string {
	weighted := riskWeightedSQL(prefix)
	capped := fmt.Sprintf("LEAST(%d,%s)", riskIndexCeiling, weighted)
	level := fmt.Sprintf("CASE WHEN %s>=%d THEN 'high' WHEN %s>=%d THEN 'mid' WHEN %s>0 THEN 'low' ELSE 'none' END",
		capped, riskLevelHighCutoff, capped, riskLevelMidCutoff, capped)
	return weighted + ` AS "riskScore",` + capped + ` AS "riskIndex",` + level + ` AS "riskLevel"`
}

// districtRiskIndex 描述当前范围未销项隐患的综合风险指数。
type districtRiskIndex struct {
	Index   int64  `json:"index"`
	Score   int64  `json:"score"`
	Level   string `json:"level"`
	Weights string `json:"weights"`
}

// districtDutySummary 描述当前范围需要监管处理的待办汇总。
type districtDutySummary struct {
	Total             int64  `json:"total"`
	OverdueHazards    int64  `json:"overdueHazards"`
	PendingReview     int64  `json:"pendingReview"`
	PendingAlerts     int64  `json:"pendingAlerts"`
	PendingPlanReview int64  `json:"pendingPlanReview"`
	TopDistrict       string `json:"topDistrict,omitempty"`
	TopDistrictIndex  int64  `json:"topDistrictIndex"`
	Conclusion        string `json:"conclusion"`
}

// buildRiskIndex 按统一口径计算综合风险指数及其分级。
// 参数 overdue、high、pendingRetest 为当前范围内对应状态的未销项隐患数。
// 返回值 0 至 100 的综合风险指数、未封顶加权分、分级与口径说明。
func buildRiskIndex(overdue, high, pendingRetest int64) districtRiskIndex {
	score := overdue*riskWeightOverdue + high*riskWeightHigh + pendingRetest*riskWeightRetest
	index := score
	if index > riskIndexCeiling {
		index = riskIndexCeiling
	}
	return districtRiskIndex{
		Index:   index,
		Score:   score,
		Level:   riskLevel(index),
		Weights: riskIndexWeightsText,
	}
}

// riskLevel 按综合风险指数返回界面使用的分级。
// 参数 index 为 0 至 100 的综合风险指数。
// 返回值 high、mid、low 或 none。
func riskLevel(index int64) string {
	switch {
	case index >= riskLevelHighCutoff:
		return "high"
	case index >= riskLevelMidCutoff:
		return "mid"
	case index > 0:
		return "low"
	default:
		return "none"
	}
}

// buildDutySummary 汇总当前范围待办数量并生成可直接展示的结论。
// 参数 counts 为当前范围的工作计数，risk 为综合风险指数，topDistrict 与 topIndex 为范围内最重区县及其指数。
// 返回值 待办汇总与中文结论。
func buildDutySummary(counts districtWorkCounts, risk districtRiskIndex,
	topDistrict string, topIndex int64,
) districtDutySummary {
	summary := districtDutySummary{
		OverdueHazards:    counts.Overdue,
		PendingReview:     counts.PendingReview,
		PendingAlerts:     counts.PendingAlerts,
		PendingPlanReview: counts.PendingPlanReview,
		TopDistrict:       topDistrict,
		TopDistrictIndex:  topIndex,
	}
	summary.Total = summary.OverdueHazards + summary.PendingReview +
		summary.PendingAlerts + summary.PendingPlanReview
	summary.Conclusion = dutyConclusion(summary, risk)
	return summary
}

// dutyConclusion 生成当前范围的态势结论文字。
// 参数 summary 为待办汇总，risk 为综合风险指数。
// 返回值 中文结论文本，无待办时给出明确说明。
func dutyConclusion(summary districtDutySummary, risk districtRiskIndex) string {
	if summary.Total == 0 {
		return "当前范围暂无待办事项。"
	}
	details := make([]string, 0, 4)
	if summary.OverdueHazards > 0 {
		details = append(details, fmt.Sprintf("%d 项已逾期", summary.OverdueHazards))
	}
	if summary.PendingAlerts > 0 {
		details = append(details, fmt.Sprintf("%d 条待处理预警", summary.PendingAlerts))
	}
	if summary.PendingReview > 0 {
		details = append(details, fmt.Sprintf("%d 项待监管复核", summary.PendingReview))
	}
	if summary.PendingPlanReview > 0 {
		details = append(details, fmt.Sprintf("%d 项待审核整改规划", summary.PendingPlanReview))
	}
	parts := []string{fmt.Sprintf("当前范围待办 %d 项", summary.Total)}
	if len(details) > 0 {
		parts = append(parts, "其中 "+strings.Join(details, "、"))
	}
	if summary.TopDistrict != "" && summary.TopDistrictIndex > 0 {
		parts = append(parts, fmt.Sprintf("%s 风险指数最高（%d），建议优先督办",
			summary.TopDistrict, summary.TopDistrictIndex))
	}
	return strings.Join(parts, "；") + "。"
}
