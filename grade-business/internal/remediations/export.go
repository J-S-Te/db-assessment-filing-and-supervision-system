package remediations

import (
	"context"
	"encoding/csv"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"grade-business/internal/access"
	"grade-business/internal/httpx"
)

const exportFilename = "整改规划台账.csv"

// handleExport 以附件形式导出当前筛选命中的完整整改规划台账。
// 参数 w 为响应，r 携带列表实际应用的关键词、状态和期限。
// 返回值 无。
func (s *Service) handleExport(w http.ResponseWriter, r *http.Request) {
	input, err := exportFilters(r.URL.RawQuery)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_input", "请核对导出使用的关键词、审核状态和整改期限。")
		return
	}
	err = httpx.DownloadCSV(w, r, exportFilename, func(ctx context.Context, dst io.Writer) error {
		return s.writeExport(ctx, dst, input)
	})
	if err != nil {
		respondError(w, err)
	}
}

// exportFilters 复用列表筛选校验并去除只适用于分页的参数。
// 参数 raw 为原始 URL 查询。
// 返回值 有效的导出筛选，或查询参数错误。
func exportFilters(raw string) (listInput, error) {
	values, err := url.ParseQuery(raw)
	if err != nil || values.Has("cursor") || values.Has("limit") {
		return listInput{}, errInvalid
	}
	return parseList(values, false)
}

// writeExport 按和列表相同的条件及顺序编码完整台账到临时文件。
// 参数 ctx 为请求上下文，dst 为文件写入器，input 为校验后的筛选条件。
// 返回值 完整写入时为 nil，或查询、编码与文件错误。
func (s *Service) writeExport(ctx context.Context, dst io.Writer, input listInput) error {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return err
	}
	rows, err := s.db.Query(ctx, "SELECT "+projection+" "+filteredPlans+`
		AND `+access.FilingDistrictScope("f.id", "$4", false)+`
		ORDER BY p.created_at DESC,p.id DESC`, input.Query, input.Status, input.Due, access.Districts(ctx))
	if err != nil {
		return err
	}
	defer rows.Close()
	if _, err := io.WriteString(dst, "\ufeff"); err != nil {
		return err
	}
	writer := csv.NewWriter(dst)
	if err := writer.Write([]string{"备案编号", "系统名称", "运营单位", "提交版本", "提交时间",
		"高风险项", "中风险项", "低风险项", "整改期限", "审核状态"}); err != nil {
		return err
	}
	for rows.Next() {
		var item summary
		if err := rows.Scan(scanFields(&item)...); err != nil {
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

// exportRow 将列表中的单条记录映射为管理端台账列。
// 参数 item 为持久化规划摘要，location 为提交时间使用的上海时区。
// 返回值 按导出表头顺序排列的安全文本单元格。
func exportRow(item summary, location *time.Location) []string {
	statuses := map[string]string{
		draft: "草稿", pendingReview: "待审核", returned: "退回补正", approved: "已通过", withdrawn: "已撤回",
	}
	version, submittedAt := "未提交", ""
	if item.Submission > 0 {
		version = "V" + strconv.Itoa(item.Submission) + ".0"
	}
	if item.SubmittedAt != nil {
		submittedAt = item.SubmittedAt.In(location).Format("2006-01-02 15:04:05")
	}
	return []string{httpx.CSVText(item.FilingNumber), httpx.CSVText(item.SystemName), httpx.CSVText(item.OrganizationName),
		version, submittedAt, strconv.Itoa(item.RiskCounts.High), strconv.Itoa(item.RiskCounts.Medium),
		strconv.Itoa(item.RiskCounts.Low), item.Deadline, statuses[item.Status]}
}
