package directory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const maxMergeSources = 8
const maxMergeFilings = 300
const complexMerge = "complex"

var (
	errMergeBlocked = errors.New("unit merge blocked")
	errMergeBusy    = errors.New("unit business is currently being changed")
)

type mergeInput struct {
	TargetID  string   `json:"targetId"`
	SourceIDs []string `json:"sourceIds"`
	Token     string   `json:"token,omitempty"`
	Mode      string   `json:"mode,omitempty"`
}

type mergeUnit struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ScopeName string `json:"scopeName"`
	Version   int64  `json:"version"`
}

type mergeAlias struct {
	mergeUnit
	MergedIntoID string `json:"mergedIntoId"`
}

type mergeMove struct {
	ID         string `json:"id"`
	Number     string `json:"number"`
	SystemName string `json:"systemName"`
	SourceID   string `json:"sourceId"`
	SourceName string `json:"sourceName"`
	Version    int64  `json:"version"`
}

type mergeBlocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Count   int64  `json:"count"`
}

type mergePlan struct {
	Mode      string         `json:"mode,omitempty"`
	Impacts   []mergeImpact  `json:"impacts,omitempty"`
	Target    mergeUnit      `json:"target"`
	Sources   []mergeUnit    `json:"sources"`
	Aliases   []mergeAlias   `json:"aliases,omitempty"`
	Moves     []mergeMove    `json:"moves"`
	Blockers  []mergeBlocker `json:"blockers"`
	Linked    int64          `json:"membershipsToAdd"`
	Cancelled int64          `json:"retainedCancelled"`
	Token     string         `json:"token,omitempty"`
}

type mergeResult struct {
	ID         string    `json:"id"`
	TargetID   string    `json:"targetId"`
	SourceIDs  []string  `json:"sourceIds"`
	MovedCount int       `json:"movedCount"`
	MergedAt   time.Time `json:"mergedAt"`
	Replayed   bool      `json:"replayed"`
}

type mergeHistory struct {
	mergeResult
	Actor   string    `json:"actor"`
	Preview mergePlan `json:"preview"`
}

// normalizeMerge 规范单位顺序并拒绝重复、主从相同和非法预览令牌。
// 参数 input 为请求数据，confirm 表示是否正在确认预览。
// 返回值 规范化请求或输入错误。
func normalizeMerge(input mergeInput, confirm bool) (mergeInput, error) {
	if !validID(input.TargetID) || len(input.SourceIDs) < 1 || len(input.SourceIDs) > maxMergeSources {
		return mergeInput{}, errInvalid
	}
	switch input.Mode {
	case "", "simple":
		input.Mode = ""
	case complexMerge:
	default:
		return mergeInput{}, errInvalid
	}
	input.TargetID = strings.ToLower(input.TargetID)
	for i, id := range input.SourceIDs {
		if !validID(id) || strings.EqualFold(id, input.TargetID) {
			return mergeInput{}, errInvalid
		}
		input.SourceIDs[i] = strings.ToLower(id)
	}
	slices.Sort(input.SourceIDs)
	if len(slices.Compact(slices.Clone(input.SourceIDs))) != len(input.SourceIDs) {
		return mergeInput{}, errInvalid
	}
	_, err := hex.DecodeString(input.Token)
	if (confirm && (len(input.Token) != 64 || err != nil)) || (!confirm && input.Token != "") {
		return mergeInput{}, errInvalid
	}
	input.Token = strings.ToLower(input.Token)
	return input, nil
}

// planToken 绑定当前归属、版本、备案清单及拟增机构关系。
// 参数 plan 为已排序预览，members 为拟新增关系的稳定标识。
// 返回值 内容摘要或预览编码错误。
func planToken(plan mergePlan, members []string) (string, error) {
	payload, err := json.Marshal(struct {
		Plan    mergePlan
		Members []string
	}{plan, members})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

// previewMerge 在一致快照中生成预览，预检过程不修改业务数据。
// 参数 ctx 控制查询，input 已通过主从范围校验。
// 返回值 预览或查询错误。
func (s service) previewMerge(ctx context.Context, input mergeInput) (mergePlan, error) {
	var plan mergePlan
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly},
		func(tx pgx.Tx) error {
			var err error
			plan, err = readMergePlan(ctx, tx, input)
			return err
		})
	return plan, err
}

