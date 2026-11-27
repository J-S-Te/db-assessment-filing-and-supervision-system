package assessmentdue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

const noticeRecipients = ` AS r(audience,recipient)`
const validRecipient = `char_length(r.recipient) BETWEEN 1 AND 200 AND btrim(r.recipient)<>''`
const pendingSQL = `SELECT d.id::text` + source + `
	WHERE d.closed_at IS NULL AND d.due_on<=` + todaySQL + `+d.warning_days
	AND stage.status IN ('due_soon','due','expired')
	AND EXISTS (SELECT 1 FROM (VALUES('operator',f.organization_name),('institution',f.assessment_org))` + noticeRecipients + `
	 WHERE ` + validRecipient + ` AND NOT EXISTS (
	  SELECT 1 FROM review_notices n WHERE n.resource='assessment-dues' AND n.resource_id=d.id
	   AND n.event_version=d.cycle AND n.audience=r.audience AND n.recipient=r.recipient
	   AND n.state=CASE WHEN stage.status='due_soon' THEN 'assessment_due_soon' ELSE 'assessment_due' END
	 )) ORDER BY d.due_on,d.sequence LIMIT 200`

// Run 启动后立即同步一次，再按分钟核对明确登记的下一次测评日期。
// 参数 ctx 控制后台任务生命周期。
// 返回值 无，取消后停止工作。
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for ctx.Err() == nil {
		attempt, cancel := context.WithTimeout(ctx, 45*time.Second)
		_, err := s.Sweep(attempt)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Warn("下一次测评提醒同步未完成", "error_type", fmt.Sprintf("%T", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Sweep 扫描有限候选并按当前有效状态幂等投递站内提醒。
// 参数 ctx 控制本轮扫描及逐条事务。
// 返回值 实际新增消息数，以及需要记录或重试的存储错误。
func (s *Service) Sweep(ctx context.Context) (int64, error) {
	rows, err := s.db.Query(ctx, pendingSQL)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	var total int64
	var failures error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return total, errors.Join(failures, err)
		}
		count, err := s.notifyLocked(ctx, id)
		total += count
		failures = errors.Join(failures, err)
	}
	return total, failures
}

// notifyLocked 在持锁期间复核期限及实际归属，提交后才计入送达。
// 参数 ctx、id 为本轮上下文和待核实登记标识。
// 返回值 发送数量、略过或实际事务错误。
func (s *Service) notifyLocked(ctx context.Context, id string) (int64, error) {
	var count int64
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		item, err := lockRecord(ctx, tx, id, true)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		count, err = writeNotice(ctx, tx, item)
		return err
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// writeNotice 向当前运营者和测评机构发送本周期对应阶段的站内提醒。
// 参数 ctx、tx、item 为请求上下文、事务及已锁定的当前期限。
// 返回值 事务内实际写入数或存储错误。
func writeNotice(ctx context.Context, tx pgx.Tx, item record) (int64, error) {
	state := "assessment_due"
	var message string
	switch item.Status {
	case "due_soon":
		state = "assessment_due_soon"
		message = "下一次测评临近，登记截止日期 " + item.DueOn + "。"
	case "due":
		message = "下一次测评已到期，登记截止日期 " + item.DueOn + "。"
	case "expired":
		message = "下一次测评已超过登记截止日期 " + item.DueOn + "。"
	default:
		return 0, nil
	}
	result, err := tx.Exec(ctx, `INSERT INTO review_notices
		(audience,resource,resource_id,event_version,state,recipient,reference,subject,comment)
		SELECT r.audience,'assessment-dues',$1,$2,$3,r.recipient,$6,$7,$8
		FROM (VALUES('operator',$4::text),('institution',$5::text))`+noticeRecipients+`
		WHERE `+validRecipient+` ON CONFLICT DO NOTHING`, item.ID, item.cycle, state,
		item.OrganizationName, item.InstitutionName, item.FilingNumber, item.SystemName, message)
	return result.RowsAffected(), err
}
