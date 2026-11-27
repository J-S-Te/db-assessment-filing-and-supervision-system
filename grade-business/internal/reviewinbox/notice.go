package reviewinbox

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pageSize = 20

// Notice 描述随业务事件一起提交的本地站内意见。
type Notice struct {
	Audience, Resource, ResourceID, State, Recipient, Reference, Subject, Comment string
	Version                                                                       int64
}

// Deliver 在调用者的事务中登记站内意见，同版本对同接收方只投递一次。
// 参数 ctx 为操作上下文，tx 为业务事务，n 为已经确定归属的消息内容。
// 返回值 登记错误，成功或相同消息已登记时为 nil。
func Deliver(ctx context.Context, tx pgx.Tx, n Notice) error {
	_, err := tx.Exec(ctx, `INSERT INTO review_notices
		(audience,resource,resource_id,event_version,state,recipient,reference,subject,comment)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT DO NOTHING`,
		n.Audience, n.Resource, n.ResourceID, n.Version, n.State, n.Recipient, n.Reference, n.Subject, n.Comment)
	return err
}

// DeliverReminder 按业务日期登记运营者整改催办，同一接收方每日最多一条。
// 参数 ctx 为请求上下文，tx 为已锁定整改记录的业务事务，n 为意见内容，day 为业务日期。
// 返回值 已送达消息标识、本次是否首次送达，以及写入错误。
func DeliverReminder(ctx context.Context, tx pgx.Tx, n Notice, day string) (string, bool, error) {
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO review_notices
		(audience,resource,resource_id,event_version,state,recipient,reference,subject,comment,reminder_day)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::date)
		ON CONFLICT (audience,resource,resource_id,recipient,reminder_day)
		WHERE reminder_day IS NOT NULL DO NOTHING RETURNING id::text`,
		n.Audience, n.Resource, n.ResourceID, n.Version, n.State, n.Recipient,
		n.Reference, n.Subject, n.Comment, day).Scan(&id)
	if err != pgx.ErrNoRows {
		return id, err == nil, err
	}
	err = tx.QueryRow(ctx, `SELECT id::text FROM review_notices
		WHERE audience=$1 AND resource=$2 AND resource_id=$3 AND recipient=$4 AND reminder_day=$5::date`,
		n.Audience, n.Resource, n.ResourceID, n.Recipient, day).Scan(&id)
	return id, false, err
}

type message struct {
	ID             string     `json:"id"`
	Resource       string     `json:"resource"`
	ResourceID     string     `json:"resourceId"`
	Reference      string     `json:"reference"`
	Subject        string     `json:"subject"`
	State          string     `json:"state"`
	Comment        string     `json:"comment,omitempty"`
	Status         *string    `json:"status,omitempty"`
	SentAt         time.Time  `json:"sentAt"`
	AcknowledgedAt *time.Time `json:"acknowledgedAt,omitempty"`
	Sequence       int64      `json:"-"`
}

type inboxPage struct {
	Items      []message `json:"items"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

type delivery struct {
	ID             string     `json:"id"`
	Recipient      string     `json:"recipient"`
	State          string     `json:"state"`
	SentAt         time.Time  `json:"sentAt"`
	AcknowledgedAt *time.Time `json:"acknowledgedAt,omitempty"`
}

type receipt struct {
	AcknowledgedAt time.Time `json:"acknowledgedAt"`
}

// 仅凭机构自填单位名称投递的历史退回与材料异常意见不应向运营者展示。
const trustedFilingNotice = `(n.audience<>'operator' OR (
	    (n.resource<>'filings' OR n.state<>'returned'
	      OR EXISTS (SELECT 1 FROM business_cases c WHERE c.id=n.resource_id AND c.kind='filing'
	        AND (c.operator_origin OR c.directory_unit_id IS NOT NULL OR c.status IN ('approved','withdrawn'))))
	    AND (n.resource<>'material-issues' OR NOT EXISTS (
	      SELECT 1 FROM material_issues i JOIN business_cases c ON c.id=i.case_id
	      WHERE i.id=n.resource_id AND i.case_kind='filing' AND NOT c.operator_origin
	        AND c.directory_unit_id IS NULL AND c.status NOT IN ('approved','withdrawn')))))`

