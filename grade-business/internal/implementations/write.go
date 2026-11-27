package implementations

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/directoryref"
	"grade-business/internal/reviewinbox"
)

// createReport 获取有效备案快照并原子保存报备草稿及首次事件。
// 参数 ctx 控制写入生命周期，institution 为可选机构范围，item 为已校验的报备输入。
// 返回值 新建记录的标识、状态和版本或写入失败原因。
func (s *Service) createReport(ctx context.Context, institution string, item input) (mutation, error) {
	if institution != "" {
		if item.AssessmentOrg != "" && item.AssessmentOrg != institution {
			return mutation{}, errInvalid
		}
		item.AssessmentOrg = institution
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mutation{}, err
	}
	defer tx.Rollback(ctx)
	filing, err := lockActiveFiling(ctx, tx, item.FilingID, institution)
	if err != nil {
		return mutation{}, err
	}
	snapshot, err := prepareImplementationDirectory(ctx, tx, filing, &item,
		institution, operatorOrganization(ctx) != "", nil)
	if err != nil {
		return mutation{}, err
	}
	unitID, institutionID, personID := snapshot.References()
	var result mutation
	operator := s.reportOperator(ctx, institution)
	err = tx.QueryRow(ctx, `INSERT INTO implementation_reports (
	  filing_id, filing_number, system_name, organization_name, assessment_org, report_type,
	  reporter,contact,planned_start,planned_end,plan,created_by,directory_unit_id,
	  directory_institution_id,directory_person_id,directory_snapshot
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::text::date,$10::text::date,$11,$12,$13,$14,$15,$16)
	RETURNING id::text,status,version`, item.FilingID, filing.Number, filing.SystemName,
		filing.OrganizationName, item.AssessmentOrg, item.Type, item.Reporter, item.Contact,
		item.PlannedStart, item.PlannedEnd, item.Plan, operator, unitID, institutionID, personID, snapshot).
		Scan(&result.ID, &result.Status, &result.Version)
	if err != nil {
		return mutation{}, err
	}
	return finish(ctx, tx, result, storedRecord{}, command{Action: "create"}, operator, s.now())
}

// editReport 校验版本、经办权限及时间边界，再更新报备。
// 参数 ctx 控制写入，id 为记录标识，institution 为可选机构范围，patch 为新字段和已见版本。
// 返回值 新版本及数据库或领域错误。
func (s *Service) editReport(ctx context.Context, id, institution string, patch patchInput) (mutation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mutation{}, err
	}
	defer tx.Rollback(ctx)
	record, err := getRecord(ctx, tx, id, institution, true)
	if err != nil {
		return mutation{}, err
	}
	if patch.ExpectedVersion != record.Version {
		return mutation{}, errStale
	}
	operator := s.reportOperator(ctx, institution)
	now := s.now()
	if record.Status == submitted {
		return editSubmittedPlan(ctx, tx, record, institution, operator, patch.input, now)
	}
	reassigning := patch.AssessmentOrg != "" && patch.AssessmentOrg != record.AssessmentOrg
	if institution != "" && (!canInstitutionEdit(record, operator) || reassigning) {
		return mutation{}, errConflict
	}
	if operatorOrganization(ctx) != "" && !canOperatorEdit(record, operator) {
		return mutation{}, errConflict
	}
	if record.Status == returned && planStarted(record, now) {
		return mutation{}, errPlanClosed
	}
	filing, err := editableFiling(ctx, tx, record, institution, operator, &patch.input)
	if err != nil {
		return mutation{}, err
	}
	snapshot, err := prepareImplementationDirectory(ctx, tx, filing, &patch.input,
		institution, operatorInitiated(record), &record)
	if err != nil {
		return mutation{}, err
	}
	result, err := applyEdit(ctx, tx, id, filing, patch.input, snapshot)
	if err != nil {
		return mutation{}, err
	}
	if record.Status == returned && patch.AssessmentOrg != record.AssessmentOrg {
		record.AssessmentOrg = patch.AssessmentOrg
		if err := republishImplementationNotice(ctx, tx, record); err != nil {
			return mutation{}, err
		}
	}
	return finish(ctx, tx, result, record, command{Action: "edit"}, operator, now)
}

