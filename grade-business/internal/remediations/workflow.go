package remediations

import (
	"context"
	"errors"

	"grade-business/internal/hazards"
	"grade-business/internal/reviewinbox"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// submit 校验关联、材料和版本后提交整改规划，并在同一事务登记历史。
// 参数 ctx 含企业端可选范围，id 为整改规划标识，input 为用户读取的版本。
// 返回值 待审核的新版本，或状态、材料和持久化错误。
func (s *Service) submit(ctx context.Context, id string, input submitInput) (mutation, error) {
	out := mutation{ID: id}
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		current, err := lockPlan(ctx, tx, id)
		if err != nil {
			return err
		}
		if current.Version != input.ExpectedVersion || !editable(current.Status) {
			return errConflict
		}
		return s.recordSubmission(ctx, tx, &out, current)
	})
	return out, err
}

// recordSubmission 执行锁定计划的必要条件检查和提交流程。
// 参数 ctx 为请求上下文，tx 为事务，out 为结果，current 为锁定计划。
// 返回值 已登记状态返回 nil，或有效报告、材料与持久化错误。
func (s *Service) recordSubmission(ctx context.Context, tx pgx.Tx, out *mutation, current lockedPlan) error {
	if current.Submission == 0 {
		if err := lockEligible(ctx, tx, current.ReportID); err != nil {
			return err
		}
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(DISTINCT kind) FROM remediation_attachments
		WHERE plan_id=$1 AND kind IN ('plan','responsibility','schedule')`, out.ID).Scan(&count); err != nil {
		return err
	}
	if count != 3 {
		return errMaterials
	}
	err := tx.QueryRow(ctx, `UPDATE remediation_plans SET status='pending_review',
		version=version+1,submission=submission+1,submitted_at=clock_timestamp()
		WHERE id=$1 RETURNING status,version`, out.ID).Scan(&out.Status, &out.Version)
	if err != nil {
		var postgres *pgconn.PgError
		if errors.As(err, &postgres) && postgres.Code == "23505" &&
			postgres.ConstraintName == "remediation_filing_in_review" {
			return errFilingInReview
		}
		return err
	}
	return s.saveEvent(ctx, tx, *out, current, reviewInput{
		Decision: "submit", Deadline: current.Deadline,
	}, current.Submission+1)
}

// withdraw 将当前单位仍待审核的方案撤回为终态，并在同一事务写入审核轨迹。
// 参数 ctx 含当前运营单位范围，id 为规划标识，expected 为页面读取的版本。
// 返回值 撤回后的状态与版本，或归属、状态和持久化错误。
func (s *Service) withdraw(ctx context.Context, id string, expected int64) (mutation, error) {
	out := mutation{ID: id, Status: withdrawn}
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		current, err := lockPlan(ctx, tx, id)
		if err != nil {
			return err
		}
		if current.Version != expected || current.Status != pendingReview {
			return errConflict
		}
		err = tx.QueryRow(ctx, `UPDATE remediation_plans SET status='withdrawn',version=version+1
			WHERE id=$1 RETURNING version`, id).Scan(&out.Version)
		if err != nil {
			return err
		}
		return s.saveEvent(ctx, tx, out, current, reviewInput{Decision: "withdraw", Deadline: current.Deadline}, current.Submission)
	})
	return out, err
}

// review 审核待处理规划，并原子保存审核意见和整改期限。
// 参数 ctx 为请求上下文，id 为整改规划标识，input 为审核录入内容及预期版本。
// 返回值 新审核状态与版本，或状态冲突、期限和持久化错误。
func (s *Service) review(ctx context.Context, id string, input reviewInput) (mutation, error) {
	out := mutation{ID: id, Status: returned}
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		current, err := lockPlan(ctx, tx, id)
		if err != nil {
			return err
		}
		if current.Version != input.ExpectedVersion || current.Status != pendingReview {
			return errConflict
		}
		return s.recordReview(ctx, tx, &out, current, input)
	})
	return out, err
}

// recordReview 核对修正期限，并原子保存结论、留痕、企业反馈和通过后产生的隐患。
// 参数 ctx 为请求上下文，tx 为事务，out 为更新结果，current 为原始状态，input 为结论。
// 返回值 结论和历史均写入后为 nil，或期限及数据库错误。
func (s *Service) recordReview(ctx context.Context, tx pgx.Tx, out *mutation, current lockedPlan, input reviewInput) error {
	deadline := current.Deadline
	if input.Decision == "approve" {
		var invalid bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM remediation_plans p,
			jsonb_array_elements(p.risks) risk WHERE p.id=$1 AND risk->>'deadline' > $2)`,
			out.ID, input.Deadline).Scan(&invalid)
		if err != nil {
			return err
		}
		if invalid {
			return errRiskDueDate
		}
		out.Status, deadline = approved, input.Deadline
	}
	err := tx.QueryRow(ctx, `UPDATE remediation_plans SET status=$2,deadline=$3::text::date,
		version=version+1 WHERE id=$1 RETURNING version`, out.ID, out.Status, deadline).
		Scan(&out.Version)
	if err != nil {
		return err
	}
	if err := s.saveEvent(ctx, tx, *out, current, input, current.Submission); err != nil {
		return err
	}
	if err := deliverReview(ctx, tx, *out, input); err != nil {
		return err
	}
	if out.Status == approved {
		_, err := hazards.SyncApproved(ctx, tx, out.ID)
		return err
	}
	return nil
}

// deliverReview 将审核结论和可执行的退回意见送达当前备案单位。
// 参数 ctx 为请求上下文，tx 为审核事务，out 为新版本和状态，input 为监管意见和退回原因。
// 返回值 查询或登记错误，反馈已登记时返回 nil。
func deliverReview(ctx context.Context, tx pgx.Tx, out mutation, input reviewInput) error {
	notice := reviewinbox.Notice{Audience: "operator", Resource: "remediations", ResourceID: out.ID,
		State: out.Status, Version: out.Version, Comment: input.Comment}
	if input.Reason != "" {
		notice.Comment = input.Reason + "\n" + input.Comment
	}
	err := tx.QueryRow(ctx, `SELECT f.organization_name,r.number,f.system_name `+joins+`
		WHERE p.id=$1 FOR SHARE OF r,f`, out.ID).Scan(&notice.Recipient, &notice.Reference, &notice.Subject)
	if err != nil {
		return err
	}
	return reviewinbox.Deliver(ctx, tx, notice)
}

// saveEvent 保留操作人、决定、意见、退回原因和期限的单次有效记录。
// 参数 ctx 含企业端可选范围，tx 为事务，out 和 current 为前后状态，input 为决定，submission 为轮次。
// 返回值 事件保存结果，失败时调用方必须回滚业务更新。
func (s *Service) saveEvent(ctx context.Context, tx pgx.Tx, out mutation, current lockedPlan, input reviewInput, submission int) error {
	operator := s.operator
	if organization := operatorOrganization(ctx); organization != "" {
		operator = organization
	}
	_, err := tx.Exec(ctx, `INSERT INTO remediation_review_events
		(plan_id,action,from_status,to_status,operator,comment,reason,deadline,submission,version)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8::text::date,$9,$10)`,
		out.ID, input.Decision, current.Status, out.Status, operator, input.Comment,
		input.Reason, input.Deadline, submission, out.Version)
	return err
}
