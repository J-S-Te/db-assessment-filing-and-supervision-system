package hazards

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/httpx"
)

type workbenchHazard struct {
	ID          string `json:"id"`
	SystemName  string `json:"systemName"`
	Description string `json:"description"`
	Deadline    string `json:"deadline"`
	Status      string `json:"status"`
}

type workbenchPlan struct {
	ID           string `json:"id"`
	SystemName   string `json:"systemName"`
	ReportNumber string `json:"reportNumber"`
	Deadline     string `json:"deadline"`
}

// WorkbenchSummary 是工作台公开的整改统计及有界待办。
type WorkbenchSummary struct {
	PendingPlanReview int64             `json:"pendingPlanReview"`
	PendingReview     int64             `json:"pendingReview"`
	DueWithin10Days   int64             `json:"dueWithin10Days"`
	Overdue           int64             `json:"overdue"`
	Items             []workbenchHazard `json:"items"`
	PendingPlans      []workbenchPlan   `json:"pendingPlans"`
}

type workbenchReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// RegisterWorkbench 注册工作台使用的整改统计与待办查询。
// 参数 mux 为业务 HTTP 路由。
// 返回值 无。
func (s *Service) RegisterWorkbench(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/hazards/workbench", s.handleWorkbench)
}

// handleWorkbench 返回工作台用于展示和跳转的统计及摘要。
// 参数 w 为响应对象，r 为无查询参数的工作台请求。
// 返回值 无。
func (s *Service) handleWorkbench(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_input", "整改待办无需查询参数")
		return
	}
	result, err := ReadWorkbench(r.Context(), s.db)
	if err != nil {
		slog.Error("整改待办读取失败", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "暂时无法读取整改待办")
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

const workbenchQuery = `
WITH business_day AS (SELECT (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date AS today),
counters AS (
    SELECT count(*) FILTER (WHERE h.status='pending_review') AS pending,
           count(*) FILTER (WHERE h.deadline BETWEEN d.today AND d.today+10) AS due,
           count(*) FILTER (WHERE h.deadline<d.today) AS overdue
    FROM hazard_items h CROSS JOIN business_day d WHERE h.status<>'closed'
),
pending_hazards AS (
    SELECT h.id, f.system_name, h.description, h.deadline, h.status,
           CASE WHEN h.status='pending_review' THEN 0 ELSE 1 END AS priority
    FROM hazard_items h
    JOIN remediation_plans p ON p.id=h.plan_id
    JOIN business_cases r ON r.id=p.report_id
    JOIN business_cases f ON f.id=r.filing_id
    CROSS JOIN business_day d
    WHERE h.status<>'closed' AND (h.status='pending_review' OR h.deadline<=d.today+10)
    ORDER BY priority,h.deadline,h.id LIMIT 5
),
pending_plans AS (
    SELECT p.id,f.system_name,r.number AS report_number,p.deadline
    FROM remediation_plans p
    JOIN business_cases r ON r.id=p.report_id
    JOIN business_cases f ON f.id=r.filing_id
    WHERE p.status='pending_review'
    ORDER BY p.deadline,p.id LIMIT 5
)
SELECT (SELECT count(*) FROM remediation_plans WHERE status='pending_review'),
       counters.pending,counters.due,counters.overdue,
       COALESCE((SELECT jsonb_agg(jsonb_build_object(
           'id',id,'systemName',system_name,'description',description,
           'deadline',to_char(deadline,'YYYY-MM-DD'),'status',status
       ) ORDER BY priority,deadline,id) FROM pending_hazards),'[]'::jsonb),
       COALESCE((SELECT jsonb_agg(jsonb_build_object(
           'id',id,'systemName',system_name,'reportNumber',report_number,
           'deadline',to_char(deadline,'YYYY-MM-DD')
       ) ORDER BY deadline,id) FROM pending_plans),'[]'::jsonb)
FROM counters
`

// ReadWorkbench 读取整改计数和有限的待办列表。
// 参数 ctx 为请求上下文，db 为独立连接池或调用者持有的读取事务。
// 返回值 工作台计数及摘要，或读取失败。
func ReadWorkbench(ctx context.Context, db workbenchReader) (WorkbenchSummary, error) {
	var value WorkbenchSummary
	err := db.QueryRow(ctx, workbenchQuery).Scan(
		&value.PendingPlanReview, &value.PendingReview, &value.DueWithin10Days,
		&value.Overdue, &value.Items, &value.PendingPlans,
	)
	return value, err
}
