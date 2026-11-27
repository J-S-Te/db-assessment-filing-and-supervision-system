package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/alerts"
	"grade-business/internal/hazards"
	"grade-business/internal/httpx"
)

type overview struct {
	FilingTotal            int64                    `json:"filingTotal"`
	FilingsThisYear        int64                    `json:"filingsThisYear"`
	ImplementationOpen     int64                    `json:"implementationOpen"`
	ImplementationInReview int64                    `json:"implementationInReview"`
	ReportInitial          int64                    `json:"reportInitial"`
	ReportFinal            int64                    `json:"reportFinal"`
	HazardTotal            int64                    `json:"hazardTotal"`
	HazardClosed           int64                    `json:"hazardClosed"`
	HazardDueWithin30Days  int64                    `json:"hazardDueWithin30Days"`
	Tasks                  []overviewTask           `json:"tasks"`
	Workflow               taskCounts               `json:"workflow"`
	Remediation            hazards.WorkbenchSummary `json:"remediation"`
	ReviewDeadlines        *reviewDeadlineCounts    `json:"reviewDeadlines,omitempty"`
	ActiveAlerts           int64                    `json:"activeAlerts"`
	PendingAlertReviews    int64                    `json:"pendingAlertReviews,omitempty"`
	OpenMaterialIssues     int64                    `json:"openMaterialIssues,omitempty"`
}

type overviewTask struct {
	ID         string `json:"id"`
	Resource   string `json:"resource"`
	Label      string `json:"label"`
	SystemName string `json:"systemName"`
	Number     string `json:"number"`
	Deadline   string `json:"deadline,omitempty"`
	Status     string `json:"status"`
	Urgency    string `json:"urgency,omitempty"`
	CanRemind  bool   `json:"canRemind,omitempty"`
}

// ReviewWindows 定义各类审核每个阶段允许的工作日数。
type ReviewWindows struct {
	FilingDays         int
	FilingFinalDays    int
	ReportDays         int
	ImplementationDays int
	RemediationDays    int
}

type reviewDeadlineCounts struct {
	Overdue  int64 `json:"overdue"`
	Near     int64 `json:"near"`
	Upcoming int64 `json:"upcoming"`
}

// RegisterWorkbenchOverview 注册 V2 工作台使用的业务计数与跨模块待办接口。
// 参数 mux 为同源 HTTP 路由，pool 为当前业务库，windows 为本地审核计时口径。
// 返回值 无。
func RegisterWorkbenchOverview(mux *http.ServeMux, pool *pgxpool.Pool, windows ReviewWindows) {
	registerScopedWorkbenches(mux, pool, windows)
	registerDeadlines(mux, pool, windows)
	mux.HandleFunc("GET /api/v1/workbench/overview", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			httpx.Error(w, http.StatusBadRequest, "invalid_input", "工作台总览无需查询参数")
			return
		}
		result, err := readOverview(r.Context(), pool, windows)
		if err != nil {
			slog.Error("工作台总览读取失败", "error_type", fmt.Sprintf("%T", err))
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "暂时无法读取工作台总览")
			return
		}
		httpx.JSON(w, http.StatusOK, result)
	})
}

const overviewSQL = `
WITH business_day AS (
	SELECT timezone('Asia/Shanghai',CURRENT_TIMESTAMP)::date AS today,
	       date_trunc('year',timezone('Asia/Shanghai',CURRENT_TIMESTAMP)) AT TIME ZONE 'Asia/Shanghai' AS year_start
), cases AS (
	SELECT count(*) FILTER (WHERE kind='filing' AND status<>'withdrawn' AND discarded_at IS NULL) AS filing_total,
	       count(*) FILTER (WHERE kind='filing' AND status<>'withdrawn' AND discarded_at IS NULL
	                       AND created_at>=d.year_start) AS filing_new,
	       count(*) FILTER (WHERE kind='report' AND status='pending_initial') AS report_initial,
	       count(*) FILTER (WHERE kind='report' AND status='pending_final') AS report_final
	FROM business_cases CROSS JOIN business_day d
), implementation AS (
	SELECT count(*) FILTER (WHERE status IN ('submitted','pending_initial','pending_final','pending_supervision','returned')) AS open,
	       count(*) FILTER (WHERE status='pending_supervision') AS for_supervision
	FROM implementation_reports
), hazards AS (
	SELECT count(*) AS total,
	       count(*) FILTER (WHERE status='closed') AS closed,
	       count(*) FILTER (WHERE status NOT IN ('pending_dispatch','closed')
	           AND deadline BETWEEN d.today AND d.today+30) AS due
	FROM hazard_items CROSS JOIN business_day d
)
SELECT c.filing_total,c.filing_new,i.open,i.for_supervision,c.report_initial,c.report_final,h.total,h.closed,h.due
FROM cases c CROSS JOIN implementation i CROSS JOIN hazards h
`

// readOverview 从一次数据库快照读取工作台数据，保证统计口径和待办队列一致。
// 参数 ctx 为业务请求上下文，pool 为业务库，windows 为配置的审核期限。
// 返回值 公开的计数和有界待办记录，或数据库错误。
func readOverview(ctx context.Context, pool *pgxpool.Pool, windows ReviewWindows) (overview, error) {
	var result overview
	var deadlines reviewDeadlineCounts
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	err := pgx.BeginTxFunc(ctx, pool, options, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, overviewSQL).
			Scan(&result.FilingTotal, &result.FilingsThisYear,
				&result.ImplementationOpen, &result.ImplementationInReview, &result.ReportInitial,
				&result.ReportFinal, &result.HazardTotal, &result.HazardClosed,
				&result.HazardDueWithin30Days); err != nil {
			return err
		}
		var err error
		result.Tasks, result.Workflow, deadlines, err = readQueue(ctx, tx, windows, queueScope{audience: "regulator"})
		if err != nil {
			return err
		}
		remediation, err := hazards.ReadWorkbench(ctx, tx)
		result.Remediation = remediation
		if err != nil {
			return err
		}
		summary, err := alerts.ReadSummary(ctx, tx)
		result.ActiveAlerts = summary.Pending + summary.Processing
		result.PendingAlertReviews = summary.PendingDispositions
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, "SELECT count(*) FROM material_issues WHERE closed_at IS NULL").
			Scan(&result.OpenMaterialIssues)
	})
	if windows.FilingDays > 0 || windows.FilingFinalDays > 0 || windows.ReportDays > 0 ||
		windows.ImplementationDays > 0 || windows.RemediationDays > 0 {
		result.ReviewDeadlines = &deadlines
	}
	return result, err
}
