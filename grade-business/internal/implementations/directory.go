package implementations

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/directoryref"
)

// prepareImplementationDirectory 把表单选择解析为实际、可用的单位、机构和报备人员。
// 参数 ctx、tx 为业务事务，filing 和 item 描述备案与表单，institution 为机构请求范围，
// operatorOrigin 表示报备由运营者发起，previous 为编辑已有记录时的历史信息。
// 返回值 本轮名录快照或人员归属、资质、写入错误。
func prepareImplementationDirectory(ctx context.Context, tx pgx.Tx, filing choice, item *input,
	institution string, operatorOrigin bool, previous *storedRecord,
) (*directoryref.Snapshot, error) {
	item.AssessmentOrg = fallbackOrg(item.AssessmentOrg, filing.AssessmentOrg)
	if previous != nil && item.ReporterID == nil && strings.EqualFold(item.AssessmentOrg, previous.AssessmentOrg) {
		item.ReporterID = previous.ReporterID
	}
	snapshot, err := resolveImplementationDirectory(ctx, tx, filing.OrganizationName, item.AssessmentOrg,
		item.ReporterID, operatorOrigin)
	if err != nil {
		return nil, err
	}
	if snapshot != nil && snapshot.Institution != nil && institution == "" {
		item.AssessmentOrg = snapshot.Institution.ScopeName
	}
	if snapshot != nil && snapshot.Person != nil {
		item.Reporter = snapshot.Person.Name
	}
	if operatorOrigin {
		if err := validateOperatorChange(item.Type, item.AssessmentOrg, filing); err != nil {
			return nil, err
		}
	}
	return snapshot, nil
}

// resolveImplementationDirectory 以机构名录校验人员，运营者换机构申请保留申请人角色。
// 参数 ctx、tx 为业务事务，unit、institution 为备案单位及目标机构，personID 为所选人员，
// operatorOrigin 标识申请发起方。
// 返回值 本轮名录快照；人员伪造、停用、过期或数据库错误。
func resolveImplementationDirectory(ctx context.Context, tx pgx.Tx, unit, institution string,
	personID *string, operatorOrigin bool,
) (*directoryref.Snapshot, error) {
	var selected string
	if personID != nil {
		selected = *personID
	}
	if operatorOrigin && selected != "" {
		return nil, directoryref.ErrPersonInvalid
	}
	return directoryref.Resolve(ctx, tx, unit, institution, selected, !operatorOrigin)
}

// refreshImplementationDirectory 送审前重新校验名录并保存此轮证据。
// 参数 ctx、tx 为送审事务，record 为已锁定、通过既有状态机检查的报备。
// 返回值 已更新报备的名录有效性和持久化错误；失败时不推进审核。
func refreshImplementationDirectory(ctx context.Context, tx pgx.Tx, record storedRecord) error {
	snapshot, err := resolveImplementationDirectory(ctx, tx, record.OrganizationName, record.AssessmentOrg,
		record.ReporterID, operatorInitiated(record))
	if err != nil {
		return err
	}
	unitID, institutionID, personID := snapshot.References()
	reporter := record.Reporter
	if snapshot != nil && snapshot.Person != nil {
		reporter = snapshot.Person.Name
	}
	_, err = tx.Exec(ctx, `UPDATE implementation_reports SET directory_unit_id=$2,directory_institution_id=$3,
	    directory_person_id=$4,directory_snapshot=$5,reporter=$6 WHERE id=$1`,
		record.ID, unitID, institutionID, personID, snapshot, reporter)
	return err
}
