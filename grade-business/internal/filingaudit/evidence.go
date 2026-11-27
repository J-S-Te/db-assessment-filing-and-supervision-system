package filingaudit

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
)

const (
	form   = "filing_form"
	report = "classification_report"
	expert = "expert_opinion"
	survey = "data_survey"
	proof  = "filing_certificate"
)

// Document 是备案附件或其归档成员中已读取的正文。
type Document struct {
	AttachmentID string
	FileName     string
	Kind         string
	Lines        []Line
	Images       []Image
}

// Line 指向原件的物理页、文档段落或表格行。
type Line struct {
	Location string
	Text     string
}

// Unit 是业务名录中的在册责任单位。
type Unit struct {
	Name, Code, District string
}

// Evidence 返回可打开原件的定位和实际命中的文字。
type Evidence struct {
	AttachmentID string `json:"attachmentId"`
	FileName     string `json:"fileName"`
	Location     string `json:"location"`
	Quote        string `json:"quote"`
	Kind         string `json:"kind,omitempty"`
}

// Result 保存一条规则的独立结论、理由和出处。
type Result struct {
	RuleID   string     `json:"ruleId"`
	Status   string     `json:"status"`
	Message  string     `json:"message"`
	Evidence []Evidence `json:"evidence,omitempty"`
}

var headingPrefix = regexp.MustCompile(`^(?:[（(][一二三四五六七八九十0-9]+[）)]|[0-9一二三四五六七八九十]+[、.)．])\s*`)
var identityNumber = regexp.MustCompile(`[0-9]{6}(?:19|20)[0-9]{9}[0-9Xx]`)

var labels = map[string][]string{
	"object":              {"定级对象编号", "对象编号"},
	"count":               {"定级对象总数", "定级对象数量"},
	"unit":                {"单位名称", "定级单位名称", "运营使用单位名称", "备案单位名称"},
	"system":              {"系统名称", "定级对象名称", "网络名称"},
	"district":            {"行政区划", "行政区划代码", "单位属地"},
	"relation":            {"隶属关系", "单位隶属关系"},
	"credit":              {"统一社会信用代码", "单位统一社会信用代码", "社会信用代码"},
	"classification_date": {"定级时间", "定级日期"},
	"commission_date":     {"投入运行时间", "投入运行日期", "上线时间"},
	"seal":                {"用章状态", "盖章状态", "盖章核验结果", "用章核验结果"},
	"signature":           {"专家签字", "专家签名", "签字状态"},
	"person":              {"主管人员", "直接负责的主管人员", "负责人姓名"},
	"level":               {"安全保护等级", "定级对象安全保护等级", "网络安全保护等级", "系统定级级别", "保护等级"},
	"business_level":      {"业务保护等级", "业务信息安全保护等级", "业务信息保护等级"},
	"system_level":        {"系统保护等级", "系统服务安全保护等级", "系统服务保护等级"},
	"object_business":     {"业务信息侵害客体论证", "业务信息受到破坏时所侵害客体的确定"},
	"object_system":       {"系统服务侵害客体论证", "系统服务受到破坏时所侵害客体的确定"},
	"assets":              {"资产清单", "资产列表", "平台构成"},
	"topology":            {"拓扑节点", "拓扑资产", "网络拓扑资产", "网络结构拓扑图", "网络拓扑图"},
	"network":             {"网络性质"},
	"boundary":            {"网络边界", "网络边界描述"},
	"business":            {"承载业务", "业务描述", "定级对象描述", "定级对象情况"},
	"new_tech":            {"新技术新应用", "新技术新应用场景"},
	"data":                {"承载数据", "数据类别", "数据类型"},
	"sharing":             {"是否单位间数据流转", "是否存在单位间数据流转", "单位间数据流转"},
	"recipient":           {"数据流转对象", "对外提供对象", "单位间数据流转对象"},
	"responsible":         {"责任主体", "网络安全保护责任主体"},
	"maintenance":         {"运维主体", "运行维护主体"},
	"cloud":               {"云计算场景", "是否采用云计算", "采用云计算"},
	"bigdata":             {"大数据场景", "是否采用大数据", "采用大数据"},
	"cloud_name":          {"云平台名称", "云服务平台名称"},
	"bigdata_name":        {"大数据平台名称", "大数据系统名称"},
}

type value struct {
	text   string
	source Evidence
}

type entries map[string]map[string][]value

