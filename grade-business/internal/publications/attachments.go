package publications

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"grade-business/internal/cases"
)

const (
	maxAttachments    = 8
	maxAttachmentSize = 20 << 20
	maxAttachmentSum  = 40 << 20
)

var (
	errAttachmentLimit    = errors.New("publication attachment limit exceeded")
	errStorageUnavailable = errors.New("publication attachment store unavailable")
)

// AttachmentStore 提供公文附件的受控暂存、下载及未登记文件清理能力。
type AttachmentStore interface {
	StagePublicationAttachment(string, io.Reader) (cases.Attachment, string, string, error)
	ValidatePublicationPDF(context.Context, string) error
	ServeBusinessAttachment(http.ResponseWriter, *http.Request, cases.Attachment, string) error
	RemoveUnregisteredAttachment(string) error
}

type documentAttachment struct {
	ID         string    `json:"id"`
	FileName   string    `json:"fileName"`
	Size       int64     `json:"size"`
	UploadedAt time.Time `json:"uploadedAt"`
}

type stagedDocument struct {
	fileName string
	size     int64
	key      string
	digest   string
}

// attachmentSnapshot 为一条不可变公告版本投影附件的最小公开字段。
// 参数 versionKey 为内部声明的公告标识和版本 SQL 表达式。
// 返回值 不包含磁盘索引和摘要的 JSON 数组查询。
func attachmentSnapshot(versionKey string) string {
	return `(SELECT jsonb_agg(jsonb_build_object(
		'id',a.id,'fileName',a.file_name,'size',a.size_bytes,'uploadedAt',
		to_char(a.uploaded_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'))
		ORDER BY m.position)
	FROM publication_version_attachments m JOIN publication_attachments a
	ON (a.publication_id,a.id)=(m.publication_id,m.attachment_id)
	WHERE (m.publication_id,m.version)=(` + versionKey + `))`
}

// saveAttachmentManifest 把有序的附件引用固定在本次写入的公告版本下。
// 参数 ctx 与 tx 控制当前写入，item 为已经写入事件的公告及其附件。
// 返回值 关联写入错误，或没有附件时直接成功。
func saveAttachmentManifest(ctx context.Context, tx pgx.Tx, item detail) error {
	if len(item.Attachments) == 0 {
		return nil
	}
	ids := make([]string, len(item.Attachments))
	for i, file := range item.Attachments {
		ids[i] = file.ID
	}
	_, err := tx.Exec(ctx, `INSERT INTO publication_version_attachments
		(publication_id,version,attachment_id,position)
	SELECT $1,$2,files.id::uuid,files.position::smallint
	FROM unnest($3::text[]) WITH ORDINALITY AS files(id,position)`, item.ID, item.Version, ids)
	return err
}

// addAttachments 在版本锁内原子登记一批附件并保留当前的公开快照。
// 参数 ctx 控制请求，id 与 expected 为公告标识及预期版本，files 为已验证文件。
// 返回值 更新后的公告、重复批次对应的原公告，或写入冲突。
func (s *Service) addAttachments(ctx context.Context, id pgtype.UUID, expected int64,
	files []stagedDocument,
) (detail, error) {
	return s.reviseAttachments(ctx, id, expected, func(tx pgx.Tx, item *detail) (bool, error) {
		return appendDocumentFiles(ctx, tx, id, item, files)
	})
}

// appendDocumentFiles 去重并把已校验的文件加入本次公告草稿。
// 参数 ctx、tx 控制当前写入，id 与 item 代表公告，files 为暂存文件。
// 返回值 是否有实际变化，以及额度或写入错误。
func appendDocumentFiles(ctx context.Context, tx pgx.Tx, id pgtype.UUID, item *detail,
	files []stagedDocument,
) (bool, error) {
	count := len(item.Attachments)
	for _, file := range files {
		meta, err := registerDocument(ctx, tx, id, file)
		if err != nil {
			return false, err
		}
		if findAttachment(item.Attachments, meta.ID) >= 0 {
			continue
		}
		item.Attachments = append(item.Attachments, meta)
	}
	if err := checkAttachmentLimits(item.Attachments); err != nil {
		return false, err
	}
	return len(item.Attachments) != count, nil
}

// checkAttachmentLimits 控制版本持有的附属材料数量和总体积。
// 参数 files 为最终候选附件清单。
// 返回值 超出额度时返回业务错误，其他情况返回 nil。
func checkAttachmentLimits(files []documentAttachment) error {
	var total int64
	for _, file := range files {
		total += file.Size
	}
	if len(files) > maxAttachments || total > maxAttachmentSum {
		return errAttachmentLimit
	}
	return nil
}

// findAttachment 查找附件是否属于当前版本，用于移除和重复检测。
// 参数 files 为版本附件清单，id 为附件的业务标识。
// 返回值 匹配索引，或未找到时的 -1。
func findAttachment(files []documentAttachment, id string) int {
	return slices.IndexFunc(files, func(item documentAttachment) bool { return item.ID == id })
}

