package dashboard

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/httpx"
)

type industryBucket struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	districtCounts
}

type issueBucket struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	Count int64  `json:"count"`
}

type industryAnalysis struct {
	Items        []industryBucket `json:"items"`
	Unclassified districtCounts   `json:"unclassified"`
}

type commonIssues struct {
	Items        []issueBucket `json:"items"`
	Unclassified int64         `json:"unclassified"`
}

type dashboardAnalysis struct {
	Industries   industryAnalysis `json:"industries"`
	CommonIssues commonIssues     `json:"commonIssues"`
}

const eligibleIndustrySQL = `f.kind='filing' AND f.status='approved'
    AND f.cancelled_at IS NULL AND f.discarded_at IS NULL`
const eligibleIssueSQL = `h.status<>'pending_dispatch'`

const dashboardAnalysisSQL = dashboardScopeSQL + `, open_hazards AS (
    SELECT h.scope_filing_id AS filing_id, count(*) FILTER (WHERE h.severity='high') AS high,
           count(*) FILTER (WHERE h.severity='medium') AS medium,
           count(*) FILTER (WHERE h.severity='low') AS low
    FROM scope_hazards h
    WHERE ` + eligibleIssueSQL + ` AND h.status<>'closed' GROUP BY h.scope_filing_id
), industry_counts AS (
    SELECT c.code,count(*) AS systems,COALESCE(sum(r.high),0)::bigint AS high,
           COALESCE(sum(r.medium),0)::bigint AS medium,COALESCE(sum(r.low),0)::bigint AS low
    FROM scope_filings f
    LEFT JOIN dashboard_filing_industries c ON c.filing_id=f.id
    LEFT JOIN open_hazards r ON r.filing_id=f.id
    WHERE ` + eligibleIndustrySQL + ` GROUP BY c.code
), issue_counts AS (
    SELECT c.code,count(*) AS count
    FROM scope_hazards h LEFT JOIN dashboard_hazard_issues c ON c.hazard_id=h.id
    WHERE ` + eligibleIssueSQL + ` GROUP BY c.code
), ranked_issues AS (
    SELECT i.code,o.label,i.count FROM issue_counts i
    JOIN dashboard_classification_options o ON o.kind='issue' AND o.code=i.code
    ORDER BY i.count DESC,i.code COLLATE "C" LIMIT 10
)
SELECT
    COALESCE((SELECT jsonb_agg(to_jsonb(i)||jsonb_build_object('label',o.label)
        ORDER BY i.systems DESC,i.code COLLATE "C")
        FROM industry_counts i
        JOIN dashboard_classification_options o ON o.kind='industry' AND o.code=i.code),'[]'::jsonb),
    COALESCE((SELECT to_jsonb(i)-'code' FROM industry_counts i WHERE i.code IS NULL),'{}'::jsonb),
    COALESCE((SELECT jsonb_agg(to_jsonb(i) ORDER BY i.count DESC,i.code COLLATE "C")
        FROM ranked_issues i),'[]'::jsonb),
    COALESCE((SELECT count FROM issue_counts WHERE code IS NULL),0)
`

// handleAnalysis 汇总有效备案行业和有效入账隐患的问题归类。
// 参数 pool 为业务数据库。
// 返回值 驾驶舱分析请求处理器。
func handleAnalysis(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			httpx.Error(w, http.StatusBadRequest, "invalid_input", "驾驶舱分析无需查询参数。")
			return
		}
		result, err := readAnalysis(r.Context(), pool, nil)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "暂时无法读取驾驶舱分析。")
			return
		}
		httpx.JSON(w, http.StatusOK, result)
	}
}

// readAnalysis 读取同一辖区的行业及隐患问题分布。
// 参数 ctx 为查询上下文，db 为数据库或读取事务，district 为空指针时读取全域。
// 返回值 归类统计及查询错误。
func readAnalysis(ctx context.Context, db dashboardQueryer, district *string) (dashboardAnalysis, error) {
	var result dashboardAnalysis
	industries, issues := &result.Industries, &result.CommonIssues
	err := db.QueryRow(ctx, dashboardAnalysisSQL, district).Scan(
		&industries.Items, &industries.Unclassified, &issues.Items, &issues.Unclassified)
	return result, err
}
