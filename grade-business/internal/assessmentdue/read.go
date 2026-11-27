package assessmentdue

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"
)

const todaySQL = `(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date`
const activeFiling = `f.kind='filing' AND f.status='approved' AND f.discarded_at IS NULL AND f.cancelled_at IS NULL
	AND char_length(btrim(f.organization_name)) BETWEEN 1 AND 200`
const dueStatus = `CASE WHEN d.closed_at IS NOT NULL THEN 'closed'
	WHEN NOT (` + activeFiling + `) THEN 'suspended'
	WHEN d.due_on<` + todaySQL + ` THEN 'expired'
	WHEN d.due_on=` + todaySQL + ` THEN 'due'
	WHEN d.due_on<=` + todaySQL + `+d.warning_days THEN 'due_soon' ELSE 'upcoming' END`
const source = ` FROM assessment_due_records d JOIN business_cases f ON f.id=d.filing_id
	CROSS JOIN LATERAL (SELECT ` + dueStatus + ` AS status) stage`
const recordColumns = `d.id::text,d.filing_id::text,f.number,f.system_name,f.organization_name,f.assessment_org,
	to_char(d.due_on,'YYYY-MM-DD'),d.warning_days,stage.status,d.version,d.evidence,d.sequence,d.cycle,
	d.created_at,d.updated_at`
const byAudience = `($1='' OR ($1='operator' AND f.organization_name COLLATE "C"=$2)
	OR ($1='institution' AND f.assessment_org COLLATE "C"=$2))`

type reader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// scanRecord 将已限定范围的数据库投影读入内部记录。
// 参数 row 为当前业务查询行。
// 返回值 期限记录或数据库读取错误。
func scanRecord(row pgx.Row) (record, error) {
	var out record
	err := row.Scan(&out.ID, &out.FilingID, &out.FilingNumber, &out.SystemName, &out.OrganizationName,
		&out.InstitutionName, &out.DueOn, &out.WarningDays, &out.Status, &out.Version, &out.evidence,
		&out.sequence, &out.cycle, &out.created, &out.updated)
	return out, err
}

// loadRecord 按当前备案归属读取明确登记的测评日期。
// 参数 ctx、db 控制读取，query 和 id 为已校验的访问范围和登记标识。
// 返回值 内部登记快照或范围和数据库错误。
func loadRecord(ctx context.Context, db reader, query filters, id string) (record, error) {
	return scanRecord(db.QueryRow(ctx, `SELECT `+recordColumns+source+` WHERE `+byAudience+` AND d.id=$3`,
		query.audience, query.owner, id))
}

// list 返回满足备案、状态和实际收件范围的一页日期数据。
// 参数 ctx、query 分别为请求上下文和已校验筛选。
// 返回值 期限摘要、分页位置或查询错误。
func (s *Service) list(ctx context.Context, query filters) (recordPage, error) {
	rows, err := s.db.Query(ctx, `SELECT `+recordColumns+source+` WHERE `+byAudience+`
		AND ($3='' OR d.filing_id=NULLIF($3,'')::uuid) AND ($4='' OR stage.status=$4)
		AND ($5::bigint=0 OR d.sequence<$5) ORDER BY d.sequence DESC LIMIT $6`,
		query.audience, query.owner, query.filing, query.status, query.before, pageSize+1)
	if err != nil {
		return recordPage{}, err
	}
	defer rows.Close()
	out := recordPage{Items: make([]summary, 0, pageSize)}
	var last int64
	for rows.Next() {
		if len(out.Items) == pageSize {
			out.NextBefore = strconv.FormatInt(last, 10)
			break
		}
		item, err := scanRecord(rows)
		if err != nil {
			return recordPage{}, err
		}
		out.Items, last = append(out.Items, item.summary), item.sequence
	}
	return out, rows.Err()
}

// detail 在一致快照中返回日期和监管专用的最近业务留痕。
// 参数 ctx 控制读取，query、id 为办理端和记录标识。
// 返回值 日期详情或实际的范围和存储错误。
func (s *Service) detail(ctx context.Context, query filters, id string) (detail, error) {
	var out detail
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	err := pgx.BeginTxFunc(ctx, s.db, options, func(tx pgx.Tx) error {
		item, err := loadRecord(ctx, tx, query, id)
		if err != nil {
			return err
		}
		out = detail{summary: item.summary, CreatedAt: item.created, UpdatedAt: item.updated}
		if query.audience == "" {
			out.Evidence = item.evidence
			out.History, err = events(ctx, tx, id)
		}
		return err
	})
	return out, err
}

// events 按最近的审计版本返回明确日期及原始留痕。
// 参数 ctx、db 控制查询，id 为已经核验的记录。
// 返回值 最近 30 条监管留痕或读取错误。
func events(ctx context.Context, db reader, id string) ([]event, error) {
	rows, err := db.Query(ctx, `SELECT version,cycle,action,to_char(due_on,'YYYY-MM-DD'),warning_days,
		evidence,actor,created_at FROM assessment_due_events WHERE record_id=$1 ORDER BY version DESC LIMIT 30`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[event])
}
