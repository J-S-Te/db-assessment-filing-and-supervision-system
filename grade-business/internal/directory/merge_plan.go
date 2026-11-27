package directory

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type mergeUnitStatus struct {
	mergeUnit
	Active     bool
	HasAliases bool
}

type filingMergeCandidate struct {
	mergeMove
	Status         string
	Approved       bool
	OperatorReview bool
	ExactScope     bool
	HasConflict    bool
}

const mergeOperatorFilingReview = `f.operator_origin AND f.status IN ('pending_initial','pending_final','returned')`

const mergeSourceFilings = `
	FROM business_cases f
	JOIN directory_parties u ON u.id::text=ANY($1::text[])
	  AND (f.directory_unit_id=u.id OR lower(btrim(f.organization_name)) IN
	    (lower(u.scope_name), lower(u.name)))
	WHERE f.kind='filing' AND f.status<>'withdrawn' AND f.discarded_at IS NULL`

// readMergePlan 读取精确备案范围及下游权限阻塞，只给可执行方案生成令牌。
// 参数 ctx 和 db 为一致查询，input 是规范化主从单位标识。
// 返回值 可完整展示的合并预览，或不存在和查询错误。
func readMergePlan(ctx context.Context, db reader, input mergeInput) (mergePlan, error) {
	plan, err := mergeParties(ctx, db, input)
	if err != nil {
		return mergePlan{}, err
	}
	if input.Mode == complexMerge {
		plan.Aliases, err = mergeSourceAliases(ctx, db, input.SourceIDs)
		if err != nil {
			return mergePlan{}, err
		}
	}
	if err := addFilingPlan(ctx, db, input, &plan); err != nil {
		return mergePlan{}, err
	}
	memberships, err := mergeMemberships(ctx, db, input)
	if err != nil {
		return mergePlan{}, err
	}
	plan.Linked = int64(len(memberships))
	if len(plan.Blockers) == 0 {
		plan.Token, err = planToken(plan, memberships)
	}
	return plan, err
}