// registerDocument 为同公告同文件名及同内容复用同一不可变文件。
// 参数 ctx、tx 为持锁事务，id 为公告标识，file 为已验证并落盘的材料。
// 返回值 实际文件的最小元数据，或存储错误。
func registerDocument(ctx context.Context, tx pgx.Tx, id pgtype.UUID,
	file stagedDocument,
) (documentAttachment, error) {
	var out documentAttachment
	err := tx.QueryRow(ctx, `WITH inserted AS (
		INSERT INTO publication_attachments(publication_id,file_name,file_key,digest,size_bytes)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT (publication_id,file_name,digest) DO NOTHING
		RETURNING id,file_name,size_bytes,uploaded_at
	)
	SELECT id::text,file_name,size_bytes,uploaded_at FROM inserted
	UNION ALL
	SELECT id::text,file_name,size_bytes,uploaded_at FROM publication_attachments
	WHERE publication_id=$1 AND file_name=$2 AND digest=$4`,
		id, file.fileName, file.key, file.digest, file.size).
		Scan(&out.ID, &out.FileName, &out.Size, &out.UploadedAt)
	out.UploadedAt = out.UploadedAt.UTC()
	return out, err
}

// removeAttachment 从当前草稿移除引用，已经投递或历史版本的文件继续保留。
// 参数 ctx、id、expected、attachmentID 为请求、公告、预期版本和附件标识。
// 返回值 移除后公告，或版本冲突及附件不存在错误。
func (s *Service) removeAttachment(ctx context.Context, id pgtype.UUID, expected int64,
	attachmentID string,
) (detail, error) {
	return s.reviseAttachments(ctx, id, expected, func(_ pgx.Tx, item *detail) (bool, error) {
		position := findAttachment(item.Attachments, attachmentID)
		if position < 0 {
			return false, errMissing
		}
		item.Attachments = slices.Delete(item.Attachments, position, position+1)
		return true, nil
	})
}

// reviseAttachments 锁定公告并按实际变化写入新的附件版本。
// 参数 ctx、id、expected 为请求、公告及预期版本，edit 为受控修改动作。
// 返回值 同事务得到的公告详情，或版本与更新错误。
func (s *Service) reviseAttachments(ctx context.Context, id pgtype.UUID, expected int64,
	edit func(pgx.Tx, *detail) (bool, error),
) (detail, error) {
	var out detail
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := lockVersion(ctx, tx, id, expected); err != nil {
			return err
		}
		current, err := scanDetail(tx.QueryRow(ctx, "SELECT "+detailFields+visibleJoin+" WHERE p.id=$4",
			false, "", "", id))
		if err != nil {
			return err
		}
		out = current
		changed, err := edit(tx, &out)
		if err != nil || !changed {
			return err
		}
		if err := updateState(ctx, tx, id, "edit", &out); err != nil {
			return err
		}
		return s.saveRevision(ctx, tx, out, "edit")
	})
	out.AllowedActions = available(out)
	return out, err
}

// readAttachmentFile 按公告、确切版本和收件凭据查找文件，无权访问时视为不存在。
// 参数 ctx、id、version、attachmentID 为下载范围，scope 在门户端包含角色和规范收件名。
// 返回值 用于已有存储下载器的元数据、内部键和查询错误。
func (s *Service) readAttachmentFile(ctx context.Context, id pgtype.UUID, version int64,
	attachmentID pgtype.UUID, scope recipient,
) (cases.Attachment, string, error) {
	var file cases.Attachment
	var key string
	err := s.db.QueryRow(ctx, `SELECT a.file_name,a.uploaded_at,a.file_key
	FROM publication_version_attachments m JOIN publication_attachments a
	ON (a.publication_id,a.id)=(m.publication_id,m.attachment_id)
	JOIN local_publications p ON p.id=m.publication_id
	WHERE m.publication_id=$1 AND m.version=$2 AND m.attachment_id=$3
		AND ($4='' OR (p.published_version=m.version AND EXISTS (
			SELECT 1 FROM publication_deliveries d WHERE
			(d.publication_id,d.version,d.audience,d.recipient)=(m.publication_id,m.version,$4,$5))))`,
		id, version, attachmentID, scope.Audience, scope.Recipient).
		Scan(&file.FileName, &file.UploadedAt, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		err = errMissing
	}
	return file, key, err
}

// cleanupDocuments 清理未获数据库引用的暂存文件，提交结果不明时先检查数据库。
// 参数 ctx 为原请求上下文，files 为此次请求生成的暂存文件。
// 返回值 无，存储或查询错误仅记录必要类型。
func (s *Service) cleanupDocuments(ctx context.Context, files []stagedDocument) {
	if len(files) == 0 {
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	keys := make([]string, len(files))
	for i, file := range files {
		keys[i] = file.key
	}
	var referenced []string
	err := s.db.QueryRow(cleanup, `SELECT coalesce(array_agg(file_key),'{}'::text[])
	    FROM publication_attachments WHERE file_key=ANY($1)`, keys).Scan(&referenced)
	if err != nil {
		slog.Warn("公文附件暂存清理失败", "error_type", fmt.Sprintf("%T", err))
		return
	}
	for _, key := range keys {
		if !slices.Contains(referenced, key) {
			if err := s.store.RemoveUnregisteredAttachment(key); err != nil {
				slog.Warn("公文附件文件清理失败", "error_type", fmt.Sprintf("%T", err))
			}
		}
	}
}
