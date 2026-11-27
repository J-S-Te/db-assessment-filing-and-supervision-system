package cases

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/access"
	"grade-business/internal/httpx"
)

// registerInstitutionFilings 挂载本地机构侧备案及生命周期申请入口。
// 参数 mux 为本地业务路由。
// 返回值 无。
func (s *Service) registerInstitutionFilings(mux *http.ServeMux) {
	const filingPath = "/api/v1/institution/filings/{id}"
	const applicationPath = "/api/v1/institution/filing-applications/{id}"
	h := endpoint{service: s, kind: filing}
	mux.HandleFunc("GET "+filingPath, withInstitution(h.institutionFilingDetail))
	mux.HandleFunc("PATCH "+filingPath, withInstitution(h.institutionFilingUpdate))
	mux.HandleFunc("POST "+filingPath+"/submit", withInstitution(h.institutionFilingSubmit))
	mux.HandleFunc("POST "+filingPath+"/review-actions", withInstitution(h.reviewInstitutionFiling))
	mux.HandleFunc("GET "+filingPath+"/review-history", withInstitution(h.institutionFilingHistory))
	mux.HandleFunc("GET "+filingPath+"/attachments", withInstitution(h.institutionMaterials))
	mux.HandleFunc("POST "+filingPath+"/attachments", withInstitution(h.uploadToScope))
	mux.HandleFunc("GET "+filingPath+"/attachments/{attachmentId}", withInstitution(h.downloadFromScope))
	mux.HandleFunc("GET "+filingPath+"/attachments/{attachmentId}/text", withInstitution(h.filingMaterialTextFromScope))
	mux.HandleFunc("GET "+applicationPath, withInstitution(s.institutionApplicationDetail))
	mux.HandleFunc("PATCH "+applicationPath, withInstitution(s.institutionApplicationUpdate))
	mux.HandleFunc("POST "+applicationPath+"/submit", withInstitution(s.institutionApplicationSubmit))
	mux.HandleFunc("POST "+applicationPath+"/review-actions", withInstitution(s.reviewInstitutionApplication))
	mux.HandleFunc("GET "+applicationPath+"/review-history", withInstitution(s.institutionApplicationHistory))
	mux.HandleFunc("POST "+applicationPath+"/withdraw", withInstitution(s.withdrawScopedApplication))
}

// institutionFilingDetail 返回所属备案及机构可办理的编辑或初审动作。
// 参数 w、r 为响应和详情请求，institution 为机构范围。
// 返回值 无。
func (h endpoint) institutionFilingDetail(w http.ResponseWriter, r *http.Request, institution string) {
	item, err := h.service.detailInScope(r.Context(), filing, r.PathValue("id"), institution)
	item.AllowedActions = institutionEditActions(item.Status)
	if item.InitialReviewAudience != "" {
		_, authenticated := access.Current(r.Context())
		item.AllowedActions = initialReviewActions(item.Status, !authenticated)
	}
	if item.withdrawalPending {
		item.AllowedActions = []string{}
	}
	respond(w, 200, item, err)
}

// institutionEditActions 只向机构展示草稿与补正的提交及编辑操作。
// 参数 status 为数据库存储的状态。
// 返回值 非空切片形式的允许操作。
func institutionEditActions(status string) []string {
	if editable(status) {
		return []string{"edit", submit}
	}
	return []string{}
}

// institutionFilingUpdate 用途为编辑本机构草稿或补正备案，允许修改或清空预填编号。
// 参数 w、r 为响应和编辑请求，institution 为接收范围。
// 返回值 无。
func (h endpoint) institutionFilingUpdate(w http.ResponseWriter, r *http.Request, institution string) {
	var input struct {
		filingInput
		versionInput
	}
	if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
		respondError(w, ErrInvalid)
		return
	}
	if input.AssessmentOrg == "" {
		input.AssessmentOrg = institution
	}
	fields, err := parseFilingFields(input.filingInput, false)
	if err != nil {
		respondError(w, err)
		return
	}
	item, err := h.service.update(r.Context(), filing, r.PathValue("id"), input.ExpectedVersion, fields, institution)
	respond(w, 200, item, err)
}

