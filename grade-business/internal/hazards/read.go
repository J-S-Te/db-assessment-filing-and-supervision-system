package hazards

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/access"
)

const hazardColumns = `h.id::text,COALESCE(h.plan_id::text,''),COALESCE(c.report_id::text,''),c.filing_id::text,
	c.filing_number,c.report_number,c.system_name,c.organization_name,
	h.severity,h.description,h.responsibility,to_char(h.deadline,'YYYY-MM-DD'),h.status,h.created_at,
	h.ledger,COALESCE(h.title,''),COALESCE(h.source,''),COALESCE(to_char(h.discovered_on,'YYYY-MM-DD'),''),
	h.dispatched_at,c.assessment_org`

const hazardAncestry = ` JOIN remediation_plans p ON p.id=h.plan_id
	JOIN business_cases report ON report.id=p.report_id AND report.kind='report'
	JOIN business_cases filing ON filing.id=report.filing_id AND filing.kind='filing'`

const hazardJoin = ` FROM hazard_items h JOIN hazard_context c ON c.id=h.id`

const hazardLockedJoin = hazardJoin + `
	JOIN business_cases filing ON filing.id=c.filing_id
	JOIN business_cases anchor ON anchor.id=COALESCE(c.report_id,c.filing_id)`

const lockedRetestInstitution = `COALESCE(h.retest_institution,
	CASE WHEN anchor.kind='report' THEN anchor.assessment_org END,'')`

const lockedRetestAllowed = `(h.retest_institution IS NOT NULL OR
	(h.registration_filing_id IS NULL AND anchor.kind='report' AND anchor.status='approved'))`

const lockedHazardScope = `($2='' OR (h.status<>'pending_dispatch' AND COALESCE(h.frozen_organization_name,filing.organization_name)=$2))
	AND ($3='' OR (h.status<>'pending_dispatch' AND ` + lockedRetestAllowed + ` AND ` + lockedRetestInstitution + `=$3))`

const hazardInstitutionScope = `($3='' OR (h.status<>'pending_dispatch' AND c.can_retest AND c.assessment_org=$3))`
const hazardScope = `($2='' OR (h.status<>'pending_dispatch' AND c.organization_name=$2)) AND ` + hazardInstitutionScope

// scanSummary 返回公开台账字段与查询列一一对应的接收位置。
// 参数 item 为待填充的台账摘要。
// 返回值 可传给 PostgreSQL 扫描方法的字段指针。
func scanSummary(item *hazardSummary) []any {
	return []any{&item.ID, &item.PlanID, &item.ReportID, &item.FilingID, &item.FilingNumber,
		&item.ReportNumber, &item.SystemName, &item.OrganizationName, &item.Severity, &item.Description,
		&item.Responsibility, &item.Deadline, &item.Status, &item.CreatedAt, &item.Ledger, &item.Title,
		&item.Source, &item.DiscoveredOn, &item.DispatchedAt, &item.AssessmentOrg}
}

// list 返回当前范围与筛选条件下的台账记录和稳定游标。
// 参数 ctx 含可选运营单位或机构，filter 为已校验的筛选与分页条件。
// 返回值 当前页的公开台账，或数据库读取错误。
func (s *Service) list(ctx context.Context, filter hazardFilter) (hazardPage, error) {
	rows, err := queryHazards(ctx, s.db, filter, filter.limit+1)
	if err != nil {
		return hazardPage{}, err
	}
	defer rows.Close()
	page, err := collectHazards(rows, filter.limit)
	if hazardInstitution(ctx) == "" {
		for index := range page.Items {
			if page.Items[index].Source == "" {
				page.Items[index].AssessmentOrg = ""
			}
		}
	}
	return page, err
}

