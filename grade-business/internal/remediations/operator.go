package remediations

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

const operatorRoot = "/api/v1/operator/remediations"
const operatorRoute = operatorRoot + "/{id}"

type organizationContextKey struct{}

// RegisterOperator 注册运营者编制、查询、提交、撤回、补正、材料、模板及回执接口。
// 参数 mux 为统一业务路由。
// 返回值 无。
func (s *Service) RegisterOperator(mux *http.ServeMux) {
	mux.HandleFunc("GET "+operatorRoot, withOrganizationFilters(s.handleList))
	mux.HandleFunc("POST "+operatorRoot, withOrganization(s.handlePlan))
	mux.HandleFunc("GET "+operatorRoot+"/eligible-reports", withOrganizationFilters(s.handleList))
	mux.HandleFunc("GET "+operatorRoot+"/eligible-reports/{id}/material-templates/{kind}",
		withID(withOrganization(s.handleMaterialTemplate(true))))
	mux.HandleFunc("GET "+operatorRoute, withID(withOrganization(s.handleOperatorDetail)))
	mux.HandleFunc("GET "+operatorRoute+"/receipt", withID(withOrganization(s.handleOperatorReceipt)))
	mux.HandleFunc("PATCH "+operatorRoute, withID(withOrganization(s.handlePlan)))
	mux.HandleFunc("POST "+operatorRoute+"/submit", withID(withOrganization(s.handleSubmit)))
	mux.HandleFunc("POST "+operatorRoute+"/withdraw", withID(withOrganization(s.handleWithdraw)))
	mux.HandleFunc("GET "+operatorRoute+"/review-history", withID(withOrganization(s.handleHistory)))
	mux.HandleFunc("GET "+operatorRoute+"/material-templates/{kind}",
		withID(withOrganization(s.handleMaterialTemplate(false))))
	mux.HandleFunc("GET "+operatorRoute+"/attachments", withID(withOrganization(s.handleAttachments)))
	mux.HandleFunc("POST "+operatorRoute+"/attachments", withID(withOrganization(s.handleUpload)))
	mux.HandleFunc("GET "+operatorRoute+"/attachments/{attachmentId}", withID(withOrganization(s.handleDownload)))
}

// withOrganizationFilters 核对运营单位后保留已有列表筛选的严格解析。
// 参数 next 为读取正文的已有查询处理器。
// 返回值 验证唯一单位查询参数并把其余查询交给列表筛选的处理器。
func withOrganizationFilters(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			respondError(w, errInvalid)
			return
		}
		scopeRequest := r.Clone(r.Context())
		scopeRequest.URL.RawQuery = url.Values{"organization": values["organization"]}.Encode()
		organization, err := reviewinbox.Scope(scopeRequest, "organization")
		if err != nil {
			respondError(w, errInvalid)
			return
		}
		delete(values, "organization")
		ctx := context.WithValue(r.Context(), organizationContextKey{}, organization)
		queryRequest := r.Clone(ctx)
		queryRequest.URL.RawQuery = values.Encode()
		next(w, queryRequest)
	}
}

// withOrganization 校验企业端查询范围并随请求传给数据读写。
// 参数 next 为需要按当前备案单位读取或修改整改方案的处理器。
// 返回值 要求唯一单位范围的 HTTP 处理器。
func withOrganization(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		organization, err := reviewinbox.Scope(r, "organization")
		if err != nil {
			respondError(w, errInvalid)
			return
		}
		ctx := context.WithValue(r.Context(), organizationContextKey{}, organization)
		next(w, r.WithContext(ctx))
	}
}

// operatorOrganization 读取企业端路由校验后的查询范围。
// 参数 ctx 为当前请求及事务上下文。
// 返回值 企业单位全称，监管端请求返回空字符串。
func operatorOrganization(ctx context.Context) string {
	organization, _ := ctx.Value(organizationContextKey{}).(string)
	return organization
}

// handleWithdraw 接收运营者主动撤回其名下待审核规划的版本请求。
// 参数 w 为响应对象，r 携带规划标识、当前单位及预期版本。
// 返回值 无。
func (s *Service) handleWithdraw(w http.ResponseWriter, r *http.Request) {
	var input submitInput
	if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
		respondError(w, errInvalid)
		return
	}
	out, err := s.withdraw(r.Context(), r.PathValue("id"), input.ExpectedVersion)
	respond(w, http.StatusOK, out, err)
}

type operatorReceiptEvent struct {
	filing, system, action string
	version, submission    *int64
	at                     *time.Time
}

// handleOperatorDetail 读取本单位规划，并附带当前实际可下载回执的标志。
// 参数 w 为响应，r 携带已验证的运营单位及规划标识。
// 返回值 无。
func (s *Service) handleOperatorDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := s.get(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	event, err := s.operatorReceipt(r.Context(), id)
	respond(w, http.StatusOK, struct {
		detail
		ReceiptAvailable bool `json:"receiptAvailable"`
	}{item, event.at != nil}, err)
}

// handleOperatorReceipt 下载来自真实提交或审核通过事件的整改规划回执。
// 参数 w 为附件响应，r 携带本地运营单位和规划标识。
// 返回值 无。
func (s *Service) handleOperatorReceipt(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	event, err := s.operatorReceipt(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	if event.at == nil || event.version == nil || event.submission == nil {
		httpx.Error(w, http.StatusConflict, "receipt_unavailable", "整改规划提交审核后可下载办理回执。")
		return
	}
	title := "整改规划提交回执"
	if event.action == "approve" {
		title = "整改规划审核通过回执"
	}
	body := fmt.Sprintf("%s\n规划编号：%s\n备案编号：%s\n系统名称：%s\n提交轮次：%d\n办理版本：%d\n记录时间：%s\n",
		title, id, strings.Join(strings.Fields(event.filing), " "), strings.Join(strings.Fields(event.system), " "),
		*event.submission, *event.version, event.at.Format(time.RFC3339))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=remediation-receipt.txt")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, body)
}

// operatorReceipt 在同一快照验证当前备案单位并查询最新提交或审核通过事件。
// 参数 ctx 含已验证运营单位，id 为整改规划标识。
// 返回值 当前单位可读取的回执依据；无适用事件时保留空事件，非本单位返回不存在。
func (s *Service) operatorReceipt(ctx context.Context, id string) (operatorReceiptEvent, error) {
	var result operatorReceiptEvent
	err := s.db.QueryRow(ctx, `SELECT `+planFilingNumber+`,`+planSystemName+`,COALESCE(event.action,''),
		event.version,event.submission,event.occurred_at `+joins+` LEFT JOIN LATERAL (
		SELECT e.action,e.version,e.submission,e.occurred_at FROM remediation_review_events e
		WHERE e.plan_id=p.id AND (e.action='submit' OR (e.action='approve' AND e.to_status='approved'))
		ORDER BY e.version DESC LIMIT 1) event ON true WHERE p.id=$1 AND COALESCE(p.frozen_organization_name,f.organization_name)=$2`,
		id, operatorOrganization(ctx)).Scan(&result.filing, &result.system, &result.action,
		&result.version, &result.submission, &result.at)
	if errors.Is(err, pgx.ErrNoRows) {
		return operatorReceiptEvent{}, errNotFound
	}
	return result, err
}
