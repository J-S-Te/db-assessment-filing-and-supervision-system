package materialexpiry

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"
)

type reader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

const originalOperator = `($1='operator' AND e.handoff_version IS NOT NULL
    AND e.organization_name COLLATE "C"=$2)`

const successorOperator = `($1='operator' AND e.handoff_version IS NOT NULL
    AND s.organization_name COLLATE "C"=$2)`

const bySeat = `($1='' OR ` + originalOperator + ` OR (s.scope_current OR e.keep_retired_scope) AND (
    $1='operator' AND s.organization_name COLLATE "C"=$2
    OR $1='institution' AND e.institution_name COLLATE "C"=$2))`

const columns = `
 e.id::text,e.case_id::text,e.case_kind,e.number,e.system_name,
 CASE WHEN ` + originalOperator + ` THEN e.organization_name ELSE s.organization_name END,e.institution_name,
 e.attachment_kind,to_char(CASE WHEN ` + originalOperator + ` THEN e.handoff_valid_until ELSE e.valid_until END,
   'YYYY-MM-DD'),e.warning_days,CASE WHEN ` + originalOperator + ` THEN 'retired' ELSE s.date_status END,
 NOT ` + originalOperator + ` AND EXISTS(
   SELECT 1 FROM material_expiry_renewals r WHERE r.record_id=e.id AND r.status='pending'),
 CASE WHEN ` + originalOperator + ` THEN e.handoff_version-1 ELSE e.version END,
 e.sequence,CASE WHEN ` + originalOperator + ` THEN e.handoff_cycle ELSE e.cycle END,
 e.source_attachment_id::text,
 CASE WHEN ` + successorOperator + ` THEN '' ELSE a.file_name END,
 CASE WHEN ` + originalOperator + ` THEN COALESCE((
     SELECT h.evidence FROM material_expiry_events h WHERE h.record_id=e.id AND h.action='register'
   ),'') WHEN ` + successorOperator + ` AND e.cycle=1 THEN '' ELSE e.evidence END,
 ` + originalOperator + `
`
const recordSource = `
 FROM material_expiry_records e
 JOIN material_expiry_status s ON s.id=e.id
 JOIN case_attachments a ON a.id=e.source_attachment_id
 WHERE ` + bySeat

const sourceColumns = `
 a.id::text,c.id::text,f.id::text,c.kind,c.number,f.system_name,f.organization_name,c.assessment_org,
 a.kind,a.file_name,a.uploaded_at
`
const sourceRelations = `
 FROM case_attachments a
 JOIN business_cases c ON c.id=a.case_id
 JOIN business_cases f ON f.id=COALESCE(c.filing_id,c.id)
`
const assessmentExpiryKindsSQL = `('assessment_conclusion','report_file')`
const validSource = sourceRelations + `
 CROSS JOIN LATERAL (
   SELECT CASE WHEN c.kind='filing' THEN a.uploaded_at
               ELSE LEAST(a.uploaded_at,COALESCE(c.submitted_at,c.created_at)) END AS original_at
 ) history
 WHERE c.status='approved' AND c.discarded_at IS NULL
   AND f.status='approved' AND f.discarded_at IS NULL AND f.cancelled_at IS NULL
   AND btrim(f.organization_name)<>''
   AND (c.kind='filing' AND a.kind IN ('filing_form','classification_report')
        OR c.kind='report' AND a.kind IN ('report_file','assessment_conclusion','remediation_plan'))
   AND (c.kind='filing' OR (
      COALESCE(NULLIF(c.directory_snapshot#>>'{unit,scopeName}',''),
        NULLIF(c.organization_name,''),f.organization_name) COLLATE "C"=f.organization_name
      AND (c.directory_unit_id IS NULL OR f.directory_unit_id IS NULL OR c.directory_unit_id=f.directory_unit_id)
   ))
   AND NOT EXISTS (SELECT 1 FROM filing_applications request
     WHERE request.filing_id=f.id AND request.application_type='change' AND request.status='approved'
       AND request.applied_at>=history.original_at
       AND (request.before_values->>'organizationName' IS DISTINCT FROM request.after_values->>'organizationName'
            OR (c.kind='filing' AND request.before_values->>'assessmentOrg'
                IS DISTINCT FROM request.after_values->>'assessmentOrg')))
   AND NOT EXISTS (SELECT 1 FROM directory_unit_merges moved
     WHERE moved.merged_at>=history.original_at
       AND moved.plan->'moves' @> jsonb_build_array(jsonb_build_object('id',f.id::text)))
   AND lower(a.file_name) ~ '\.(pdf|docx)$'
   AND NOT EXISTS (
     SELECT 1 FROM case_attachments b WHERE b.case_id=a.case_id AND b.kind=a.kind
       AND (b.uploaded_at,b.id)>(a.uploaded_at,a.id)
   )
`

