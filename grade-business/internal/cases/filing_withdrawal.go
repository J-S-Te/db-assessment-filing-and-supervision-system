package cases

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/reviewinbox"
)

const withdrawalFilings = "filings"
const withdrawalApplications = "filing-applications"
const withdrawalPending = "pending"
const withdrawalRejected = "rejected"

var errWithdrawalPending = errors.New("filing withdrawal awaiting confirmation")

type withdrawalChange struct {
	versionInput
	Reason string `json:"reason"`
}

type withdrawalDecision struct {
	versionInput
	WithdrawalID string `json:"withdrawalId"`
	Decision     string `json:"decision"`
	Comment      string `json:"comment"`
}

type withdrawalRecord struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	CaseVersion int64      `json:"caseVersion"`
	Reason      string     `json:"reason"`
	RequestedAt time.Time  `json:"requestedAt"`
	Comment     *string    `json:"comment,omitempty"`
	DecidedBy   *string    `json:"decidedBy,omitempty"`
	DecidedAt   *time.Time `json:"decidedAt,omitempty"`
}

type withdrawalView struct {
	Request    *withdrawalRecord  `json:"request,omitempty"`
	Previous   []withdrawalRecord `json:"previous,omitempty"`
	CanRequest bool               `json:"canRequest,omitempty"`
	CanDecide  bool               `json:"canDecide,omitempty"`
}

type withdrawalScope struct {
	resource, id, organization, institution string
}

type withdrawalTarget struct {
	status, organization, institution, number, system string
	version                                           int64
	eligible                                          bool
}

const withdrawalFields = `id::text,status,case_version,reason,requested_at,comment,decided_by,decided_at`

// lockWithdrawalTarget 按现有业务顺序锁定关联主备案和实际待撤回单据。
// 参数 ctx、tx 为数据库事务，scope 指定记录、业务和访问范围。
// 返回值 有权访问的目标或归属、数据库错误。
func lockWithdrawalTarget(ctx context.Context, tx pgx.Tx, scope withdrawalScope) (withdrawalTarget, error) {
	if scope.resource == withdrawalApplications {
		var id string
		err := tx.QueryRow(ctx, `SELECT f.id::text FROM filing_applications a
			JOIN business_cases f ON f.id=a.filing_id
			WHERE a.id=$1 AND ($2='' OR a.before_values->>'organizationName'=$2)
			FOR UPDATE OF f`, scope.id, scope.organization).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrNotFound
		}
		if err != nil {
			return withdrawalTarget{}, err
		}
	}
	return readWithdrawalTarget(ctx, tx, scope, true)
}

// readWithdrawalTarget 检查三端归属，并判断运营者是否仍有权发起在审撤回。
// 参数 ctx、tx 为数据库事务，scope 为目标范围，lock 指示事务已锁主备案并需要锁目标。
// 返回值 当前版本、归属与状态或未找到和读取错误。
func readWithdrawalTarget(ctx context.Context, tx pgx.Tx, scope withdrawalScope,
	lock bool,
) (withdrawalTarget, error) {
	var target withdrawalTarget
	query := `SELECT c.status,c.version,c.organization_name,c.assessment_org,c.number,c.system_name,
		c.operator_origin AND c.cancelled_at IS NULL FROM business_cases c
		WHERE c.id=$1 AND c.kind='filing' AND c.discarded_at IS NULL
		  AND ($2='' OR c.organization_name=$2) AND ($3='' OR c.assessment_org=$3)`
	if scope.resource == withdrawalApplications {
		query = `SELECT a.status,a.version,a.before_values->>'organizationName',
			a.before_values->>'assessmentOrg',a.filing_number,a.before_values->>'systemName',
			a.operator_origin AND a.filing_version=f.version AND f.status='approved'
			AND f.cancelled_at IS NULL AND a.before_values->>'organizationName'=f.organization_name
			FROM filing_applications a JOIN business_cases f ON f.id=a.filing_id
			WHERE a.id=$1 AND ($2='' OR a.before_values->>'organizationName'=$2)
			  AND ($3='' OR a.before_values->>'assessmentOrg'=$3)`
	}
	if lock && scope.resource == withdrawalFilings {
		query += " FOR UPDATE OF c"
	}
	if lock && scope.resource == withdrawalApplications {
		query += " FOR UPDATE OF a"
	}
	err := tx.QueryRow(ctx, query, scope.id, scope.organization, scope.institution).Scan(
		&target.status, &target.version, &target.organization, &target.institution,
		&target.number, &target.system, &target.eligible)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return target, err
}

