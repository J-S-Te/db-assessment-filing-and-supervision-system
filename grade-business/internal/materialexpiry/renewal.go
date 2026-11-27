package materialexpiry

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/cases"
)

type stagedRenewal struct {
	file        cases.Attachment
	key, digest string
}

// renew 以文件摘要和发起版本核对重放，再登记一笔待审核续传。
// 参数 ctx、seat、id、input 和 staged 为范围、目标、更新依据及临时材料。
// 返回值 登记后的最小业务响应，或者范围、版本、有效期和写入错误。
func (s *Service) renew(ctx context.Context, seat scope, id string, input renewalInput,
	staged stagedRenewal,
) (expirySummary, error) {
	var out expiryRecord
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		item, err := lockRecord(ctx, tx, seat, id, false)
		if err != nil {
			return err
		}
		out = item
		if item.Version != input.version {
			return checkRenewalReplay(ctx, tx, seat, id, input, staged)
		}
		if item.DateStatus == "retired" || item.RenewalPending {
			return errConflict
		}
		if err = checkRenewalMaterial(ctx, tx, item, input, staged.digest); err != nil {
			return err
		}
		out, err = saveRenewal(ctx, tx, seat, item, input, staged)
		return err
	})
	return out.expirySummary, err
}

// checkRenewalReplay 只将同席位、日期、证据和文件的重试作为同一份续传处理。
// 参数 ctx、tx 控制查询，seat、id、input 和 staged 表示待核对的完整请求。
// 返回值 原样重试时为空，其他情况为版本或数据库错误。
func checkRenewalReplay(ctx context.Context, tx pgx.Tx, seat scope, id string, input renewalInput,
	staged stagedRenewal,
) error {
	var replay bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM material_expiry_renewals
	  WHERE record_id=$1 AND version=$2::bigint+1 AND audience=$3 AND valid_until=$4::text::date
	   AND evidence=$5 AND digest=$6 AND file_name=$7)`,
		id, input.version, seat.audience, input.until, input.evidence, staged.digest, staged.file.FileName).Scan(&replay)
	if err == nil && !replay {
		return errConflict
	}
	return err
}

// checkRenewalMaterial 校验更新周期确实延长，并拒绝以同一份已续期文件再次办理。
// 参数 ctx、tx、item、input 和 digest 为数据库事务、登记项、所报续期及文件摘要。
// 返回值 可登记时为空，否则有效期、材料或查询错误。
func checkRenewalMaterial(ctx context.Context, tx pgx.Tx, item expiryRecord, input renewalInput, digest string) error {
	var future, changed bool
	err := tx.QueryRow(ctx, `SELECT $2::text::date>GREATEST(valid_until,`+todaySQL+`),
	  $3<>(COALESCE((SELECT digest FROM material_expiry_renewals
	    WHERE record_id=$1 AND status='approved' ORDER BY version DESC LIMIT 1),''))
	  FROM material_expiry_records WHERE id=$1`, item.ID, input.until, digest).Scan(&future, &changed)
	if err != nil {
		return err
	}
	if !future {
		return errDate
	}
	if !changed {
		return errUnchanged
	}
	return nil
}

// saveRenewal 将已有结构校验的附件、登记版本和提交记录一并保存。
// 参数 ctx、tx、seat、item、input、staged 表示事务、席位、原登记和有效材料。
// 返回值 待审核登记快照，或者存储错误。
func saveRenewal(ctx context.Context, tx pgx.Tx, seat scope, item expiryRecord, input renewalInput,
	staged stagedRenewal,
) (expiryRecord, error) {
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO material_expiry_renewals
	  (record_id,version,audience,valid_until,evidence,file_name,file_key,digest,size_bytes)
	  VALUES($1,$2,$3,$4::text::date,$5,$6,$7,$8,$9) RETURNING id::text`,
		item.ID, item.Version+1, seat.audience, input.until, input.evidence, staged.file.FileName,
		staged.key, staged.digest, staged.file.Size).Scan(&id)
	if err != nil {
		return expiryRecord{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE material_expiry_records SET version=version+1 WHERE id=$1`, item.ID); err != nil {
		return expiryRecord{}, err
	}
	out, err := loadRecord(ctx, tx, seat, item.ID)
	if err != nil {
		return expiryRecord{}, err
	}
	actor := map[string]string{"operator": "网络运营者", "institution": "测评机构"}[seat.audience]
	err = writeEvent(ctx, tx, out, eventInput{action: "submit", evidence: input.evidence,
		actor: actor, until: input.until, renewalID: id})
	return out, err
}

// releaseUnused 在已确认没有业务记录引用后清理本次请求的暂存文件。
// 参数 ctx 为锁定此次请求的上下文，key 为随机生成的内部存储键。
// 返回值 无，无法确认时保留文件供后续检查。
func (s *Service) releaseUnused(ctx context.Context, key string) {
	check, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var used bool
	err := s.db.QueryRow(check, `SELECT EXISTS(SELECT 1 FROM material_expiry_renewals WHERE file_key=$1)`, key).Scan(&used)
	if err != nil {
		slog.Warn("材料续传暂存引用核查未完成")
		return
	}
	if !used {
		if err := s.files.RemoveUnregisteredAttachment(key); err != nil {
			slog.Warn("材料续传暂存清理未完成")
		}
	}
}

// downloadRenewal 在同一查询中检查记录归属、轮次归属与正式可见状态。
// 参数 w、r 为下载响应和请求，seat、id、renewalID 为访问者及对象。
// 返回值 已发送内容时为空，或不存在与读取错误。
func (s *Service) downloadRenewal(w http.ResponseWriter, r *http.Request, seat scope, id, renewalID string) error {
	var item cases.Attachment
	var key string
	err := s.db.QueryRow(r.Context(), `SELECT rn.id::text,e.attachment_kind,rn.file_name,rn.size_bytes,
	  rn.submitted_at,rn.file_key FROM material_expiry_records e
	  JOIN material_expiry_status s ON s.id=e.id
	  JOIN material_expiry_renewals rn ON rn.record_id=e.id
	  WHERE `+bySeat+` AND e.id=$3 AND rn.id=$4
	    AND NOT (`+originalOperator+`)
	    AND ($1='' OR rn.status='approved' OR rn.audience=$1)`, seat.audience, seat.name, id, renewalID).
		Scan(&item.ID, &item.Kind, &item.FileName, &item.Size, &item.UploadedAt, &key)
	if err != nil {
		return err
	}
	err = s.files.ServeBusinessAttachment(w, r, item, key)
	if errors.Is(err, cases.ErrNotFound) {
		return pgx.ErrNoRows
	}
	return err
}
