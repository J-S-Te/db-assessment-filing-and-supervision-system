package filingaudit

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"grade-business/internal/geoarea"
)

type evaluation struct {
	Evidence []Evidence
	Failed   []string
	Manual   []string
}

type auditInput struct {
	entries
	unit Unit
	date time.Time
}

var evaluateRules = [...]func(*evaluation, auditInput){
	evaluateObjectNumber, evaluateSystemAndUnit, evaluateDates, evaluateSeals,
	evaluateResponsiblePerson, evaluateLevels, evaluateAssets, evaluateBusiness,
	evaluateCarriedData, evaluateResponsibleEntity, evaluateCertificate,
}

// Evaluate 根据正文和可信名录按原表顺序返回 11 项确定性结论。
// 参数 docs 为实际备案材料，unit 为关联名录，asOf 为本次执行日期。
// 返回值 每项的状态、理由及真实原文依据。
func Evaluate(docs []Document, unit Unit, asOf time.Time) []Result {
	data := auditInput{collect(docs), unit, asOf}
	results := make([]Result, 0, len(catalog))
	for index, rule := range catalog {
		var outcome evaluation
		evaluateRules[index](&outcome, data)
		result := Result{RuleID: rule.ID, Status: "pass", Message: "已核对规则所需字段，未发现不一致。", Evidence: outcome.Evidence}
		switch {
		case len(outcome.Failed) > 0:
			result.Status, result.Message = "fail", strings.Join(outcome.Failed, "；")+"。"
		case len(outcome.Manual) > 0:
			result.Status, result.Message = "manual", strings.Join(outcome.Manual, "；")+"。"
		case rule.ID == "filing_certificate" && unusedTechnologies(data):
			result.Status, result.Message = "not_applicable", "备案表明确填写未采用云计算及大数据场景。"
		}
		results = append(results, result)
	}
	return results
}

// read 读取实际命中的值并附上有限数量的不重复原文。
// 参数 values、normalize 为候选字段及规范化方法。
// 返回值 可参与比对的不同值。
func (out *evaluation) read(values []value, normalize func(string) string) []string {
	keys, evidence := valuesFor(values, normalize)
	for _, item := range evidence {
		if len(out.Evidence) >= 18 {
			break
		}
		if !slices.Contains(out.Evidence, item) {
			out.Evidence = append(out.Evidence, item)
		}
	}
	return keys
}

// compare 检查所需材料的字段是否齐全且可比对。
// 参数 label 为业务字段，strict 表示差异能直接判定异常，normalize 和 groups 为比较方法和字段组。
// 返回值 无，比较情况写入本项结论。
func (out *evaluation) compare(label string, strict bool, normalize func(string) string, groups ...[]value) {
	var keys []string
	missing := false
	for _, group := range groups {
		values := out.read(group, normalize)
		missing = missing || len(values) == 0
		for _, key := range values {
			if !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
		}
	}
	switch {
	case len(keys) > 1 && strict:
		out.Failed = append(out.Failed, label+"存在明确差异")
	case len(keys) > 1:
		out.Manual = append(out.Manual, label+"表述不同，请核对原文")
	case missing:
		out.Manual = append(out.Manual, label+"缺少可比对字段")
	}
}

// require 至少要求一个明确填写的字段存在。
// 参数 label、values 为业务提示和候选原文。
// 返回值 无，缺项写入人工核实理由。
func (out *evaluation) require(label string, values []value) {
	if len(out.read(values, comparable)) == 0 {
		out.Manual = append(out.Manual, "未提取到"+label)
	}
}

