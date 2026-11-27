package tagcatalog

import (
	"context"
	"encoding/base64"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const listing = `
WITH selected AS (
    SELECT id,name,color,active,version,name_key FROM knowledge_tag_catalog
    WHERE ($1::text='' OR strpos(name_key,lower($1))>0)
      AND ($2::text='' OR (active=($2='active')))
      AND ($3::text='' OR name_key>$3)
    ORDER BY name_key LIMIT $4
), usage AS (
    SELECT s.name_key,count(DISTINCT e.id) AS amount
    FROM knowledge_entries e CROSS JOIN LATERAL unnest(e.tags) AS t(name)
    JOIN selected s ON s.name_key=lower(t.name) COLLATE "C" GROUP BY s.name_key
)
SELECT s.id::text,s.name,s.color,s.active,COALESCE(u.amount,0),s.version,s.name_key
FROM selected s LEFT JOIN usage u USING (name_key) ORDER BY s.name_key`

// readPage 使用有界名称游标读取目录，并从当前条目计算每个标签的引用数。
// 参数 ctx 控制读取，input 为已校验的分页和检索条件。
// 返回值 一页使用统计和后续分页位置，或数据库错误。
func (s service) readPage(ctx context.Context, input listOptions) (catalogPage, error) {
	rows, err := s.pool.Query(ctx, listing, input.keyword, input.status, input.after, input.limit+1)
	if err != nil {
		return catalogPage{}, err
	}
	items, err := pgx.CollectRows(rows, scanCatalogEntry)
	if err != nil {
		return catalogPage{}, err
	}
	result := catalogPage{Items: items}
	if len(items) > input.limit {
		result.Items = items[:input.limit]
		result.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(items[input.limit-1].key))
	}
	return result, nil
}

// scanCatalogEntry 转换已有目录数据及当前快照的使用计数。
// 参数 row 为已选目录及计数的数据库行。
// 返回值 可公开的目录记录，或扫描错误。
func scanCatalogEntry(row pgx.CollectableRow) (catalogEntry, error) {
	var item catalogEntry
	err := row.Scan(&item.ID, &item.Name, &item.Color, &item.Active, &item.UsageCount, &item.Version, &item.key)
	return item, err
}

// edit 以明确版本保存标签配置并返回引用数。
// 参数 ctx 控制事务，id 为标签标识，input 为新的颜色、状态及版本。
// 返回值 更新后目录行，或数据库与写入冲突错误。
func (s service) edit(ctx context.Context, id pgtype.UUID, input patchInput) (catalogEntry, error) {
	var result catalogEntry
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `UPDATE knowledge_tag_catalog SET color=$3,active=$4,version=version+1
		  WHERE id=$1 AND version=$2 RETURNING id::text,name,color,active,version,name_key`,
			id, input.ExpectedVersion, input.Color, *input.Active).
			Scan(&result.ID, &result.Name, &result.Color, &result.Active, &result.Version, &result.key)
		if errors.Is(err, pgx.ErrNoRows) {
			return catalogEditConflict(ctx, tx, id)
		}
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM knowledge_entries e WHERE EXISTS (
		  SELECT 1 FROM unnest(e.tags) AS t(name) WHERE lower(t.name) COLLATE "C"=$1)`, result.key).
			Scan(&result.UsageCount)
	})
	return result, err
}

// catalogEditConflict 区分过期版本与无对应标签的修改。
// 参数 ctx 控制查询，tx 为目录写入事务，id 为请求标签标识。
// 返回值 目录冲突、记录不存在或数据库查询错误。
func catalogEditConflict(ctx context.Context, tx pgx.Tx, id pgtype.UUID) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_tag_catalog WHERE id=$1)`, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return errVersion
	}
	return pgx.ErrNoRows
}
