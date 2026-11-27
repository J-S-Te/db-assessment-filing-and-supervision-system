package filingaudit

import (
	"strings"
	"testing"
	"time"
)

// TestEvaluateFilingRules 核对真实材料片段可生成 11 项且引用实际原文。
// 参数 t 提供无个人信息的合成材料和断言。
// 返回值 无。
func TestEvaluateFilingRules(t *testing.T) {
	docs := []Document{
		auditFixture("filing_form", "备案表.doc", "定级对象编号：合成-A01", "定级对象总数：1",
			"单位名称：合成测试单位", "系统名称：合成系统", "行政区划：330802", "隶属关系：县",
			"统一社会信用代码：913308020000000001", "定级时间：2026年1月22日",
			"投入运行时间：2025-12-01", "承载业务：受理测试工单", "新技术新应用：无",
			"云计算场景：未采用", "大数据场景：否"),
		auditFixture("classification_report", "定级报告.docx", "定级对象编号：合成-A01",
			"单位名称：合成测试单位", "系统名称：合成系统", "定级时间：2026-01-22",
			"投入运行时间：2025年12月1日", "盖章状态：未盖章", "承载业务：受理测试工单",
			"新技术新应用：无", "承载数据：测试工单", "是否单位间数据流转：有",
			"数据流转对象：外部测试单位"),
		auditFixture("expert_opinion", "专家意见书.pdf", "单位名称：另一单位",
			"系统名称：合成系统", "定级时间：2026/1/22", "专家签字：张某"),
		auditFixture("data_survey", "数据摸底调查.docx", "承载数据：测试工单",
			"是否单位间数据流转：无"),
	}
	got := Evaluate(docs, Unit{Name: "合成测试单位", Code: "913308020000000001", District: "柯城区"},
		time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC))
	if len(got) != 11 {
		t.Fatalf("规则数为 %d", len(got))
	}
	expected := map[string]string{
		"object_number": "pass", "system_and_unit": "fail", "classification_date": "pass",
		"seals": "fail", "responsible_person": "manual", "protection_level": "manual",
		"assets": "manual", "business_description": "pass", "carried_data": "fail",
		"responsible_entity": "manual", "filing_certificate": "not_applicable",
	}
	for _, row := range got {
		if row.Status != expected[row.RuleID] {
			t.Errorf("%s 得到 %s (%s)，预期 %s", row.RuleID, row.Status, row.Message, expected[row.RuleID])
		}
		for _, evidence := range row.Evidence {
			if evidence.AttachmentID == "" || evidence.Location == "" || evidence.Quote == "" {
				t.Errorf("%s 引用不完整", row.RuleID)
			}
		}
	}
	if !strings.Contains(got[8].Message, "流转") || len(got[8].Evidence) < 2 {
		t.Fatal("数据流转冲突缺少具体问题和两侧证据")
	}
}

// TestEvaluateTemplateCannotPass 验证缺件、模板占位和图形线索不能冒充核验通过。
// 参数 t 提供从模板特点归纳的合成数据。
// 返回值 无。
func TestEvaluateTemplateCannotPass(t *testing.T) {
	docs := []Document{auditFixture("classification_report", "定级报告.docx",
		"姓名（签字）", "单位（盖章）", "定级时间：XXXX年XX月XX日", "网络拓扑图：见附图",
		"主管人员：测试人员 身份证号码：110101199001011234")}
	got := Evaluate(docs, Unit{}, time.Now())
	for _, row := range got {
		if row.Status != "manual" {
			t.Errorf("没有可核实材料时 %s 返回 %s", row.RuleID, row.Status)
		}
		for _, evidence := range row.Evidence {
			if strings.Contains(evidence.Quote, "110101199001011234") {
				t.Fatal("证据摘要披露完整个人编号")
			}
		}
	}
}

// auditFixture 构造带位置的合成材料。
// 参数 kind 和 name 表示来源类型和文件名，lines 为原文。
// 返回值 独立的材料视图。
func auditFixture(kind, name string, lines ...string) Document {
	doc := Document{Kind: kind, FileName: name, AttachmentID: kind + "-attachment"}
	for _, line := range lines {
		doc.Lines = append(doc.Lines, Line{Location: "正文段落", Text: line})
	}
	return doc
}
