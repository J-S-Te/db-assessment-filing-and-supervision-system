package precheck

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"testing"

	"grade-business/internal/filingaudit"
)

// TestCompletedPrecheckRecommendation 核对建议优先级、依据及过期状态。
// 参数 t 为合成材料结果及响应断言的上下文。
// 返回值 无。
func TestCompletedPrecheckRecommendation(t *testing.T) {
	var all []filingaudit.Result
	for _, rule := range filingaudit.Rules() {
		all = append(all, filingaudit.Result{RuleID: rule.ID, Status: "pass"})
	}
	doubt := slices.Clone(all)
	doubt[3].Status = "fail"
	doubt[4].Status = "manual"
	cases := []struct {
		name   string
		job    Job
		action string
		reason string
	}{
		{"缺件优先补正", Job{Status: "succeeded", Checks: all,
			Findings: []Finding{{RuleID: "configured.1.missing", Message: "缺少补充说明"}}}, "correct", "补充说明"},
		{"不通过优先补正", Job{Status: "succeeded", Checks: doubt}, "correct", "报告用章"},
		{"证据不足人工复核", Job{Status: "succeeded", Checks: slices.Delete(slices.Clone(doubt), 3, 4)},
			"manual_review", "主管人员"},
		{"材料齐全且全部通过", Job{Status: "succeeded", Checks: all}, "pass", ""},
		{"旧任务数据不全", Job{Status: "succeeded"}, "manual_review", ""},
		{"过期任务不展示", Job{Status: "succeeded", Stale: true, Checks: all}, "", ""},
		{"执行中不产生建议", Job{Status: "running", Checks: all}, "", ""},
	}
	for _, fixture := range cases {
		t.Run(fixture.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			reply(response, 200, fixture.job, nil)
			var body struct {
				Recommendation *struct {
					Action       string   `json:"action"`
					Missing      []string `json:"missingMaterials"`
					Failed       []string `json:"failedChecks"`
					ManualReview []string `json:"manualChecks"`
				} `json:"recommendation"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			rec := body.Recommendation
			if fixture.action == "" {
				if rec != nil {
					t.Fatal("只应为当前版本已完成的任务展示建议")
				}
				return
			}
			if rec == nil || rec.Action != fixture.action {
				t.Fatalf("预期建议 %s，实际 %+v", fixture.action, rec)
			}
			if fixture.reason != "" && !slices.Contains(slices.Concat(rec.Missing, rec.Failed, rec.ManualReview), fixture.reason) {
				t.Fatalf("缺少建议依据 %q", fixture.reason)
			}
		})
	}
}