// evaluateObjectNumber 核对备案表的对象编号和显式对象总数。
// 参数 out、in 为结论及材料事实。
// 返回值 无。
func evaluateObjectNumber(out *evaluation, in auditInput) {
	ids := out.read(in.entries[form]["object"], codeValue)
	counts := out.read(in.entries[form]["count"], positiveNumber)
	for _, kind := range []string{report, expert} {
		for _, id := range out.read(in.entries[kind]["object"], codeValue) {
			if len(ids) > 0 && !slices.Contains(ids, id) {
				out.Failed = append(out.Failed, "材料中出现备案表未列明的定级对象编号")
			}
		}
	}
	switch {
	case len(counts) > 1:
		out.Failed = append(out.Failed, "备案表出现多个不同的定级对象总数")
	case len(ids) == 0 || len(counts) == 0:
		out.Manual = append(out.Manual, "备案表缺少可识别的对象编号或对象总数")
	case counts[0] != strconv.Itoa(len(ids)):
		out.Manual = append(out.Manual, "可提取的对象编号数量与明示总数不一致，请核对表一")
	}
}

// evaluateSystemAndUnit 对齐三类文档名称并与本地名录核对。
// 参数 out、in 为结论及材料事实。
// 返回值 无。
func evaluateSystemAndUnit(out *evaluation, in auditInput) {
	a, b, c := in.entries[form], in.entries[report], in.entries[expert]
	out.compare("单位名称", true, comparable, a["unit"], b["unit"], c["unit"])
	out.compare("系统名称", true, comparable, a["system"], b["system"], c["system"])
	out.require("备案表单位隶属关系", a["relation"])
	out.matchDirectory("单位名称", a["unit"], in.unit.Name, comparable)
	out.matchDirectory("统一社会信用代码", a["credit"], in.unit.Code, codeValue)
	out.matchDirectory("行政区划", a["district"], in.unit.District, districtValue)
}

// matchDirectory 将材料明确值和同一备案所绑定的名录值比较。
// 参数 label、values、registered、normalize 分别为提示、原文、名录值和归一化方法。
// 返回值 无。
func (out *evaluation) matchDirectory(label string, values []value, registered string, normalize func(string) string) {
	candidates := out.read(values, normalize)
	known := normalize(registered)
	switch {
	case known == "" || len(candidates) == 0:
		out.Manual = append(out.Manual, label+"缺少材料或可核验名录值")
	case len(candidates) > 1 || candidates[0] != known:
		out.Failed = append(out.Failed, label+"与本地单位名录不一致")
	}
}

// evaluateDates 比较实际定级日期及投入运行日期，避免把版式差异当作异常。
// 参数 out、in 为结论及材料事实。
// 返回值 无。
func evaluateDates(out *evaluation, in auditInput) {
	a, b, c := in.entries[form], in.entries[report], in.entries[expert]
	out.compare("三份材料的定级时间", true, dateValue, a["classification_date"], b["classification_date"], c["classification_date"])
	operation := out.read(a["commission_date"], dateValue)
	if len(operation) == 0 {
		out.Manual = append(out.Manual, "备案表投入运行时间无法确认")
	}
	if len(b["commission_date"]) != 0 {
		out.compare("投入运行日期", true, dateValue, a["commission_date"], b["commission_date"])
	}
	for _, date := range operation {
		if date > in.date.Format("2006-01-02") {
			out.Manual = append(out.Manual, "填写的投入运行时间晚于预审日期，请确认是否为计划日期")
			break
		}
	}
}

// evaluateSeals 捕获写明的缺章结论，纸质或图片章印交由人工。
// 参数 out、in 为结论及材料事实。
// 返回值 无。
func evaluateSeals(out *evaluation, in auditInput) {
	for _, kind := range []string{form, report, "authorization", "supporting"} {
		values := out.read(in.entries[kind]["seal"], comparable)
		if slices.Contains(values, "未盖章") || slices.Contains(values, "未用章") || slices.Contains(values, "缺章") {
			out.Failed = append(out.Failed, "原件明确记载存在未盖章材料")
			return
		}
	}
	out.Manual = append(out.Manual, "请按保护等级核对适用的用章要求并查验原件章印")
}

// evaluateResponsiblePerson 检查明确的签字状态并提示核对主管人员。
// 参数 out、in 为结论及材料事实。
// 返回值 无。
func evaluateResponsiblePerson(out *evaluation, in auditInput) {
	out.require("定级报告主管人员信息", in.entries[report]["person"])
	signatures := out.read(in.entries[expert]["signature"], comparable)
	if slices.Contains(signatures, "未签字") || slices.Contains(signatures, "未签名") {
		out.Failed = append(out.Failed, "专家意见材料明确记载未签字")
		return
	}
	out.Manual = append(out.Manual, "请核对专家签字原件和主管人员身份信息")
}

