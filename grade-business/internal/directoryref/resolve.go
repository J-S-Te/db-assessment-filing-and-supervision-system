package directoryref

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

var (
	ErrUnavailable    = errors.New("所选单位、机构或人员已停用，或资质已过期。")
	ErrPersonRequired = errors.New("请从当前测评机构的有效人员中选择。")
	ErrPersonInvalid  = errors.New("所选人员不属于当前测评机构。")
)

// Party 保存办件引用的单位或测评机构当时的名录字段。
type Party struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ScopeName     string `json:"scopeName"`
	Code          string `json:"code,omitempty"`
	Qualification string `json:"qualification,omitempty"`
	ValidUntil    string `json:"validUntil,omitempty"`
}

// Person 保存办件选用的人员及其当时的机构归属。
type Person struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Code          string `json:"code"`
	CertificateNo string `json:"certificateNo,omitempty"`
	Qualification string `json:"qualification,omitempty"`
	InstitutionID string `json:"institutionId"`
}

// Snapshot 固定一次业务选择使用的单位、机构和人员信息。
type Snapshot struct {
	Unit        *Party  `json:"unit,omitempty"`
	Institution *Party  `json:"institution,omitempty"`
	Person      *Person `json:"person,omitempty"`
}

const usable = `status='active' AND (valid_until IS NULL
	OR valid_until >= (clock_timestamp() AT TIME ZONE 'Asia/Shanghai')::date)`

// Resolve 在业务事务中核对归属与资质有效性并生成可持久化的引用快照。
// 参数 ctx、tx 控制读取和行锁；unitScope、institutionScope 为备案或表单的归属，
// personID 为选中人员，requirePerson 表示登记机构须使用其人员名录。
// 返回值 实际匹配的名录快照；全为历史自由名称时为空，停用、失效和归属错误单独返回。
func Resolve(ctx context.Context, tx pgx.Tx, unitScope, institutionScope, personID string,
	requirePerson bool,
) (*Snapshot, error) {
	return resolve(ctx, tx, unitScope, institutionScope, personID, requirePerson, " FOR SHARE")
}

// Inspect 在只读事务中使用同一套名录条件核对当前归属及资质。
// 参数 ctx、tx 控制读取快照，unitScope、institutionScope 为业务归属，
// personID 为已选人员，requirePerson 表示登记机构须使用名录人员。
// 返回值 用于提示的当前名录快照或业务校验和读取错误。
func Inspect(ctx context.Context, tx pgx.Tx, unitScope, institutionScope, personID string,
	requirePerson bool,
) (*Snapshot, error) {
	return resolve(ctx, tx, unitScope, institutionScope, personID, requirePerson, "")
}

// resolve 复用表单写入和辅助读取的同一套名录校验。
// 参数 ctx、tx 控制读取，unitScope、institutionScope、personID 指定名录引用，
// requirePerson 指定人员要求，lockClause 为本模块决定的数据库行锁子句。
// 返回值 名录证据或无效引用和数据库错误。
func resolve(ctx context.Context, tx pgx.Tx, unitScope, institutionScope, personID string,
	requirePerson bool, lockClause string,
) (*Snapshot, error) {
	snapshot, err := findParties(ctx, tx, unitScope, institutionScope, lockClause)
	if err != nil {
		return nil, err
	}
	if personID == "" {
		if requirePerson && snapshot.Institution != nil {
			return nil, ErrPersonRequired
		}
	} else {
		if snapshot.Institution == nil {
			return nil, ErrPersonInvalid
		}
		snapshot.Person, err = findPerson(ctx, tx, snapshot.Institution.ID, personID, lockClause)
		if err != nil {
			return nil, err
		}
	}
	if snapshot.Unit == nil && snapshot.Institution == nil {
		return nil, nil
	}
	return &snapshot, nil
}

// References 取出业务表分别保存的三个名录外键。
// 参数 无，快照来自本次事务，允许为空。
// 返回值 单位、机构、人员的可空标识。
func (snapshot *Snapshot) References() (unit, institution, person *string) {
	if snapshot == nil {
		return nil, nil, nil
	}
	if snapshot.Unit != nil {
		unit = &snapshot.Unit.ID
	}
	if snapshot.Institution != nil {
		institution = &snapshot.Institution.ID
	}
	if snapshot.Person != nil {
		person = &snapshot.Person.ID
	}
	return unit, institution, person
}

// findParties 按稳定范围匹配单位与机构，兼容旧接口传递当前显示名称。
// 参数 ctx、tx 控制查询，unitScope、institutionScope 为引用范围，lockClause 为行锁子句。
// 返回值 一致的内部快照或停用、资质过期、查询失败原因。
func findParties(ctx context.Context, tx pgx.Tx, unitScope, institutionScope, lockClause string) (Snapshot, error) {
	rows, err := tx.Query(ctx, `SELECT id::text,kind,scope_name,name,code,qualification,
	        coalesce(to_char(valid_until,'YYYY-MM-DD'),''),`+usable+`,
	        lower(scope_name)=lower(CASE WHEN kind='unit' THEN $1 ELSE $2 END)
	    FROM directory_parties
	    WHERE (kind='unit' AND $1<>'' AND lower($1) IN (lower(scope_name),lower(name)))
	       OR (kind='institution' AND $2<>'' AND lower($2) IN (lower(scope_name),lower(name)))
	    ORDER BY id`+lockClause, unitScope, institutionScope)
	if err != nil {
		return Snapshot{}, err
	}
	defer rows.Close()
	var snapshot Snapshot
	var unitUsable, institutionUsable bool
	for rows.Next() {
		var item Party
		var kind string
		var active, exact bool
		if err := rows.Scan(&item.ID, &kind, &item.ScopeName, &item.Name, &item.Code,
			&item.Qualification, &item.ValidUntil, &active, &exact); err != nil {
			return Snapshot{}, err
		}
		if kind == "unit" && (snapshot.Unit == nil || exact) {
			snapshot.Unit, unitUsable = &item, active
		}
		if kind == "institution" && (snapshot.Institution == nil || exact) {
			snapshot.Institution, institutionUsable = &item, active
		}
	}
	if err := rows.Err(); err != nil {
		return Snapshot{}, err
	}
	if (snapshot.Unit != nil && !unitUsable) || (snapshot.Institution != nil && !institutionUsable) {
		return Snapshot{}, ErrUnavailable
	}
	return snapshot, nil
}

// findPerson 核对人员确实属于所选机构并持有可用资质。
// 参数 ctx、tx 控制查询，institutionID 与 personID 固定归属，lockClause 为行锁子句。
// 返回值 人员快照，或无效归属、停用、过期、查询失败。
func findPerson(ctx context.Context, tx pgx.Tx, institutionID, personID, lockClause string) (*Person, error) {
	var item Person
	var active bool
	err := tx.QueryRow(ctx, `SELECT id::text,institution_id::text,name,code,certificate_no,
	        qualification,`+usable+`
	    FROM directory_people WHERE id=$1 AND institution_id=$2`+lockClause, personID, institutionID).
		Scan(&item.ID, &item.InstitutionID, &item.Name, &item.Code, &item.CertificateNo,
			&item.Qualification, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPersonInvalid
	}
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, ErrUnavailable
	}
	return &item, nil
}
