package filingaudit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ReviewCheckpoints 用途 在同一备案与规则快照内重用经过当前证据校验的审核结果。
type ReviewCheckpoints interface {
	Load(context.Context, string) (BatchResult, bool, error)
	Save(context.Context, string, BatchResult) error
	BeforeRequest(context.Context) error
}

const checkpointProtocol = "filing-precheck-evidence-v1"

// checkpointKeys 用途 按完整输入生成独立的材料及整批指纹。
// 参数 docs 为按原件位置解析的本批材料，unit 为同次读取的名录信息。
// 返回值 整批指纹、按当前材料顺序排列的单件指纹及编码错误。
func (d *DeepSeek) checkpointKeys(docs []Document, unit Unit) (string, []string, error) {
	profile, err := fingerprint(
		checkpointProtocol, d.Profile(), auditSystemPrompt, documentReviewPrompt,
		summaryReviewPrompt, Rules(), unit,
	)
	if err != nil {
		return "", nil, err
	}
	keys := make([]string, len(docs))
	for index, doc := range docs {
		keys[index], err = fingerprint("document", profile, doc)
		if err != nil {
			return "", nil, err
		}
	}
	batch, err := fingerprint("batch", profile, keys)
	return batch, keys, err
}

// fingerprint 用途 以分隔编码计算审核内容指纹，持久化仅使用摘要。
// 参数 values 为参与匹配的材料、名录或审核协议。
// 返回值 SHA-256 十六进制摘要及输入编码错误。
func fingerprint(values ...any) (string, error) {
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// reusableChecks 用途 确认检查项完整，避免复用因模型证据无效而降级的结果。
// 参数 checks 为原有证据检查完成后的逐项结论。
// 返回值 允许持久复用时返回真。
func reusableChecks(checks []Result) bool {
	if len(checks) != len(catalog) {
		return false
	}
	for index, check := range checks {
		if check.RuleID != catalog[index].ID || check.Message == invalidEvidenceMessage {
			return false
		}
	}
	return true
}
