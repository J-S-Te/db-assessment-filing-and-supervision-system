package reportextract

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/cases"
)

var (
	errMissing  = errors.New("extraction not found")
	errConflict = errors.New("extraction version changed")
	errInput    = errors.New("invalid correction")
)

// Service 管理单个报告附件的本地提取结果和校对留痕。
type Service struct {
	db       *pgxpool.Pool
	cases    *cases.Service
	operator string
	pdfSlots chan struct{}
}

type result struct {
	Fields      []field    `json:"fields"`
	Version     int64      `json:"version"`
	ConfirmedAt *time.Time `json:"confirmedAt,omitempty"`
	ConfirmedBy *string    `json:"confirmedBy,omitempty"`
}

type correction struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type confirmation struct {
	Version int64        `json:"expectedVersion"`
	Changes []correction `json:"changes"`
}

// New 创建使用既有附件存储的报告提取服务。
// 参数 pool 为业务数据库，records 为报告材料服务，operator 为经办人标签。
// 返回值 可注册路由的提取服务。
func New(pool *pgxpool.Pool, records *cases.Service, operator string) *Service {
	return &Service{db: pool, cases: records, operator: operator, pdfSlots: make(chan struct{}, 2)}
}

const resultColumns = "fields, version, confirmed_at, confirmed_by"

const liveExtraction = `SELECT e.fields,e.version,e.confirmed_at,e.confirmed_by
	FROM report_extractions e JOIN case_attachments a ON a.id=e.attachment_id
	JOIN business_cases c ON c.id=a.case_id
	WHERE c.id=$1 AND a.id=$2 AND a.kind='report_file' AND c.kind='report' AND c.discarded_at IS NULL`

// readResult 将保存在数据库中的结构化字段与校对信息解码。
// 参数 row 为数据库查询返回的单行。
// 返回值 公开的提取数据，或不存在及读取错误。
func readResult(row pgx.Row) (result, error) {
	var out result
	err := row.Scan(&out.Fields, &out.Version, &out.ConfirmedAt, &out.ConfirmedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, errMissing
	}
	return out, err
}

// read 验证报告归属并读取保存的提取结果。
// 参数 ctx 为请求上下文，id 和 attachment 为报告及附件标识。
// 返回值 当前校对版本，或不存在及读取错误。
func (s *Service) read(ctx context.Context, id, attachment string) (result, error) {
	return readResult(s.db.QueryRow(ctx, liveExtraction, id, attachment))
}

// start 解析实际附件并将提取初稿只保存一次。
// 参数 ctx 为请求上下文，id 和 attachment 为报告与附件标识。
// 返回值 已持久化结果、是否首次创建以及业务错误。
func (s *Service) start(ctx context.Context, id, attachment string) (result, bool, error) {
	out, err := s.read(ctx, id, attachment)
	if !errors.Is(err, errMissing) {
		return out, false, err
	}
	file, source, err := s.cases.OpenReportSource(ctx, id, attachment)
	if err != nil {
		return result{}, false, err
	}
	defer file.Close()
	fields, err := s.extractSource(ctx, file, source)
	if err != nil {
		return result{}, false, err
	}
	out, err = readResult(s.db.QueryRow(ctx, `INSERT INTO report_extractions(attachment_id,fields)
		SELECT a.id,$3 FROM case_attachments a JOIN business_cases c ON c.id=a.case_id
		WHERE c.id=$1 AND a.id=$2 AND a.kind='report_file' AND c.kind='report' AND c.discarded_at IS NULL
		FOR SHARE OF c,a ON CONFLICT(attachment_id) DO NOTHING RETURNING `+resultColumns,
		id, attachment, fields))
	if errors.Is(err, errMissing) {
		out, err = s.read(ctx, id, attachment)
		return out, false, err
	}
	return out, err == nil, err
}

// extractSource 按正文格式启动解析，并限制 PDF 工具的同时执行数量。
// 参数 ctx 为当前请求上下文，file 为受控附件，source 为已验证的材料元数据。
// 返回值 报告结构化字段，或格式、提取服务不可用和资源限制错误。
func (s *Service) extractSource(ctx context.Context, file *os.File, source cases.Attachment) ([]field, error) {
	if !strings.EqualFold(filepath.Ext(source.FileName), ".pdf") {
		return extractDOCX(file, source.Size)
	}
	select {
	case s.pdfSlots <- struct{}{}:
		defer func() { <-s.pdfSlots }()
	default:
		return nil, errPDFBusy
	}
	return extractPDF(ctx, file, source.Size)
}

// confirm 校验预期版本后原子保存校对结果及变更记录。
// 参数 ctx 为请求上下文，id 和 attachment 为业务标识，input 为校对提交。
// 返回值 修改后的公开结果，或并发冲突、输入与事务错误。
func (s *Service) confirm(ctx context.Context, id, attachment string, input confirmation) (result, error) {
	if input.Version < 1 || input.Changes == nil || len(input.Changes) > maxFields {
		return result{}, errInput
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return result{}, err
	}
	defer tx.Rollback(ctx)
	locked, err := readResult(tx.QueryRow(ctx, liveExtraction+" FOR UPDATE OF e FOR SHARE OF c", id, attachment))
	if err != nil {
		return result{}, err
	}
	if locked.Version != input.Version || locked.ConfirmedAt != nil && len(input.Changes) == 0 {
		return result{}, errConflict
	}
	if err := applyChanges(locked.Fields, input.Changes); err != nil {
		return result{}, err
	}
	out, err := s.saveConfirmation(ctx, tx, attachment, locked.Fields, input.Changes)
	if err != nil {
		return result{}, err
	}
	return out, tx.Commit(ctx)
}

// saveConfirmation 在同一个事务中写入结构化数据和经办人审计记录。
// 参数 ctx 为请求上下文，tx 为锁定的事务，attachment 为材料标识，fields 及 changes 为最新值和本次变更。
// 返回值 最新公开数据，或数据库错误。
func (s *Service) saveConfirmation(ctx context.Context, tx pgx.Tx, attachment string,
	fields []field, changes []correction,
) (result, error) {
	out, err := readResult(tx.QueryRow(ctx, `
		UPDATE report_extractions SET fields=$2, version=version+1, confirmed_by=$3,
		confirmed_at=clock_timestamp() WHERE attachment_id=$1 RETURNING `+resultColumns,
		attachment, fields, s.operator))
	if err != nil {
		return result{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO report_extraction_reviews
		(attachment_id,version,changes,operator,confirmed_at) VALUES($1,$2,$3,$4,$5)`,
		attachment, out.Version, changes, s.operator, out.ConfirmedAt)
	return out, err
}

// applyChanges 仅更新提取字段的校对值，始终保留独立的原文。
// 参数 fields 为已提取字段，changes 为客户端提交的字段修正。
// 返回值 发现重复、未知或超出限制的字段时返回输入错误。
func applyChanges(fields []field, changes []correction) error {
	remaining := make(map[string]*field, len(fields))
	for i := range fields {
		remaining[fields[i].Key] = &fields[i]
	}
	for _, change := range changes {
		item := remaining[change.Key]
		if item == nil || !utf8.ValidString(change.Value) || strings.ContainsRune(change.Value, 0) ||
			utf8.RuneCountInString(change.Value) > maxValue {
			return errInput
		}
		delete(remaining, change.Key)
		value := strings.TrimSpace(change.Value)
		item.Value = &value
		if value == item.Original {
			item.Value = nil
		}
	}
	return nil
}
