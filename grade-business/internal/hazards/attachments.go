package hazards

import (
	"context"
	"errors"
	"net/http"
	"time"

	"grade-business/internal/cases"

	"github.com/jackc/pgx/v5"
)

const hazardAttachmentScope = hazardInstitutionScope + ` AND ($2='' OR (h.status<>'pending_dispatch'
	AND COALESCE(a.merge_history_scope,c.organization_name) COLLATE "C"=$2))`

// validHazardMaterial 核对办理端上传的文件属于其职责范围。
// 参数 ctx 含当前办理端，kind 为多段表单的材料类别。
// 返回值 类别为运营者整改证据或机构复测报告且匹配办理端时为真。
func validHazardMaterial(ctx context.Context, kind string) bool {
	if hazardInstitution(ctx) != "" {
		return kind == kindRetestReport
	}
	return kind == kindEvidence
}

// attachments 加载指定台账实际拥有的证据元数据。
// 参数 ctx 含可选运营单位或机构，id 为台账条目公开标识。
// 返回值 该台账的证据列表，或不存在和读取错误。
func (s *Service) attachments(ctx context.Context, id string) ([]cases.Attachment, error) {
	if err := s.ensureHazardAttachmentScope(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT a.id::text,a.kind,a.file_name,a.size_bytes,a.uploaded_at`+hazardJoin+`
		JOIN hazard_attachments a ON a.hazard_id=h.id
		WHERE h.id=$1 AND `+hazardAttachmentScope+` ORDER BY a.item_version DESC`,
		id, operatorOrganization(ctx), hazardInstitution(ctx))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[cases.Attachment])
}

// evidenceUploadVersion 在读取表单前确认范围、版本及当前是否允许上传材料。
// 参数 ctx 含可选运营单位或机构，id 为隐患标识，expected 为请求版本，零表示取当前版本。
// 返回值 暂存文件前确认的版本，或记录不存在、冲突及读取错误。
func (s *Service) evidenceUploadVersion(ctx context.Context, id string, expected int64) (int64, error) {
	var version int64
	var status string
	err := s.db.QueryRow(ctx, `SELECT h.version,h.status`+hazardJoin+` WHERE h.id=$1 AND `+hazardScope,
		id, operatorOrganization(ctx), hazardInstitution(ctx)).Scan(&version, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errNotFound
	}
	if err != nil {
		return 0, err
	}
	if (expected != 0 && version != expected) || !evidenceWritable(ctx, status) {
		return 0, errConflict
	}
	return version, nil
}

// evidenceWritable 核对当前办理端和隐患状态是否允许登记材料。
// 参数 ctx 含当前办理端，status 为隐患当前状态。
// 返回值 处于办理端可上传状态时为真。
func evidenceWritable(ctx context.Context, status string) bool {
	if hazardInstitution(ctx) != "" {
		return status == statusRetest
	}
	return editable(status)
}

// addEvidence 仅在上传期间版本未变化时登记材料，避免迟到文件串入后续办理轮次。
// 参数 ctx 含办理端范围，id、item、key、expected 为隐患、材料、存储键及暂存前确认的版本。
// 返回值 已登记的材料，或版本和状态冲突、记录不存在及持久化错误。
func (s *Service) addEvidence(ctx context.Context, id string, item cases.Attachment,
	key string, expected int64) (cases.Attachment, error) {
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		current, err := lockHazard(ctx, tx, id)
		if err != nil {
			return err
		}
		if !validHazardMaterial(ctx, item.Kind) {
			return errInvalid
		}
		if current.Version != expected || !evidenceWritable(ctx, current.Status) {
			return errConflict
		}
		err = tx.QueryRow(ctx, `INSERT INTO hazard_attachments
			(hazard_id,kind,file_name,file_key,size_bytes,item_version)
			VALUES($1,$2,$3,$4,$5,$6) RETURNING id::text,uploaded_at`,
			id, item.Kind, item.FileName, key, item.Size, current.Version+1).
			Scan(&item.ID, &item.UploadedAt)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE hazard_items SET version=version+1 WHERE id=$1`, id)
		return err
	})
	return item, err
}

// discardEvidence 清理失败请求中新保存且数据库未引用的证据文件。
// 参数 ctx 为原请求上下文，key 为该请求唯一生成的存储键。
// 返回值 清理完成时为 nil，数据库或文件操作失败时返回可追踪错误。
func (s *Service) discardEvidence(ctx context.Context, key string) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	var exists bool
	err := s.db.QueryRow(cleanup, `SELECT EXISTS(SELECT 1 FROM hazard_attachments
		WHERE file_key=$1)`, key).Scan(&exists)
	if err != nil || exists {
		return err
	}
	return s.records.RemoveUnregisteredAttachment(key)
}

// downloadEvidence 核对台账与证据的联合归属后发送受控文件。
// 参数 w、r 为响应和可选办理端范围的请求，id 为条目，attachmentID 为证据标识。
// 返回值 已发送文件时为 nil，或未找到、归属和文件读取错误。
func (s *Service) downloadEvidence(w http.ResponseWriter, r *http.Request, id, attachmentID string) error {
	if !validID(id) || !validID(attachmentID) {
		return errNotFound
	}
	var item cases.Attachment
	var key string
	err := s.db.QueryRow(r.Context(), `SELECT a.id::text,a.kind,a.file_name,a.size_bytes,a.uploaded_at,a.file_key`+
		hazardJoin+` JOIN hazard_attachments a ON a.hazard_id=h.id
		WHERE h.id=$1 AND `+hazardAttachmentScope+` AND a.id=$4`, id, operatorOrganization(r.Context()),
		hazardInstitution(r.Context()), attachmentID).
		Scan(&item.ID, &item.Kind, &item.FileName, &item.Size, &item.UploadedAt, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	return s.records.ServeBusinessAttachment(w, r, item, key)
}
