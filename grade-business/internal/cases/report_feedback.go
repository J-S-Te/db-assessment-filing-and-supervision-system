package cases

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/directoryref"
)

const feedbackPageSize = 20

type feedbackInboxItem struct {
	ID             string     `json:"id"`
	ReportID       string     `json:"reportId"`
	Number         string     `json:"number"`
	SystemName     string     `json:"systemName"`
	Decision       string     `json:"decision"`
	Comment        string     `json:"comment,omitempty"`
	Status         *string    `json:"status,omitempty"`
	SentAt         time.Time  `json:"sentAt"`
	AcknowledgedAt *time.Time `json:"acknowledgedAt,omitempty"`
}

type feedbackInbox struct {
	Items      []feedbackInboxItem `json:"items"`
	NextCursor string              `json:"nextCursor,omitempty"`
}

type feedbackDelivery struct {
	Institution    string     `json:"institution"`
	Decision       string     `json:"decision"`
	SentAt         time.Time  `json:"sentAt"`
	AcknowledgedAt *time.Time `json:"acknowledgedAt,omitempty"`
}

type feedbackReceipt struct {
	AcknowledgedAt time.Time `json:"acknowledgedAt"`
}

type institutionReport struct {
	ID                 string                 `json:"id"`
	Number             string                 `json:"number"`
	SystemName         string                 `json:"systemName"`
	OrganizationName   string                 `json:"organizationName"`
	AssessmentOrg      string                 `json:"assessmentOrg"`
	FilingID           string                 `json:"filingId"`
	FilingNumber       string                 `json:"filingNumber"`
	Risk               string                 `json:"risk"`
	Status             string                 `json:"status"`
	SubmittedAt        *time.Time             `json:"submittedAt,omitempty"`
	Version            int64                  `json:"version"`
	CanCorrect         bool                   `json:"canCorrect"`
	AllowedActions     []string               `json:"allowedActions"`
	RequiredMaterials  []string               `json:"requiredMaterials,omitempty"`
	ReturnComment      *string                `json:"returnComment,omitempty"`
	Attachments        []Attachment           `json:"attachments"`
	AssessmentPersonID *string                `json:"assessmentPersonId,omitempty"`
	DirectorySnapshot  *directoryref.Snapshot `json:"directorySnapshot,omitempty"`
}

// checkInstitutionEdit 核对机构可以办理本机构的可编辑记录，自主备案只读。
// 参数 institution 是本地所选机构，空值由原监管流程校验。
// 返回值 归属不同、状态不允许或允许进入当前操作。
func (row stateRow) checkInstitutionEdit(institution string) error {
	if institution == "" {
		return nil
	}
	if row.assessmentOrg != institution {
		return ErrNotFound
	}
	if row.operatorOrigin || !editable(row.status) {
		return ErrConflict
	}
	return nil
}

