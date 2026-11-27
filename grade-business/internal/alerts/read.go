package alerts

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type summaryReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

const alertSourceJoins = ` JOIN hazard_items h ON h.id=a.hazard_id AND h.status<>'pending_dispatch'
	JOIN hazard_context hc ON hc.id=h.id JOIN business_cases f ON f.id=hc.filing_id`

const alertBusinessDateSQL = `(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date`

const listFields = `a.id::text,a.hazard_id::text,a.kind,a.severity,hc.system_name,hc.organization_name,
	to_char(h.deadline,'YYYY-MM-DD'),a.status,a.triggered_at,a.closed_at,
	CASE WHEN a.deadline<>h.deadline THEN to_char(a.deadline,'YYYY-MM-DD') END,
	CASE WHEN a.deadline<>h.deadline THEN h.status<>'closed'
	  AND h.deadline<` + alertBusinessDateSQL + ` END`

const dispositionJSON = `jsonb_strip_nulls(jsonb_build_object(
	'id',d.id,'action',d.action,'status',d.status,'comment',NULLIF(d.comment,''),
	'requestedDeadline',to_char(d.requested_deadline,'YYYY-MM-DD'),'requestedBy',d.requested_by,
	'requesterAudience',d.requester_audience,'requestedAt',d.requested_at,
	'decisionComment',d.decision_comment,'decidedBy',d.decided_by,'decidedAt',d.decided_at))`

// scanTargets 提供预警摘要各列的查询目标。
// 参数 item 为要填充的预警摘要。
// 返回值 与 listFields 对齐的字段指针。
func scanTargets(item *alertItem) []any {
	return []any{
		&item.ID, &item.HazardID, &item.Type, &item.Severity, &item.SystemName,
		&item.OrganizationName, &item.Deadline, &item.Status, &item.TriggeredAt, &item.ClosedAt,
		&item.TriggeredDeadline, &item.CurrentlyOverdue,
	}
}

