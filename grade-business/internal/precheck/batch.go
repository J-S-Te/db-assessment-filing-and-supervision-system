package precheck

import (
	"context"
	"time"

	"grade-business/internal/filingaudit"
)

// assessContents 使用独立批次模型并区分进度写入失败与模型回退。
// 参数 ctx、docs、materials、unit、progress 为任务、已解析材料、材料快照、名录及进度写入函数。
// 返回值 内容结论、模型错误、进度写入错误。
func (s *Service) assessContents(ctx context.Context, docs []filingaudit.Document,
	materials []Material, unit filingaudit.Unit, progress filingaudit.BatchProgress,
) ([]filingaudit.Result, error, error) {
	model, ok := s.assessor.(batchAssessor)
	if !ok {
		if err := progress(filingaudit.ReviewingMaterials, nil, 0); err != nil {
			return nil, nil, err
		}
		modelContext, cancel := context.WithTimeout(ctx, s.assessor.Timeout())
		defer cancel()
		checks, err := s.assessor.Assess(modelContext, docs, unit)
		return checks, err, nil
	}
	docs = includeUnreadableMaterials(docs, materials)
	duration := min(20*time.Minute, s.assessor.Timeout()*time.Duration(len(docs)+1))
	modelContext, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	var progressErr error
	result, err := model.AssessBatch(modelContext, docs, unit,
		func(stage string, reviews []filingaudit.MaterialReview, total int) error {
			progressErr = progress(stage, reviews, total)
			return progressErr
		})
	return result.Checks, err, progressErr
}

// includeUnreadableMaterials 为因格式、体积或读取失败而缺失的附件保留处理记录。
// 参数 docs、materials 分别为解析结果和本次备案的原始附件快照。
// 返回值 含未能读取附件的批次输入。
func includeUnreadableMaterials(docs []filingaudit.Document, materials []Material) []filingaudit.Document {
	seen := make(map[string]bool, len(docs))
	for _, doc := range docs {
		seen[doc.AttachmentID] = true
	}
	for _, item := range materials {
		if !seen[item.ID] {
			docs = append(docs, filingaudit.Document{
				AttachmentID: item.ID, FileName: item.FileName, Kind: item.Kind,
			})
		}
	}
	return docs
}
