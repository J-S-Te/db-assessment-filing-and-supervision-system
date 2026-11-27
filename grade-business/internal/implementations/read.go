package implementations

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type rowReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type historyReader interface {
	rowReader
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

const summarySQL = `id::text, filing_number, system_name, organization_name, assessment_org,
  report_type, status, submitted_at, to_char(planned_start,'YYYY-MM-DD'), to_char(planned_end,'YYYY-MM-DD'), archived_at`

// getRecord 按标识读取报备详情和仅供规则校验的内部字段。
// 参数 ctx 控制查询，reader 为连接池或事务，id 为公开标识，
// institution 为可选机构范围，lock 表示是否需要写入行锁。
// 返回值 数据库记录或不存在错误。
func getRecord(ctx context.Context, reader rowReader, id, institution string, lock bool) (storedRecord, error) {
	query := `SELECT ` + summarySQL + `, filing_id::text, reporter, contact, plan, version,
	  accepted_at, created_by, quota_year,directory_person_id::text,directory_snapshot
	  FROM implementation_reports i
	  WHERE id=$1 AND ($2::text='' OR assessment_org COLLATE "C" = $2)
	  AND ($3::text='' OR COALESCE(i.frozen_organization_name, (SELECT f.organization_name
	    FROM business_cases f WHERE f.id=i.filing_id AND f.kind='filing')) COLLATE "C" = $3)`
	if lock {
		query += " FOR UPDATE"
	}
	var item storedRecord
	err := reader.QueryRow(ctx, query, id, institution, operatorOrganization(ctx)).Scan(&item.ID, &item.FilingNumber,
		&item.SystemName, &item.OrganizationName, &item.AssessmentOrg, &item.Type, &item.Status,
		&item.SubmittedAt, &item.PlannedStart, &item.PlannedEnd, &item.ArchivedAt,
		&item.FilingID, &item.Reporter, &item.Contact,
		&item.Plan, &item.Version, &item.AcceptedAt, &item.CreatedBy, &item.QuotaYear,
		&item.ReporterID, &item.DirectorySnapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return storedRecord{}, errNotFound
	}
	return item, err
}

// readPage 根据流转、归档等条件读取列表记录及下页游标。
// 参数 ctx 控制查询，institution 为可选归属范围，options 为已经校验的筛选条件。
// 返回值 分页结果或数据库错误。
func (s *Service) readPage(ctx context.Context, institution string, options listOptions) (page, error) {
	query := `SELECT ` + summarySQL + `, created_at FROM implementation_reports i
	 WHERE ($1::text='' OR strpos(lower(concat_ws(' ',filing_number,system_name,organization_name,assessment_org,reporter)),lower($1))>0)
	 AND ($2::text='' OR status=$2) AND ($3::text='' OR report_type=$3)
	 AND ($4::timestamptz IS NULL OR (created_at,id)<($4,$5::uuid))
	 AND ($7::text='' OR assessment_org COLLATE "C" = $7)
	 AND ($8::text='' OR COALESCE(i.frozen_organization_name, (SELECT f.organization_name
	   FROM business_cases f WHERE f.id=i.filing_id AND f.kind='filing')) COLLATE "C" = $8)`
	switch options.archive {
	case archived:
		query += ` AND archived_at IS NOT NULL`
	case unarchived:
		query += ` AND archived_at IS NULL`
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT $6`
	rows, err := s.pool.Query(ctx, query, options.q, options.status, options.kind,
		options.after, options.afterID, options.limit+1, institution, operatorOrganization(ctx))
	if err != nil {
		return page{}, err
	}
	defer rows.Close()
	result := page{Items: make([]summary, 0, options.limit)}
	var lastTime time.Time
	for rows.Next() {
		if len(result.Items) == options.limit {
			last := result.Items[len(result.Items)-1]
			result.NextCursor = encodeCursor(lastTime, last.ID)
			break
		}
		var item summary
		if err := rows.Scan(&item.ID, &item.FilingNumber, &item.SystemName, &item.OrganizationName, &item.AssessmentOrg,
			&item.Type, &item.Status, &item.SubmittedAt, &item.PlannedStart, &item.PlannedEnd,
			&item.ArchivedAt, &lastTime); err != nil {
			return page{}, err
		}
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}

// retestHandoffSQL 确认可由目标机构接手的运营者换机构草稿。
// 参数 institution 为当前机构对应的内部 SQL 参数位置，外层必须以 f 表示有效备案。
// 返回值 只认可当前备案版本、所属单位和明确选中本机构的条件。
func retestHandoffSQL(institution string) string {
	return `EXISTS (SELECT 1 FROM filing_applications a
	 WHERE a.filing_id=f.id AND a.filing_version=f.version AND a.operator_origin
	 AND a.application_type='change' AND a.status IN ('draft','returned')
	 AND a.before_values->>'organizationName'=f.organization_name
	 AND (a.after_values->>'assessmentOrg') COLLATE "C" = ` + institution + `)`
}

// filingChoices 按现有归属或换机构草稿授权读取可关联的有效备案。
// 参数 ctx 控制查询并携带运营者范围，institution 为可选机构归属，q、filingID 为已校验筛选。
// 返回值 可关联备案的必要字段或数据库错误。
func (s *Service) filingChoices(ctx context.Context, institution, q, filingID string) ([]choice, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, number, system_name, organization_name, assessment_org FROM business_cases f
	 WHERE kind='filing' AND status='approved' AND cancelled_at IS NULL AND discarded_at IS NULL
	 AND ($1::text='' OR strpos(lower(concat_ws(' ',number,system_name,organization_name)),lower($1))>0)
	 AND ($2::text='' OR assessment_org COLLATE "C" = $2 OR `+retestHandoffSQL("$2")+`)
	 AND ($3::text='' OR organization_name COLLATE "C" = $3)
	 AND ($4::text='' OR id=NULLIF($4,'')::uuid)
	 ORDER BY created_at DESC,id DESC LIMIT 20`, q, institution, operatorOrganization(ctx), filingID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[choice])
}

// countChanges 计算指定上海年度仍占用配额的变更报备数。
// 参数 ctx 控制查询，reader 为池或事务，filingID 和 year 确定配额范围。
// 返回值 已占用额度及数据库错误。
func countChanges(ctx context.Context, reader rowReader, filingID string, year int) (int, error) {
	var count int
	err := reader.QueryRow(ctx, `SELECT count(*) FROM implementation_reports
	 WHERE filing_id=$1 AND quota_year=$2 AND status<>'withdrawn'`, filingID, year).Scan(&count)
	return count, err
}

const latestWithdrawalSQL = `SELECT max(e.occurred_at)
  FROM implementation_reports r JOIN implementation_events e ON e.report_id=r.id
  WHERE r.filing_id=$1 AND r.status='withdrawn' AND e.action='withdraw'`

// readResubmitAvailableAt 读取同一备案最近有效撤销对应的冷却截止时刻。
// 参数 ctx、reader 为查询范围，filingID 为备案，now 为当前业务时间。
// 返回值 尚在冷却时的截止时刻，否则为空，也可能返回数据库错误。
func readResubmitAvailableAt(ctx context.Context, reader rowReader, filingID string, now time.Time) (*time.Time, error) {
	var withdrawnAt *time.Time
	err := reader.QueryRow(ctx, latestWithdrawalSQL, filingID).Scan(&withdrawnAt)
	return resubmitAvailableAt(withdrawnAt, now), err
}

// resubmitAvailableAt 按撤销时刻计算仍在生效的 15 天冷却。
// 参数 withdrawnAt 为最近一次撤销留痕时间，now 为当前业务时间。
// 返回值 尚不能重新提交时的上海时间，或空值。
func resubmitAvailableAt(withdrawnAt *time.Time, now time.Time) *time.Time {
	if withdrawnAt == nil {
		return nil
	}
	available := withdrawnAt.In(shanghai).AddDate(0, 0, 15)
	if now.Before(available) {
		return &available
	}
	return nil
}

// readQuota 为已有归属或目标机构获取有效备案的年度额度和冷却截止时间。
// 参数 ctx 控制查询，filingID 为备案标识，institution 为可选机构归属。
// 返回值 年度额度或不可关联错误。
func (s *Service) readQuota(ctx context.Context, filingID, institution string) (quota, error) {
	now := s.now()
	result := quota{Year: quotaYear(now), Limit: quotaLimit}
	var withdrawnAt *time.Time
	err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM implementation_reports
	 WHERE filing_id=$1 AND quota_year=$2 AND status<>'withdrawn'), (`+latestWithdrawalSQL+`)
	 FROM business_cases f
	 WHERE id=$1 AND kind='filing' AND status='approved' AND cancelled_at IS NULL AND discarded_at IS NULL
	 AND ($3::text='' OR assessment_org COLLATE "C" = $3 OR `+retestHandoffSQL("$3")+`)
	 AND ($4::text='' OR organization_name COLLATE "C" = $4)`,
		filingID, result.Year, institution, operatorOrganization(ctx)).Scan(&result.Used, &withdrawnAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return quota{}, errFiling
	}
	result.ResubmitAt = resubmitAvailableAt(withdrawnAt, now)
	return result, err
}

// readHistory 读取既有报备的审核事件及更早事件的游标。
// 参数 ctx 控制查询，id 为报备标识，before 和 limit 控制分页。
// 返回值 事件分页或数据库错误。
func (s *Service) readHistory(ctx context.Context, id string, before int64, limit int) (eventPage, error) {
	if operatorOrganization(ctx) == "" {
		return readImplementationHistory(ctx, s.pool, id, before, limit)
	}
	var result eventPage
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	err := pgx.BeginTxFunc(ctx, s.pool, options, func(tx pgx.Tx) error {
		var err error
		result, err = readImplementationHistory(ctx, tx, id, before, limit)
		return err
	})
	return result, err
}

// readImplementationHistory 以同一读取范围核对报备归属并分页加载审核事件。
// 参数 ctx 控制查询，reader 为连接池或读取快照，id 为报备标识，before 和 limit 控制分页。
// 返回值 事件分页或数据库错误。
func readImplementationHistory(ctx context.Context, reader historyReader, id string, before int64, limit int) (eventPage, error) {
	var exists bool
	err := reader.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM implementation_reports i WHERE i.id=$1
	  AND ($2::text='' OR COALESCE(i.frozen_organization_name, (SELECT f.organization_name
	    FROM business_cases f WHERE f.id=i.filing_id AND f.kind='filing')) COLLATE "C" = $2))`,
		id, operatorOrganization(ctx)).Scan(&exists)
	if err != nil {
		return eventPage{}, err
	}
	if !exists {
		return eventPage{}, errNotFound
	}
	rows, err := reader.Query(ctx, `SELECT action,from_status,to_status,operator,comment,occurred_at,version,
	     CASE WHEN NOT $4 THEN directory_snapshot END
	 FROM implementation_events WHERE report_id=$1 AND ($2::bigint=0 OR version<$2)
	 ORDER BY version DESC LIMIT $3`, id, before, limit+1, operatorOrganization(ctx) != "")
	if err != nil {
		return eventPage{}, err
	}
	defer rows.Close()
	result := eventPage{Items: make([]event, 0, limit)}
	var lastVersion int64
	for rows.Next() {
		if len(result.Items) == limit {
			result.NextBeforeVersion = lastVersion
			break
		}
		var item event
		if err := rows.Scan(&item.Action, &item.FromStatus, &item.ToStatus, &item.Operator,
			&item.Comment, &item.OccurredAt, &lastVersion, &item.DirectorySnapshot); err != nil {
			return eventPage{}, err
		}
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}