// editSubmittedPlan 更新从未受理的报备计划，并保留提交及快照信息。
// 参数 ctx、tx 控制持锁事务，record 为报备，institution、operator 为经办范围，item 和 now 为拟改内容及时间。
// 返回值 已更新记录的版本，或规则与持久化错误。
func editSubmittedPlan(ctx context.Context, tx pgx.Tx, record storedRecord,
	institution, operator string, item input, now time.Time,
) (mutation, error) {
	if !canAmendSubmittedPlan(record, operator) {
		return mutation{}, errConflict
	}
	if planStarted(record, now) {
		return mutation{}, errPlanClosed
	}
	if !matchesSubmittedIdentity(record, item) {
		return mutation{}, errConflict
	}
	if _, err := lockActiveFiling(ctx, tx, record.FilingID, institution); err != nil {
		return mutation{}, err
	}
	var result mutation
	err := tx.QueryRow(ctx, `UPDATE implementation_reports SET planned_start=$2::text::date,
	  planned_end=$3::text::date,plan=$4,version=version+1
	  WHERE id=$1 RETURNING id::text,status,version`, record.ID, item.PlannedStart, item.PlannedEnd, item.Plan).
		Scan(&result.ID, &result.Status, &result.Version)
	if err != nil {
		return mutation{}, err
	}
	return finish(ctx, tx, result, record, command{Action: "edit"}, operator, now)
}

// matchesSubmittedIdentity 防止修订既有计划时变更报备归属、人员或类型。
// 参数 record 为已提交的报备，item 为经基础校验的表单。
// 返回值 已提交的关键字段保持原值或省略时为真。
func matchesSubmittedIdentity(record storedRecord, item input) bool {
	return record.FilingID == item.FilingID && record.Type == item.Type &&
		(item.AssessmentOrg == "" || record.AssessmentOrg == item.AssessmentOrg) &&
		(item.Reporter == "" || record.Reporter == item.Reporter) &&
		(item.Contact == "" || record.Contact == item.Contact) &&
		(item.ReporterID == nil || *item.ReporterID == reporterIdentifier(record))
}

// editableFiling 判断当前状态能否编辑关联并取得相应备案资料。
// 参数 ctx 控制锁定，tx 为当前事务，record 为现有报备，institution、operator 为经办范围及标记，item 为拟更新字段。
// 返回值 对应的备案快照或冲突原因。
func editableFiling(ctx context.Context, tx pgx.Tx, record storedRecord, institution, operator string, item *input) (choice, error) {
	if operatorOrganization(ctx) != "" {
		return operatorEditableFiling(ctx, tx, record, operator, item)
	}
	if record.Status == draft {
		return lockActiveFiling(ctx, tx, item.FilingID, institution)
	}
	if record.Status != returned || record.FilingID != item.FilingID || record.Type != item.Type {
		return choice{}, errConflict
	}
	return choice{ID: record.FilingID, Number: record.FilingNumber, SystemName: record.SystemName,
		OrganizationName: record.OrganizationName, AssessmentOrg: record.AssessmentOrg}, nil
}

// applyEdit 保存表单字段和新增版本号。
// 参数 ctx、tx 控制写入，id 为报备标识，filing、item、snapshot 为备案、输入和名录的已核对数据。
// 返回值 更新后的版本或数据库错误。
func applyEdit(ctx context.Context, tx pgx.Tx, id string, filing choice, item input,
	snapshot *directoryref.Snapshot,
) (mutation, error) {
	var result mutation
	unitID, institutionID, personID := snapshot.References()
	err := tx.QueryRow(ctx, `UPDATE implementation_reports SET
	  filing_id=$2, filing_number=$3, system_name=$4, organization_name=$5, assessment_org=$6,
	  report_type=$7, reporter=$8, contact=$9, planned_start=$10::text::date, planned_end=$11::text::date,
	  plan=$12,version=version+1,directory_unit_id=$13,directory_institution_id=$14,
	  directory_person_id=$15,directory_snapshot=$16 WHERE id=$1 RETURNING id::text,status,version`,
		id, item.FilingID, filing.Number, filing.SystemName, filing.OrganizationName, item.AssessmentOrg,
		item.Type, item.Reporter, item.Contact, item.PlannedStart, item.PlannedEnd, item.Plan,
		unitID, institutionID, personID, snapshot).
		Scan(&result.ID, &result.Status, &result.Version)
	return result, err
}

// execute 锁定当前版本并办理受理、审核、提交、撤销或归档。
// 参数 ctx 控制写入，id 为报备标识，institution 为可选机构范围，change 为命令和已见版本。
// 返回值 更新后的状态、版本或领域拒绝原因。
func (s *Service) execute(ctx context.Context, id, institution string, change command) (mutation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mutation{}, err
	}
	defer tx.Rollback(ctx)
	record, err := getRecord(ctx, tx, id, institution, true)
	if err != nil {
		return mutation{}, err
	}
	if record.Version != change.ExpectedVersion {
		return mutation{}, errStale
	}
	operator := s.reportOperator(ctx, institution)
	switch {
	case operatorOrganization(ctx) != "":
		err = checkOperatorImplementationAction(record, operator, change.Action)
	case institution != "":
		err = checkInstitutionAction(record, operator, change.Action)
	}
	if err != nil {
		return mutation{}, err
	}
	now := s.now()
	next, err := nextStatus(record, change.Action, change.Comment, operator, now)
	if err != nil {
		return mutation{}, err
	}
	if err := prepareAction(ctx, tx, &record, institution, change.Action, now); err != nil {
		return mutation{}, err
	}
	if change.Action == "submit" {
		if err := refreshImplementationDirectory(ctx, tx, record); err != nil {
			return mutation{}, err
		}
	}
	result, err := updateStatus(ctx, tx, record, change.Action, next, now)
	if err != nil {
		return mutation{}, err
	}
	return finish(ctx, tx, result, record, change, operator, now)
}

