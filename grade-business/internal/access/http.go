package access

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/httpx"
)

const authRoot = "/api/v1/auth"
const loginPattern = "POST " + authRoot + "/login"
const loginAreasPattern = "GET " + authRoot + "/login-areas"
const logoutPattern = "POST " + authRoot + "/logout"
const sessionPattern = "GET " + authRoot + "/session"
const passwordPattern = "POST " + authRoot + "/password"

type loginInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
	AreaCode string `json:"areaCode"`
}

type passwordInput struct {
	Current string `json:"currentPassword"`
	Next    string `json:"newPassword"`
}

// Protect 挂载认证路由并统一保护所有业务 API。
// 参数 mux 为已注册业务与页面的应用路由。
// 返回值 先验证会话与权限，再派发到原路由的处理器。
func (s *Service) Protect(mux *http.ServeMux) http.Handler {
	mux.HandleFunc(loginPattern, s.login)
	mux.HandleFunc(loginAreasPattern, loginAreas)
	mux.HandleFunc(sessionPattern, s.current)
	mux.HandleFunc(aiReviewContextPattern, s.current)
	mux.HandleFunc(logoutPattern, s.logout)
	mux.HandleFunc(passwordPattern, s.changePassword)
	mux.HandleFunc(institutionAccountPattern, s.createInstitutionAccount)
	s.registerOperatorRegistration(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := mux.Handler(r)
		switch pattern {
		case loginPattern, loginAreasPattern, registrationSubmitPattern, registrationStatusPattern, registrationAreasPattern:
			mux.ServeHTTP(w, r)
			return
		}
		if !isAIReviewPattern(pattern) && !strings.HasPrefix(r.URL.Path, "/api/") && !strings.Contains(pattern, " /api/") {
			mux.ServeHTTP(w, r)
			return
		}
		s.authorize(w, r, mux, pattern)
	})
}

// authorize 为业务接口核对会话、首登改密状态及角色和主体权限。
// 参数 w、r 为 HTTP 环境，next 为原路由，pattern 为匹配的接口模式。
// 返回值 无。
func (s *Service) authorize(w http.ResponseWriter, r *http.Request, next http.Handler, pattern string) {
	w.Header().Set("Cache-Control", "no-store")
	actor, err := s.session(r)
	if errors.Is(err, pgx.ErrNoRows) {
		if isAIReviewPattern(pattern) && !strings.HasPrefix(r.URL.Path, "/api/") {
			http.Redirect(w, r, "/login?redirect=%2Freview%2F", http.StatusSeeOther)
			return
		}
		httpx.Error(w, 401, "authentication_required", "请登录后再办理。")
		return
	}
	if err != nil {
		httpx.Error(w, 503, "session_unavailable", "暂时无法校验登录状态。")
		return
	}
	ctx := context.WithValue(r.Context(), principalKey{}, actor)
	switch pattern {
	case sessionPattern, logoutPattern, passwordPattern:
		next.ServeHTTP(w, r.WithContext(ctx))
		return
	}
	if actor.MustChange {
		s.record(ctx, actor, "denied", pattern)
		if isAIReviewPattern(pattern) && !strings.HasPrefix(r.URL.Path, "/api/") {
			http.Redirect(w, r, "/account/password", http.StatusSeeOther)
			return
		}
		httpx.Error(w, 403, "password_change_required", "请先修改初始密码。")
		return
	}
	if !allowed(actor, pattern) || !bindSubject(r, actor, pattern) {
		s.record(ctx, actor, "denied", pattern)
		httpx.Error(w, 403, "permission_denied", "当前账号无权访问此业务。")
		return
	}
	next.ServeHTTP(w, r.WithContext(ctx))
}

