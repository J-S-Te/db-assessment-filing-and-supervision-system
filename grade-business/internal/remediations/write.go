package remediations

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/access"
)

type lockedPlan struct {
	Status     string
	ReportID   string
	Deadline   string
	Version    int64
	Submission int
}

// create 在锁定报告和备案有效状态后创建一份整改规划草案。
// 参数 ctx 为业务请求上下文，input 为校验后的整改材料信息。
// 返回值 已创建的草稿版本，或关联和写入错误。
func (s *Service) create(ctx context.Context, input planInput) (mutation, error) {
	var out mutation
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := lockEligible(ctx, tx, input.ReportID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO remediation_plans(report_id,deadline,risks)
			VALUES($1,$2::text::date,$3) RETURNING id::text,status,version`,
			input.ReportID, input.Deadline, input.Risks).Scan(&out.ID, &out.Status, &out.Version)
	})
	return out, err
}

// edit 在版本匹配且记录可编辑时保存整改计划变更。
// 参数 ctx 含企业端可选范围，id 为当前规划标识，input 为期望版本和新方案。
// 返回值 保存后的版本，或业务冲突及写入错误。
func (s *Service) edit(ctx context.Context, id string, input planInput) (mutation, error) {
	out := mutation{ID: id}
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		current, err := lockPlan(ctx, tx, id)
		if err != nil {
			return err
		}
		if current.Version != input.ExpectedVersion || !editable(current.Status) {
			return errConflict
		}
		if changed := !strings.EqualFold(current.ReportID, input.ReportID); changed {
			if err := checkReportChange(ctx, tx, current, id, input.ReportID); err != nil {
				return err
			}
		} else if current.Status == draft && operatorOrganization(ctx) != "" {
			if err := lockEligible(ctx, tx, input.ReportID); err != nil {
				return err
			}
		}
		return tx.QueryRow(ctx, `UPDATE remediation_plans SET
			report_id=$2,deadline=$3::text::date,risks=$4,version=version+1
			WHERE id=$1 RETURNING status,version`, id, input.ReportID, input.Deadline, input.Risks).
			Scan(&out.Status, &out.Version)
	})
	return out, err
}

// checkReportChange 核对无已登记材料的草稿能否更换测评报告。
// 参数 ctx 为请求上下文，tx 已锁定计划，current 为原状态，id 为计划，reportID 为待关联报告。
// 返回值 可变更时为 nil，其他情况为计划、报告或读取错误。
func checkReportChange(ctx context.Context, tx pgx.Tx, current lockedPlan, id, reportID string) error {
	if current.Status != draft {
		return errReportChange
	}
	var hasMaterials bool
	// 与材料登记共用计划行锁，读取锁取得后已提交的附件。
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM remediation_attachments WHERE plan_id=$1)`,
		id).Scan(&hasMaterials)
	if err != nil {
		return err
	}
	if hasMaterials {
		return errReportChange
	}
	return lockEligible(ctx, tx, reportID)
}

// lockPlan 按整改标识获取行锁，串行化附件、草稿和审核状态写入。
// 参数 ctx 含企业端可选范围，tx 为当前事务，id 为整改规划标识。
// 返回值 当前事务内的一致计划状态，或记录不存在与读取错误。
func lockPlan(ctx context.Context, tx pgx.Tx, id string) (lockedPlan, error) {
	var out lockedPlan
	err := tx.QueryRow(ctx, `SELECT p.status,p.report_id::text,to_char(p.deadline,'YYYY-MM-DD'),
		p.version,p.submission `+joins+` WHERE p.id=$1
		AND ($2='' OR p.frozen_organization_name IS NULL)
		AND `+access.FilingDistrictScope("f.id", "$3", true)+` FOR UPDATE OF p`,
		id, operatorOrganization(ctx), access.Districts(ctx)).
		Scan(&out.Status, &out.ReportID, &out.Deadline, &out.Version, &out.Submission)
	if organization := operatorOrganization(ctx); err == nil && organization != "" {
		err = lockOrganization(ctx, tx, out.ReportID, organization)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return lockedPlan{}, errNotFound
	}
	return out, err
}

// lockOrganization 在整改写事务内核对并锁定报告关联的当前备案单位。
// 参数 ctx 为请求上下文，tx 为已锁整改规划的事务，reportID 为其关联报告，organization 为企业全称。
// 返回值 匹配时为 nil，未找到记录或数据库失败时返回错误。
func lockOrganization(ctx context.Context, tx pgx.Tx, reportID, organization string) error {
	var matched string
	return tx.QueryRow(ctx, `SELECT f.organization_name FROM business_cases r
		JOIN business_cases f ON f.id=r.filing_id AND f.kind='filing'
		WHERE r.id=$1 AND r.kind='report' AND f.organization_name=$2
		FOR SHARE OF r,f`, reportID, organization).Scan(&matched)
}

// lockEligible 校验有效备案下的报告，运营者首次制订使用本单位最新通过报告。
// 参数 ctx 含可选单位范围，tx 为当前事务，reportID 为关联报告标识。
// 返回值 加共享锁确认报告有效后为 nil，或报告不可用及读取错误。
func lockEligible(ctx context.Context, tx pgx.Tx, reportID string) error {
	var id string
	organization := operatorOrganization(ctx)
	err := tx.QueryRow(ctx, `SELECT r.id FROM business_cases r
		JOIN business_cases f ON f.id=r.filing_id WHERE r.id=$1
		AND r.kind='report' AND r.status='approved' AND f.kind='filing' AND
		f.status='approved' AND f.cancelled_at IS NULL AND ($2='' OR f.organization_name=$2)
		FOR SHARE OF r,f`, reportID, organization).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return errReport
	}
	if err != nil || organization == "" {
		return err
	}
	var newest bool
	err = tx.QueryRow(ctx, "SELECT "+newestReport+" FROM business_cases r WHERE r.id=$1", reportID).Scan(&newest)
	if err == nil && !newest {
		return errLatestReport
	}
	return err
}

// editable 判断整改规划材料是否可编辑或提交。
// 参数 status 为数据库保存的当前审核状态。
// 返回值 草稿或退回后可继续完善时为真。
func editable(status string) bool {
	return status == draft || status == returned
}
