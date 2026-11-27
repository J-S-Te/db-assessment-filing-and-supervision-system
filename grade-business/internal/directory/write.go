package directory

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"grade-business/internal/geoarea"
)

// create 完成主档登记、机构关联和首条历史的同一事务。
// 参数 ctx 为写入上下文，kind、scope 指定目录和机构范围，input 为登记字段。
// 返回值 新目录条目或校验、并发和数据库错误。
func (s service) create(ctx context.Context, kind, scope string, input change) (entry, error) {
	if input.ExpectedVersion != 0 || !validateChangeKind(kind, scope, input) {
		return entry{}, errInvalid
	}
	item := mergeChange(entry{Status: active}, input)
	var result entry
	err := s.write(ctx, func(tx pgx.Tx) error {
		var err error
		item, err = prepareCreate(ctx, tx, kind, scope, item)
		if err != nil {
			return err
		}
		id, err := insertEntry(ctx, tx, kind, item)
		if err != nil {
			return err
		}
		if item.ownerID != "" {
			if _, err := linkUnit(ctx, tx, id, item.ownerID); err != nil {
				return err
			}
		}
		result, err = readEntry(ctx, tx, kind, scope, id)
		if err != nil {
			return err
		}
		return recordEvent(ctx, tx, kind, scope, "create", nil, result)
	})
	return result, err
}

// prepareCreate 验证归属资质，确定新单位或机构不会改写旧业务范围。
// 参数 ctx、tx 为登记事务，kind、scope 为目标目录和机构，item 为待保存字段。
// 返回值 含实际归属和范围的候选条目或业务错误。
func prepareCreate(ctx context.Context, tx pgx.Tx, kind, scope string, item entry) (entry, error) {
	if kind == person || scope != "" {
		if scope == "" && !validID(item.InstitutionID) {
			return entry{}, errInvalid
		}
		id, err := eligibleInstitution(ctx, tx, scope, item.InstitutionID)
		if err != nil {
			return entry{}, err
		}
		if kind == person {
			item.InstitutionID = id
		} else {
			item.ownerID = id
		}
	}
	if !validEntry(kind, item) {
		return entry{}, errInvalid
	}
	if kind == person {
		return item, nil
	}
	if err := checkPartyName(ctx, tx, kind, "", item.Name, ""); err != nil {
		return entry{}, err
	}
	scopeName, err := stableScopeName(ctx, tx, kind, item.Name)
	item.ScopeName = scopeName
	return item, err
}

// stableScopeName 在登记旧名称时延续实际备案范围，减少仅因大小写产生的分裂档案。
// 参数 ctx、tx 为登记事务，kind 和 name 为类型及当前登记名称。
// 返回值 已知历史范围或首次登记名称，以及查询错误。
func stableScopeName(ctx context.Context, tx pgx.Tx, kind, name string) (string, error) {
	var result string
	err := tx.QueryRow(ctx, `SELECT COALESCE((
		SELECT names.name FROM (
		    SELECT CASE $1 WHEN 'unit' THEN organization_name ELSE assessment_org END AS name
		    FROM business_cases f WHERE f.kind='filing'
		) names WHERE lower(names.name)=lower($2)
		ORDER BY (names.name=$2) DESC,names.name COLLATE "C" LIMIT 1
	),$2)`, kind, name).Scan(&result)
	return result, err
}

// insertEntry 将校验后的名录候选写入对应的主表。
// 参数 ctx、tx 为登记事务，kind、item 为类型和目录字段。
// 返回值 新记录标识或数据库错误。
func insertEntry(ctx context.Context, tx pgx.Tx, kind string, item entry) (string, error) {
	var id string
	var row pgx.Row
	if kind == person {
		row = tx.QueryRow(ctx, `INSERT INTO directory_people(
			institution_id,name,code,certificate_no,qualification,valid_until,status)
			VALUES ($1,$2,$3,$4,$5,NULLIF($6,'')::date,$7) RETURNING id::text`,
			item.InstitutionID, item.Name, item.Code, item.CertificateNo, item.Qualification,
			item.ValidUntil, item.Status)
	} else {
		row = tx.QueryRow(ctx, `INSERT INTO directory_parties(
			kind,scope_name,name,code,classification,district,qualification,valid_until,status,owner_institution_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::date,$9,NULLIF($10,'')::uuid)
			RETURNING id::text`, kind, item.ScopeName, item.Name, item.Code, item.Classification,
			item.District, item.Qualification, item.ValidUntil, item.Status, item.ownerID)
	}
	err := row.Scan(&id)
	return id, err
}