// reportOperator 根据已验证的归属选择实施报备的服务端留痕经办标记。
// 参数 ctx 携带运营者范围，institution 为当前机构范围，两者空值表示监管经办人。
// 返回值 用于发起人约束及写入留痕的名称。
func (s *Service) reportOperator(ctx context.Context, institution string) string {
	if organization := operatorOrganization(ctx); organization != "" {
		return operatorApplicantPrefix + organization
	}
	if institution != "" {
		return "本地机构：" + institution
	}
	return s.operator
}

// prepareAction 串行处理同一备案的撤销和提交，并在提交前校验冷却及额度。
// 参数 ctx 控制查询，tx 持有报备锁，record 为待操作记录，institution、action、now 描述经办范围与动作。
// 返回值 归属、备案状态、目标机构或额度错误。
func prepareAction(ctx context.Context, tx pgx.Tx, record *storedRecord, institution, action string, now time.Time) error {
	switch action {
	case "submit":
		return prepareSubmission(ctx, tx, record, institution, now)
	case "withdraw":
		_, err := lockScopedFiling(ctx, tx, record.FilingID, "", false)
		return err
	}
	return nil
}

// checkInstitutionAction 限定机构端可用的提交和撤销命令。
// 参数 record 为已锁定报备，operator 为服务端确定的机构经办标记，action 为提交的命令。
// 返回值 超出机构办理范围时的领域错误。
func checkInstitutionAction(record storedRecord, operator, action string) error {
	switch action {
	case "submit":
		if !canInstitutionEdit(record, operator) {
			return errConflict
		}
	case "withdraw":
		// 状态、发起人和撤销期限由统一状态机校验。
	default:
		return errInvalid
	}
	return nil
}

// prepareSubmission 在备案行锁下核对每次提交冷却，并在首次提交时占用额度。
// 参数 ctx 控制查询，tx 为当前事务，record 为待提交记录，institution 为机构范围，now 为提交时刻。
// 返回值 备案失效、冷却未结束、年度额度用尽或查询错误。
func prepareSubmission(ctx context.Context, tx pgx.Tx, record *storedRecord, institution string, now time.Time) error {
	first := record.Status == draft
	operator := operatorOrganization(ctx) != ""
	if !first {
		institution = ""
	}
	filing, err := lockScopedFiling(ctx, tx, record.FilingID, institution, first || operator)
	if err != nil {
		return err
	}
	available, err := readResubmitAvailableAt(ctx, tx, record.FilingID, now)
	if err != nil {
		return err
	}
	if available != nil {
		return errCooldown
	}
	if operator {
		if err := validateOperatorChange(record.Type, record.AssessmentOrg, filing); err != nil {
			return err
		}
	}
	if !first || record.Type != change {
		return nil
	}
	year := quotaYear(now)
	used, err := countChanges(ctx, tx, record.FilingID, year)
	if err != nil {
		return err
	}
	if used >= quotaLimit {
		return errQuota
	}
	record.QuotaYear = &year
	return nil
}

// updateStatus 按动作更新状态、版本和报备的相关时间。
// 参数 ctx 控制写入，tx 为事务，record 为旧记录，action 和 next 为转移，now 为操作时刻。
// 返回值 更新结果或数据库错误。
func updateStatus(ctx context.Context, tx pgx.Tx, record storedRecord, action, next string, now time.Time) (mutation, error) {
	var result mutation
	err := tx.QueryRow(ctx, `UPDATE implementation_reports SET status=$2, version=version+1, quota_year=$3,
	  submitted_at=CASE WHEN $4='submit' THEN $5::timestamptz ELSE submitted_at END,
	  accepted_at=CASE WHEN $4='accept' THEN COALESCE(accepted_at,$5::timestamptz) ELSE accepted_at END,
	  archived_at=CASE WHEN $4='archive' THEN $5::timestamptz ELSE archived_at END
	WHERE id=$1 RETURNING id::text, status, version, archived_at`, record.ID, next, record.QuotaYear, action, now).
		Scan(&result.ID, &result.Status, &result.Version, &result.ArchivedAt)
	return result, err
}

