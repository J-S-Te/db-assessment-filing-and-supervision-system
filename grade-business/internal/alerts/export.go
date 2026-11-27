package alerts

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"grade-business/internal/httpx"
)

const exportLimit = 10000

var errExportLimit = errors.New("alert export row limit exceeded")

// handleOperatorExport 按关闭时间导出当前运营单位的预警处置留痕。
// 参数 w 为下载响应，r 包含日期区间和经路由校验的单位。
// 返回值 无。
func (s *Service) handleOperatorExport(w http.ResponseWriter, r *http.Request) {
	start, end, err := parseExportDates(r.URL.RawQuery)
	if err != nil {
		respond(w, nil, err)
		return
	}
	err = httpx.DownloadCSV(w, r, "预警处置台账.csv", func(ctx context.Context, dest io.Writer) error {
		return s.writeOperatorExport(ctx, dest, start, end)
	})
	if errors.Is(err, errExportLimit) {
		httpx.Error(w, 422, "export_limit_exceeded", "导出超过一万条，请缩小日期范围。")
	} else if err != nil {
		respond(w, nil, err)
	}
}

// parseExportDates 校验闭区间内的上海日期及请求边界。
// 参数 raw 为剥离单位范围的 URL 查询字符串。
// 返回值 包含首尾的起止日期，或者无效日期及区间错误。
func parseExportDates(raw string) (string, string, error) {
	values, err := url.ParseQuery(raw)
	if err != nil || len(values) != 2 || len(values["from"]) != 1 || len(values["to"]) != 1 {
		return "", "", errInvalid
	}
	start, err := time.Parse(time.DateOnly, values.Get("from"))
	end, endErr := time.Parse(time.DateOnly, values.Get("to"))
	if err != nil || endErr != nil || start.Year() < 1 || end.Before(start) ||
		end.Sub(start) >= 366*24*time.Hour {
		return "", "", errInvalid
	}
	return values.Get("from"), values.Get("to"), nil
}

// writeOperatorExport 读取本单位已关闭的实际预警及已审核的最近申请。
// 参数 ctx 包含业务范围，dest 为临时 CSV，start、end 为闭区间日期。
// 返回值 查询、编码或数量限制错误。
func (s *Service) writeOperatorExport(ctx context.Context, dest io.Writer, start, end string) error {
	scope := currentAlertScope(ctx)
	rows, err := s.db.Query(ctx, `
		SELECT f.number,f.system_name,a.kind,a.severity,
		       to_char(a.triggered_at AT TIME ZONE 'Asia/Shanghai','YYYY-MM-DD HH24:MI:SS'),
		       to_char(a.closed_at AT TIME ZONE 'Asia/Shanghai','YYYY-MM-DD HH24:MI:SS'),
		       COALESCE(d.action,'')
		FROM local_alerts a`+alertSourceJoins+`
		LEFT JOIN LATERAL (
			SELECT action FROM local_alert_dispositions
			WHERE alert_id=a.id AND status='approved' ORDER BY requested_version DESC LIMIT 1
		) d ON true
		WHERE hc.organization_name COLLATE "C"=$1 AND a.status='closed'
		  AND a.closed_at>=$2::text::date::timestamp AT TIME ZONE 'Asia/Shanghai'
		  AND a.closed_at<($3::text::date+1)::timestamp AT TIME ZONE 'Asia/Shanghai'
		ORDER BY a.closed_at DESC,a.id DESC LIMIT $4`, scope.recipient, start, end, exportLimit+1)
	if err != nil {
		return err
	}
	defer rows.Close()
	if _, err := io.WriteString(dest, "\ufeff"); err != nil {
		return err
	}
	csvWriter := csv.NewWriter(dest)
	if err := csvWriter.Write([]string{"备案编号", "系统名称", "预警类型", "风险等级", "触发时间", "关闭时间", "处置结果"}); err != nil {
		return err
	}
	for count := 0; rows.Next(); count++ {
		if count == exportLimit {
			return errExportLimit
		}
		var row [7]string
		if err := rows.Scan(&row[0], &row[1], &row[2], &row[3], &row[4], &row[5], &row[6]); err != nil {
			return err
		}
		if err := csvWriter.Write(displayExportRow(row)); err != nil {
			return err
		}
	}
	csvWriter.Flush()
	if err := rows.Err(); err != nil {
		return err
	}
	return csvWriter.Error()
}

// displayExportRow 为到账处置结果提供简明中文，并保护用户可填写单元格。
// 参数 row 为数据库按导出顺序返回的七列。
// 返回值 Excel 和普通 CSV 阅读器都可安全展示的内容。
func displayExportRow(row [7]string) []string {
	row[0], row[1] = httpx.CSVText(row[0]), httpx.CSVText(row[1])
	row[2] = map[string]string{"high_risk": "高风险", "overdue": "逾期"}[row[2]]
	row[3] = map[string]string{"high": "高风险", "medium": "中风险", "low": "低风险"}[row[3]]
	switch row[6] {
	case actionFalseAlarm:
		row[6] = "误报关闭"
	case actionFeedback:
		row[6] = "机构反馈已核准"
	default:
		row[6] = "已关闭"
	}
	return row[:]
}