// mergeParties 读取主从目录版本，并说明停用、已合并和合并链限制。
// 参数 ctx、db 控制查询，input 含已排序的主从范围。
// 返回值 有主从快照的初始计划，或不存在及查询错误。
func mergeParties(ctx context.Context, db reader, input mergeInput) (mergePlan, error) {
	all := append([]string{input.TargetID}, input.SourceIDs...)
	rows, err := db.Query(ctx, `SELECT p.id::text,p.name,p.scope_name,p.version,
		p.status='active' AND p.merged_into_id IS NULL,
		EXISTS(SELECT 1 FROM directory_parties a WHERE a.merged_into_id=p.id)
		FROM directory_parties p WHERE p.kind='unit' AND p.id::text=ANY($1::text[]) ORDER BY p.id`, all)
	if err != nil {
		return mergePlan{}, err
	}
	defer rows.Close()
	plan := mergePlan{Mode: input.Mode, Sources: []mergeUnit{}, Moves: []mergeMove{}, Blockers: []mergeBlocker{}}
	for rows.Next() {
		var item mergeUnitStatus
		if err := rows.Scan(&item.ID, &item.Name, &item.ScopeName, &item.Version, &item.Active, &item.HasAliases); err != nil {
			return mergePlan{}, err
		}
		if !item.Active {
			plan.block("unit_unavailable", "主单位和来源单位须为尚未合并的启用主档。", 1)
		}
		if item.ID == input.TargetID {
			plan.Target = item.mergeUnit
		} else {
			plan.Sources = append(plan.Sources, item.mergeUnit)
			if item.HasAliases && input.Mode != complexMerge {
				plan.block("merge_source_has_aliases", "已有归并来源的主单位需要先核对完整归属链。", 1)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return mergePlan{}, err
	}
	if plan.Target.ID == "" || len(plan.Sources) != len(input.SourceIDs) {
		return mergePlan{}, pgx.ErrNoRows
	}
	return plan, nil
}

// mergeSourceAliases 读取各来源下面已经归并的历史目录及其原直接目标。
// 参数 ctx、db 控制一致查询，sourceIDs 为本次办理归属将迁移的来源单位。
// 返回值 按标识稳定排序的历史归属快照或查询错误。
func mergeSourceAliases(ctx context.Context, db reader, sourceIDs []string) ([]mergeAlias, error) {
	rows, err := db.Query(ctx, `WITH RECURSIVE aliases AS (
		SELECT id,name,scope_name,version,merged_into_id FROM directory_parties
		WHERE kind='unit' AND merged_into_id=ANY($1::uuid[])
		UNION
		SELECT p.id,p.name,p.scope_name,p.version,p.merged_into_id FROM directory_parties p
		JOIN aliases a ON p.merged_into_id=a.id WHERE p.kind='unit'
	)
	SELECT id::text,name,scope_name,version,merged_into_id::text FROM aliases ORDER BY id`, sourceIDs)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (mergeAlias, error) {
		var alias mergeAlias
		err := row.Scan(&alias.ID, &alias.Name, &alias.ScopeName, &alias.Version, &alias.MergedIntoID)
		return alias, err
	})
}

// addFilingPlan 界定可迁移备案并核对活跃事项及同名系统。
// 参数 ctx、db 管理实时快照，input 为来源范围，plan 用于追加实际范围与阻塞。
// 返回值 数据库错误，成功为 nil。
func addFilingPlan(ctx context.Context, db reader, input mergeInput, plan *mergePlan) error {
	items, err := filingMergeCandidates(ctx, db, input, plan.Target)
	if err != nil {
		return err
	}
	if len(items) > maxMergeFilings {
		plan.block("filing_batch_limit", "一次最多核对 300 条有效或办理中的备案，请减少来源范围。", int64(len(items)))
		return nil
	}
	for _, item := range items {
		switch {
		case !item.ExactScope:
			addFilingRule(plan, item, "legacy_scope_conflict")
		case !item.Approved && (input.Mode != complexMerge || !item.OperatorReview):
			addFilingRule(plan, item, "filing_open")
		default:
			plan.Moves = append(plan.Moves, item.mergeMove)
			if !item.Approved {
				addFilingRule(plan, item, "filing_review_transfer")
			}
			if item.HasConflict {
				addFilingRule(plan, item, "system_name_conflict")
			}
		}
	}
	if err := db.QueryRow(ctx, "SELECT count(*)"+mergeSourceFilings+" AND f.cancelled_at IS NOT NULL",
		input.SourceIDs).Scan(&plan.Cancelled); err != nil {
		return err
	}
	if plan.Mode == complexMerge {
		return addComplexDependencies(ctx, db, input, plan)
	}
	return addDependencyBlockers(ctx, db, plan)
}

// filingMergeCandidates 返回来源备案精确范围、版本和归属冲突信息。
// 参数 ctx、db 为一致读，input 为来源单位，target 为主单位范围。
// 返回值 有界的备案方案或数据库错误。
func filingMergeCandidates(ctx context.Context, db reader, input mergeInput,
	target mergeUnit,
) ([]filingMergeCandidate, error) {
	rows, err := db.Query(ctx, `WITH candidates AS (
        SELECT f.id::text,f.number,f.system_name,u.id::text AS source_id,u.name AS source_name,
            f.version,f.status,f.status='approved' AS approved,
            (`+mergeOperatorFilingReview+`) AS operator_review,
            (f.organization_name COLLATE "C"=u.scope_name AND
              (f.directory_unit_id IS NULL OR f.directory_unit_id=u.id)) AS exact_scope
        `+mergeSourceFilings+` AND f.cancelled_at IS NULL
        ORDER BY f.id LIMIT $5
    )
    SELECT c.*, EXISTS(
        SELECT 1 FROM business_cases t
        WHERE t.kind='filing' AND t.cancelled_at IS NULL AND t.status<>'withdrawn' AND t.discarded_at IS NULL
            AND (t.directory_unit_id::text=$2 OR lower(btrim(t.organization_name)) IN (lower($3),lower($4)))
            AND lower(btrim(t.system_name))=lower(btrim(c.system_name))
    ) OR count(*) OVER (PARTITION BY lower(btrim(c.system_name)))>1
    FROM candidates c ORDER BY c.id`, input.SourceIDs, target.ID, target.ScopeName, target.Name,
		maxMergeFilings+1)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (filingMergeCandidate, error) {
		var item filingMergeCandidate
		err := row.Scan(&item.ID, &item.Number, &item.SystemName, &item.SourceID, &item.SourceName,
			&item.Version, &item.Status, &item.Approved, &item.OperatorReview, &item.ExactScope, &item.HasConflict)
		return item, err
	})
}

