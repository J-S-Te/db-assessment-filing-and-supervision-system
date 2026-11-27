package cases

import (
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	filing = "filing"
	report = "report"

	draft           = "draft"
	pendingInitial  = "pending_initial"
	pendingFinal    = "pending_final"
	approved        = "approved"
	returned        = "returned"
	submit          = "submit"
	approve         = "approve"
	returnForFix    = "return_for_correction"
	maxCommentRunes = 1000

	legacySubmissionProfile      = "legacy"
	institutionSubmissionProfile = "institution_v2"
)

var (
	ErrNotFound             = errors.New("case or attachment not found")
	ErrInvalid              = errors.New("invalid case input")
	ErrConflict             = errors.New("record state changed")
	ErrDuplicate            = errors.New("number already exists")
	errFilingNumberRequired = errors.New("filing number must be assigned at final approval")
	ErrMaterials            = errors.New("required attachments missing")
	ErrOpenMaterialIssues   = errors.New("material issue not corrected")
)

// transition 计算备案或测评审核动作对应的下一状态。
// 参数 status 为当前状态，action 为操作，comment 为审核意见，ready 表示必传材料已齐备。
// 返回值 目标状态或输入及状态错误。
func transition(status, action, comment string, ready bool) (string, error) {
	if !validReviewComment(comment) {
		return "", ErrInvalid
	}
	switch action {
	case submit:
		if !editable(status) {
			return "", ErrConflict
		}
		if !ready {
			return "", ErrMaterials
		}
		return pendingInitial, nil
	case approve, returnForFix:
		if status != pendingInitial && status != pendingFinal {
			return "", ErrConflict
		}
		if action == returnForFix {
			if strings.TrimSpace(comment) == "" {
				return "", ErrInvalid
			}
			return returned, nil
		}
		if status == pendingInitial {
			return pendingFinal, nil
		}
		return approved, nil
	default:
		return "", ErrInvalid
	}
}

// validReviewComment 验证可选审核意见是否符合长度和内容限制。
// 参数 comment 为审核意见，空意见由操作自行判断是否允许。
// 返回值 内容可存储时为真。
func validReviewComment(comment string) bool {
	return utf8.RuneCountInString(comment) <= maxCommentRunes && !strings.ContainsRune(comment, 0)
}

// editable 检查业务状态是否允许修改或新增材料。
// 参数 status 为记录的当前状态。
// 返回值 可编辑时为真。
func editable(status string) bool {
	return status == draft || status == returned
}

// allowedActions 返回业务状态在详情页可执行的命令。
// 参数 status 为记录的当前状态。
// 返回值 业务操作列表，已办结时返回空切片。
func allowedActions(status string) []string {
	switch status {
	case draft, returned:
		return []string{"edit", submit}
	case pendingInitial, pendingFinal:
		return []string{approve, returnForFix}
	default:
		return []string{}
	}
}

// validStatus 验证列表所用的状态筛选值。
// 参数 status 为筛选值，空字符串表示全部状态。
// 返回值 指定状态有效时为真。
func validStatus(status string) bool {
	switch status {
	case "", draft, pendingInitial, pendingFinal, approved, returned:
		return true
	default:
		return false
	}
}

// validFilingStatus 为备案查询允许历史撤回状态。
// 参数 status 为客户端请求的筛选值。
// 返回值 状态适用于备案列表时为真。
func validFilingStatus(status string) bool {
	return status == applicationWithdrawn || validStatus(status)
}

var institutionReportMaterials = []string{
	"report_file", "remediation_plan", "assessment_conclusion", "issues_recommendations", "cover_basic_statement",
}

var optionalInstitutionReportMaterials = []string{
	"cloud_extension", "big_data_extension", "major_risk_recommendations", "supporting",
}

// requiredCaseMaterials 返回此次提交对应的既有或机构九类模式必传材料。
// 参数 kind 为记录类型，profile 为记录保存时固化的提交配置。
// 返回值 当前记录提交必须齐备的材料类别。
func requiredCaseMaterials(kind, profile string) []string {
	if kind == filing {
		return []string{"filing_form", "classification_report"}
	}
	if profile == institutionSubmissionProfile {
		return institutionReportMaterials
	}
	return []string{"report_file"}
}

// validMaterialKind 检查材料类别是否与记录类型和固化配置匹配。
// 参数 kind 为业务记录类型，material 为材料类别，profile 为该记录的提交配置。
// 返回值 记录可接收该材料类别时为真。
func validMaterialKind(kind, material, profile string) bool {
	if material == "supporting" || slices.Contains(requiredCaseMaterials(kind, profile), material) {
		return true
	}
	return kind == report && profile == institutionSubmissionProfile &&
		slices.Contains(optionalInstitutionReportMaterials, material)
}
