package archives

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type coverageCreate struct {
	UnitID     string `json:"unitId"`
	FilingID   string `json:"filingId"`
	SystemName string `json:"systemName"`
	Reference  string `json:"reference"`
	Source     string `json:"source"`
}

type coverageUpdate struct {
	FilingID string `json:"filingId"`
	Active   *bool  `json:"active"`
}

type coverageTarget struct {
	rowOrder
	UnitID       string    `json:"unitId"`
	FilingID     *string   `json:"filingId,omitempty"`
	SystemName   string    `json:"systemName"`
	Reference    string    `json:"reference"`
	Source       string    `json:"source"`
	Active       bool      `json:"active"`
	Version      int64     `json:"version"`
	UpdatedAt    time.Time `json:"updatedAt"`
	UnitName     string    `json:"unitName"`
	District     string    `json:"district"`
	FilingNumber string    `json:"filingNumber"`
	Covered      bool      `json:"covered"`
}

const coverageColumns = `id::text,created_at,unit_id::text,filing_id::text,system_name,reference,source,
 active,version,updated_at,unit_name,district,filing_number,covered`
const coverageRow = `SELECT ` + coverageColumns + ` FROM archive_coverage_status`

// validateCoverageCreate 校验覆盖对象的稳定登记编号和来源凭据。
// 参数 input 为当前清册对象请求，由校验过程清理多余空白。
// 返回值 合法请求返回空错误，字段不合规则返回参数错误。
func validateCoverageCreate(input *coverageCreate) error {
	input.SystemName = strings.TrimSpace(input.SystemName)
	input.Reference = strings.TrimSpace(input.Reference)
	input.Source = strings.TrimSpace(input.Source)
	if !registrationText(input.SystemName, 200, true) || !registrationText(input.Reference, 120, true) ||
		!registrationText(input.Source, 300, true) {
		return errInvalid
	}
	return nil
}

// readCoverageTargets 读取备案覆盖范围的有据登记对象及实时匹配状态。
// 参数 ctx 控制数据库读取，input 为分页和检索位置。
// 返回值 覆盖登记页，或查询错误。
func (s archiveService) readCoverageTargets(ctx context.Context, input listOptions) (page[coverageTarget], error) {
	rows, err := s.pool.Query(ctx, coverageRow+` WHERE
	 ($1::timestamptz IS NULL OR (created_at,id)<($1,$2::uuid))
	 ORDER BY created_at DESC,id DESC LIMIT $3`, input.after.stamp, input.after.id, input.limit+1)
	if err != nil {
		return page[coverageTarget]{}, err
	}
	return timedPage[coverageTarget](rows, input.limit)
}

// createCoverageTarget 以全局稳定登记编号新增清册范围并允许没有备案的对象入册。
// 参数 ctx 控制操作，unit 和 filing 为已解析的归属标识，input 为来源及系统信息。
// 返回值 当前登记、是否新建，或无法匹配和数据库错误。
func (s archiveService) createCoverageTarget(ctx context.Context, unit, filing pgtype.UUID, input coverageCreate) (coverageTarget, bool, error) {
	var result coverageTarget
	created := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := checkCoverageAssociation(ctx, tx, unit, filing, input.SystemName); err != nil {
			return err
		}
		var id pgtype.UUID
		err := tx.QueryRow(ctx, `INSERT INTO archive_coverage_targets(unit_id,filing_id,system_name,reference,source)
		 VALUES ($1,$2,$3,$4,$5) ON CONFLICT (lower(reference)) DO NOTHING RETURNING id`,
			unit, filing, input.SystemName, input.Reference, input.Source).Scan(&id)
		return resolveCoverageCreate(ctx, tx, id, err, unit, filing, input, &result, &created)
	})
	return result, created, coverageDBError(err)
}

// resolveCoverageCreate 处理首次清册登记与同一登记编号的重试。
// 参数 ctx 和 tx 为事务上下文，id 和 insertErr 为写入结果，unit、filing、input 为业务归属及凭据。
// 参数 result 和 created 接收最终记录及是否新建。
// 返回值 不同数据占用编号时返回冲突，其余返回数据库处理结果。
func resolveCoverageCreate(ctx context.Context, tx pgx.Tx, id pgtype.UUID, insertErr error,
	unit, filing pgtype.UUID, input coverageCreate, result *coverageTarget, created *bool) error {
	var err error
	switch {
	case insertErr == nil:
		*result, err = queryArchiveRow[coverageTarget](ctx, tx, coverageRow+" WHERE id=$1", id)
		*created = err == nil
	case errors.Is(insertErr, pgx.ErrNoRows):
		*result, err = queryArchiveRow[coverageTarget](ctx, tx, coverageRow+" WHERE lower(reference)=lower($1)", input.Reference)
		if err == nil && (!result.Active || result.UnitID != unit.String() || !sameFiling(result.FilingID, filing) ||
			result.SystemName != input.SystemName || result.Source != input.Source) {
			return errRegistrationConflict
		}
	default:
		return insertErr
	}
	return err
}

