package reporthistory

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	errResultConflict = errors.New("assessment result version changed")
	errResultEvidence = errors.New("assessment evidence is invalid")
	assessmentScore   = regexp.MustCompile(`^(0|[1-9][0-9]{0,2})(\.[0-9]{1,2})?$`)
)

type assessmentResultInput struct {
	ExpectedReportVersion int64           `json:"expectedReportVersion"`
	ExpectedResultVersion *int64          `json:"expectedResultVersion"`
	EvidenceID            string          `json:"evidenceId"`
	Score                 json.RawMessage `json:"score"`
	Conclusion            string          `json:"conclusion"`
	High                  *int            `json:"high"`
	Medium                *int            `json:"medium"`
	Low                   *int            `json:"low"`
}

type assessmentValues struct {
	score             int
	conclusion        string
	high, medium, low int
}

type assessmentSummary struct {
	Score      float64 `json:"score"`
	Conclusion string  `json:"conclusion"`
	High       int     `json:"high"`
	Medium     int     `json:"medium"`
	Low        int     `json:"low"`
}

type assessmentResult struct {
	assessmentSummary
	Version    int64     `json:"version"`
	EvidenceID string    `json:"evidenceId"`
	Actor      string    `json:"actor"`
	RecordedAt time.Time `json:"recordedAt"`
}

type assessmentEvidence struct {
	ID       string `json:"id"`
	FileName string `json:"fileName"`
	Kind     string `json:"kind"`
}

type assessmentEditor struct {
	Result   *assessmentResult    `json:"result,omitempty"`
	Evidence []assessmentEvidence `json:"evidence"`
	Editable bool                 `json:"editable"`
}

type assessmentRevisionPage struct {
	Items      []assessmentResult `json:"items"`
	NextBefore int64              `json:"nextBefore,omitempty"`
}

type assessmentReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

const resultColumns = `score_hundredths,conclusion,high_count,medium_count,low_count,
	version,evidence_id::text,actor,recorded_at`

// values 校验证据标识、快照版本、百分制分数、枚举结果及三档风险数量。
// 参数 无，使用 JSON 解析后的输入。
// 返回值 整数百分位分数与风险数量、规范证据标识或输入错误。
func (input assessmentResultInput) values() (assessmentValues, pgtype.UUID, error) {
	evidence, valid := parseHistoryUUID(input.EvidenceID)
	if !valid || input.ExpectedReportVersion < 1 || input.ExpectedResultVersion == nil ||
		*input.ExpectedResultVersion < 0 || !assessmentScore.Match(input.Score) ||
		!validAssessmentConclusion(input.Conclusion) || !validRiskCount(input.High) ||
		!validRiskCount(input.Medium) || !validRiskCount(input.Low) {
		return assessmentValues{}, evidence, errInput
	}
	score, err := strconv.ParseFloat(string(input.Score), 64)
	if err != nil || score > 100 {
		return assessmentValues{}, evidence, errInput
	}
	values := assessmentValues{int(math.Round(score * 100)), input.Conclusion,
		*input.High, *input.Medium, *input.Low}
	return values, evidence, nil
}

// validAssessmentConclusion 约束对运营者公开的标准结论枚举。
// 参数 value 为监管确认的结论代码。
// 返回值 枚举内返回真。
func validAssessmentConclusion(value string) bool {
	return value == "conforming" || value == "basically_conforming" || value == "nonconforming"
}

// validRiskCount 区分未填写和事实记录中的零个风险项。
// 参数 count 为报告给出的该级风险项数。
// 返回值 已填写且在可接受整数范围内返回真。
func validRiskCount(count *int) bool {
	return count != nil && *count >= 0 && *count <= 100000
}

// summary 将数据库的整数百分位分数转成业务百分制摘要。
// 参数 无，使用已经验证或存储的结构化值。
// 返回值 可对运营者公开的结果字段。
func (item assessmentValues) summary() assessmentSummary {
	return assessmentSummary{float64(item.score) / 100, item.conclusion, item.high, item.medium, item.low}
}

// scanAssessmentResult 同时读取可比对的持久值和监管所见版本详情。
// 参数 row 为数据库返回的一个结果版本。
// 返回值 监管结果、用于重复校验的整数值及读取错误。
func scanAssessmentResult(row pgx.Row) (assessmentResult, assessmentValues, error) {
	var result assessmentResult
	var values assessmentValues
	err := row.Scan(&values.score, &values.conclusion, &values.high, &values.medium, &values.low,
		&result.Version, &result.EvidenceID, &result.Actor, &result.RecordedAt)
	result.assessmentSummary = values.summary()
	result.RecordedAt = result.RecordedAt.UTC()
	return result, values, err
}

// assessmentRecord 查找指定报告当前登记的监管结果。
// 参数 ctx 为查询上下文，db 为事务或连接池，id 为报告标识。
// 返回值 最新快照及其值，没有登记时返回 ErrNoRows。
func assessmentRecord(ctx context.Context, db assessmentReader, id pgtype.UUID) (assessmentResult, assessmentValues, error) {
	return scanAssessmentResult(db.QueryRow(ctx, `SELECT `+resultColumns+`
		FROM assessment_result_revisions WHERE report_id=$1 ORDER BY version DESC LIMIT 1`, id))
}