// finish 同事务保存实施事件及其触发的审核意见，再提交报备写入。
// 参数 ctx 控制事务，tx 为当前事务，result、previous 为新旧记录，change、operator、now 为事件信息。
// 返回值 已提交的写入结果，或回滚前的错误。
func finish(ctx context.Context, tx pgx.Tx, result mutation,
	previous storedRecord, change command, operator string, now time.Time) (mutation, error) {
	_, err := tx.Exec(ctx, `INSERT INTO implementation_events
	 (report_id,version,action,from_status,to_status,operator,comment,occurred_at,directory_snapshot)
	 SELECT id,$2,$3,$4,$5,$6,$7,$8,directory_snapshot FROM implementation_reports WHERE id=$1`,
		result.ID, result.Version, change.Action, previous.Status,
		result.Status, operator, change.Comment, now)
	if err != nil {
		return mutation{}, err
	}
	if change.Action == "return_for_correction" || (change.Action == "approve" && result.Status == approved) {
		if err := deliverImplementationNotice(ctx, tx, previous, result.Status, result.Version, change.Comment); err != nil {
			return mutation{}, err
		}
		if operatorInitiated(previous) {
			if err := deliverOperatorImplementationReview(ctx, tx, previous, result, change.Comment); err != nil {
				return mutation{}, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return mutation{}, err
	}
	return result, nil
}

// deliverImplementationNotice 将审核事件送达报备登记的测评机构。
// 参数 ctx 控制写入，tx 为报备事务，record 为归属快照，state、version、comment 描述审核事件。
// 返回值 写入站内意见失败时的错误。
func deliverImplementationNotice(ctx context.Context, tx pgx.Tx, record storedRecord,
	state string, version int64, comment string) error {
	return reviewinbox.Deliver(ctx, tx, reviewinbox.Notice{
		Audience: "institution", Resource: "implementations", ResourceID: record.ID,
		State: state, Version: version, Comment: comment,
		Recipient: record.AssessmentOrg, Reference: record.FilingNumber, Subject: record.SystemName,
	})
}

// republishImplementationNotice 把当前退回意见重新送达给改派后的实施机构。
// 参数 ctx 控制查询和写入，tx 为改派事务，record 为更新了接收方的报备快照。
// 返回值 实际查询或重新送达失败时的错误。
func republishImplementationNotice(ctx context.Context, tx pgx.Tx, record storedRecord) error {
	var version int64
	var comment string
	err := tx.QueryRow(ctx, `SELECT version,comment FROM implementation_events
	 WHERE report_id=$1 AND action='return_for_correction' AND to_status='returned'
	 ORDER BY version DESC LIMIT 1`, record.ID).Scan(&version, &comment)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return deliverImplementationNotice(ctx, tx, record, returned, version, comment)
}

// lockActiveFiling 获取有效备案并与该备案的提交或注销操作串行。
// 参数 ctx 控制查询，tx 为当前事务，id 为备案标识，institution 为可选机构归属。
// 返回值 备案的只读字段或不可关联错误。
func lockActiveFiling(ctx context.Context, tx pgx.Tx, id, institution string) (choice, error) {
	return lockScopedFiling(ctx, tx, id, institution, true)
}

// lockScopedFiling 按经办范围锁住当前备案，供报备事务检查其归属及有效性。
// 参数 ctx 控制查询并携带运营者，tx 为事务，id、institution 为备案及机构，active 为是否要求备案仍有效。
// 返回值 备案的当前可用字段或不可关联错误。
func lockScopedFiling(ctx context.Context, tx pgx.Tx, id, institution string, active bool) (choice, error) {
	var result choice
	err := tx.QueryRow(ctx, `SELECT id::text, number, system_name, organization_name, assessment_org FROM business_cases
	 WHERE id=$1 AND kind='filing' AND ($2::text='' OR assessment_org COLLATE "C" = $2)
	 AND ($3::text='' OR organization_name COLLATE "C" = $3)
	 AND (NOT $4::bool OR (status='approved' AND cancelled_at IS NULL)) FOR UPDATE`,
		id, institution, operatorOrganization(ctx), active).
		Scan(&result.ID, &result.Number, &result.SystemName, &result.OrganizationName, &result.AssessmentOrg)
	if errors.Is(err, pgx.ErrNoRows) {
		return choice{}, errFiling
	}
	return result, err
}

// fallbackOrg 在表单未指定测评机构时采用备案已有机构。
// 参数 selected 为表单字段，previous 为已保存的机构名称。
// 返回值 应保存到实施报备的测评机构。
func fallbackOrg(selected, previous string) string {
	if selected != "" {
		return selected
	}
	return previous
}