// updateCoverageTarget 调整已有对象的备案关联或启停状态并留存递增版本。
// 参数 ctx 控制操作，id 为清册对象，filing 为可空目标备案，expected 为旧版本，active 为目标状态。
// 返回值 最新范围对象，或归属、版本和数据库错误。
func (s archiveService) updateCoverageTarget(ctx context.Context, id, filing pgtype.UUID, expected int64, active bool) (coverageTarget, error) {
	var result coverageTarget
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		owner, err := lockCoverageOwner(ctx, tx, id)
		if err != nil {
			return err
		}
		var unit pgtype.UUID
		var name string
		var version int64
		err = tx.QueryRow(ctx, `SELECT unit_id,system_name,version FROM archive_coverage_targets WHERE id=$1 FOR UPDATE`,
			id).Scan(&unit, &name, &version)
		if err != nil {
			return err
		}
		if version != expected || unit != owner {
			return errRegistrationConflict
		}
		return writeCoverageUpdate(ctx, tx, id, unit, filing, name, active, &result)
	})
	return result, coverageDBError(err)
}

// lockCoverageOwner 先锁清册所属名录，再锁清册行，与单位归并保持一致的加锁顺序。
// 参数 ctx 和 tx 为编辑事务，id 为清册对象。
// 返回值 编辑时所见归属，或不存在及数据库错误。
func lockCoverageOwner(ctx context.Context, tx pgx.Tx, id pgtype.UUID) (pgtype.UUID, error) {
	var owner pgtype.UUID
	err := tx.QueryRow(ctx, `SELECT u.id FROM archive_coverage_targets t
		JOIN directory_parties u ON u.id=t.unit_id WHERE t.id=$1 FOR SHARE OF u`, id).Scan(&owner)
	return owner, err
}

// writeCoverageUpdate 在旧版本已锁定后执行清册变更并返回实时覆盖状态。
// 参数 ctx 和 tx 为事务上下文，id、unit、filing、name、active 表示目标记录、归属及要求状态，result 接收结果。
// 返回值 无变更时成功，非法归属或数据库操作失败时返回错误。
func writeCoverageUpdate(ctx context.Context, tx pgx.Tx, id, unit, filing pgtype.UUID,
	name string, active bool, result *coverageTarget) error {
	var err error
	*result, err = queryArchiveRow[coverageTarget](ctx, tx, coverageRow+" WHERE id=$1", id)
	if err != nil || (result.Active == active && sameFiling(result.FilingID, filing)) {
		return err
	}
	if active || !sameFiling(result.FilingID, filing) {
		if err := checkCoverageAssociation(ctx, tx, unit, filing, name); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE archive_coverage_targets SET filing_id=$2,active=$3,
	 version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id, filing, active)
	if err != nil {
		return err
	}
	*result, err = queryArchiveRow[coverageTarget](ctx, tx, coverageRow+" WHERE id=$1", id)
	return err
}

// checkCoverageAssociation 核实当前在用单位和可选备案的系统名称及所属单位。
// 参数 ctx 控制操作，tx 为当前事务，unit 和 filing 为关联标识，systemName 为清册系统名称。
// 返回值 单位不存在、备案归属不符、状态冲突或数据库错误。
func checkCoverageAssociation(ctx context.Context, tx pgx.Tx, unit, filing pgtype.UUID, systemName string) error {
	var name, scope string
	err := tx.QueryRow(ctx, `SELECT name,scope_name FROM directory_parties
	  WHERE id=$1 AND kind='unit' AND status='active' AND merged_into_id IS NULL FOR SHARE`, unit).Scan(&name, &scope)
	if err != nil || !filing.Valid {
		return err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT status='approved' AND cancelled_at IS NULL AND discarded_at IS NULL
	  AND system_name=$3 AND COALESCE(directory_unit_id=$2,organization_name IN ($4,$5))
	 FROM business_cases WHERE id=$1 AND kind='filing' FOR SHARE`, filing, unit, systemName, name, scope).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !valid) {
		return errRegistrationConflict
	}
	return err
}

// sameFiling 比较数据库登记中可空备案和请求指向的备案标识。
// 参数 current 为当前可读标识或空值，requested 为目标可空 UUID。
// 返回值 两者匹配时为真。
func sameFiling(current *string, requested pgtype.UUID) bool {
	return (current == nil && !requested.Valid) || (current != nil && *current == requested.String())
}

// coverageDBError 将重复占用同一备案或登记编号的检查映射为可处理冲突。
// 参数 err 为清册写入结果。
// 返回值 清册领域冲突或原数据库错误。
func coverageDBError(err error) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" &&
		(pgError.ConstraintName == "archive_coverage_filing" || pgError.ConstraintName == "archive_coverage_reference") {
		return errRegistrationConflict
	}
	return err
}
