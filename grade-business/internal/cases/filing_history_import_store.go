package cases

import (
	"archive/zip"
	"context"
	"errors"
	"log/slog"
	"os"
	"path"
	"strings"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/directoryref"
)

type existingHistoryFiling struct {
	id, unit, system string
}

// classifyHistoryImport 定位既有备案号并去重，统一名录仍可用的稳定归属。
// 参数 ctx、tx 为数据库读取事务，items 为已校验 ZIP 的候选记录。
// 返回值 无写入的逐行分类结果或查询错误。
func classifyHistoryImport(ctx context.Context, tx pgx.Tx, items []historyImportCandidate) error {
	seen := make(map[string]existingHistoryFiling)
	var materialBytes uint64
	for i := range items {
		item := &items[i]
		if item.Status != historyImportReady {
			continue
		}
		if err := resolveHistoryImportParty(ctx, tx, item); err != nil {
			return err
		}
		if item.Status != historyImportReady {
			continue
		}
		number := strings.ToLower(item.fields.number)
		first, found := seen[number]
		if found {
			item.followsPending = first.id == ""
			compareHistoryTriple(item, first)
			continue
		}
		existing, err := findHistoryFiling(ctx, tx, item.fields.number)
		if errors.Is(err, pgx.ErrNoRows) {
			size := item.form.UncompressedSize64 + item.classification.UncompressedSize64
			if size > maxPackageSize-materialBytes {
				item.Status = historyImportInvalid
				item.Reason = "待入档材料累计超过 200 MiB，请拆成多个 ZIP 后重试。"
				continue
			}
			materialBytes += size
			seen[number] = existingHistoryFiling{unit: item.fields.organizationName, system: item.fields.systemName}
			continue
		}
		if err != nil {
			return err
		}
		compareHistoryTriple(item, existing)
		seen[number] = existing
	}
	return nil
}

// resolveHistoryImportParty 核对已有名录条目的可用性，保存稳定单位和机构范围。
// 参数 ctx、tx 为数据库事务，item 为准备匹配的清单条目。
// 返回值 可报告的名录冲突通过状态表达，其余查询错误返回给上层。
func resolveHistoryImportParty(ctx context.Context, tx pgx.Tx, item *historyImportCandidate) error {
	fields := &item.fields
	party, err := directoryref.Resolve(ctx, tx, fields.organizationName, fields.assessmentOrg, "", false)
	if errors.Is(err, directoryref.ErrUnavailable) {
		item.Status, item.Reason = historyImportConflict, "单位或机构名录已停用、资质已过期。"
		return nil
	}
	if err != nil || party == nil {
		return err
	}
	if party.Unit != nil {
		fields.organizationName = party.Unit.ScopeName
	}
	if party.Institution != nil {
		fields.assessmentOrg = party.Institution.ScopeName
	}
	item.snapshot = party
	return nil
}

// findHistoryFiling 查询当前目录同一备案号对应的归属及业务标识。
// 参数 ctx、tx 为候选检查所在事务，number 为真实备案编号。
// 返回值 已存在的当前备案，或 pgx.ErrNoRows 和查询错误。
func findHistoryFiling(ctx context.Context, tx pgx.Tx, number string) (existingHistoryFiling, error) {
	var result existingHistoryFiling
	err := tx.QueryRow(ctx, `SELECT id::text,organization_name,system_name FROM business_cases
		WHERE kind='filing' AND lower(number)=lower($1)
		  AND discarded_at IS NULL AND status<>'withdrawn'`, number).
		Scan(&result.id, &result.unit, &result.system)
	return result, err
}

// compareHistoryTriple 根据相同备案号下的归属和系统名判定跳过或冲突。
// 参数 item 为当前清单行，prior 为同批或已有备案的实际范围。
// 返回值 无。
func compareHistoryTriple(item *historyImportCandidate, prior existingHistoryFiling) {
	if strings.EqualFold(item.fields.organizationName, prior.unit) &&
		strings.EqualFold(item.fields.systemName, prior.system) {
		item.Status, item.Reason, item.FilingID = historyImportDuplicate, "相同备案已存在或已在本批登记。", prior.id
		return
	}
	item.Status, item.Reason, item.FilingID = historyImportConflict, "备案编号已对应其他单位或系统。", ""
}

// previewHistoryImport 对本次上传重新查询真实名录和既有备案并生成逐行结果。
// 参数 ctx 为请求上下文，items 为 ZIP 预检后的候选条目。
// 返回值 只读预检结果或数据库错误。
func (s *Service) previewHistoryImport(ctx context.Context, items []historyImportCandidate) (historyImportResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return historyImportResult{}, err
	}
	defer tx.Rollback(ctx)
	err = classifyHistoryImport(ctx, tx, items)
	return summarizeHistoryImport(items), err
}

// commitHistoryImport 执行一个内容幂等的受控导入事务。
// 参数 ctx 为请求上下文，digest 为 ZIP SHA-256，items 为本次包内已经验证的候选记录。
// 返回值 新批次或既有相同批次的结果，以及存储错误。
func (s *Service) commitHistoryImport(ctx context.Context, digest string, items []historyImportCandidate) (historyImportResult, error) {
	var result historyImportResult
	staged := make([]string, 0)
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(574831092)"); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, "SELECT result FROM filing_history_import_batches WHERE digest=$1", digest).Scan(&result)
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		// 归并先锁备案表再更新名录。导入保持同一顺序，避免双方互相等待。
		if _, err := tx.Exec(ctx, "LOCK TABLE business_cases IN ROW EXCLUSIVE MODE"); err != nil {
			return err
		}
		if err := s.persistHistoryRows(ctx, tx, items, &staged); err != nil {
			return err
		}
		result = summarizeHistoryImport(items)
		return s.createHistoryImportAudit(ctx, tx, digest, &result)
	})
	if err != nil {
		s.cleanHistoryImportStaging(ctx, staged)
	}
	return result, constraintError(err)
}

