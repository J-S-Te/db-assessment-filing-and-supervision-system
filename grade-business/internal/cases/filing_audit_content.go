package cases

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"slices"
	"strings"

	"grade-business/internal/filingaudit"
	"grade-business/internal/filingpolicy"
)

const maxAuditDocuments, maxAuditInputBytes = 32, 80 << 20

// AuditText 约束备案材料内容读取函数，解析器仅访问本任务受控附件。
type AuditText func(context.Context, string, io.ReaderAt, int64) ([]filingaudit.Line, []filingaudit.Image, error)

// FilingAuditContent 加载预审快照中的附件及备案当下绑定的单位名录。
// 参数 ctx、id、materials 和 read 为任务上下文、备案、快照附件和实际正文读取函数。
// 返回值 可读取的材料及名录，或数据库和任务取消错误。
func (s *Service) FilingAuditContent(ctx context.Context, id string, materials []filingpolicy.Material,
	read AuditText,
) ([]filingaudit.Document, filingaudit.Unit, error) {
	unit, err := s.FilingAuditUnit(ctx, id)
	if err != nil {
		return nil, unit, err
	}
	ordered := slices.Clone(materials)
	slices.SortStableFunc(ordered, func(a, b filingpolicy.Material) int {
		return auditMaterialPriority(a) - auditMaterialPriority(b)
	})
	var result []filingaudit.Document
	var budget int64 = maxAuditInputBytes
	for _, material := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, unit, err
		}
		if len(result) >= maxAuditDocuments || budget <= 0 {
			break
		}
		item, err := s.readAttachment(ctx, filing, id, material.ID, "")
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, unit, err
		}
		docs := s.readAuditAttachment(ctx, item, &budget, maxAuditDocuments-len(result), read)
		result = append(result, docs...)
	}
	return result, unit, ctx.Err()
}

// FilingAuditUnit 用途 读取备案当前绑定的审核用单位名录。
// 参数 ctx 控制查询，id 为备案标识。
// 返回值 审核输入中的单位名称、信用代码、行政区划及查询错误。
func (s *Service) FilingAuditUnit(ctx context.Context, id string) (filingaudit.Unit, error) {
	var unit filingaudit.Unit
	err := s.db.QueryRow(ctx, `SELECT COALESCE(u.name,''),COALESCE(u.code,''),COALESCE(u.district,'')
		FROM business_cases c LEFT JOIN directory_parties u ON u.id=c.directory_unit_id AND u.kind='unit'
		WHERE c.id=$1 AND c.kind='filing' AND c.discarded_at IS NULL`, id).
		Scan(&unit.Name, &unit.Code, &unit.District)
	return unit, err
}

// auditMaterialPriority 优先读取基础材料与可识别的专家、摸底和证明附件。
// 参数 material 为快照中的材料元信息。
// 返回值 用于稳定排序的处理顺序。
func auditMaterialPriority(material filingpolicy.Material) int {
	switch auditMaterialKind(material.Kind, material.FileName) {
	case "filing_form", "classification_report", "filing_package":
		return 0
	case "expert_opinion", "data_survey", "filing_certificate", "authorization":
		return 1
	default:
		return 2
	}
}

// readAuditAttachment 按实际存储标识读取单件或以原 ZIP 附件为证据读取成员。
// 参数 ctx、item、budget、remaining、read 为上下文、附件及本任务的字节、件数和正文读法。
// 返回值 带归档内来源的可读取文档。
func (s *Service) readAuditAttachment(ctx context.Context, item attachmentRow,
	budget *int64, remaining int, read AuditText,
) []filingaudit.Document {
	file, err := openAttachment(s.attachmentDir, item.fileKey)
	if err != nil {
		return nil
	}
	defer file.Close()
	if item.Kind == "filing_package" && strings.EqualFold(path.Ext(item.FileName), ".zip") {
		return auditPackageContent(ctx, file, item.Attachment, budget, remaining, read)
	}
	if item.Size <= 0 || item.Size > *budget {
		return nil
	}
	*budget -= item.Size
	lines, images, err := read(ctx, item.FileName, file, item.Size)
	if err != nil {
		return nil
	}
	return []filingaudit.Document{{
		AttachmentID: item.ID, FileName: item.FileName,
		Kind: auditMaterialKind(item.Kind, item.FileName), Lines: lines, Images: images,
	}}
}

