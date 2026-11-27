package publications

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// validatePublishedMaterial 核对本次发布快照具备材料，并验证其中的 PDF 可以解析。
// 参数 ctx 和 tx 为公告发布上下文和已持锁事务，item 为页面将公开的实际版本。
// 返回值 所有材料合格时为 nil，或材料缺失、解析及存储错误。
func (s *Service) validatePublishedMaterial(ctx context.Context, tx pgx.Tx, item detail) error {
	if len(item.Attachments) == 0 {
		return errMaterialAttachmentRequired
	}
	if s.store == nil {
		return errStorageUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	rows, err := tx.Query(ctx, `SELECT a.file_key FROM publication_version_attachments m
		JOIN publication_attachments a ON (a.publication_id,a.id)=(m.publication_id,m.attachment_id)
		WHERE m.publication_id=$1 AND m.version=$2 AND right(lower(a.file_name),4)='.pdf'
		ORDER BY m.position`, item.ID, item.Version)
	if err != nil {
		return err
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, key := range keys {
		if err := s.store.ValidatePublicationPDF(ctx, key); err != nil {
			return err
		}
	}
	return nil
}
