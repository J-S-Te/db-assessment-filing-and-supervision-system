package cases

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// FilingMaterials 返回在同一数据库快照中读取的备案实际材料和材料版本。
// 参数 ctx 为请求上下文，id 为备案记录标识。
// 返回值 备案材料快照，或记录不存在及数据库错误。
func (s *Service) FilingMaterials(ctx context.Context, id string) (MaterialSnapshot, error) {
	return s.materials(ctx, filing, id)
}

// materials 返回指定记录的附件版本和成员。
// 参数 ctx 为请求上下文，kind 为业务类型，id 为记录标识。
// 返回值 一致的附件快照，或归属及数据库错误。
func (s *Service) materials(ctx context.Context, kind, id string) (MaterialSnapshot, error) {
	return s.materialsInScope(ctx, kind, id, "")
}

// materialsInScope 查询当前归属记录的附件及材料版本。
// 参数 ctx 携带可选运营单位，kind 和 id 指定记录，institution 为可选机构范围。
// 返回值 一致的附件快照或数据库错误。
func (s *Service) materialsInScope(ctx context.Context, kind, id, institution string) (MaterialSnapshot, error) {
	if !validID(id) {
		return MaterialSnapshot{}, ErrNotFound
	}
	rows, err := s.db.Query(ctx, `
		SELECT c.material_revision,a.id::text,a.kind,a.file_name,a.size_bytes,a.uploaded_at
		FROM business_cases c LEFT JOIN case_attachments a ON c.id=a.case_id
		  AND ($4='' OR COALESCE(a.merge_history_scope,c.organization_name)=$4)
		WHERE c.kind=$1 AND c.id=$2 AND c.discarded_at IS NULL
		  AND ($3='' OR c.assessment_org=$3)
		  AND ($4='' OR (c.kind='filing' AND (c.organization_name=$4 OR a.id IS NOT NULL)))
		ORDER BY a.uploaded_at,a.id`, kind, id, institution, operatorFilingScope(ctx))
	if err != nil {
		return MaterialSnapshot{}, err
	}
	defer rows.Close()
	return readMaterials(rows)
}

