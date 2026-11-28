package supervision

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/access"
	"grade-business/internal/cases"
)

const materialColumns = `a.id::text,a.kind,a.file_name,a.size_bytes,a.uploaded_at`

type savedMaterial struct {
	cases.Attachment
	Version int64
	Digest  string
}

// materialTargets 提供材料表列所对应的公开附件扫描字段。
// 参数 item 为需要写入的附件元数据。
// 返回值 依次为标识、类别、文件、字节及时间字段。
func materialTargets(item *cases.Attachment) []any {
	return []any{&item.ID, &item.Kind, &item.FileName, &item.Size, &item.UploadedAt}
}

// isTemplate 判断类别是否属于监管下发的模板材料。
// 参数 kind 是已解析材料分类。
// 返回值 模板文件为真。
func isTemplate(kind string) bool {
	return kind == inspectionTemplate || kind == summaryTemplate
}

// canUpload 在文件暂存与正式写入时核对材料分类、阶段和席位。
// 参数 record 为当前任务状态，owner 为可选运营单位，kind 为上传类别。
// 返回值 可以进行本轮上传时返回真。
func canUpload(record taskRecord, owner, kind string) bool {
	if owner == "" {
		return record.Status == "draft" && isTemplate(kind)
	}
	return editableResult(record.Status) && (kind == inspectionForm || kind == rectificationForm)
}

// latestMaterial 查询指定类别的当前材料和上传版本用于幂等及补正判断。
// 参数 ctx、db 控制查询，id 为任务标识，kind、after 为材料类别和移交截止版本。
// 返回值 最新材料或查询失败。
func latestMaterial(ctx context.Context, db reader, id, kind string, after int64) (savedMaterial, error) {
	var item savedMaterial
	err := db.QueryRow(ctx, `SELECT `+materialColumns+`,a.task_version,a.digest
		FROM supervision_attachments a WHERE a.task_id=$1 AND a.kind=$2 AND a.task_version>$3
		ORDER BY a.task_version DESC LIMIT 1`, id, kind, after).
		Scan(append(materialTargets(&item.Attachment), &item.Version, &item.Digest)...)
	return item, err
}

// requiredMaterials 读取一对必传模板或业务文件，并统一缺件提示。
// 参数 ctx、db 为当前事务，record 是任务，first 与 second 指所需类别。
// 返回值 两份材料及必要的校验或查询错误。
func requiredMaterials(ctx context.Context, db reader, record taskRecord,
	first, second string,
) (savedMaterial, savedMaterial, error) {
	after := record.HistoryVersion
	if isTemplate(first) && isTemplate(second) {
		after = 0
	}
	left, err := latestMaterial(ctx, db, record.ID, first, after)
	if err != nil {
		return savedMaterial{}, savedMaterial{}, requiredError(err)
	}
	right, err := latestMaterial(ctx, db, record.ID, second, after)
	return left, right, requiredError(err)
}

// requiredError 把缺少指定材料映射成可理解的提交校验错误。
// 参数 err 为读取必传文件的结果。
// 返回值 缺件错误或原始错误。
func requiredError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errMaterials
	}
	return err
}

// latestVisibleMaterials 返回子任务模板和按有效送审版本可见的最新材料。
// 参数 ctx、db 为当前查询，record 为任务，owner 为可选运营单位。
// 返回值 四类可见材料或数据库错误。
func latestVisibleMaterials(ctx context.Context, db reader, record taskRecord, owner string) ([]cases.Attachment, error) {
	versions := inspectionVersions(owner, record.Historical, record.HistoryVersion)
	rows, err := db.Query(ctx, `SELECT DISTINCT ON (kind) id,kind,file_name,size_bytes,uploaded_at FROM (
		SELECT `+materialColumns+`,a.task_version FROM supervision_attachments a WHERE a.task_id=$1
		  AND (a.kind IN ('inspection_template','summary_template') OR (( $2 OR a.task_version<=$3)
		    AND a.task_version>$4 AND ($5::bigint=0 OR a.task_version<=$5)))
		UNION ALL
		SELECT `+materialColumns+`,0 FROM supervision_dispatched_templates st
		JOIN supervision_attachments a ON a.id=st.source_attachment_id WHERE st.task_id=$1
		) visible ORDER BY kind,task_version DESC`, record.ID, owner != "", record.SubmittedVer,
		versions.after, versions.through)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[cases.Attachment])
}

