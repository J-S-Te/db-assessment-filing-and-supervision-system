package cases

import (
	"context"
	"errors"
	"strconv"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/reviewinbox"
)

const materialIssueResource = "material-issues"

var ErrUploadInterrupted = errors.New("attachment upload interrupted")

type materialIssue struct {
	ID               string     `json:"id"`
	CaseID           string     `json:"caseId,omitempty"`
	CaseKind         string     `json:"caseKind"`
	Reference        string     `json:"reference"`
	SystemName       string     `json:"systemName"`
	OrganizationName string     `json:"organizationName,omitempty"`
	Institution      string     `json:"institution,omitempty"`
	MaterialKind     string     `json:"materialKind"`
	FileName         string     `json:"fileName,omitempty"`
	ReasonCode       string     `json:"reasonCode"`
	Reason           string     `json:"reason"`
	Status           string     `json:"status"`
	Resolution       string     `json:"resolution,omitempty"`
	OpenedAt         time.Time  `json:"openedAt"`
	ClosedAt         *time.Time `json:"closedAt,omitempty"`
	CaseVersion      int64      `json:"caseVersion,omitempty"`
	CanRetry         bool       `json:"canRetry,omitempty"`
	CanDelete        bool       `json:"canDelete,omitempty"`
	Sequence         int64      `json:"-"`
}

type materialIssueFilter struct {
	audience, name, status, caseKind string
	id                               *string
	before                           int64
}

type materialIssuePage struct {
	Items      []materialIssue `json:"items"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type issueRecipient struct {
	ID, Institution, Organization, Reference, SystemName string
}

const issueRecipientColumns = "id::text,institution,organization_name,reference,system_name"

const materialIssueListSQL = `
	SELECT i.id::text,
	       CASE WHEN $1<>'operator' THEN i.case_id::text ELSE '' END,
	       i.case_kind,i.reference,i.system_name,
	       CASE WHEN $1='regulator' THEN i.organization_name ELSE '' END,
	       CASE WHEN $1='regulator' THEN i.institution ELSE '' END,
	       i.material_kind,CASE WHEN $1<>'operator' THEN i.file_name ELSE '' END,
	       i.reason_code,CASE WHEN $1='operator' THEN '机构材料未成功提交。' ELSE i.reason END,
	       CASE WHEN i.closed_at IS NULL THEN 'open' ELSE 'closed' END,
	       COALESCE(i.resolution,''),i.opened_at,i.closed_at,
	       CASE WHEN $1='institution' AND c.discarded_at IS NULL THEN c.version ELSE 0 END,
	       $1='institution' AND c.assessment_org=i.institution
	           AND i.closed_at IS NULL AND c.discarded_at IS NULL AND c.status IN ('draft','returned'),
	       $1='institution' AND c.assessment_org=i.institution AND i.case_kind='report'
	           AND c.discarded_at IS NULL AND c.submitted_at IS NULL AND c.status='draft'
	           AND i.closed_at IS NULL,
	       i.sequence
	FROM material_issues i JOIN business_cases c ON c.id=i.case_id
	WHERE ($1='regulator' OR ($1='institution' AND i.institution=$2)
	    OR ($1='operator' AND i.organization_name=$2))
	  AND ($3='all' OR ($3='open' AND i.closed_at IS NULL) OR ($3='closed' AND i.closed_at IS NOT NULL))
	  AND ($4='' OR i.case_kind=$4) AND ($5::uuid IS NULL OR i.id=$5)
	  AND ($6::bigint=0 OR i.sequence<$6)
	ORDER BY i.sequence DESC LIMIT 21
`

// listMaterialIssues 按办理端范围分页读取材料异常及其补正结果。
// 参数 ctx 为查询上下文，filter 为范围、状态和分页条件。
// 返回值 一页精简的异常记录或数据库错误。
func (s *Service) listMaterialIssues(ctx context.Context, filter materialIssueFilter) (materialIssuePage, error) {
	rows, err := s.db.Query(ctx, materialIssueListSQL, filter.audience, filter.name, filter.status,
		filter.caseKind, filter.id, filter.before)
	if err != nil {
		return materialIssuePage{}, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[materialIssue])
	result := materialIssuePage{Items: items}
	if len(items) > 20 {
		result.NextCursor = strconv.FormatInt(items[19].Sequence, 10)
		result.Items = items[:20]
	}
	return result, err
}

// materialFailureReason 将校验失败映射为可公开的原因，不含路径或内部错误。
// 参数 err 为上传时实际发生的校验或传输错误。
// 返回值 可跟踪的错误代码和机构及监管可见的说明。
func materialFailureReason(err error) (string, string) {
	switch {
	case errors.Is(err, ErrFilingPackage):
		return "filing_package", err.Error()
	case errors.Is(err, ErrFileType):
		return "attachment_type", "文件格式不受支持，或者文件内容损坏。"
	case errors.Is(err, ErrFileSize):
		return "attachment_size", "文件为空，或者超过材料大小限制。"
	case errors.Is(err, ErrUploadInterrupted):
		return "attachment_interrupted", "上传传输中断，请选择本地文件重新提交。"
	default:
		return "", ""
	}
}

// issueFileName 核对失败材料的显示名称，异常字符与路径不会进入业务记录。
// 参数 name 为上传表单声明的文件名。
// 返回值 通过校验的名称或空字符串。
func issueFileName(name string) string {
	if !validText(name, 150) {
		return ""
	}
	for _, symbol := range name {
		if symbol == '/' || symbol == '\\' || unicode.IsControl(symbol) {
			return ""
		}
	}
	return name
}

// recordMaterialFailure 在业务记录仍可补正且版本未变化时登记异常并同时送达。
// 参数 ctx 为有界写入上下文，kind、id、institution 与 version 表示上传时业务范围，
//
//	upload 为尝试的材料信息，code 和 reason 为可公开的失败原因。
//
// 返回值 成功、同类异常已存在，或归属、状态和数据库错误。
func (s *Service) recordMaterialFailure(ctx context.Context, kind, id, institution string,
	version int64, upload attachmentRow, code, reason string,
) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		current, err := lockRecord(ctx, tx, kind, id)
		if err != nil {
			return err
		}
		if err := validateMaterialUpload(ctx, tx, current, kind, id, institution, upload); err != nil {
			return err
		}
		if current.version != version {
			return ErrConflict
		}
		row, err := insertMaterialIssue(ctx, tx, kind, id, upload, code, reason)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return deliverMaterialNotice(ctx, tx, row, false, reason)
	})
}

// insertMaterialIssue 固定此次异常的业务归属，相同类别未关闭时沿用原异常。
// 参数 ctx 与 tx 为请求和事务，kind、id 指定记录，upload、code、reason 描述本次失败。
// 返回值 新异常的收件信息，无新增时返回 pgx.ErrNoRows。
func insertMaterialIssue(ctx context.Context, tx pgx.Tx, kind, id string, upload attachmentRow,
	code, reason string,
) (issueRecipient, error) {
	var row issueRecipient
	err := tx.QueryRow(ctx, `INSERT INTO material_issues(
		case_id,case_kind,institution,organization_name,system_name,reference,
		material_kind,file_name,reason_code,reason)
		SELECT c.id,c.kind,c.assessment_org,
		       COALESCE(c.directory_snapshot#>>'{unit,scopeName}',f.organization_name,c.organization_name),
		       COALESCE(f.system_name,c.system_name),c.number,$3,$4,$5,$6
		FROM business_cases c LEFT JOIN business_cases f ON f.id=c.filing_id
		WHERE c.id=$1 AND c.kind=$2
		ON CONFLICT (case_id,material_kind) WHERE closed_at IS NULL DO NOTHING
		RETURNING `+issueRecipientColumns,
		id, kind, upload.Kind, issueFileName(upload.FileName), code, reason).
		Scan(&row.ID, &row.Institution, &row.Organization, &row.Reference, &row.SystemName)
	return row, err
}

// ensureRetryIssue 核对重发对应当前记录的未关闭异常和原材料类别。
// 参数 ctx 与 tx 为核对事务，caseID、institution 为当前归属，upload 为材料及可选异常标识。
// 返回值 允许重发时为空，异常已关闭、归属或类别不符时返回冲突。
func ensureRetryIssue(ctx context.Context, tx pgx.Tx, caseID, institution string, upload attachmentRow) error {
	if upload.issueID == "" {
		return nil
	}
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM material_issues
		WHERE id=$1 AND case_id=$2 AND institution=$3 AND material_kind=$4 AND closed_at IS NULL)`,
		upload.issueID, caseID, institution, upload.Kind).Scan(&exists)
	if err == nil && !exists {
		return ErrConflict
	}
	return err
}

