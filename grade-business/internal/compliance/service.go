package compliance

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

const pageLimit = 20

const reportSource = ` FROM business_cases WHERE id=$1 AND kind='report' AND discarded_at IS NULL`

var (
	errMissing = errors.New("report not found")
	errInvalid = errors.New("invalid compliance input")
	errState   = errors.New("report version or status changed")
	errSource  = errors.New("evidence or knowledge version unavailable")
)

type recordInput struct {
	RequestID         string `json:"requestId"`
	ClauseNumber      string `json:"clauseNumber"`
	ClauseVersion     int64  `json:"clauseVersion"`
	ReportVersion     int64  `json:"expectedReportVersion"`
	AttachmentID      string `json:"attachmentId"`
	ExtractionVersion int64  `json:"extractionVersion"`
	FieldKey          string `json:"fieldKey"`
	KnowledgeID       string `json:"knowledgeId"`
	KnowledgeVersion  int64  `json:"knowledgeVersion"`
	Decision          string `json:"decision"`
	Comment           string `json:"comment"`
}

type evidenceField struct {
	Key       string  `json:"key"`
	Original  string  `json:"original"`
	Value     *string `json:"value,omitempty"`
	Start     int     `json:"startParagraph"`
	End       int     `json:"endParagraph"`
	Truncated bool    `json:"truncated,omitempty"`
	StartPage int     `json:"startPage,omitempty"`
	EndPage   int     `json:"endPage,omitempty"`
	Source    string  `json:"source,omitempty"`
}

type auditRecord struct {
	ID                string          `json:"id"`
	ReportVersion     int64           `json:"reportVersion"`
	AttachmentID      string          `json:"attachmentId"`
	AttachmentName    string          `json:"attachmentName"`
	ExtractionVersion int64           `json:"extractionVersion"`
	Field             evidenceField   `json:"field"`
	KnowledgeID       string          `json:"knowledgeId"`
	KnowledgeVersion  int64           `json:"knowledgeVersion"`
	KnowledgeTitle    string          `json:"knowledgeTitle"`
	KnowledgeSource   string          `json:"knowledgeSource"`
	Decision          string          `json:"decision"`
	Comment           string          `json:"comment"`
	Operator          string          `json:"operator"`
	CreatedAt         time.Time       `json:"createdAt"`
	Sequence          int64           `json:"-"`
	Clause            *clauseSnapshot `json:"clause,omitempty"`
	ReportID          string          `json:"-"`
}

const auditQuery = `SELECT c.id::text,c.report_version,c.attachment_id::text,a.file_name,
	c.extraction_version,c.field,c.knowledge_id::text,c.knowledge_version,k.title,k.source,
	c.decision,c.comment,c.operator,c.created_at,c.sequence,
	CASE WHEN l.version IS NOT NULL THEN jsonb_build_object('number',l.number,'version',l.version,'text',l.text) END,
	c.report_id::text
	FROM report_compliance_records c JOIN case_attachments a ON a.id=c.attachment_id
	JOIN knowledge_revisions k ON k.entry_id=c.knowledge_id AND k.version=c.knowledge_version
	LEFT JOIN knowledge_clause_revisions l ON l.knowledge_id=c.knowledge_id AND l.knowledge_version=c.knowledge_version
	AND l.number=c.clause_number AND l.version=c.clause_version`

type auditPage struct {
	Items      []auditRecord `json:"items"`
	CanRecord  bool          `json:"canRecord"`
	NextBefore string        `json:"nextBefore,omitempty"`
}

// canRecord 表示当前报告处于允许监管员登记依据的人工审核阶段。
// 参数 status 为报告当前的持久化状态。
// 返回值 待初审或待终审时为真。
func canRecord(status string) bool {
	return status == "pending_initial" || status == "pending_final"
}

// list 加载报告的历史合规依据及登记入口状态。
// 参数 ctx 为请求上下文，id 为报告标识，before 为可选分页游标。
// 返回值 稳定分页的留痕结果，或报告不存在及数据库错误。
func (s *Service) list(ctx context.Context, id string, before int64) (auditPage, error) {
	var status string
	err := s.db.QueryRow(ctx, "SELECT status"+reportSource, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return auditPage{}, errMissing
	}
	if err != nil {
		return auditPage{}, err
	}
	rows, err := s.db.Query(ctx, auditQuery+`
		WHERE c.report_id=$1 AND ($2::bigint=0 OR c.sequence<$2) ORDER BY c.sequence DESC LIMIT $3`,
		id, before, pageLimit+1)
	if err != nil {
		return auditPage{}, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[auditRecord])
	if err != nil {
		return auditPage{}, err
	}
	result := auditPage{Items: items, CanRecord: canRecord(status)}
	if len(items) > pageLimit {
		result.Items = items[:pageLimit]
		result.NextBefore = strconv.FormatInt(items[pageLimit-1].Sequence, 10)
	}
	return result, nil
}

