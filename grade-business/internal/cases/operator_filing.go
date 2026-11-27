package cases

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/classification"
	"grade-business/internal/httpx"
	"grade-business/internal/reviewinbox"
)

type operatorFilingScopeKey struct{}

type operatorFilingInput struct {
	Number         string                `json:"number"` // 兼容旧客户端，运营者提交的编号不入库。
	SystemName     string                `json:"systemName"`
	AssessmentOrg  string                `json:"assessmentOrg"`
	Level          string                `json:"level"`
	Classification *classification.Input `json:"classificationAssessment,omitempty"`
}

const filingReceiptEvent = "(e.action='submit' OR e.to_status='approved')"

// RegisterOperatorFilingRoutes 挂载运营者自主备案及办理进度接口。
// 参数 mux 为管理端共用 HTTP 路由。
// 返回值 无。
func (s *Service) RegisterOperatorFilingRoutes(mux *http.ServeMux) {
	const collection = "/api/v1/operator/filings"
	const detail = collection + "/{id}"
	h := endpoint{service: s, kind: filing}
	mux.HandleFunc("GET "+collection, h.operatorFilingList)
	mux.HandleFunc("POST "+collection, withOperatorFiling(h.createOperatorFiling))
	mux.HandleFunc("GET "+detail, withOperatorFiling(h.operatorFilingDetail))
	mux.HandleFunc("PATCH "+detail, withOperatorFiling(h.updateOperatorFiling))
	mux.HandleFunc("POST "+detail+"/submit", withOperatorFiling(h.submit))
	mux.HandleFunc("GET "+detail+"/attachments", withOperatorFiling(h.listAttachments))
	mux.HandleFunc("POST "+detail+"/attachments", withOperatorFiling(h.upload))
	mux.HandleFunc("GET "+detail+"/attachments/{attachmentId}", withOperatorFiling(h.download))
	mux.HandleFunc("GET "+detail+"/attachments/{attachmentId}/text", withOperatorFiling(h.filingMaterialText))
	mux.HandleFunc("GET "+detail+"/review-history", withOperatorFiling(h.history))
	mux.HandleFunc("GET "+detail+"/receipt", withOperatorFiling(h.downloadOperatorFilingReceipt))
}

// withOperatorFiling 将长度与字段校验通过的本地单位范围绑定到业务调用。
// 参数 handle 为读取范围并办理备案的处理方法。
// 返回值 要求请求明确选择单一运营单位的 HTTP 处理方法。
func withOperatorFiling(handle http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		organization, err := reviewinbox.Scope(r, "organization")
		if err != nil {
			respondError(w, ErrInvalid)
			return
		}
		ctx := context.WithValue(r.Context(), operatorFilingScopeKey{}, organization)
		handle(w, r.WithContext(ctx))
	}
}

// operatorFilingScope 读取本地运营者备案路由绑定的所选单位。
// 参数 ctx 为包含备案访问信息的业务上下文。
// 返回值 运营单位名称，其他业务入口返回空字符串。
func operatorFilingScope(ctx context.Context) string {
	organization, _ := ctx.Value(operatorFilingScopeKey{}).(string)
	return organization
}

// operatorFilingList 查询指定运营单位的备案及真实办理状态。
// 参数 w 为响应，r 包含单位名称、查询过滤条件和分页位置。
// 返回值 无。
func (h endpoint) operatorFilingList(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		respondError(w, ErrInvalid)
		return
	}
	organization := strings.TrimSpace(query.Get("organization"))
	if len(query["organization"]) != 1 || !validText(organization, 200) || query.Has("risk") {
		respondError(w, ErrInvalid)
		return
	}
	query.Del("organization")
	input, err := parseListQuery(query, validFilingStatus)
	if err != nil {
		respondError(w, err)
		return
	}
	ctx := context.WithValue(r.Context(), operatorFilingScopeKey{}, organization)
	result, err := h.service.list(ctx, filing, input)
	respond(w, http.StatusOK, result, err)
}

// operatorFilingDetail 只展示该运营单位可办理的详情及回执状态。
// 参数 w 为响应，r 为包含备案标识和本地运营单位的请求。
// 返回值 无。
func (h endpoint) operatorFilingDetail(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.detail(r.Context(), filing, r.PathValue("id"))
	item.AllowedActions = institutionEditActions(item.Status)
	item.RequiredMaterials = requiredCaseMaterials(filing, legacySubmissionProfile)
	respond(w, http.StatusOK, item, err)
}

