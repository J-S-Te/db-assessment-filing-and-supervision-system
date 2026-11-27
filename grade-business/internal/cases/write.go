package cases

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/access"
	"grade-business/internal/classification"
	"grade-business/internal/directoryref"
	"grade-business/internal/filingpolicy"
)

type saveFields struct {
	number, systemName, organizationName, assessmentOrg string
	level, risk                                         string
	filingID                                            *string
	assessmentPersonID                                  *string
	classification                                      *classification.Assessment
}

type stateRow struct {
	status             string
	version            int64
	filingID           string
	assessmentOrg      string
	submissionProfile  string
	operatorOrigin     bool
	assessmentPersonID *string
}

// filingFields 去除备案表单的首尾空白并检查业务字段。
// 参数 input 为备案录入或编辑表单。
// 返回值 待持久化字段或表单错误。
func filingFields(input filingInput) (saveFields, error) {
	fields := saveFields{
		number: strings.TrimSpace(input.Number), systemName: strings.TrimSpace(input.SystemName),
		organizationName: strings.TrimSpace(input.OrganizationName),
		assessmentOrg:    strings.TrimSpace(input.AssessmentOrg), level: input.Level,
	}
	if !validText(fields.number, 80) || !validText(fields.systemName, 200) ||
		!validText(fields.organizationName, 200) || !validText(fields.assessmentOrg, 200) ||
		len(fields.level) != 1 || !strings.Contains("12345", fields.level) {
		return saveFields{}, ErrInvalid
	}
	if input.Classification != nil {
		assessment, err := classification.Evaluate(*input.Classification)
		if err != nil {
			return saveFields{}, err
		}
		fields.classification = &assessment
	}
	return fields, nil
}

// reportFields 去除测评表单的首尾空白并检查关联参数。
// 参数 input 为报告录入或编辑表单。
// 返回值 待持久化字段或表单错误。
func reportFields(input reportInput) (saveFields, error) {
	fields := saveFields{
		number: strings.TrimSpace(input.Number), filingID: &input.FilingID,
		assessmentOrg: strings.TrimSpace(input.AssessmentOrg), risk: input.Risk,
		assessmentPersonID: input.AssessmentPersonID,
	}
	if !validText(fields.number, 80) || !validText(fields.assessmentOrg, 200) ||
		!validID(input.FilingID) || !validRisk(fields.risk) {
		return saveFields{}, ErrInvalid
	}
	if fields.assessmentPersonID != nil && *fields.assessmentPersonID != "" && !validID(*fields.assessmentPersonID) {
		return saveFields{}, directoryref.ErrPersonInvalid
	}
	return fields, nil
}

// validRisk 核对报告使用的风险等级是否为业务约定值。
// 参数 risk 为报告录入或筛选传入的等级。
// 返回值 已定义的低、中或高风险返回真。
func validRisk(risk string) bool {
	return risk == "low" || risk == "medium" || risk == "high"
}

// create 保存新草稿，新测评在事务内确认所选备案仍可关联。
// 参数 ctx 为请求上下文，kind 为记录类型，fields 为录入字段，institution 为可选所选机构。
// 返回值 新草稿标识、状态和版本，或关联及写入错误。
func (s *Service) create(ctx context.Context, kind string, fields saveFields, institution string) (mutation, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return mutation{}, err
	}
	defer tx.Rollback(ctx)
	result, err := createCaseInTx(ctx, tx, kind, fields, institution)
	if err != nil {
		return mutation{}, err
	}
	return result, tx.Commit(ctx)
}

// createCaseInTx 在既有事务内沿用新建草稿的名录与材料配置。
// 参数 ctx、tx 为请求及写入事务，kind、fields、institution 为业务类型、输入和机构范围。
// 返回值 本事务保存的草稿或业务错误。
func createCaseInTx(ctx context.Context, tx pgx.Tx, kind string, fields saveFields, institution string) (mutation, error) {
	profile, snapshot, err := prepareCaseCreation(ctx, tx, kind, institution, &fields)
	if err != nil {
		return mutation{}, err
	}
	unitID, institutionID, personID := snapshot.References()
	row := tx.QueryRow(ctx, `
		INSERT INTO business_cases
		  (kind,number,system_name,organization_name,assessment_org,level,filing_id,risk,
		   submission_profile,operator_origin,directory_unit_id,directory_institution_id,
		   directory_person_id,directory_snapshot,classification_assessment)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id::text,status,version`, kind, fields.number, fields.systemName, fields.organizationName,
		fields.assessmentOrg, fields.level, fields.filingID, fields.risk, profile, operatorFilingScope(ctx) != "",
		unitID, institutionID, personID, snapshot, fields.classification)
	return scanMutation(row)
}