// evaluateLevels 核对三类保护等级以及报告中的受侵害客体说明。
// 参数 out、in 为结论及材料事实。
// 返回值 无。
func evaluateLevels(out *evaluation, in auditInput) {
	a, b, c := in.entries[form], in.entries[report], in.entries[expert]
	for _, field := range []struct{ key, label string }{
		{"level", "安全保护等级"}, {"business_level", "业务保护等级"}, {"system_level", "系统保护等级"},
	} {
		out.compare(field.label, true, levelValue, a[field.key], b[field.key], c[field.key])
	}
	business := out.read(b["object_business"], comparable)
	system := out.read(b["object_system"], comparable)
	switch {
	case len(business) == 0 || len(system) == 0:
		out.Manual = append(out.Manual, "请核对定级报告的业务信息和系统服务侵害客体论证")
	case slices.Equal(business, system):
		out.Manual = append(out.Manual, "两处侵害客体论证文字相同，请核对是否结合了本对象")
	}
}

// evaluateAssets 比对可提取的资产和拓扑节点，图形需要人工核对。
// 参数 out、in 为结论及材料事实。
// 返回值 无。
func evaluateAssets(out *evaluation, in auditInput) {
	out.require("备案表网络性质", in.entries[form]["network"])
	out.require("定级报告网络边界", in.entries[report]["boundary"])
	out.compare("资产清单与拓扑节点", false, listValue, in.entries[report]["assets"], in.entries[report]["topology"])
}

// evaluateBusiness 发现新技术栏的明确差异并对业务描述作一致性比对。
// 参数 out、in 为结论及材料事实。
// 返回值 无。
func evaluateBusiness(out *evaluation, in auditInput) {
	a, b := in.entries[form], in.entries[report]
	out.compare("承载业务", false, comparable, a["business"], b["business"])
	out.compare("新技术新应用场景", false, listValue, a["new_tech"], b["new_tech"])
}

// evaluateCarriedData 对齐数据类型和明确的跨单位流转表述。
// 参数 out、in 为结论及材料事实。
// 返回值 无。
func evaluateCarriedData(out *evaluation, in auditInput) {
	a, b := in.entries[report], in.entries[survey]
	out.compare("承载数据", false, listValue, a["data"], b["data"])
	out.compare("单位间数据流转", true, yesNo, a["sharing"], b["sharing"])
	uses := out.read(a["sharing"], yesNo)
	if slices.Contains(uses, "yes") || len(a["recipient"])+len(b["recipient"]) > 0 {
		out.compare("数据流转对象", false, recipientValue, a["recipient"], b["recipient"])
	}
}

// evaluateResponsibleEntity 校对责任主体、可证实的自运维关系和投入运行时间。
// 参数 out、in 为结论及材料事实。
// 返回值 无。
func evaluateResponsibleEntity(out *evaluation, in auditInput) {
	a, b := in.entries[form], in.entries[report]
	out.compare("责任主体与备案单位", true, comparable, a["unit"], b["responsible"])
	out.compare("备案表与定级报告系统名称", true, comparable, a["system"], b["system"])
	out.compare("投入运行时间", true, dateValue, a["commission_date"], b["commission_date"])
	out.compare("运维主体与责任单位", false, comparable, b["maintenance"], b["responsible"])
}

// evaluateCertificate 按新技术是否采用核对适用的备案证明。
// 参数 out、in 为结论及材料事实。
// 返回值 无。
func evaluateCertificate(out *evaluation, in auditInput) {
	for _, item := range []struct{ key, label string }{{"cloud", "云计算"}, {"bigdata", "大数据"}} {
		values := out.read(in.entries[form][item.key], yesNo)
		switch {
		case len(values) != 1:
			out.Manual = append(out.Manual, "请确认备案表是否采用"+item.label)
		case values[0] == "yes":
			key := item.key + "_name"
			out.compare(item.label+"平台与备案证明", true, comparable, in.entries[form][key], in.entries[proof][key])
		}
	}
}

