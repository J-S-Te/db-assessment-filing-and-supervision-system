package remediations

import (
	"bytes"
	"context"
	_ "embed"
	"mime"
	"net/http"
	"time"
)

const (
	docxContentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	xlsxContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
)

//go:embed templates/plan.docx
var planTemplate []byte

//go:embed templates/responsibility.xlsx
var responsibilityTemplate []byte

//go:embed templates/schedule.xlsx
var scheduleTemplate []byte

var materialTemplates = map[string]struct {
	name        string
	contentType string
	data        []byte
}{
	"plan":           {"整改规划方案模板.docx", docxContentType, planTemplate},
	"responsibility": {"整改责任分工表模板.xlsx", xlsxContentType, responsibilityTemplate},
	"schedule":       {"整改实施计划表模板.xlsx", xlsxContentType, scheduleTemplate},
}

const activeTemplateReport = `r.status='approved' AND f.status='approved' AND f.cancelled_at IS NULL`

// templateScope 核对模板所在规划或新建关联报告的当前备案范围。
// 参数 ctx 含企业端可选范围，id 为规划或报告标识，byReport 表示从新建时选择的报告下载。
// 返回值 可下载时为 nil，否则返回不可见或读取错误。
func (s *Service) templateScope(ctx context.Context, id string, byReport bool) error {
	source := joins + ` WHERE p.id=$1 AND ($2='' OR COALESCE(p.frozen_organization_name,f.organization_name)=$2)`
	if byReport {
		source = `FROM business_cases r JOIN business_cases f ON f.id=r.filing_id AND f.kind='filing'
			WHERE r.id=$1 AND r.kind='report'
			AND ($2='' OR (f.organization_name=$2 AND ` + newestReport + `))`
	}
	var allowed bool
	err := s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 "+source+" AND "+activeTemplateReport+")",
		id, operatorOrganization(ctx)).Scan(&allowed)
	if err == nil && !allowed {
		return errNotFound
	}
	return err
}

// handleMaterialTemplate 在校验当前业务范围后发送对应类别的空白填报文件。
// 参数 byReport 为是否使用新建时选择的报告范围。
// 返回值 写出对应类别的下载响应，或公开错误的 HTTP 处理器。
func (s *Service) handleMaterialTemplate(byReport bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		template, exists := materialTemplates[r.PathValue("kind")]
		if !exists {
			respondError(w, errNotFound)
			return
		}
		if err := s.templateScope(r.Context(), r.PathValue("id"), byReport); err != nil {
			respondError(w, err)
			return
		}
		w.Header().Set("Content-Type", template.contentType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment",
			map[string]string{"filename": template.name}))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, template.name, time.Time{}, bytes.NewReader(template.data))
	}
}
