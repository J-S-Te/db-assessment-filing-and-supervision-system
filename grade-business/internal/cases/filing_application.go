package cases

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"grade-business/internal/access"
	"grade-business/internal/filingpolicy"
	"grade-business/internal/reviewinbox"
)

const (
	applicationChange    = "change"
	applicationCancel    = "cancel"
	withdrawApplication  = "withdraw"
	applicationWithdrawn = "withdrawn"
)

var (
	// ErrFilingInactive 表示备案已注销，无法发起需要有效备案的业务。
	ErrFilingInactive = errors.New("filing is inactive")
	// ErrActiveApplication 表示同一备案已有未办结的变更或注销申请。
	ErrActiveApplication = errors.New("filing already has an active application")
)

type filingValues struct {
	SystemName       string `json:"systemName"`
	OrganizationName string `json:"organizationName"`
	AssessmentOrg    string `json:"assessmentOrg"`
	Level            string `json:"level"`
}

type applicationChanges struct {
	SystemName       *string `json:"systemName"`
	OrganizationName *string `json:"organizationName"`
	AssessmentOrg    *string `json:"assessmentOrg"`
	Level            *string `json:"level"`
}

type applicationForm struct {
	Reason  string              `json:"reason"`
	Changes *applicationChanges `json:"changes"`
}

type applicationRow struct {
	ID                    string    `json:"id"`
	FilingID              string    `json:"filingId"`
	FilingNumber          string    `json:"filingNumber"`
	SystemName            string    `json:"systemName"`
	OrganizationName      string    `json:"organizationName"`
	Type                  string    `json:"type"`
	Status                string    `json:"status"`
	CreatedAt             time.Time `json:"createdAt"`
	InitialReviewAudience string    `json:"initialReviewAudience,omitempty"`
}

type applicationDetail struct {
	applicationRow
	Reason               string              `json:"reason"`
	Changes              []applicationDiff   `json:"changes,omitempty"`
	Version              int64               `json:"version"`
	AllowedActions       []string            `json:"allowedActions"`
	AppliedAt            *time.Time          `json:"appliedAt,omitempty"`
	ImplementationID     *string             `json:"implementationId,omitempty"`
	RequiredMaterials    []string            `json:"requiredMaterials,omitempty"`
	PolicyMaterials      []filingpolicy.Rule `json:"policyMaterials,omitempty"`
	ReceiptAvailable     *bool               `json:"receiptAvailable,omitempty"`
	RetestImplementation *applicationRetest  `json:"retestImplementation,omitempty"`
}

type applicationDiff struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type applicationState struct {
	filingID, kind, status string
	number                 string
	version, filingVersion int64
	before, after          filingValues
	operatorOrigin         bool
	implementationID       *string
}

type applicationFiling struct {
	number  string
	version int64
	values  filingValues
}

// proposal 校验申请类型和理由并计算完整生效快照。
// 参数 kind 为申请类型，before 为原备案快照，form 为录入内容。
// 返回值 修改后的备案快照、规范化理由及校验错误。
func proposal(kind string, before filingValues, form applicationForm) (filingValues, string, error) {
	reason := strings.TrimSpace(form.Reason)
	if !validText(reason, 1000) {
		return filingValues{}, "", ErrInvalid
	}
	if kind == applicationCancel && form.Changes == nil {
		return before, reason, nil
	}
	if kind != applicationChange || form.Changes == nil {
		return filingValues{}, "", ErrInvalid
	}
	after := before
	changes := form.Changes
	for _, change := range []struct{ value, target *string }{
		{changes.SystemName, &after.SystemName}, {changes.OrganizationName, &after.OrganizationName},
		{changes.AssessmentOrg, &after.AssessmentOrg},
	} {
		if change.value == nil {
			continue
		}
		value := strings.TrimSpace(*change.value)
		if !validText(value, 200) {
			return filingValues{}, "", ErrInvalid
		}
		*change.target = value
	}
	if changes.Level != nil {
		after.Level = *changes.Level
	}
	if after == before || len(after.Level) != 1 || !strings.Contains("12345", after.Level) {
		return filingValues{}, "", ErrInvalid
	}
	return after, reason, nil
}

// applicationDiffs 摘取原备案与申请方案之间实际变化的字段。
// 参数 before 和 after 为原始和拟生效的完整备案快照。
// 返回值 按界面字段顺序排列的变化明细。
func applicationDiffs(before, after filingValues) []applicationDiff {
	var result []applicationDiff
	for _, change := range []applicationDiff{
		{"systemName", before.SystemName, after.SystemName},
		{"organizationName", before.OrganizationName, after.OrganizationName},
		{"assessmentOrg", before.AssessmentOrg, after.AssessmentOrg},
		{"level", before.Level, after.Level},
	} {
		if change.Before != change.After {
			result = append(result, change)
		}
	}
	return result
}