// closeMaterialIssues 保存真实的补正附件或删除结果，并为两类收件方登记关闭提醒。
// 参数 ctx 与 tx 控制写入，id 与 categories 指定异常范围，attachmentID 为有效替代材料或空值。
// 返回值 查询或投递错误，调用者根据结果提交或回滚。
func closeMaterialIssues(ctx context.Context, tx pgx.Tx, id string, categories []string, attachmentID *string) error {
	resolution, message := "corrected", "补正材料已通过校验，材料异常已关闭。"
	if attachmentID == nil {
		resolution, message = "discarded", "未送审的业务草稿已删除，材料异常已关闭。"
	}
	rows, err := tx.Query(ctx, `UPDATE material_issues
		SET closed_at=clock_timestamp(),resolution=$3,resolved_attachment_id=$4
		WHERE case_id=$1 AND ($2::text[] IS NULL OR material_kind=ANY($2)) AND closed_at IS NULL
		RETURNING `+issueRecipientColumns, id, categories, resolution, attachmentID)
	if err != nil {
		return err
	}
	recipients, err := pgx.CollectRows(rows, pgx.RowToStructByPos[issueRecipient])
	if err != nil {
		return err
	}
	for _, row := range recipients {
		if err := deliverMaterialNotice(ctx, tx, row, true, message); err != nil {
			return err
		}
	}
	return nil
}

// deliverMaterialNotice 向异常所属的机构与运营单位送出一次业务状态提醒。
// 参数 ctx 与 tx 为写入事务，row 为归属，closed 标记完成，reason 为可公开的处理说明。
// 返回值 两端通知均持久化成功或数据库错误。
func deliverMaterialNotice(ctx context.Context, tx pgx.Tx, row issueRecipient, closed bool,
	reason string,
) error {
	message := reviewinbox.Notice{Resource: materialIssueResource, ResourceID: row.ID,
		Reference: row.Reference, Subject: row.SystemName, State: "material_failed", Version: 1}
	if closed {
		message.State, message.Version = "material_closed", 2
	}
	message.Audience, message.Recipient, message.Comment = "institution", row.Institution, reason
	if err := reviewinbox.Deliver(ctx, tx, message); err != nil {
		return err
	}
	message.Audience, message.Recipient = "operator", row.Organization
	if !closed {
		message.Comment = "机构材料未成功提交，已提醒机构补正。"
	}
	return reviewinbox.Deliver(ctx, tx, message)
}
