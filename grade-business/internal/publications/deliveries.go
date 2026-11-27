package publications

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const rosterLimit = 30

// validateRecipients 确认草稿指定的收件方目前可在业务名录中选择。
// 参数 ctx 为请求上下文，tx 为写入事务，targets 为规范化且已去重的明确目标。
// 返回值 收件方失效时为输入错误，否则为查询错误或 nil。
func validateRecipients(ctx context.Context, tx pgx.Tx, targets []recipient) error {
	if len(targets) == 0 {
		return nil
	}
	var unavailable bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM jsonb_to_recordset($1::jsonb) target(audience text,recipient text)
		WHERE NOT EXISTS (SELECT 1 FROM publication_recipient_roster roster
			WHERE (roster.audience,roster.recipient)=(target.audience,target.recipient)))`,
		targets).Scan(&unavailable)
	if err == nil && unavailable {
		return errInvalid
	}
	return err
}

// deliverPublished 将发布版本按事务内的有效名单投递给每个独立角色与名称。
// 参数 ctx 为请求上下文，tx 为发布事务，item 为已写入历史的发布版本。
// 返回值 数据库错误，或定向名单失效时的输入错误，成功时为 nil。
func deliverPublished(ctx context.Context, tx pgx.Tx, item detail) error {
	if err := lockPublicationDirectory(ctx, tx, item); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `INSERT INTO publication_deliveries
		(publication_id,version,audience,recipient,delivered_at,recipient_directory_id,recipient_display_name)
		SELECT $1,$2,roster.audience,roster.recipient,$3,roster.directory_id,
			NULLIF(roster.display_name,roster.recipient)
		FROM publication_recipient_roster roster
		WHERE $4='all' OR roster.audience=$4 OR ($4='selected' AND EXISTS (
			SELECT 1 FROM jsonb_to_recordset($5::jsonb) target(audience text,recipient text)
			WHERE (target.audience,target.recipient)=(roster.audience,roster.recipient)))`,
		item.ID, item.Version, item.PublishedAt, item.Audience, item.Targets)
	if err == nil && item.Audience == selectedAudience && result.RowsAffected() != int64(len(item.Targets)) {
		return errInvalid
	}
	return err
}

// lockPublicationDirectory 与名录状态写入串行，随后由发布语句复查收件资格和名称。
// 参数 ctx 和 tx 为发布事务，item 为已固定的发布范围及目标。
// 返回值 加锁错误，或相应的名录行已锁定。
func lockPublicationDirectory(ctx context.Context, tx pgx.Tx, item detail) error {
	_, err := tx.Exec(ctx, `SELECT p.id FROM directory_parties p
		CROSS JOIN LATERAL (VALUES (CASE p.kind WHEN 'unit' THEN 'operator' ELSE 'institution' END)) r(audience)
		WHERE $1='all' OR $1=r.audience OR ($1='selected' AND EXISTS (
			SELECT 1 FROM jsonb_to_recordset($2::jsonb) t(audience text,recipient text)
			WHERE (t.audience,t.recipient)=(r.audience,p.scope_name)))
		ORDER BY p.id FOR SHARE OF p`, item.Audience, item.Targets)
	return err
}

// readRecipients 查询监管端可选择的当前有效收件方。
// 参数 ctx 为请求上下文，audience 为收件角色，query 为名称关键词。
// 返回值 按名称排序的有限收件方，或数据库错误。
func (s *Service) readRecipients(ctx context.Context, audience, query string) (page[recipientOption], error) {
	rows, err := s.db.Query(ctx, `SELECT audience,recipient,
			CASE WHEN display_name=recipient THEN '' ELSE display_name END
		FROM publication_recipient_roster
		WHERE audience=$1 AND ($2='' OR strpos(lower(recipient),lower($2))>0
			OR strpos(lower(display_name),lower($2))>0)
		ORDER BY display_name,recipient LIMIT $3`, audience, query, rosterLimit)
	if err != nil {
		return page[recipientOption]{}, err
	}
	items, err := pgx.CollectRows(rows, scanRecipientOption)
	return page[recipientOption]{Items: items}, err
}

// scanRecipientOption 读取有限候选字段，范围键独立于当前显示名称。
// 参数 row 为名录候选结果行。
// 返回值 当前收件选项及扫描错误。
func scanRecipientOption(row pgx.CollectableRow) (recipientOption, error) {
	var item recipientOption
	err := row.Scan(&item.Audience, &item.Recipient, &item.DisplayName)
	return item, err
}

// canonicalRecipient 读取名录固定范围，即使实体后来改名或停用也能查原回执。
// 参数 ctx 控制查询，scope 为门户给出的角色和名称。
// 返回值 已登记实体的固定范围、原有未登记范围，或查询错误。
func (s *Service) canonicalRecipient(ctx context.Context, scope recipient) (recipient, error) {
	if scope.Recipient == "" {
		return scope, nil
	}
	var name string
	err := s.db.QueryRow(ctx, `SELECT scope_name FROM directory_parties
		WHERE kind=CASE WHEN $1='operator' THEN 'unit' ELSE 'institution' END
		  AND (lower(scope_name)=lower($2) OR lower(name)=lower($2))
		ORDER BY (lower(scope_name)=lower($2)) DESC,id LIMIT 1`,
		scope.Audience, scope.Recipient).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return scope, nil
	}
	scope.Recipient = name
	return scope, err
}

// readDeliveries 返回一次正式发布的全量统计和已筛选的收件回执页。
// 参数 ctx 为请求上下文，id 为公告标识，filter 为角色、名称和版本分页条件。
// 返回值 同一发布版本的统计与回执，或发布版本不存在和数据库错误。
func (s *Service) readDeliveries(ctx context.Context, id pgtype.UUID, filter deliveryFilter) (receiptPage, error) {
	rows, err := s.db.Query(ctx, `WITH publication_version AS MATERIALIZED (
		SELECT publication_id,version,`+attachmentSnapshot("e.publication_id,e.version")+` AS attachments
		FROM publication_events e WHERE publication_id=$1 AND action='publish' AND ($2::bigint=0 OR version=$2)
		ORDER BY version DESC LIMIT 1
	)
	SELECT v.version,counts.delivered,counts.read,counts.acknowledged,coalesce(d.sequence,0),
		coalesce(d.audience,''),coalesce(d.recipient,''),coalesce(d.recipient_display_name,''),
		d.delivered_at,d.read_at,d.acknowledged_at,v.attachments
	FROM publication_version v
	CROSS JOIN LATERAL (
		SELECT count(*) AS delivered,count(read_at) AS read,count(acknowledged_at) AS acknowledged
		FROM publication_deliveries WHERE (publication_id,version)=(v.publication_id,v.version)
	) counts
	LEFT JOIN LATERAL (
		SELECT sequence,audience,recipient,recipient_display_name,delivered_at,read_at,acknowledged_at
		FROM publication_deliveries
		WHERE (publication_id,version)=(v.publication_id,v.version)
		AND ($3='' OR audience=$3) AND ($4='' OR strpos(lower(recipient),lower($4))>0
			OR strpos(lower(recipient_display_name),lower($4))>0)
		AND ($5::bigint=0 OR sequence<$5)
		ORDER BY sequence DESC LIMIT $6
	) d ON true
	ORDER BY d.sequence DESC`, id, filter.version, filter.audience, filter.query, filter.before, pageLimit+1)
	if err != nil {
		return receiptPage{}, err
	}
	defer rows.Close()
	return collectDeliveries(rows)
}

// collectDeliveries 读取一次发布的统计和收件明细，并生成绑定该版本的游标。
// 参数 rows 为一次 SQL 查询取得的发布版本和已筛选回执。
// 返回值 明细页，或指定发布版本不存在及读行错误。
func collectDeliveries(rows pgx.Rows) (receiptPage, error) {
	out := receiptPage{page: page[delivery]{Items: []delivery{}}}
	for rows.Next() {
		var item delivery
		err := rows.Scan(&out.Version, &out.Counts.Delivered, &out.Counts.Read,
			&out.Counts.Acknowledged, &item.sequence, &item.Audience, &item.Recipient, &item.DisplayName,
			&item.DeliveredAt, &item.ReadAt, &item.AcknowledgedAt, &out.Attachments)
		if err != nil {
			return receiptPage{}, err
		}
		if item.sequence != 0 {
			out.Items = append(out.Items, item)
		}
	}
	if err := rows.Err(); err != nil {
		return receiptPage{}, err
	}
	if out.Version == 0 {
		return receiptPage{}, errMissing
	}
	if len(out.Items) > pageLimit {
		last := out.Items[pageLimit-1]
		out.Items = out.Items[:pageLimit]
		out.NextBefore = strconv.FormatInt(out.Version, 10) + ":" + strconv.FormatInt(last.sequence, 10)
	}
	return out, nil
}

// updateReceipt 确认当前收件方和实际发布版本并记录已读或签收。
// 参数 ctx 为请求上下文，id 为公告，expected 为读者所见发布版本，scope 为收件名称，ack 表示显式签收。
// 返回值 首次已读和签收时间，或未送达、版本冲突及写入错误。
func (s *Service) updateReceipt(ctx context.Context, id pgtype.UUID, expected int64,
	scope recipient, ack bool,
) (readState, error) {
	var out readState
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		version, err := checkLiveReceipt(ctx, tx, id, expected, scope)
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, `UPDATE publication_deliveries
			SET read_at=coalesce(read_at,greatest(statement_timestamp(),delivered_at)),
				acknowledged_at=CASE WHEN $5 THEN coalesce(acknowledged_at,
					greatest(statement_timestamp(),read_at,delivered_at)) ELSE acknowledged_at END
			WHERE (publication_id,version,audience,recipient)=($1,$2,$3,$4)
			RETURNING read_at,acknowledged_at`, id, version, scope.Audience, scope.Recipient, ack).
			Scan(&out.ReadAt, &out.AcknowledgedAt)
	})
	return out, err
}

// checkLiveReceipt 持有公告共享锁后，核对读者是否实际收到当前发布版本。
// 参数 ctx 为请求上下文，tx 为回执事务，id 为公告，expected 为页面版本，scope 为门户收件方。
// 返回值 可更新的当前发布版本，或不存在、版本冲突及数据库错误。
func checkLiveReceipt(ctx context.Context, tx pgx.Tx, id pgtype.UUID, expected int64,
	scope recipient,
) (int64, error) {
	var version *int64
	err := tx.QueryRow(ctx, `SELECT published_version FROM local_publications WHERE id=$1 FOR SHARE`,
		id).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && version == nil) {
		return 0, errMissing
	}
	if err != nil {
		return 0, err
	}
	var exists bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM publication_deliveries
		WHERE (publication_id,version,audience,recipient)=($1,$2,$3,$4))`,
		id, *version, scope.Audience, scope.Recipient).Scan(&exists)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, errMissing
	}
	if *version != expected {
		return 0, errConflict
	}
	return *version, nil
}

