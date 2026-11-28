package supervision

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/access"
)

const taskColumns = `t.id::text,t.filing_id::text,t.filing_number,t.system_name,t.organization_name,
	t.title,t.due_on::text,t.status,t.version,t.lead_alert_id::text,t.published_at,t.submitted_at,
	t.sequence,COALESCE(t.submitted_version,0),COALESCE(t.return_version,0),COALESCE(t.correction_kind,'')`

const matchingScope = `($2='' OR (t.organization_name COLLATE "C"=$2 AND t.published_at IS NOT NULL))`
const originalTaskOwner = `COALESCE(t.merge_history_scope COLLATE "C"=$2,false)`
const readableScope = `(` + matchingScope + ` OR (` + originalTaskOwner + ` AND t.published_at IS NOT NULL
	AND t.merge_history_snapshot->>'status'<>'draft'))`
const historyColumns = `,COALESCE(t.merge_history_version,0),
	CASE WHEN ` + originalTaskOwner + ` THEN t.merge_history_snapshot END`
const scopedTaskStatus = `(CASE WHEN ` + originalTaskOwner + ` THEN t.merge_history_snapshot->>'status' ELSE t.status END)`

const eligibleFiling = `f.kind='filing' AND f.status='approved' AND f.cancelled_at IS NULL
	AND length(f.organization_name) BETWEEN 1 AND 200 AND btrim(f.organization_name)<>''`

const currentTaskFiling = `EXISTS (SELECT 1 FROM supervision_filing_scope f
	WHERE f.task_id=t.id AND f.current_owner)`

const currentSitePending = `EXISTS (SELECT 1 FROM supervision_site_inspections site
	WHERE site.task_id=t.id AND site.status NOT IN ('closed','cancelled'))`

const leadStillOpen = `a.status<>'closed' AND h.status<>'closed'
	AND h.deadline<(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date`

type reader interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type taskHistorySnapshot struct {
	Status         string     `json:"status"`
	SubmittedAt    *time.Time `json:"submitted_at"`
	SubmittedVer   int64      `json:"submitted_version"`
	ReturnedVer    int64      `json:"return_version"`
	CorrectionKind string     `json:"correction_kind"`
}

type inspectionVersionRange struct{ after, through int64 }

// inspectionVersions 按监管、原单位和承接单位限制可读取的历史版本。
// 参数 owner、historical 为视角，handoff 为最后一个原单位版本。
// 返回值 前后版本范围，零表示对应端无限制。
func inspectionVersions(owner string, historical bool, handoff int64) inspectionVersionRange {
	switch {
	case owner == "":
		return inspectionVersionRange{}
	case historical:
		return inspectionVersionRange{through: handoff}
	default:
		return inspectionVersionRange{after: handoff}
	}
}

// taskTargets 返回查询字段和任务标识、快照及版本数据的扫描目标。
// 参数 record 为待填充的任务记录。
// 返回值 按 taskColumns 排列的列指针。
func taskTargets(record *taskRecord) []any {
	return []any{&record.ID, &record.FilingID, &record.FilingNumber, &record.SystemName,
		&record.OrganizationName, &record.Title, &record.DueOn, &record.Status,
		&record.Version, &record.LeadAlertID, &record.PublishedAt, &record.SubmittedAt,
		&record.Sequence, &record.SubmittedVer, &record.ReturnedVer, &record.CorrectionKind}
}

// scanScopedTask 将原单位投影到移交时的年度状态和材料版本。
// 参数 row、owner 为任务查询行和已校验的单位范围。
// 返回值 可见任务的快照或查询错误。
func scanScopedTask(row pgx.Row, owner string) (taskRecord, error) {
	var record taskRecord
	var history *taskHistorySnapshot
	err := row.Scan(append(taskTargets(&record), &record.HistoryVersion, &history)...)
	if err == nil && history != nil {
		record.Historical = true
		record.OrganizationName = owner
		record.Version = record.HistoryVersion
		record.Status, record.SubmittedAt = history.Status, history.SubmittedAt
		record.SubmittedVer, record.ReturnedVer = history.SubmittedVer, history.ReturnedVer
		record.CorrectionKind = history.CorrectionKind
	}
	return record, err
}

