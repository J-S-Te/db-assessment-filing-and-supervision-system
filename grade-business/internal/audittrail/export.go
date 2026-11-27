package audittrail

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"grade-business/internal/httpx"
)

const auditExportMaxRows = 10000

var errAuditExportLimit = errors.New("audit export row limit exceeded")

var auditResourceNames = map[string]string{
	"filings": "备案", "filing-applications": "备案变更与注销", "implementations": "实施报备",
	"reports": "测评报告", "remediations": "整改规划", "hazards": "隐患台账", "alerts": "站内预警",
	"publications": "信息发布", "workday-calendar": "工作日历",
}

// handleExport 按监管端已应用的筛选条件下载操作日志。
// 参数 w 为响应写入端，r 为包含业务类型和关键词的下载请求。
// 返回值 无，超限或生成失败时返回业务错误。
func (s *Service) handleExport(w http.ResponseWriter, r *http.Request) {
	filters, err := parseFilter(r.URL.RawQuery, false)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_input", "请核对业务类型和关键词。")
		return
	}
	filters.limit = auditExportMaxRows
	err = httpx.DownloadCSV(w, r, "业务操作日志.csv", func(ctx context.Context, dst io.Writer) error {
		return s.writeExport(ctx, dst, filters)
	})
	if errors.Is(err, errAuditExportLimit) {
		message := fmt.Sprintf("导出结果超过 %d 条，请缩小筛选范围后重试。", auditExportMaxRows)
		httpx.Error(w, http.StatusUnprocessableEntity, "export_limit_exceeded", message)
		return
	}
	if err != nil {
		slog.Error("业务操作审计导出失败", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "暂时无法导出操作日志，请稍后重试。")
	}
}

// writeExport 使用与列表一致的查询和动作名称，生成可供电子表格打开的日志。
// 参数 ctx 为下载请求上下文，dst 为文件写入端，filters 为已校验的业务筛选。
// 返回值 查询、编码或写入错误，记录超限时返回导出上限错误。
func (s *Service) writeExport(ctx context.Context, dst io.Writer, filters auditFilter) error {
	result, err := s.list(ctx, filters)
	if err != nil {
		return err
	}
	if result.NextCursor != "" {
		return errAuditExportLimit
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return err
	}
	if _, err := io.WriteString(dst, "\ufeff"); err != nil {
		return err
	}
	writer := csv.NewWriter(dst)
	if err := writer.Write([]string{"操作人", "操作", "业务类型", "业务对象", "对象编号", "发生时间"}); err != nil {
		return err
	}
	for _, item := range result.Items {
		if err := writer.Write(auditCSVRow(item, location)); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

// auditCSVRow 映射审计摘要并保护用户输入的文本单元格。
// 参数 item 为审计事件摘要，location 为监管工作台使用的时区。
// 返回值 按表头排列的 CSV 单元格。
func auditCSVRow(item auditItem, location *time.Location) []string {
	identifier := item.Reference
	if identifier == "" {
		identifier = item.ResourceID
	}
	return []string{
		httpx.CSVText(item.Operator), httpx.CSVText(item.ActionLabel), auditResourceNames[item.Resource],
		httpx.CSVText(item.Title), httpx.CSVText(identifier),
		item.OccurredAt.In(location).Format("2006-01-02 15:04:05"),
	}
}
