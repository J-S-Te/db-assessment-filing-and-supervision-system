package archives

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const assetColumns = `id::text,created_at,filing_id::text,kind,identifier,related_endpoint,
 label,source,description,COALESCE(to_char(occurred_on,'YYYY-MM-DD'),''),active,version,updated_at,
 COALESCE(supplier_id::text,''),COALESCE((SELECT s.name FROM archive_suppliers s WHERE s.id=supplier_id),'')`

// readSystemAssets 读取指定备案档案的资产登记及其停用历史。
// 参数 ctx 控制数据库操作，filing 为档案标识，input 为游标和分页大小，kind 为可选登记类别。
// 返回值 资产页，或档案不存在及数据库错误。
func (s archiveService) readSystemAssets(ctx context.Context, filing pgtype.UUID, input listOptions, kind string) (page[systemAsset], error) {
	var result page[systemAsset]
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		if err := checkInventoryFiling(ctx, tx, filing, false); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+assetColumns+` FROM archive_system_assets WHERE filing_id=$1
		 AND ($2::text='' OR kind=$2) AND ($3::timestamptz IS NULL OR (created_at,id)<($3,$4::uuid))
		 ORDER BY created_at DESC,id DESC LIMIT $5`, filing, kind, input.after.stamp, input.after.id, input.limit+1)
		if err == nil {
			result, err = timedPage[systemAsset](rows, input.limit)
		}
		return err
	})
	return result, err
}

// checkInventoryFiling 校验资产的备案归属，并在写入时与注销操作互斥。
// 参数 ctx 控制数据库操作，tx 为当前事务，filing 为备案标识，write 为是否修改。
// 返回值 备案不存在、已注销禁止修改或数据库异常。
func checkInventoryFiling(ctx context.Context, tx pgx.Tx, filing pgtype.UUID, write bool) error {
	var active bool
	query := `SELECT cancelled_at IS NULL FROM business_cases f WHERE id=$1
	 AND ` + archiveFilings + ` AND discarded_at IS NULL`
	if write {
		query += " FOR SHARE"
	}
	if err := tx.QueryRow(ctx, query, filing).Scan(&active); err != nil {
		return err
	}
	if write && !active {
		return errRegistrationConflict
	}
	return nil
}

// createSystemAsset 基于备案内唯一登记标识防止并发重复写入。
// 参数 ctx 控制操作，filing 为活跃备案，input 为已验证的完整登记内容。
// 返回值 登记记录、是否新建及重复冲突或数据库错误。
func (s archiveService) createSystemAsset(ctx context.Context, filing pgtype.UUID, input assetCreate) (systemAsset, bool, error) {
	var result systemAsset
	created := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := checkInventoryFiling(ctx, tx, filing, true); err != nil {
			return err
		}
		if input.SupplierID != "" {
			if err := checkActiveSupplier(ctx, tx, input.SupplierID); err != nil {
				return err
			}
		}
		var err error
		result, err = queryArchiveRow[systemAsset](ctx, tx, `INSERT INTO archive_system_assets
		 (filing_id,kind,identifier,related_endpoint,label,source,description,occurred_on,supplier_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::date,NULLIF($9,'')::uuid)
		 ON CONFLICT DO NOTHING RETURNING `+assetColumns,
			filing, input.Kind, input.Identifier, input.RelatedEndpoint,
			input.Label, input.Source, input.Description, input.OccurredOn, input.SupplierID)
		if !errors.Is(err, pgx.ErrNoRows) {
			created = err == nil
			return err
		}
		result, err = queryArchiveRow[systemAsset](ctx, tx, `SELECT `+assetColumns+` FROM archive_system_assets
		 WHERE filing_id=$1 AND kind=$2 AND identifier=$3 AND related_endpoint=$4`,
			filing, input.Kind, input.Identifier, input.RelatedEndpoint)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!result.Active ||
			result.assetMetadata != input.assetMetadata || result.SupplierID != input.SupplierID)) {
			return errRegistrationConflict
		}
		return err
	})
	return result, created, err
}

