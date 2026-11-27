package cases

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// readWithdrawal 在一致快照中读取当前回执和最近的撤回处理记录。
// 参数 ctx 控制读取，scope 指定业务和可见范围。
// 返回值 可办理动作与留痕，或记录范围及查询错误。
func (s *Service) readWithdrawal(ctx context.Context, scope withdrawalScope) (withdrawalView, error) {
	var view withdrawalView
	err := pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly},
		func(tx pgx.Tx) error {
			target, err := readWithdrawalTarget(ctx, tx, scope, false)
			if err != nil {
				return err
			}
			items, err := latestWithdrawals(ctx, tx, scope)
			if err != nil {
				return err
			}
			pending := len(items) != 0 && items[0].Status == withdrawalPending
			if len(items) != 0 {
				view.Request, view.Previous = &items[0], items[1:]
			}
			view.CanRequest = scope.organization != "" && !pending && eligibleWithdrawalStage(target)
			view.CanDecide = scope.organization == "" && scope.institution == "" && pending
			return nil
		})
	return view, err
}

// latestWithdrawals 读取有限且按申请版本排序的撤回留痕。
// 参数 ctx、tx 控制事务，scope 为已核对归属的单据。
// 返回值 由新到旧的记录，或查询错误。
func latestWithdrawals(ctx context.Context, tx pgx.Tx, scope withdrawalScope) ([]withdrawalRecord, error) {
	rows, err := tx.Query(ctx, `SELECT `+withdrawalFields+` FROM filing_withdrawal_requests
		WHERE resource=$1 AND resource_id=$2 ORDER BY requested_from_version DESC LIMIT 20`,
		scope.resource, scope.id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (withdrawalRecord, error) {
		return scanWithdrawal(row)
	})
}

// requestWithdrawal 开启一次在审撤回，并锁定原审核分支。
// 参数 ctx 控制写入，scope 绑定主管单位，input 提供原版本和原因。
// 返回值 实际保存或幂等复用的回执，或状态及持久化错误。
func (s *Service) requestWithdrawal(ctx context.Context, scope withdrawalScope,
	input withdrawalChange,
) (withdrawalView, error) {
	var item withdrawalRecord
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		target, err := lockWithdrawalTarget(ctx, tx, scope)
		if err != nil {
			return err
		}
		item, err = withdrawalByVersion(ctx, tx, scope, input.ExpectedVersion)
		if err == nil {
			if item.Reason != input.Reason {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if !eligibleWithdrawalStage(target) || target.version != input.ExpectedVersion {
			return ErrConflict
		}
		if err := guardWithdrawalReview(ctx, tx, scope.resource, scope.id); err != nil {
			return err
		}
		item, err = createWithdrawal(ctx, tx, scope, target, input.Reason)
		return err
	})
	return withdrawalView{Request: &item}, err
}

// createWithdrawal 持久化撤回申请、并发版本及原接收方消息。
// 参数 ctx、tx 为事务，scope 和 target 指定记录及当前状态，reason 为已验证理由。
// 返回值 撤回回执或写入错误。
func createWithdrawal(ctx context.Context, tx pgx.Tx, scope withdrawalScope,
	target withdrawalTarget, reason string,
) (withdrawalRecord, error) {
	var filingID, applicationID *string
	if scope.resource == withdrawalFilings {
		filingID = &scope.id
	} else {
		applicationID = &scope.id
	}
	row := tx.QueryRow(ctx, `INSERT INTO filing_withdrawal_requests
		(resource,filing_id,application_id,requested_from_version,case_version,reason)
		VALUES($1,$2,$3,$4,($4::bigint+1),$5) RETURNING `+withdrawalFields,
		scope.resource, filingID, applicationID, target.version, reason)
	item, err := scanWithdrawal(row)
	if err != nil {
		return withdrawalRecord{}, err
	}
	if err := updateWithdrawalTarget(ctx, tx, scope, target, target.status, ""); err != nil {
		return withdrawalRecord{}, err
	}
	return item, notifyWithdrawal(ctx, tx, scope, target, item)
}

