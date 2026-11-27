package alerts

import "context"

type ruleSweep struct {
	Matched  int64 `json:"matched"`
	Notified int64 `json:"notified"`
}

type ruleHit struct {
	Resource         string `json:"resource"`
	ResourceID       string `json:"resourceId"`
	Reference        string `json:"reference"`
	SystemName       string `json:"systemName"`
	OrganizationName string `json:"organizationName"`
	Audience         string `json:"audience"`
	Recipient        string `json:"recipient"`
	Deadline         string `json:"deadline"`
}

// 预览与投递共用当前有效的业务来源，投递时对真实来源加共享锁。
const hazardRuleSources = `
	SELECT 'alerts'::text AS resource,a.id AS resource_id,1::bigint AS stage_version,a.kind AS trigger,
	       'pending'::text AS notice_state,f.id AS filing_id,f.number AS reference,
	       f.system_name,f.organization_name,` + lockedAlertInstitution + ` AS institution,h.deadline
	FROM local_alerts a` + alertSourceJoins + `
	JOIN business_cases anchor ON anchor.id=COALESCE(hc.report_id,hc.filing_id)
	WHERE a.status<>'closed' AND h.status<>'closed' AND f.status='approved' AND f.cancelled_at IS NULL
	  AND (a.kind='high_risk' AND h.severity='high' OR a.kind='overdue' AND h.deadline<` + alertBusinessDateSQL + `)
	  AND EXISTS (SELECT 1 FROM policies p WHERE p.trigger_kind=a.kind)
`

const filingRuleSources = `
	SELECT 'filings',f.id,w.stage_version,'filing_review_overdue','deadline_overdue',
	       f.id,w.number,w.system_name,w.organization,w.institution,d.due
	FROM workflow_tasks w JOIN business_cases f ON w.resource='filings' AND f.id=w.id
	CROSS JOIN LATERAL (SELECT workflow_due((w.stage_at AT TIME ZONE 'Asia/Shanghai')::date,
		CASE w.window_kind WHEN 'filing_initial' THEN $1::int WHEN 'filing_final' THEN $2::int END) AS due) d
	WHERE d.due<` + alertBusinessDateSQL + `
	  AND EXISTS (SELECT 1 FROM policies p WHERE p.trigger_kind='filing_review_overdue')
`

const ruleCandidateJoin = `
), candidates AS (
	SELECT p.id AS rule_id,p.version AS rule_version,p.name AS rule_name,
	       s.resource,s.resource_id,s.stage_version,s.notice_state,s.reference,s.system_name,
	       s.organization_name,p.audience,r.recipient,s.deadline
	FROM sources s JOIN policies p ON p.trigger_kind=s.trigger
	JOIN business_cases f ON f.id=s.filing_id
	LEFT JOIN directory_parties u ON u.kind='unit' AND u.status='active' AND u.merged_into_id IS NULL
	  AND (u.id=f.directory_unit_id OR (f.directory_unit_id IS NULL AND u.scope_name COLLATE "C"=s.organization_name))
	CROSS JOIN LATERAL (SELECT CASE p.audience WHEN 'operator' THEN s.organization_name
		WHEN 'institution' THEN s.institution ELSE '监管端' END AS recipient) r
	WHERE btrim(r.recipient)<>'' AND length(r.recipient)<=200
	  AND (p.target_unit_id IS NULL OR p.target_unit_id=u.id)
	  AND (p.district='' OR p.district COLLATE "C"=u.district)
	  AND (p.level='' OR p.level=f.level)
)`

const rulePreviewSQL = `
WITH policies AS (
	SELECT NULL::uuid AS id,0::bigint AS version,''::text AS name,$3::text AS trigger_kind,
	       $4::text AS audience,NULLIF($5,'')::uuid AS target_unit_id,$6::text AS district,$7::text AS level
),sources AS (` + hazardRuleSources + ` UNION ALL ` + filingRuleSources + ruleCandidateJoin + `
SELECT resource,resource_id::text,reference,system_name,organization_name,audience,recipient,to_char(deadline,'YYYY-MM-DD')
FROM candidates ORDER BY deadline,resource,resource_id LIMIT 21`

const ruleSweepSQL = `
WITH policies AS MATERIALIZED (
	SELECT id,version,name,trigger_kind,audience,target_unit_id,district,level
	FROM alert_config_rules WHERE enabled FOR SHARE SKIP LOCKED
), hazard_sources AS MATERIALIZED (
	` + hazardRuleSources + ` FOR SHARE OF a,h,f SKIP LOCKED
), filing_sources AS MATERIALIZED (
	` + filingRuleSources + ` FOR SHARE OF f SKIP LOCKED
), sources AS (
	SELECT * FROM hazard_sources UNION ALL SELECT * FROM filing_sources
` + ruleCandidateJoin + `, fresh AS (
	SELECT c.* FROM candidates c WHERE NOT EXISTS (
		SELECT 1 FROM alert_rule_matches m
		WHERE m.rule_id=c.rule_id AND m.resource=c.resource AND m.resource_id=c.resource_id
		  AND m.stage_version=c.stage_version AND m.audience=c.audience AND m.recipient=c.recipient
	)
	ORDER BY deadline,resource_id,rule_id LIMIT 200
), inserted AS (
	INSERT INTO alert_rule_matches(rule_id,rule_version,rule_name,resource,resource_id,stage_version,notice_state,
		reference,system_name,organization_name,audience,recipient,deadline)
	SELECT rule_id,rule_version,rule_name,resource,resource_id,stage_version,notice_state,
		reference,system_name,organization_name,audience,recipient,deadline FROM fresh
	ON CONFLICT DO NOTHING RETURNING *
), notices AS (
	INSERT INTO review_notices(audience,resource,resource_id,event_version,state,recipient,reference,
		subject,comment,workflow_day)
	SELECT audience,resource,resource_id,stage_version,notice_state,recipient,reference,system_name,
		CASE WHEN notice_state='deadline_overdue' THEN '备案审核已超过办理截止日期 '||to_char(deadline,'YYYY-MM-DD') ELSE '' END,
		CASE WHEN notice_state='deadline_overdue' THEN deadline END
	FROM inserted WHERE audience IN ('institution','operator')
	ORDER BY resource_id,audience,recipient,rule_id
	ON CONFLICT DO NOTHING RETURNING 1
)
SELECT (SELECT count(*) FROM inserted),(SELECT count(*) FROM notices)
`

// sweepRules 将规则、当期待办、真实收件范围和通知统一在一个数据库语句中登记。
// 参数 ctx 为本轮数据库操作的上下文。
// 返回值 新增规则命中和首次实际通知数量，或扫描错误。
func (s *Service) sweepRules(ctx context.Context) (ruleSweep, error) {
	var out ruleSweep
	err := s.db.QueryRow(ctx, ruleSweepSQL, s.ruleWindows.InitialDays, s.ruleWindows.FinalDays).
		Scan(&out.Matched, &out.Notified)
	return out, err
}
