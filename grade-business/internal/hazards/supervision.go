package hazards

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/reviewinbox"
)

// canSupervise 判断重大风险是否处在需要督办的整改或机构复测阶段。
// 参数 status 为持锁台账的当前状态。
// 返回值 当前办理方可接收督办时为真。
func canSupervise(status string) bool {
	return editable(status) || status == statusRetest
}

// deliverSupervision 向当前办理方登记上海当天的一条重大风险督办。
// 参数 ctx 控制写入，tx 为办理事务，notice 带当前状态下的目标及督办意见。
// 返回值 投递成功时为 nil，或今日重复、收件范围及数据库错误。
func deliverSupervision(ctx context.Context, tx pgx.Tx, notice reviewinbox.Notice) error {
	if strings.TrimSpace(notice.Recipient) == "" {
		return errConflict
	}
	tag, err := tx.Exec(ctx, `INSERT INTO review_notices (audience,resource,resource_id,event_version,
		state,recipient,reference,subject,comment,workflow_day)
		VALUES($1,'hazards',$2,$3,'hazard_supervision',$4,$5,$6,$7,
		       (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date)
		ON CONFLICT (resource_id,workflow_day) WHERE state='hazard_supervision' DO NOTHING`,
		notice.Audience, notice.ResourceID, notice.Version, notice.Recipient,
		notice.Reference, notice.Subject, notice.Comment)
	if err == nil && tag.RowsAffected() == 0 {
		return errAlreadySupervised
	}
	return err
}