// loadTask 读取移交前后对应的任务视角，写入锁定只接受当前范围。
// 参数 ctx、db 为读写环境，id 和 owner 指定任务及操作归属，lock 决定是否锁行。
// 返回值 已持久化的任务或查询失败。
func loadTask(ctx context.Context, db reader, id, owner string, lock bool) (taskRecord, error) {
	query := `SELECT ` + taskColumns + historyColumns + ` FROM supervision_tasks t WHERE t.id=$1 AND `
	districts := access.Districts(ctx)
	districtScope := " AND " + access.FilingDistrictScope("t.filing_id", "$3", false)
	if !lock {
		return scanScopedTask(db.QueryRow(ctx, query+readableScope+districtScope, id, owner, districts), owner)
	}
	record, err := scanScopedTask(db.QueryRow(ctx, query+matchingScope+districtScope+" FOR UPDATE OF t",
		id, owner, districts), owner)
	if err == nil && districts != nil {
		var visible bool
		err = db.QueryRow(ctx, "SELECT true WHERE "+access.FilingDistrictScope("$1::uuid", "$2", true),
			record.FilingID, districts).Scan(&visible)
	}
	return record, err
}

// list 查询受当前归属和状态筛选限制的监督检查任务。
// 参数 ctx、owner、status、before 分别为请求上下文、作用域、状态及分页序号。
// 返回值 任务摘要、下一页游标或查询错误。
func (s *Service) list(ctx context.Context, owner, status string, before int64) (page[taskSummary], error) {
	rows, err := s.db.Query(ctx, `SELECT `+taskColumns+historyColumns+` FROM supervision_tasks t
		WHERE ($1::bigint=0 OR t.sequence<$1) AND `+readableScope+`
		AND ($3='' OR `+scopedTaskStatus+`=$3) AND `+access.FilingDistrictScope("t.filing_id", "$5", false)+`
		ORDER BY t.sequence DESC LIMIT $4`, before, owner, status, pageSize+1, access.Districts(ctx))
	if err != nil {
		return page[taskSummary]{}, err
	}
	defer rows.Close()
	result := page[taskSummary]{Items: make([]taskSummary, 0, pageSize)}
	var last int64
	for rows.Next() {
		if len(result.Items) == pageSize {
			result.NextBefore = strconv.FormatInt(last, 10)
			break
		}
		record, err := scanScopedTask(rows, owner)
		if err != nil {
			return page[taskSummary]{}, err
		}
		result.Items = append(result.Items, record.taskSummary)
		last = record.Sequence
	}
	return result, rows.Err()
}

// options 查询已通过且有效的备案，线索入口额外核对当前仍在超期的对应备案。
// 参数 ctx 控制查询，query 是可选的备案检索，lead 是可选真实线索。
// 返回值 有界的备案选项或业务数据库错误。
func (s *Service) options(ctx context.Context, query, lead string) ([]filingOption, error) {
	rows, err := s.db.Query(ctx, `SELECT f.id::text,f.number,f.system_name,f.organization_name
		FROM business_cases f WHERE `+eligibleFiling+`
		AND `+access.FilingDistrictScope("f.id", "$3", false)+`
		AND ($1='' OR strpos(lower(concat_ws(' ',f.number,f.system_name,f.organization_name)),lower($1))>0)
		AND (NULLIF($2,'') IS NULL OR EXISTS (
			SELECT 1 FROM alert_supervision_leads l JOIN local_alerts a ON a.id=l.alert_id
			JOIN hazard_items h ON h.id=a.hazard_id
			JOIN hazard_context hc ON hc.id=h.id
			WHERE l.alert_id=NULLIF($2,'')::uuid AND hc.filing_id=f.id
			  AND l.organization_name COLLATE "C"=f.organization_name AND `+leadStillOpen+`))
		ORDER BY f.created_at DESC,f.id DESC LIMIT 25`, query, lead, access.Districts(ctx))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[filingOption])
}

// canAct 依据席位、任务状态和当前备案归属给出真实可用动作。
// 参数 record 为任务，operator、active、sitePending 为席位、原归属及现场办理情况。
// 返回值 用于界面约束的动作列表。
func canAct(record taskRecord, operator, active, sitePending bool) []string {
	if operator {
		if active && editableResult(record.Status) {
			return []string{"upload", "submit"}
		}
		return []string{}
	}
	switch record.Status {
	case "draft":
		if active {
			return []string{"upload", "publish", "cancel"}
		}
		return []string{"cancel"}
	case "open", "returned":
		if sitePending {
			return []string{}
		}
		return []string{"cancel"}
	case "submitted":
		return []string{"return", "approve"}
	default:
		return []string{}
	}
}

