package cases

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/httpx"
	"grade-business/internal/reviewinbox"
)

const operatorApplicationPath = "/api/v1/operator/filing-applications"
const applicationReceiptEvent = `(e.action='submit' OR (e.action='approve' AND e.to_status='approved'))`

type applicationOrganizationKey struct{}

// RegisterOperatorApplicationRoutes 挂载运营者备案变更、注销、材料、回执及进度接口。
// 参数 mux 为业务路由。
// 返回值 无。
func (s *Service) RegisterOperatorApplicationRoutes(mux *http.ServeMux) {
	base := operatorApplicationPath
	item := base + "/{id}"
	mux.HandleFunc("GET "+base, withApplicationOrganization(s.listApplicationsHTTP, true))
	mux.HandleFunc("POST "+base, withApplicationOrganization(s.createApplicationHTTP, false))
	mux.HandleFunc("GET "+item, withApplicationOrganization(s.applicationHTTP, false))
	mux.HandleFunc("PATCH "+item, withApplicationOrganization(s.updateApplicationHTTP, false))
	mux.HandleFunc("POST "+item+"/submit", withApplicationOrganization(s.submitApplicationHTTP, false))
	mux.HandleFunc("POST "+item+"/withdraw", withApplicationOrganization(s.operatorApplicationWithdraw, false))
	mux.HandleFunc("GET "+item+"/review-history", withApplicationOrganization(s.applicationHistoryHTTP, false))
	mux.HandleFunc("GET "+item+"/attachments", withApplicationOrganization(s.adminApplicationMaterials, false))
	mux.HandleFunc("POST "+item+"/attachments", withApplicationOrganization(s.uploadApplicationMaterial, false))
	mux.HandleFunc("GET "+item+"/attachments/{attachmentId}", withApplicationOrganization(s.adminApplicationDownload, false))
	mux.HandleFunc("GET "+item+"/receipt", withApplicationOrganization(s.downloadApplicationReceipt, false))
}

// withApplicationOrganization 确认唯一运营单位并为既有申请流程提供范围。
// 参数 next 为申请处理器，allowFilters 表示列表是否接受已有筛选字段。
// 返回值 校验范围后调用业务逻辑的处理器。
func withApplicationOrganization(next http.HandlerFunc, allowFilters bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		checked := r
		if allowFilters {
			values, err := url.ParseQuery(r.URL.RawQuery)
			if err != nil {
				respondError(w, ErrInvalid)
				return
			}
			checked = r.Clone(r.Context())
			checked.URL.RawQuery = url.Values{"organization": values["organization"]}.Encode()
			values.Del("organization")
			r = r.Clone(r.Context())
			r.URL.RawQuery = values.Encode()
		}
		organization, err := reviewinbox.Scope(checked, "organization")
		if err != nil {
			respondError(w, ErrInvalid)
			return
		}
		ctx := context.WithValue(r.Context(), applicationOrganizationKey{}, organization)
		next(w, r.WithContext(ctx))
	}
}

// applicationOrganization 返回已规范校验过的本地运营单位。
// 参数 ctx 为处理当前备案申请的请求上下文。
// 返回值 当前选择的运营单位；无运营者范围时为空。
func applicationOrganization(ctx context.Context) string {
	organization, _ := ctx.Value(applicationOrganizationKey{}).(string)
	return organization
}

// operatorApplicationWithdraw 复用申请撤回状态机及其留痕。
// 参数 w 为响应，r 携带已核验的申请、范围和预期版本。
// 返回值 无。
func (s *Service) operatorApplicationWithdraw(w http.ResponseWriter, r *http.Request) {
	s.withdrawScopedApplication(w, r, "")
}

// downloadApplicationReceipt 返回来自实际申请留痕的纯文本业务回执。
// 参数 w 为附件响应，r 为已校验运营单位范围的回执请求。
// 返回值 无。
func (s *Service) downloadApplicationReceipt(w http.ResponseWriter, r *http.Request) {
	text, err := s.applicationReceipt(r.Context(), r.PathValue("id"))
	if errors.Is(err, ErrConflict) {
		httpx.Error(w, http.StatusConflict, "receipt_unavailable", "申请送审后可下载办理回执。")
		return
	}
	if err != nil {
		respondError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=filing-application-receipt.txt")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, text)
}

// applicationReceipt 读取当前范围的申请及最近一条已保存回执事件。
// 参数 ctx 含已核验运营单位，id 为申请标识。
// 返回值 回执文字，或归属、状态及读取错误。
func (s *Service) applicationReceipt(ctx context.Context, id string) (string, error) {
	organization := applicationOrganization(ctx)
	if !validID(id) || organization == "" {
		return "", ErrNotFound
	}
	var number, systemName, kind, state string
	var occurred *time.Time
	err := s.db.QueryRow(ctx, `SELECT a.filing_number,a.before_values->>'systemName',a.application_type,
		COALESCE(receipt.to_status,''),receipt.occurred_at FROM filing_applications a LEFT JOIN LATERAL (
		SELECT e.to_status,e.occurred_at FROM filing_application_review_events e
		WHERE e.application_id=a.id AND `+applicationReceiptEvent+` ORDER BY e.id DESC LIMIT 1
		) receipt ON true WHERE a.id=$1 AND a.before_values->>'organizationName'=$2`, id, organization).
		Scan(&number, &systemName, &kind, &state, &occurred)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if occurred == nil {
		return "", ErrConflict
	}
	title := "备案变更"
	if kind == applicationCancel {
		title = "备案注销"
	}
	if state == approved {
		title += "审核通过回执"
	} else {
		title += "提交回执"
	}
	return fmt.Sprintf("%s\n申请编号：%s\n备案编号：%s\n系统名称：%s\n记录时间：%s\n", title, id,
		strings.Join(strings.Fields(number), " "), strings.Join(strings.Fields(systemName), " "),
		occurred.Format(time.RFC3339)), nil
}
