package hazards

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/access"
)

// ErrDeadlineConflict 表示隐患已关闭、期限已变化或新的延期日期无效。
var ErrDeadlineConflict = errors.New("hazard deadline cannot be extended")

// ExtendDeadline 在延期审核事务内更新真实隐患期限和办理版本，并追加进度留痕。
// 参数 ctx、tx 为已核对关联归属的审核上下文与事务，id、newDate、operator、reason 为隐患、日期、经办人和意见。
// 返回值 期限和留痕均已登记时为 nil；不可办理时返回 ErrDeadlineConflict，存储故障返回数据库错误。
func ExtendDeadline(ctx context.Context, tx pgx.Tx, id, newDate, operator, reason string) error {
	date, parseErr := time.Parse("2006-01-02", newDate)
	operator, reason = strings.TrimSpace(operator), strings.TrimSpace(reason)
	if !validID(id) || parseErr != nil || date.Year() < 1 || date.Format("2006-01-02") != newDate ||
		operator == "" || !validText(operator, 200) || reason == "" || !validText(reason, 1000) {
		return ErrDeadlineConflict
	}
	var status, previous, today string
	err := tx.QueryRow(ctx, `SELECT status,to_char(deadline,'YYYY-MM-DD'),
		to_char(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai','YYYY-MM-DD')
		FROM hazard_items WHERE id=$1 FOR UPDATE`, id).Scan(&status, &previous, &today)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDeadlineConflict
	}
	if err != nil {
		return err
	}
	if status == statusClosed || newDate <= previous || newDate <= today {
		return ErrDeadlineConflict
	}
	var version int64
	err = tx.QueryRow(ctx, `UPDATE hazard_items SET deadline=$2::text::date,version=version+1
		WHERE id=$1 RETURNING version`, id, newDate).Scan(&version)
	if err != nil {
		return err
	}
	comment := fmt.Sprintf("整改期限由 %s 延至 %s。%s", previous, newDate, reason)
	actor, _ := access.Current(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO hazard_events
		(hazard_id,action,from_status,to_status,operator,comment,version,operator_id,operator_role)
		VALUES($1,'extend_deadline',$2,$2,$3,$4,$5,NULLIF($6,'')::uuid,NULLIF($7,''))`,
		id, status, operator, comment, version, actor.ID, actor.Role)
	return err
}