// lockApplicationFiling 锁定已通过且有效的主备案，为发起与审核建立统一加锁顺序。
// 参数 ctx 含可选运营单位，tx 为持久化事务，id 为主备案标识，institution 为可选机构范围。
// 返回值 锁定的备案快照，或不存在、备案失效及查询错误。
func lockApplicationFiling(ctx context.Context, tx pgx.Tx, id, institution string) (applicationFiling, error) {
	var source applicationFiling
	var status string
	var cancelled *time.Time
	fields := &source.values
	err := tx.QueryRow(ctx, `SELECT number,version,status,cancelled_at,
		system_name,organization_name,assessment_org,level
		FROM business_cases WHERE kind='filing' AND id=$1 AND discarded_at IS NULL
		AND ($2='' OR assessment_org=$2)
		AND ($3='' OR organization_name=$3) FOR UPDATE`,
		id, institution, applicationOrganization(ctx)).Scan(
		&source.number, &source.version, &status, &cancelled,
		&fields.SystemName, &fields.OrganizationName, &fields.AssessmentOrg, &fields.Level)
	if errors.Is(err, pgx.ErrNoRows) {
		return applicationFiling{}, ErrNotFound
	}
	if err == nil && (status != approved || cancelled != nil) {
		return applicationFiling{}, ErrFilingInactive
	}
	return source, err
}

// createApplication 锁定有效备案并在数据库唯一约束下保存申请草稿。
// 参数 ctx 含可选运营单位，filingID、kind 为关联备案和申请类型，form 为申请表单，institution 为可选发起机构。
// 返回值 申请标识与版本，或校验及写入错误。
func (s *Service) createApplication(ctx context.Context, filingID, kind string, form applicationForm, institution string) (mutation, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return mutation{}, err
	}
	defer tx.Rollback(ctx)
	source, err := lockApplicationFiling(ctx, tx, filingID, institution)
	if err != nil {
		return mutation{}, err
	}
	operatorOrigin := applicationOrganization(ctx) != ""
	after, reason, err := scopedProposal(kind, source.values, form, operatorOrigin)
	if err != nil {
		return mutation{}, err
	}
	row := tx.QueryRow(ctx, `INSERT INTO filing_applications
		(filing_id,application_type,filing_number,filing_version,reason,before_values,after_values,operator_origin)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text,status,version`,
		filingID, kind, source.number, source.version, reason, source.values, after, operatorOrigin)
	result, err := scanMutation(row)
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.ConstraintName == "filing_applications_one_active" {
		return mutation{}, ErrActiveApplication
	}
	if err != nil {
		return mutation{}, err
	}
	return result, tx.Commit(ctx)
}

// scopedProposal 根据申请来源限制运营者自助变更组织所有权。
// 参数 kind、before 和 form 指定申请类型及前后值，restrictOrganization 表示是否保持原运营单位。
// 返回值 有效快照、规范化理由及业务校验结果。
func scopedProposal(kind string, before filingValues, form applicationForm, restrictOrganization bool) (filingValues, string, error) {
	after, reason, err := proposal(kind, before, form)
	if err == nil && restrictOrganization && after.OrganizationName != before.OrganizationName {
		return filingValues{}, "", ErrInvalid
	}
	return after, reason, err
}

// lockApplication 按主备案、申请的顺序锁定两条记录并核对申请人权限。
// 参数 ctx 含可选运营单位，tx、id、expected 指定事务、申请和预期版本，institution 为可选原发起机构。
// 返回值 已锁定申请的状态与快照，或不存在、状态冲突及查询错误。
func lockApplication(ctx context.Context, tx pgx.Tx, id string, expected int64, institution string) (applicationState, error) {
	app, err := lockApplicationInScope(ctx, tx, id, expected, institution)
	if err == nil && institution != "" && (app.operatorOrigin || !editable(app.status)) {
		return applicationState{}, ErrConflict
	}
	return app, err
}

