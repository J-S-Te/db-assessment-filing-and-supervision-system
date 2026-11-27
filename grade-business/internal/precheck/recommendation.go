package precheck

import (
	"strings"

	"grade-business/internal/filingaudit"
)

// Recommendation 汇总当前预审的审核建议及可核对的待办项。
type Recommendation struct {
	Action           string   `json:"action"`
	MissingMaterials []string `json:"missingMaterials,omitempty"`
	FailedChecks     []string `json:"failedChecks,omitempty"`
	ManualChecks     []string `json:"manualChecks,omitempty"`
	Incomplete       bool     `json:"incomplete,omitempty"`
}

// recommendReview 从当前已完成任务的缺件与内容检查得出建议。
// 参数 job 为携带结果和过期状态的预审任务。
// 返回值 当前任务的审核建议，未完成或已过期时为空。
func recommendReview(job Job) *Recommendation {
	if job.Status != "succeeded" || job.Stale {
		return nil
	}
	recommendation := &Recommendation{Action: "pass"}
	for _, finding := range job.Findings {
		if strings.HasSuffix(finding.RuleID, ".missing") {
			material := strings.TrimSpace(strings.TrimPrefix(finding.Message, "缺少"))
			recommendation.MissingMaterials = append(recommendation.MissingMaterials, material)
		}
	}
	catalog := filingaudit.Rules()
	statuses := make(map[string]string, len(job.Checks))
	for _, check := range job.Checks {
		if _, exists := statuses[check.RuleID]; exists {
			recommendation.Incomplete = true
			if statuses[check.RuleID] == "fail" {
				continue
			}
		}
		statuses[check.RuleID] = check.Status
	}
	for _, rule := range catalog {
		switch statuses[rule.ID] {
		case "pass", "not_applicable":
		case "fail":
			recommendation.FailedChecks = append(recommendation.FailedChecks, rule.Title)
		case "manual":
			recommendation.ManualChecks = append(recommendation.ManualChecks, rule.Title)
		default:
			recommendation.Incomplete = true
		}
	}
	switch {
	case len(recommendation.MissingMaterials) > 0 || len(recommendation.FailedChecks) > 0:
		recommendation.Action = "correct"
	case recommendation.Incomplete || len(recommendation.ManualChecks) > 0:
		recommendation.Action = "manual_review"
	}
	return recommendation
}
