package cases

import (
	"context"
	"encoding/csv"
	"io"
	"net/http"
	"time"

	"grade-business/internal/httpx"
)

// downloadReports 导出命中测评列表条件的全部报告摘要。
// 参数 w 为下载响应，r 携带已应用的状态、关键词和风险条件。
// 返回值 无，成功时发送 CSV 附件。
func (s *Service) downloadReports(w http.ResponseWriter, r *http.Request) {
	s.downloadReportExport(w, r, false)
}

// downloadInstitutionReports 按本机构的指定筛选下载测评台账。
// 参数 w、r 为响应与含机构范围的请求。
// 返回值 无，成功时发送机构专属 CSV。
func (s *Service) downloadInstitutionReports(w http.ResponseWriter, r *http.Request) {
	s.downloadReportExport(w, r, true)
}

// downloadReportExport 复用同一套台账筛选与输出，在机构路径强制核对归属。
// 参数 w、r 为响应与下载请求，scoped 表示是否必须明确机构。
// 返回值 无，筛选不符时发出已定义的业务错误。
func (s *Service) downloadReportExport(w http.ResponseWriter, r *http.Request, scoped bool) {
	values, institution, err := listRequestValues(r.URL.RawQuery, scoped)
	if err != nil || values.Has("cursor") || values.Has("limit") || values.Has("lifecycle") {
		respondError(w, ErrInvalid)
		return
	}
	input, err := parseListQuery(values, validStatus)
	if err != nil {
		respondError(w, err)
		return
	}
	input.institution = institution
	err = httpx.DownloadCSV(w, r, "测评报告台账.csv", func(ctx context.Context, dst io.Writer) error {
		return s.writeReportExport(ctx, dst, input)
	})
	if err != nil {
		respondError(w, err)
	}
}

// writeReportExport 按列表列名和顺序生成完整的测评报告台账。
// 参数 ctx 为请求上下文，dst 为文件写入端，input 为严格校验后的筛选条件。
// 返回值 查询、编码或文件错误，完整生成时为 nil。
func (s *Service) writeReportExport(ctx context.Context, dst io.Writer, input listQuery) error {
	const columns = "c.number, f.system_name, c.assessment_org, c.submitted_at, c.risk, c.status"
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return err
	}
	rows, err := s.db.Query(ctx, "SELECT "+columns+recordJoin+recordFilters+
		" AND ($6='' OR c.assessment_org=$6) ORDER BY c.created_at DESC,c.id DESC",
		report, input.status, input.keyword, "", input.risk, input.institution)
	if err != nil {
		return err
	}
	defer rows.Close()
	if _, err := io.WriteString(dst, "\ufeff"); err != nil {
		return err
	}
	writer := csv.NewWriter(dst)
	if err := writer.Write([]string{"报告编号", "系统名称", "测评机构", "提交时间", "风险级别", "审核状态"}); err != nil {
		return err
	}
	for rows.Next() {
		var row recordRow
		if err := rows.Scan(&row.Number, &row.SystemName, &row.AssessmentOrg,
			&row.SubmittedAt, &row.Risk, &row.Status); err != nil {
			return err
		}
		if err := writer.Write(reportCSVRow(row, location)); err != nil {
			return err
		}
	}
	writer.Flush()
	if err := rows.Err(); err != nil {
		return err
	}
	return writer.Error()
}

// reportCSVRow 映射报告摘要的六个有效列。
// 参数 row 为数据库报告字段，location 为上海业务时区。
// 返回值 编码前的安全 CSV 单元格。
func reportCSVRow(row recordRow, location *time.Location) []string {
	statuses := map[string]string{
		"draft": "草稿", "pending_initial": "待初审", "pending_final": "待终审",
		"approved": "已通过", "returned": "退回补正",
	}
	risks := map[string]string{"low": "低风险", "medium": "中风险", "high": "高风险"}
	stamp := ""
	if row.SubmittedAt != nil {
		stamp = row.SubmittedAt.In(location).Format("2006-01-02 15:04:05")
	}
	return []string{httpx.CSVText(row.Number), httpx.CSVText(row.SystemName), httpx.CSVText(row.AssessmentOrg),
		stamp, risks[row.Risk], statuses[row.Status]}
}
