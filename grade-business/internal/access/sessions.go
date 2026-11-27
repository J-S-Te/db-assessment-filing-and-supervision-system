package access

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	sessionCookie = "grade_business_session"
	sessionHours  = 8
)

type Service struct {
	db    *pgxpool.Pool
	mu    sync.Mutex
	rates map[string]loginWindow
}

type loginWindow struct {
	until time.Time
	count int
}

const principalFields = `a.id::text,a.display_name,a.role_code,COALESCE(a.area_code,''),
    COALESCE(a.subject_id::text,''),COALESCE(p.scope_name,''),a.must_change_password`
const principalJoin = ` LEFT JOIN directory_parties p ON p.id=a.subject_id AND p.kind=a.subject_kind`
const loginEligible = `a.enabled AND (a.subject_id IS NULL OR
    (p.status='active' AND p.merged_into_id IS NULL
      AND (p.kind='unit' OR p.valid_until >= (statement_timestamp() AT TIME ZONE 'Asia/Shanghai')::date)))`

// New 创建基于数据库会话的权限服务。
// 参数 pool 为包含账号及业务主体名录的连接池。
// 返回值 可注册登录接口与权限网关的服务。
func New(pool *pgxpool.Pool) *Service {
	return &Service{db: pool, rates: make(map[string]loginWindow)}
}

// fields 提供当前会话身份查询的扫描目标。
// 参数 actor 为待填充的账号身份。
// 返回值 与 SQL 查询列顺序一致的字段指针。
func fields(actor *Principal) []any {
	return []any{&actor.ID, &actor.Name, &actor.Role, &actor.AreaCode, &actor.SubjectID, &actor.Subject, &actor.MustChange}
}

// authenticate 验证密码并重新核对账号及主体是否可用。
// 参数 ctx 为请求上下文，username、password 为提交的账号和密码。
// 返回值 当前账号身份及校验过的密码哈希，或统一凭据错误。
func (s *Service) authenticate(ctx context.Context, username, password string) (Principal, []byte, error) {
	var actor Principal
	salt, hash := make([]byte, 16), make([]byte, 32)
	err := s.db.QueryRow(ctx, "SELECT "+principalFields+
		",a.password_salt,a.password_hash FROM access_accounts a"+principalJoin+
		" WHERE a.username=$1 AND "+loginEligible, username).
		Scan(append(fields(&actor), &salt, &hash)...)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, nil, err
	}
	if !passwordMatches(password, salt, hash) || err != nil {
		return Principal{}, nil, errCredential
	}
	return actor, hash, nil
}

// issue 为通过认证的账号发行只在客户端存放明文的短期令牌。
// 参数 ctx、w、r 为请求环境，actor 和 fingerprint 为已验证的账号和口令版本。
// 返回值 会话写入或并发凭据变动错误。
func (s *Service) issue(ctx context.Context, w http.ResponseWriter, r *http.Request,
	actor Principal, fingerprint []byte,
) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	hash := sha256.Sum256(raw)
	tag, err := s.db.Exec(ctx, `
		INSERT INTO access_sessions(token_hash,account_id,expires_at)
		SELECT $1,a.id,statement_timestamp()+interval '8 hours'
		FROM access_accounts a WHERE a.id=$2 AND a.password_hash=$3 AND a.enabled FOR UPDATE OF a`,
		hash[:], actor.ID, fingerprint)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errCredential
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: base64.RawURLEncoding.EncodeToString(raw),
		Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode,
		MaxAge: sessionHours * 3600})
	return nil
}

// sessionToken 只接受固定长度、单一的会话 Cookie。
// 参数 r 为原始浏览器请求。
// 返回值 可用于数据库查找的摘要和格式校验结果。
func sessionToken(r *http.Request) ([32]byte, bool) {
	cookies := r.CookiesNamed(sessionCookie)
	if len(cookies) != 1 || len(cookies[0].Value) != 43 {
		return [32]byte{}, false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(cookies[0].Value)
	if err != nil || len(raw) != 32 {
		return [32]byte{}, false
	}
	return sha256.Sum256(raw), true
}

// session 返回仍有效的会话身份，核对名录资格和账号状态。
// 参数 r 为携带浏览器会话 Cookie 的请求。
// 返回值 当前身份，或无会话、资格变更、数据库错误。
func (s *Service) session(r *http.Request) (Principal, error) {
	var actor Principal
	hash, ok := sessionToken(r)
	if !ok {
		return actor, pgx.ErrNoRows
	}
	err := s.db.QueryRow(r.Context(), "SELECT "+principalFields+
		" FROM access_sessions t JOIN access_accounts a ON a.id=t.account_id"+principalJoin+
		" WHERE t.token_hash=$1 AND t.expires_at>statement_timestamp() AND "+loginEligible,
		hash[:]).Scan(fields(&actor)...)
	return actor, err
}

// limitLogin 限制账号口令校验的频率及计数器存量。
// 参数 remote 和 username 为连接来源与标准化用户名。
// 返回值 当前尝试在来源和账户的频率额度内时为真。
func (s *Service) limitLogin(remote, username string) bool {
	ip, _, err := net.SplitHostPort(remote)
	if err != nil {
		ip = remote
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, item := range s.rates {
		if !now.Before(item.until) {
			delete(s.rates, key)
		}
	}
	keys := []string{"ip:" + ip, "user:" + username}
	if len(s.rates) > 4094 || s.rates[keys[0]].count >= 60 || s.rates[keys[1]].count >= 12 {
		return false
	}
	for _, key := range keys {
		item := s.rates[key]
		if item.count == 0 {
			item.until = now.Add(5 * time.Minute)
		}
		item.count++
		s.rates[key] = item
	}
	return true
}

// record 将身份认证和权限拒绝写入独立安全审计表。
// 参数 ctx 为请求环境，actor 为可选的已验证账号，event、target 为事件类型及受控目标。
// 返回值 无。
func (s *Service) record(ctx context.Context, actor Principal, event, target string) {
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_, err := s.db.Exec(auditCtx, `
		INSERT INTO access_events(operator_id,role_code,event,target)
		VALUES (NULLIF($1,'')::uuid,NULLIF($2,''),$3,$4)`, actor.ID, actor.Role, event, strings.TrimSpace(target))
	if err != nil {
		slog.Error("身份权限审计写入失败", "event", event, "operatorId", actor.ID, "roleCode", actor.Role)
	}
}

// replacePassword 通过凭据版本比较原子地改密并注销存量会话。
// 参数 ctx 为请求环境，id 为当前账号，input 为旧、新口令。
// 返回值 凭据错误、并发变更或数据库错误，成功时为空。
func (s *Service) replacePassword(ctx context.Context, id string, input passwordInput) error {
	var oldSalt, oldHash []byte
	err := s.db.QueryRow(ctx, "SELECT password_salt,password_hash FROM access_accounts WHERE id=$1 AND enabled", id).
		Scan(&oldSalt, &oldHash)
	if err != nil {
		return err
	}
	if !passwordMatches(input.Current, oldSalt, oldHash) {
		return errCredential
	}
	salt, hash, err := newPassword(input.Next)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE access_accounts
		SET password_salt=$2,password_hash=$3,must_change_password=false
		WHERE id=$1 AND password_hash=$4 AND enabled`, id, salt, hash, oldHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errCredential
	}
	if _, err = tx.Exec(ctx, "DELETE FROM access_sessions WHERE account_id=$1", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
