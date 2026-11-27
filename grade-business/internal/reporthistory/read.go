package reporthistory

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"grade-business/internal/cases"
)

type historyReport struct {
	ID            string            `json:"id"`
	Number        string            `json:"number"`
	SystemName    string            `json:"systemName"`
	AssessmentOrg string            `json:"assessmentOrg"`
	Status        string            `json:"status"`
	Risk          string            `json:"risk"`
	CreatedAt     time.Time         `json:"createdAt"`
	SubmittedAt   *time.Time        `json:"submittedAt,omitempty"`
	ApprovedAt    *time.Time        `json:"approvedAt,omitempty"`
	ReportFile    *cases.Attachment `json:"reportFile,omitempty"`
}

type archivePage struct {
	Items      []historyReport `json:"items"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type archiveRow struct {
	report   historyReport
	fileID   *string
	fileName *string
	fileSize *int64
	fileAt   *time.Time
}

const archiveProjection = `r.id::text,r.number,filing.system_name,r.assessment_org,r.status,r.risk,
	r.created_at,r.submitted_at,approval.occurred_at,body.id::text,body.file_name,body.size_bytes,body.uploaded_at`

const reportChronology = `COALESCE(r.submitted_at,r.created_at)`

const archiveFrom = `
	FROM business_cases anchor
	JOIN business_cases r ON r.filing_id=anchor.filing_id AND r.kind='report' AND r.status='approved'
	JOIN business_cases filing ON filing.id=r.filing_id AND filing.kind='filing'
	LEFT JOIN LATERAL (
		SELECT a.id,a.file_name,a.size_bytes,a.uploaded_at FROM case_attachments a
		WHERE a.case_id=r.id AND a.kind='report_file' ORDER BY a.uploaded_at DESC,a.id DESC LIMIT 1
	) body ON true
	LEFT JOIN LATERAL (
		SELECT e.occurred_at FROM case_review_events e
		WHERE e.case_id=r.id AND e.action='approve' AND e.to_status='approved'
		ORDER BY e.version DESC LIMIT 1
	) approval ON true
`

// targets 提供归档报告常用列的扫描目标。
// 参数 无，使用待填充的归档和附件字段。
// 返回值 按 SQL 投影顺序排列的字段指针。
func (row *archiveRow) targets() []any {
	return []any{
		&row.report.ID, &row.report.Number, &row.report.SystemName, &row.report.AssessmentOrg,
		&row.report.Status, &row.report.Risk, &row.report.CreatedAt, &row.report.SubmittedAt,
		&row.report.ApprovedAt, &row.fileID, &row.fileName, &row.fileSize, &row.fileAt,
	}
}

// publicReport 返回模板和对比共用的报告元数据。
// 参数 无，使用已读归档记录和正文附件。
// 返回值 仅包含公开字段的归档报告。
func (row *archiveRow) publicReport() historyReport {
	item := row.report
	if row.fileID != nil {
		item.ReportFile = &cases.Attachment{
			ID: *row.fileID, Kind: "report_file", FileName: *row.fileName, Size: *row.fileSize, UploadedAt: *row.fileAt,
		}
	}
	return item
}

// archive 按真实提交时序分页读取同备案已通过报告。
// 参数 ctx 控制读取，anchor 为归属报告，filters 指定排序游标和页大小。
// 返回值 有界的同备案归档，或记录不存在及数据库错误。
func (s *Service) archive(ctx context.Context, anchor pgtype.UUID, filters archiveFilters) (archivePage, error) {
	at := pgtype.Timestamptz{Time: filters.cursor.time, Valid: filters.cursor.id.Valid}
	rows, err := s.db.Query(ctx, `SELECT `+archiveProjection+archiveFrom+`
		WHERE anchor.id=$1 AND anchor.kind='report'
		  AND ($2::timestamptz IS NULL OR (`+reportChronology+`,r.id)<($2,$3::uuid))
		ORDER BY `+reportChronology+` DESC,r.id DESC LIMIT $4`, anchor, at, filters.cursor.id, filters.limit+1)
	if err != nil {
		return archivePage{}, err
	}
	defer rows.Close()
	result := archivePage{Items: make([]historyReport, 0, filters.limit)}
	for rows.Next() {
		if len(result.Items) == filters.limit {
			result.NextCursor = encodeHistoryCursor(result.Items[len(result.Items)-1])
			break
		}
		var item archiveRow
		if err := rows.Scan(item.targets()...); err != nil {
			return archivePage{}, err
		}
		result.Items = append(result.Items, item.publicReport())
	}
	if err := rows.Err(); err != nil {
		return archivePage{}, err
	}
	if len(result.Items) != 0 {
		return result, nil
	}
	rows.Close()
	var found int
	err = s.db.QueryRow(ctx, `SELECT 1 FROM business_cases
		WHERE id=$1 AND kind='report' AND discarded_at IS NULL`, anchor).Scan(&found)
	return result, err
}