// lockReportFiling 锁定报告所选备案，保证机构发起时使用所属的有效备案。
// 参数 ctx 为请求上下文，tx 为报告事务，id 为备案标识，institution 为可选机构范围。
// 返回值 备案不存在、失效或数据库错误，可关联时返回 nil。
func lockReportFiling(ctx context.Context, tx pgx.Tx, id, institution string) error {
	var status string
	var cancelled *time.Time
	err := tx.QueryRow(ctx, `SELECT status,cancelled_at FROM business_cases
		WHERE id=$1 AND kind='filing' AND discarded_at IS NULL
		  AND ($2='' OR assessment_org=$2) FOR SHARE`,
		id, institution).Scan(&status, &cancelled)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err == nil && (cancelled != nil || (institution != "" && status != approved)) {
		return ErrFilingInactive
	}
	return err
}

// update 保存尚可编辑的记录并检测版本变化。
// 参数 ctx 携带可选运营单位，kind、id、expected 和 fields 指定记录与编辑输入，institution 为机构范围。
// 返回值 新版本或不存在、版本冲突、写入错误。
func (s *Service) update(ctx context.Context, kind, id string, expected int64,
	fields saveFields, institution string,
) (mutation, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return mutation{}, err
	}
	defer tx.Rollback(ctx)
	current, err := lockRecord(ctx, tx, kind, id)
	if err != nil {
		return mutation{}, err
	}
	if err := current.checkInstitutionEdit(institution); err != nil {
		return mutation{}, err
	}
	fixedAssessment := institution != "" || (operatorFilingScope(ctx) != "" && current.status == returned)
	if fixedAssessment && fields.assessmentOrg != current.assessmentOrg {
		return mutation{}, ErrConflict
	}
	if current.version != expected || !editable(current.status) {
		return mutation{}, ErrConflict
	}
	snapshot, err := prepareCaseUpdate(ctx, tx, kind, institution, current, &fields)
	if err != nil {
		return mutation{}, err
	}
	unitID, institutionID, personID := snapshot.References()
	row := tx.QueryRow(ctx, `
		UPDATE business_cases SET number=$2,system_name=$3,organization_name=$4,assessment_org=$5,
		    level=$6,filing_id=$7,risk=$8,version=version+1,directory_unit_id=$9,
		    directory_institution_id=$10,directory_person_id=$11,directory_snapshot=$12,
		    classification_assessment=COALESCE($13,classification_assessment)
		WHERE id=$1
		RETURNING id::text,status,version`, id, fields.number, fields.systemName, fields.organizationName,
		fields.assessmentOrg, fields.level, fields.filingID, fields.risk, unitID, institutionID,
		personID, snapshot, fields.classification)
	result, err := scanMutation(row)
	if err != nil {
		return mutation{}, err
	}
	if current.status == returned && fields.assessmentOrg != current.assessmentOrg {
		if err := redeliverCorrection(ctx, tx, kind, id); err != nil {
			return mutation{}, err
		}
	}
	return result, tx.Commit(ctx)
}

// validateReportAssociation 锁定新选择的备案，并在新版报告的草稿阶段重新核对归属。
// 参数 ctx 与 tx 控制写入，current、fields 为已有记录状态与表单，institution 为可选机构范围。
// 返回值 关联备案无效或范围不符时返回业务错误。
func validateReportAssociation(ctx context.Context, tx pgx.Tx, current stateRow, fields saveFields, institution string) error {
	newAssociation := !strings.EqualFold(*fields.filingID, current.filingID)
	institutionReport := current.submissionProfile == institutionSubmissionProfile
	if !newAssociation && !current.requiresInitialFilingScope(institution) {
		return nil
	}
	if institutionReport {
		institution = fields.assessmentOrg
	}
	return lockReportFiling(ctx, tx, *fields.filingID, institution)
}

// requiresInitialFilingScope 判断首次机构报送是否需要核对当前备案归属。
// 参数 institution 为当前显式办理机构，空值表示监管端。
// 返回值 草稿属于机构路径或使用机构报送配置时为真。
func (row stateRow) requiresInitialFilingScope(institution string) bool {
	return row.status == draft && (institution != "" || row.submissionProfile == institutionSubmissionProfile)
}