// unusedTechnologies 判断备案表是否明确声明两类证明均不适用。
// 参数 in 为当前备案事实。
// 返回值 两项场景均填未采用时为真。
func unusedTechnologies(in auditInput) bool {
	cloud, _ := valuesFor(in.entries[form]["cloud"], yesNo)
	bigData, _ := valuesFor(in.entries[form]["bigdata"], yesNo)
	return slices.Equal(cloud, []string{"no"}) && slices.Equal(bigData, []string{"no"})
}

var knownDate = regexp.MustCompile(`^([0-9]{4})\s*[年./-]\s*([0-9]{1,2})\s*[月./-]\s*([0-9]{1,2})\s*日?$`)
var knownLevel = regexp.MustCompile(`^(?:第)?([一二三四五1-5])(?:级|等)?$`)

// dateValue 将明确且实际存在的日期转为统一格式。
// 参数 text 为日期字段原文。
// 返回值 年月日格式，无法确认时为空。
func dateValue(text string) string {
	match := knownDate.FindStringSubmatch(strings.TrimSpace(text))
	if match == nil {
		return ""
	}
	date, err := time.Parse("2006-1-2", strings.Join(match[1:], "-"))
	if err != nil {
		return ""
	}
	return date.Format("2006-01-02")
}

// levelValue 统一五级保护等级表示。
// 参数 text 为原始等级字段。
// 返回值 数字等级，不能解析时为空。
func levelValue(text string) string {
	match := knownLevel.FindStringSubmatch(comparable(text))
	if match == nil {
		return ""
	}
	if level := strings.Index("一二三四五", match[1]); level >= 0 {
		return strconv.Itoa(level/3 + 1)
	}
	return match[1]
}

// codeValue 保留编号内部符号，避免将不同编号合并。
// 参数 text 为材料中的对象编号或信用代码。
// 返回值 忽略大小写和首尾空白后的值。
func codeValue(text string) string { return strings.ToUpper(strings.TrimSpace(text)) }

// districtValue 归一化已知衢州区划编码和名称。
// 参数 text 为单一标准区划字段。
// 返回值 归一化区划标识。
func districtValue(text string) string {
	if code := geoarea.QuzhouAreaCode(text); code != "" {
		return code
	}
	return comparable(text)
}

// positiveNumber 读取明确填写的正整数对象数量。
// 参数 text 为总数字段。
// 返回值 十进制数，非正整数时为空。
func positiveNumber(text string) string {
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(text), "个"))
	if err != nil || n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// yesNo 只匹配文本里明确填写的采用或存在状态。
// 参数 text 为原始是非字段。
// 返回值 yes、no 或未知时的空值。
func yesNo(text string) string {
	switch comparable(text) {
	case "是", "有", "采用", "已采用", "存在", "有单位间数据流转":
		return "yes"
	case "否", "无", "未采用", "不采用", "不存在", "不涉及", "无单位间数据流转":
		return "no"
	default:
		return ""
	}
}

// listValue 比较列表时消除顺序和常见分隔符差异。
// 参数 text 为原文中的列表或简要描述。
// 返回值 排序后的标准值，简称的图片引用返回为空。
func listValue(text string) string {
	if strings.HasPrefix(comparable(text), "见附图") || strings.HasPrefix(comparable(text), "见图") {
		return ""
	}
	values := strings.FieldsFunc(text, func(ch rune) bool { return strings.ContainsRune("、，,；;\n", ch) })
	flat := make([]string, 0, len(values))
	for _, item := range values {
		if item := comparable(item); item != "" {
			flat = append(flat, item)
		}
	}
	slices.Sort(flat)
	return strings.Join(slices.Compact(flat), "|")
}

// recipientValue 排除仅用数字占位的流转对象。
// 参数 text 为数据流转对象原文。
// 返回值 文本对象标识，不能核实时为空。
func recipientValue(text string) string {
	if _, err := strconv.Atoi(strings.TrimSpace(text)); err == nil {
		return ""
	}
	return listValue(text)
}
