package assessmentdue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// storeRegistration 将一个显式测评期限、依据和初始通知原子保存。
// 参数 ctx 控制事务，input 为已校验的登记数据。
// 返回值 新建或安全重放的期限摘要，或范围和持久化错误。
func (s *Service) storeRegistration(ctx context.Context, input registration) (summary, error) {
	warning := defaultWarningDays
	if input.WarningDays != nil {
		warning = *input.WarningDays
	}
	data := strings.Join([]string{input.FilingID, input.DueOn, strconv.Itoa(warning), input.Evidence}, "\x00")
	hash := sha256.Sum256([]byte(data))
	digest := hex.EncodeToString(hash[:])
	var item record
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		item, err = loadRegistration(ctx, tx, input.RequestID, digest)
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err = lockFiling(ctx, tx, input.FilingID); err != nil {
			return err
		}
		item, err = insertRegistration(ctx, tx, input, warning, digest)
		if err != nil {
			return err
		}
		if item.Version == 0 {
			item, err = loadRegistration(ctx, tx, input.RequestID, digest)
			if errors.Is(err, pgx.ErrNoRows) {
				return errConflict
			}
			return err
		}
		return s.saveEventAndNotice(ctx, tx, item, "register", input.Evidence)
	})
	return item.summary, err
}

// loadRegistration 验证重复请求的规范输入并读取原登记的最新快照。
// 参数 ctx、tx 控制查询，requestID、digest 为登记标识及输入摘要。
// 返回值 相同输入的记录或重放冲突、未登记和查询错误。
func loadRegistration(ctx context.Context, tx pgx.Tx, requestID, digest string) (record, error) {
	var id, stored string
	err := tx.QueryRow(ctx, `SELECT id::text,request_digest FROM assessment_due_records WHERE request_id=$1`,
		requestID).Scan(&id, &stored)
	if err != nil {
		return record{}, err
	}
	if digest != stored {
		return record{}, errConflict
	}
	return loadRecord(ctx, tx, filters{}, id)
}

// lockFiling 确保登记时确实存在仍有效且归属明确的备案。
// 参数 ctx、tx 控制事务，id 为明确选择的备案标识。
// 返回值 备案有效则为空，否则为范围或数据库错误。
func lockFiling(ctx context.Context, tx pgx.Tx, id string) error {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT true FROM business_cases f WHERE f.id=$1 AND `+activeFiling+
		` FOR SHARE OF f`, id).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return errScope
	}
	return err
}

// insertRegistration 尝试写入备案唯一在办期限并读取最新收件信息。
// 参数 ctx、tx、input、warning、digest 分别为事务、登记与规范化参数。
// 返回值 新记录，已存在时为零值，或数据库错误。
func insertRegistration(ctx context.Context, tx pgx.Tx, input registration, warning int,
	digest string,
) (record, error) {
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO assessment_due_records
		(request_id,request_digest,filing_id,due_on,warning_days,evidence)
		VALUES($1,$2,$3,$4::text::date,$5,$6) ON CONFLICT DO NOTHING RETURNING id::text`,
		input.RequestID, digest, input.FilingID, input.DueOn, warning, input.Evidence).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return record{}, nil
	}
	if err != nil {
		return record{}, err
	}
	return loadRecord(ctx, tx, filters{}, id)
}

// lockRecord 锁定期限及对应备案，再按最新快照复核状态和受件范围。
// 参数 ctx、tx、id 提供事务和登记标识，skip 允许后台略过在办写入。
// 返回值 操作时可见的记录，或锁、范围和数据库错误。
func lockRecord(ctx context.Context, tx pgx.Tx, id string, skip bool) (record, error) {
	query := `SELECT true FROM assessment_due_records d JOIN business_cases f ON f.id=d.filing_id WHERE d.id=$1`
	lock := ` FOR UPDATE OF d FOR SHARE OF f`
	if skip {
		lock = ` FOR UPDATE OF d SKIP LOCKED FOR SHARE OF f SKIP LOCKED`
	}
	var ok bool
	if err := tx.QueryRow(ctx, query+lock, id).Scan(&ok); err != nil {
		return record{}, err
	}
	return loadRecord(ctx, tx, filters{}, id)
}

