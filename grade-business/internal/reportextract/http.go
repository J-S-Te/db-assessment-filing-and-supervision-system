package reportextract

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"grade-business/internal/cases"
	"grade-business/internal/httpx"
)

// Register 挂载按测评报告和实际材料定位的提取接口。
// 参数 mux 为业务路由。
// 返回值 无。
func (s *Service) Register(mux *http.ServeMux) {
	const path = "/api/v1/reports/{id}/attachments/{attachmentId}/extraction"
	mux.HandleFunc("GET "+path, s.handle)
	mux.HandleFunc("POST "+path, s.handle)
	mux.HandleFunc("PATCH "+path, s.handle)
}

// handle 校验路由和请求内容，再执行提取创建、查看或人工校对。
// 参数 w 为响应对象，r 为携带报告和附件标识的请求。
// 返回值 无。
func (s *Service) handle(w http.ResponseWriter, r *http.Request) {
	id, attachment := r.PathValue("id"), r.PathValue("attachmentId")
	if !validUUID(id) || !validUUID(attachment) {
		respondError(w, cases.ErrNotFound)
		return
	}
	if r.URL.RawQuery != "" {
		respondError(w, errInput)
		return
	}
	out, status, err := s.process(w, r, id, attachment)
	if err != nil {
		respondError(w, err)
		return
	}
	httpx.JSON(w, status, out)
}

// process 按 HTTP 方法处理指定报告附件的提取数据。
// 参数 w 为响应对象，r 为请求，id 和 attachment 为已校验的业务标识。
// 返回值 提取结果、HTTP 状态及处理错误。
func (s *Service) process(w http.ResponseWriter, r *http.Request, id, attachment string) (result, int, error) {
	switch r.Method {
	case http.MethodPost:
		if r.ContentLength != 0 {
			return result{}, 0, errInput
		}
		out, created, err := s.start(r.Context(), id, attachment)
		if created {
			return out, http.StatusCreated, err
		}
		return out, http.StatusOK, err
	case http.MethodPatch:
		var input confirmation
		if err := httpx.Decode(w, r, &input); err != nil {
			return result{}, 0, errInput
		}
		out, err := s.confirm(r.Context(), id, attachment, input)
		return out, http.StatusOK, err
	default:
		out, err := s.read(r.Context(), id, attachment)
		return out, http.StatusOK, err
	}
}

// validUUID 判断报告和附件路由能否安全地用于数据库查询。
// 参数 value 为客户端传入的路由标识。
// 返回值 是否为规范长度的 UUID。
func validUUID(value string) bool {
	var id pgtype.UUID
	return len(value) == 36 && id.Scan(value) == nil && id.Valid && strings.EqualFold(value, id.String())
}

// respondError 返回可展示的错误摘要，内部错误只记录类型。
// 参数 w 为响应对象，err 为提取操作的错误。
// 返回值 无。
func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errMissing):
		httpx.Error(w, 404, "extraction_not_found", "该附件暂无提取结果")
	case errors.Is(err, cases.ErrNotFound), errors.Is(err, os.ErrNotExist):
		httpx.Error(w, 404, "not_found", "报告或材料不存在")
	case errors.Is(err, cases.ErrReportFormat):
		httpx.Error(w, 415, "unsupported_report", "请选择 DOCX 或 PDF 格式的测评报告正文")
	case errors.Is(err, errPDFUnavailable):
		httpx.Error(w, 503, "pdf_extraction_unavailable", "当前服务缺少 PDF 文本提取工具，请联系管理员")
	case errors.Is(err, errOCRUnavailable):
		httpx.Error(w, 503, "ocr_unavailable", "当前服务缺少扫描件识别工具或中英文语言包，请联系管理员")
	case errors.Is(err, errPDFBusy):
		httpx.Error(w, 503, "report_extraction_busy", "PDF 提取正在处理中，请稍后重试")
	case errors.Is(err, errPDFTimeout):
		httpx.Error(w, 422, "report_extraction_timeout", "PDF 提取已超时，请缩减正文页数或上传可复制文字的 PDF")
	case errors.Is(err, errNoFields):
		httpx.Error(w, 422, "no_report_fields", "未识别到资产清单、漏洞详情、风险等级或整改建议")
	case errors.Is(err, errDocument):
		httpx.Error(w, 422, "invalid_report", "报告正文格式异常或超过解析限制")
	case errors.Is(err, errInput):
		httpx.Error(w, 400, "invalid_input", "请检查校对内容和版本")
	case errors.Is(err, errConflict):
		httpx.Error(w, 409, "version_conflict", "提取结果已更新，请重新加载再校对")
	default:
		slog.Error("测评报告提取失败", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, 500, "internal_error", "处理提取结果失败，请稍后重试")
	}
}