// lockApplicationInScope 为申请与审核锁定相同记录并核对版本及双方归属。
// 参数 ctx 含运营单位，tx、id、expected 为事务、申请及预期版本，institution 为可选原机构。
// 返回值 持锁的申请及前后快照，或不存在、版本冲突及读取错误。
func lockApplicationInScope(ctx context.Context, tx pgx.Tx, id string, expected int64,
	institution string,
) (applicationState, error) {
	if !validID(id) {
		return applicationState{}, ErrNotFound
	}
	var app applicationState
	err := tx.QueryRow(ctx, `SELECT filing_id::text,filing_number FROM filing_applications
		WHERE id=$1 AND ($2='' OR before_values->>'assessmentOrg'=$2)
		AND ($3='' OR before_values->>'organizationName'=$3)`, id, institution, applicationOrganization(ctx)).
		Scan(&app.filingID, &app.number)
	if errors.Is(err, pgx.ErrNoRows) {
		return applicationState{}, ErrNotFound
	}
	if err != nil {
		return applicationState{}, err
	}
	filing, err := lockApplicationFiling(ctx, tx, app.filingID, institution)
	if err != nil {
		return applicationState{}, err
	}
	row := tx.QueryRow(ctx, `SELECT application_type,status,version,filing_version,before_values,after_values,
		operator_origin,implementation_id::text
		FROM filing_applications WHERE id=$1 AND ($2='' OR before_values->>'assessmentOrg'=$2)
		AND ($3='' OR before_values->>'organizationName'=$3) FOR UPDATE`,
		id, institution, applicationOrganization(ctx))
	if err = row.Scan(&app.kind, &app.status, &app.version, &app.filingVersion, &app.before, &app.after,
		&app.operatorOrigin, &app.implementationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return applicationState{}, ErrNotFound
		}
		return applicationState{}, err
	}
	if (expected != 0 && app.version != expected) || app.filingVersion != filing.version {
		return applicationState{}, ErrConflict
	}
	return app, nil
}

// updateApplication 修改仍可编辑的申请内容并保留原备案快照。
// 参数 ctx、id、expected 与 form 指定申请和新内容，institution 为可选原发起机构。
// 返回值 更新后的申请状态和版本，或校验及写入错误。
func (s *Service) updateApplication(ctx context.Context, id string, expected int64,
	form applicationForm, institution string,
) (mutation, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return mutation{}, err
	}
	defer tx.Rollback(ctx)
	app, err := lockApplication(ctx, tx, id, expected, institution)
	if err != nil {
		return mutation{}, err
	}
	if !editable(app.status) {
		return mutation{}, ErrConflict
	}
	restricted := app.operatorOrigin || applicationOrganization(ctx) != ""
	after, reason, err := scopedProposal(app.kind, app.before, form, restricted)
	if err != nil {
		return mutation{}, err
	}
	if after.AssessmentOrg != app.after.AssessmentOrg {
		app.implementationID = nil
	}
	result, err := scanMutation(tx.QueryRow(ctx, `UPDATE filing_applications SET
		reason=$2,after_values=$3,version=version+1,implementation_id=$4 WHERE id=$1
		RETURNING id::text,status,version`, id, reason, after, app.implementationID))
	if err != nil {
		return mutation{}, err
	}
	return result, tx.Commit(ctx)
}

// applicationTransition 计算备案申请的审核或撤回状态变更。
// 参数 status 为当前申请状态，action 为操作，comment 为规范化后的意见。
// 返回值 下一状态，或非法输入及状态冲突。
func applicationTransition(status, action, comment string) (string, error) {
	if action != withdrawApplication {
		return transition(status, action, comment, true)
	}
	if !validReviewComment(comment) {
		return "", ErrInvalid
	}
	if !editable(status) {
		return "", ErrConflict
	}
	return applicationWithdrawn, nil
}

// changeApplicationStatus 审核申请，并在终审时原子更新主备案与留痕。
// 参数 ctx 含可选运营单位，id 为申请标识，input 为审核动作、意见及客户端版本。
// 返回值 申请变更结果，或业务状态和写入错误。
func (s *Service) changeApplicationStatus(ctx context.Context, id string, input reviewInput) (mutation, error) {
	if input.FilingNumber != "" {
		return mutation{}, ErrInvalid
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return mutation{}, err
	}
	defer tx.Rollback(ctx)
	app, err := lockApplicationStatus(ctx, tx, id, input)
	if err != nil {
		return mutation{}, err
	}
	if err := requireSeparateReviewer(ctx, tx, id, app.status, true); err != nil {
		return mutation{}, err
	}
	if err := guardWithdrawalReview(ctx, tx, withdrawalApplications, id); err != nil {
		return mutation{}, err
	}
	comment := strings.TrimSpace(input.Comment)
	next, err := applicationTransition(app.status, input.Decision, comment)
	if err != nil {
		return mutation{}, err
	}
	if err := checkApplicationEvidence(ctx, tx, id, &app, input.Decision); err != nil {
		return mutation{}, err
	}
	if next == approved {
		if err := applyFilingChange(ctx, tx, app); err != nil {
			return mutation{}, err
		}
	}
	operator := input.reviewActor(ctx, s.operator, applicationOrganization(ctx))
	result, err := s.persistApplicationDecision(ctx, tx, id, app, next, input.Decision, comment, operator)
	if err != nil {
		return mutation{}, err
	}
	if err := deliverApplicationReview(ctx, tx, result, app, comment); err != nil {
		return mutation{}, err
	}
	return result, tx.Commit(ctx)
}

