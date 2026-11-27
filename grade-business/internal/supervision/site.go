package supervision

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/cases"
)

const siteEvidence = "inspection_evidence"
const siteFollowUpEvidence = "follow_up_evidence"

type siteActionInput struct {
	Action          string `json:"action"`
	ExpectedVersion *int64 `json:"expectedVersion"`
	VisitAt         string `json:"visitAt,omitempty"`
	Location        string `json:"location,omitempty"`
	Inspectors      string `json:"inspectors,omitempty"`
	Outcome         string `json:"outcome,omitempty"`
	FollowUpDueOn   string `json:"followUpDueOn,omitempty"`
	Comment         string `json:"comment,omitempty"`
}

type siteRecord struct {
	Status         string     `json:"status,omitempty"`
	Version        int64      `json:"version"`
	VisitAt        *time.Time `json:"visitAt,omitempty"`
	Location       string     `json:"location,omitempty"`
	Inspectors     string     `json:"inspectors,omitempty"`
	Outcome        string     `json:"outcome,omitempty"`
	FollowUpDueOn  string     `json:"followUpDueOn,omitempty"`
	historyVersion int64
}

type siteEvent struct {
	Version    int64     `json:"version"`
	Action     string    `json:"action"`
	Status     string    `json:"status"`
	Actor      string    `json:"actor"`
	Comment    string    `json:"comment,omitempty"`
	EvidenceID string    `json:"evidenceId,omitempty"`
	OccurredAt time.Time `json:"occurredAt"`
}

type siteView struct {
	siteRecord
	AllowedActions []string           `json:"allowedActions"`
	Attachments    []cases.Attachment `json:"attachments"`
	Events         []siteEvent        `json:"events"`
}

type siteNext struct {
	siteRecord
	evidenceID string
}

// validateSiteAction 约束各动作的版本、办理方及实际使用的字段。
// 参数 input 为现地检查动作，operator 表示运营单位入口。
// 返回值 符合对应业务输入时为真。
func validateSiteAction(input *siteActionInput, operator bool) bool {
	if input.ExpectedVersion == nil || *input.ExpectedVersion < 0 || *input.ExpectedVersion == math.MaxInt64 {
		return false
	}
	input.Comment = strings.TrimSpace(input.Comment)
	input.Location = strings.TrimSpace(input.Location)
	input.Inspectors = strings.TrimSpace(input.Inspectors)
	if input.Comment != "" && !validText(input.Comment, 1000) {
		return false
	}
	if input.Action == "schedule" {
		return !operator && validateSiteSchedule(input)
	}
	if *input.ExpectedVersion == 0 || input.VisitAt != "" || input.Location != "" || input.Inspectors != "" {
		return false
	}
	return validateSiteDecision(*input, operator)
}

// validateSiteSchedule 核对现场时间格式、地点和检查组所需字段。
// 参数 input 为监管安排请求。
// 返回值 可进行安排时为真，时间同时规范为 UTC。
func validateSiteSchedule(input *siteActionInput) bool {
	visit, err := time.Parse(time.RFC3339, input.VisitAt)
	if err != nil || visit.Year() <= 0 || !validText(input.Location, 200) ||
		!validText(input.Inspectors, 200) || input.Outcome != "" || input.FollowUpDueOn != "" {
		return false
	}
	input.VisitAt = visit.UTC().Format(time.RFC3339)
	return true
}

// validateSiteDecision 在具体阶段只允许实际使用的字段，并区分两端动作。
// 参数 input 为已清理的字段，operator 标记运营端。
// 返回值 字段组合合法时为真。
func validateSiteDecision(input siteActionInput, operator bool) bool {
	if operator {
		return input.Action == "submit_follow_up" && input.Outcome == "" && input.FollowUpDueOn == ""
	}
	switch input.Action {
	case "record":
		return input.Comment != "" && input.FollowUpDueOn == "" &&
			slices.Contains([]string{"pass", "follow_up_required"}, input.Outcome)
	case "follow_up":
		date, err := time.Parse(time.DateOnly, input.FollowUpDueOn)
		return err == nil && date.Year() > 0 && input.Comment != "" && input.Outcome == ""
	case "return_follow_up", "close", "cancel":
		return input.Comment != "" && input.Outcome == "" && input.FollowUpDueOn == ""
	default:
		return false
	}
}