// login 验证账号和口令，为通过认证的用户建立会话。
// 参数 w、r 为响应及请求，输入包括账号、密码和监管员选择的登录辖区。
// 返回值 无。
func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	var input loginInput
	if httpx.Decode(w, r, &input) != nil {
		httpx.Error(w, 400, "invalid_input", "请填写账号和密码。")
		return
	}
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	validUsername := usernamePattern.MatchString(input.Username)
	rateKey := input.Username
	if !validUsername {
		rateKey = ""
	}
	allowed, reportThrottle := s.limitLogin(s.authSource(r), rateKey)
	if !allowed {
		if reportThrottle {
			s.record(r.Context(), Principal{}, "login_throttled", "login")
		}
		w.Header().Set("Retry-After", "300")
		httpx.Error(w, 429, "login_throttled", "登录尝试过多，请五分钟后重试。")
		return
	}
	if !validUsername || !validPassword(input.Password) {
		s.record(r.Context(), Principal{}, "login_failed", "login")
		rejectLogin(w)
		return
	}
	actor, fingerprint, err := s.authenticate(r.Context(), input)
	if err == nil {
		err = s.issue(r.Context(), w, r, actor, fingerprint)
	}
	if errors.Is(err, errCredential) {
		s.record(r.Context(), Principal{}, "login_failed", "login")
		rejectLogin(w)
		return
	}
	if err != nil {
		httpx.Error(w, 503, "login_unavailable", "暂时无法登录，请稍后重试。")
		return
	}
	s.record(r.Context(), actor, "login", "login")
	httpx.JSON(w, 200, view(actor))
}

// rejectLogin 以统一提示拒绝密码、账号状态或辖区不匹配的请求。
// 参数 w 为当前登录响应。
// 返回值 无。
func rejectLogin(w http.ResponseWriter) {
	httpx.Error(w, 401, "invalid_credentials", "账号、密码或登录辖区不匹配，或账号不可用。")
}

// current 返回当前会话的角色、数据范围、菜单和权限。
// 参数 w、r 为 HTTP 环境，账号来自已经过验证的会话。
// 返回值 无。
func (s *Service) current(w http.ResponseWriter, r *http.Request) {
	actor, _ := Current(r.Context())
	httpx.JSON(w, 200, view(actor))
}

// logout 撤销当前会话并清除浏览器 Cookie。
// 参数 w、r 为当前 HTTP 环境。
// 返回值 无。
func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	hash, _ := sessionToken(r)
	actor, _ := Current(r.Context())
	if _, err := s.db.Exec(r.Context(), "DELETE FROM access_sessions WHERE token_hash=$1", hash[:]); err != nil {
		httpx.Error(w, 503, "logout_unavailable", "暂时无法退出，请重试。")
		return
	}
	s.record(r.Context(), actor, "logout", "session")
	s.clearCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// changePassword 校验旧密码、保存新摘要并撤销该账号的全部会话。
// 参数 w、r 为 HTTP 环境，输入包含当前密码和新密码。
// 返回值 无。
func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) {
	var input passwordInput
	if httpx.Decode(w, r, &input) != nil || !strongPassword(input.Next) ||
		!validPassword(input.Current) || input.Current == input.Next {
		httpx.Error(w, 400, "invalid_password",
			"请填写当前密码，新密码至少 12 位且含大小写字母、数字、符号四类中的三类，并须与当前密码不同。")
		return
	}
	actor, _ := Current(r.Context())
	allowed, reportThrottle := s.limitLogin(s.authSource(r), actor.ID)
	if !allowed {
		if reportThrottle {
			s.record(r.Context(), actor, "password_change_throttled", "password")
		}
		httpx.Error(w, 429, "login_throttled", "口令校验次数过多，请五分钟后重试。")
		return
	}
	err := s.replacePassword(r.Context(), actor.ID, input)
	if errors.Is(err, errCredential) {
		s.record(r.Context(), actor, "password_change_failed", "password")
		httpx.Error(w, 400, "invalid_credentials", "当前密码无效。")
		return
	}
	if err != nil {
		httpx.Error(w, 503, "password_unavailable", "暂时无法修改密码，请重试。")
		return
	}
	s.record(r.Context(), actor, "password_changed", "password")
	s.clearCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// clearCookie 让浏览器删除当前应用的会话 Cookie。
// 参数 w、r 为响应及用于识别传输方式的请求。
// 返回值 无。
func (s *Service) clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.options.SecureCookies || r.TLS != nil, SameSite: http.SameSiteStrictMode})
}
