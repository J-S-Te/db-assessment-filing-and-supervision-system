package publications

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const visibleJoin = ` FROM local_publications p JOIN publication_events e
	ON e.publication_id=p.id AND e.version=CASE WHEN $1::boolean THEN p.published_version ELSE p.version END
	LEFT JOIN publication_deliveries d ON $1::boolean AND d.publication_id=p.id
	AND d.version=e.version AND d.audience=$2 AND d.recipient=$3`

const visibleWhere = ` WHERE (NOT $1::boolean OR d.publication_id IS NOT NULL
	OR ($3='' AND e.audience='all' AND e.category NOT IN ('policy_standard','common_material')))`

const visibleFields = `p.id::text,e.category,e.title,e.publisher,e.pinned,p.published_at,
	p.status,e.version,p.published_version,CASE WHEN $1::boolean THEN p.published_at ELSE p.updated_at END,
	e.sequence,e.audience,d.delivered_at,d.read_at,d.acknowledged_at`

var detailFields = visibleFields + `,e.body,CASE WHEN $1::boolean THEN NULL ELSE e.alert_id::text END,
	CASE WHEN $1::boolean THEN NULL ELSE e.targets END,
	CASE WHEN NOT $1::boolean OR d.publication_id IS NOT NULL THEN ` + attachmentSnapshot("e.publication_id,e.version") + ` END`

// readList 按置顶与最新事件分页读取发布列表，资料库同时返回当前送达版本的附件。
// 参数 ctx 为请求上下文，query 为有效筛选，public 表示按已发布快照读取。
// 返回值 公告摘要页，或查询错误。
func (s *Service) readList(ctx context.Context, query listFilter, public bool) (page[summary], error) {
	attachments := `,CASE WHEN $11::boolean AND d.publication_id IS NOT NULL THEN ` +
		attachmentSnapshot("e.publication_id,e.version") + ` END`
	rows, err := s.db.Query(ctx, "SELECT "+visibleFields+attachments+visibleJoin+visibleWhere+`
		AND ($4='' OR e.category=$4) AND ($5='' OR p.status=$5)
		AND ($6='' OR strpos(lower(concat_ws(' ',e.title,e.publisher,e.body)),lower($6))>0)
		AND ($7::bigint=0 OR (e.pinned,e.sequence)<($8::boolean,$7))
		AND ($9='' OR ($9='read' AND d.read_at IS NOT NULL) OR ($9='unread' AND d.read_at IS NULL))
		AND (NOT $11::boolean OR (d.publication_id IS NOT NULL AND e.category IN ('policy_standard','common_material')))
		ORDER BY e.pinned DESC,e.sequence DESC LIMIT $10`, public, query.scope.Audience,
		query.scope.Recipient, query.category, query.status, query.query, query.before,
		query.pinned, query.read, pageLimit+1, query.materials)
	if err != nil {
		return page[summary]{}, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[summary])
	if err != nil {
		return page[summary]{}, err
	}
	out := page[summary]{Items: items}
	if len(items) > pageLimit {
		out.Items = items[:pageLimit]
		last := items[pageLimit-1]
		flag := "0:"
		if last.Pinned {
			flag = "1:"
		}
		out.NextBefore = flag + strconv.FormatInt(last.Sequence, 10)
	}
	return out, nil
}

// readDetail 返回公告正文和当前端可用信息。
// 参数 ctx 为请求上下文，id 为公告标识，scope 为门户角色和可选收件方。
// 返回值 公告详情，或记录无权展示、不存在及查询错误。
func (s *Service) readDetail(ctx context.Context, id pgtype.UUID, scope recipient) (detail, error) {
	public := scope.Audience != ""
	row := s.db.QueryRow(ctx, "SELECT "+detailFields+visibleJoin+visibleWhere+" AND p.id=$4",
		public, scope.Audience, scope.Recipient, id)
	out, err := scanDetail(row)
	if err == nil && !public {
		out.AllowedActions = available(out)
	}
	return out, err
}

// scanDetail 解码当前公告摘要和同版本正文。
// 参数 row 为使用公告公共投影视图的结果行。
// 返回值 可公开的详情，或不存在及数据库错误。
func scanDetail(row pgx.Row) (detail, error) {
	var out detail
	err := row.Scan(&out.ID, &out.Category, &out.Title, &out.Publisher, &out.Pinned, &out.PublishedAt,
		&out.Status, &out.Version, &out.PublishedVersion, &out.UpdatedAt, &out.Sequence, &out.Audience,
		&out.DeliveredAt, &out.ReadAt, &out.AcknowledgedAt, &out.Body, &out.AlertID, &out.Targets, &out.Attachments)
	if errors.Is(err, pgx.ErrNoRows) {
		return detail{}, errMissing
	}
	return out, err
}

// available 根据当前状态和已公开版本给出监管可确认的操作。
// 参数 item 为当前草稿和线上快照状态。
// 返回值 监管端可用操作。
func available(item detail) []string {
	actions := []string{"edit"}
	if item.PublishedVersion == nil || item.Version != *item.PublishedVersion {
		actions = append(actions, "publish")
	}
	if item.Status == published {
		actions = append(actions, "withdraw")
	}
	return actions
}

// readHistory 读取单条公告的版本及发布历史。
// 参数 ctx 为请求上下文，id 为公告标识，before 为排除边界的版本游标。
// 返回值 监管端版本历史，或公告不存在及数据库错误。
func (s *Service) readHistory(ctx context.Context, id pgtype.UUID, before int64) (page[revision], error) {
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM local_publications WHERE id=$1)`, id).Scan(&exists); err != nil {
		return page[revision]{}, err
	}
	if !exists {
		return page[revision]{}, errMissing
	}
	rows, err := s.db.Query(ctx, `SELECT version,action,status,category,title,publisher,body,pinned,alert_id::text,
		audience,targets,operator,occurred_at,`+attachmentSnapshot("e.publication_id,e.version")+`
		FROM publication_events e WHERE publication_id=$1 AND ($2::bigint=0 OR version<$2)
		ORDER BY version DESC LIMIT $3`, id, before, pageLimit+1)
	if err != nil {
		return page[revision]{}, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[revision])
	if err != nil {
		return page[revision]{}, err
	}
	out := page[revision]{Items: items}
	if len(items) > pageLimit {
		out.Items = items[:pageLimit]
		out.NextBefore = strconv.FormatInt(items[pageLimit-1].Version, 10)
	}
	return out, nil
}