// readMaterials 将可空的连接结果转成由已存在附件组成的快照。
// 参数 rows 为数据库返回的记录和附件列。
// 返回值 数据库读取得到的快照，或没有该记录及数据库错误。
func readMaterials(rows pgx.Rows) (MaterialSnapshot, error) {
	out := MaterialSnapshot{Attachments: []Attachment{}}
	found := false
	for rows.Next() {
		var id, kind, name *string
		var size *int64
		var uploaded *time.Time
		if err := rows.Scan(&out.Revision, &id, &kind, &name, &size, &uploaded); err != nil {
			return MaterialSnapshot{}, err
		}
		found = true
		if id != nil {
			out.Attachments = append(out.Attachments, Attachment{
				ID: *id, Kind: *kind, FileName: *name, Size: *size, UploadedAt: *uploaded,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return MaterialSnapshot{}, err
	}
	if !found {
		return MaterialSnapshot{}, ErrNotFound
	}
	return out, nil
}

// insertAttachment 核对锁定记录的材料类别，保存元数据并同步推进业务和材料版本。
// 参数 ctx 为请求上下文，kind 和 id 标识父记录，institution 为可选机构范围，uploaded 为上传文件。
// 返回值 已登记附件、是否复用同一材料，或父记录状态、归属及写入错误。
func (s *Service) insertAttachment(ctx context.Context, kind, id, institution string,
	uploaded attachmentRow,
) (Attachment, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Attachment{}, false, err
	}
	defer tx.Rollback(ctx)
	current, err := lockRecord(ctx, tx, kind, id)
	if err != nil {
		return Attachment{}, false, err
	}
	if err := validateMaterialUpload(ctx, tx, current, kind, id, institution, uploaded); err != nil {
		return Attachment{}, false, err
	}
	row, reused, err := persistMaterial(ctx, tx, id, uploaded)
	if err != nil {
		return Attachment{}, false, err
	}
	if err := closeValidatedMaterial(ctx, tx, kind, id, row); err != nil {
		return Attachment{}, false, err
	}
	return row, reused, tx.Commit(ctx)
}

// validateMaterialUpload 核对持锁后的机构归属、材料类别、在审限制和重发标识。
// 参数 ctx、tx 为写入事务，current 是锁定记录，kind、id、institution 与 upload 指定待办上传。
// 返回值 可处理时为空，或材料归属、状态和参数错误。
func validateMaterialUpload(ctx context.Context, tx pgx.Tx, current stateRow, kind, id,
	institution string, upload attachmentRow,
) error {
	if err := current.checkInstitutionEdit(institution); err != nil {
		return err
	}
	if !editable(current.status) {
		return ErrConflict
	}
	if !allowedUploadKind(kind, upload.Kind, current.submissionProfile, institution != "") {
		return ErrInvalid
	}
	if kind == report && current.requiresInitialFilingScope(institution) {
		if err := lockReportFiling(ctx, tx, current.filingID, current.assessmentOrg); err != nil {
			return err
		}
	}
	return ensureRetryIssue(ctx, tx, id, institution, upload)
}

// persistMaterial 复用同一类别最近一次相同内容，否则登记附件并推进版本。
// 参数 ctx、tx 为已锁定业务记录的事务，id 和 uploaded 指定文件与父记录。
// 返回值 实际附件、是否复用现有附件，或写入错误。
func persistMaterial(ctx context.Context, tx pgx.Tx, id string, uploaded attachmentRow) (Attachment, bool, error) {
	row := uploaded.Attachment
	if uploaded.digest != "" {
		existing, err := lastMatchingMaterial(ctx, tx, id, uploaded)
		if err == nil {
			return existing, true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Attachment{}, false, err
		}
	}
	err := tx.QueryRow(ctx, `INSERT INTO case_attachments(case_id,kind,file_name,file_key,size_bytes,upload_digest)
		VALUES($1,$2,$3,$4,$5,NULLIF($6,'')) RETURNING id::text,uploaded_at`,
		id, row.Kind, row.FileName, uploaded.fileKey, row.Size, uploaded.digest).
		Scan(&row.ID, &row.UploadedAt)
	if err != nil {
		return Attachment{}, false, err
	}
	_, err = tx.Exec(ctx, `UPDATE business_cases
		SET version=version+1,material_revision=material_revision+1 WHERE id=$1`, id)
	return row, false, err
}

// lastMatchingMaterial 匹配该类别最新的同名同内容文件，避免重复写入。
// 参数 ctx、tx、id 为记录查询范围，uploaded 为待校验的文件。
// 返回值 匹配材料；没有或已被其他材料替换时为 pgx.ErrNoRows。
func lastMatchingMaterial(ctx context.Context, tx pgx.Tx, id string, uploaded attachmentRow) (Attachment, error) {
	var row Attachment
	err := tx.QueryRow(ctx, `SELECT id::text,kind,file_name,size_bytes,uploaded_at FROM (
		SELECT id,kind,file_name,size_bytes,uploaded_at,upload_digest
		FROM case_attachments WHERE case_id=$1 AND kind=$2
		ORDER BY uploaded_at DESC,id DESC LIMIT 1
	) latest WHERE upload_digest=$3 AND file_name=$4`,
		id, uploaded.Kind, uploaded.digest, uploaded.FileName).
		Scan(&row.ID, &row.Kind, &row.FileName, &row.Size, &row.UploadedAt)
	return row, err
}

// closeValidatedMaterial 用实际登记的替代附件关闭同类异常。
// 参数 ctx、tx 为材料事务，kind、id 为父记录，row 为已验证的附件。
// 返回值 异常和关闭通知均写入时为 nil，否则返回事务错误。
func closeValidatedMaterial(ctx context.Context, tx pgx.Tx, kind, id string, row Attachment) error {
	categories := []string{row.Kind}
	if kind == filing && row.Kind == "filing_package" {
		categories = append(categories, "filing_form", "classification_report")
	}
	return closeMaterialIssues(ctx, tx, id, categories, &row.ID)
}

// readAttachment 查找归属于指定业务记录的单个附件。
// 参数 ctx 携带可选运营单位，kind、id 和 materialID 指定父记录及附件，institution 为可选机构范围。
// 返回值 附件元数据及内部存储键，或未找到和数据库错误。
func (s *Service) readAttachment(ctx context.Context, kind, id, materialID, institution string) (attachmentRow, error) {
	if !validID(id) || !validID(materialID) {
		return attachmentRow{}, ErrNotFound
	}
	var a attachmentRow
	err := s.db.QueryRow(ctx, `
		SELECT a.id::text,a.kind,a.file_name,a.size_bytes,a.uploaded_at,a.file_key
		FROM case_attachments a JOIN business_cases c ON a.case_id=c.id
		WHERE c.kind=$1 AND c.id=$2 AND a.id=$3 AND c.discarded_at IS NULL
		  AND ($4='' OR c.assessment_org=$4)
		  AND ($5='' OR (c.kind='filing' AND COALESCE(a.merge_history_scope,c.organization_name)=$5))`,
		kind, id, materialID, institution, operatorFilingScope(ctx)).
		Scan(&a.ID, &a.Kind, &a.FileName, &a.Size, &a.UploadedAt, &a.fileKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return attachmentRow{}, ErrNotFound
	}
	return a, err
}