// record 在同一数据库事务中锁定当前审核阶段、实际证据和启用的知识版本并追加记录。
// 参数 ctx 为请求上下文，id 为报告标识，input 为明确选择的版本及判定内容。
// 返回值 追加后的留痕记录，或业务状态、证据失效及持久化错误。
func (s *Service) record(ctx context.Context, id string, input recordInput) (auditRecord, error) {
	item := auditRecord{
		ReportVersion: input.ReportVersion, AttachmentID: input.AttachmentID, ExtractionVersion: input.ExtractionVersion,
		KnowledgeID: input.KnowledgeID, KnowledgeVersion: input.KnowledgeVersion,
		Decision: input.Decision, Comment: input.Comment, Operator: s.operator,
	}
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		replay, err := restoreRecord(ctx, tx, id, input, &item)
		if replay || err != nil {
			return err
		}
		if err := lockReview(ctx, tx, id, input.ReportVersion); err != nil {
			return err
		}
		if err := lockSources(ctx, tx, id, input.FieldKey, &item); err != nil {
			return err
		}
		if err := loadClauseSnapshot(ctx, tx, input, &item); err != nil {
			return err
		}
		return insertRecord(ctx, tx, id, input, &item)
	})
	return item, err
}

// lockReview 核对页面看到的报告版本仍处于人工审核，防止已终审或退回后追加新记录。
// 参数 ctx 为请求上下文，tx 为登记事务，id 和 expected 为报告标识和预期版本。
// 返回值 已锁定正确审核版本时为 nil，否则为业务或数据库错误。
func lockReview(ctx context.Context, tx pgx.Tx, id string, expected int64) error {
	var status string
	var version int64
	err := tx.QueryRow(ctx, "SELECT status,version"+reportSource+" FOR SHARE", id).Scan(&status, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return errMissing
	}
	if err == nil && (!canRecord(status) || version != expected) {
		return errState
	}
	return err
}

// lockSources 固定报告所选栏目的真实校对快照和当前启用的知识版本。
// 参数 ctx 为请求上下文，tx 为已锁定报告的事务，id 为报告标识，fieldKey 为证据栏目，item 接收来源。
// 返回值 两项来源仍可用时为 nil，或来源版本冲突及数据库错误。
func lockSources(ctx context.Context, tx pgx.Tx, id, fieldKey string, item *auditRecord) error {
	err := tx.QueryRow(ctx, `SELECT a.file_name,f.field FROM case_attachments a
		JOIN report_extractions e ON e.attachment_id=a.id
		JOIN report_extraction_reviews r ON r.attachment_id=e.attachment_id AND r.version=e.version
		CROSS JOIN LATERAL jsonb_array_elements(e.fields) AS f(field)
		WHERE a.case_id=$1 AND a.kind='report_file' AND a.id=$2 AND e.version=$3
		AND e.confirmed_at IS NOT NULL AND f.field->>'key'=$4 FOR SHARE OF a,e`,
		id, item.AttachmentID, item.ExtractionVersion, fieldKey).Scan(&item.AttachmentName, &item.Field)
	if errors.Is(err, pgx.ErrNoRows) {
		return errSource
	}
	if err != nil {
		return err
	}
	if item.Field.Start < 1 || item.Field.End < item.Field.Start {
		return errSource
	}
	err = tx.QueryRow(ctx, `SELECT r.title,r.source FROM knowledge_entries k
		JOIN knowledge_revisions r ON r.entry_id=k.id AND r.version=k.version
		WHERE k.id=$1 AND k.version=$2 AND k.inactive_at IS NULL AND r.inactive_at IS NULL FOR SHARE OF k`,
		item.KnowledgeID, item.KnowledgeVersion).Scan(&item.KnowledgeTitle, &item.KnowledgeSource)
	if errors.Is(err, pgx.ErrNoRows) {
		return errSource
	}
	return err
}