// detail 在只读快照中返回任务、席位可见材料和整改汇总。
// 参数 ctx 控制查询，id、owner 指定当前任务与所选单位。
// 返回值 业务详情，或范围和数据库错误。
func (s *Service) detail(ctx context.Context, id, owner string) (taskDetail, error) {
	var result taskDetail
	err := pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		record, err := loadTask(ctx, tx, id, owner, false)
		if err != nil {
			return err
		}
		var active, sitePending bool
		err = tx.QueryRow(ctx, `SELECT `+currentTaskFiling+`,`+currentSitePending+`
			FROM supervision_tasks t WHERE t.id=$1`, id).Scan(&active, &sitePending)
		if err != nil {
			return err
		}
		result.taskSummary = record.taskSummary
		result.CorrectionKind = record.CorrectionKind
		active = active && !record.Historical
		result.AllowedActions = canAct(record, owner != "", active, sitePending)
		result.Attachments, err = latestVisibleMaterials(ctx, tx, record, owner)
		if err != nil {
			return err
		}
		result.Rectification, err = taskRectification(ctx, tx, record, owner, active)
		return err
	})
	return result, err
}

// taskRectification 保留实际送审当时的整改快照，草稿可查看当前有效备案的实时汇总。
// 参数 ctx、db 为读环境，record 是任务，owner、active 标记收件范围及其是否有效。
// 返回值 真实汇总、未生成时为空，或者数据库错误。
func taskRectification(ctx context.Context, db reader, record taskRecord, owner string,
	active bool,
) (*rectificationCounts, error) {
	if record.SubmittedVer != 0 && !editableResult(record.Status) {
		if record.SubmittedVer <= inspectionVersions(owner, record.Historical, record.HistoryVersion).after {
			return nil, nil
		}
		var counts rectificationCounts
		err := db.QueryRow(ctx, `SELECT rectification FROM supervision_events
			WHERE task_id=$1 AND version=$2 AND action='submit'`, record.ID, record.SubmittedVer).Scan(&counts)
		return &counts, err
	}
	if !active {
		return nil, nil
	}
	counts, err := countRectification(ctx, db, record.FilingID)
	return &counts, err
}

// countRectification 基于已派发台账计算同一备案下的未销项与已销项数量。
// 参数 ctx、db 为查询环境，filing 为任务所指定的有效备案。
// 返回值 当前整改状态合计或查询错误。
func countRectification(ctx context.Context, db reader, filing string) (rectificationCounts, error) {
	var counts rectificationCounts
	err := db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE h.status<>'closed'),
		count(*) FILTER (WHERE h.status='closed')
		FROM hazard_context hc JOIN hazard_items h ON h.id=hc.id
		WHERE hc.filing_id=$1 AND h.status<>'pending_dispatch'`, filing).Scan(&counts.Open, &counts.Closed)
	return counts, err
}

const eventUploads = `(SELECT jsonb_agg(jsonb_build_object('id', a.id, 'kind', a.kind,
	'fileName', a.file_name, 'size', a.size_bytes, 'uploadedAt', a.uploaded_at) ORDER BY a.kind)
	FROM supervision_attachments a WHERE a.task_id=e.task_id AND a.id IN (e.inspection_file_id,e.summary_file_id))`

// history 返回同一任务的完整可分页留痕，含每轮提交锁定的材料版本。
// 参数 ctx、id、owner、before 提供请求上文、范围及分页位置。
// 返回值 带可选下一页游标的留痕或查验错误。
func (s *Service) history(ctx context.Context, id, owner string, before int64) (page[reviewEvent], error) {
	var result page[reviewEvent]
	err := pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		record, err := loadTask(ctx, tx, id, owner, false)
		if err != nil {
			return err
		}
		versions := inspectionVersions(owner, record.Historical, record.HistoryVersion)
		rows, err := tx.Query(ctx, `SELECT e.version,e.action,COALESCE(e.from_status,''),e.to_status,e.actor,
			e.comment,COALESCE(e.correction_kind,''),e.created_at,`+eventUploads+`,e.rectification
			FROM supervision_events e WHERE e.task_id=$1 AND ($2::bigint=0 OR e.version<$2)
			  AND ($3='' OR e.action<>'create') AND e.version>$4 AND ($5::bigint=0 OR e.version<=$5)
			ORDER BY e.version DESC LIMIT $6`, id, before, owner, versions.after, versions.through, pageSize+1)
		if err != nil {
			return err
		}
		result.Items, err = pgx.CollectRows(rows, pgx.RowToStructByPos[reviewEvent])
		if len(result.Items) > pageSize {
			result.Items = result.Items[:pageSize]
			result.NextBefore = strconv.FormatInt(result.Items[pageSize-1].Version, 10)
		}
		return err
	})
	return result, err
}
