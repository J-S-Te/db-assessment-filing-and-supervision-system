package compliance

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// restoreRecord 先恢复已经登记的同一请求，即使来源后来已更新。
// 参数 ctx 为请求上下文，tx 为事务，id 为当前报告，input 为用户提交，item 接收原记录。
// 返回值 是否重现了原登记，或重复请求冲突及存储错误。
func restoreRecord(ctx context.Context, tx pgx.Tx, id string, input recordInput, item *auditRecord) (bool, error) {
	if input.RequestID == "" {
		return false, nil
	}
	if err := lockRequest(ctx, tx, "assessment:"+input.RequestID); err != nil {
		return false, err
	}
	rows, err := tx.Query(ctx, auditQuery+" WHERE c.request_id=$1", input.RequestID)
	if err != nil {
		return false, err
	}
	saved, err := pgx.CollectOneRow(rows, pgx.RowToStructByPos[auditRecord])
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	savedInput := recordInput{
		RequestID: input.RequestID, ReportVersion: saved.ReportVersion, AttachmentID: saved.AttachmentID,
		ExtractionVersion: saved.ExtractionVersion, FieldKey: saved.Field.Key, KnowledgeID: saved.KnowledgeID,
		KnowledgeVersion: saved.KnowledgeVersion, Decision: saved.Decision, Comment: saved.Comment,
	}
	if saved.Clause != nil {
		savedInput.ClauseNumber, savedInput.ClauseVersion = saved.Clause.Number, saved.Clause.Version
	}
	if saved.ReportID != id || savedInput != input {
		return false, errRequestConflict
	}
	*item = saved
	return true, nil
}

// loadClauseSnapshot 固定所属知识条目的当前启用条款，沿用外层事务已锁定的知识版本。
// 参数 ctx 为请求上下文，tx 为登记事务，input 为选择，item 接收条款快照。
// 返回值 无条款或选中版本有效时为 nil，否则为来源及存储错误。
func loadClauseSnapshot(ctx context.Context, tx pgx.Tx, input recordInput, item *auditRecord) error {
	if input.ClauseNumber == "" {
		return nil
	}
	snapshot := clauseSnapshot{Number: input.ClauseNumber}
	var active bool
	err := tx.QueryRow(ctx, `SELECT version,text,active FROM knowledge_clause_revisions
		WHERE knowledge_id=$1 AND knowledge_version=$2 AND number=$3 ORDER BY version DESC LIMIT 1`,
		item.KnowledgeID, item.KnowledgeVersion, snapshot.Number).Scan(&snapshot.Version, &snapshot.Text, &active)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!active || snapshot.Version != input.ClauseVersion)) {
		return errSource
	}
	if err == nil {
		item.Clause = &snapshot
	}
	return err
}

// insertRecord 保存依据快照及外键，并返回新登记标识与时间。
// 参数 ctx 为请求上下文，tx 为事务，id 为报告标识，input 和 item 为校验后的引用和内容。
// 返回值 持久化错误或 nil。
func insertRecord(ctx context.Context, tx pgx.Tx, id string, input recordInput, item *auditRecord) error {
	return tx.QueryRow(ctx, `INSERT INTO report_compliance_records
		(report_id,report_version,attachment_id,extraction_version,field,knowledge_id,knowledge_version,
		decision,comment,operator,request_id,clause_number,clause_version)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,nullif($11,'')::uuid,nullif($12,''),nullif($13::bigint,0))
		RETURNING id::text,created_at`, id, item.ReportVersion, item.AttachmentID, item.ExtractionVersion,
		item.Field, item.KnowledgeID, item.KnowledgeVersion, item.Decision, item.Comment, item.Operator,
		input.RequestID, input.ClauseNumber, input.ClauseVersion).Scan(&item.ID, &item.CreatedAt)
}
