package implementations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/directoryref"
)

type filingMaterialRule struct {
	kind  string
	label string
}

const filingPackageKind = "filing_package"

// filingMaterialRules 列出备案既有最低材料分类，供关联备案核对使用。
var filingMaterialRules = []filingMaterialRule{
	{kind: "filing_form", label: "备案表"},
	{kind: "classification_report", label: "定级报告"},
}

// formPrecheck 核对已保存报备的必填字段和计划日期。
// 参数 record 为已加载的报备数据快照。
// 返回值 与联系人和计划内容及日期对应的规则结果。
func formPrecheck(record storedRecord) []precheckFinding {
	return []precheckFinding{
		checkResult("report_fields", strings.TrimSpace(record.Reporter) != "" && strings.TrimSpace(record.Plan) != "",
			"已填写报备人员和实施计划。", "请补充报备人员或实施计划。"),
		checkResult("planned_dates", validPlannedDates(record.PlannedStart, record.PlannedEnd),
			"计划起止日期有效，完成日期不早于开始日期。", "请核对计划起止日期和先后顺序。"),
	}
}

// checkResult 将确定性判断转换为带有可读信息的辅助规则结果。
// 参数 id 为规则标识，matches 为核对结论，passed、warning 为对应提示。
// 返回值 通过或待核对提示。
func checkResult(id string, matches bool, passed, warning string) precheckFinding {
	if matches {
		return precheckFinding{RuleID: id, Status: "pass", Message: passed}
	}
	return precheckFinding{RuleID: id, Status: "warning", Message: warning}
}

// checkImplementationDirectory 按当前名录及送审规则核对归属和可用资质。
// 参数 ctx、tx 为当前读取快照，record 为报备中保存的机构和人员。
// 返回值 名录核对提示，或读取名录时的数据库错误。
func checkImplementationDirectory(ctx context.Context, tx pgx.Tx, record storedRecord) (precheckFinding, error) {
	const rule = "directory"
	snapshot, err := directoryref.Inspect(ctx, tx, record.OrganizationName, record.AssessmentOrg,
		reporterIdentifier(record), !operatorInitiated(record))
	if errors.Is(err, directoryref.ErrUnavailable) || errors.Is(err, directoryref.ErrPersonRequired) ||
		errors.Is(err, directoryref.ErrPersonInvalid) {
		return checkResult(rule, false, "", err.Error()), nil
	}
	if err != nil {
		return precheckFinding{}, err
	}
	if snapshot == nil || snapshot.Institution == nil {
		return precheckFinding{RuleID: rule, Status: "unavailable", Message: "当前名录无可核对的测评机构。"}, nil
	}
	return checkResult(rule, true, "当前测评机构及所选报备人员符合名录校验。", ""), nil
}

// reporterIdentifier 读取已保存的名录人员标识，兼容历史报备未选名录人员。
// 参数 record 为已加载的报备记录。
// 返回值 人员标识或空值。
func reporterIdentifier(record storedRecord) string {
	if record.ReporterID != nil {
		return *record.ReporterID
	}
	return ""
}

// filingPrecheck 核对当前备案、报备快照、变更申报规则及材料证据。
// 参数 ctx、tx 为同一读取快照，record 和 filing 为当前报备与关联备案。
// 返回值 备案和材料的辅助核对结果或查询错误。
func (s *Service) filingPrecheck(ctx context.Context, tx pgx.Tx, record storedRecord,
	filing precheckFiling,
) ([]precheckFinding, error) {
	identityMatches := record.FilingNumber == filing.Number && record.SystemName == filing.SystemName &&
		record.OrganizationName == filing.OrganizationName
	findings := []precheckFinding{
		checkResult("filing_registration", filing.Active, "关联备案已通过且有效。", "请核对关联备案是否仍然有效。"),
		checkResult("filing_identity", identityMatches, "备案编号、系统名称、所属单位与当前备案一致。",
			"备案编号、系统名称或所属单位与当前备案存在差异，请核对报备时的记录。"),
	}
	if operatorInitiated(record) && record.Status != approved && record.Status != withdrawn {
		targetValid := validateOperatorChange(record.Type, record.AssessmentOrg, filing.choice) == nil
		findings = append(findings, checkResult("assessment_org", targetValid,
			"拟换入测评机构与当前备案机构不同。", "请核对换机构复测的报备类型和拟换入测评机构。"))
	}
	if record.Type == change {
		quotaFinding, err := s.quotaPrecheck(ctx, tx, record)
		if err != nil {
			return nil, err
		}
		findings = append(findings, quotaFinding)
	}
	materials, err := readPrecheckMaterials(ctx, tx, filing)
	return append(findings, materials...), err
}

// quotaPrecheck 核对上海年度内的实时额度，区分首次提交和已有额度占用。
// 参数 ctx、reader 为读取快照，record 为待核对的变更报备。
// 返回值 当前额度提示或查询错误。
func (s *Service) quotaPrecheck(ctx context.Context, reader rowReader, record storedRecord) (precheckFinding, error) {
	year := quotaYear(s.now())
	if record.QuotaYear != nil {
		year = *record.QuotaYear
	}
	used, err := countChanges(ctx, reader, record.FilingID, year)
	if err != nil {
		return precheckFinding{}, err
	}
	available := used < quotaLimit || (record.QuotaYear != nil && record.Status != withdrawn)
	message := fmt.Sprintf("%d 年度已占用 %d/%d 次。", year, used, quotaLimit)
	if record.QuotaYear != nil && record.Status != withdrawn {
		message += "本报备已占用额度。"
	} else if !available {
		message += "当前无可用额度。"
	}
	return checkResult("change_quota", available, message, message), nil
}

// readPrecheckMaterials 从可下载的备案附件选取单件，缺项时使用已校验的备案包。
// 参数 ctx、tx 为已完成备案归属校验的读取快照，filing 为关联备案及当前归属。
// 返回值 备案两类最低材料的齐备情况，或数据库错误。
func readPrecheckMaterials(ctx context.Context, tx pgx.Tx, filing precheckFiling) ([]precheckFinding, error) {
	kinds := []string{filingPackageKind}
	for _, rule := range filingMaterialRules {
		kinds = append(kinds, rule.kind)
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT ON (kind) kind,id::text,file_name
	  FROM case_attachments WHERE case_id=$1 AND kind=ANY($2::text[])
	  AND ($3::text='' OR COALESCE(merge_history_scope,$4::text)=$3)
	  ORDER BY kind,uploaded_at DESC,id DESC`, filing.ID, kinds,
		operatorOrganization(ctx), filing.OrganizationName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	materials := make(map[string]precheckEvidence, len(kinds))
	for rows.Next() {
		var kind string
		var material precheckEvidence
		if err := rows.Scan(&kind, &material.AttachmentID, &material.FileName); err != nil {
			return nil, err
		}
		materials[kind] = material
	}
	findings := make([]precheckFinding, 0, len(filingMaterialRules))
	for _, rule := range filingMaterialRules {
		material, exists := materials[rule.kind]
		message := "关联备案已登记" + rule.label + "。"
		if !exists {
			material, exists = materials[filingPackageKind]
			message = "关联备案已登记包含" + rule.label + "的已校验备案包。"
		}
		finding := checkResult(rule.kind, exists, message, "当前范围未找到关联备案的"+rule.label+"。")
		if exists {
			finding.Evidence = &material
		}
		findings = append(findings, finding)
	}
	return findings, rows.Err()
}