// scanRecord 扫描已核对范围的登记和周期信息。
// 参数 row 为选定有效期的查询结果。
// 返回值 登记快照或数据库错误。
func scanRecord(row pgx.Row) (expiryRecord, error) {
	var e expiryRecord
	err := row.Scan(&e.ID, &e.CaseID, &e.CaseKind, &e.Number, &e.SystemName,
		&e.OrganizationName, &e.InstitutionName, &e.AttachmentKind, &e.ValidUntil,
		&e.WarningDays, &e.DateStatus, &e.RenewalPending, &e.Version, &e.sequence,
		&e.cycle, &e.sourceID, &e.name, &e.evidence, &e.historical)
	return e, err
}

// scanSource 读取可登记的正式来源材料及对应备案关系。
// 参数 row 为一个已核验的来源材料查询行。
// 返回值 安全来源信息或数据库错误。
func scanSource(row pgx.Row) (sourceOption, error) {
	var source sourceOption
	err := row.Scan(&source.AttachmentID, &source.CaseID, &source.FilingID, &source.CaseKind,
		&source.Number, &source.SystemName, &source.OrganizationName, &source.InstitutionName,
		&source.AttachmentKind, &source.FileName, &source.uploadedAt)
	return source, err
}

// registrationSource 先锁定文件及所属业务，再按最新快照核验是否仍可登记。
// 参数 ctx、tx 控制登记事务，id 为监管选中的原件标识。
// 返回值 仍属正确范围的原件或查询错误。
func registrationSource(ctx context.Context, tx pgx.Tx, id string) (sourceOption, error) {
	var locked int
	err := tx.QueryRow(ctx, "SELECT 1"+sourceRelations+" WHERE a.id=$1 FOR SHARE OF c,f,a", id).Scan(&locked)
	if err != nil {
		return sourceOption{}, err
	}
	return scanSource(tx.QueryRow(ctx, "SELECT "+sourceColumns+validSource+" AND a.id=$1", id))
}

// loadRecord 查询一个有效期记录并按访问端核验原归属和当前归属。
// 参数 ctx、db 控制查询，seat、id 为访问端和标识。
// 返回值 当前详情快照，或不存在和数据库错误。
func loadRecord(ctx context.Context, db reader, seat scope, id string) (expiryRecord, error) {
	row := db.QueryRow(ctx, "SELECT "+columns+recordSource+" AND e.id=$3", seat.audience, seat.name, id)
	return scanRecord(row)
}

