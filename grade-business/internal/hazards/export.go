package hazards

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"grade-business/internal/httpx"
)

const hazardExportMaxRows = 10000

var errHazardExportLimit = errors.New("hazard export row limit exceeded")

var exportLedgerNames = map[string]string{
	ledgerVulnerability: "系统漏洞台账",
	ledgerMajorRisk:     "重大风险台账",
}

// handleExport 以附件下载监管端筛选命中的系统漏洞或重大风险台账。
// 参数 w 为响应对象，r 携带已应用的台账筛选。
// 返回值 无。
func (s *Service) handleExport(w http.ResponseWriter, r *http.Request) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || values.Has("cursor") || values.Has("limit") {
		respondError(w, errInvalid)
		return
	}
	filter, err := decodeFilter(values)
	if err != nil {
		respondError(w, err)
		return
	}
	filename := "隐患台账.csv"
	if filter.ledger != "" {
		filename = exportLedgerNames[filter.ledger] + ".csv"
	}
	err = httpx.DownloadCSV(w, r, filename, func(ctx context.Context, dst io.Writer) error {
		return s.writeExport(ctx, dst, filter)
	})
	if errors.Is(err, errHazardExportLimit) {
		message := fmt.Sprintf("导出结果超过 %d 条，请缩小筛选范围后重试。", hazardExportMaxRows)
		httpx.Error(w, http.StatusUnprocessableEntity, "export_limit_exceeded", message)
		return
	}
	if err != nil {
		respondError(w, err)
	}
}

// writeExport 按列表顺序写入筛选结果，命中超限探测行时放弃临时文件。
// 参数 ctx 为请求上下文，dst 为临时文件，filter 为已校验的筛选条件。
// 返回值 查询、编码、写入或超限错误，完整写入时为 nil。
func (s *Service) writeExport(ctx context.Context, dst io.Writer, filter hazardFilter) error {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return err
	}
	rows, err := queryHazards(ctx, s.db, filter, hazardExportMaxRows+1)
	if err != nil {
		return err
	}
	defer rows.Close()
	if _, err := io.WriteString(dst, "\ufeff"); err != nil {
		return err
	}
	writer := csv.NewWriter(dst)
	if err := writer.Write([]string{
		"系统名称", "运营单位", "台账类型", "问题标题", "风险等级", "问题来源",
		"发现日期", "登记日期", "派单日期", "整改期限",
		"办理状态", "责任人或单位", "备案编号", "问题描述",
	}); err != nil {
		return err
	}
	for count := 0; rows.Next(); count++ {
		if count == hazardExportMaxRows {
			return errHazardExportLimit
		}
		var item hazardSummary
		if err := rows.Scan(scanSummary(&item)...); err != nil {
			return err
		}
		if err := writer.Write(exportRow(item, location)); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writer.Flush()
	return writer.Error()
}

// exportRow 映射持久化台账字段和上海业务日期，并保护文本单元格。
// 参数 item 为台账摘要，location 为上海业务时区。
// 返回值 与导出表头顺序一致的 CSV 单元格。
func exportRow(item hazardSummary, location *time.Location) []string {
	severity := map[string]string{"low": "低风险", "medium": "中风险", "high": "高风险"}
	sources := map[string]string{
		"": "整改规划", "manual": "人工录入", "assessment": "测评发现", "scan": "漏扫对接",
	}
	statuses := map[string]string{
		statusDispatch: "待派单", statusOpen: "待整改", statusRetest: "待机构复测",
		statusPending: "待监管复核", statusReturned: "退回补正", statusClosed: "已销项",
	}
	title := item.Title
	if title == "" {
		title = item.Description
	}
	dispatchedOn := ""
	if item.DispatchedAt != nil {
		dispatchedOn = item.DispatchedAt.In(location).Format(time.DateOnly)
	}
	return []string{
		httpx.CSVText(item.SystemName), httpx.CSVText(item.OrganizationName), exportLedgerNames[item.Ledger],
		httpx.CSVText(title), severity[item.Severity], sources[item.Source], item.DiscoveredOn,
		item.CreatedAt.In(location).Format(time.DateOnly), dispatchedOn, item.Deadline,
		statuses[item.Status], httpx.CSVText(item.Responsibility),
		httpx.CSVText(item.FilingNumber), httpx.CSVText(item.Description),
	}
}