// institutionFilingSubmit 使用现有状态机提交本机构草稿或补正备案。
// 参数 w、r 为响应和提交请求，institution 为接收范围。
// 返回值 无。
func (h endpoint) institutionFilingSubmit(w http.ResponseWriter, r *http.Request, institution string) {
	var input versionInput
	if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
		respondError(w, ErrInvalid)
		return
	}
	item, err := h.service.changeStatus(r.Context(), filing, r.PathValue("id"),
		reviewInput{versionInput: input, Decision: submit, institution: institution})
	respond(w, 200, item, err)
}

// institutionMaterials 查询所属备案或测评报告已真实登记的公开材料。
// 参数 w、r 为响应和材料请求，institution 为接收范围。
// 返回值 无。
func (h endpoint) institutionMaterials(w http.ResponseWriter, r *http.Request, institution string) {
	result, err := h.service.materialsInScope(r.Context(), h.kind, r.PathValue("id"), institution)
	respond(w, 200, itemResponse[Attachment]{Items: result.Attachments}, err)
}

// institutionApplicationDetail 查询归属原备案机构的申请及可办理动作。
// 参数 w、r 为响应和详情请求，institution 为原申请机构。
// 返回值 无。
func (s *Service) institutionApplicationDetail(w http.ResponseWriter, r *http.Request, institution string) {
	item, err := s.applicationInScope(r.Context(), r.PathValue("id"), institution)
	respond(w, 200, item, err)
}

// institutionApplicationUpdate 保存本机构草稿或补正申请的内容。
// 参数 w、r 为响应和编辑请求，institution 为原申请机构。
// 返回值 无。
func (s *Service) institutionApplicationUpdate(w http.ResponseWriter, r *http.Request, institution string) {
	s.updateApplicationInScope(w, r, institution)
}

// institutionApplicationSubmit 将本机构草稿或补正申请提交初审。
// 参数 w、r 为响应和提交请求，institution 为原申请机构。
// 返回值 无。
func (s *Service) institutionApplicationSubmit(w http.ResponseWriter, r *http.Request, institution string) {
	s.submitApplicationInScope(w, r, institution)
}

// publishFilingReview 按实际办理阶段向登记机构和运营单位送达备案通知及意见。
// 参数 ctx、tx、id 指定业务事务和备案，version 为审核版本，零值仅向机构重发最新意见。
// 返回值 同事务写入失败时的数据库错误。
func publishFilingReview(ctx context.Context, tx pgx.Tx, id string, version int64) error {
	_, err := tx.Exec(ctx, `INSERT INTO review_notices
		(audience,resource,resource_id,event_version,state,recipient,reference,subject,comment)
		SELECT target.audience,'filings',c.id,e.version,e.to_status,target.recipient,
		    c.number,c.system_name,e.comment
		FROM business_cases c
		CROSS JOIN LATERAL (
		    SELECT version,to_status,comment,operator_role FROM case_review_events
		    WHERE case_id=c.id AND ($2<>0 OR to_status IN ('returned','approved'))
		      AND ($2::bigint=0 OR version=$2)
		    ORDER BY version DESC LIMIT 1
		) e
		CROSS JOIN LATERAL (
		    VALUES ('institution',c.assessment_org), ('operator',c.organization_name)
		) target(audience,recipient)
		WHERE c.kind='filing' AND c.id=$1
		  AND ((e.to_status IN ('returned','approved') AND (target.audience='institution'
		            OR ($2<>0 AND length(target.recipient) BETWEEN 1 AND 200
		                AND (e.to_status='approved' OR c.operator_origin OR c.directory_unit_id IS NOT NULL))))
		    OR (c.operator_origin AND target.audience='institution' AND e.to_status='pending_initial'
		            AND e.operator_role IS NULL)
		    OR (c.operator_origin AND target.audience='operator' AND e.to_status='pending_final'
		            AND length(target.recipient) BETWEEN 1 AND 200))
		ON CONFLICT DO NOTHING`, id, version)
	return err
}

// redeliverCorrection 在监管改派后保留原机构回执并通知新的接收机构。
// 参数 ctx 和 tx 为业务事务，kind 与 id 为已改派的业务。
// 返回值 派送错误。
func redeliverCorrection(ctx context.Context, tx pgx.Tx, kind, id string) error {
	if kind == report {
		return republishReturnedFeedback(ctx, tx, id)
	}
	return publishFilingReview(ctx, tx, id, 0)
}