// confirmMerge 在单一事务中复核权限影响、比对预览并迁移。
// 参数 ctx 控制操作，input 包含已确认的主从范围及预览令牌。
// 返回值 合并回执、幂等重放或状态冲突。
func (s service) confirmMerge(ctx context.Context, input mergeInput) (mergeResult, error) {
	var result mergeResult
	err := s.write(ctx, func(tx pgx.Tx) error {
		past, err := findMergedUnitRecords(ctx, tx, "preview_token", input.Token)
		if err == nil {
			if past.TargetID != input.TargetID || past.Preview.Mode != input.Mode ||
				!slices.Equal(past.SourceIDs, input.SourceIDs) {
				return errVersion
			}
			result = past.mergeResult
			result.Replayed = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := lockMergeWork(ctx, tx, input); err != nil {
			return err
		}
		plan, err := readMergePlan(ctx, tx, input)
		if err != nil {
			return err
		}
		if len(plan.Blockers) > 0 {
			if input.Mode == "" {
				return errMergeBlocked
			}
			return errVersion
		}
		if plan.Token != input.Token {
			return errVersion
		}
		result, err = commitMergePlan(ctx, tx, plan, input.SourceIDs)
		return err
	})
	return result, err
}

// lockMergeWork 防止确认与备案、子业务同时改写范围，纯查询仍可继续。
// 参数 ctx 和 tx 为当前合并事务，input 提供业务模式及主从目录范围。
// 返回值 已取得排他业务写锁，或需要稍后重试的业务冲突。
func lockMergeWork(ctx context.Context, tx pgx.Tx, input mergeInput) error {
	tables := `business_cases,filing_applications,implementation_reports,
		remediation_plans,hazard_items,material_issues,supervision_tasks,supervision_site_inspections,
		material_expiry_records`
	if input.Mode == complexMerge {
		tables += `,remediation_attachments,hazard_attachments,supervision_attachments,
			material_expiry_renewals,local_alerts,hazard_events,filing_withdrawal_requests,case_attachments,
			case_review_events`
	}
	if _, err := tx.Exec(ctx, "LOCK TABLE "+tables+" IN EXCLUSIVE MODE NOWAIT"); err != nil {
		return mergeLockError(err)
	}
	// 清册登记先锁目录行。冲突时先退出，避免阻塞清册写入后再等待目录行。
	ids := append([]string{input.TargetID}, input.SourceIDs...)
	if _, err := tx.Exec(ctx, `SELECT id FROM directory_parties WHERE kind='unit'
		AND id=ANY($1::uuid[]) ORDER BY id FOR NO KEY UPDATE NOWAIT`, ids); err != nil {
		return mergeLockError(err)
	}
	if input.Mode == complexMerge {
		_, err := tx.Exec(ctx, "LOCK TABLE archive_coverage_targets IN EXCLUSIVE MODE NOWAIT")
		return mergeLockError(err)
	}
	return nil
}

// mergeLockError 将无法立即取得锁的数据库错误转换成可重试的业务冲突。
// 参数 err 为加锁结果，可能为空。
// 返回值 业务忙碌或原加锁结果。
func mergeLockError(err error) error {
	var dbError *pgconn.PgError
	if errors.As(err, &dbError) && dbError.Code == "55P03" {
		return errMergeBusy
	}
	return err
}

// findMergedUnitRecords 返回已持久化的监管合并回执和原预览内容。
// 参数 ctx、db 控制查询，key 为代码指定的 id 或 preview_token，value 为查询值。
// 返回值 原回执及其计划，或未找到和持久化错误。
func findMergedUnitRecords(ctx context.Context, db reader, key, value string) (mergeHistory, error) {
	var item mergeHistory
	err := db.QueryRow(ctx, `SELECT id::text,target_id::text,plan,actor,merged_at
		FROM directory_unit_merges WHERE `+key+`=$1`, value).
		Scan(&item.ID, &item.TargetID, &item.Preview, &item.Actor, &item.MergedAt)
	for _, source := range item.Preview.Sources {
		item.SourceIDs = append(item.SourceIDs, source.ID)
	}
	item.MovedCount = len(item.Preview.Moves)
	return item, err
}
