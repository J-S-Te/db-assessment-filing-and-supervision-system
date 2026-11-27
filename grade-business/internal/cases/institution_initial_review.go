package cases

import (
	"net/http"
	"net/url"

	"grade-business/internal/httpx"
)

const institutionReviewAudience = "institution"

// checkReviewRole 验证当前审核阶段是否由对应机构或监管席位办理。
// 参数 status 为加锁后的状态，operatorOrigin 为持久化的运营者发起标记。
// 返回值 当前席位不允许执行审核时返回状态冲突。
func (input reviewInput) checkReviewRole(status string, operatorOrigin bool) error {
	if input.Decision != approve && input.Decision != returnForFix {
		return nil
	}
	institutionRequired := operatorOrigin && status == pendingInitial
	if institutionRequired != (input.institutionReviewer != "") {
		return ErrConflict
	}
	return nil
}

// reviewActor 确定审核轨迹需要登记的真实办理席位。
// 参数 regulator 为监管配置席位，organization 为本地运营者范围。
// 返回值 能区分机构、运营者及监管的办理人名称。
func (input reviewInput) reviewActor(regulator, organization string) string {
	if input.institutionReviewer != "" {
		return "本地机构：" + input.institutionReviewer
	}
	if input.institution != "" {
		return "本地机构：" + input.institution
	}
	if organization != "" {
		return "本地运营者：" + organization
	}
	return regulator
}

// initialReviewActions 返回运营者提交后，当前详情角色可执行的审核动作。
// 参数 status 为已保存状态，institution 表示当前为所属机构席位。
// 返回值 当前角色可见的操作列表，未分配时为空列表。
func initialReviewActions(status string, institution bool) []string {
	if institution && status == pendingInitial {
		return allowedActions(status)
	}
	return []string{}
}

// decodeReviewAction 校验监管端和机构端共用的审核决定请求。
// 参数 w、r 为审核请求与响应，allowWithdraw 表示该入口是否支持已有撤回命令。
// 返回值 已校验的请求对象及无效输入错误。
func decodeReviewAction(w http.ResponseWriter, r *http.Request, allowWithdraw bool) (reviewInput, error) {
	var input reviewInput
	if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
		return reviewInput{}, ErrInvalid
	}
	if input.Decision != approve && input.Decision != returnForFix &&
		!(allowWithdraw && input.Decision == withdrawApplication) {
		return reviewInput{}, ErrInvalid
	}
	return input, nil
}

// consumeAssignedReview 提取只在机构备案业务集合支持的待初审过滤条件。
// 参数 query 为列表查询，available 表示当前列表为机构备案或备案申请。
// 返回值 是否筛选分派初审，以及不合法或重复筛选时的错误。
func consumeAssignedReview(query url.Values, available bool) (bool, error) {
	values, exists := query["review"]
	if !exists {
		return false, nil
	}
	if !available || len(values) != 1 || values[0] != "assigned" {
		return false, ErrInvalid
	}
	query.Del("review")
	return true, nil
}

// reviewInstitutionFiling 办理当前备案负责机构收到的初审。
// 参数 w、r 为审核响应与请求，institution 为已校验的负责机构。
// 返回值 无，响应申请状态和当前版本或业务错误。
func (h endpoint) reviewInstitutionFiling(w http.ResponseWriter, r *http.Request, institution string) {
	input, err := decodeReviewAction(w, r, false)
	if err != nil {
		respondError(w, err)
		return
	}
	input.institutionReviewer = institution
	result, err := h.service.changeStatus(r.Context(), h.kind, r.PathValue("id"), input)
	respond(w, http.StatusOK, result, err)
}

// reviewInstitutionApplication 办理运营者变更注销申请的原机构初审。
// 参数 w、r 为审核响应与请求，institution 为已校验的原备案机构。
// 返回值 无，响应申请状态和当前版本或业务错误。
func (s *Service) reviewInstitutionApplication(w http.ResponseWriter, r *http.Request, institution string) {
	input, err := decodeReviewAction(w, r, false)
	if err != nil {
		respondError(w, err)
		return
	}
	input.institutionReviewer = institution
	result, err := s.changeApplicationStatus(r.Context(), r.PathValue("id"), input)
	respondApplication(w, result, err)
}

// institutionFilingHistory 返回当前所属备案的实际审核留痕。
// 参数 w、r 为审核历史响应与请求，institution 为已校验当前机构。
// 返回值 无，响应从新到旧的历史事件或归属错误。
func (h endpoint) institutionFilingHistory(w http.ResponseWriter, r *http.Request, institution string) {
	items, err := h.service.history(r.Context(), h.kind, r.PathValue("id"), institution)
	respond(w, http.StatusOK, itemResponse[reviewEvent]{Items: items}, err)
}

// institutionApplicationHistory 返回申请原备案机构可以检查的审核留痕。
// 参数 w、r 为审核历史响应与请求，institution 为已校验原备案机构。
// 返回值 无，响应从新到旧的历史事件或归属错误。
func (s *Service) institutionApplicationHistory(w http.ResponseWriter, r *http.Request, institution string) {
	items, err := s.applicationHistory(r.Context(), r.PathValue("id"), institution)
	respond(w, http.StatusOK, itemResponse[reviewEvent]{Items: items}, err)
}
