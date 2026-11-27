package dashboard

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/httpx"
)

type districtCounts struct {
	Systems int64 `json:"systems"`
	High    int64 `json:"high"`
	Medium  int64 `json:"medium"`
	Low     int64 `json:"low"`
}

type districtRisk struct {
	District string `json:"district"`
	districtResponseCounts
	RiskIndex int64  `json:"riskIndex"`
	RiskLevel string `json:"riskLevel"`
}

type districtResponseCounts struct {
	districtCounts
	Overdue       int64 `json:"overdue"`
	PendingRetest int64 `json:"pendingRetest"`
}

type districtRiskSummary struct {
	Items            []districtRisk         `json:"items"`
	WithoutDistrict  districtResponseCounts `json:"withoutDistrict"`
	OmittedDistricts int64                  `json:"omittedDistricts"`
}

const districtDefaultLimit = 20
const districtMaxLimit = 200
const districtBusinessDaySQL = `(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date`
const filingDistrictSQL = `COALESCE(NULLIF(btrim(unit.district),''), '') COLLATE "C"`
const filingDistrictJoinSQL = `LEFT JOIN directory_parties unit ON unit.kind='unit' AND (
	unit.id=f.directory_unit_id OR (f.directory_unit_id IS NULL
	AND lower(unit.scope_name)=lower(f.organization_name)))`
const scopedFilingDistrictSQL = `($1::jsonb IS NULL OR $1::jsonb ? (` + filingDistrictSQL + `))
	AND (NOT $2::boolean OR unit.id=f.directory_unit_id)`
const districtPrioritySQL = `high DESC, overdue DESC, medium DESC, low DESC, district`

var districtRiskSQL = `
WITH groups AS (
	SELECT COALESCE($1::jsonb ->> (` + filingDistrictSQL + `),
	       $4::jsonb ->> (` + filingDistrictSQL + `), ` + filingDistrictSQL + `) AS district,
	       count(DISTINCT f.id) AS systems,
	       count(*) FILTER (WHERE h.severity='high') AS high,
	       count(*) FILTER (WHERE h.severity='medium') AS medium,
	       count(*) FILTER (WHERE h.severity='low') AS low,
	       count(*) FILTER (WHERE h.deadline<` + districtBusinessDaySQL + `) AS overdue,
	       count(*) FILTER (WHERE h.status='pending_retest') AS "pendingRetest"
	FROM hazard_items h JOIN hazard_context hc ON hc.id=h.id
	JOIN business_cases f ON f.id=hc.filing_id AND f.kind='filing'
	` + filingDistrictJoinSQL + `
	WHERE h.status NOT IN ('pending_dispatch','closed')
	  AND ` + eligibleIndustrySQL + ` AND ` + scopedFilingDistrictSQL + `
	GROUP BY 1
), scored AS (
	SELECT g.*,` + riskIndexSQL("g") + `
	FROM groups g
), ranked AS (
	SELECT * FROM scored WHERE district<>'' ORDER BY "riskScore" DESC,"riskIndex" DESC,high DESC,overdue DESC,district LIMIT $3
)
SELECT COALESCE((SELECT jsonb_agg(jsonb_build_object(
		'district',district,'systems',systems,'high',high,'medium',medium,'low',low,
		'overdue',overdue,'pendingRetest',"pendingRetest",'riskIndex',"riskIndex",'riskLevel',"riskLevel")
		ORDER BY "riskScore" DESC,"riskIndex" DESC,high DESC,overdue DESC,district) FROM ranked r),'[]'::jsonb),
	COALESCE((SELECT to_jsonb(s)-'district'-'riskScore' FROM scored s WHERE district=''),'{}'::jsonb),
	GREATEST((SELECT count(*) FROM groups WHERE district<>'')-$3,0)
`

// handleDistrictRisk 返回使用当前名录区县登记的未销项风险分布。
// 参数 pool 为业务数据库。
// 返回值 驾驶舱区县统计处理器。
func handleDistrictRisk(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, valid := districtRiskLimit(r.URL.RawQuery)
		if !valid {
			httpx.Error(w, http.StatusBadRequest, "invalid_input", "区县统计上限须为 1 至 200。")
			return
		}
		_, scope, authorized := resolveDistrictScope(r.Context(), nil)
		if !authorized {
			httpx.Error(w, http.StatusForbidden, "permission_denied", "当前账号无权查看区县风险。")
			return
		}
		var result districtRiskSummary
		err := pool.QueryRow(r.Context(), districtRiskSQL, scope.aliases, scope.requireUnit, limit, dashboardDistrictNames).
			Scan(&result.Items, &result.WithoutDistrict, &result.OmittedDistricts)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "暂时无法读取区县风险。")
			return
		}
		httpx.JSON(w, http.StatusOK, result)
	}
}

// districtRiskLimit 读取区县统计上限并保留原有默认范围。
// 参数 raw 为请求 URL 中的原始查询字符串。
// 返回值 通过验证的区县数量上限和参数是否有效。
func districtRiskLimit(raw string) (int, bool) {
	if raw == "" {
		return districtDefaultLimit, true
	}
	values, err := url.ParseQuery(raw)
	if err != nil || len(values) != 1 || len(values["limit"]) != 1 {
		return 0, false
	}
	limit, err := strconv.Atoi(values.Get("limit"))
	return limit, err == nil && limit >= 1 && limit <= districtMaxLimit
}