// auditPackageContent 复用安全归档规则读取实际备案包成员。
// 参数 ctx、file、item、budget、remaining、read 为任务、归档、元信息、边界及正文读法。
// 返回值 保留外层附件标识及成员名的正文集合。
func auditPackageContent(ctx context.Context, file *os.File, item Attachment,
	budget *int64, remaining int, read AuditText,
) []filingaudit.Document {
	archive, err := zip.NewReader(file, item.Size)
	if err != nil {
		return nil
	}
	members, err := filingPackageMembers(archive, maxPackageSize)
	if err != nil {
		return nil
	}
	required, err := requiredFilingPackageFiles(members)
	if err != nil {
		return nil
	}
	first := []*zip.File{required[filingPackageForm], required[filingPackageReport]}
	candidates := slices.Concat(first, members)
	var result []filingaudit.Document
	seen := make(map[string]bool)
	for _, member := range candidates {
		if ctx.Err() != nil {
			break
		}
		if seen[member.Name] || filingPackageMetadata(member.Name) {
			continue
		}
		if len(result) >= remaining {
			result = append(result, filingaudit.Document{AttachmentID: item.ID,
				FileName: item.FileName + " / 超出逐件读取数量限制的材料", Kind: item.Kind})
			break
		}
		seen[member.Name] = true
		result = append(result, readAuditMember(ctx, member, item, budget, read))
	}
	return result
}

// readAuditMember 限量读取可解析的单个归档成员。
// 参数 ctx、member、item、budget、read 为任务、成员、外层附件、余量与正文读法。
// 返回值 保留材料标识和可读取内容的文档，无法解析时仅返回标识。
func readAuditMember(ctx context.Context, member *zip.File, item Attachment, budget *int64,
	read AuditText,
) filingaudit.Document {
	doc := filingaudit.Document{AttachmentID: item.ID, FileName: item.FileName + " / " + member.Name,
		Kind: auditMaterialKind("", member.Name)}
	switch strings.ToLower(path.Ext(member.Name)) {
	case ".doc", ".docx", ".pdf", ".xlsx":
	default:
		return doc
	}
	size := member.UncompressedSize64
	if size == 0 || size > maxDocumentSize || size > uint64(*budget) {
		return doc
	}
	*budget -= int64(size)
	var content bytes.Buffer
	if err := readFilingPackageMember(member, &content); err != nil {
		return doc
	}
	lines, images, err := read(ctx, member.Name, bytes.NewReader(content.Bytes()), int64(content.Len()))
	if err == nil {
		doc.Lines, doc.Images = lines, images
	}
	return doc
}

// auditMaterialKind 依据已登记类别和常见正式材料名称组织同类证据。
// 参数 kind、name 为保存的材料类别和用于显示的原件名。
// 返回值 预审使用的材料类别。
func auditMaterialKind(kind, name string) string {
	switch kind {
	case "filing_form", "classification_report", "filing_package":
		return kind
	}
	switch filingPackageCategory(name) {
	case filingPackageForm:
		return "filing_form"
	case filingPackageReport:
		return "classification_report"
	}
	switch {
	case strings.Contains(name, "专家") && strings.Contains(name, "意见"):
		return "expert_opinion"
	case strings.Contains(name, "摸底") || strings.Contains(name, "承载数据调查"):
		return "data_survey"
	case strings.Contains(name, "备案证明") || strings.Contains(name, "备案凭证"):
		return "filing_certificate"
	case strings.Contains(name, "委托书"):
		return "authorization"
	default:
		return "supporting"
	}
}
