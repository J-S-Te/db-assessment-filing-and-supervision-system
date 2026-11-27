package supervision

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/reviewinbox"
)

// executeSiteAction 串行锁定同一任务，检查现场版本后保存动作、材料和定向消息。
// 参数 ctx 为事务控制，id、owner 为任务与席位，input 是规范动作请求。
// 返回值 独立现场状态和版本，或范围、材料和并发错误。
func (s *Service) executeSiteAction(ctx context.Context, id, owner string, input siteActionInput) (mutation, error) {
	var result mutation
	hash, err := supervisionHash(input)
	if err != nil {
		return result, err
	}
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		task, err := loadTask(ctx, tx, id, owner, true)
		if err != nil {
			return err
		}
		record, err := loadSite(ctx, tx, id, false)
		if err != nil {
			return err
		}
		if record.Version != *input.ExpectedVersion {
			version := *input.ExpectedVersion + 1
			if owner != "" && version <= record.historyVersion {
				return errState
			}
			result, err = replaySiteAction(ctx, tx, id, version, hash)
			return err
		}
		next, err := prepareSiteAction(ctx, tx, task, record, owner, input)
		if err != nil {
			return err
		}
		result = mutation{id, next.Status, next.Version}
		return s.saveSiteAction(ctx, tx, task, owner, input, next, hash)
	})
	return result, err
}

// replaySiteAction 查已提交动作确切版本及输入摘要，防止重试改写轨迹。
// 参数 ctx、tx 为办理事务，id、version、hash 表示需要查证的原动作。
// 返回值 原动作结果或版本冲突。
func replaySiteAction(ctx context.Context, tx pgx.Tx, id string, version int64, hash string) (mutation, error) {
	result := mutation{ID: id, Version: version}
	err := tx.QueryRow(ctx, `SELECT status FROM supervision_site_events WHERE task_id=$1 AND version=$2
		AND input_hash=$3`, id, version, hash).Scan(&result.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return mutation{}, errState
	}
	return result, err
}

// prepareSiteAction 确定状态、时间及本轮证据，禁止提交旧资料代替补正。
// 参数 ctx、tx 为写事务，task、record 为锁后快照，owner、input 为席位和请求。
// 返回值 下一阶段及必要公开材料或应拒绝的原因。
func prepareSiteAction(ctx context.Context, tx pgx.Tx, task taskRecord, record siteRecord,
	owner string, input siteActionInput,
) (siteNext, error) {
	active, err := siteScopeActive(ctx, tx, task.ID)
	if err != nil {
		return siteNext{}, err
	}
	if !slices.Contains(siteActions(task, record, owner != "", active), input.Action) {
		return siteNext{}, errState
	}
	if owner != "" || input.Action == "schedule" || input.Action == "follow_up" || input.Action == "return_follow_up" {
		if err = lockWritableFiling(ctx, tx, task, false); err != nil {
			return siteNext{}, err
		}
	}
	next := siteNext{siteRecord: record}
	next.Version++
	next.Status = siteNextStatus(input.Action)
	return applySiteActionData(ctx, tx, task.ID, next, input)
}

// applySiteActionData 验证并填入安排、现场结果或整改动作专属数据。
// 参数 ctx、tx、id 为持久化环境与任务标识，next、input 是目标状态及字段。
// 返回值 已校验的目标状态或材料、时限错误。
func applySiteActionData(ctx context.Context, tx pgx.Tx, id string, next siteNext,
	input siteActionInput,
) (siteNext, error) {
	switch input.Action {
	case "schedule":
		visit, _ := time.Parse(time.RFC3339, input.VisitAt)
		if !visit.After(time.Now()) {
			return siteNext{}, errInput
		}
		next.VisitAt, next.Location, next.Inspectors = &visit, input.Location, input.Inspectors
	case "follow_up":
		var future bool
		err := tx.QueryRow(ctx, `SELECT $1::date>(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date`,
			input.FollowUpDueOn).Scan(&future)
		if err != nil {
			return siteNext{}, err
		}
		if !future {
			return siteNext{}, errInput
		}
		next.FollowUpDueOn = input.FollowUpDueOn
	case "record":
		next.Outcome = input.Outcome
		evidence, err := siteEvidenceForAction(ctx, tx, id, siteEvidence, next.historyVersion, []string{"schedule"})
		next.evidenceID = evidence
		return next, err
	case "submit_follow_up":
		evidence, err := siteEvidenceForAction(ctx, tx, id, siteFollowUpEvidence, next.historyVersion,
			[]string{"follow_up", "return_follow_up"})
		next.evidenceID = evidence
		return next, err
	}
	return next, nil
}