// list 返回符合实际筛选条件的一页已生成预警。
// 参数 ctx 控制查询，filters 为已验证的列表筛选与分页条件。
// 返回值 有序预警摘要和可选的下一页游标。
func (s *Service) list(ctx context.Context, filters listFilter) (alertPage, error) {
	at := pgtype.Timestamptz{Time: filters.cursor.at, Valid: filters.cursor.id.Valid}
	scope := currentAlertScope(ctx)
	rows, err := s.db.Query(ctx, `SELECT `+listFields+` FROM local_alerts a`+alertSourceJoins+`
		WHERE ($1::text='' OR a.status=$1) AND ($2::text='' OR a.severity=$2)
		  AND ($3::text='' OR strpos(lower(concat_ws(' ',hc.system_name,hc.organization_name)),lower($3))>0)
		  AND ($4::timestamptz IS NULL OR (a.triggered_at,a.id)<($4,$5::uuid))
		  AND ($7::text='' OR ($7='operator' AND hc.organization_name COLLATE "C"=$10)
		    OR ($7='institution' AND hc.can_retest AND hc.assessment_org COLLATE "C"=$10))
		  AND ($8::text='' OR a.kind=$8)
		  AND ($9::text='' OR EXISTS (SELECT 1 FROM local_alert_dispositions d WHERE d.alert_id=a.id AND d.status='pending'))
		ORDER BY a.triggered_at DESC,a.id DESC LIMIT $6`,
		filters.status, filters.severity, filters.query, at, filters.cursor.id, filters.limit+1,
		scope.audience, filters.kind, filters.review, scope.recipient)
	if err != nil {
		return alertPage{}, err
	}
	defer rows.Close()
	result := alertPage{Items: make([]alertItem, 0, filters.limit)}
	for rows.Next() {
		if len(result.Items) == filters.limit {
			result.NextCursor = encodeCursor(result.Items[len(result.Items)-1])
			break
		}
		var item alertItem
		if err := rows.Scan(scanTargets(&item)...); err != nil {
			return alertPage{}, err
		}
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}

// detail 在同一查询快照读取预警状态、当前隐患概况和有序处置历史。
// 参数 ctx 控制读取，id 为预警标识。
// 返回值 当前详情及可执行动作，或记录不存在及查询错误。
func (s *Service) detail(ctx context.Context, id pgtype.UUID) (alertDetail, error) {
	var item alertDetail
	var pendingAlert, today string
	var converted bool
	scope := currentAlertScope(ctx)
	row := s.db.QueryRow(ctx, `SELECT `+listFields+`,a.version,h.status,h.description,
		COALESCE((SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
			'action',e.action,'fromStatus',e.from_status,'toStatus',e.to_status,'operator',e.operator,
			'comment',NULLIF(e.comment,''),'occurredAt',e.occurred_at,
			'dispositionAction',d.action,'requestedDeadline',to_char(d.requested_deadline,'YYYY-MM-DD')
		)) ORDER BY e.version) FROM local_alert_events e LEFT JOIN local_alert_dispositions d
		  ON d.alert_id=e.alert_id AND (d.requested_version=e.version OR d.decision_version=e.version)
		  WHERE e.alert_id=a.id),'[]'::jsonb),
		(SELECT `+dispositionJSON+` FROM local_alert_dispositions d
		  WHERE d.alert_id=a.id ORDER BY requested_version DESC LIMIT 1),
		COALESCE((SELECT d.alert_id::text FROM local_alert_dispositions d WHERE d.hazard_id=h.id AND d.status='pending'),''),
		EXISTS(SELECT 1 FROM local_alert_dispositions d WHERE d.hazard_id=h.id AND d.action='convert'),
		to_char(`+alertBusinessDateSQL+`,'YYYY-MM-DD')
		FROM local_alerts a`+alertSourceJoins+` WHERE a.id=$1
		  AND ($2::text='' OR ($2='operator' AND hc.organization_name COLLATE "C"=$3)
		    OR ($2='institution' AND hc.can_retest AND hc.assessment_org COLLATE "C"=$3))`, id, scope.audience, scope.recipient)
	err := row.Scan(append(scanTargets(&item.alertItem), &item.Version, &item.HazardStatus, &item.Description,
		&item.History, &item.Disposition, &pendingAlert, &converted, &today)...)
	if err != nil {
		return alertDetail{}, err
	}
	if scope.audience == "" && pendingAlert == item.ID && item.Disposition != nil && item.Disposition.Action == actionExtension {
		item.ApprovalBlockedReason = extensionApprovalBlockedReason(item.HazardStatus, item.Deadline, today, item.Disposition.RequestedDeadline)
	}
	item.AllowedActions = detailActions(item, pendingAlert, converted, scope.audience)
	return item, nil
}

// detailActions 在现有告警状态之上施加待审、隐患状态和重复转办约束。
// 参数 item 为当前详情，pendingAlert 为同源待审申请所属预警，converted 为已转办标记，audience 为办理端。
// 返回值 当前可用的真实业务操作列表。
func detailActions(item alertDetail, pendingAlert string, converted bool, audience string) []string {
	if item.Status == statusClosed {
		return []string{}
	}
	if pendingAlert != "" {
		if audience == "" && pendingAlert == item.ID {
			if item.ApprovalBlockedReason != "" {
				return []string{actionReject}
			}
			return []string{actionApprove, actionReject}
		}
		return []string{}
	}
	if audience == "" {
		return allowedActions(item.Status)
	}
	if audience == audienceInstitution {
		if item.Status == statusPending {
			return []string{actionProcess}
		}
		return []string{actionFeedback}
	}
	if item.HazardStatus == statusClosed {
		return []string{actionFalseAlarm}
	}
	if converted {
		return []string{actionExtension, actionFalseAlarm}
	}
	return []string{actionConvert, actionExtension, actionFalseAlarm}
}

// ReadSummary 汇总本地站内预警的真实状态计数。
// 参数 ctx 控制查询，db 为当前业务数据库或读取事务。
// 返回值 全部及各状态数量，或数据库查询错误。
func ReadSummary(ctx context.Context, db summaryReader) (Summary, error) {
	var result Summary
	err := db.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE status='pending'),
		count(*) FILTER (WHERE status='processing'),count(*) FILTER (WHERE status='closed'),
		(SELECT count(*) FROM local_alert_dispositions WHERE status='pending')
		FROM local_alerts`).Scan(&result.Total, &result.Pending, &result.Processing, &result.Closed, &result.PendingDispositions)
	return result, err
}