// collect 为当前材料建立只包含已填写字段的索引。
// 参数 docs 为按附件和原件位置排序的材料正文。
// 返回值 按材料类别及字段分组的原文值。
func collect(docs []Document) entries {
	fields := make(map[string]string)
	for key, names := range labels {
		for _, name := range names {
			fields[name] = key
		}
	}
	result := make(entries)
	for _, doc := range docs {
		group := result[doc.Kind]
		if group == nil {
			group = make(map[string][]value)
			result[doc.Kind] = group
		}
		for i, line := range doc.Lines {
			key, text, quote := fieldAt(doc.Lines, i, fields)
			if key == "" || !filled(text) {
				continue
			}
			e := Evidence{AttachmentID: doc.AttachmentID, FileName: doc.FileName,
				Location: line.Location, Quote: evidenceQuote(quote)}
			group[key] = append(group[key], value{strings.TrimSpace(text), e})
		}
	}
	return result
}

// fieldAt 识别带分隔符的字段及 Word 表格中分开的标签和值。
// 参数 lines、i 为原文和行位置，fields 为支持的标签。
// 返回值 字段标识、值和带上下文的原文引证。
func fieldAt(lines []Line, i int, fields map[string]string) (string, string, string) {
	text := strings.TrimSpace(lines[i].Text)
	field := headingPrefix.ReplaceAllString(text, "")
	if !filled(field) {
		return "", "", ""
	}
	separator := strings.IndexAny(field, "：:\t")
	if separator < 0 {
		separator = strings.Index(field, "  ")
	}
	if separator >= 0 {
		key := fields[strings.TrimSpace(field[:separator])]
		width := 1
		if strings.HasPrefix(field[separator:], "：") {
			width = len("：")
		}
		raw := strings.TrimSpace(field[separator+width:])
		if key != "" && raw != "" {
			return key, raw, text
		}
		field = field[:separator]
	}
	key := fields[strings.TrimSpace(field)]
	if key == "" || i+1 >= len(lines) {
		return "", "", ""
	}
	next := strings.TrimSpace(lines[i+1].Text)
	nextLabel, _, _ := strings.Cut(headingPrefix.ReplaceAllString(next, ""), "：")
	if fields[nextLabel] != "" || fields[strings.SplitN(next, ":", 2)[0]] != "" ||
		!filled(next) || strings.HasPrefix(next, "表") {
		return "", "", ""
	}
	return key, next, text + " " + next
}

// filled 过滤空白、模板占位和说明段落。
// 参数 text 为原始标签、值或候选段落。
// 返回值 有值得核对的正文时为真。
func filled(text string) bool {
	value := strings.ToUpper(strings.Trim(text, " \t　_—-□【】"))
	return value != "" && !strings.Contains(value, "XXX") &&
		!strings.HasPrefix(value, "填写说明") && !strings.HasPrefix(value, "请填写") &&
		!strings.HasPrefix(value, "请选择") && !strings.HasPrefix(value, "示例")
}

// evidenceQuote 限制展示原文长度并隐去完整身份证号码。
// 参数 text 为从原件获取的真实文本。
// 返回值 可展示的有界证据摘录。
func evidenceQuote(text string) string {
	text = maskPersonalData(text)
	characters := []rune(strings.Join(strings.Fields(text), " "))
	if len(characters) > 240 {
		return string(characters[:240]) + "…"
	}
	return string(characters)
}

// comparable 归一化版式空白和标点，保留实际字词。
// 参数 text 为参与比较的文档值。
// 返回值 用于确定性一致性比对的字符串。
func comparable(text string) string {
	return strings.Map(func(ch rune) rune {
		if unicode.IsSpace(ch) || unicode.IsPunct(ch) || unicode.Is(unicode.Cf, ch) {
			return -1
		}
		if ch >= '０' && ch <= '９' {
			return ch - '０' + '0'
		}
		return unicode.ToLower(ch)
	}, text)
}

// valuesFor 读取同一组字段可解析的去重值，并携带实际证据。
// 参数 values 为匹配条目，normalize 为当前字段的归一化逻辑。
// 返回值 可比较值及支撑它们的材料。
func valuesFor(values []value, normalize func(string) string) ([]string, []Evidence) {
	var matched []string
	var evidence []Evidence
	for _, item := range values {
		key := normalize(item.text)
		if key == "" {
			continue
		}
		if !slices.Contains(matched, key) {
			matched = append(matched, key)
		}
		evidence = append(evidence, item.source)
	}
	return matched, evidence
}