// storeChange 在事务内版本保护地记录一次修订或关闭及其依据。
// 参数 ctx、id、input 提供请求上下文、登记标识和合法操作。
// 返回值 最新期限摘要，或并发、范围及持久化错误。
func (s *Service) storeChange(ctx context.Context, id string, input change) (summary, error) {
	var item record
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		item, err = lockRecord(ctx, tx, id, false)
		if err != nil {
			return err
		}
		if item.Version != input.ExpectedVersion {
			return verifyReplay(ctx, tx, item, input)
		}
		if item.Status == "closed" {
			return errConflict
		}
		if input.Action == "revise" && item.Status == "suspended" {
			return errScope
		}
		item, err = applyChange(ctx, tx, item, input)
		if err != nil {
			return err
		}
		return s.saveEventAndNotice(ctx, tx, item, input.Action, input.Evidence)
	})
	return item.summary, err
}

// verifyReplay 确认陈旧版本请求是刚成功的同一操作，而非覆盖其他人改动。
// 参数 ctx、db、item、input 为事务、已锁定记录和原始操作。
// 返回值 确认重放时为空，或并发冲突和存储错误。
func verifyReplay(ctx context.Context, db reader, item record, input change) error {
	if item.Version != input.ExpectedVersion+1 || (input.Action == "revise" &&
		(item.DueOn != input.DueOn || (input.WarningDays != nil && item.WarningDays != *input.WarningDays))) {
		return errConflict
	}
	var matches bool
	err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM assessment_due_events
		WHERE record_id=$1 AND version=$2 AND action=$3 AND evidence=$4)`,
		item.ID, item.Version, input.Action, input.Evidence).Scan(&matches)
	if err == nil && !matches {
		return errConflict
	}
	return err
}

// applyChange 保存明确期限更新或结束，截止日变化时开启新提醒周期。
// 参数 ctx、tx 为事务，item、input 为已锁定记录和变更数据。
// 返回值 变更后完整记录或数据库错误。
func applyChange(ctx context.Context, tx pgx.Tx, item record, input change) (record, error) {
	var err error
	if input.Action == "close" {
		_, err = tx.Exec(ctx, `UPDATE assessment_due_records SET version=version+1,
			closed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, item.ID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE assessment_due_records SET version=version+1,
			cycle=cycle+CASE WHEN due_on<>$2::text::date THEN 1 ELSE 0 END,
			due_on=$2::text::date,warning_days=COALESCE($3,warning_days),
			evidence=$4,updated_at=clock_timestamp() WHERE id=$1`, item.ID, input.DueOn, input.WarningDays, input.Evidence)
	}
	if err != nil {
		return record{}, err
	}
	return loadRecord(ctx, tx, filters{}, item.ID)
}

// saveEventAndNotice 保存本轮明确依据及符合当前期限阶段的实际站内投递。
// 参数 ctx、tx 为事务，item、action、evidence 为确定的记录、动作和留痕说明。
// 返回值 任一留痕或投递错误会回滚本次操作。
func (s *Service) saveEventAndNotice(ctx context.Context, tx pgx.Tx, item record, action, evidence string) error {
	_, err := tx.Exec(ctx, `INSERT INTO assessment_due_events
		(record_id,version,cycle,action,due_on,warning_days,evidence,actor)
		VALUES($1,$2,$3,$4,$5::text::date,$6,$7,$8)`,
		item.ID, item.Version, item.cycle, action, item.DueOn, item.WarningDays, evidence, s.actor)
	if err != nil {
		return err
	}
	_, err = writeNotice(ctx, tx, item)
	return err
}
