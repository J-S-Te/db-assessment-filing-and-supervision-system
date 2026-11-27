package access

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)
	errCredential   = errors.New("账号或密码无效")
	errProvision    = errors.New("账号已经存在、主体已绑定或名录记录不可用")
	errAccountInput = errors.New("请核对账号、角色、辖区、主体和临时密码")
)

type AccountInput struct {
	Username, Name, PersonKey, AreaCode, SubjectID, Password string
	Role                                                     Role
}

// Provision 建立唯一角色账号并要求首次登录修改临时密码。
// 参数 ctx、db 为数据库操作环境，input 为新账号、稳定归属与初始口令。
// 返回值 新账号标识，或账号、主体及数据库错误。
func Provision(ctx context.Context, db *pgxpool.Pool, input AccountInput) (string, error) {
	return provisionAccount(ctx, db, input, Principal{})
}

// provisionAccount 原子开通账号及留痕，网页开户仅允许绑定有效机构。
// 参数 ctx、db 为数据库操作环境，input 为账号资料，actor 为网页操作人或零值的运维入口。
// 返回值 新账号标识、业务校验或数据库错误。
func provisionAccount(ctx context.Context, db *pgxpool.Pool, input AccountInput, actor Principal) (string, error) {
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	input.Name = strings.TrimSpace(input.Name)
	input.PersonKey = strings.ToLower(strings.TrimSpace(input.PersonKey))
	if !validAccount(input) {
		return "", errAccountInput
	}
	salt, hash, err := newPassword(input.Password)
	if err != nil {
		return "", err
	}
	var id string
	err = db.QueryRow(ctx, provisionAccountSQL, input.Username, input.Name, input.Role, input.PersonKey,
		input.AreaCode, input.SubjectID, salt, hash, actor.ID, actor.Role).Scan(&id)
	var databaseError *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) || errors.As(err, &databaseError) &&
		(databaseError.Code == "23505" || databaseError.Code == "23503" || databaseError.Code == "23514") {
		return "", errProvision
	}
	if err != nil {
		return "", errors.New("账号写入失败，请确认已应用权限迁移且业务库可用")
	}
	return id, nil
}

const provisionAccountSQL = `
	WITH account AS (
		INSERT INTO access_accounts(username,display_name,role_code,person_key,area_code,
		                            subject_id,password_salt,password_hash)
		SELECT $1,$2,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,'')::uuid,$7,$8
		WHERE $9='' OR EXISTS (
			SELECT 1 FROM directory_parties p
			WHERE p.id=NULLIF($6,'')::uuid AND p.kind='institution'
			  AND p.status='active' AND p.merged_into_id IS NULL
			  AND p.valid_until >= (statement_timestamp() AT TIME ZONE 'Asia/Shanghai')::date
			FOR SHARE OF p
		)
		RETURNING id,role_code
	), audit AS (
		INSERT INTO access_events(operator_id,role_code,event,target)
		SELECT COALESCE(NULLIF($9,'')::uuid,id),COALESCE(NULLIF($10,''),role_code),
		       'account_provisioned',CASE WHEN $9='' THEN 'database_operator' ELSE id::text END
		FROM account
	)
	SELECT id::text FROM account`

// validAccount 验证角色与账号的唯一主体或人员约束。
// 参数 input 为待配置账号，辖区和关联标识来自授权的运维操作。
// 返回值 字段与所选角色匹配时为真。
func validAccount(input AccountInput) bool {
	if !usernamePattern.MatchString(input.Username) || !validPassword(input.Password) ||
		input.Name == "" || utf8.RuneCountInString(input.Name) > 100 ||
		utf8.RuneCountInString(input.PersonKey) > 160 {
		return false
	}
	switch input.Role {
	case SystemAdmin:
		return input.PersonKey != "" && input.AreaCode == "" && input.SubjectID == ""
	case Regulator:
		return input.PersonKey != "" && districtNames[input.AreaCode] != "" && input.SubjectID == ""
	case OrgAdmin, Operator:
		var id pgtype.UUID
		return input.PersonKey == "" && input.AreaCode == "" &&
			input.SubjectID != "" && id.Scan(input.SubjectID) == nil && id.Valid
	default:
		return false
	}
}

// validPassword 检查首次设置和后续修改时的密码长度。
// 参数 password 为用户提交的口令。
// 返回值 长度符合要求时为真。
func validPassword(password string) bool {
	return len(password) <= 256 && utf8.RuneCountInString(password) >= 12
}

// newPassword 生成随机盐和当前算法派生的密码摘要。
// 参数 password 为本次创建或更换的口令。
// 返回值 随机盐、摘要以及随机数或算法错误。
func newPassword(password string) ([]byte, []byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, nil, err
	}
	hash, err := derive(password, salt)
	return salt, hash, err
}

// derive 以 PBKDF2-HMAC-SHA256 派生密码摘要。
// 参数 password 和 salt 为口令与账号的独立随机盐。
// 返回值 固定长度摘要或算法错误。
func derive(password string, salt []byte) ([]byte, error) {
	return pbkdf2.Key(sha256.New, password, salt, 600_000, 32)
}

// passwordMatches 对登录密码做固定工作量比较。
// 参数 password 为候选口令，salt 和 hash 为账户的存储值。
// 返回值 密码与存储摘要一致时为真。
func passwordMatches(password string, salt, hash []byte) bool {
	candidate, err := derive(password, salt)
	return err == nil && subtle.ConstantTimeCompare(candidate, hash) == 1
}
