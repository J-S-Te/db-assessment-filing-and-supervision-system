package archives

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type archiveReader interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

const archiveFilings = `f.kind='filing' AND f.status='approved'`

// 档案按已绑定单位归组。历史自由名称可按稳定名称或当前名称查询，监管授权仍使用单位外键。
const archiveUnitJoin = `LEFT JOIN LATERAL (
 SELECT u.scope_name,u.name,u.code FROM directory_parties u
 WHERE u.kind='unit' AND (u.id=f.directory_unit_id OR (f.directory_unit_id IS NULL AND (
   lower(u.scope_name)=lower(f.organization_name) OR
   (u.merged_into_id IS NULL AND lower(u.name)=lower(f.organization_name)))))
 ORDER BY (lower(u.scope_name)=lower(f.organization_name)) DESC,u.id LIMIT 1
) directory_unit ON TRUE`
const archiveOrganization = `COALESCE(directory_unit.scope_name,f.organization_name) COLLATE "C"`
const filingScope = archiveFilings + `
 AND ($1::text='' OR ` + archiveOrganization + `=$1)
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
WITH filings AS (
 SELECT f.id,f.cancelled_at,f.organization_name COLLATE "C" AS recorded_name,
        ` + archiveOrganization + ` AS organization
 FROM business_cases f ` + archiveUnitJoin + `
 WHERE ` + archiveFilings + ` AND f.organization_name<>''
), organizations AS (
 SELECT scope_name COLLATE "C" AS organization FROM directory_parties
 WHERE kind='unit' AND merged_into_id IS NULL
 UNION
 SELECT organization FROM filings
), requested_unit AS (
 SELECT CASE WHEN EXISTS (SELECT 1 FROM organizations WHERE organization=$1) THEN $1
   ELSE (SELECT min(organization) FROM filings WHERE recorded_name=$1
         HAVING count(DISTINCT organization)=1)
 END AS organization
), selected AS (
 SELECT o.organization,u.name,u.code,u.classification,u.district,u.status
 FROM organizations o LEFT JOIN directory_parties u
   ON u.kind='unit' AND u.scope_name COLLATE "C"=o.organization
 WHERE ($1::text='' OR o.organization=(SELECT organization FROM requested_unit))
   AND ($2::text='' OR strpos(lower(concat_ws(' ',o.organization,u.name,u.code)),lower($2))>0)
   AND ($3::text='' OR o.organization>$3)
 ORDER BY o.organization LIMIT $4
)
SELECT o.organization,count(f.id),count(f.id) FILTER (WHERE f.cancelled_at IS NULL),
       max(t.latest_report_at),sum(t.open_count)::bigint,sum(t.closed_count)::bigint,
       coalesce(o.name,''),coalesce(o.code,''),coalesce(o.classification,''),coalesce(o.district,''),coalesce(o.status,'')
FROM selected o
LEFT JOIN filings f ON f.organization=o.organization
` + filingStatistics + `
GROUP BY o.organization,o.name,o.code,o.classification,o.district,o.status ORDER BY o.organization`

const systemsSQL = `
WITH selected AS (
 SELECT f.id,f.created_at,f.number,f.system_name,f.organization_name,f.assessment_org,f.level,f.cancelled_at
 FROM business_cases f ` + archiveUnitJoin + `
 WHERE ` + filingScope + `
   AND ($3::text='' OR strpos(lower(concat_ws(' ',f.system_name,f.organization_name,f.number,
       directory_unit.scope_name,directory_unit.name,directory_unit.code)),lower($3))>0)
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

// scanUnit 将名录单位和备案统计转换为档案摘要。
// 参数 row 为已选单位及其统计的数据库行。
// 返回值 单位摘要或数据库扫描错误。
func scanUnit(row pgx.CollectableRow) (unitRow, error) {
	var result unitRow
	err := row.Scan(&result.OrganizationName, &result.SystemCount, &result.ActiveSystemCount,
		&result.LatestReportAt, &result.OpenHazardCount, &result.ClosedHazardCount,
		&result.DirectoryName, &result.CreditCode, &result.Classification, &result.District, &result.DirectoryStatus)
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
