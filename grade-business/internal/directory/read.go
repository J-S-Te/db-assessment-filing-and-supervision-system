package directory

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type service struct{ pool *pgxpool.Pool }

type reader interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

const partyEligible = `p.status='active' AND (p.kind='unit' OR p.valid_until >= ` + businessToday + `)`
const activeInstitution = `a.status='active' AND a.valid_until >= ` + businessToday

const activeFilings = `f.kind='filing' AND f.status='approved' AND f.cancelled_at IS NULL`
const assessmentInFlight = `c.kind='report' AND c.status IN ('pending_initial','pending_final')`
const implementationInFlight = `i.status IN ('submitted','pending_initial','pending_final','pending_supervision')`

const scopedUnit = `(
	EXISTS (SELECT 1 FROM directory_unit_memberships member JOIN directory_parties a
	    ON a.id=member.institution_id WHERE unit_id=p.id AND a.scope_name=$2)
	OR EXISTS (SELECT 1 FROM business_cases f WHERE ` + activeFilings + `
	    AND f.organization_name=p.scope_name AND f.assessment_org=$2)
)`

const exclusiveUnit = `EXISTS(
	SELECT 1 FROM directory_parties a WHERE a.id=p.owner_institution_id AND a.scope_name=$2
	AND ` + activeInstitution + `
	AND NOT EXISTS (SELECT 1 FROM directory_unit_memberships member
	    WHERE member.unit_id=p.id AND member.institution_id<>a.id)
	AND NOT EXISTS (SELECT 1 FROM business_cases f WHERE ` + activeFilings + `
	    AND f.organization_name=p.scope_name AND f.assessment_org<>$2)
)`

const entriesProjection = `id,name,scope_name,code,classification,district,qualification,valid_until,
	institution_id,institution_name,certificate_no,status,usable,can_edit,version,system_count,
	in_review_count,owner_id,merged_into_id,merge_id,merged_at`

const partySource = `
	SELECT p.id::text AS id,p.name,p.scope_name,p.code,p.classification,p.district,p.qualification,
	       COALESCE(p.valid_until::text,'') AS valid_until,'' AS institution_id,'' AS institution_name,
	       '' AS certificate_no,p.status,` + partyEligible + ` AS usable,
	       (p.merged_into_id IS NULL AND ($2='' OR (p.kind='unit' AND ` + exclusiveUnit + `))) AS can_edit,
	       p.version,
	       CASE WHEN $9::bool THEN 0 ELSE (SELECT count(*) FROM business_cases f WHERE ` + activeFilings + `
	            AND CASE p.kind WHEN 'unit' THEN f.organization_name=p.scope_name
	                ELSE f.assessment_org=p.scope_name END
	            AND ($2='' OR f.assessment_org=$2)) END AS system_count,
	       0::bigint AS in_review_count,COALESCE(p.owner_institution_id::text,'') AS owner_id,
	       COALESCE(p.merged_into_id::text,'') AS merged_into_id,
	       COALESCE(p.merge_id::text,'') AS merge_id,p.merged_at
	FROM directory_parties p
	WHERE p.kind=$1 AND ($3='' OR p.id::text=$3) AND $4=''
	AND ($2='' OR CASE p.kind WHEN 'institution' THEN p.scope_name=$2 ELSE ` + scopedUnit + ` END)
	UNION ALL
	SELECT '',legacy.name,legacy.name,'','','','','','','','','active',true,false,0,
	       CASE WHEN $9::bool THEN 0 ELSE legacy.system_count END,0,'','','',NULL::timestamptz
	FROM (
	    SELECT CASE $1 WHEN 'unit' THEN f.organization_name ELSE f.assessment_org END AS name,
	           count(*) AS system_count
	    FROM business_cases f WHERE ` + activeFilings + `
	    AND ($2='' OR f.assessment_org=$2) GROUP BY 1
	) legacy
	WHERE $3='' AND legacy.name<>'' AND NOT EXISTS(
	    SELECT 1 FROM directory_parties p WHERE p.kind=$1
	    AND lower(legacy.name) IN (lower(p.scope_name),lower(p.name)))
`

const peopleSource = `
	SELECT p.id::text AS id,p.name,'' AS scope_name,p.code,'' AS classification,'' AS district,
	       p.qualification,COALESCE(p.valid_until::text,'') AS valid_until,a.id::text AS institution_id,
	       a.name AS institution_name,p.certificate_no,p.status,
	       (p.status='active' AND (p.valid_until IS NULL OR p.valid_until >= ` + businessToday + `)
	           AND ` + activeInstitution + `) AS usable,
	       ($2='' OR (` + activeInstitution + `)) AS can_edit,p.version,0::bigint AS system_count,
	       CASE WHEN $9::bool THEN 0 ELSE (
	           (SELECT count(*) FROM business_cases c WHERE ` + assessmentInFlight + `
	                AND c.directory_person_id=p.id)
	           + (SELECT count(*) FROM implementation_reports i WHERE ` + implementationInFlight + `
	                AND i.directory_person_id=p.id)
	       ) END AS in_review_count,'' AS owner_id,
	       '' AS merged_into_id,'' AS merge_id,NULL::timestamptz AS merged_at
	FROM directory_people p JOIN directory_parties a ON p.institution_id=a.id
	WHERE $1='person' AND ($2='' OR a.scope_name=$2) AND ($3='' OR p.id::text=$3)
	AND ($4='' OR a.id::text=$4)
`

