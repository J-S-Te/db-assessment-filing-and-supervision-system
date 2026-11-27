package reporthistory

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type assessmentSystem struct {
	ID         string `json:"id"`
	Number     string `json:"number"`
	SystemName string `json:"systemName"`
}

type operatorSystemsPage struct {
	Items      []assessmentSystem `json:"items"`
	NextCursor string             `json:"nextCursor,omitempty"`
}

type assessmentPoint struct {
	ID            string             `json:"id"`
	Number        string             `json:"number"`
	AssessmentOrg string             `json:"assessmentOrg"`
	Risk          string             `json:"risk"`
	AssessedAt    time.Time          `json:"assessedAt"`
	Result        *assessmentSummary `json:"result,omitempty"`
	score         int
}

type operatorHistoryPage struct {
	System     assessmentSystem  `json:"system"`
	Items      []assessmentPoint `json:"items"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

type operatorAssessmentComparison struct {
	From        assessmentPoint `json:"from"`
	To          assessmentPoint `json:"to"`
	RiskChange  string          `json:"riskChange"`
	ScoreDelta  *float64        `json:"scoreDelta,omitempty"`
	HighDelta   *int            `json:"highDelta,omitempty"`
	MediumDelta *int            `json:"mediumDelta,omitempty"`
	LowDelta    *int            `json:"lowDelta,omitempty"`
}

const operatorAssessmentScope = `COALESCE((SELECT d.scope_name FROM directory_parties d
	WHERE d.kind='unit' AND (d.scope_name=$1 OR d.name=$1)
	ORDER BY (d.scope_name=$1) DESC,d.id LIMIT 1),$1)`

const operatorFilingState = `f.kind='filing' AND f.status='approved' AND f.discarded_at IS NULL`
const operatorCurrentOwner = `f.organization_name = ` + operatorAssessmentScope
const operatorOriginalReport = `r.merge_history_scope = ` + operatorAssessmentScope

// 复杂归并后未冻结的旧报告不具备可移交摘要，沿用简单归并原有摘要规则。
const operatorNotMovedByComplexMerge = `NOT EXISTS (
	SELECT 1 FROM directory_unit_merges m
	WHERE m.plan->>'mode'='complex' AND m.merged_at>=` + reportChronology + `
	  AND m.plan->'moves' @> jsonb_build_array(jsonb_build_object('id',f.id::text))
)`

const operatorSummaryReport = `((r.merge_history_scope IS NULL AND ` + operatorCurrentOwner + `
	AND ` + operatorNotMovedByComplexMerge + `) OR ` + operatorOriginalReport + `)`
const operatorApprovedReport = `r.kind='report' AND r.status='approved' AND r.discarded_at IS NULL`

const operatorArchiveSystemSQL = `SELECT f.id::text,
	CASE WHEN archived.id IS NULL THEN f.number ELSE archived.merge_history_number END::text AS number,
	CASE WHEN archived.id IS NULL THEN f.system_name ELSE archived.merge_history_system_name END::text AS system_name
	FROM business_cases f LEFT JOIN LATERAL (
		SELECT r.id,r.merge_history_number,r.merge_history_system_name
		FROM business_cases r WHERE r.filing_id=f.id AND ` + operatorApprovedReport + `
		AND ` + operatorOriginalReport + `
		ORDER BY ` + reportChronology + ` DESC,r.id DESC LIMIT 1
	) archived ON NOT (` + operatorCurrentOwner + `)
	WHERE ` + operatorFilingState + ` AND ((` + operatorCurrentOwner + `) OR archived.id IS NOT NULL)`

const assessmentPointQuery = `SELECT r.id::text,r.number,
	COALESCE(NULLIF(r.directory_snapshot->'institution'->>'name',''),r.assessment_org),
	r.risk,` + reportChronology + `,a.version,COALESCE(a.score_hundredths,0),COALESCE(a.conclusion,''),
	COALESCE(a.high_count,0),COALESCE(a.medium_count,0),COALESCE(a.low_count,0)
	FROM business_cases f
	JOIN business_cases r ON r.filing_id=f.id AND ` + operatorApprovedReport + `
	LEFT JOIN LATERAL (
		SELECT version,score_hundredths,conclusion,high_count,medium_count,low_count
		FROM assessment_result_revisions WHERE report_id=r.id ORDER BY version DESC LIMIT 1
	) a ON TRUE
	WHERE ` + operatorFilingState + ` AND ` + operatorSummaryReport + ` AND f.id=$2`

// listOperatorSystems 查找运营单位拥有历史测评的备案，按备案标识稳定翻页。
// 参数 ctx、organization、keyword、before、limit 为查询及有界筛选条件。
// 返回值 该单位可选择的备案及下一页游标，或数据库错误。
func (s *Service) listOperatorSystems(ctx context.Context, organization, keyword string,
	before pgtype.UUID, limit int,
) (operatorSystemsPage, error) {
	rows, err := s.db.Query(ctx, `SELECT v.id,v.number,v.system_name FROM (`+operatorArchiveSystemSQL+`
		AND ($2::uuid IS NULL OR f.id < $2)
		AND EXISTS (SELECT 1 FROM business_cases r WHERE r.filing_id=f.id AND `+operatorApprovedReport+`
		  AND `+operatorSummaryReport+`)
	) v WHERE ($3='' OR strpos(lower(v.number||' '||v.system_name),lower($3))>0)
	ORDER BY v.id DESC LIMIT $4`, organization, before, keyword, limit+1)
	if err != nil {
		return operatorSystemsPage{}, err
	}
	defer rows.Close()
	page := operatorSystemsPage{Items: make([]assessmentSystem, 0, limit)}
	for rows.Next() {
		if len(page.Items) == limit {
			page.NextCursor = page.Items[limit-1].ID
			break
		}
		var item assessmentSystem
		if err := rows.Scan(&item.ID, &item.Number, &item.SystemName); err != nil {
			return operatorSystemsPage{}, err
		}
		page.Items = append(page.Items, item)
	}
	return page, rows.Err()
}

// operatorHistory 同一数据库快照内确认备案归属并读取已批准的测评摘要。
// 参数 ctx、organization、filing、filters 为请求上下文、所选单位、备案及分页条件。
// 返回值 备案和有界归档列表，或所选备案不在范围、数据库读取错误。
func (s *Service) operatorHistory(ctx context.Context, organization string, filing pgtype.UUID,
	filters archiveFilters,
) (operatorHistoryPage, error) {
	var result operatorHistoryPage
	err := pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly},
		func(tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, operatorArchiveSystemSQL+" AND f.id=$2", organization, filing).
				Scan(&result.System.ID, &result.System.Number, &result.System.SystemName); err != nil {
				return err
			}
			var err error
			result.Items, result.NextCursor, err = operatorHistoryRows(ctx, tx, organization, filing, filters)
			return err
		})
	return result, err
}

// operatorHistoryRows 在已校验的所属范围读取一页测评结果。
// 参数 ctx、tx、organization、filing、filters 为读取事务及备案分页。
// 返回值 有界摘要、下一页游标及数据库错误。
func operatorHistoryRows(ctx context.Context, tx pgx.Tx, organization string, filing pgtype.UUID,
	filters archiveFilters,
) ([]assessmentPoint, string, error) {
	at := pgtype.Timestamptz{Time: filters.cursor.time, Valid: filters.cursor.id.Valid}
	rows, err := tx.Query(ctx, assessmentPointQuery+`
		AND ($3::timestamptz IS NULL OR (`+reportChronology+`,r.id)<($3,$4::uuid))
		ORDER BY `+reportChronology+` DESC,r.id DESC LIMIT $5`,
		organization, filing, at, filters.cursor.id, filters.limit+1)
	if err != nil {
		return nil, "", err
	}
	items, err := pgx.CollectRows(rows, scanAssessmentPoint)
	if len(items) <= filters.limit {
		return items, "", err
	}
	last := items[filters.limit-1]
	cursor := base64.RawURLEncoding.EncodeToString(
		[]byte(last.AssessedAt.UTC().Format(time.RFC3339Nano) + "," + last.ID))
	return items[:filters.limit], cursor, err
}

// scanAssessmentPoint 转换存储结果，仅发送允许运营者读取的字段。
// 参数 row 为一个已审批、归属已核对的报告行。
// 返回值 安全摘要，或查询字段无法解码的错误。
func scanAssessmentPoint(row pgx.CollectableRow) (assessmentPoint, error) {
	var point assessmentPoint
	var values assessmentValues
	var version *int64
	err := row.Scan(&point.ID, &point.Number, &point.AssessmentOrg, &point.Risk, &point.AssessedAt,
		&version, &values.score, &values.conclusion, &values.high, &values.medium, &values.low)
	if version != nil {
		summary := values.summary()
		point.Result, point.score = &summary, values.score
	}
	point.AssessedAt = point.AssessedAt.UTC()
	return point, err
}

// compareOperatorHistory 在一个数据库快照比较两份同备案的审批报告。
// 参数 ctx、organization、filing、from、to 依次限定读取范围和两份报告。
// 返回值 风险等级变化和可核验的计数、得分差值，或无权读取及顺序错误。
func (s *Service) compareOperatorHistory(ctx context.Context, organization string, filing, from, to pgtype.UUID,
) (operatorAssessmentComparison, error) {
	rows, err := s.db.Query(ctx, assessmentPointQuery+` AND r.id IN ($3,$4)
		ORDER BY `+reportChronology+`,r.id`, organization, filing, from, to)
	if err != nil {
		return operatorAssessmentComparison{}, err
	}
	items, err := pgx.CollectRows(rows, scanAssessmentPoint)
	if err != nil {
		return operatorAssessmentComparison{}, err
	}
	if len(items) != 2 {
		return operatorAssessmentComparison{}, pgx.ErrNoRows
	}
	if items[0].ID != from.String() {
		return operatorAssessmentComparison{}, errOrder
	}
	return differenceBetweenAssessments(items[0], items[1]), nil
}

// differenceBetweenAssessments 仅在前后期都存在已核验结果时计算差值。
// 参数 from、to 分别为数据库中同备案的先后期数据。
// 返回值 风险等级变化和可选的数值差异。
func differenceBetweenAssessments(from, to assessmentPoint) operatorAssessmentComparison {
	out := operatorAssessmentComparison{From: from, To: to, RiskChange: compareRisk(from.Risk, to.Risk)}
	if from.Result == nil || to.Result == nil {
		return out
	}
	score := float64(to.score-from.score) / 100
	high, medium, low := to.Result.High-from.Result.High, to.Result.Medium-from.Result.Medium, to.Result.Low-from.Result.Low
	out.ScoreDelta, out.HighDelta, out.MediumDelta, out.LowDelta = &score, &high, &medium, &low
	return out
}