// updateSystemAsset 修改登记说明或启停状态，并检查客户端看到的版本。
// 参数 ctx 控制操作，filing 和 id 指定档案内条目，expected 为旧版本，input 为登记属性。
// 返回值 当前登记资料，或校验、并发和数据库错误。
func (s archiveService) updateSystemAsset(ctx context.Context, filing, id pgtype.UUID, expected int64, input assetUpdate) (systemAsset, error) {
	var result systemAsset
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := checkInventoryFiling(ctx, tx, filing, true); err != nil {
			return err
		}
		var err error
		result, err = queryArchiveRow[systemAsset](ctx, tx, `SELECT `+assetColumns+`
		 FROM archive_system_assets WHERE filing_id=$1 AND id=$2 FOR UPDATE`, filing, id)
		if err != nil {
			return err
		}
		if err := validateAssetMetadata(&input.assetMetadata, result.Kind); err != nil {
			return err
		}
		if result.Version != expected {
			return errRegistrationConflict
		}
		return writeSystemAssetUpdate(ctx, tx, id, input, &result)
	})
	return result, supplierDBError(err)
}

// writeSystemAssetUpdate 写入可编辑凭据、记录状态和首次明确的主档关联。
// 参数 ctx 和 tx 为登记事务，id 为已锁定条目，input 为目标资料，result 接收更新结果。
// 返回值 无变更或更新成功返回空错误，否则返回关联、状态或数据库错误。
func writeSystemAssetUpdate(ctx context.Context, tx pgx.Tx, id pgtype.UUID, input assetUpdate, result *systemAsset) error {
	supplierID, err := resolveSupplierAssociation(*result, input.SupplierID)
	if err != nil {
		return err
	}
	if result.assetMetadata == input.assetMetadata && result.Active == *input.Active && result.SupplierID == supplierID {
		return nil
	}
	if supplierID != "" && (result.SupplierID == "" || (!result.Active && *input.Active)) {
		if err := checkActiveSupplier(ctx, tx, supplierID); err != nil {
			return err
		}
	}
	*result, err = queryArchiveRow[systemAsset](ctx, tx, `UPDATE archive_system_assets
	 SET label=$2,source=$3,description=$4,occurred_on=NULLIF($5,'')::date,active=$6,
	     supplier_id=NULLIF($7,'')::uuid,version=version+1,updated_at=clock_timestamp()
	 WHERE id=$1 RETURNING `+assetColumns, id, input.Label, input.Source, input.Description,
		input.OccurredOn, *input.Active, supplierID)
	return err
}

// resolveSupplierAssociation 允许旧文字登记补充主档标识，并维持已有凭据的主体关联。
// 参数 current 为已有备案登记，requested 为本次明确提供的供应商标识。
// 返回值 当前或首次提供的有效标识，或无效和改绑冲突错误。
func resolveSupplierAssociation(current systemAsset, requested *string) (string, error) {
	if requested == nil {
		return current.SupplierID, nil
	}
	id, err := parseArchiveID(*requested)
	if err != nil || current.Kind != "supplier" {
		return "", errInvalid
	}
	if current.SupplierID != "" && current.SupplierID != id.String() {
		return "", errRegistrationConflict
	}
	return id.String(), nil
}

// queryArchiveRow 从有序公开列读取一条登记对象。
// 参数 ctx 控制数据库操作，reader 为事务或连接池，query 和 args 为查询模板和业务参数。
// 返回值 一条登记或查询、行数和列校验错误。
func queryArchiveRow[T any](ctx context.Context, reader archiveReader, query string, args ...any) (T, error) {
	rows, err := reader.Query(ctx, query, args...)
	if err != nil {
		var zero T
		return zero, err
	}
	return pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[T])
}
