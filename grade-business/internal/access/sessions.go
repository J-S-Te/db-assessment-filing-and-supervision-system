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
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	sessionCookie  = "grade_business_session"
	sessionHours   = 8
	maxRateSources = 4096
	rateDuration   = 5 * time.Minute
	idleTimeout    = 30 * time.Minute
)

type Service struct {
	db                *pgxpool.Pool
	options           Options
	mu                sync.Mutex
	loginRates        map[string]loginSourceWindow
	nextLoginCleanup  time.Time
	registrationRates map[string]loginWindow
}

// Options 指定部署端明确启用的会话传输、空闲超时与代理信任设置。
type Options struct {
	SecureCookies bool
	TrustedProxy  netip.Addr
	IdleTimeout   time.Duration
}

type loginWindow struct {
	until time.Time
	count int
}

type loginSourceWindow struct {
	loginWindow
	accounts         map[string]int
	reportedThrottle bool
}

const principalFields = `a.id::text,a.display_name,a.role_code,COALESCE(a.area_code,''),
    COALESCE(a.subject_id::text,''),COALESCE(p.scope_name,''),a.must_change_password`
const principalJoin = ` LEFT JOIN directory_parties p ON p.id=a.subject_id AND p.kind=a.subject_kind`
const loginEligible = `a.enabled AND (a.subject_id IS NULL OR
    (p.status='active' AND p.merged_into_id IS NULL
      AND (p.kind='unit' OR p.valid_until >= (statement_timestamp() AT TIME ZONE 'Asia/Shanghai')::date)))`

// New 创建基于数据库会话的权限服务。
// 参数 pool 为账号及名录连接池，options 为 Cookie、空闲超时和可信反代配置，未指定空闲超时采用 30 分钟。
// 返回值 可注册登录接口与权限网关的服务。
func New(pool *pgxpool.Pool, options ...Options) *Service {
	var cfg Options
	if len(options) > 0 {
		cfg = options[0]
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = idleTimeout
	}
	return &Service{db: pool, options: cfg,
		loginRates: make(map[string]loginSourceWindow), registrationRates: make(map[string]loginWindow)}
}

// fields 提供当前会话身份查询的扫描目标。
// 参数 actor 为待填充的账号身份。
// 返回值 与 SQL 查询列顺序一致的字段指针。
func fields(actor *Principal) []any {
	return []any{&actor.ID, &actor.Name, &actor.Role, &actor.AreaCode, &actor.SubjectID, &actor.Subject, &actor.MustChange}
}

