package cases

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/access"
)

var errSameReviewer = errors.New("终审须由不同于初审的监管账号办理")

// requireSeparateReviewer 在锁定单据后核对账号是否曾执行本轮初审。
// 参数 ctx、tx 为认证环境及写入事务，id、status、application 指定单据、阶段和类型。
// 返回值 同账号试图终审时返回业务错误，其余返回查询结果。
func requireSeparateReviewer(ctx context.Context, tx pgx.Tx, id, status string, application bool) error {
	actor, authenticated := access.Current(ctx)
	if !authenticated || status != pendingFinal {
		return nil
	}
	query := `SELECT COALESCE(operator_id::text,'') FROM case_review_events
		WHERE case_id=$1 AND action='approve' AND from_status='pending_initial' AND to_status='pending_final'
		ORDER BY id DESC LIMIT 1`
	if application {
		query = `SELECT COALESCE(operator_id::text,'') FROM filing_application_review_events
			WHERE application_id=$1 AND action='approve' AND from_status='pending_initial' AND to_status='pending_final'
			ORDER BY id DESC LIMIT 1`
	}
	var initial string
	err := tx.QueryRow(ctx, query, id).Scan(&initial)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err == nil && initial == actor.ID {
		return errSameReviewer
	}
	return err
}