// decideWithdrawal 按来件版本确认或驳回撤回申请。
// 参数 ctx 控制事务，scope 指定监管办理单据，input 包含撤回编号、版本、决定和意见。
// 返回值 已保存的回执，或过时、缺失和写入错误。
func (s *Service) decideWithdrawal(ctx context.Context, scope withdrawalScope,
	input withdrawalDecision,
) (withdrawalView, error) {
	var item withdrawalRecord
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		target, err := lockWithdrawalTarget(ctx, tx, scope)
		if err != nil {
			return err
		}
		row := tx.QueryRow(ctx, `SELECT `+withdrawalFields+` FROM filing_withdrawal_requests
			WHERE id=$1 AND resource=$2 AND resource_id=$3`, input.WithdrawalID, scope.resource, scope.id)
		item, err = scanWithdrawal(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return s.applyWithdrawalDecision(ctx, tx, scope, target, &item, input)
	})
	return withdrawalView{Request: &item}, err
}

// applyWithdrawalDecision 校验决定是否重放并一次性保存状态、回执及收件通知。
// 参数 ctx、tx 为事务，scope、target、item 指定来件和锁定状态，input 给出所选决定。
// 返回值 确认事务可提交为 nil，或状态及数据库错误。
func (s *Service) applyWithdrawalDecision(ctx context.Context, tx pgx.Tx, scope withdrawalScope,
	target withdrawalTarget, item *withdrawalRecord, input withdrawalDecision,
) error {
	state, next := withdrawalRejected, target.status
	if input.Decision == approve {
		state, next = approved, applicationWithdrawn
	}
	if item.Status != withdrawalPending {
		if item.Status == state && item.Comment != nil && *item.Comment == input.Comment &&
			item.CaseVersion-1 == input.ExpectedVersion {
			return nil
		}
		return ErrConflict
	}
	if target.version != item.CaseVersion || item.CaseVersion != input.ExpectedVersion ||
		!eligibleWithdrawalStage(target) {
		return ErrConflict
	}
	confirmed, err := scanWithdrawal(tx.QueryRow(ctx, `UPDATE filing_withdrawal_requests
		SET status=$2,case_version=case_version+1,comment=$3,decided_by=$4,decided_at=clock_timestamp()
		WHERE id=$1 RETURNING `+withdrawalFields, item.ID, state, input.Comment, s.operator))
	if err != nil {
		return err
	}
	if err := updateWithdrawalTarget(ctx, tx, scope, target, next, s.operator); err != nil {
		return err
	}
	*item = confirmed
	return notifyWithdrawal(ctx, tx, scope, target, confirmed)
}

// updateWithdrawalTarget 保留既有审核阶段或标记已撤回并记录真实业务决定。
// 参数 ctx、tx 为事务，scope、target 指定原单，next 为下一状态，actor 为确认人。
// 返回值 已写入对象与必要审核事件时为 nil，或数据库错误。
func updateWithdrawalTarget(ctx context.Context, tx pgx.Tx, scope withdrawalScope,
	target withdrawalTarget, next, actor string,
) error {
	query := `UPDATE business_cases SET status=$2,version=version+1 WHERE id=$1`
	if scope.resource == withdrawalApplications {
		query = `UPDATE filing_applications SET status=$2,version=version+1 WHERE id=$1`
	}
	if _, err := tx.Exec(ctx, query, scope.id, next); err != nil || next != applicationWithdrawn {
		return err
	}
	return saveWithdrawalReviewEvent(ctx, tx, scope, target.status, actor)
}

// saveWithdrawalReviewEvent 固化撤回决定到原业务的正式审核轨迹。
// 参数 ctx、tx 为事务，scope 指定新状态单据，previous、actor 记录原阶段和确认人。
// 返回值 写入轨迹的错误。
func saveWithdrawalReviewEvent(ctx context.Context, tx pgx.Tx, scope withdrawalScope,
	previous, actor string,
) error {
	query := `INSERT INTO case_review_events(case_id,action,from_status,to_status,operator,comment,version,directory_snapshot)
		SELECT c.id,'withdraw',$2,'withdrawn',$3,r.comment,c.version,c.directory_snapshot
		FROM business_cases c JOIN filing_withdrawal_requests r ON r.resource_id=c.id
		  AND r.resource='filings' AND r.case_version=c.version AND r.status='approved' WHERE c.id=$1`
	if scope.resource == withdrawalApplications {
		query = `INSERT INTO filing_application_review_events
			(application_id,action,from_status,to_status,operator,comment,version,implementation_id)
			SELECT a.id,'withdraw',$2,'withdrawn',$3,r.comment,a.version,a.implementation_id
			FROM filing_applications a JOIN filing_withdrawal_requests r ON r.resource_id=a.id
			  AND r.resource='filing-applications' AND r.case_version=a.version AND r.status='approved' WHERE a.id=$1`
	}
	_, err := tx.Exec(ctx, query, scope.id, previous, actor)
	return err
}
