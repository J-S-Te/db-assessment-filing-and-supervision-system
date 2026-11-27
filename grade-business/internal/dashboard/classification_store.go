package dashboard

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type classificationSQL struct{ list, lock, update string }

var classificationQueries = map[string]classificationSQL{
	"industry": {
		list: `
SELECT f.id::text,f.number,f.system_name,f.organization_name,COALESCE(a.code,''),COALESCE(a.version,0)
FROM business_cases f LEFT JOIN dashboard_filing_industries a ON a.filing_id=f.id
WHERE ` + eligibleIndustrySQL + `
  AND ($1='' OR strpos(lower(concat_ws(' ',f.number,f.system_name,f.organization_name)),lower($1))>0)
ORDER BY f.created_at DESC,f.id DESC LIMIT $2 OFFSET $3`,
		lock:   `SELECT id::text FROM business_cases f WHERE f.id=$1 AND ` + eligibleIndustrySQL + ` FOR UPDATE`,
		update: assignmentUpsert("dashboard_filing_industries", "filing_id"),
	},
	"issue": {
		list: `
SELECT h.id::text,hc.filing_number,COALESCE(h.title,h.description),hc.system_name,
       COALESCE(a.code,''),COALESCE(a.version,0)
FROM hazard_items h JOIN hazard_context hc ON hc.id=h.id
LEFT JOIN dashboard_hazard_issues a ON a.hazard_id=h.id
WHERE ` + eligibleIssueSQL + `
  AND ($1='' OR strpos(lower(concat_ws(' ',hc.filing_number,h.title,h.description,hc.system_name,
     hc.organization_name)),lower($1))>0)
ORDER BY h.created_at DESC,h.id DESC LIMIT $2 OFFSET $3`,
		lock:   `SELECT id::text FROM hazard_items h WHERE h.id=$1 AND ` + eligibleIssueSQL + ` FOR UPDATE`,
		update: assignmentUpsert("dashboard_hazard_issues", "hazard_id"),
	},
}

const classificationOptionsSQL = `SELECT code,label FROM dashboard_classification_options
    WHERE kind=$1 ORDER BY code COLLATE "C"`

// assignmentUpsert 构造更新语句，清除归类时保留最新版本。
// 参数 table 和 key 为服务端预定义的归类表名和业务外键列名。
// 返回值 接收标识、编码和预期版本的归类更新 SQL。
func assignmentUpsert(table, key string) string {
	return `INSERT INTO ` + table + ` (` + key + `,code,version)
SELECT $1,NULLIF($2,''),1 WHERE $3::bigint=0 OR EXISTS(SELECT 1 FROM ` + table + ` WHERE ` + key + `=$1)
ON CONFLICT (` + key + `) DO UPDATE
SET code=EXCLUDED.code,version=` + table + `.version+1,updated_at=clock_timestamp()
WHERE ` + table + `.version=$3
RETURNING COALESCE(code,''),version`
}

// readClassificationPage 读取当前页可维护的业务记录及分类字典。
// 参数 ctx 控制读取，pool 为业务库，queries 和 kind 为分类对应查询，query 与 page 为筛选。
// 返回值 包含当前归类、可选项及下一页标记的结果，或数据库错误。
func readClassificationPage(ctx context.Context, pool *pgxpool.Pool, queries classificationSQL,
	kind, query string, page int,
) (classificationPage, error) {
	var result classificationPage
	rows, err := pool.Query(ctx, queries.list, query, classificationPageSize+1, (page-1)*classificationPageSize)
	if err != nil {
		return result, err
	}
	result.Items, err = pgx.CollectRows(rows, pgx.RowToStructByPos[classificationRow])
	if err != nil {
		return result, err
	}
	result.HasMore = page < classificationPageLimit && len(result.Items) > classificationPageSize
	if len(result.Items) > classificationPageSize {
		result.Items = result.Items[:classificationPageSize]
	}
	options, err := pool.Query(ctx, classificationOptionsSQL, kind)
	if err != nil {
		return result, err
	}
	result.Options, err = pgx.CollectRows(options, pgx.RowToStructByPos[classificationOption])
	return result, err
}

// saveClassification 在锁定业务记录后校验编辑资格、字典和版本，原子保存归类。
// 参数 ctx 控制事务，pool 为业务库，queries 和 kind 为归类范围，id 与 input 为维护目标及输入。
// 返回值 已保存的编码和版本，或指明失效、冲突、字典及数据库错误。
func saveClassification(ctx context.Context, pool *pgxpool.Pool, queries classificationSQL, kind string,
	id pgtype.UUID, input classificationInput,
) (classificationValue, error) {
	var result classificationValue
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var target string
		if err := tx.QueryRow(ctx, queries.lock, id).Scan(&target); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errClassificationMissing
			}
			return err
		}
		code := *input.Code
		if code != "" {
			var valid bool
			const option = `SELECT EXISTS(SELECT 1 FROM dashboard_classification_options WHERE kind=$1 AND code=$2)`
			if err := tx.QueryRow(ctx, option, kind, code).Scan(&valid); err != nil {
				return err
			}
			if !valid {
				return errClassificationCode
			}
		}
		err := tx.QueryRow(ctx, queries.update, id, code, *input.ExpectedVersion).Scan(&result.Code, &result.Version)
		if errors.Is(err, pgx.ErrNoRows) {
			return errClassificationConflict
		}
		return err
	})
	return result, err
}
