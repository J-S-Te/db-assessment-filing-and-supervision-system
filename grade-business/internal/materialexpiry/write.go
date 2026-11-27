package materialexpiry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/reviewinbox"
)

const todaySQL = `(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date`

type eventInput struct {
	action, evidence, actor, until, renewalID string
}

// requestDigest 封存登记请求标识对应的最小输入。
// 参数 input 为清理后的请求，warning 为应用缺省值后的提前日数。
// 返回值 可验证重放内容的 SHA-256 摘要，不含存储标识。
func requestDigest(input registerInput, warning int) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		input.SourceAttachmentID, input.ValidUntil, strconv.Itoa(warning), input.Evidence,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// register 从现有正式材料登记有效期，并同事务保存审计与当前到期消息。
// 参数 ctx 控制办理，input 是已验证的原件、日期、提醒天数和依据。
// 返回值 新建或已有登记的摘要，或者范围、并发及持久化错误。
func (s *Service) register(ctx context.Context, input registerInput) (expirySummary, error) {
	warning := defaultWarningDays
	if input.WarningDays != nil {
		warning = *input.WarningDays
	}
	digest := requestDigest(input, warning)
	var out expiryRecord
	err := pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		var err error
		out, err = existingRegistration(ctx, tx, input.RequestID, digest)
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		source, err := registrationSource(ctx, tx, input.SourceAttachmentID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errScope
		}
		if err != nil {
			return err
		}
		out, err = s.insertRegistration(ctx, tx, input, warning, digest, source)
		return err
	})
	return out.expirySummary, err
}

// existingRegistration 识别相同登记请求的安全重放。
// 参数 ctx、tx 是事务，key、digest 是请求标识和一致性摘要。
// 返回值 原登记当前摘要，不存在、输入冲突或读取错误。
func existingRegistration(ctx context.Context, tx pgx.Tx, key, digest string) (expiryRecord, error) {
	var id, stored string
	err := tx.QueryRow(ctx, `SELECT id::text,request_digest FROM material_expiry_records
		WHERE request_id=$1`, key).Scan(&id, &stored)
	if err != nil {
		return expiryRecord{}, err
	}
	if stored != digest {
		return expiryRecord{}, errConflict
	}
	return loadRecord(ctx, tx, scope{}, id)
}

