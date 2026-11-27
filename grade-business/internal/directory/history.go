package directory

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// eventAction 由值变化判断历史动作，调入与停用另行标记。
// 参数 before、after 为事务中的目录变更前后值。
// 返回值 可独立审计的动作名称。
func eventAction(before, after entry) string {
	switch {
	case before.InstitutionID != after.InstitutionID:
		return "transfer"
	case before.Status != after.Status && after.Status == inactive:
		return "disable"
	case before.Status != after.Status:
		return "enable"
	default:
		return "update"
	}
}

// recordEvent 保存目录变更及操作范围的审计快照。
// 参数 ctx、tx 为目录事务，kind、scope、action 为事件分类，before、after 为前后值。
// 返回值 历史持久化失败或 nil。
func recordEvent(ctx context.Context, tx pgx.Tx, kind, scope, action string,
	before *entry, after entry,
) error {
	actor := "监管端"
	if scope != "" {
		actor = "机构：" + scope
	}
	_, err := tx.Exec(ctx, `INSERT INTO directory_events (
		entry_kind,entry_id,entry_version,action,actor,institution_id,before_values,after_values)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8)`,
		kind, after.ID, after.Version, action, actor, after.InstitutionID, before, after)
	return err
}

// checkInFlight 阻止停用或调离仍绑定在审业务的主体。
// 参数 ctx、tx 为更新事务，kind、item 为原始目录条目。
// 返回值 有在审业务时返回冲突，否则为查询错误或 nil。
func checkInFlight(ctx context.Context, tx pgx.Tx, kind string, item entry) error {
	caseField, implementationField := dependencyFields(kind)
	var blocked bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (
	    SELECT 1 FROM business_cases c LEFT JOIN business_cases f ON f.id=c.filing_id
	    WHERE c.status IN ('pending_initial','pending_final') AND c.discarded_at IS NULL
	      AND (`+caseField+`)
	) OR EXISTS (
	    SELECT 1 FROM implementation_reports i
	    WHERE `+implementationInFlight+` AND (`+implementationField+`)
	)`, item.ID, item.ScopeName).Scan(&blocked)
	if err != nil {
		return err
	}
	if blocked {
		return errInFlight
	}
	return nil
}

// dependencyFields 提供条目类型对应的业务引用条件，仅为固定的服务器定义 SQL。
// 参数 kind 为名录实体类别。
// 返回值 备案/测评和实施表各自的检查条件。
func dependencyFields(kind string) (string, string) {
	switch kind {
	case unit:
		return `c.directory_unit_id=$1 OR (c.directory_unit_id IS NULL
		    AND COALESCE(NULLIF(c.organization_name,''),f.organization_name)=$2)`,
			`i.directory_unit_id=$1 OR (i.directory_unit_id IS NULL AND i.organization_name=$2)`
	case institution:
		return `c.directory_institution_id=$1 OR (c.directory_institution_id IS NULL AND c.assessment_org=$2)`,
			`i.directory_institution_id=$1 OR (i.directory_institution_id IS NULL AND i.assessment_org=$2)`
	default:
		return `c.directory_person_id=$1 AND $2='' `, `i.directory_person_id=$1`
	}
}

// linkUnit 为已登记的机构维护单份被测单位关系。
// 参数 ctx、tx 为目录事务，unitID、institutionID 为关联双方。
// 返回值 是否新增关系，以及写入错误。
func linkUnit(ctx context.Context, tx pgx.Tx, unitID, institutionID string) (bool, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO directory_unit_memberships(institution_id,unit_id)
		VALUES($1,$2) ON CONFLICT DO NOTHING`, institutionID, unitID)
	return tag.RowsAffected() == 1, err
}

// link 按有效范围为机构关联已有单位，重复提交沿用原关系和历史。
// 参数 ctx 为登记上下文，scope 为主动关联机构，id 为已登记单位。
// 返回值 关联后的单位详情或无权维护、停用及数据库错误。
func (s service) link(ctx context.Context, scope, id string) (entry, error) {
	var result entry
	err := s.write(ctx, func(tx pgx.Tx) error {
		institutionID, err := eligibleInstitution(ctx, tx, scope, "")
		if err != nil {
			return err
		}
		item, err := readEntry(ctx, tx, unit, "", id)
		if err != nil {
			return err
		}
		if !item.Usable {
			return errUnavailable
		}
		created, err := linkUnit(ctx, tx, id, institutionID)
		if err != nil {
			return err
		}
		result, err = readEntry(ctx, tx, unit, scope, id)
		if err != nil || !created {
			return err
		}
		return recordEvent(ctx, tx, unit, scope, "link", nil, result)
	})
	return result, err
}
