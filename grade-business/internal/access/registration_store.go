package access

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/directory"
	"grade-business/internal/geoarea"
)

type registrationReceipt struct {
	ID         string `json:"id"`
	LookupCode string `json:"lookupCode"`
	registrationResult
}

// submitRegistration 登记待人工核实的资料并返回独立查询码。
// 参数 ctx 为申请上下文，input 为已完成格式验证的注册资料。
// 返回值 待审申请及私密查询码，或持久化错误。
func (s *Service) submitRegistration(ctx context.Context, input registrationInput) (registrationReceipt, error) {
	var result registrationReceipt
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return result, err
	}
	lookup := sha256.Sum256(raw)
	salt, hash, err := newPassword(input.Password)
	if err != nil {
		return result, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	requestedArea := ""
	if input.AreaCode != nil {
		requestedArea = *input.AreaCode
	}
	unit, area, err := registrationRouting(ctx, tx, input.CreditCode, requestedArea)
	if err != nil {
		return result, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO operator_registrations
		(unit_name,credit_code,contact_name,phone,username,area_code,requested_area_code,
		 original_unit_id,lookup_hash,password_salt,password_hash)
		VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::uuid,$9,$10,$11)
		RETURNING id::text,status,created_at`, input.UnitName, input.CreditCode,
		input.ContactName, input.Phone, input.Username, area, requestedArea, unit, lookup[:], salt, hash).
		Scan(&result.ID, &result.Status, &result.CreatedAt)
	if err != nil {
		return result, err
	}
	if err := recordRegistration(ctx, tx, Principal{}, result.ID, "submitted"); err != nil {
		return result, err
	}
	result.LookupCode = base64.RawURLEncoding.EncodeToString(raw)
	return result, tx.Commit(ctx)
}

// registrationRouting 为待审申请分派可信辖区，分派结果只对审核端可见。
// 参数 ctx、tx 为申请事务，code、requestedArea 为信用代码及申请选择的辖区。
// 返回值 当时的单位标识、有效辖区（无法确定时为空）和读取错误。
func registrationRouting(ctx context.Context, tx pgx.Tx, code, requestedArea string) (string, string, error) {
	var id, district string
	err := tx.QueryRow(ctx, `SELECT id::text,district FROM directory_parties
		WHERE kind='unit' AND lower(code)=lower($1)`, code).Scan(&id, &district)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", unlistedRegistrationArea(code, requestedArea), nil
	}
	return id, geoarea.RegulatorAreaCode(district), err
}

// unlistedRegistrationArea 为未建档单位选择注册审批辖区，并兼容旧申请的归属推断。
// 参数 code、requestedArea 为信用代码和申请人选择的辖区。
// 返回值 受支持的辖区代码，无法确认时为空。
func unlistedRegistrationArea(code, requestedArea string) string {
	if requestedArea != "" {
		return requestedArea
	}
	return geoarea.RegulatorAreaCode(code[2:8])
}

// registrationSubject 以信用代码确定可绑定的有效主档及可信辖区，并锁定命中的名录。
// 参数 ctx、tx 为审批事务，name、code、requestedArea 为申请时的单位身份及所选辖区。
// 返回值 现有单位标识或空值、标准辖区码，以及名录冲突或数据库错误。
func registrationSubject(ctx context.Context, tx pgx.Tx, name, code, requestedArea string) (string, string, error) {
	var id, currentName, district string
	var usable, linked bool
	err := tx.QueryRow(ctx, `SELECT p.id::text,p.name,p.district,p.status='active' AND p.merged_into_id IS NULL,
		EXISTS (SELECT 1 FROM access_accounts a WHERE a.subject_id=p.id)
		FROM directory_parties p WHERE p.kind='unit' AND lower(p.code)=lower($1) FOR UPDATE OF p`, code).
		Scan(&id, &currentName, &district, &usable, &linked)
	if errors.Is(err, pgx.ErrNoRows) {
		area := unlistedRegistrationArea(code, requestedArea)
		if area == "" {
			return "", "", errRegistrationArea
		}
		return "", area, nil
	}
	if err != nil {
		return "", "", err
	}
	if linked {
		return "", "", errRegistrationAccount
	}
	if !usable || currentName != name {
		return "", "", errRegistrationUnit
	}
	if area := geoarea.RegulatorAreaCode(district); area != "" {
		return id, area, nil
	}
	return "", "", errRegistrationArea
}

// listRegistrations 用途为分页读取辖区内的申请并按信用代码归并待审队列。
// 参数 ctx 含已认证的审核身份，filter 为状态、检索、信用代码和分页条件。
// 返回值 一页申请或待审分组、下一页游标或数据库错误。
func (s *Service) listRegistrations(ctx context.Context, filter registrationFilter) ([]registrationItem, int64, error) {
	pageRows := `SELECT sequence,0::bigint AS pending_count FROM eligible WHERE matches_query`
	if filter.status == "pending" && filter.creditCode == "" {
		// 最早申请决定分组位置，重复提交不会将已有分组推回队首。
		pageRows = `SELECT min(sequence) FILTER (WHERE matches_query) AS sequence, count(*) AS pending_count
			FROM eligible GROUP BY credit_code HAVING bool_or(matches_query)`
	}
	query := `WITH eligible AS (
		SELECT sequence,credit_code,($4='' OR strpos(lower(unit_name),lower($4))>0
		       OR strpos(lower(credit_code),lower($4))>0 OR strpos(username,lower($4))>0) AS matches_query
		FROM operator_registrations
		WHERE status=$1 AND ($2::text[] IS NULL OR area_code=ANY($2))
		  AND ($6='' OR credit_code=$6)
	), candidates AS (` + pageRows + `)
	SELECT ` + registrationColumns + `,pending_count FROM candidates
	JOIN operator_registrations USING (sequence)
	WHERE ($3::bigint=0 OR sequence<$3)
	ORDER BY sequence DESC LIMIT $5`
	rows, err := s.db.Query(ctx, query, filter.status, Districts(ctx), filter.cursor,
		filter.query, filter.limit+1, filter.creditCode)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]registrationItem, 0, filter.limit+1)
	for rows.Next() {
		var item registrationItem
		if err := registrationFields(&item, rows); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(items) > filter.limit {
		return items[:filter.limit], items[filter.limit-1].sequence, nil
	}
	return items, 0, nil
}

// reviewRegistration 用途为按可信辖区锁定并办理单份申请，原子地开通及留痕。
// 参数 ctx、actor 为审核会话，id 为单据标识，input 为已校验的审批决定。
// 返回值 已保存的审核记录，或越域、重复办理和事务错误。
func (s *Service) reviewRegistration(ctx context.Context, actor Principal, id string,
	input registrationDecision,
) (registrationItem, error) {
	var result registrationItem
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	pending, err := lockRegistration(ctx, tx, id)
	if err != nil {
		return result, err
	}
	status, accountID, subjectID := "rejected", "", ""
	if input.Decision == "approve" {
		if pending.area == "" && actor.Role != SystemAdmin {
			return result, errRegistrationArea
		}
		status = "approved"
		accountID, subjectID, err = activateRegistration(ctx, tx, pending)
		if err != nil {
			return result, err
		}
	}
	err = registrationFields(&result, tx.QueryRow(ctx, `UPDATE operator_registrations
		SET status=$2, reviewed_at=clock_timestamp(), reviewer_id=$3, reviewer_role=$4,
		    reviewer_name=$5, reason=$6, account_id=NULLIF($7,'')::uuid,
		    subject_id=NULLIF($8,'')::uuid, password_salt=NULL, password_hash=NULL
		WHERE id=$1 RETURNING `+registrationColumns+`,0::bigint`, id, status, actor.ID, actor.Role,
		actor.Name, input.Reason, accountID, subjectID))
	if err != nil {
		return result, err
	}
	if err := recordRegistration(ctx, tx, actor, id, status); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

type pendingRegistration struct {
	id, unitName, code, area, requestedArea, originalUnitID string
}

// lockRegistration 锁定本辖区的一条申请并检查是否仍可办理。
// 参数 ctx、tx 为审核会话和事务，id 为申请标识。
// 返回值 可办理申请的主体信息，或不存在及状态冲突错误。
func lockRegistration(ctx context.Context, tx pgx.Tx, id string) (pendingRegistration, error) {
	pending := pendingRegistration{id: id}
	var status string
	err := tx.QueryRow(ctx, `SELECT unit_name,credit_code,area_code,requested_area_code,
		COALESCE(original_unit_id::text,''),status FROM operator_registrations
		WHERE id=$1 AND ($2::text[] IS NULL OR area_code=ANY($2)) FOR UPDATE`, id, Districts(ctx)).
		Scan(&pending.unitName, &pending.code, &pending.area, &pending.requestedArea, &pending.originalUnitID, &status)
	if err == nil && status != "pending" {
		err = errRegistrationReviewed
	}
	return pending, err
}

// activateRegistration 复核名录归属并以申请时的口令摘要开通固定运营者角色。
// 参数 ctx、tx 为锁定后的审批事务，pending 为提交时的单位身份及辖区快照。
// 返回值 运营账号与单位标识，或归属变更、账号冲突及数据库错误。
func activateRegistration(ctx context.Context, tx pgx.Tx, pending pendingRegistration) (string, string, error) {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))",
		"operator-registration:"+pending.code); err != nil {
		return "", "", err
	}
	subjectID, area, err := registrationSubject(ctx, tx, pending.unitName, pending.code, pending.requestedArea)
	if err != nil {
		return "", "", err
	}
	if (pending.area != "" && area != pending.area) ||
		(pending.originalUnitID != "" && subjectID != pending.originalUnitID) {
		return "", "", errRegistrationUnit
	}
	if subjectID == "" {
		subjectID, err = directory.CreateRegistrationUnit(ctx, tx, pending.unitName, pending.code, districtNames[area])
		if err != nil {
			return "", "", err
		}
	}
	var accountID string
	err = tx.QueryRow(ctx, `INSERT INTO access_accounts
		(username,display_name,role_code,subject_id,password_salt,password_hash,must_change_password)
		SELECT username,contact_name,'ROLE_OPERATOR',$2,password_salt,password_hash,false
		FROM operator_registrations WHERE id=$1 RETURNING id::text`, pending.id, subjectID).Scan(&accountID)
	if err == nil && pending.area == "" {
		_, err = tx.Exec(ctx, "UPDATE operator_registrations SET area_code=$2 WHERE id=$1", pending.id, area)
	}
	return accountID, subjectID, err
}

// recordRegistration 将申请和审批结果写入已有权限审计表。
// 参数 ctx、tx 为业务事务，actor 为审核身份或匿名申请，id、action 为单据标识和状态事件。
// 返回值 审计持久化错误，失败时调用方回滚业务改动。
func recordRegistration(ctx context.Context, tx pgx.Tx, actor Principal, id, action string) error {
	_, err := tx.Exec(ctx, `INSERT INTO access_events(operator_id,role_code,event,target)
		VALUES(NULLIF($1,'')::uuid,NULLIF($2,''),$3,$4)`, actor.ID, actor.Role,
		"operator_registration_"+action, id)
	return err
}