// writeMaterial 原子写入一份有效文件元数据及任务版本，并复用相同最新文件。
// 参数 ctx 控制事务，id、owner、expected 指当前办理归属和版本，item、key、digest 为已校验附件。
// 返回值 最小附件结果与任务版本，或归属、持久化及阶段错误。
func (s *Service) writeMaterial(ctx context.Context, id, owner string, expected int64,
	item cases.Attachment, key, digest string,
) (attachmentMutation, error) {
	var result attachmentMutation
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		record, err := loadTask(ctx, tx, id, owner, true)
		if err != nil {
			return err
		}
		if !canUpload(record, owner, item.Kind) {
			return errState
		}
		if err := lockWritableFiling(ctx, tx, record, false); err != nil {
			return err
		}
		after := inspectionVersions(owner, false, record.HistoryVersion).after
		last, err := latestMaterial(ctx, tx, id, item.Kind, after)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if last.Digest == digest && expected <= record.Version {
			result = attachmentMutation{last.Attachment, record.Version}
			return nil
		}
		if record.Version != expected {
			return errState
		}
		result, err = persistMaterial(ctx, tx, record, item, key, digest)
		return err
	})
	return result, err
}

// persistMaterial 记录文件及对应版本，确保操作前后任务一致。
// 参数 ctx、tx 为已锁定事务，record、item、key、digest 为持久化条件。
// 返回值 元数据与任务版本，或任一步写入错误。
func persistMaterial(ctx context.Context, tx pgx.Tx, record taskRecord,
	item cases.Attachment, key, digest string,
) (attachmentMutation, error) {
	version := record.Version + 1
	err := tx.QueryRow(ctx, `INSERT INTO supervision_attachments(task_id,kind,file_name,file_key,digest,size_bytes,task_version)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text,uploaded_at`, record.ID, item.Kind,
		item.FileName, key, digest, item.Size, version).Scan(&item.ID, &item.UploadedAt)
	if err != nil {
		return attachmentMutation{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE supervision_tasks SET version=$2,updated_at=clock_timestamp() WHERE id=$1`, record.ID, version)
	return attachmentMutation{item, version}, err
}

// discardUnused 清理确认没有数据库引用的本次暂存文件。
// 参数 ctx 为原始请求环境，key 为本次唯一暂存键。
// 返回值 无。数据库状态不明确时保留文件并登记错误类型。
func (s *Service) discardUnused(ctx context.Context, key string) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	var exists bool
	err := s.db.QueryRow(cleanup, `SELECT EXISTS (SELECT 1 FROM supervision_attachments WHERE file_key=$1)
		OR EXISTS (SELECT 1 FROM supervision_site_attachments WHERE file_key=$1)`, key).
		Scan(&exists)
	if err == nil && !exists {
		err = s.files.RemoveUnregisteredAttachment(key)
	}
	if err != nil {
		slog.Warn("监督检查附件清理待核对", "error_type", fmt.Sprintf("%T", err))
	}
}

// download 按任务、材料及历史收件范围发送文件，包括已核验的批次模板引用。
// 参数 w、r、id、owner 提供响应、附件请求、任务与运营者可选范围。
// 返回值 成功时为空，否则为附件不可见、记录缺失或存储错误。
func (s *Service) download(w http.ResponseWriter, r *http.Request, id, owner string) error {
	var item cases.Attachment
	var key string
	districtScope := " AND " + access.FilingDistrictScope("t.filing_id", "$4", false)
	err := s.db.QueryRow(r.Context(), `SELECT `+materialColumns+`,a.file_key
		FROM supervision_attachments a JOIN supervision_tasks t ON t.id=a.task_id
		WHERE t.id=$1 AND `+readableScope+districtScope+` AND a.id=$3
		 AND CASE WHEN a.kind IN ('inspection_template','summary_template')
		   THEN ($2='' OR a.task_version=(SELECT max(latest.task_version) FROM supervision_attachments latest
		          WHERE latest.task_id=a.task_id AND latest.kind=a.kind))
		   WHEN `+originalTaskOwner+` THEN a.task_version<=t.merge_history_version
		   WHEN $2<>'' THEN a.task_version>COALESCE(t.merge_history_version,0)
		   ELSE EXISTS (SELECT 1 FROM supervision_events e WHERE e.task_id=a.task_id
		         AND a.id IN (e.inspection_file_id,e.summary_file_id))
		 END
		UNION ALL
		SELECT `+materialColumns+`,a.file_key FROM supervision_dispatched_templates st
		JOIN supervision_attachments a ON a.id=st.source_attachment_id
		JOIN supervision_tasks t ON t.id=st.task_id
		WHERE t.id=$1 AND `+readableScope+districtScope+` AND a.id=$3 LIMIT 1`,
		id, owner, r.PathValue("attachmentId"), access.Districts(r.Context())).
		Scan(append(materialTargets(&item), &key)...)
	if err != nil {
		return err
	}
	return s.files.ServeBusinessAttachment(w, r, item, key)
}