// readUnreadCount 统计收件方没有标记已读的当前公开版本。
// 参数 ctx 为请求上下文，scope 为当前门户角色与名称。
// 返回值 未读数量，或查询错误。
func (s *Service) readUnreadCount(ctx context.Context, scope recipient) (int64, error) {
	var count int64
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM publication_deliveries d
		JOIN local_publications p ON (p.id,p.published_version)=(d.publication_id,d.version)
		WHERE (d.audience,d.recipient)=($1,$2) AND d.read_at IS NULL`, scope.Audience, scope.Recipient).
		Scan(&count)
	return count, err
}

// markAllRead 将该收件方各页仍有效的送达记录标记为已读。
// 参数 ctx 为请求上下文，scope 为当前门户角色与名称。
// 返回值 首次标记已读的记录数，或数据库错误。
func (s *Service) markAllRead(ctx context.Context, scope recipient) (int64, error) {
	result, err := s.db.Exec(ctx, `WITH active AS MATERIALIZED (
		SELECT p.id,p.published_version FROM publication_deliveries d
		JOIN local_publications p ON (p.id,p.published_version)=(d.publication_id,d.version)
		WHERE (d.audience,d.recipient)=($1,$2) AND d.read_at IS NULL
		ORDER BY p.id FOR SHARE OF p
	)
	UPDATE publication_deliveries d SET read_at=greatest(statement_timestamp(),d.delivered_at)
	FROM active WHERE (d.publication_id,d.version)=(active.id,active.published_version)
	AND (d.audience,d.recipient)=($1,$2) AND d.read_at IS NULL`, scope.Audience, scope.Recipient)
	return result.RowsAffected(), err
}
