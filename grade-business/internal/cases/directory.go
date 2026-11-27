package cases

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/directoryref"
)

// prepareCaseCreation 为新测评建立名录引用，沿用备案的现有录入和审核流程。
// 参数 ctx、tx 为创建事务，kind、institution 和 fields 描述业务类型、机构范围、拟保存字段。
// 返回值 材料配置、可空的名录快照或业务校验错误。
func prepareCaseCreation(ctx context.Context, tx pgx.Tx, kind, institution string,
	fields *saveFields,
) (string, *directoryref.Snapshot, error) {
	if kind != report {
		return legacySubmissionProfile, nil, nil
	}
	if err := lockReportFiling(ctx, tx, *fields.filingID, institution); err != nil {
		return "", nil, err
	}
	profile := legacySubmissionProfile
	if institution != "" {
		profile = institutionSubmissionProfile
	}
	snapshot, err := resolveReportDirectory(ctx, tx, *fields.filingID, fields.assessmentOrg, fields.assessmentPersonID)
	applyAssessmentScope(fields, institution, snapshot)
	return profile, snapshot, err
}

// prepareCaseUpdate 在现有业务编辑约束下解析有效的测评单位、机构和人员。
// 参数 ctx、tx 为更新事务，kind、institution、current、fields 为范围、现状和新输入。
// 返回值 本次确认的名录快照或归属、版本化关联校验错误。
func prepareCaseUpdate(ctx context.Context, tx pgx.Tx, kind, institution string,
	current stateRow, fields *saveFields,
) (*directoryref.Snapshot, error) {
	if kind != report {
		return nil, nil
	}
	if err := validateReportAssociation(ctx, tx, current, *fields, institution); err != nil {
		return nil, err
	}
	if fields.assessmentPersonID == nil && strings.EqualFold(fields.assessmentOrg, current.assessmentOrg) {
		fields.assessmentPersonID = current.assessmentPersonID
	}
	snapshot, err := resolveReportDirectory(ctx, tx, *fields.filingID, fields.assessmentOrg, fields.assessmentPersonID)
	applyAssessmentScope(fields, institution, snapshot)
	return snapshot, err
}

// resolveReportDirectory 从报告关联备案取得被测单位并验证共享名录的选择。
// 参数 ctx、tx 为业务事务，filingID、assessmentOrg、personID 为报告已核对的关联选择。
// 返回值 真实名录快照或读取、停用或资质校验错误。
func resolveReportDirectory(ctx context.Context, tx pgx.Tx, filingID, assessmentOrg string,
	personID *string,
) (*directoryref.Snapshot, error) {
	var organization string
	if err := tx.QueryRow(ctx, `SELECT organization_name FROM business_cases
		WHERE id=$1 AND kind='filing' FOR SHARE`, filingID).Scan(&organization); err != nil {
		return nil, err
	}
	var person string
	if personID != nil {
		person = *personID
	}
	return directoryref.Resolve(ctx, tx, organization, assessmentOrg, person, true)
}

// applyAssessmentScope 将监管表单的显示名称规范为名录稳定的业务范围。
// 参数 fields 为当前编辑输入，institution 为机构请求范围，snapshot 为已读取的名录。
// 返回值 无，机构请求保持通过既有归属校验的名称。
func applyAssessmentScope(fields *saveFields, institution string, snapshot *directoryref.Snapshot) {
	if institution == "" && snapshot != nil && snapshot.Institution != nil {
		fields.assessmentOrg = snapshot.Institution.ScopeName
	}
}

// refreshReportDirectory 送审时再检验可用资质，并保存本轮留痕的名录快照。
// 参数 ctx、tx 为送审事务，id 为报告标识，current 为加锁后的报告数据。
// 返回值 校验或写入错误，失败时审核状态保持原值。
func refreshReportDirectory(ctx context.Context, tx pgx.Tx, id string, current stateRow) error {
	snapshot, err := resolveReportDirectory(ctx, tx, current.filingID, current.assessmentOrg, current.assessmentPersonID)
	if err != nil {
		return err
	}
	unit, institution, person := snapshot.References()
	_, err = tx.Exec(ctx, `UPDATE business_cases SET directory_unit_id=$2,directory_institution_id=$3,
	    directory_person_id=$4,directory_snapshot=$5 WHERE id=$1`, id, unit, institution, person, snapshot)
	return err
}