// createOperatorFiling 将自主备案表单绑定到所选运营单位并保存草稿。
// 参数 w 为响应，r 为包含已选单位与草稿字段的请求。
// 返回值 无。
func (h endpoint) createOperatorFiling(w http.ResponseWriter, r *http.Request) {
	var input operatorFilingInput
	if httpx.Decode(w, r, &input) != nil {
		respondError(w, ErrInvalid)
		return
	}
	fields, err := input.asFields(operatorFilingScope(r.Context()))
	if err != nil {
		respondError(w, err)
		return
	}
	item, err := h.service.create(r.Context(), filing, fields, "")
	respond(w, http.StatusCreated, item, err)
}

// updateOperatorFiling 按运营单位和版本修改尚可编辑的备案草稿或补正材料表单。
// 参数 w 为响应，r 包含备案标识、编辑字段及已读版本。
// 返回值 无。
func (h endpoint) updateOperatorFiling(w http.ResponseWriter, r *http.Request) {
	var input struct {
		operatorFilingInput
		versionInput
	}
	if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
		respondError(w, ErrInvalid)
		return
	}
	fields, err := input.asFields(operatorFilingScope(r.Context()))
	if err != nil {
		respondError(w, err)
		return
	}
	item, err := h.service.update(r.Context(), filing, r.PathValue("id"), input.ExpectedVersion, fields, "")
	respond(w, http.StatusOK, item, err)
}

// asFields 依据选定运营者归属生成共用备案处理字段。
// 参数 organization 为服务端已校验的运营单位名称。
// 返回值 可存储的备案字段或表单错误。
func (input operatorFilingInput) asFields(organization string) (saveFields, error) {
	return parseFilingFields(filingInput{
		SystemName: input.SystemName, OrganizationName: organization,
		AssessmentOrg: input.AssessmentOrg, Level: input.Level, Classification: input.Classification,
	}, false)
}

// downloadOperatorFilingReceipt 根据最新提交或审核通过记录返回 UTF-8 业务回执。
// 参数 w 为附件响应，r 为包含备案标识与运营单位范围的请求。
// 返回值 无。
func (h endpoint) downloadOperatorFilingReceipt(w http.ResponseWriter, r *http.Request) {
	body, err := h.service.operatorFilingReceipt(r.Context(), r.PathValue("id"))
	if errors.Is(err, ErrConflict) {
		httpx.Error(w, http.StatusConflict, "receipt_unavailable", "送审后可下载备案回执。")
		return
	}
	if err != nil {
		respondError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=filing-receipt.txt")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "filing-receipt.txt", time.Time{}, strings.NewReader(body))
}

// operatorFilingReceipt 在同一快照中读取本单位备案及其最近的有效回执事件。
// 参数 ctx 为包含本地运营单位的请求上下文，id 为备案标识。
// 返回值 回执内容或记录不存在、暂无回执及数据库错误。
func (s *Service) operatorFilingReceipt(ctx context.Context, id string) (string, error) {
	organization := operatorFilingScope(ctx)
	if !validID(id) || organization == "" {
		return "", ErrNotFound
	}
	var number, systemName, state string
	var occurred *time.Time
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(receipt.merge_history_number,c.number),COALESCE(receipt.merge_history_system_name,c.system_name),
		  COALESCE(receipt.to_status,''),receipt.occurred_at
		FROM business_cases c LEFT JOIN LATERAL (
		    SELECT e.to_status,e.occurred_at,e.merge_history_number,e.merge_history_system_name
		    FROM case_review_events e
		    WHERE e.case_id=c.id AND COALESCE(e.merge_history_scope,c.organization_name)=$2
		      AND `+filingReceiptEvent+`
		    ORDER BY e.id DESC LIMIT 1
		) receipt ON true
		WHERE c.kind='filing' AND c.id=$1 AND (c.organization_name=$2 OR receipt.occurred_at IS NOT NULL)
		  AND c.discarded_at IS NULL`, id, organization).
		Scan(&number, &systemName, &state, &occurred)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", ErrNotFound
	case err != nil:
		return "", err
	case occurred == nil:
		return "", ErrConflict
	}
	title := "备案提交回执"
	if state == approved {
		title = "备案审核通过回执"
	}
	if number == "" {
		number = "待分配"
	}
	return fmt.Sprintf("%s\n当前备案编号：%s\n当前系统名称：%s\n记录时间：%s\n",
		title, strings.Join(strings.Fields(number), " "), strings.Join(strings.Fields(systemName), " "),
		occurred.Format(time.RFC3339)), nil
}