// siteEvidenceForAction 选择确实晚于本轮开启操作的有效材料。
// 参数 ctx、tx 为事务，id、kind、after、actions 为任务、材料类别、交接版本及开启本轮的动作。
// 返回值 当前材料标识，缺少新材料时给出可操作错误。
func siteEvidenceForAction(ctx context.Context, tx pgx.Tx, id, kind string, after int64,
	actions []string,
) (string, error) {
	var evidence string
	err := tx.QueryRow(ctx, `SELECT id::text FROM supervision_site_attachments
		WHERE task_id=$1 AND kind=$2 AND version>$3 AND version>COALESCE((SELECT max(version)
		 FROM supervision_site_events WHERE task_id=$1 AND action=ANY($4::text[])),0)
		ORDER BY version DESC LIMIT 1`, id, kind, after, actions).Scan(&evidence)
	return evidence, requiredError(err)
}

// saveSiteAction 持久化独立状态及相应材料轨迹，并给原运营单位投递状态消息。
// 参数 ctx、tx 为办理事务，task、owner、input、next、hash 为原归属和状态变更。
// 返回值 任一步失败导致整笔回滚。
func (s *Service) saveSiteAction(ctx context.Context, tx pgx.Tx, task taskRecord, owner string,
	input siteActionInput, next siteNext, hash string,
) error {
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO supervision_site_inspections(task_id,status,version,visit_at,
			location,inspectors,outcome,follow_up_due_on)
		VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,'')::date)
		ON CONFLICT (task_id) DO UPDATE SET status=excluded.status,version=excluded.version,
			visit_at=excluded.visit_at,location=excluded.location,inspectors=excluded.inspectors,
			outcome=excluded.outcome,follow_up_due_on=excluded.follow_up_due_on`, task.ID, next.Status,
		next.Version, next.VisitAt, next.Location, next.Inspectors, next.Outcome, next.FollowUpDueOn)
	actor := s.actor
	if owner != "" {
		actor = owner
	}
	batch.Queue(`INSERT INTO supervision_site_events(task_id,version,action,status,actor,comment,input_hash,evidence_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::uuid)`, task.ID, next.Version,
		input.Action, next.Status, actor, input.Comment, hash, next.evidenceID)
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return err
	}
	if owner != "" {
		return nil
	}
	return sendSiteNotice(ctx, tx, task, input, next)
}

// sendSiteNotice 只向既有任务的原收件单位投递现场安排及每轮结果。
// 参数 ctx、tx 为写事务，task、input、next 为任务快照与刚生效的操作。
// 返回值 通知写入错误或成功。
func sendSiteNotice(ctx context.Context, tx pgx.Tx, task taskRecord, input siteActionInput, next siteNext) error {
	states := map[string]string{"schedule": "site_scheduled", "record": "site_result",
		"follow_up": "site_follow_up", "return_follow_up": "site_returned", "close": "site_closed",
		"cancel": "site_cancelled"}
	comment := input.Comment
	if input.Action == "schedule" {
		comment = "请查看现场时间、地点及检查组安排。" + comment
	}
	return reviewinbox.Deliver(ctx, tx, reviewinbox.Notice{
		Audience: "operator", Resource: "supervisions", ResourceID: task.ID,
		Version: next.Version, State: states[input.Action], Recipient: task.OrganizationName,
		Reference: task.FilingNumber, Subject: task.Title, Comment: comment,
	})
}
