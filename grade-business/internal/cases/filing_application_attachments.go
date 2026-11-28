package cases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/access"
)

const applicationAttachmentScope = `a.id=$1 AND ($2='' OR a.before_values->>'assessmentOrg'=$2)
	AND ($3='' OR a.before_values->>'organizationName'=$3)`

// adminApplicationMaterials 查询监管端或已核验运营者的材料元数据。
// 参数 w 和 r 为响应及所属申请的请求。
// 返回值 无。
func (s *Service) adminApplicationMaterials(w http.ResponseWriter, r *http.Request) {
	s.applicationMaterialsHTTP(w, r, "")
}

// adminApplicationDownload 下载监管端或已核验运营者可读的申请材料。
// 参数 w 和 r 为附件响应及路径请求。
// 返回值 无。
func (s *Service) adminApplicationDownload(w http.ResponseWriter, r *http.Request) {
	s.applicationDownloadHTTP(w, r, "")
}

// applicationMaterialsHTTP 返回当前持久化备案申请真实拥有的文件列表。
// 参数 w、r 为响应及选择请求，institution 为可选原机构范围。
// 返回值 无。
func (s *Service) applicationMaterialsHTTP(w http.ResponseWriter, r *http.Request, institution string) {
	id := r.PathValue("id")
	ctx := r.Context()
	if _, err := s.applicationInScope(ctx, id, institution); err != nil {
		respondError(w, err)
		return
	}
	rows, err := s.db.Query(ctx, `SELECT f.id::text,f.kind,f.file_name,f.size_bytes,f.uploaded_at
		FROM filing_application_attachments f JOIN filing_applications a ON a.id=f.application_id
		WHERE `+applicationAttachmentScope+` AND `+access.FilingDistrictScope("a.filing_id", "$4", false)+`
		ORDER BY f.uploaded_at DESC,f.id DESC`, id, institution, applicationOrganization(ctx), access.Districts(ctx))
	if err != nil {
		respondError(w, err)
		return
	}
	files, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Attachment])
	respond(w, http.StatusOK, itemResponse[Attachment]{Items: files}, err)
}

// applicationDownloadHTTP 按申请、附件和办理范围的联合归属发送已校验文件。
// 参数 w、r 为附件响应及路径请求，institution 为可选原机构范围。
// 返回值 无。
func (s *Service) applicationDownloadHTTP(w http.ResponseWriter, r *http.Request, institution string) {
	id, file := r.PathValue("id"), r.PathValue("attachmentId")
	if !validID(id) || !validID(file) {
		respondError(w, ErrNotFound)
		return
	}
	var item Attachment
	var key string
	err := s.db.QueryRow(r.Context(), `SELECT f.id::text,f.kind,f.file_name,f.size_bytes,f.uploaded_at,f.file_key
		FROM filing_application_attachments f JOIN filing_applications a ON a.id=f.application_id
		WHERE `+applicationAttachmentScope+` AND f.id=$4
		AND `+access.FilingDistrictScope("a.filing_id", "$5", false),
		id, institution, applicationOrganization(r.Context()), file, access.Districts(r.Context())).
		Scan(&item.ID, &item.Kind, &item.FileName, &item.Size, &item.UploadedAt, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	if err == nil {
		err = s.ServeBusinessAttachment(w, r, item, key)
	}
	if err != nil {
		respondError(w, err)
	}
}

// uploadApplicationMaterial 保存运营者草稿或补正申请的受控说明附件。
// 参数 w 为响应，r 为已核验运营单位的 multipart 文件请求。
// 返回值 无。
func (s *Service) uploadApplicationMaterial(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	app, err := s.application(r.Context(), id)
	if err == nil && !editable(app.Status) {
		err = ErrConflict
	}
	if err != nil {
		respondError(w, err)
		return
	}
	item, key, err := s.StageBusinessAttachment(w, r, func(kind string) bool {
		return kind == requiredApplicationMaterial(app.Type) || kind == "supporting"
	})
	if err != nil {
		respondError(w, err)
		return
	}
	result, err := s.storeApplicationMaterial(r.Context(), id, item, key)
	if err != nil {
		if cleanup := s.discardApplicationMaterial(r.Context(), key); cleanup != nil {
			slog.Warn("未登记的备案申请材料待清理", "error_type", fmt.Sprintf("%T", cleanup))
		}
	}
	respond(w, http.StatusCreated, result, err)
}

// storeApplicationMaterial 在主备案与申请的行锁下重验归属及写入操作。
// 参数 ctx 为带运营单位的请求上下文，id、item 和 key 为申请及已暂存材料。
// 返回值 登记材料或状态、范围和写入错误。
func (s *Service) storeApplicationMaterial(ctx context.Context, id string, item Attachment, key string) (Attachment, error) {
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		app, err := lockApplication(ctx, tx, id, 0, "")
		if err != nil {
			return err
		}
		if !editable(app.status) {
			return ErrConflict
		}
		if item.Kind != requiredApplicationMaterial(app.kind) && item.Kind != "supporting" {
			return ErrInvalid
		}
		err = tx.QueryRow(ctx, `INSERT INTO filing_application_attachments
			(application_id,kind,file_name,file_key,size_bytes) VALUES($1,$2,$3,$4,$5)
			RETURNING id::text,uploaded_at`, id, item.Kind, item.FileName, key, item.Size).
			Scan(&item.ID, &item.UploadedAt)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE filing_applications SET version=version+1 WHERE id=$1`, id)
		return err
	})
	return item, err
}

// discardApplicationMaterial 仅清理已确认未登记的本次上传文件。
// 参数 ctx 为原请求上下文，key 为本次上传唯一生成的文件键。
// 返回值 清理、查询错误；数据库已登记时为 nil。
func (s *Service) discardApplicationMaterial(ctx context.Context, key string) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	var registered bool
	err := s.db.QueryRow(cleanup, `SELECT EXISTS(SELECT 1 FROM filing_application_attachments WHERE file_key=$1)`, key).
		Scan(&registered)
	if err != nil || registered {
		return err
	}
	return s.RemoveUnregisteredAttachment(key)
}
