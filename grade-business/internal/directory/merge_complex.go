package directory

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/reviewinbox"
)

const maxMergeImpacts = 600

type mergeImpact struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	FilingID    string `json:"filingId,omitempty"`
	Number      string `json:"number"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	SourceName  string `json:"sourceName"`
	Disposition string `json:"disposition"`
	Reason      string `json:"reason"`
	Version     int64  `json:"version"`
}

type impactRule struct {
	disposition string
	message     string
}

var mergeImpactRules = map[string]impactRule{
	"expiry_already_handed_off":      {"blocked", "该登记已有一次续办移交。先由监管结束当前登记，再核验符合新归属的原件并重新登记。"},
	"alert_retained":                 {"preserve", "已关闭预警、处置记录和原单位历史范围保持原样。"},
	"alert_open":                     {"blocked", "先完成或关闭预警及相关处置审批。"},
	"coverage_transfer":              {"transfer", "在册对象与现行备案关联交由主单位承接。"},
	"coverage_retained":              {"preserve", "已停用清册保留原归属与留痕。"},
	"legacy_scope_conflict":          {"blocked", "先核对并修正历史单位名称与目录归属。"},
	"filing_open":                    {"blocked", "先办结、撤回或依法处置未完成备案。"},
	"filing_review_transfer":         {"transfer", "主单位承接在审首报，原审核机构、材料与历史回执继续保留。"},
	"system_name_conflict":           {"blocked", "先核对同名系统的实际归属和备案编号。"},
	"filing_retained":                {"preserve", "已注销或撤回的备案、原件和历史留在原单位。"},
	"filing_withdrawal_open":         {"blocked", "先处理待确认的备案撤回申请。"},
	"report_open":                    {"blocked", "先完成报告审核或撤回，避免变更在途审核的单位范围。"},
	"report_retained":                {"preserve", "原报告、机构提交范围和审核快照继续保留。"},
	"implementation_open":            {"blocked", "先办结或撤回实施报备，保留原联系人与办理范围。"},
	"implementation_retained":        {"preserve", "原报备作者、联系人和运营者历史访问范围继续保留。"},
	"filing_application_open":        {"blocked", "先办结或撤回备案变更或注销申请。"},
	"filing_application_retained":    {"preserve", "原变更或注销申请及其历史收件范围继续保留。"},
	"remediation_transfer":           {"transfer", "未提交且没有材料的整改草稿由主单位继续编制。"},
	"remediation_retained":           {"preserve", "已办结整改规划及原件、审核、回执留在原单位。"},
	"remediation_open":               {"blocked", "先办结或撤回已有材料、提交或补正记录的整改规划。"},
	"hazard_transfer":                {"transfer", "主单位接续当前隐患，原证据、审核、回执及送达保留在原范围。"},
	"hazard_retained":                {"preserve", "办结隐患的证据、作者与原运营者范围继续保留。"},
	"hazard_open":                    {"blocked", "存在关联预警或无法接续的状态，须先核对处置与原收件范围。"},
	"material_issue_open":            {"blocked", "先关闭材料异常并确认实际恢复结果。"},
	"material_issue_retained":        {"preserve", "已关闭材料异常的处理记录和原收件范围继续保留。"},
	"supervision_transfer":           {"transfer", "监督任务未正式下发，主单位将在下发后接收任务。"},
	"supervision_issued_transfer":    {"transfer", "主单位接续已下发的监督任务，原范围保留历史材料与送达。"},
	"supervision_retained":           {"preserve", "原监督送达、现场材料及已办快照继续留在原收件范围。"},
	"supervision_open":               {"blocked", "先核对关联预警、原归属或尚不能承接的监督状态。"},
	"supervision_already_handed_off": {"blocked", "监督任务已有原收件快照，须先办结当前监督和现场工作。"},
	"supervision_site_open":          {"blocked", "现场检查随所属监督任务办理，须先核对其承接条件。"},
	"supervision_site_transfer":      {"transfer", "主单位接续现场检查，原记录和证据保留原范围。"},
	"supervision_site_retained":      {"preserve", "现场检查的证据和经办历史继续留在原收件范围。"},
	"expiry_transfer":                {"transfer", "当前有效期及续办责任移交主单位，历史登记依据和原件保留。"},
	"expiry_open":                    {"blocked", "先处理续传审核；已有续传历史或旧归属不符的记录需由监管核对后结束登记。"},
	"expiry_retained":                {"preserve", "已结束有效期登记、原件和续传证据保留原收件范围。"},
}

// addFilingRule 对同一备案规则保留简单模式提示，并逐笔记录复杂模式的首报接续与阻塞。
// 参数 plan 为当前计划，item 为来源备案，rule 为命中的规则标识。
// 返回值 无。
func addFilingRule(plan *mergePlan, item filingMergeCandidate, rule string) {
	if plan.Mode != complexMerge {
		message := map[string]string{
			"legacy_scope_conflict": "存在与目录标识不一致的历史单位名称，需先核对归属。",
			"filing_open":           "来源单位仍有未完成的备案草稿、补正或在审记录。",
			"system_name_conflict":  "主从范围中存在同名系统，请核对原备案后再合并。",
		}[rule]
		plan.block(rule, message, 1)
		return
	}
	addMergeImpact(plan, rule, mergeImpact{Kind: "filing", ID: item.ID, FilingID: item.ID,
		Number: item.Number, Title: item.SystemName, Status: item.Status,
		SourceName: item.SourceName, Version: item.Version})
}

// addMergeImpact 用统一的逐笔规则同时生成监管说明与确认阻塞。
// 参数 plan 是待生成的计划，rule 为规则标识，item 是待核对的真实业务记录。
// 返回值 无。
func addMergeImpact(plan *mergePlan, rule string, item mergeImpact) {
	classification := mergeImpactRules[rule]
	item.Disposition, item.Reason = classification.disposition, classification.message
	plan.Impacts = append(plan.Impacts, item)
	if item.Disposition == "blocked" {
		plan.block(rule, item.Reason, 1)
	}
}

// addComplexDependencies 枚举当前备案关联事项和明确保留的来源历史。
// 参数 ctx、db 控制一致性查询，input 提供来源，plan 返回逐笔事项及阻塞。
// 返回值 查询失败时返回错误，不完整的超限清单不产生确认令牌。
func addComplexDependencies(ctx context.Context, db reader, input mergeInput, plan *mergePlan) error {
	rows, err := db.Query(ctx, complexDependenciesSQL, moveIDs(plan.Moves), input.SourceIDs, maxMergeImpacts+1)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item mergeImpact
		var rule string
		err := rows.Scan(&item.Kind, &item.ID, &item.FilingID, &item.Number, &item.Title,
			&item.Status, &item.SourceName, &item.Version, &rule)
		if err != nil {
			return err
		}
		if _, ok := mergeImpactRules[rule]; !ok {
			return fmt.Errorf("未知的复杂归并事项规则 %q", rule)
		}
		if len(plan.Impacts) >= maxMergeImpacts {
			plan.block("impact_batch_limit", "本批次受影响事项超过 600 条，请缩小归并范围后重试。", 1)
			break
		}
		addMergeImpact(plan, rule, item)
	}
	return rows.Err()
}

const complexDependenciesSQL = `
WITH moved AS (
    SELECT id,number,system_name,organization_name FROM business_cases WHERE id::text=ANY($1::text[])
), reports AS (
    SELECT r.id,r.number,r.status,r.version,f.id AS filing_id,f.system_name,f.organization_name
    FROM business_cases r JOIN moved f ON f.id=r.filing_id WHERE r.kind='report' AND r.discarded_at IS NULL
), hazards AS (
    SELECT h.*,f.id AS filing_id,f.number,f.system_name,hc.organization_name
    FROM hazard_items h JOIN hazard_context hc ON hc.id=h.id JOIN moved f ON f.id=hc.filing_id
), supervisions AS (
    SELECT t.*,f.number AS current_filing_number,s.status AS site_status,s.version AS site_version,
        CASE
            WHEN t.status IN ('approved','cancelled')
              AND (s.status IS NULL OR s.status IN ('closed','cancelled')) THEN 'supervision_retained'
            WHEN t.lead_alert_id IS NOT NULL OR t.organization_name COLLATE "C"<>f.organization_name
              THEN 'supervision_open'
            WHEN t.merge_history_scope IS NOT NULL OR s.merge_history_version IS NOT NULL
              THEN 'supervision_already_handed_off'
            WHEN t.status='draft' AND t.published_at IS NULL AND s.status IS NULL THEN 'supervision_transfer'
            WHEN t.status NOT IN ('draft','cancelled') AND t.published_at IS NOT NULL
              THEN 'supervision_issued_transfer'
            ELSE 'supervision_open'
        END AS handoff_rule
    FROM supervision_tasks t JOIN moved f ON f.id=t.filing_id
    LEFT JOIN supervision_site_inspections s ON s.task_id=t.id
), dependencies(kind,id,filing_id,number,title,status,organization,version,rule) AS (
    SELECT 'filing',f.id,f.id,f.number,f.system_name,
        CASE WHEN f.cancelled_at IS NULL THEN f.status ELSE 'cancelled' END,
        f.organization_name,f.version,'filing_retained'
    FROM business_cases f WHERE f.kind='filing' AND f.discarded_at IS NULL
      AND (f.cancelled_at IS NOT NULL OR f.status='withdrawn') AND EXISTS (
        SELECT 1 FROM directory_parties u WHERE u.id::text=ANY($2::text[]) AND
         (f.directory_unit_id=u.id OR lower(btrim(f.organization_name)) IN (lower(u.name),lower(u.scope_name)))
    )
    UNION ALL
    SELECT 'filing-withdrawal',w.id,f.id,f.number,'备案撤回申请',w.status,f.organization_name,w.case_version,
        'filing_withdrawal_open'
    FROM filing_withdrawal_requests w JOIN moved f ON f.id=w.filing_id WHERE w.status='pending'
    UNION ALL
    SELECT 'report',r.id,r.filing_id,r.number,r.system_name,r.status,r.organization_name,r.version,
        CASE WHEN r.status='approved' THEN 'report_retained' ELSE 'report_open' END FROM reports r
    UNION ALL
    SELECT 'implementation',i.id,f.id,f.number,
        CASE i.report_type WHEN 'change' THEN '变更实施报备' ELSE '常规实施报备' END,
        i.status,COALESCE(i.frozen_organization_name,f.organization_name),i.version,
        CASE WHEN i.status IN ('approved','withdrawn') THEN 'implementation_retained'
             ELSE 'implementation_open' END
    FROM implementation_reports i JOIN moved f ON f.id=i.filing_id
    UNION ALL
    SELECT 'filing-application',a.id,f.id,f.number,
        CASE a.application_type WHEN 'change' THEN '备案变更' ELSE '备案注销' END,
        a.status,COALESCE(a.before_values->>'organizationName',f.organization_name),a.version,
        CASE WHEN a.status IN ('approved','withdrawn') THEN 'filing_application_retained'
             ELSE 'filing_application_open' END
    FROM filing_applications a JOIN moved f ON f.id=a.filing_id
    UNION ALL
    SELECT 'remediation',p.id,r.filing_id,r.number,'整改规划',p.status,COALESCE(p.frozen_organization_name,r.organization_name),p.version,
        CASE WHEN p.frozen_organization_name IS NOT NULL OR p.status IN ('approved','withdrawn')
             THEN 'remediation_retained'
             WHEN p.status='draft' AND NOT EXISTS (
                 SELECT 1 FROM remediation_attachments a WHERE a.plan_id=p.id)
             THEN 'remediation_transfer' ELSE 'remediation_open' END
    FROM remediation_plans p JOIN reports r ON r.id=p.report_id
    UNION ALL
    SELECT 'hazard',h.id,h.filing_id,h.number,COALESCE(h.title,'整改计划隐患'),
        h.status,h.organization_name,h.version,
        CASE WHEN h.status='closed' THEN 'hazard_retained'
             WHEN EXISTS (SELECT 1 FROM local_alerts a WHERE a.hazard_id=h.id) THEN 'hazard_open'
             WHEN h.status IN ('pending_dispatch','open','returned','pending_retest','pending_review')
             THEN 'hazard_transfer' ELSE 'hazard_open' END
    FROM hazards h
    UNION ALL
    SELECT 'alert',a.id,h.filing_id,h.number,'整改风险预警',a.status,h.organization_name,a.version,
        CASE WHEN a.status='closed' THEN 'alert_retained' ELSE 'alert_open' END
    FROM local_alerts a JOIN hazards h ON h.id=a.hazard_id
    UNION ALL
    SELECT 'coverage',c.id,c.filing_id,c.reference,c.system_name,
        CASE WHEN c.active THEN 'active' ELSE 'inactive' END,u.name,c.version,
        CASE WHEN c.active THEN 'coverage_transfer' ELSE 'coverage_retained' END
    FROM archive_coverage_targets c JOIN directory_parties u ON u.id=c.unit_id
    WHERE c.unit_id::text=ANY($2::text[])
    UNION ALL
    SELECT 'material-issue',m.id,f.id,f.number,m.material_kind,
        CASE WHEN m.closed_at IS NULL THEN 'open' ELSE 'closed' END,m.organization_name,m.sequence,
        CASE WHEN m.closed_at IS NULL THEN 'material_issue_open' ELSE 'material_issue_retained' END
    FROM material_issues m LEFT JOIN reports r ON r.id=m.case_id JOIN moved f ON f.id=COALESCE(r.filing_id,m.case_id)
    UNION ALL
    SELECT 'supervision',t.id,t.filing_id,t.current_filing_number,t.title,t.status,t.organization_name,
        t.version,t.handoff_rule FROM supervisions t
    UNION ALL
    SELECT 'supervision-site',t.id,t.filing_id,t.current_filing_number,t.title,t.site_status,t.organization_name,
        t.site_version,CASE WHEN t.site_status IN ('closed','cancelled') THEN 'supervision_site_retained'
             WHEN t.handoff_rule='supervision_issued_transfer' THEN 'supervision_site_transfer'
             WHEN t.handoff_rule='supervision_already_handed_off' THEN t.handoff_rule
             ELSE 'supervision_site_open' END
    FROM supervisions t WHERE t.site_status IS NOT NULL
    UNION ALL
    SELECT 'material-expiry',e.id,f.id,f.number,'材料有效期',s.date_status,
        s.organization_name,e.version,
        CASE WHEN e.retired_at IS NOT NULL THEN 'expiry_retained'
             WHEN e.current_organization_name IS NOT NULL THEN 'expiry_already_handed_off'
             WHEN s.scope_current AND NOT EXISTS (
               SELECT 1 FROM material_expiry_renewals rn WHERE rn.record_id=e.id)
             THEN 'expiry_transfer' ELSE 'expiry_open' END
    FROM material_expiry_records e JOIN moved f ON f.id=e.filing_id JOIN material_expiry_status s ON s.id=e.id
)
SELECT kind,id::text,COALESCE(filing_id::text,''),number,title,status,organization,version,rule
FROM dependencies ORDER BY kind,id,rule LIMIT $3
`

// recordComplexMergeImpact 更新可交接责任、封存历史访问范围并保留会发出原件的原账户。
// 参数 ctx、tx 为原子确认环境，plan 是已复核预览，mergeID 是本次确认的正式标识。
// 返回值 归属变更失败或影响范围变化时返回错误。
func recordComplexMergeImpact(ctx context.Context, tx pgx.Tx, plan mergePlan, mergeID string) error {
	batch := &pgx.Batch{}
	filings := moveIDs(plan.Moves)
	batch.Queue(`UPDATE case_attachments a SET merge_history_scope=f.organization_name
		FROM business_cases f WHERE f.id=a.case_id AND f.id::text=ANY($1::text[])
		AND a.merge_history_scope IS NULL`, filings)
	batch.Queue(`UPDATE case_review_events e SET merge_history_scope=f.organization_name,
		merge_history_number=f.number,merge_history_system_name=f.system_name
		FROM business_cases f WHERE f.id=e.case_id AND f.id::text=ANY($1::text[])
		AND e.merge_history_scope IS NULL`, filings)
	batch.Queue(freezeApprovedReportHistory, filings, mergeID)
	queueComplexDependencies(batch, plan)
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return err
	}
	return deliverComplexHandoffs(ctx, tx, plan)
}

// deliverComplexHandoffs 向新承办单位登记接续消息，保留原送达和签收。
// 参数 ctx、tx 为事务上下文，plan 为已核对的实际归并计划。
// 返回值 写入成功为 nil，发现错误时由调用方原子回滚。
func deliverComplexHandoffs(ctx context.Context, tx pgx.Tx, plan mergePlan) error {
	for _, item := range plan.Impacts {
		if item.Disposition != "transfer" {
			continue
		}
		notice := reviewinbox.Notice{Audience: "operator", ResourceID: item.ID, Version: item.Version + 1,
			Recipient: plan.Target.ScopeName, Reference: item.Number, Subject: item.Title}
		switch {
		case item.Kind == "filing":
			notice.Resource, notice.State = "filings", "merge_handoff"
			notice.Comment = "单位归并后，请查看本项首报的办理进度。"
		case item.Kind == "hazard" && item.Status != "pending_dispatch":
			notice.Resource, notice.State = "hazards", item.Status
			notice.Comment = "单位归并后，请接续本项隐患并查看当前办理进度。"
		case item.Kind == "supervision" && item.Status != "draft":
			notice.Resource, notice.State = "supervisions", "merge_handoff"
			notice.Comment = "单位归并后，请查看本项监督任务及现场检查的办理进度。"
		default:
			continue
		}
		if err := reviewinbox.Deliver(ctx, tx, notice); err != nil {
			return err
		}
	}
	return nil
}

// queueComplexDependencies 根据逐笔预览安排历史范围封存及办理版本迁移。
// 参数 batch 为同一归并事务的写入序列，plan 提供复核后的目标与实际事项。
// 返回值 无。
func queueComplexDependencies(batch *pgx.Batch, plan mergePlan) {
	for _, item := range plan.Impacts {
		switch {
		case item.Kind == "supervision" && item.Disposition == "transfer":
			queueSupervisionHandoff(batch, item, plan.Target.ScopeName)
		case item.Kind == "material-expiry" && item.Disposition == "transfer":
			batch.Queue(`UPDATE material_expiry_records SET current_organization_name=$2,
				handoff_version=version+1,handoff_valid_until=valid_until,handoff_cycle=cycle,
				version=version+1 WHERE id=$1`,
				item.ID, plan.Target.ScopeName)
		case item.Kind == "material-expiry" && item.Disposition == "preserve":
			batch.Queue(`UPDATE material_expiry_records e SET keep_retired_scope=true FROM material_expiry_status s
				WHERE e.id=$1 AND s.id=e.id AND s.scope_current`, item.ID)
		case item.Kind == "remediation" && item.Disposition == "preserve":
			batch.Queue(`UPDATE remediation_plans p SET frozen_organization_name=$2,
				frozen_filing_number=f.number,frozen_system_name=f.system_name FROM business_cases f
				WHERE p.id=$1 AND f.id=$3 AND p.frozen_organization_name IS NULL`,
				item.ID, item.SourceName, item.FilingID)
		case item.Kind == "remediation" && item.Disposition == "transfer":
			batch.Queue(`UPDATE remediation_plans SET version=version+1 WHERE id=$1`, item.ID)
		case item.Kind == "implementation" && item.Disposition == "preserve":
			batch.Queue(`UPDATE implementation_reports SET frozen_organization_name=$2
				WHERE id=$1 AND frozen_organization_name IS NULL`, item.ID, item.SourceName)
		case item.Kind == "hazard" && item.Disposition == "preserve":
			batch.Queue(`UPDATE hazard_items h SET frozen_organization_name=$2,
				frozen_filing_number=f.number,frozen_system_name=f.system_name FROM business_cases f
				WHERE h.id=$1 AND f.id=$3 AND h.frozen_organization_name IS NULL`,
				item.ID, item.SourceName, item.FilingID)
		case item.Kind == "hazard" && item.Disposition == "transfer":
			queueHazardHandoff(batch, item)
		}
	}
}

// queueSupervisionHandoff 移交年度任务与现场检查，冻结已获送达的原收件版本及状态。
// 参数 batch 为归并事务的批量写入，item、targetScope 为原监督任务及承接范围。
// 返回值 无。
func queueSupervisionHandoff(batch *pgx.Batch, item mergeImpact, targetScope string) {
	if item.Status == "draft" {
		batch.Queue(`UPDATE supervision_tasks SET organization_name=$2,version=version+1,
			updated_at=clock_timestamp() WHERE id=$1`, item.ID, targetScope)
		return
	}
	batch.Queue(`UPDATE supervision_tasks SET organization_name=$2,
		merge_history_scope=organization_name,merge_history_version=version,
		merge_history_snapshot=jsonb_build_object('status',status,'submitted_at',submitted_at,
			'submitted_version',submitted_version,'return_version',return_version,'correction_kind',correction_kind),
		version=version+1,updated_at=clock_timestamp() WHERE id=$1`, item.ID, targetScope)
	batch.Queue(`UPDATE supervision_site_inspections SET merge_history_version=version,
		merge_history_snapshot=jsonb_build_object('status',status,'visit_at',visit_at,'location',location,
			'inspectors',inspectors,'outcome',outcome,'follow_up_due_on',follow_up_due_on),
		version=version+1 WHERE task_id=$1`, item.ID)
}

// queueHazardHandoff 保留原单位证据及回执快照，并划定承接方整改版本起点。
// 参数 batch 为归并事务内的批量写入，item 为已核对的隐患及原备案。
// 返回值 无。
func queueHazardHandoff(batch *pgx.Batch, item mergeImpact) {
	batch.Queue(`UPDATE hazard_attachments SET merge_history_scope=$2
		WHERE hazard_id=$1 AND merge_history_scope IS NULL`, item.ID, item.SourceName)
	batch.Queue(`UPDATE hazard_events e SET merge_history_scope=$2,
		merge_history_number=f.number,merge_history_system_name=f.system_name
		FROM business_cases f WHERE e.hazard_id=$1 AND e.merge_history_scope IS NULL AND f.id=$3`,
		item.ID, item.SourceName, item.FilingID)
	batch.Queue(`UPDATE hazard_items SET version=version+1,handoff_version=version+1 WHERE id=$1`, item.ID)
}

// 仅封存原单位在本次归并生效前已经具备的正式报告查看范围。
const freezeApprovedReportHistory = `
	UPDATE business_cases r SET merge_history_scope=f.organization_name,
		merge_history_number=f.number,merge_history_system_name=f.system_name
	FROM business_cases f
	WHERE f.id::text=ANY($1::text[]) AND f.status='approved' AND r.filing_id=f.id
	  AND r.kind='report' AND r.status='approved' AND r.discarded_at IS NULL
	  AND r.merge_history_scope IS NULL
	  AND COALESCE(NULLIF(r.directory_snapshot#>>'{unit,scopeName}',''),
	    NULLIF(r.organization_name,''),f.organization_name) COLLATE "C"=f.organization_name
	  AND (r.directory_unit_id IS NULL OR f.directory_unit_id IS NULL
	    OR r.directory_unit_id=f.directory_unit_id)
	  AND NOT EXISTS (
	    SELECT 1 FROM filing_applications a
	    WHERE a.filing_id=f.id AND a.application_type='change' AND a.status='approved'
	      AND a.applied_at>=COALESCE(r.submitted_at,r.created_at)
	      AND a.before_values->>'organizationName' IS DISTINCT FROM a.after_values->>'organizationName')
	  AND NOT EXISTS (
	    SELECT 1 FROM directory_unit_merges m
	    WHERE m.id<>$2 AND m.merged_at>=COALESCE(r.submitted_at,r.created_at)
	      AND m.plan->'moves' @> jsonb_build_array(jsonb_build_object('id',f.id::text)))
`