// withdrawalByVersion 读取指定预期版本对应的申请用于识别重放。
// 参数 ctx、tx 为当前事务，scope 为目标，version 为最初发起时读取的业务版本。
// 返回值 已保存回执、缺失或查询错误。
func withdrawalByVersion(ctx context.Context, tx pgx.Tx, scope withdrawalScope,
	version int64,
) (withdrawalRecord, error) {
	row := tx.QueryRow(ctx, `SELECT `+withdrawalFields+` FROM filing_withdrawal_requests
		WHERE resource=$1 AND resource_id=$2 AND requested_from_version=$3`,
		scope.resource, scope.id, version)
	return scanWithdrawal(row)
}

// scanWithdrawal 将数据库行转换成可公开的撤回回执。
// 参数 row 为固定字段顺序的接收行。
// 返回值 已保存回执或数据库扫描错误。
func scanWithdrawal(row pgx.Row) (withdrawalRecord, error) {
	var item withdrawalRecord
	err := row.Scan(&item.ID, &item.Status, &item.CaseVersion, &item.Reason, &item.RequestedAt,
		&item.Comment, &item.DecidedBy, &item.DecidedAt)
	return item, err
}

// eligibleWithdrawalStage 判断是否在允许发起监管确认的审核阶段。
// 参数 target 为已校验归属及发起方式的业务记录。
// 返回值 自主报送且仍处于初审、终审时返回真。
func eligibleWithdrawalStage(target withdrawalTarget) bool {
	return target.eligible && (target.status == pendingInitial || target.status == pendingFinal)
}

// guardWithdrawalReview 防止确认撤回期间推进原审核流转。
// 参数 ctx、tx 为已锁定业务记录的事务，resource、id 为当前单据。
// 返回值 正在撤回时返回冲突，或查询错误。
func guardWithdrawalReview(ctx context.Context, tx pgx.Tx, resource, id string) error {
	var pending bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM filing_withdrawal_requests
		WHERE resource=$1 AND resource_id=$2 AND status='pending')`, resource, id).Scan(&pending)
	if err == nil && pending {
		return errWithdrawalPending
	}
	return err
}

// notifyWithdrawal 在同一事务中投递给原测评机构和运营者。
// 参数 ctx、tx 为持锁事务，scope、target 提供原归属，record 是已保存的撤回阶段。
// 返回值 送达登记错误，重复通知利用原有数据库约束。
func notifyWithdrawal(ctx context.Context, tx pgx.Tx, scope withdrawalScope,
	target withdrawalTarget, record withdrawalRecord,
) error {
	state, message := "withdrawal_requested", "运营者已申请撤回，等待监管确认。"
	if record.Status != withdrawalPending {
		state, message = "withdrawal_"+record.Status, *record.Comment
	}
	notice := reviewinbox.Notice{Resource: scope.resource, ResourceID: scope.id,
		Version: record.CaseVersion, State: state, Reference: target.number, Subject: target.system,
		Comment: message, Audience: "institution", Recipient: target.institution}
	if err := reviewinbox.Deliver(ctx, tx, notice); err != nil {
		return err
	}
	notice.Audience, notice.Recipient = "operator", target.organization
	return reviewinbox.Deliver(ctx, tx, notice)
}
