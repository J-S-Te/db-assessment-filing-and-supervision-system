package materialexpiry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

const scanLimit = 200
const pendingNoticeSQL = `
 SELECT e.id::text FROM material_expiry_records e
 JOIN material_expiry_status s ON s.id=e.id
 WHERE e.retired_at IS NULL AND e.valid_until<=` + todaySQL + `+e.warning_days
   AND s.date_status IN ('due','expired')
   AND EXISTS (
     SELECT 1 FROM (VALUES('operator',s.organization_name),('institution',e.institution_name)) AS r(audience,recipient)
     WHERE btrim(r.recipient)<>''
       AND NOT EXISTS(SELECT 1 FROM review_notices n
          WHERE n.resource='material-expiries' AND n.resource_id=e.id AND n.event_version=e.cycle
            AND n.state='material_'||s.date_status AND n.recipient=r.recipient AND n.audience=r.audience)
   )
 ORDER BY e.valid_until,e.sequence LIMIT $1
`

// Run 在启动后及每分钟核对到期材料，所选周期和同一收件端只提醒一次。
// 参数 ctx 控制本地任务生命周期。
// 返回值 无，上下文取消后停止。
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for ctx.Err() == nil {
		work, cancel := context.WithTimeout(ctx, 45*time.Second)
		_, err := s.Sweep(work)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Warn("材料到期提醒扫描未完成", "error_type", fmt.Sprintf("%T", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Sweep 读取有界候选，逐项按最新有效证据投递并统计实际新增提醒。
// 参数 ctx 控制扫描及各项状态核验。
// 返回值 新增送达数，以及候选查询或各项登记错误。
func (s *Service) Sweep(ctx context.Context) (int64, error) {
	rows, err := s.db.Query(ctx, pendingNoticeSQL, scanLimit)
	if err != nil {
		return 0, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	var created int64
	var combined error
	for _, id := range items {
		count, err := s.notifyDate(ctx, id)
		created += count
		combined = errors.Join(combined, err)
	}
	return created, combined
}

// notifyDate 持锁复核当前归属和日期；成功提交后才计数。
// 参数 ctx 控制事务，id 为已选候选标识。
// 返回值 实际新增数、略过或数据库错误。
func (s *Service) notifyDate(ctx context.Context, id string) (int64, error) {
	var count int64
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		item, err := lockRecord(ctx, tx, scope{}, id, true)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		count, err = writeDateNotice(ctx, tx, item)
		return err
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// writeDateNotice 按有效期周期和实际接收方登记到期或临期提醒。
// 参数 ctx、tx、item 为现有事务及锁内计算的记录快照。
// 返回值 成功写入的提醒数量，或者持久化错误。
func writeDateNotice(ctx context.Context, tx pgx.Tx, item expiryRecord) (int64, error) {
	label := "材料有效期"
	if item.CaseKind == "report" &&
		(item.AttachmentKind == "assessment_conclusion" || item.AttachmentKind == "report_file") {
		label = "测评材料有效期"
	}
	var comment string
	switch item.DateStatus {
	case "due":
		comment = label + "即将结束，截止日期 " + item.ValidUntil + "。"
	case "expired":
		comment = label + "已届满，截止日期 " + item.ValidUntil + "。请续传有效材料。"
	default:
		return 0, nil
	}
	result, err := tx.Exec(ctx, `INSERT INTO review_notices
		(audience,resource,resource_id,event_version,state,recipient,reference,subject,comment)
	 SELECT r.audience,'material-expiries',$1,$2,$3,r.recipient,$6,$7,$8
	 FROM (VALUES('operator',$4::text),('institution',$5::text)) AS r(audience,recipient)
	 WHERE btrim(r.recipient)<>'' ON CONFLICT DO NOTHING`, item.ID, item.cycle,
		"material_"+item.DateStatus, item.OrganizationName, item.InstitutionName, item.Number, item.SystemName, comment)
	return result.RowsAffected(), err
}
