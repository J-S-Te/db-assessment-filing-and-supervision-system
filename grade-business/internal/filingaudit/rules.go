package filingaudit

import "slices"

// Rule 保存备案内容审核表中的检查主题和可执行的判断要求。
type Rule struct {
	ID     string `json:"ruleId"`
	Title  string `json:"title"`
	Method string `json:"method"`
}

var catalog = []Rule{
	{"object_number", "定级对象编号", "核对备案表定级对象和表一的对象总数，比较材料中的对象编号。"},
	{"system_and_unit", "系统信息和单位信息", "比较备案表、定级报告和专家意见书的单位与系统名称，核对备案表单位属地、隶属关系、行政区划和可核验的统一社会信用代码。"},
	{"classification_date", "定级时间", "比较备案表、定级报告及专家意见表的定级时间，核对投入运行日期是否合理。"},
	{"seals", "报告用章", "按安全保护等级及适用要求，逐份核查备案表、定级报告、委托书等应盖章材料的用章。"},
	{"responsible_person", "主管人员", "核对专家意见表的签字及定级报告的主管人员信息。"},
	{"protection_level", "安全保护等级", "比较三份材料的安全、业务和系统保护等级，核对报告所列受侵害客体及判断依据是否贴合本对象。"},
	{"assets", "被测对象资产", "比较定级报告的平台构成、资产、网络边界与网络拓扑，结合备案表核对网络性质。"},
	{"business_description", "业务描述准确性", "比较定级报告的对象描述与备案表中的对象情况、承载业务及新技术新应用场景。"},
	{"carried_data", "承载数据准确性", "将定级报告与数据摸底调查逐项比对，核对数据类别、用途、流转对象及对外提供，区分内部系统交互和单位间流转。"},
	{"responsible_entity", "责任主体合规性", "核对责任主体和运维主体、单位及系统名称是否一致，并比较报告与备案表记载的投入运行时间。"},
	{"filing_certificate", "备案证明", "核对新技术新应用表的云计算和大数据场景，适用时比较对应的备案证明。"},
}

// Rules 返回按审核表原顺序排列的正式条目。
// 参数 无。
// 返回值 调用方可安全读取的目录副本。
func Rules() []Rule { return slices.Clone(catalog) }

// HasRule 判断提交的规则标识是否属于审核表。
// 参数 id 为规则标识。
// 返回值 属于 11 项正式规则时为真。
func HasRule(id string) bool {
	return slices.ContainsFunc(catalog, func(rule Rule) bool { return rule.ID == id })
}