// persistHistoryRows 复核编号及材料总量，然后登记实际草稿和两项附件。
// 参数 ctx、tx 为保存事务，items 为待写入的候选记录，staged 跟踪新文件以便失败恢复。
// 返回值 存储错误或 nil。
func (s *Service) persistHistoryRows(ctx context.Context, tx pgx.Tx, items []historyImportCandidate, staged *[]string) error {
	if err := classifyHistoryImport(ctx, tx, items); err != nil {
		return err
	}
	committed := make(map[string]existingHistoryFiling)
	for i := range items {
		item := &items[i]
		if item.followsPending {
			compareHistoryTriple(item, committed[strings.ToLower(item.fields.number)])
			continue
		}
		if item.Status != historyImportReady {
			continue
		}
		filing, err := s.insertHistoryFiling(ctx, tx, item, staged)
		if err != nil {
			return err
		}
		committed[strings.ToLower(item.fields.number)] = filing
	}
	return nil
}

// insertHistoryFiling 用编号唯一索引保护并发导入，向真实草稿登记两项必须材料。
// 参数 ctx、tx 为同批事务，item 为当前记录，staged 用于跟踪文件回滚。
// 返回值 确定的备案归属，或由调用方回滚的写入错误。
func (s *Service) insertHistoryFiling(ctx context.Context, tx pgx.Tx, item *historyImportCandidate,
	staged *[]string,
) (existingHistoryFiling, error) {
	fields := item.fields
	unitID, institutionID, _ := item.snapshot.References()
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO business_cases (
		kind,number,system_name,organization_name,assessment_org,level,version,material_revision,
		directory_unit_id,directory_institution_id,directory_snapshot)
		VALUES ('filing',$1,$2,$3,$4,$5,3,2,$6,$7,$8)
		ON CONFLICT (kind,lower(number)) WHERE discarded_at IS NULL AND status<>'withdrawn'
		    AND number<>''
		DO NOTHING RETURNING id::text`,
		fields.number, fields.systemName, fields.organizationName, fields.assessmentOrg, fields.level,
		unitID, institutionID, item.snapshot).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, lookupErr := findHistoryFiling(ctx, tx, fields.number)
		if lookupErr == nil {
			compareHistoryTriple(item, existing)
		}
		return existing, lookupErr
	}
	if err != nil {
		return existingHistoryFiling{}, err
	}
	if err := s.saveHistoryImportMaterial(ctx, tx, id, "filing_form", item.form, staged); err != nil {
		return existingHistoryFiling{}, err
	}
	if err := s.saveHistoryImportMaterial(ctx, tx, id, "classification_report", item.classification, staged); err != nil {
		return existingHistoryFiling{}, err
	}
	item.Status, item.FilingID = historyImportImported, id
	return existingHistoryFiling{id: id, unit: fields.organizationName, system: fields.systemName}, nil
}

// saveHistoryImportMaterial 复用受控存储登记归档成员，保证下载仍走材料归属校验。
// 参数 ctx、tx 为数据库事务，id、kind、file 为备案标识、材料分类和归档原件，staged 跟踪新存储键。
// 返回值 存储或资料写入错误。
func (s *Service) saveHistoryImportMaterial(ctx context.Context, tx pgx.Tx, id, kind string,
	file *zip.File, staged *[]string,
) error {
	reader, err := file.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	validator := validatePublicationAttachment
	if strings.EqualFold(path.Ext(file.Name), ".doc") {
		validator = validateLegacyFilingWord
	}
	material, err := stageFileWithValidation(s.attachmentDir, path.Base(file.Name), reader, validator)
	if err != nil {
		return err
	}
	*staged = append(*staged, material.fileKey)
	_, err = tx.Exec(ctx, `INSERT INTO case_attachments(case_id,kind,file_name,file_key,size_bytes)
		VALUES ($1,$2,$3,$4,$5)`, id, kind, material.FileName, material.fileKey, material.Size)
	return err
}

// cleanHistoryImportStaging 按实际数据库引用清理未提交文件，保护提交结果不明期间的材料。
// 参数 ctx 为原始上下文，staged 为当前调用已存储的内部键。
// 返回值 无，由服务端日志记录必要的清理失败。
func (s *Service) cleanHistoryImportStaging(ctx context.Context, staged []string) {
	for _, key := range staged {
		if err := s.discardUnreferenced(ctx, key); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("待清理的历史备案暂存材料")
		}
	}
}

// createHistoryImportAudit 与备案和材料同批次保存操作人、时间及逐条结果。
// 参数 ctx、tx 为当前事务，digest 为幂等键，result 为需要补上批次元数据的报告。
// 返回值 批次留痕的保存错误。
func (s *Service) createHistoryImportAudit(ctx context.Context, tx pgx.Tx, digest string, result *historyImportResult) error {
	result.Operator = s.operator
	err := tx.QueryRow(ctx, `INSERT INTO filing_history_import_batches(digest,operator,result)
		VALUES($1,$2,'{}') RETURNING id::text,created_at`, digest, s.operator).
		Scan(&result.BatchID, &result.ImportedAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "UPDATE filing_history_import_batches SET result=$2 WHERE id=$1", result.BatchID, result)
	return err
}
