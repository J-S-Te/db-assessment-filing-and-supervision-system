package knowledge

import (
	"context"

	"github.com/jackc/pgx/v5"
)

const disabledImportMessage = "包含已停用标签，请移除或改用启用标签。"

// checkStorage 收集重复条目及不能新关联的目录标签。
// 参数 ctx 控制检查，target 为预检连接或正式批次事务。
// 返回值 检索数据库出现的错误，具体文件问题记录在批次中。
func (batch *importBatch) checkStorage(ctx context.Context, target importQuery) error {
	if err := batch.checkExisting(ctx, target); err != nil {
		return err
	}
	return batch.checkTagCatalog(ctx, target)
}

// checkTagCatalog 根据当前目录把已停用标签的问题定位到 CSV 行。
// 参数 ctx 控制检查，target 为预检连接或正式批次事务。
// 返回值 查询停用标签时的数据库错误。
func (batch *importBatch) checkTagCatalog(ctx context.Context, target importQuery) error {
	var names []string
	var owners []int64
	for index, row := range batch.rows {
		for _, name := range row.tags {
			names = append(names, name)
			owners = append(owners, int64(index+1))
		}
	}
	if len(names) == 0 {
		return nil
	}
	rows, err := target.Query(ctx, `SELECT DISTINCT i.owner
	  FROM unnest($1::text[], $2::bigint[]) AS i(name,owner)
	  JOIN knowledge_tag_catalog c ON c.name_key=lower(i.name) COLLATE "C"
	  WHERE NOT c.active ORDER BY i.owner`, names, owners)
	if err != nil {
		return err
	}
	indexes, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	batch.excludeRows(indexes, disabledImportMessage)
	return nil
}
