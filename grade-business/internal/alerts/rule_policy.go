package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// RuleWindows 按列表和审核流程的相同配置确定备案超期日。
type RuleWindows struct {
	InitialDays int
	FinalDays   int
}

type rulePolicy struct {
	Name     string `json:"name"`
	Trigger  string `json:"trigger"`
	Audience string `json:"audience"`
	District string `json:"district,omitempty"`
	Level    string `json:"level,omitempty"`
	UnitID   string `json:"unitId,omitempty"`
	All      bool   `json:"all,omitempty"`
	Enabled  bool   `json:"enabled"`
}

type ruleRow struct {
	rulePolicy
	ID        string    `json:"id"`
	Version   int64     `json:"version"`
	Actor     string    `json:"actor"`
	UpdatedAt time.Time `json:"updatedAt"`
	Sequence  int64     `json:"-"`
}

type ruleCollection[T any] struct {
	Items      []T    `json:"items"`
	NextBefore string `json:"nextBefore,omitempty"`
}

const rulesPageSize = 20
const ruleColumns = `name,trigger_kind,audience,district,level,COALESCE(target_unit_id::text,''),
	all_scope,enabled,id::text,version,actor,updated_at,sequence`

// normalizeRule 校验支持的业务来源、接收角色及显式的收件范围。
// 参数 input 为业务表单，haveEnabled 表示是否明确填写了启用状态。
// 返回值 保持规范化之后的表单能保存时为真。
func normalizeRule(input *rulePolicy, haveEnabled bool) bool {
	input.Name = strings.TrimSpace(input.Name)
	input.District = strings.TrimSpace(input.District)
	input.UnitID = strings.ToLower(input.UnitID)
	if !haveEnabled || input.Name == "" || !validText(input.Name, 100) ||
		!validText(input.District, 80) || (input.District != "" && strings.ContainsAny(input.District, "\r\n\t")) {
		return false
	}
	if input.UnitID != "" {
		if _, ok := parseAlertID(input.UnitID); !ok {
			return false
		}
	}
	switch input.Trigger {
	case "high_risk", "overdue", "filing_review_overdue":
	default:
		return false
	}
	switch input.Audience {
	case "regulator", "institution", "operator":
	default:
		return false
	}
	switch input.Level {
	case "", "1", "2", "3", "4", "5":
	default:
		return false
	}
	return input.All == (input.UnitID == "" && input.District == "" && input.Level == "")
}

// checkRuleUnit 验证选用的是可办理的真实单位名录，停用规则仍可保存原范围。
// 参数 ctx、tx 控制并发写，input 为已解析的规则参数。
// 返回值 校验通过为空，或范围失效及数据库错误。
func checkRuleUnit(ctx context.Context, tx pgx.Tx, input rulePolicy) error {
	if input.UnitID == "" {
		return nil
	}
	var valid bool
	err := tx.QueryRow(ctx, `SELECT status='active' AND merged_into_id IS NULL FROM directory_parties
		WHERE id=$1 AND kind='unit' FOR SHARE`, input.UnitID).Scan(&valid)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return errInvalid
	case err != nil:
		return err
	case !valid && input.Enabled:
		return errConflict
	default:
		return nil
	}
}

// storeRule 以固定请求标识进行规则登记及并发幂等重放。
// 参数 ctx 控制操作，requestID 为重放标识，input 为经核对的初次规则参数。
// 返回值 固定规则标识与当前版本，或范围、冲突及存储错误。
func (s *Service) storeRule(ctx context.Context, requestID string, input rulePolicy) (ruleRow, error) {
	result := ruleRow{}
	payload, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		existing, err := loadRuleByRequest(ctx, tx, requestID, payload)
		if !errors.Is(err, pgx.ErrNoRows) {
			result = existing
			return err
		}
		if err := checkRuleUnit(ctx, tx, input); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, insertRuleSQL, requestID, payload, input.Name, input.Trigger,
			input.Audience, input.UnitID, input.District, input.Level, input.All, input.Enabled, s.operator)
		if err != nil {
			return err
		}
		result, err = pgx.CollectOneRow(rows, pgx.RowToStructByPos[ruleRow])
		if errors.Is(err, pgx.ErrNoRows) {
			result, err = loadRuleByRequest(ctx, tx, requestID, payload)
			if errors.Is(err, pgx.ErrNoRows) {
				return errConflict
			}
		}
		return err
	})
	return result, err
}

const insertRuleSQL = `INSERT INTO alert_config_rules(request_id,original_input,name,trigger_kind,
	audience,target_unit_id,district,level,all_scope,enabled,actor)
	VALUES($1,$2::jsonb,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8,$9,$10,$11)
	ON CONFLICT DO NOTHING RETURNING ` + ruleColumns

// loadRuleByRequest 核对重放参数并读取已登记规则。
// 参数 ctx、tx 为数据库操作，requestID 和 payload 为原请求的规范输入。
// 返回值 找到的原规则或无记录、参数冲突和查询错误。
func loadRuleByRequest(ctx context.Context, tx pgx.Tx, requestID string, payload []byte) (ruleRow, error) {
	rows, err := tx.Query(ctx, `SELECT `+ruleColumns+` FROM alert_config_rules
		WHERE request_id=$1 AND original_input=$2::jsonb`, requestID, payload)
	if err != nil {
		return ruleRow{}, err
	}
	return pgx.CollectOneRow(rows, pgx.RowToStructByPos[ruleRow])
}

// updateRule 在锁定规则版本后保存修改，同内容提交沿用现有版本。
// 参数 ctx 控制更新，id、expected 指定规则及读到的版本，input 为明确的新设置。
// 返回值 最终设置或不存在、版本冲突及持久化错误。
func (s *Service) updateRule(ctx context.Context, id string, expected int64, input rulePolicy) (ruleRow, error) {
	var result ruleRow
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "SELECT "+ruleColumns+" FROM alert_config_rules WHERE id=$1 FOR UPDATE", id)
		if err != nil {
			return err
		}
		result, err = pgx.CollectOneRow(rows, pgx.RowToStructByPos[ruleRow])
		if err != nil || (result.rulePolicy == input && (expected == result.Version || expected+1 == result.Version)) {
			return err
		}
		if result.Version != expected {
			return errConflict
		}
		if err := checkRuleUnit(ctx, tx, input); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, updateRuleSQL, id, input.Name, input.Trigger, input.Audience,
			input.UnitID, input.District, input.Level, input.All, input.Enabled, s.operator)
		if err != nil {
			return err
		}
		result, err = pgx.CollectOneRow(rows, pgx.RowToStructByPos[ruleRow])
		return err
	})
	var dbError *pgconn.PgError
	if errors.As(err, &dbError) && dbError.ConstraintName == "alert_config_rule_name" {
		err = errConflict
	}
	return result, err
}

const updateRuleSQL = `UPDATE alert_config_rules SET name=$2,trigger_kind=$3,audience=$4,
	target_unit_id=NULLIF($5,'')::uuid,district=$6,level=$7,all_scope=$8,enabled=$9,actor=$10,
	version=version+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING ` + ruleColumns