// listRecords 按明确归属和状态筛选，返回有界、稳定翻页的台账。
// 参数 ctx、seat 为读取上下文和访问端，state、reportID、before 为状态、报告和游标。
// 返回值 可公开记录的有界清单。
func (s *Service) listRecords(ctx context.Context, seat scope, state, reportID string,
	before int64,
) (page[expirySummary], error) {
	rows, err := s.db.Query(ctx, "SELECT "+columns+recordSource+`
	  AND ($3='' OR (CASE WHEN `+originalOperator+` THEN 'retired' ELSE s.date_status END)=$3)
	  AND ($4::bigint=0 OR e.sequence<$4)
	  AND ($5='' OR (e.case_id=NULLIF($5,'')::uuid AND e.case_kind='report'
	       AND e.attachment_kind IN `+assessmentExpiryKindsSQL+`))
	  ORDER BY e.sequence DESC LIMIT $6`, seat.audience, seat.name, state, before, reportID, pageLength+1)
	if err != nil {
		return page[expirySummary]{}, err
	}
	entries, err := pgx.CollectRows(rows, collectRecord)
	out := page[expirySummary]{Items: make([]expirySummary, 0, min(len(entries), pageLength))}
	for _, item := range entries[:min(len(entries), pageLength)] {
		out.Items = append(out.Items, item.expirySummary)
	}
	if len(entries) > pageLength {
		out.NextBefore = strconv.FormatInt(entries[pageLength-1].sequence, 10)
	}
	return out, err
}

// sourceOptions 逐页提供仍可登记的正式文件，并隐去内部存储字段。
// 参数 ctx 控制查询，keyword、kind、reportID 为材料筛选，before 为续页位置。
// 返回值 候选材料列表或数据库错误。
func (s *Service) sourceOptions(ctx context.Context, keyword, kind, reportID string,
	before sourceCursor,
) (page[sourceOption], error) {
	rows, err := s.db.Query(ctx, "SELECT "+sourceColumns+validSource+`
	  AND NOT EXISTS (SELECT 1 FROM material_expiry_records e
	      WHERE e.case_id=c.id AND e.attachment_kind=a.kind AND e.retired_at IS NULL)
	  AND ($1='' OR strpos(lower(concat_ws(' ',c.number,f.system_name,f.organization_name,
	      f.number,a.file_name)),lower($1))>0)
	  AND ($2='' OR c.kind=$2)
	  AND ($3::timestamptz IS NULL OR (a.uploaded_at,a.id)<($3,NULLIF($4,'')::uuid))
	  AND ($5='' OR (c.id=NULLIF($5,'')::uuid AND c.kind='report'
	       AND a.kind IN `+assessmentExpiryKindsSQL+`))
	  ORDER BY a.uploaded_at DESC,a.id DESC LIMIT $6`,
		keyword, kind, before.uploadedAt, before.attachmentID, reportID, pageLength+1)
	if err != nil {
		return page[sourceOption]{}, err
	}
	items, err := pgx.CollectRows(rows, collectSource)
	if err != nil {
		return page[sourceOption]{}, err
	}
	out := page[sourceOption]{Items: items[:min(len(items), pageLength)]}
	if len(items) > pageLength {
		out.NextBefore = sourcePageCursor(items[pageLength-1])
	}
	return out, nil
}

// collectRecord 将游标包装的数据库行交给台账适配器。
// 参数 row 为 pgx 分页结果。
// 返回值 当前台账行或读取错误。
func collectRecord(row pgx.CollectableRow) (expiryRecord, error) {
	return scanRecord(row)
}

// collectSource 将游标包装的数据库行交给来源适配器。
// 参数 row 为 pgx 分页结果。
// 返回值 当前来源文件或读取错误。
func collectSource(row pgx.CollectableRow) (sourceOption, error) {
	return scanSource(row)
}

// allowedActions 区分监管决定与原归属单位的下一步操作。
// 参数 e 为台账当前快照，seat 为已验证的业务席位。
// 返回值 当前能显式发起的动作。
func allowedActions(e expiryRecord, seat scope) []string {
	if e.DateStatus == "retired" {
		return []string{}
	}
	if seat.audience == "" {
		if e.RenewalPending {
			if e.DateStatus == "suspended" {
				return []string{"reject"}
			}
			return []string{"approve", "reject"}
		}
		return []string{"retire"}
	}
	if e.RenewalPending || e.DateStatus == "suspended" {
		return []string{}
	}
	return []string{"renew"}
}

