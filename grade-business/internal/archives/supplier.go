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

type supplierFields struct {
	Name         string `json:"name"`
	ServiceType  string `json:"serviceType"`
	UnitID       string `json:"unitId,omitempty"`
	StartedOn    string `json:"startedOn,omitempty"`
	ContactName  string `json:"contactName,omitempty"`
	ContactPhone string `json:"contactPhone,omitempty"`
	Source       string `json:"source"`
}

type supplierInput struct {
	supplierFields
	Active *bool `json:"active"`
}

type supplierRow struct {
	rowOrder
	supplierFields
	Active    bool      `json:"active"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updatedAt"`
	UnitName  string    `json:"unitName"`
}

type supplierSystem struct {
	systemAsset
	SystemName       string `json:"systemName"`
	OrganizationName string `json:"organizationName"`
	FilingNumber     string `json:"filingNumber"`
	FilingStatus     string `json:"filingStatus"`
}

type supplierProfile struct {
	Supplier supplierRow          `json:"supplier"`
	Systems  page[supplierSystem] `json:"systems"`
}

const supplierColumns = `id::text,created_at,name,service_type,COALESCE(unit_id::text,''),
 COALESCE(to_char(started_on,'YYYY-MM-DD'),''),contact_name,contact_phone,source,active,version,updated_at,
 COALESCE((SELECT u.name FROM directory_parties u WHERE u.id=archive_suppliers.unit_id),'')`

const supplierSelect = `SELECT ` + supplierColumns + ` FROM archive_suppliers`

// validateSupplierFields 清理主档文字并验证凭据、可选合作日期和服务单位。
// 参数 input 为可编辑的供应商主档资料。
// 返回值 资料有效时返回空错误，否则返回参数错误。
func validateSupplierFields(input *supplierFields) error {
	input.Name = strings.TrimSpace(input.Name)
	input.ServiceType = strings.TrimSpace(input.ServiceType)
	input.Source = strings.TrimSpace(input.Source)
	input.ContactName = strings.TrimSpace(input.ContactName)
	input.ContactPhone = strings.TrimSpace(input.ContactPhone)
	if !registrationText(input.Name, 200, true) || !registrationText(input.ServiceType, 120, true) ||
		!registrationText(input.Source, 300, true) || !registrationText(input.ContactName, 120, false) ||
		!registrationText(input.ContactPhone, 80, false) {
		return errInvalid
	}
	unit, err := optionalArchiveID(input.UnitID)
	if err != nil {
		return err
	}
	if unit.Valid {
		input.UnitID = unit.String()
	}
	if input.StartedOn != "" {
		date, err := time.Parse(time.DateOnly, input.StartedOn)
		if err != nil || date.Year() == 0 {
			return errInvalid
		}
	}
	return nil
}

// readSuppliers 按名称或服务类型检索供应商主档及其合作状态。
// 参数 ctx 控制数据库访问，input 为列表查询与分页位置，active 为可空状态条件。
// 返回值 有界主档列表及查询错误。
func (s archiveService) readSuppliers(ctx context.Context, input listOptions, active *bool) (page[supplierRow], error) {
	rows, err := s.pool.Query(ctx, supplierSelect+` WHERE ($1::text='' OR strpos(lower(name),lower($1))>0
	 OR strpos(lower(service_type),lower($1))>0) AND ($2::boolean IS NULL OR active=$2)
	 AND ($3::timestamptz IS NULL OR (created_at,id)<($3,$4::uuid))
	 ORDER BY created_at DESC,id DESC LIMIT $5`, input.keyword, active, input.after.stamp, input.after.id, input.limit+1)
	if err != nil {
		return page[supplierRow]{}, err
	}
	return timedPage[supplierRow](rows, input.limit)
}

// readSupplierProfile 同时读取主档和具备登记来源的备案系统关联。
// 参数 ctx 控制快照读取，id 为主档标识，input 为关联分页位置。
// 返回值 主档画像，或未找到和数据库错误。
func (s archiveService) readSupplierProfile(ctx context.Context, id pgtype.UUID, input listOptions) (supplierProfile, error) {
	var result supplierProfile
	options := pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead}
	err := pgx.BeginTxFunc(ctx, s.pool, options, func(tx pgx.Tx) error {
		var err error
		result.Supplier, err = queryArchiveRow[supplierRow](ctx, tx, supplierSelect+" WHERE id=$1", id)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+assetColumns+`,system_name,organization_name,number,filing_status FROM (
		 SELECT a.*,f.system_name,f.organization_name,f.number,
		  CASE WHEN f.cancelled_at IS NULL THEN 'active' ELSE 'cancelled' END AS filing_status
		 FROM archive_system_assets a JOIN business_cases f ON f.id=a.filing_id
		 WHERE a.supplier_id=$1 AND `+archiveFilings+` AND f.discarded_at IS NULL
		  AND ($2::timestamptz IS NULL OR (a.created_at,a.id)<($2,$3::uuid))
		 ORDER BY a.created_at DESC,a.id DESC LIMIT $4
		) systems ORDER BY created_at DESC,id DESC`, id, input.after.stamp, input.after.id, input.limit+1)
		if err == nil {
			result.Systems, err = timedPage[supplierSystem](rows, input.limit)
		}
		return err
	})
	return result, err
}

