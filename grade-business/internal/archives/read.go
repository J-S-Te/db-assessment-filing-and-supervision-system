package archives

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type archiveReader interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

const archiveFilings = `f.kind='filing' AND f.status='approved'`
const filingScope = archiveFilings + `
 AND ($1::text='' OR f.organization_name COLLATE "C"=$1)
 AND ($2::uuid IS NULL OR f.id=$2)`

const relatedReportJoin = `JOIN business_cases r ON r.filing_id=f.id AND r.kind='report' AND r.status<>'draft'`
const relatedPlanJoin = `JOIN remediation_plans p ON p.report_id=r.id AND p.status<>'draft'`
const relatedHazards = `hazard_context hc JOIN hazard_items h ON h.id=hc.id
 LEFT JOIN remediation_plans p ON p.id=h.plan_id LEFT JOIN business_cases r ON r.id=hc.report_id`
const archiveHazardScope = `hc.filing_id=f.id AND h.status<>'pending_dispatch'
 AND (h.plan_id IS NULL OR (p.status<>'draft' AND r.status<>'draft'))`
const filingStatistics = `CROSS JOIN LATERAL (
 SELECT (SELECT max(submitted_at) FROM business_cases
         WHERE filing_id=f.id AND kind='report' AND status<>'draft') AS latest_report_at,
    count(*) FILTER (WHERE h.status<>'closed') AS open_count,
    count(*) FILTER (WHERE h.status='closed') AS closed_count
 FROM ` + relatedHazards + ` WHERE ` + archiveHazardScope + `
) t`

const unitsSQL = `
WITH selected AS (
 SELECT f.organization_name COLLATE "C" AS organization, count(*) AS systems,
        count(*) FILTER (WHERE f.cancelled_at IS NULL) AS active
 FROM business_cases f
 WHERE ` + archiveFilings + ` AND f.organization_name<>''
   AND ($1::text='' OR f.organization_name COLLATE "C"=$1)
   AND ($2::text='' OR strpos(lower(f.organization_name),lower($2))>0)
   AND ($3::text='' OR f.organization_name COLLATE "C">$3)
 GROUP BY f.organization_name COLLATE "C" ORDER BY organization LIMIT $4
)
SELECT o.organization,o.systems,o.active,max(t.latest_report_at),sum(t.open_count)::bigint,sum(t.closed_count)::bigint
FROM selected o
JOIN business_cases f ON f.organization_name COLLATE "C"=o.organization AND ` + archiveFilings + `
` + filingStatistics + `
GROUP BY o.organization,o.systems,o.active ORDER BY o.organization`

const systemsSQL = `
WITH selected AS (
 SELECT f.id,f.created_at,f.number,f.system_name,f.organization_name,f.assessment_org,f.level,f.cancelled_at
 FROM business_cases f WHERE ` + filingScope + `
   AND ($3::text='' OR strpos(lower(concat_ws(' ',f.system_name,f.organization_name,f.number)),lower($3))>0)
   AND ($4::text='' OR ($4='active' AND f.cancelled_at IS NULL)
        OR ($4='cancelled' AND f.cancelled_at IS NOT NULL))
   AND ($5::text='' OR f.level=$5)
   AND ($6::timestamptz IS NULL OR (f.created_at,f.id)<($6,$7::uuid))
 ORDER BY f.created_at DESC,f.id DESC LIMIT $8
)
SELECT f.id::text,f.created_at,f.number,f.system_name,f.organization_name,f.assessment_org,f.level,
       CASE WHEN f.cancelled_at IS NULL THEN 'active' ELSE 'cancelled' END, f.cancelled_at,t.*,
       (SELECT count(*) FROM archive_system_assets a WHERE a.filing_id=f.id AND a.active
          AND a.kind IN ('ip','domain','cloud','big_data')) AS asset_count
FROM selected f
` + filingStatistics + `
ORDER BY f.created_at DESC,f.id DESC`

// readUnits 查询当前分页的单位分组、测评日期和隐患数量。
// 参数 ctx 控制查询，reader 为连接池或快照，organization 为可选精确归属，input 为列表条件。
// 返回值 有界的单位档案页，或数据库错误。
func readUnits(ctx context.Context, reader archiveReader, organization string, input listOptions) (page[unitRow], error) {
	rows, err := reader.Query(ctx, unitsSQL, organization, input.keyword, input.unitAfter, input.limit+1)
	if err != nil {
		return page[unitRow]{}, err
	}
	items, err := pgx.CollectRows(rows, scanUnit)
	if err != nil {
		return page[unitRow]{}, err
	}
	result := page[unitRow]{Items: items}
	if len(items) > input.limit {
		result.Items = items[:input.limit]
		result.NextCursor = items[input.limit-1].Key
	}
	return result, nil
}

// scanUnit 将真实备案单位分组的列转换为公开档案摘要。
// 参数 row 为已选单位分组及其统计的数据库行。
// 返回值 单位摘要或数据库扫描错误。
func scanUnit(row pgx.CollectableRow) (unitRow, error) {
	var result unitRow
	err := row.Scan(&result.OrganizationName, &result.SystemCount, &result.ActiveSystemCount,
		&result.LatestReportAt, &result.OpenHazardCount, &result.ClosedHazardCount)
	result.Key = groupKey(result.OrganizationName)
	return result, err
}

// readSystems 从指定备案范围读取系统页及每条系统的真实测评和隐患统计。
// 参数 ctx 控制查询，reader 为连接池或快照，scope 指定归属，input 为查询及分页条件。
// 返回值 有界系统档案页，或数据库错误。
func readSystems(ctx context.Context, reader archiveReader, scope archiveScope, input listOptions) (page[systemRow], error) {
	rows, err := reader.Query(ctx, systemsSQL, scope.organization, scope.filingID, input.keyword,
		input.status, input.level, input.after.stamp, input.after.id, input.limit+1)
	if err != nil {
		return page[systemRow]{}, err
	}
	return timedPage[systemRow](rows, input.limit)
}

// timedPage 读取有界有序行并为确实还有数据的列表生成游标。
// 参数 rows 为数据库返回的有序结果，limit 为实际返回条数上限。
// 返回值 可分页的公开数据，或数据库扫描错误。
func timedPage[T interface{ cursor() string }](rows pgx.Rows, limit int) (page[T], error) {
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[T])
	if err != nil {
		return page[T]{}, err
	}
	result := page[T]{Items: items}
	if len(items) > limit {
		result.Items = items[:limit]
		result.NextCursor = items[limit-1].cursor()
	}
	return result, nil
}