// loadSite 读取已有现场记录或原范围在交接时的冻结详情。
// 参数 ctx、db 负责查询，taskID 是已核验任务，historical 为原历史范围。
// 返回值 独立现场状态和版本，或数据库错误。
func loadSite(ctx context.Context, db reader, taskID string, historical bool) (siteRecord, error) {
	var record siteRecord
	err := db.QueryRow(ctx, `SELECT
		CASE WHEN $2 THEN merge_history_snapshot->>'status' ELSE status END,
		CASE WHEN $2 THEN merge_history_version ELSE version END,
		CASE WHEN $2 THEN (merge_history_snapshot->>'visit_at')::timestamptz ELSE visit_at END,
		CASE WHEN $2 THEN merge_history_snapshot->>'location' ELSE location END,
		CASE WHEN $2 THEN merge_history_snapshot->>'inspectors' ELSE inspectors END,
		COALESCE(CASE WHEN $2 THEN merge_history_snapshot->>'outcome' ELSE outcome END,''),
		COALESCE(CASE WHEN $2 THEN merge_history_snapshot->>'follow_up_due_on' ELSE follow_up_due_on::text END,''),
		COALESCE(merge_history_version,0) FROM supervision_site_inspections
		WHERE task_id=$1 AND (NOT $2 OR merge_history_version IS NOT NULL)`, taskID, historical).
		Scan(&record.Status, &record.Version, &record.VisitAt, &record.Location, &record.Inspectors,
			&record.Outcome, &record.FollowUpDueOn, &record.historyVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return record, err
}

// siteActions 将现场阶段映射到当前监管或运营席位的合法动作。
// 参数 task 为监督任务，record 为现场记录，operator、active 为席位及是否当前有权办理。
// 返回值 用于界面提示的动作集合。
func siteActions(task taskRecord, record siteRecord, operator, active bool) []string {
	if operator {
		if record.Status == "following_up" && active && task.Status != "cancelled" {
			return []string{"upload", "submit_follow_up"}
		}
		return []string{}
	}
	switch {
	case (record.Status == "" || record.Status == "scheduled") &&
		active && task.PublishedAt != nil && task.Status != "cancelled":
		if record.Version == 0 {
			return []string{"schedule"}
		}
		return []string{"schedule", "upload", "record", "cancel"}
	case record.Status == "recorded" && record.Outcome == "pass":
		return []string{"close", "cancel"}
	case record.Status == "recorded" && active && task.Status != "cancelled":
		return []string{"follow_up", "cancel"}
	case record.Status == "follow_up_submitted":
		if active && task.Status != "cancelled" {
			return []string{"return_follow_up", "close", "cancel"}
		}
		return []string{"close", "cancel"}
	case record.Status != "" && record.Status != "closed" && record.Status != "cancelled":
		return []string{"cancel"}
	default:
		return []string{}
	}
}

// siteNextStatus 确定通过权限和阶段校验后的现场动作目标。
// 参数 action 为已经允许的现场业务命令。
// 返回值 对应目标阶段。
func siteNextStatus(action string) string {
	switch action {
	case "schedule":
		return "scheduled"
	case "record":
		return "recorded"
	case "follow_up", "return_follow_up":
		return "following_up"
	case "submit_follow_up":
		return "follow_up_submitted"
	case "cancel":
		return "cancelled"
	default:
		return "closed"
	}
}

// siteScopeActive 核对现行备案仍属于原收件单位。
// 参数 ctx、db 控制查询，id 是已核准监督任务。
// 返回值 当前可以流转时为真，或数据库错误。
func siteScopeActive(ctx context.Context, db reader, id string) (bool, error) {
	var active bool
	err := db.QueryRow(ctx, `SELECT `+currentTaskFiling+` FROM supervision_tasks t WHERE t.id=$1`, id).Scan(&active)
	return active, err
}

// siteDetail 在同一只读快照返回已授权的现场计划、材料和历史。
// 参数 ctx 控制查询，id、owner 提供监督任务和运营单位范围。
// 返回值 现场计划详情，或范围及读取错误。
func (s *Service) siteDetail(ctx context.Context, id, owner string) (siteView, error) {
	result := siteView{Attachments: []cases.Attachment{}, Events: []siteEvent{}}
	err := pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly},
		func(tx pgx.Tx) error {
			task, err := loadTask(ctx, tx, id, owner, false)
			if err != nil {
				return err
			}
			result.siteRecord, err = loadSite(ctx, tx, id, task.Historical)
			if err != nil {
				return err
			}
			active, err := siteScopeActive(ctx, tx, id)
			if err != nil {
				return err
			}
			result.AllowedActions = siteActions(task, result.siteRecord, owner != "", active && !task.Historical)
			if result.Version == 0 {
				return nil
			}
			versions := inspectionVersions(owner, task.Historical, result.historyVersion)
			result.Attachments, err = latestSiteAttachments(ctx, tx, id, owner, versions)
			if err != nil {
				return err
			}
			result.Events, err = siteHistory(ctx, tx, id, versions)
			return err
		})
	return result, err
}

// siteHistory 读取最近完成的现场动作与实际归档材料标识。
// 参数 ctx、db 为读事务，id、versions 为监督任务及已核验的版本范围。
// 返回值 近期轨迹或数据库错误。
func siteHistory(ctx context.Context, db reader, id string, versions inspectionVersionRange) ([]siteEvent, error) {
	rows, err := db.Query(ctx, `SELECT version,action,status,actor,comment,COALESCE(evidence_id::text,''),occurred_at
		FROM supervision_site_events WHERE task_id=$1 AND version>$2 AND ($3::bigint=0 OR version<=$3)
		ORDER BY version DESC LIMIT 30`, id, versions.after, versions.through)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[siteEvent])
}