// queryHazards 为列表及导出复用同一记录投影、匹配条件和稳定排序。
// 参数 ctx 含可选业务范围，db 为实际数据库，filter 和 limit 为筛选范围及查询上限。
// 返回值 台账查询游标或查询错误，调用者负责关闭游标。
func queryHazards(ctx context.Context, db *pgxpool.Pool, filter hazardFilter, limit int) (pgx.Rows, error) {
	var at *time.Time
	var id, planID, filingID *string
	if filter.cursor.ID != "" {
		at, id = &filter.cursor.CreatedAt, &filter.cursor.ID
	}
	if filter.planID != "" {
		planID = &filter.planID
	}
	if filter.filingID != "" {
		filingID = &filter.filingID
	}
	return db.Query(ctx, hazardListSQL, filter.status, filter.severity, planID, filter.query,
		filter.due, at, id, limit, operatorOrganization(ctx), filingID, hazardInstitution(ctx),
		filter.ledger, access.Districts(ctx))
}

var hazardListSQL = `WITH business_day AS (
	SELECT timezone('Asia/Shanghai',CURRENT_TIMESTAMP)::date AS today
) SELECT ` + hazardColumns + hazardJoin + ` CROSS JOIN business_day d
WHERE ($1='' OR h.status=$1) AND ($2='' OR h.severity=$2)
  AND ($3::uuid IS NULL OR h.plan_id=$3)
  AND ($4='' OR strpos(lower(concat_ws(' ',h.id::text,c.filing_number,c.report_number,c.system_name,
       c.organization_name,h.title,h.description,h.responsibility)),lower($4))>0)
  AND ($5='' OR (h.status NOT IN ('pending_dispatch','closed') AND (
       ($5='overdue' AND h.deadline<d.today) OR
       ($5='within_10_days' AND h.deadline BETWEEN d.today AND d.today+10))))
  AND ($6::timestamptz IS NULL OR (h.created_at,h.id)<($6,$7::uuid))
  AND ($9='' OR (h.status<>'pending_dispatch' AND c.organization_name=$9))
  AND ($10::uuid IS NULL OR c.filing_id=$10)
  AND ($11='' OR (h.status<>'pending_dispatch' AND c.can_retest AND c.assessment_org=$11))
  AND ($12='' OR h.ledger=$12)
` + " AND " + access.FilingDistrictScope("c.filing_id", "$13", false) + `
ORDER BY h.created_at DESC,h.id DESC LIMIT $8`

// collectHazards 组装一页台账并在有后续记录时返回分页游标。
// 参数 rows 为已排序的查询结果，limit 为公开返回的最多条数。
// 返回值 当前页，或数据库行扫描错误。
func collectHazards(rows pgx.Rows, limit int) (hazardPage, error) {
	page := hazardPage{Items: make([]hazardSummary, 0, limit)}
	for rows.Next() {
		if len(page.Items) == limit {
			last := page.Items[limit-1]
			page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
			break
		}
		var item hazardSummary
		if err := rows.Scan(scanSummary(&item)...); err != nil {
			return hazardPage{}, err
		}
		page.Items = append(page.Items, item)
	}
	return page, rows.Err()
}