// detail 读取同一快照下台账状态和受范围约束的历次续传证据。
// 参数 ctx、seat、id 为读取上下文、角色和记录标识。
// 返回值 台账详情，或不存在和读取错误。
func (s *Service) detail(ctx context.Context, seat scope, id string) (expiryDetail, error) {
	var result expiryDetail
	err := pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly},
		func(tx pgx.Tx) error {
			item, err := loadRecord(ctx, tx, seat, id)
			if err != nil {
				return err
			}
			result.expirySummary, result.SourceFileName, result.Evidence = item.expirySummary, item.name, item.evidence
			if seat.audience == "" {
				result.SourceAttachmentID = item.sourceID
			}
			result.AllowedActions = allowedActions(item, seat)
			result.Renewals, err = readRenewals(ctx, tx, seat, id)
			if err != nil {
				return err
			}
			result.History, err = readHistory(ctx, tx, seat, id)
			return err
		})
	return result, err
}

// readRenewals 只向原提交席位、监管和正式通过的共同收件方提供证据元数据。
// 参数 ctx、db 控制查询，seat、id 为已核验的席位和记录。
// 返回值 最近 20 次续传及其可下载范围。
func readRenewals(ctx context.Context, db reader, seat scope, id string) ([]renewal, error) {
	rows, err := db.Query(ctx, `
	  SELECT r.id::text,r.audience,r.status,to_char(r.valid_until,'YYYY-MM-DD'),
	   CASE WHEN ($2='' OR r.audience=$2 OR r.status='approved') THEN r.evidence ELSE '' END,
	   CASE WHEN ($2='' OR r.audience=$2 OR r.status='approved') THEN r.file_name ELSE '' END,
	   CASE WHEN ($2='' OR r.audience=$2 OR r.status='approved') THEN r.size_bytes ELSE 0 END,
	   ($2='' OR r.audience=$2 OR r.status='approved'),r.submitted_at,r.decided_at
	  FROM material_expiry_renewals r
	  JOIN material_expiry_records e ON e.id=r.record_id
	  WHERE r.record_id=$1 AND NOT ($2='operator' AND e.handoff_version IS NOT NULL
	     AND e.organization_name COLLATE "C"=$3)
	  ORDER BY r.version DESC LIMIT 20`, id, seat.audience, seat.name)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[renewal])
}

// readHistory 查询已核验的操作轨迹，并遮盖另一席位待审或被驳回的证据文本。
// 参数 ctx、db 是查询上下文和快照，seat、id 为可访问记录和席位。
// 返回值 有界历史或数据库错误。
func readHistory(ctx context.Context, db reader, seat scope, id string) ([]expiryEvent, error) {
	rows, err := db.Query(ctx, `
	  SELECT e.version,e.cycle,e.action,
	    CASE WHEN e.action='submit' AND $2<>'' AND r.audience<>$2 AND r.status<>'approved'
	         THEN '' ELSE e.evidence END,
	    to_char(e.valid_until,'YYYY-MM-DD'),e.actor,COALESCE(e.renewal_id::text,''),e.created_at
	  FROM material_expiry_events e
	  JOIN material_expiry_records scope ON scope.id=e.record_id
	  LEFT JOIN material_expiry_renewals r ON r.record_id=e.record_id AND r.id=e.renewal_id
	  WHERE e.record_id=$1 AND ($2<>'operator' OR scope.handoff_version IS NULL OR
	    ($3=scope.organization_name COLLATE "C" AND e.version<scope.handoff_version) OR
	    ($3=scope.current_organization_name COLLATE "C" AND e.version>=scope.handoff_version))
	  ORDER BY e.version DESC LIMIT 20`, id, seat.audience, seat.name)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[expiryEvent])
}
