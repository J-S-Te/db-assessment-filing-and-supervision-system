package filingpolicy

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const (
	Initial = "initial"
	Change  = "change"
	Cancel  = "cancel"
)

var ErrInvalid = errors.New("请核对材料规则的事项、材料类别、名称和文件名关键词")

type Rule struct {
	Stage            string `json:"stage"`
	Kind             string `json:"kind"`
	Label            string `json:"label"`
	FilenameContains string `json:"filenameContains,omitempty"`
}

type Material struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	FileName string `json:"fileName,omitempty"`
}

type Policy struct {
	Version int64  `json:"version"`
	Rules   []Rule `json:"rules"`
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Load 读取当前备案材料规则，提交事务可锁定本次使用的版本。
// 参数 ctx 为请求上下文，db 为连接池或事务，lock 为是否获取共享行锁。
// 返回值 当前配置或数据库错误。
func Load(ctx context.Context, db queryRower, lock bool) (Policy, error) {
	query := "SELECT version,rules FROM filing_material_policy WHERE singleton"
	if lock {
		query += " FOR SHARE"
	}
	var policy Policy
	err := db.QueryRow(ctx, query).Scan(&policy.Version, &policy.Rules)
	return policy, err
}

// Replace 提交校验通过的材料规则，防止覆盖其他监管席位刚保存的版本。
// 参数 ctx、db 为请求上下文和连接，expected 为已读版本，rules 为本次规则全集。
// 返回值 新配置，或无效规则、pgx.ErrNoRows 版本冲突及数据库错误。
func Replace(ctx context.Context, db queryRower, expected int64, rules []Rule) (Policy, error) {
	policy, err := normalize(rules)
	if err != nil || expected < 1 {
		return Policy{}, ErrInvalid
	}
	err = db.QueryRow(ctx, `UPDATE filing_material_policy SET version=version+1,rules=$2
		WHERE singleton AND version=$1 RETURNING version`, expected, policy.Rules).Scan(&policy.Version)
	return policy, err
}

// For 返回当前阶段使用的材料规则，保持监管配置顺序。
// 参数 stage 为首报、变更或注销。
// 返回值 固定阶段的规则切片。
func (policy Policy) For(stage string) []Rule {
	rules := make([]Rule, 0)
	for _, rule := range policy.Rules {
		if rule.Stage == stage {
			rules = append(rules, rule)
		}
	}
	return rules
}

// Match 按材料类别和可选文件名关键词寻找真实附件。
// 参数 rule 为需核对的规则，materials 为当前记录可用的材料清单。
// 返回值 可作为证明的附件标识，找不到时为空。
func Match(rule Rule, materials []Material) string {
	return evidence(rule, materials)
}

// evidence 筛选同类且符合名称的材料，保留经过校验的备案 ZIP 对基础单件的覆盖。
// 参数 rule、materials 为监管规则及已有附件。
// 返回值 首个匹配的附件标识，缺件时为空。
func evidence(rule Rule, materials []Material) string {
	for _, item := range materials {
		direct := item.Kind == rule.Kind &&
			strings.Contains(strings.ToLower(item.FileName), strings.ToLower(rule.FilenameContains))
		covered := rule.Stage == Initial && rule.FilenameContains == "" && item.Kind == "filing_package" &&
			(rule.Kind == "filing_form" || rule.Kind == "classification_report")
		if direct || covered {
			return item.ID
		}
	}
	return ""
}

// normalize 清理配置文本并校验所选阶段、材料类别及规则名称。
// 参数 input 为监管页面的提交列表。
// 返回值 规范化配置，无效或重复规则时返回 ErrInvalid。
func normalize(input []Rule) (Policy, error) {
	if len(input) > 40 {
		return Policy{}, ErrInvalid
	}
	policy := Policy{Rules: make([]Rule, 0, len(input))}
	seen := make(map[string]bool, len(input))
	for _, rule := range input {
		rule.Label = strings.TrimSpace(rule.Label)
		rule.FilenameContains = strings.TrimSpace(rule.FilenameContains)
		key := rule.Stage + ":" + strings.ToLower(rule.Label)
		if !ruleKind(rule.Stage, rule.Kind) || rule.Label == "" || seen[key] ||
			!validText(rule.Label) || !validText(rule.FilenameContains) {
			return Policy{}, ErrInvalid
		}
		seen[key] = true
		policy.Rules = append(policy.Rules, rule)
	}
	return policy, nil
}

// ruleKind 校验阶段是否支持配置的真实上传材料类别。
// 参数 stage、kind 为请求中的阶段及附件类别。
// 返回值 可在该阶段上传时为真。
func ruleKind(stage, kind string) bool {
	switch stage {
	case Initial:
		return kind == "filing_form" || kind == "classification_report" || kind == "supporting" || kind == "filing_package"
	case Change:
		return kind == "change_statement" || kind == "supporting"
	case Cancel:
		return kind == "cancellation_statement" || kind == "supporting"
	default:
		return false
	}
}

// validText 校验业务配置文本长度并拒绝控制字符。
// 参数 value 为需要保存的简短配置。
// 返回值 可稳定显示和搜索时为真。
func validText(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= 120 &&
		!strings.ContainsAny(value, "\x00\r\n")
}
