package hazards

import (
	"context"
	"errors"

	"grade-business/internal/reviewinbox"

	"github.com/jackc/pgx/v5"
)

// SyncApproved 登记已批准规划的风险并通知企业，重复调用时保留已有台账状态。
// 参数 ctx 为请求上下文，tx 为批准或手动同步事务，planID 为整改规划标识。
// 返回值 该规划已有的台账总条数，或规划不符及数据库错误。
func SyncApproved(ctx context.Context, tx pgx.Tx, planID string) (int, error) {
	if !validID(planID) {
		return 0, errNotFound
	}
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM remediation_plans WHERE id=$1 FOR UPDATE`, planID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errNotFound
	}
	if err != nil {
		return 0, err
	}
	if status != "approved" {
		return 0, errPlanNotApproved
	}
	if err := insertHazardsAndDeliver(ctx, tx, planID); err != nil {
		return 0, err
	}
	var count int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM hazard_items WHERE plan_id=$1`, planID).Scan(&count)
	return count, err
}

// insertHazardsAndDeliver 新登记风险并仅为新建隐患生成企业端待整改意见。
// 参数 ctx 为请求上下文，tx 为已锁规划的事务，planID 为批准规划标识。
// 返回值 隐患与意见均保存时为 nil，或登记错误。
func insertHazardsAndDeliver(ctx context.Context, tx pgx.Tx, planID string) error {
	rows, err := tx.Query(ctx, `WITH new_items AS (
		INSERT INTO hazard_items(plan_id,risk_index,severity,description,measure,responsibility,deadline)
		SELECT p.id,r.risk_index,r.value->>'risk',r.value->>'description',r.value->>'measure',
		       r.value->>'responsibility',(r.value->>'deadline')::date
		FROM remediation_plans p CROSS JOIN LATERAL
		    jsonb_array_elements(p.risks) WITH ORDINALITY r(value,risk_index)
		WHERE p.id=$1 ORDER BY r.risk_index ON CONFLICT(plan_id,risk_index) DO NOTHING
		RETURNING id,plan_id,description,version
	) SELECT h.id::text,filing.organization_name,report.number,filing.system_name,h.description,h.version
	FROM new_items h`+hazardAncestry+` FOR SHARE OF report,filing`, planID)
	if err != nil {
		return err
	}
	notices, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (reviewinbox.Notice, error) {
		item := reviewinbox.Notice{Audience: "operator", Resource: "hazards", State: statusOpen}
		err := row.Scan(&item.ResourceID, &item.Recipient, &item.Reference, &item.Subject, &item.Comment, &item.Version)
		return item, err
	})
	if err != nil {
		return err
	}
	for _, notice := range notices {
		if err := reviewinbox.Deliver(ctx, tx, notice); err != nil {
			return err
		}
	}
	return nil
}

// syncPlan 在独立写事务内手动同步此前已经批准的整改规划。
// 参数 ctx 为请求上下文，planID 为用于回填历史台账的整改规划标识。
// 返回值 本规划台账总条数，或未批准、不存在和持久化错误。
func (s *Service) syncPlan(ctx context.Context, planID string) (int, error) {
	var count int
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		count, err = SyncApproved(ctx, tx, planID)
		return err
	})
	return count, err
}
