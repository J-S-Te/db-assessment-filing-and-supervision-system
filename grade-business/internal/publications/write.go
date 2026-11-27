package publications

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// create 在同一事务保存草稿及初始留痕，资料默认面向运营单位。
// 参数 ctx 为请求上下文，input 为通过校验的公告内容。
// 返回值 版本一的详情，或数据库错误。
func (s *Service) create(ctx context.Context, input content) (detail, error) {
	out := detail{summary: summary{Status: draft, Version: 1, Audience: allAudience}}
	if isMaterialCategory(input.Category) {
		out.Audience = operatorAudience
	}
	applyContent(&out, input)
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO local_publications DEFAULT VALUES RETURNING id::text,updated_at`).
			Scan(&out.ID, &out.UpdatedAt)
		if err != nil {
			return err
		}
		if err := validateRecipients(ctx, tx, out.Targets); err != nil {
			return err
		}
		return s.saveRevision(ctx, tx, out, "create")
	})
	out.AllowedActions = available(out)
	return out, err
}

// change 锁定当前公告版本并提交一次编辑、发布或撤回。
// 参数 ctx 为请求上下文，id 和 expected 为公告与预期版本，action 为操作，input 为可选修订内容。
// 返回值 新版本详情，或并发冲突和业务写入错误。
func (s *Service) change(ctx context.Context, id pgtype.UUID, expected int64, action string, input *content) (detail, error) {
	var out detail
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := lockVersion(ctx, tx, id, expected); err != nil {
			return err
		}
		return s.writeChange(ctx, tx, id, action, input, &out)
	})
	out.AllowedActions = available(out)
	return out, err
}

// writeChange 在持锁事务中写入公告历史，并为新发布的版本确定收件方。
// 参数 ctx 为请求上下文，tx 为持锁事务，id 为公告标识，action 为变更动作，input 为可选修订，out 接收新状态。
// 返回值 状态冲突、业务校验或事务写入错误，成功时为 nil。
func (s *Service) writeChange(ctx context.Context, tx pgx.Tx, id pgtype.UUID, action string,
	input *content, out *detail,
) error {
	row := tx.QueryRow(ctx, "SELECT "+detailFields+visibleJoin+" WHERE p.id=$4", false, "", "", id)
	var err error
	*out, err = scanDetail(row)
	if err != nil {
		return err
	}
	if !slices.Contains(available(*out), action) {
		return errConflict
	}
	if input != nil {
		applyContent(out, *input)
		if input.Audience != nil {
			if err := validateRecipients(ctx, tx, out.Targets); err != nil {
				return err
			}
		}
	}
	if action == "publish" && isMaterialCategory(out.Category) {
		if err := s.validatePublishedMaterial(ctx, tx, *out); err != nil {
			return err
		}
	}
	if err := updateState(ctx, tx, id, action, out); err != nil {
		return err
	}
	if err := s.saveRevision(ctx, tx, *out, action); err != nil {
		return err
	}
	if action == "publish" {
		return deliverPublished(ctx, tx, *out)
	}
	return nil
}

// lockVersion 先锁定公告，再由同事务读取版本快照，避免并发操作错误显示为找不到。
// 参数 ctx 为请求上下文，tx 为变更事务，id 和 expected 为公告及页面所见版本。
// 返回值 成功锁定正确版本时为 nil，否则为不存在、版本冲突或查询错误。
func lockVersion(ctx context.Context, tx pgx.Tx, id pgtype.UUID, expected int64) error {
	var version int64
	err := tx.QueryRow(ctx, `SELECT version FROM local_publications WHERE id=$1 FOR UPDATE`, id).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return errMissing
	}
	if err == nil && version != expected {
		return errConflict
	}
	return err
}

// applyContent 将经校验的标题、发布单位、正文和展示属性应用到当前修订。
// 参数 out 为当前公告，input 为待保存的完整内容。
// 返回值 无。
func applyContent(out *detail, input content) {
	out.Category, out.Title, out.Publisher = input.Category, input.Title, input.Publisher
	out.Pinned, out.Body, out.AlertID = input.Pinned, input.Body, input.AlertID
	if input.Audience != nil {
		out.Audience, out.Targets = *input.Audience, input.Targets
	}
}

// updateState 推进公告版本，并仅在显式确认时切换运营者公开快照。
// 参数 ctx 为请求上下文，tx 为持锁事务，id 为公告标识，action 为操作，out 接收状态。
// 返回值 更新成功时为 nil，否则返回写入错误。
func updateState(ctx context.Context, tx pgx.Tx, id pgtype.UUID, action string, out *detail) error {
	return tx.QueryRow(ctx, `UPDATE local_publications SET version=version+1,updated_at=clock_timestamp(),
		status=CASE $2 WHEN 'publish' THEN 'published' WHEN 'withdraw' THEN 'withdrawn' ELSE status END,
		published_version=CASE $2 WHEN 'publish' THEN version+1 WHEN 'withdraw' THEN NULL ELSE published_version END,
		published_at=CASE $2 WHEN 'publish' THEN clock_timestamp() ELSE published_at END WHERE id=$1
		RETURNING version,status,published_version,published_at,updated_at`, id, action).
		Scan(&out.Version, &out.Status, &out.PublishedVersion, &out.PublishedAt, &out.UpdatedAt)
}

// saveRevision 将实际标题、内容、状态和操作人写入不可覆盖的版本记录。
// 参数 ctx 为请求上下文，tx 为业务事务，item 为该版本公告，action 为留痕用途。
// 返回值 历史写入错误，成功时为 nil。
func (s *Service) saveRevision(ctx context.Context, tx pgx.Tx, item detail, action string) error {
	if item.AlertID != nil {
		var alertID string
		err := tx.QueryRow(ctx, `SELECT id::text FROM local_alerts WHERE id=$1 FOR KEY SHARE`, *item.AlertID).Scan(&alertID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errInvalid
		}
		if err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO publication_events
		(publication_id,version,action,status,category,title,publisher,body,pinned,alert_id,operator,audience,targets)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,COALESCE($13::jsonb,'[]'::jsonb))`, item.ID, item.Version, action, item.Status,
		item.Category, item.Title, item.Publisher, item.Body, item.Pinned, item.AlertID, s.operator,
		item.Audience, item.Targets)
	if err != nil {
		return err
	}
	return saveAttachmentManifest(ctx, tx, item)
}