// lockApplicationStatus 按审核席位或发起人选择申请锁定检查，并校验阶段权限。
// 参数 ctx 与 tx 为本次申请事务，id 为申请标识，input 为决定、范围和预期版本。
// 返回值 已持锁且满足所需角色的申请，或归属、状态及读取错误。
func lockApplicationStatus(ctx context.Context, tx pgx.Tx, id string, input reviewInput) (applicationState, error) {
	var app applicationState
	var err error
	if input.institutionReviewer != "" {
		app, err = lockApplicationInScope(ctx, tx, id, input.ExpectedVersion, input.institutionReviewer)
	} else {
		app, err = lockApplication(ctx, tx, id, input.ExpectedVersion, input.institution)
	}
	if err != nil {
		return applicationState{}, err
	}
	return app, input.checkReviewRole(ctx, app.status, app.operatorOrigin)
}

// deliverApplicationReview 按原备案快照向机构和运营者原子送达办理通知。
// 参数 ctx 为请求上下文，tx 为审核事务，result、app 和 comment 分别为状态、归属与意见。
// 返回值 两端意见保存成功时为 nil，失败时由调用方回滚审核。
func deliverApplicationReview(ctx context.Context, tx pgx.Tx, result mutation, app applicationState, comment string) error {
	terminal := result.Status == returned || result.Status == approved
	if !terminal && (!app.operatorOrigin || (result.Status != pendingInitial && result.Status != pendingFinal)) {
		return nil
	}
	notice := reviewinbox.Notice{Audience: institutionReviewAudience, Resource: "filing-applications",
		ResourceID: result.ID, Version: result.Version, State: result.Status, Recipient: app.before.AssessmentOrg,
		Reference: app.number, Subject: app.before.SystemName, Comment: comment}
	if terminal || result.Status == pendingInitial {
		if err := reviewinbox.Deliver(ctx, tx, notice); err != nil {
			return err
		}
	}
	if result.Status == pendingInitial {
		return nil
	}
	notice.Audience, notice.Recipient = "operator", app.before.OrganizationName
	return reviewinbox.Deliver(ctx, tx, notice)
}

// applyFilingChange 更新主备案字段或标记注销，并保持原始编号和审核状态。
// 参数 ctx 为请求上下文，tx 为主备案已锁定的事务，app 为最终申请方案。
// 返回值 并发变化或写入错误，生效时返回 nil。
func applyFilingChange(ctx context.Context, tx pgx.Tx, app applicationState) error {
	values := app.after
	result, err := tx.Exec(ctx, `UPDATE business_cases
		SET system_name=$3,organization_name=$4,assessment_org=$5,level=$6,version=version+1,
		cancelled_at=CASE WHEN $7='cancel' THEN transaction_timestamp() ELSE NULL END
		WHERE id=$1 AND version=$2 AND kind='filing' AND status='approved' AND cancelled_at IS NULL`,
		app.filingID, app.filingVersion, values.SystemName, values.OrganizationName,
		values.AssessmentOrg, values.Level, app.kind)
	if err == nil && result.RowsAffected() != 1 {
		return ErrConflict
	}
	return err
}

// persistApplicationDecision 将申请状态与审核事件记入同一事务。
// 参数 ctx、tx、id 指定事务与申请，app 含原状态与复测绑定，next、action、comment、operator 说明审核操作。
// 返回值 新状态与版本，或写入错误。
func (s *Service) persistApplicationDecision(ctx context.Context, tx pgx.Tx,
	id string, app applicationState, next, action, comment, operator string) (mutation, error) {
	result, err := scanMutation(tx.QueryRow(ctx, `UPDATE filing_applications
		SET status=$2,version=version+1,implementation_id=$3,
		    applied_at=CASE WHEN $2='approved' THEN transaction_timestamp() ELSE NULL END
		WHERE id=$1 RETURNING id::text,status,version`, id, next, app.implementationID))
	if err != nil {
		var failure *pgconn.PgError
		if errors.As(err, &failure) && failure.ConstraintName == "filing_applications_implementation_once" {
			return mutation{}, errApplicationRetest
		}
		return mutation{}, err
	}
	actor, _ := access.Current(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO filing_application_review_events
		(application_id,action,from_status,to_status,operator,comment,version,implementation_id,operator_id,operator_role)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,'')::uuid,NULLIF($10,''))`, id, action, app.status, next, operator, comment, result.Version, app.implementationID, actor.ID, actor.Role)
	return result, err
}