// patch 应用版本条件下的局部修改，原始范围和既有业务快照继续保留。
// 参数 ctx 为修改上下文，kind、scope、id 为条目范围，input 为预期版本和修改字段。
// 返回值 更新条目，或权限、冲突和数据库错误。
func (s service) patch(ctx context.Context, kind, scope, id string, input change) (entry, error) {
	if input.ExpectedVersion < 1 || input == (change{ExpectedVersion: input.ExpectedVersion}) ||
		!validateChangeKind(kind, scope, input) {
		return entry{}, errInvalid
	}
	var result entry
	err := s.write(ctx, func(tx pgx.Tx) error {
		before, err := editableEntry(ctx, tx, kind, scope, id, input.ExpectedVersion)
		if err != nil {
			return err
		}
		candidate := mergeChange(before, input)
		if candidate == before {
			result = before
			return nil
		}
		result, err = applyChange(ctx, tx, kind, scope, before, candidate)
		if err != nil {
			return err
		}
		return recordEvent(ctx, tx, kind, scope, eventAction(before, result), &before, result)
	})
	return result, err
}

// applyChange 验证新归属和状态，并在提交前检查是否妨碍进行中的业务。
// 参数 ctx、tx 为修改事务，kind、scope 为目录范围，before、candidate 为修改前后值。
// 返回值 新值或不符合资质、业务时序、数据库约束的错误。
func applyChange(ctx context.Context, tx pgx.Tx, kind, scope string, before, candidate entry) (entry, error) {
	if !validEntry(kind, candidate) {
		return entry{}, errInvalid
	}
	if kind == unit && scope != "" {
		oldArea := geoarea.QuzhouAreaCode(before.District)
		newArea := geoarea.QuzhouAreaCode(candidate.District)
		if oldArea != newArea {
			return entry{}, errDistrictManaged
		}
	}
	moving := before.InstitutionID != candidate.InstitutionID
	if kind == person && (moving || candidate.Status == active && before.Status != active) {
		if _, err := eligibleInstitution(ctx, tx, "", candidate.InstitutionID); err != nil {
			return entry{}, err
		}
	}
	if kind != person && before.Name != candidate.Name {
		if err := checkPartyName(ctx, tx, kind, before.ID, candidate.Name, before.ScopeName); err != nil {
			return entry{}, err
		}
	}
	if err := updateEntry(ctx, tx, kind, candidate); err != nil {
		return entry{}, err
	}
	updated, err := readEntry(ctx, tx, kind, scope, before.ID)
	if err != nil {
		return entry{}, err
	}
	if moving || (before.Usable && !updated.Usable) ||
		(before.Status == active && updated.Status == inactive) {
		if err := checkInFlight(ctx, tx, kind, before); err != nil {
			return entry{}, err
		}
	}
	return updated, nil
}

// editableEntry 先锁定名录条目，再在相同事务中核对归属和预期版本。
// 参数 ctx、tx 为修改事务，kind、scope、id 为范围，version 为预期版本。
// 返回值 锁定条目或不可访问、不可编辑及版本冲突错误。
func editableEntry(ctx context.Context, tx pgx.Tx, kind, scope, id string, version int64) (entry, error) {
	if err := lockEntry(ctx, tx, kind, id); err != nil {
		return entry{}, err
	}
	item, err := readEntry(ctx, tx, kind, scope, id)
	if err != nil {
		return entry{}, err
	}
	if !item.CanEdit {
		return entry{}, errForbidden
	}
	if item.Version != version {
		return entry{}, errVersion
	}
	return item, nil
}