// scanMutation 将数据库写入结果转成最小业务响应。
// 参数 row 为带有标识、状态和版本的返回行。
// 返回值 变更结果或可识别的数据库写入错误。
func scanMutation(row pgx.Row) (mutation, error) {
	var result mutation
	err := row.Scan(&result.ID, &result.Status, &result.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return mutation{}, ErrNotFound
	}
	return result, constraintError(err)
}

// lockRecord 为状态或附件变更锁定目标记录。
// 参数 ctx 携带可选运营单位，tx 为当前数据库事务，kind 与 id 指定记录。
// 返回值 当前版本、状态、关联备案、机构和材料配置，或记录不存在、数据库错误。
func lockRecord(ctx context.Context, tx pgx.Tx, kind, id string) (stateRow, error) {
	return lockInstitutionRecord(ctx, tx, kind, id, "")
}

// lockInstitutionRecord 在读取和锁定记录前应用机构和已有的运营单位范围。
// 参数 ctx、tx 为请求及事务，kind、id、institution 为业务标识及可选机构。
// 返回值 当前版本、状态及归属，或记录不可见和数据库错误。
func lockInstitutionRecord(ctx context.Context, tx pgx.Tx, kind, id, institution string) (stateRow, error) {
	if !validID(id) {
		return stateRow{}, ErrNotFound
	}
	var row stateRow
	err := tx.QueryRow(ctx, `SELECT c.status,c.version,COALESCE(c.filing_id::text,''),
		    c.assessment_org,c.submission_profile,c.operator_origin,c.directory_person_id::text`+
		recordJoin+`
		WHERE c.kind=$1 AND c.id=$2 AND c.discarded_at IS NULL
		  AND ($3='' OR (c.kind='filing' AND c.organization_name=$3))
		  AND ($4='' OR c.assessment_org=$4)`+caseDistrictScope("$5")+
		` FOR UPDATE OF c`, kind, id, operatorFilingScope(ctx), institution, access.Districts(ctx)).
		Scan(&row.status, &row.version, &row.filingID, &row.assessmentOrg, &row.submissionProfile,
			&row.operatorOrigin, &row.assessmentPersonID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return row, err
}

// changeStatus 在同一事务中推进状态并写入对应审核留痕。
// 参数 ctx 携带可选运营单位，kind 与 id 指定记录，input 携带动作、意见和预期版本。
// 返回值 新状态与版本，或材料不足、状态冲突及写入错误。
func (s *Service) changeStatus(ctx context.Context, kind, id string, input reviewInput) (mutation, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return mutation{}, err
	}
	defer tx.Rollback(ctx)
	result, err := s.changeStatusInTx(ctx, tx, kind, id, input)
	if err != nil {
		return mutation{}, err
	}
	return result, tx.Commit(ctx)
}

// changeStatusInTx 在单笔办理和批量保存点中执行同一状态与材料校验。
// 参数 ctx、tx 为请求和写入事务，kind、id、input 为记录类型、标识及操作。
// 返回值 本事务内已推进的状态及版本，或业务与数据库错误。
func (s *Service) changeStatusInTx(ctx context.Context, tx pgx.Tx, kind, id string, input reviewInput) (mutation, error) {
	current, err := lockInstitutionRecord(ctx, tx, kind, id, input.institution)
	if err != nil {
		return mutation{}, err
	}
	if input.institutionReviewer != "" && current.assessmentOrg != input.institutionReviewer {
		return mutation{}, ErrNotFound
	}
	if err := current.checkInstitutionEdit(input.institution); err != nil {
		return mutation{}, err
	}
	if current.version != input.ExpectedVersion {
		return mutation{}, ErrConflict
	}
	if err := input.checkReviewRole(ctx, current.status, current.operatorOrigin); err != nil {
		return mutation{}, err
	}
	if err := requireSeparateReviewer(ctx, tx, id, current.status, false); err != nil {
		return mutation{}, err
	}
	if kind == filing {
		if err := guardWithdrawalReview(ctx, tx, withdrawalFilings, id); err != nil {
			return mutation{}, err
		}
	}
	ready, err := submissionReady(ctx, tx, kind, id, input, current)
	if err != nil {
		return mutation{}, err
	}
	if input.Decision == approve && !ready {
		return mutation{}, ErrMaterials
	}
	comment := strings.TrimSpace(input.Comment)
	next, err := transition(current.status, input.Decision, comment, ready)
	if err != nil {
		return mutation{}, err
	}
	if kind == report && input.Decision == submit {
		if err := refreshReportDirectory(ctx, tx, id, current); err != nil {
			return mutation{}, err
		}
	}
	operator := input.reviewActor(ctx, s.operator, operatorFilingScope(ctx))
	return s.persistDecision(ctx, tx, kind, id, current, next, input.Decision, comment, operator)
}