// authenticate 验证凭据、账号可用性和登录辖区。
// 参数 ctx 为请求上下文，input 为提交的账号、密码和登录辖区。
// 返回值 当前账号身份及校验过的密码哈希，或统一凭据错误。
func (s *Service) authenticate(ctx context.Context, input loginInput) (Principal, []byte, error) {
	var actor Principal
	salt, hash := make([]byte, 16), make([]byte, 32)
	err := s.db.QueryRow(ctx, "SELECT "+principalFields+
		",a.password_salt,a.password_hash FROM access_accounts a"+principalJoin+
		" WHERE a.username=$1 AND "+loginEligible, input.Username).
		Scan(append(fields(&actor), &salt, &hash)...)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, nil, err
	}
	if !passwordMatches(input.Password, salt, hash) || err != nil {
		return Principal{}, nil, errCredential
	}
	matches := input.AreaCode == ""
	if actor.Role == Regulator {
		matches = districtNames[input.AreaCode] != "" && input.AreaCode == actor.AreaCode
	}
	if !matches {
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
		FROM access_accounts a WHERE a.id=$2 AND a.password_hash=$3 AND a.enabled
		AND a.role_code=$4 AND COALESCE(a.area_code,'')=$5 FOR UPDATE OF a`,
		hash[:], actor.ID, fingerprint, actor.Role, actor.AreaCode)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errCredential
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: base64.RawURLEncoding.EncodeToString(raw),
		Path: "/", HttpOnly: true, Secure: s.options.SecureCookies || r.TLS != nil, SameSite: http.SameSiteStrictMode,
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

// session 核对有效期、账号及名录资格，同时原子记录最近活跃时间。
// 参数 r 为携带浏览器会话 Cookie 的请求。
// 返回值 当前身份，或无会话、资格变更、数据库错误。
func (s *Service) session(r *http.Request) (Principal, error) {
	var actor Principal
	hash, ok := sessionToken(r)
	if !ok {
		return actor, pgx.ErrNoRows
	}
	err := s.db.QueryRow(r.Context(), `
		UPDATE access_sessions t
		SET last_seen_at=GREATEST(t.last_seen_at,statement_timestamp())
		FROM access_accounts a`+principalJoin+`
		WHERE t.account_id=a.id AND t.token_hash=$1 AND t.expires_at>statement_timestamp()
		  AND t.last_seen_at>statement_timestamp()-make_interval(secs => $2) AND `+loginEligible+
		` RETURNING `+principalFields, hash[:], s.options.IdleTimeout.Seconds()).Scan(fields(&actor)...)
	return actor, err
}

// limitLogin 限制账号口令校验的频率及计数器存量。
// 参数 remote 和 username 为连接来源与标准化用户名。
// 返回值 本次尝试是否允许，以及是否为当前来源在窗口内首个值得记录的限流事件。
func (s *Service) limitLogin(remote, username string) (allowed, reportThrottle bool) {
	now, source := time.Now(), peerIP(remote)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !now.Before(s.nextLoginCleanup) {
		for key, window := range s.loginRates {
			if !now.Before(window.until) {
				delete(s.loginRates, key)
			}
		}
		s.nextLoginCleanup = now.Add(time.Minute)
	}
	window, exists := s.loginRates[source]
	if !exists && len(s.loginRates) >= maxRateSources {
		return false, false
	}
	if !now.Before(window.until) {
		window = loginSourceWindow{
			loginWindow: loginWindow{until: now.Add(rateDuration)}, accounts: make(map[string]int),
		}
	}
	if window.count >= 60 || window.accounts[username] >= 12 {
		report := !window.reportedThrottle
		window.reportedThrottle = true
		s.loginRates[source] = window
		return false, report
	}
	window.count++
	window.accounts[username]++
	s.loginRates[source] = window
	return true, false
}

// limitRegistration 限制匿名入口访问频率，避免影响正常登录的独立额度。
// 参数 remote 为连接来源，operation 为注册操作，threshold 为短时请求阈值。
// 返回值 本次匿名请求可继续时为真。
func (s *Service) limitRegistration(remote, operation string, threshold int) bool {
	key, now := operation+":"+peerIP(remote), time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for source, window := range s.registrationRates {
		if !now.Before(window.until) {
			delete(s.registrationRates, source)
		}
	}
	window, exists := s.registrationRates[key]
	if window.count >= threshold || (!exists && len(s.registrationRates) >= maxRateSources) {
		return false
	}
	if !exists {
		window.until = now.Add(rateDuration)
	}
	window.count++
	s.registrationRates[key] = window
	return true
}

// peerIP 获取 TCP 来源地址，避免以可伪造的请求头作限流依据。
// 参数 remote 为连接提供的远端地址及可选端口。
// 返回值 来源主机地址。
func peerIP(remote string) string {
	ip, _, err := net.SplitHostPort(remote)
	if err != nil {
		return remote
	}
	return ip
}

// authSource 为限流提取可信客户端地址。
// 参数 r 为身份入口请求，可信反代必须覆盖 X-Real-IP。
// 返回值 受信的代理来源地址，或实际 TCP 连接地址。
func (s *Service) authSource(r *http.Request) string {
	direct := peerIP(r.RemoteAddr)
	if !s.options.TrustedProxy.IsValid() {
		return direct
	}
	peer, err := netip.ParseAddr(direct)
	if err != nil || peer.Unmap() != s.options.TrustedProxy.Unmap() {
		return direct
	}
	values := r.Header.Values("X-Real-IP")
	if len(values) != 1 {
		return direct
	}
	client, err := netip.ParseAddr(values[0])
	if err != nil || client.Zone() != "" || client.IsUnspecified() {
		return direct
	}
	return client.Unmap().String()
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