// lockEntry 锁定真实主档行，和报送、测评等新引用的共享锁协调。
// 参数 ctx、tx 为修改事务，kind、id 为待锁定条目。
// 返回值 记录不存在或数据库错误。
func lockEntry(ctx context.Context, tx pgx.Tx, kind, id string) error {
	var locked string
	if kind == person {
		return tx.QueryRow(ctx, `SELECT id::text FROM directory_people WHERE id=$1 FOR UPDATE`, id).
			Scan(&locked)
	}
	return tx.QueryRow(ctx, `SELECT id::text FROM directory_parties WHERE kind=$1 AND id=$2 FOR UPDATE`,
		kind, id).Scan(&locked)
}

// checkPartyName 防止登记或改名占用另一条主档或尚未登记的旧备案范围。
// 参数 ctx、tx 为修改事务，kind 为类型，id、name、scope 为条目标识、拟用名称和原归属。
// 返回值 名称冲突或查询错误。
func checkPartyName(ctx context.Context, tx pgx.Tx, kind, id, name, scope string) error {
	var conflict bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM directory_parties
		WHERE kind=$1 AND id::text<>$2 AND lower($3) IN (lower(name),lower(scope_name)))
		OR ($2<>'' AND lower($3)<>lower($4) AND EXISTS (SELECT 1 FROM business_cases f
		    WHERE f.kind='filing'
		    AND lower(CASE $1 WHEN 'unit' THEN f.organization_name ELSE f.assessment_org END)=lower($3)))`,
		kind, id, name, scope).Scan(&conflict)
	if err != nil {
		return err
	}
	if conflict {
		return errDuplicate
	}
	return nil
}

// eligibleInstitution 锁定机构身份与资质，用于新增关系或人员调入。
// 参数 ctx、tx 为修改事务，scope、id 分别为唯一可用的机构范围和标识。
// 返回值 有效机构 ID，或已停用、已到期、未知机构错误。
func eligibleInstitution(ctx context.Context, tx pgx.Tx, scope, id string) (string, error) {
	var result string
	err := tx.QueryRow(ctx, `SELECT a.id::text FROM directory_parties a WHERE a.kind='institution'
		AND (($1<>'' AND a.scope_name=$1) OR ($1='' AND a.id::text=$2))
		AND `+activeInstitution+` FOR SHARE OF a`, scope, id).Scan(&result)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errUnavailable
	}
	return result, err
}

// updateEntry 按已锁定条目保存名录字段并递增版本。
// 参数 ctx、tx 为修改事务，kind、item 为目标主表和待保存字段。
// 返回值 修改操作的数据库错误。
func updateEntry(ctx context.Context, tx pgx.Tx, kind string, item entry) error {
	if kind == person {
		_, err := tx.Exec(ctx, `UPDATE directory_people SET institution_id=$2,name=$3,code=$4,
			certificate_no=$5,qualification=$6,valid_until=NULLIF($7,'')::date,status=$8,version=version+1
			WHERE id=$1`, item.ID, item.InstitutionID, item.Name, item.Code, item.CertificateNo,
			item.Qualification, item.ValidUntil, item.Status)
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE directory_parties SET name=$2,code=$3,classification=$4,
		district=$5,qualification=$6,valid_until=NULLIF($7,'')::date,status=$8,version=version+1
		WHERE id=$1`, item.ID, item.Name, item.Code, item.Classification, item.District,
		item.Qualification, item.ValidUntil, item.Status)
	return err
}

// write 序列化跨表的范围去重及单位归属约束，提交关系和历史在同一事务内。
// 参数 ctx 与 fn 分别为写入上下文和业务操作。
// 返回值 原始业务错误、登记重复或数据库错误。
func (s service) write(ctx context.Context, fn func(pgx.Tx) error) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(271123882)`); err != nil {
			return err
		}
		return fn(tx)
	})
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return errDuplicate
	}
	return err
}