// submissionReady 按锁定记录的提交配置核对资格与材料，并在自主备案通过前复查必传材料。
// 参数 ctx 与 tx 控制审核事务，kind、id 指定记录，input 为业务操作与范围，current 为锁定的状态。
// 返回值 必传材料是否齐备，或关联业务及数据库错误。
func submissionReady(ctx context.Context, tx pgx.Tx, kind, id string, input reviewInput, current stateRow) (bool, error) {
	submitting := input.Decision == submit && editable(current.status)
	approvingFiling := input.Decision == approve && current.operatorOrigin &&
		(current.status == pendingInitial || current.status == pendingFinal)
	if !submitting && !approvingFiling {
		return true, nil
	}
	if kind == report {
		initial := current.requiresInitialFilingScope(input.institution)
		if initial || current.submissionProfile == institutionSubmissionProfile {
			institution := ""
			if initial {
				institution = current.assessmentOrg
			}
			if err := lockReportFiling(ctx, tx, current.filingID, institution); err != nil {
				return false, err
			}
		}
	}
	required := requiredCaseMaterials(kind, current.submissionProfile)
	var ready, hasIssue bool
	scope := ""
	if submitting && kind == filing {
		scope = operatorFilingScope(ctx)
	}
	err := tx.QueryRow(ctx, `SELECT (
		    ($3='filing' AND COALESCE(bool_or(a.kind='filing_package'),false))
		    OR count(DISTINCT a.kind) FILTER (WHERE a.kind=ANY($2::text[]))=cardinality($2::text[])
		), EXISTS(SELECT 1 FROM material_issues WHERE case_id=$1 AND closed_at IS NULL)
		FROM case_attachments a JOIN business_cases c ON c.id=a.case_id
		WHERE a.case_id=$1 AND ($4='' OR COALESCE(a.merge_history_scope,c.organization_name)=$4)`,
		id, required, kind, scope).Scan(&ready, &hasIssue)
	if err == nil && hasIssue {
		return false, ErrOpenMaterialIssues
	}
	if err == nil && ready && submitting && kind == filing {
		err = checkConfiguredMaterials(ctx, tx, filingpolicy.Initial, id)
	}
	return ready, err
}

// persistDecision 同时持久化状态、审核记录及相应的测评或备案意见。
// 参数 ctx、tx、kind、id、current 为事务及锁定记录，next、action、comment、operator 描述操作。
// 返回值 新状态与版本，或数据库执行错误。
func (s *Service) persistDecision(ctx context.Context, tx pgx.Tx,
	kind, id string, current stateRow, next, action, comment, operator string,
) (mutation, error) {
	result, err := scanMutation(tx.QueryRow(ctx, `
		UPDATE business_cases SET status=$2,version=version+1,
		    submitted_at=CASE WHEN $3='submit' THEN clock_timestamp() ELSE submitted_at END
		WHERE id=$1 RETURNING id::text,status,version`, id, next, action))
	if err != nil {
		return mutation{}, err
	}
	actor, _ := access.Current(ctx)
	_, err = tx.Exec(ctx, `
		WITH event AS (
		    INSERT INTO case_review_events(case_id,action,from_status,to_status,operator,comment,version,
		                                   directory_snapshot,operator_id,operator_role)
		    SELECT id,$2,$3,$4,$5,$6,$7,directory_snapshot,NULLIF($8,'')::uuid,NULLIF($9,'') FROM business_cases WHERE id=$1
		    RETURNING id,case_id,to_status
		)
		INSERT INTO report_feedback(event_id,institution,report_number,system_name)
		SELECT e.id,c.assessment_org,c.number,f.system_name FROM event e
		JOIN business_cases c ON c.id=e.case_id
		JOIN business_cases f ON f.id=c.filing_id
		WHERE c.kind='report' AND e.to_status IN ('returned','approved')`,
		id, action, current.status, next, operator, comment, result.Version, actor.ID, actor.Role)
	if err == nil && kind == filing && (current.operatorOrigin || next == returned || next == approved) {
		err = publishFilingReview(ctx, tx, id, result.Version)
	}
	return result, err
}