const listSQL = `
	WITH selected AS (
	    SELECT * FROM review_notices n WHERE audience=$1 AND recipient=$2
	      AND ` + trustedFilingNotice + `
	      AND ($3::bigint=0 OR sequence<$3) ORDER BY sequence DESC LIMIT $4
	)
	SELECT n.id::text,n.resource,n.resource_id::text,n.reference,n.subject,n.state,n.comment,
	    CASE
	        WHEN issue.id IS NOT NULL THEN CASE WHEN issue.closed_at IS NULL THEN 'open' ELSE 'closed' END
	        WHEN expiry.id IS NOT NULL THEN expiry.date_status
	        WHEN due_filing.id IS NOT NULL THEN CASE
	            WHEN due_record.closed_at IS NOT NULL THEN 'closed'
	            WHEN due_filing.status<>'approved' OR due_filing.cancelled_at IS NOT NULL THEN 'suspended'
	            WHEN due_record.due_on < (current_timestamp AT TIME ZONE 'Asia/Shanghai')::date THEN 'expired'
	            WHEN due_record.due_on = (current_timestamp AT TIME ZONE 'Asia/Shanghai')::date THEN 'due'
	            WHEN due_record.due_on <= (current_timestamp AT TIME ZONE 'Asia/Shanghai')::date + due_record.warning_days THEN 'due_soon'
	            ELSE 'upcoming' END
	        WHEN n.resource='supervision-site' THEN
	            CASE WHEN supervision.organization_name=n.recipient AND supervision_scope.current_owner THEN site.status
	                 WHEN supervision.merge_history_scope=n.recipient THEN site.merge_history_snapshot->>'status' END
	        WHEN supervision.id IS NOT NULL THEN
	            CASE WHEN supervision.merge_history_scope=n.recipient THEN
	                   CASE WHEN left(n.state,5)='site_' THEN site.merge_history_snapshot->>'status'
	                        ELSE supervision.merge_history_snapshot->>'status' END
	                 WHEN left(n.state,5)='site_' THEN site.status ELSE supervision.status END
	        WHEN (n.audience='institution' AND filing.assessment_org=n.recipient)
	          OR (n.audience='operator' AND filing.organization_name=n.recipient) THEN
	            CASE WHEN filing.cancelled_at IS NULL THEN filing.status ELSE 'cancelled' END
	        WHEN n.audience='institution' AND a.before_values->>'assessmentOrg'=n.recipient THEN a.status
	        WHEN n.audience='institution' AND i.assessment_org=n.recipient THEN i.status
	        WHEN n.audience='institution' AND assessment.assessment_org=n.recipient THEN assessment.status
	        WHEN n.audience='institution' AND n.resource='hazards' AND hc.assessment_org=n.recipient THEN h.status
        WHEN n.audience='institution' AND n.resource='alerts' AND hc.can_retest
          AND hc.assessment_org COLLATE "C"=n.recipient THEN alert.status
	        WHEN n.audience='operator' AND a.before_values->>'organizationName'=n.recipient THEN a.status
	        WHEN n.audience='operator' AND COALESCE(i.frozen_organization_name,implementation_filing.organization_name)=n.recipient THEN i.status
	        WHEN n.audience='operator' AND hc.organization_name=n.recipient THEN COALESCE(alert.status,h.status)
	        WHEN n.audience='operator' AND COALESCE(m.frozen_organization_name,f.organization_name)=n.recipient THEN m.status
	    END,
	    n.sent_at,n.acknowledged_at,n.sequence
	FROM selected n
	LEFT JOIN business_cases filing ON n.resource='filings' AND filing.id=n.resource_id AND filing.kind='filing'
	LEFT JOIN business_cases assessment ON n.resource='reports' AND assessment.id=n.resource_id AND assessment.kind='report'
	LEFT JOIN material_issues issue ON n.resource='material-issues' AND issue.id=n.resource_id
	LEFT JOIN supervision_tasks supervision ON n.resource IN ('supervisions','supervision-site')
	    AND supervision.id=n.resource_id AND (supervision.organization_name COLLATE "C"=n.recipient
	      OR supervision.merge_history_scope COLLATE "C"=n.recipient)
	LEFT JOIN supervision_site_inspections site ON site.task_id=supervision.id
	LEFT JOIN supervision_filing_scope supervision_scope ON n.resource='supervision-site'
	    AND supervision_scope.task_id=supervision.id
	LEFT JOIN material_expiry_status expiry ON n.resource='material-expiries'
	    AND expiry.id=n.resource_id AND expiry.scope_current AND (
	      (n.audience='operator' AND expiry.organization_name COLLATE "C"=n.recipient) OR
	      (n.audience='institution' AND expiry.institution_name COLLATE "C"=n.recipient)
	    )
	LEFT JOIN assessment_due_records due_record ON n.resource='assessment-dues' AND due_record.id=n.resource_id
	LEFT JOIN business_cases due_filing ON due_filing.id=due_record.filing_id AND (
	    (n.audience='operator' AND due_filing.organization_name COLLATE "C"=n.recipient) OR
	    (n.audience='institution' AND due_filing.assessment_org COLLATE "C"=n.recipient)
	)
	LEFT JOIN filing_applications a ON n.resource='filing-applications' AND a.id=n.resource_id
	LEFT JOIN implementation_reports i ON n.resource='implementations' AND i.id=n.resource_id
	LEFT JOIN business_cases implementation_filing ON implementation_filing.id=i.filing_id
	LEFT JOIN local_alerts alert ON n.resource='alerts' AND alert.id=n.resource_id
	LEFT JOIN hazard_items h ON h.id=CASE WHEN n.resource='hazards' THEN n.resource_id ELSE alert.hazard_id END
	    AND h.status<>'pending_dispatch'
	LEFT JOIN hazard_context hc ON hc.id=h.id
	LEFT JOIN remediation_plans m ON n.resource='remediations' AND m.id=n.resource_id
	LEFT JOIN business_cases r ON r.id=m.report_id
	LEFT JOIN business_cases f ON f.id=r.filing_id
	ORDER BY n.sequence DESC
`

// listInbox 读取指定接收方的一页站内意见与仍属其办理范围的当前状态。
// 参数 ctx 为请求上下文，pool 为业务库，audience、recipient 和 before 指定范围及游标。
// 返回值 有界收件列表，或数据库读取错误。
func listInbox(ctx context.Context, pool *pgxpool.Pool, audience, recipient string, before int64) (inboxPage, error) {
	rows, err := pool.Query(ctx, listSQL, audience, recipient, before, pageSize+1)
	if err != nil {
		return inboxPage{}, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[message])
	if err != nil {
		return inboxPage{}, err
	}
	result := inboxPage{Items: items}
	if len(items) > pageSize {
		result.Items = items[:pageSize]
		result.NextCursor = strconv.FormatInt(items[pageSize-1].Sequence, 10)
	}
	return result, nil
}