// addDependencyBlockers 查找动态继承备案归属且不能无损迁移的业务引用。
// 参数 ctx、db 控制快照查询，plan 提供候选备案并接收阻塞说明。
// 返回值 保留各业务原归属时的查询结果错误。
func addDependencyBlockers(ctx context.Context, db reader, plan *mergePlan) error {
	if len(plan.Moves) == 0 {
		return nil
	}
	var reports, implementation, applications, risks, materials, supervisions, expiries int64
	err := db.QueryRow(ctx, `WITH moved AS (SELECT unnest($1::text[])::uuid AS id),
	    reports AS (SELECT r.id,r.status FROM business_cases r JOIN moved f ON f.id=r.filing_id
	                WHERE r.kind='report' AND r.discarded_at IS NULL)
	SELECT (SELECT count(*) FROM reports WHERE status<>'approved'),
	       (SELECT count(*) FROM implementation_reports i JOIN moved f ON f.id=i.filing_id),
	       (SELECT count(*) FROM filing_applications a JOIN moved f ON f.id=a.filing_id
	        WHERE a.status NOT IN ('approved','withdrawn')),
	       (SELECT count(*) FROM remediation_plans p JOIN reports r ON r.id=p.report_id)
	       + (SELECT count(*) FROM hazard_items h JOIN moved f ON f.id=h.registration_filing_id),
	       (SELECT count(*) FROM material_issues m WHERE m.closed_at IS NULL
	         AND (m.case_id IN (SELECT id FROM moved) OR m.case_id IN (SELECT id FROM reports))),
	       (SELECT count(*) FROM supervision_tasks s JOIN moved f ON f.id=s.filing_id
	        WHERE s.status IN ('draft','open','submitted','returned') OR EXISTS (
	          SELECT 1 FROM supervision_site_inspections site
	          WHERE site.task_id=s.id AND site.status NOT IN ('closed','cancelled'))),
	       (SELECT count(*) FROM material_expiry_records m JOIN moved f ON f.id=m.filing_id
	        WHERE m.retired_at IS NULL)`, moveIDs(plan.Moves)).
		Scan(&reports, &implementation, &applications, &risks, &materials, &supervisions, &expiries)
	if err != nil {
		return err
	}
	plan.block("report_open", "存在未完成的测评报告。", reports)
	plan.block("implementation_history", "存在仍保留原单位人员和联系信息的实施报备。", implementation)
	plan.block("filing_application_open", "存在未办结的备案变更或注销。", applications)
	plan.block("risk_history", "存在必须保留原单位访问范围的整改规划或隐患。", risks)
	plan.block("material_issue_open", "存在未关闭的材料异常。", materials)
	plan.block("supervision_open", "存在未办结的监督检查，自查证据和收件单位需要保留。", supervisions)
	plan.block("material_expiry_open", "存在在用材料有效期登记，需先核对续办和原收件范围。", expiries)
	return nil
}

// mergeMemberships 返回主单位尚未具备、将随本批合并补齐的机构关系。
// 参数 ctx、db 控制查询，input 为本次主从档案。
// 返回值 已排序机构 ID 与持久化错误。
func mergeMemberships(ctx context.Context, db reader, input mergeInput) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT DISTINCT m.institution_id::text FROM directory_unit_memberships m
		WHERE m.unit_id::text=ANY($1::text[]) AND NOT EXISTS (
			SELECT 1 FROM directory_unit_memberships t
			WHERE t.unit_id=$2 AND t.institution_id=m.institution_id)
		ORDER BY 1`, input.SourceIDs, input.TargetID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// block 累加同一规则发现的实际阻塞数。
// 参数 code、message、count 表示业务规则及新增影响数。
// 返回值 无。
func (p *mergePlan) block(code, message string, count int64) {
	if count == 0 {
		return
	}
	for i, item := range p.Blockers {
		if item.Code == code {
			p.Blockers[i].Count += count
			return
		}
	}
	p.Blockers = append(p.Blockers, mergeBlocker{code, message, count})
}

// moveIDs 提取将原子迁移的备案标识。
// 参数 moves 为预览中的备案范围。
// 返回值 已确认的唯一备案 ID。
func moveIDs(moves []mergeMove) []string {
	ids := make([]string, len(moves))
	for i, item := range moves {
		ids[i] = item.ID
	}
	return ids
}
