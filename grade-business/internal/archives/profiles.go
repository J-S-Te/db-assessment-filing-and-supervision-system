package archives

import (
	"context"

	"github.com/jackc/pgx/v5"
)

const reportSort = `COALESCE(r.submitted_at,r.created_at)`
const planSort = `COALESCE(p.submitted_at,p.created_at)`

const reportsSQL = `SELECT r.id::text,` + reportSort + `,f.id::text,f.system_name,
 r.number,r.assessment_org,r.risk,r.status,r.submitted_at
 FROM business_cases f ` + relatedReportJoin + `
 WHERE ` + filingScope + ` AND ($3::timestamptz IS NULL OR (` + reportSort + `,r.id)<($3,$4::uuid))
 ORDER BY ` + reportSort + ` DESC,r.id DESC LIMIT $5`

const remediationsSQL = `SELECT p.id::text,` + planSort + `,r.id::text,f.system_name,
 r.number,to_char(p.deadline,'YYYY-MM-DD'),p.status,p.submitted_at
 FROM business_cases f ` + relatedReportJoin + ` ` + relatedPlanJoin + `
 WHERE ` + filingScope + ` AND ($3::timestamptz IS NULL OR (` + planSort + `,p.id)<($3,$4::uuid))
 ORDER BY ` + planSort + ` DESC,p.id DESC LIMIT $5`

const hazardsSQL = `SELECT h.id::text,h.created_at,COALESCE(h.plan_id::text,''),f.system_name,
 h.severity,h.description,h.status,to_char(h.deadline,'YYYY-MM-DD'),h.closed_at
 FROM business_cases f JOIN (` + relatedHazards + `) ON ` + archiveHazardScope + `
 WHERE ` + filingScope + ` AND ($3::timestamptz IS NULL OR (h.created_at,h.id)<($3,$4::uuid))
 ORDER BY h.created_at DESC,h.id DESC LIMIT $5`

const unitAssetRecordsSQL = `SELECT ` + assetColumns + `,system_name FROM (
 SELECT a.*,f.system_name FROM archive_system_assets a
 JOIN business_cases f ON f.id=a.filing_id
 WHERE ` + filingScope + ` AND a.kind IN ('supplier','security_event')
   AND ($3::timestamptz IS NULL OR (a.created_at,a.id)<($3,$4::uuid))
 ORDER BY a.created_at DESC,a.id DESC LIMIT $5
) records ORDER BY created_at DESC,id DESC`

// readUnit 读取一次快照中同名备案对应的单位信息和完整可分页历史。
// 参数 ctx 控制读取，organization 为备案使用的精确单位名，input 为各分区游标。
// 返回值 单位档案快照，或档案不存在和数据库错误。
func (s archiveService) readUnit(ctx context.Context, organization string, input detailOptions) (unitProfile, error) {
	var result unitProfile
	scope := archiveScope{organization: organization}
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	err := pgx.BeginTxFunc(ctx, s.pool, options, func(tx pgx.Tx) error {
		units, err := readUnits(ctx, tx, organization, listOptions{limit: 1})
		if err != nil {
			return err
		}
		if len(units.Items) == 0 {
			return pgx.ErrNoRows
		}
		result.Unit = units.Items[0]
		result.Systems, err = readSystems(ctx, tx, scope, listOptions{limit: defaultPageSize, after: input.systems})
		if err != nil {
			return err
		}
		result.AssetRecords, err = queryRelations[unitAssetRow](ctx, tx, unitAssetRecordsSQL, scope, input.assetRecords)
		if err != nil {
			return err
		}
		result.relatedRecords, err = readRelations(ctx, tx, scope, input)
		return err
	})
	return result, err
}

// readSystem 按备案标识从一次快照读取系统信息和对应业务记录。
// 参数 ctx 控制读取，scope 限定唯一备案，input 为各分区游标。
// 返回值 系统档案快照，或档案不存在和数据库错误。
func (s archiveService) readSystem(ctx context.Context, scope archiveScope, input detailOptions) (systemProfile, error) {
	var result systemProfile
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	err := pgx.BeginTxFunc(ctx, s.pool, options, func(tx pgx.Tx) error {
		systems, err := readSystems(ctx, tx, scope, listOptions{limit: 1})
		if err != nil {
			return err
		}
		if len(systems.Items) == 0 {
			return pgx.ErrNoRows
		}
		result.System = systems.Items[0]
		result.relatedRecords, err = readRelations(ctx, tx, scope, input)
		return err
	})
	return result, err
}

// readRelations 依次读取同一档案范围的测评历史、整改规划和隐患台账。
// 参数 ctx 控制查询，tx 持有档案快照，scope 为备案范围，input 为各分区游标。
// 返回值 可独立分页的关联业务记录，或数据库错误。
func readRelations(ctx context.Context, tx pgx.Tx, scope archiveScope, input detailOptions) (relatedRecords, error) {
	var result relatedRecords
	var err error
	result.Reports, err = queryRelations[reportRow](ctx, tx, reportsSQL, scope, input.reports)
	if err != nil {
		return result, err
	}
	result.Remediations, err = queryRelations[remediationRow](ctx, tx, remediationsSQL, scope, input.remediations)
	if err != nil {
		return result, err
	}
	result.Hazards, err = queryRelations[hazardRow](ctx, tx, hazardsSQL, scope, input.hazards)
	return result, err
}

// queryRelations 查询某类档案关联记录的一页。
// 参数 ctx 控制查询，tx 持有档案快照，query 为固定关系查询，scope 和 after 限定范围及位置。
// 返回值 有序分页结果或数据库错误。
func queryRelations[T interface{ cursor() string }](ctx context.Context, tx pgx.Tx, query string,
	scope archiveScope, after timedPosition) (page[T], error) {
	rows, err := tx.Query(ctx, query, scope.organization, scope.filingID, after.stamp, after.id, defaultPageSize+1)
	if err != nil {
		return page[T]{}, err
	}
	return timedPage[T](rows, defaultPageSize)
}