// insertRegistration 写入同类材料唯一、记录明确来源的台账项。
// 参数 ctx、tx 控制事务，input、warning、digest 和 source 为完整核验的数据。
// 返回值 保存的当前快照或写入错误。
func (s *Service) insertRegistration(ctx context.Context, tx pgx.Tx, input registerInput,
	warning int, digest string, source sourceOption,
) (expiryRecord, error) {
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO material_expiry_records
	 (request_id,request_digest,filing_id,case_id,case_kind,source_attachment_id,attachment_kind,
	  number,system_name,organization_name,institution_name,valid_until,warning_days,evidence)
	 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::text::date,$13,$14)
	 ON CONFLICT DO NOTHING RETURNING id::text`, input.RequestID, digest, source.FilingID, source.CaseID,
		source.CaseKind, source.AttachmentID, source.AttachmentKind, source.Number, source.SystemName,
		source.OrganizationName, source.InstitutionName, input.ValidUntil, warning, input.Evidence).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		out, replayErr := existingRegistration(ctx, tx, input.RequestID, digest)
		if errors.Is(replayErr, pgx.ErrNoRows) {
			return expiryRecord{}, errConflict
		}
		return out, replayErr
	}
	if err != nil {
		return expiryRecord{}, err
	}
	out, err := loadRecord(ctx, tx, scope{}, id)
	if err != nil {
		return expiryRecord{}, err
	}
	if err = writeEvent(ctx, tx, out, eventInput{action: "register", evidence: input.Evidence,
		actor: s.actor, until: input.ValidUntil}); err != nil {
		return expiryRecord{}, err
	}
	_, err = writeDateNotice(ctx, tx, out)
	return out, err
}

// lockRecord 序列化同一有效期的续传、审核、退役和调度，并锁定归属证据。
// 参数 ctx、tx 是操作事务，seat、id 为访问端和登记记录，skip 供后台略过持锁记录。
// 返回值 最新记录及可判定的范围、锁或持久化错误。
func lockRecord(ctx context.Context, tx pgx.Tx, seat scope, id string, skip bool) (expiryRecord, error) {
	query := `SELECT e.id::text FROM material_expiry_records e JOIN business_cases c ON c.id=e.case_id
		JOIN business_cases f ON f.id=e.filing_id
		WHERE e.id=$1 AND ($2='' OR $2='operator' AND COALESCE(e.current_organization_name,e.organization_name) COLLATE "C"=$3
		 OR $2='institution' AND e.institution_name COLLATE "C"=$3)
		FOR UPDATE OF e FOR SHARE OF c,f`
	if skip {
		query += " SKIP LOCKED"
	}
	var locked string
	if err := tx.QueryRow(ctx, query, id, seat.audience, seat.name).Scan(&locked); err != nil {
		return expiryRecord{}, err
	}
	return loadRecord(ctx, tx, seat, id)
}

// writeEvent 保存每次办理对应的版本和日期快照。
// 参数 ctx、tx 为原子事务，e 和 event 为已保存的记录和操作。
// 返回值 无错误或审计记录错误。
func writeEvent(ctx context.Context, tx pgx.Tx, e expiryRecord, event eventInput) error {
	_, err := tx.Exec(ctx, `INSERT INTO material_expiry_events
	 (record_id,version,cycle,action,evidence,valid_until,actor,renewal_id)
	 VALUES($1,$2,$3,$4,$5,$6::text::date,$7,NULLIF($8,'')::uuid)`,
		e.ID, e.Version, e.cycle, event.action, event.evidence, event.until, event.actor, event.renewalID)
	return err
}

// act 以已经核对的办理版本审核续传或停用记录。
// 参数 ctx、id、input 为操作上下文、登记标识与动作。
// 返回值 更新后的台账摘要，或有效期、状态和持久化错误。
func (s *Service) act(ctx context.Context, id string, input actionInput) (expirySummary, error) {
	var out expiryRecord
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		item, err := lockRecord(ctx, tx, scope{}, id, false)
		if err != nil {
			return err
		}
		if item.Version != input.ExpectedVersion {
			out = item
			return checkActionReplay(ctx, tx, id, input)
		}
		if item.DateStatus == "retired" {
			return errConflict
		}
		if input.Action == "retire" {
			out, err = s.retire(ctx, tx, item, input.Comment)
		} else {
			out, err = s.decide(ctx, tx, item, input)
		}
		return err
	})
	return out.expirySummary, err
}

// checkActionReplay 把同版本、同决定及同意见的重试指向已提交的结果。
// 参数 ctx、tx 控制查询，id、input 为记录和旧请求。
// 返回值 命中原操作时无错误，否则版本冲突或读取错误。
func checkActionReplay(ctx context.Context, tx pgx.Tx, id string, input actionInput) error {
	var replay bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM material_expiry_events
	  WHERE record_id=$1 AND version=$2::bigint+1 AND action=$3 AND evidence=$4)`,
		id, input.ExpectedVersion, input.Action, input.Comment).Scan(&replay)
	if err == nil && !replay {
		return errConflict
	}
	return err
}

// retire 停止后续提醒并保留最后一周期及原来的材料。
// 参数 ctx、tx 控制事务，item 和 comment 为已锁定记录与退役原因。
// 返回值 处理后的历史记录或状态及写入错误。
func (s *Service) retire(ctx context.Context, tx pgx.Tx, item expiryRecord, comment string) (expiryRecord, error) {
	if item.RenewalPending {
		return expiryRecord{}, errConflict
	}
	_, err := tx.Exec(ctx, `UPDATE material_expiry_records
	 SET retired_at=clock_timestamp(),version=version+1 WHERE id=$1`, item.ID)
	if err != nil {
		return expiryRecord{}, err
	}
	out, err := loadRecord(ctx, tx, scope{}, item.ID)
	if err != nil {
		return expiryRecord{}, err
	}
	if err := writeEvent(ctx, tx, out, eventInput{action: "retire", evidence: comment,
		actor: s.actor, until: out.ValidUntil}); err != nil {
		return expiryRecord{}, err
	}
	if item.DateStatus == "suspended" {
		return out, nil
	}
	err = notifyBoth(ctx, tx, out, "material_expiry_retired", "材料有效期登记已结束。")
	return out, err
}