// assessmentProof 选择报告当前最新结论附件，兼容旧报告的正文证据。
// 参数 ctx 为请求上下文，db 为数据库或业务事务，id 为已确认报告标识。
// 返回值 允许作为证据的附件，缺失时为空而无错误。
func assessmentProof(ctx context.Context, db assessmentReader, id pgtype.UUID) (*assessmentEvidence, error) {
	var file assessmentEvidence
	err := db.QueryRow(ctx, `SELECT id::text,file_name,kind FROM case_attachments
		WHERE case_id=$1 AND kind IN ('assessment_conclusion','report_file')
		ORDER BY (kind='assessment_conclusion') DESC,uploaded_at DESC,id DESC LIMIT 1`, id).
		Scan(&file.ID, &file.FileName, &file.Kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &file, err
}

// resultEditor 读取报告登记窗口所需的最新结果、可用证据和状态。
// 参数 ctx 为请求上下文，id 为监管正在办理的报告。
// 返回值 当前登记窗口或报告不存在、读取错误。
func (s *Service) resultEditor(ctx context.Context, id pgtype.UUID) (assessmentEditor, error) {
	result := assessmentEditor{Evidence: []assessmentEvidence{}}
	err := pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly},
		func(tx pgx.Tx) error {
			var status string
			if err := tx.QueryRow(ctx, `SELECT status FROM business_cases
				WHERE id=$1 AND kind='report' AND discarded_at IS NULL`, id).Scan(&status); err != nil {
				return err
			}
			current, _, err := assessmentRecord(ctx, tx, id)
			if err == nil {
				result.Result = &current
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			file, err := assessmentProof(ctx, tx, id)
			if file != nil {
				result.Evidence = append(result.Evidence, *file)
			}
			result.Editable = file != nil && status == "approved"
			return err
		})
	return result, err
}

// recordAssessment 以原子事务校验当前报告、当前证据、数据版本并追加留痕。
// 参数 ctx、id、input 分别为请求上下文、报告标识及登记表单。
// 返回值 已登记或重复重试时已有的数据版本，以及冲突或校验异常。
func (s *Service) recordAssessment(ctx context.Context, id pgtype.UUID, input assessmentResultInput) (assessmentResult, error) {
	values, evidence, err := input.values()
	if err != nil {
		return assessmentResult{}, err
	}
	var result assessmentResult
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := validateAssessmentTarget(ctx, tx, id, input.ExpectedReportVersion, evidence); err != nil {
			return err
		}
		current, oldValues, err := assessmentRecord(ctx, tx, id)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if current.Version > 0 && current.EvidenceID == evidence.String() && oldValues == values {
			result = current
			return nil
		}
		if current.Version != *input.ExpectedResultVersion {
			return errResultConflict
		}
		result, _, err = scanAssessmentResult(tx.QueryRow(ctx, `INSERT INTO assessment_result_revisions
			(report_id,version,report_version,evidence_id,score_hundredths,conclusion,high_count,medium_count,low_count,actor)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+resultColumns,
			id, current.Version+1, input.ExpectedReportVersion, evidence, values.score, values.conclusion,
			values.high, values.medium, values.low, s.operator))
		return err
	})
	return result, err
}

// validateAssessmentTarget 在报告写锁内核对当前业务状态和已定稿的材料归属。
// 参数 ctx、tx、id、expected、evidence 为请求、事务、报告、报告版本和证据。
// 返回值 报告不可用、版本变化、附件不属于当前报告或数据库异常。
func validateAssessmentTarget(ctx context.Context, tx pgx.Tx, id pgtype.UUID, expected int64, evidence pgtype.UUID) error {
	var version int64
	var status string
	err := tx.QueryRow(ctx, `SELECT status,version FROM business_cases
		WHERE id=$1 AND kind='report' AND discarded_at IS NULL FOR UPDATE`, id).Scan(&status, &version)
	if err != nil {
		return err
	}
	if status != "approved" || version != expected {
		return errResultConflict
	}
	proof, err := assessmentProof(ctx, tx, id)
	if err != nil {
		return err
	}
	if proof == nil || proof.ID != evidence.String() {
		return errResultEvidence
	}
	return nil
}

// resultHistory 按版本倒序读取已登记的得分、结论、证据及经办留痕。
// 参数 ctx、id、before 分别为上下文、报告标识及可选的排除版本。
// 返回值 有界的历史版本列表、翻页位置或读取错误。
func (s *Service) resultHistory(ctx context.Context, id pgtype.UUID, before int64) (assessmentRevisionPage, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM business_cases
		WHERE id=$1 AND kind='report' AND discarded_at IS NULL)`, id).Scan(&exists)
	if err != nil {
		return assessmentRevisionPage{}, err
	}
	if !exists {
		return assessmentRevisionPage{}, pgx.ErrNoRows
	}
	rows, err := s.db.Query(ctx, `SELECT `+resultColumns+`
		FROM assessment_result_revisions WHERE report_id=$1 AND ($2::bigint=0 OR version<$2)
		ORDER BY version DESC LIMIT 21`, id, before)
	if err != nil {
		return assessmentRevisionPage{}, err
	}
	defer rows.Close()
	page := assessmentRevisionPage{Items: make([]assessmentResult, 0, 20)}
	for rows.Next() {
		if len(page.Items) == 20 {
			page.NextBefore = page.Items[19].Version
			break
		}
		item, _, err := scanAssessmentResult(rows)
		if err != nil {
			return assessmentRevisionPage{}, err
		}
		page.Items = append(page.Items, item)
	}
	return page, rows.Err()
}