// createSupplierMaster 依照已核实的主档资料和名称唯一约束登记供应商。
// 参数 ctx 控制写入，input 为已校验的资料及状态。
// 返回值 主档、是否新建及重复冲突或数据库错误。
func (s archiveService) createSupplierMaster(ctx context.Context, input supplierInput) (supplierRow, bool, error) {
	var result supplierRow
	created := false
	active := input.Active == nil || *input.Active
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := checkSupplierUnit(ctx, tx, input.UnitID); err != nil {
			return err
		}
		var err error
		result, err = queryArchiveRow[supplierRow](ctx, tx, `INSERT INTO archive_suppliers
		 (name,service_type,unit_id,started_on,contact_name,contact_phone,source,active)
		 VALUES ($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::date,$5,$6,$7,$8)
		 ON CONFLICT (lower(name)) DO NOTHING RETURNING `+supplierColumns,
			input.Name, input.ServiceType, input.UnitID, input.StartedOn, input.ContactName,
			input.ContactPhone, input.Source, active)
		if !errors.Is(err, pgx.ErrNoRows) {
			created = err == nil
			return err
		}
		result, err = queryArchiveRow[supplierRow](ctx, tx, supplierSelect+" WHERE lower(name)=lower($1)", input.Name)
		if err == nil && (result.supplierFields != input.supplierFields || result.Active != active) {
			return errRegistrationConflict
		}
		return err
	})
	return result, created, err
}

// updateSupplierMaster 保存期望版本下的主档资料和合作状态，拒绝覆盖并发编辑。
// 参数 ctx 控制操作，id 指定主档，expected 为旧版本，input 为目标资料及状态。
// 返回值 确认后的主档，或状态、版本和数据库错误。
func (s archiveService) updateSupplierMaster(ctx context.Context, id pgtype.UUID, expected int64, input supplierInput) (supplierRow, error) {
	var result supplierRow
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		result, err = queryArchiveRow[supplierRow](ctx, tx, supplierSelect+" WHERE id=$1 FOR UPDATE", id)
		if err != nil {
			return err
		}
		if result.Version != expected {
			return errRegistrationConflict
		}
		if result.supplierFields == input.supplierFields && result.Active == *input.Active {
			return nil
		}
		if result.UnitID != input.UnitID {
			if err := checkSupplierUnit(ctx, tx, input.UnitID); err != nil {
				return err
			}
		}
		result, err = queryArchiveRow[supplierRow](ctx, tx, `UPDATE archive_suppliers SET
		 name=$2,service_type=$3,unit_id=NULLIF($4,'')::uuid,started_on=NULLIF($5,'')::date,
		 contact_name=$6,contact_phone=$7,source=$8,active=$9,version=version+1,updated_at=clock_timestamp()
		 WHERE id=$1 RETURNING `+supplierColumns, id, input.Name, input.ServiceType, input.UnitID,
			input.StartedOn, input.ContactName, input.ContactPhone, input.Source, *input.Active)
		return err
	})
	return result, supplierDBError(err)
}

// checkSupplierUnit 验证主档服务单位可用并与停用或合并互斥。
// 参数 ctx 和 tx 为当前事务上下文，unitID 为可选的名录单位标识。
// 返回值 空单位或可用单位返回空错误，不存在时返回冲突。
func checkSupplierUnit(ctx context.Context, tx pgx.Tx, unitID string) error {
	if unitID == "" {
		return nil
	}
	var id pgtype.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM directory_parties WHERE id=$1
	 AND kind='unit' AND status='active' AND merged_into_id IS NULL FOR SHARE`, unitID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return errRegistrationConflict
	}
	return err
}

// checkActiveSupplier 在建立或重新启用关联时锁定主档并验证合作状态。
// 参数 ctx 和 tx 为当前事务上下文，id 为供应商主档标识。
// 返回值 可关联主档返回空错误，失效主档返回冲突，缺失或读取失败时返回对应错误。
func checkActiveSupplier(ctx context.Context, tx pgx.Tx, id string) error {
	var active bool
	err := tx.QueryRow(ctx, "SELECT active FROM archive_suppliers WHERE id=$1 FOR SHARE", id).Scan(&active)
	if err == nil && !active {
		return errRegistrationConflict
	}
	return err
}

// supplierDBError 将主档或备案内供应商的重复错误映射为业务冲突。
// 参数 err 为数据库写入结果。
// 返回值 可处理的占用冲突或原始错误。
func supplierDBError(err error) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" &&
		(pgError.ConstraintName == "archive_suppliers_name" || pgError.ConstraintName == "archive_system_assets_supplier_unique") {
		return errRegistrationConflict
	}
	return err
}