// decide 持锁核对本轮续传及归属，审核通过后建立新的有效期周期。
// 参数 ctx、tx、item、input 为事务、当前记录及监管决定。
// 返回值 已保存状态或冲突和写入错误。
func (s *Service) decide(ctx context.Context, tx pgx.Tx, item expiryRecord, input actionInput) (expiryRecord, error) {
	accepted := input.Action == "approve"
	if !item.RenewalPending || (accepted && item.DateStatus == "suspended") {
		return expiryRecord{}, errConflict
	}
	var id, until, evidence string
	var future bool
	err := tx.QueryRow(ctx, `SELECT id::text,to_char(valid_until,'YYYY-MM-DD'),evidence,
	  valid_until>`+todaySQL+` FROM material_expiry_renewals WHERE record_id=$1 AND status='pending'`,
		item.ID).Scan(&id, &until, &evidence, &future)
	if err != nil {
		return expiryRecord{}, err
	}
	if accepted && !future {
		return expiryRecord{}, errDate
	}
	if err = saveDecision(ctx, tx, item.ID, id, until, evidence, accepted); err != nil {
		return expiryRecord{}, err
	}
	out, err := loadRecord(ctx, tx, scope{}, item.ID)
	if err != nil {
		return expiryRecord{}, err
	}
	err = writeEvent(ctx, tx, out, eventInput{action: input.Action, evidence: input.Comment,
		actor: s.actor, until: until, renewalID: id})
	if err != nil {
		return expiryRecord{}, err
	}
	return out, finishDecision(ctx, tx, out, accepted)
}

// saveDecision 落下审批和版本，更新生效周期时固定获批证据。
// 参数 ctx、tx 为事务，recordID、id、until、evidence 和 accepted 为处理输入。
// 返回值 决定与有效期写入错误。
func saveDecision(ctx context.Context, tx pgx.Tx, recordID, id, until, evidence string, accepted bool) error {
	if _, err := tx.Exec(ctx, `UPDATE material_expiry_renewals SET
	 status=CASE WHEN $2 THEN 'approved' ELSE 'rejected' END,decided_at=clock_timestamp()
	 WHERE id=$1`, id, accepted); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE material_expiry_records SET version=version+1,
	 cycle=cycle+CASE WHEN $2 THEN 1 ELSE 0 END,
	 valid_until=CASE WHEN $2 THEN $3::text::date ELSE valid_until END,
	 evidence=CASE WHEN $2 THEN $4 ELSE evidence END WHERE id=$1`, recordID, accepted, until, evidence)
	return err
}

// finishDecision 按实际归属投递本轮决定，并随获批新周期刷新到期状态。
// 参数 ctx、tx、out、accepted 表示事务、决定后的快照及是否获批。
// 返回值 通知写入错误或成功。
func finishDecision(ctx context.Context, tx pgx.Tx, out expiryRecord, accepted bool) error {
	if out.DateStatus == "suspended" {
		return nil
	}
	if !accepted {
		return notifyBoth(ctx, tx, out, "material_renewal_rejected", "续传未通过，请查看监管意见后重新提交。")
	}
	if err := notifyBoth(ctx, tx, out, "material_renewal_approved", "续传已通过，新的有效期生效。"); err != nil {
		return err
	}
	_, err := writeDateNotice(ctx, tx, out)
	return err
}

// notifyBoth 将记录决定写入同一事务中的准确两端收件范围。
// 参数 ctx、tx、e 为业务事务和快照，state、comment 为状态和简要文案。
// 返回值 任一有效收件人的登记错误。
func notifyBoth(ctx context.Context, tx pgx.Tx, e expiryRecord, state, comment string) error {
	for _, to := range []scope{{"operator", e.OrganizationName}, {"institution", e.InstitutionName}} {
		if strings.TrimSpace(to.name) == "" {
			continue
		}
		if err := reviewinbox.Deliver(ctx, tx, reviewinbox.Notice{Audience: to.audience,
			Resource: resource, ResourceID: e.ID, Version: e.Version, State: state,
			Recipient: to.name, Reference: e.Number, Subject: e.SystemName, Comment: comment}); err != nil {
			return err
		}
	}
	return nil
}