const readDirectory = `SELECT ` + entriesProjection + ` FROM directory_results
	WHERE ($5='all' OR status=$5) AND (NOT $9 OR usable)
	  AND ($6='' OR strpos(lower(concat_ws(' ',name,scope_name,code,
	       CASE WHEN NOT $9 THEN certificate_no END)),lower($6))>0)
	  AND ($7='' OR (name COLLATE "C",COALESCE(NULLIF(id,''),scope_name) COLLATE "C") >
	       ($7 COLLATE "C",$8 COLLATE "C"))
	ORDER BY name COLLATE "C",COALESCE(NULLIF(id,''),scope_name) COLLATE "C" LIMIT $10
`

// entryTargets 给出公共名录投影的列扫描顺序。
// 参数 item 为要写入的目录记录。
// 返回值 与公共查询列相同顺序的扫描地址。
func entryTargets(item *entry) []any {
	return []any{&item.ID, &item.Name, &item.ScopeName, &item.Code, &item.Classification, &item.District,
		&item.Qualification, &item.ValidUntil, &item.InstitutionID, &item.InstitutionName,
		&item.CertificateNo, &item.Status, &item.Usable, &item.CanEdit, &item.Version,
		&item.SystemCount, &item.InReviewCount, &item.ownerID,
		&item.MergedIntoID, &item.MergeID, &item.MergedAt}
}

// queryDirectory 拼装已限定条目类型的查询，并统一使用参数绑定。
// 参数 ctx、q 为查询上下文和数据源，input 为筛选范围，id 为可选精确条目标识。
// 返回值 已打开的结果集或数据库错误。
func queryDirectory(ctx context.Context, q reader, input listFilter, id string) (pgx.Rows, error) {
	source := partySource
	if input.kind == person {
		source = peopleSource
	}
	return q.Query(ctx, "WITH directory_results AS ("+source+") "+readDirectory,
		input.kind, input.scope, id, input.institutionID, input.status, input.query,
		input.cursorName, input.cursorKey, input.options, pageSize+1)
}

// list 查询有界的名录及其真实状态，旧备案保持只读可识别。
// 参数 ctx 为请求上下文，input 指定类型、归属和查询条件。
// 返回值 目录分页或查询错误。
func (s service) list(ctx context.Context, input listFilter) (page[entry], error) {
	rows, err := queryDirectory(ctx, s.pool, input, "")
	if err != nil {
		return page[entry]{}, err
	}
	items, err := pgx.CollectRows(rows, scanEntry)
	result := page[entry]{Items: items}
	if len(items) > pageSize {
		result.NextCursor = encodeCursor(items[pageSize-1])
		result.Items = items[:pageSize]
	}
	return result, err
}

// scanEntry 将统一目录投影的一行映射为条目。
// 参数 row 为已经取得的数据库行。
// 返回值 名录和列解码错误。
func scanEntry(row pgx.CollectableRow) (entry, error) {
	var item entry
	err := row.Scan(entryTargets(&item)...)
	return item, err
}

// readEntry 按标识和机构归属读取单条已登记名录。
// 参数 ctx、q 为查询上下文及连接，kind、scope、id 限定条目的类别和范围。
// 返回值 名录条目或不存在、数据库错误。
func readEntry(ctx context.Context, q reader, kind, scope, id string) (entry, error) {
	rows, err := queryDirectory(ctx, q, listFilter{kind: kind, scope: scope, status: "all"}, id)
	if err != nil {
		return entry{}, err
	}
	return pgx.CollectOneRow(rows, scanEntry)
}

// history 返回当前机构有权访问的、从新到旧的目录变更记录。
// 参数 ctx 为请求上下文，kind、scope、id 指定历史记录的目标，before 为空或分页位置。
// 返回值 历史记录分页或数据库错误。
func (s service) history(ctx context.Context, kind, scope, id string, before int64) (page[event], error) {
	item, err := readEntry(ctx, s.pool, kind, scope, id)
	if err != nil {
		return page[event]{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT sequence,entry_version,action,actor,changed_at,
		CASE WHEN $4<>'' AND $1='person' AND before_values->>'institutionId'<>$5 THEN NULL
		     ELSE before_values END,
		after_values FROM directory_events
		WHERE entry_kind=$1 AND entry_id=$2 AND ($3::bigint=0 OR sequence<$3)
		AND ($4='' OR $1<>'person' OR institution_id::text=$5)
		ORDER BY sequence DESC LIMIT $6`, kind, id, before, scope, item.InstitutionID, pageSize+1)
	if err != nil {
		return page[event]{}, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[event])
	result := page[event]{Items: items}
	if len(items) > pageSize {
		result.NextCursor = strconv.FormatInt(items[pageSize-1].Sequence, 10)
		result.Items = items[:pageSize]
	}
	return result, err
}