// detail 读取台账的来源、原整改措施和当前允许的操作。
// 参数 ctx 含可选运营单位或机构范围，id 为隐患条目标识。
// 返回值 详情，或不存在及读取错误。
func (s *Service) detail(ctx context.Context, id string) (hazardDetail, error) {
	if !validID(id) {
		return hazardDetail{}, errNotFound
	}
	var item hazardDetail
	var hasReceipt, hasReport bool
	organization := operatorOrganization(ctx)
	institution := hazardInstitution(ctx)
	row := s.db.QueryRow(ctx, "SELECT "+hazardColumns+`,h.measure,h.version,h.closed_at,
		($2<>'' AND EXISTS(SELECT 1 FROM hazard_events e WHERE e.hazard_id=h.id
			AND COALESCE(e.merge_history_scope,c.organization_name)=$2 AND `+hazardReceiptEvent+`)),
		($3<>'' AND `+retestReportQuery+`)`+hazardJoin+" WHERE h.id=$1 AND "+hazardScope+
		" AND "+access.FilingDistrictScope("c.filing_id", "$4", false),
		id, organization, institution, access.Districts(ctx))
	err := row.Scan(append(scanSummary(&item.hazardSummary), &item.Measure, &item.Version, &item.ClosedAt,
		&hasReceipt, &hasReport)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return hazardDetail{}, errNotFound
	}
	item.AllowedActions = allowedActions(item.Status)
	if organization == "" && institution == "" && item.Ledger == ledgerMajorRisk && canSupervise(item.Status) {
		item.AllowedActions = append(item.AllowedActions, actionSupervise)
	}
	if actor, _ := access.Current(ctx); actor.Role == access.Regulator {
		item.AllowedActions = slices.DeleteFunc(item.AllowedActions, func(action string) bool {
			return !actionWithinScope(ctx, action)
		})
	}
	if institution == "" && item.Source == "" {
		item.AssessmentOrg = ""
	}
	if organization != "" {
		item.ReceiptAvailable = &hasReceipt
		if !editable(item.Status) {
			item.AllowedActions = []string{}
		}
	}
	if institution != "" {
		item.AssessmentOrg = institution
		item.RetestReportReady = &hasReport
		item.AllowedActions = []string{}
		if item.Status == statusRetest {
			item.AllowedActions = []string{actionRetestPass, actionRetestReturn}
		}
	}
	return item, err
}

// ensureHazardAttachmentScope 核对当前台账或已经冻结的原证据访问范围。
// 参数 ctx 含可选运营单位或机构，id 为台账公开标识。
// 返回值 有证据访问范围时返回 nil，否则为不存在或读取错误。
func (s *Service) ensureHazardAttachmentScope(ctx context.Context, id string) error {
	if !validID(id) {
		return errNotFound
	}
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1`+hazardJoin+`
		WHERE h.id=$1 AND `+access.FilingDistrictScope("c.filing_id", "$4", false)+`
		AND ((`+hazardScope+`) OR ($2<>'' AND h.status<>'pending_dispatch' AND EXISTS (
			SELECT 1 FROM hazard_attachments a WHERE a.hazard_id=h.id AND a.merge_history_scope COLLATE "C"=$2
		))))`, id, operatorOrganization(ctx), hazardInstitution(ctx), access.Districts(ctx)).Scan(&exists)
	if err == nil && !exists {
		return errNotFound
	}
	return err
}

// history 在同一查询快照核对台账归属并读取办理历史，最新版本置前。
// 参数 ctx 含可选运营单位或机构，id 为台账公开标识。
// 返回值 审核轨迹切片，或归属、读取错误。
func (s *Service) history(ctx context.Context, id string) ([]hazardEvent, error) {
	if !validID(id) {
		return nil, errNotFound
	}
	var items []hazardEvent
	err := s.db.QueryRow(ctx, `SELECT (SELECT COALESCE(jsonb_agg(jsonb_build_object(
		'action',e.action,'fromStatus',e.from_status,'toStatus',e.to_status,'operator',e.operator,
		'comment',e.comment,'occurredAt',e.occurred_at) ORDER BY e.version DESC),'[]'::jsonb)
		FROM hazard_events e WHERE e.hazard_id=h.id
		  AND ($2='' OR COALESCE(e.merge_history_scope,c.organization_name)=$2))`+hazardJoin+`
		WHERE h.id=$1 AND `+access.FilingDistrictScope("c.filing_id", "$4", false)+`
		AND ((`+hazardScope+`) OR ($2<>'' AND EXISTS (
		  SELECT 1 FROM hazard_events e WHERE e.hazard_id=h.id AND e.merge_history_scope=$2)))`,
		id, operatorOrganization(ctx), hazardInstitution(ctx), access.Districts(ctx)).Scan(&items)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	return items, err
}