// readFeedbackInbox 按回传时的接收机构和事件顺序读取一页消息。
// 参数 ctx 为请求上下文，institution 为当前机构，before 为上一页的送达序号游标。
// 返回值 收件箱消息和可选下一页位置，或数据库错误。
func (s *Service) readFeedbackInbox(ctx context.Context, institution string, before int64) (feedbackInbox, error) {
	rows, err := s.db.Query(ctx, `
		SELECT f.id::text,e.case_id::text,f.report_number,f.system_name,e.to_status,e.comment,
		    CASE WHEN c.assessment_org=f.institution THEN c.status END,
		    f.sent_at,f.acknowledged_at,f.sequence
		FROM report_feedback f JOIN case_review_events e ON e.id=f.event_id
		JOIN business_cases c ON c.id=e.case_id
		WHERE f.institution=$1 AND ($2::bigint=0 OR f.sequence<$2)
		ORDER BY f.sequence DESC LIMIT $3`, institution, before, feedbackPageSize+1)
	if err != nil {
		return feedbackInbox{}, err
	}
	defer rows.Close()
	result := feedbackInbox{Items: make([]feedbackInboxItem, 0, feedbackPageSize)}
	var cursor int64
	for rows.Next() {
		if len(result.Items) == feedbackPageSize {
			result.NextCursor = strconv.FormatInt(cursor, 10)
			break
		}
		var item feedbackInboxItem
		if err := rows.Scan(&item.ID, &item.ReportID, &item.Number, &item.SystemName, &item.Decision,
			&item.Comment, &item.Status, &item.SentAt, &item.AcknowledgedAt, &cursor); err != nil {
			return feedbackInbox{}, err
		}
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}

// acknowledgeFeedback 记录机构首次签收时刻，并让重复签收返回同一回执。
// 参数 ctx 为请求上下文，institution 与 id 指定接收范围和消息标识。
// 返回值 已持久化的签收时间，或消息不属于该机构等错误。
func (s *Service) acknowledgeFeedback(ctx context.Context, institution, id string) (feedbackReceipt, error) {
	if !validID(id) {
		return feedbackReceipt{}, ErrNotFound
	}
	var result feedbackReceipt
	err := s.db.QueryRow(ctx, `UPDATE report_feedback
		SET acknowledged_at=COALESCE(acknowledged_at,clock_timestamp())
		WHERE id=$1 AND institution=$2 RETURNING acknowledged_at`, id, institution).Scan(&result.AcknowledgedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return feedbackReceipt{}, ErrNotFound
	}
	return result, err
}

// readFeedbackDeliveries 为监管端读取指定报告最近的回传和签收摘要。
// 参数 ctx 为请求上下文，id 为测评报告标识。
// 返回值 回传摘要或记录不存在、查询错误。
func (s *Service) readFeedbackDeliveries(ctx context.Context, id string) ([]feedbackDelivery, error) {
	if err := s.ensureRecord(ctx, report, id); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT f.institution,e.to_status,f.sent_at,f.acknowledged_at
		FROM case_review_events e JOIN report_feedback f ON f.event_id=e.id
		WHERE e.case_id=$1 ORDER BY f.sequence DESC LIMIT 50`, id)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[feedbackDelivery])
	return items, err
}

const institutionReportSQL = `
	SELECT c.id::text,c.number,f.system_name,
	    COALESCE(c.directory_snapshot#>>'{unit,scopeName}',f.organization_name),c.assessment_org,
	    c.filing_id::text,f.number,c.risk,c.status,c.submitted_at,c.version,c.submission_profile,
	    CASE WHEN c.status='returned' THEN (
	        SELECT CASE WHEN m.id IS NOT NULL THEN e.comment END
	        FROM case_review_events e LEFT JOIN report_feedback m ON m.event_id=e.id AND m.institution=$2
	        WHERE e.case_id=c.id AND e.to_status='returned'
	        ORDER BY e.id DESC LIMIT 1
	    ) END,
	    COALESCE((SELECT jsonb_agg(jsonb_build_object('id',a.id,'kind',a.kind,'fileName',a.file_name,
	        'size',a.size_bytes,'uploadedAt',a.uploaded_at) ORDER BY a.uploaded_at DESC,a.id DESC)
	        FROM (SELECT id,kind,file_name,size_bytes,uploaded_at FROM (
	                SELECT id,kind,file_name,size_bytes,uploaded_at,
	                    row_number() OVER (PARTITION BY kind ORDER BY uploaded_at DESC,id DESC) AS kind_rank
	                FROM case_attachments WHERE case_id=c.id
            ) ranked ORDER BY (kind_rank=1) DESC,
	                uploaded_at DESC,id DESC LIMIT 30) a),'[]'::jsonb)
	    ,c.directory_person_id::text,c.directory_snapshot
	FROM business_cases c JOIN business_cases f ON f.id=c.filing_id
	WHERE c.kind='report' AND c.id=$1 AND c.assessment_org=$2 AND c.discarded_at IS NULL
`

// readInstitutionReport 查询本机构报告、当前退回意见和各类代表材料。
// 参数 ctx 为请求上下文，institution 为机构范围，id 为报告标识。
// 返回值 同一快照的报告数据，或归属和数据库错误。
func (s *Service) readInstitutionReport(ctx context.Context, institution, id string) (institutionReport, error) {
	if !validID(id) {
		return institutionReport{}, ErrNotFound
	}
	var result institutionReport
	var profile string
	err := s.db.QueryRow(ctx, institutionReportSQL, id, institution).Scan(&result.ID, &result.Number,
		&result.SystemName, &result.OrganizationName, &result.AssessmentOrg, &result.FilingID, &result.FilingNumber,
		&result.Risk, &result.Status, &result.SubmittedAt, &result.Version, &profile, &result.ReturnComment,
		&result.Attachments, &result.AssessmentPersonID, &result.DirectorySnapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return institutionReport{}, ErrNotFound
	}
	result.CanCorrect = editable(result.Status)
	result.AllowedActions = institutionEditActions(result.Status)
	if result.Status == draft && result.SubmittedAt == nil {
		result.AllowedActions = append(result.AllowedActions, "delete")
	}
	if profile == institutionSubmissionProfile {
		result.RequiredMaterials = institutionReportMaterials
	}
	return result, err
}

// republishReturnedFeedback 将仍待补正的报告意见送达改派后的机构。
// 参数 ctx 为请求上下文，tx 持有已更新报告，id 为报告标识。
// 返回值 送达记录写入错误，先前机构的回执不受改派影响。
func republishReturnedFeedback(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `INSERT INTO report_feedback(event_id,institution,report_number,system_name)
		SELECT e.id,c.assessment_org,c.number,f.system_name
		FROM case_review_events e JOIN business_cases c ON c.id=e.case_id
		JOIN business_cases f ON f.id=c.filing_id
		WHERE c.id=$1 AND e.to_status='returned'
		ORDER BY e.id DESC LIMIT 1
		ON CONFLICT (event_id,institution) DO NOTHING`, id)
	return err
}
