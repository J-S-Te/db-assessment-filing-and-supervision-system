package cases

import (
	"net/http"
	"strings"

	"grade-business/internal/httpx"
	"grade-business/internal/reviewinbox"
)

// registerWithdrawals 注册运营者发起、监管决定及三端进度路由。
// 参数 mux 为业务 HTTP 路由。
// 返回值 无。
func (s *Service) registerWithdrawals(mux *http.ServeMux) {
	for _, resource := range []string{withdrawalFilings, withdrawalApplications} {
		path := "/" + resource + "/{id}/withdrawal"
		admin, institution, operator := "/api/v1"+path, "/api/v1/institution"+path, "/api/v1/operator"+path
		mux.HandleFunc("GET "+admin, s.withdrawalHTTP(resource, ""))
		mux.HandleFunc("POST "+admin+"/decision", s.withdrawalHTTP(resource, ""))
		mux.HandleFunc("GET "+institution, s.withdrawalHTTP(resource, "institution"))
		mux.HandleFunc("GET "+operator, s.withdrawalHTTP(resource, "organization"))
		mux.HandleFunc("POST "+operator, s.withdrawalHTTP(resource, "organization"))
	}
}

// withdrawalHTTP 按路由席位处理待审撤回读写。
// 参数 resource 指定业务，key 为必须的收件范围参数，监管为空。
// 返回值 严格校验业务输入的处理器。
func (s *Service) withdrawalHTTP(resource, key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, err := withdrawalRequestScope(r, resource, key)
		if err != nil {
			respondError(w, err)
			return
		}
		var item withdrawalView
		switch {
		case r.Method == http.MethodGet:
			item, err = s.readWithdrawal(r.Context(), scope)
		case key == "organization":
			item, err = s.requestWithdrawalHTTP(w, r, scope)
		default:
			item, err = s.decideWithdrawalHTTP(w, r, scope)
		}
		respond(w, http.StatusOK, item, err)
	}
}

// withdrawalRequestScope 核对目标标识和唯一归属参数。
// 参数 r 为请求，resource、key 对应当前路由和角色。
// 返回值 业务和范围参数，或可公开的校验错误。
func withdrawalRequestScope(r *http.Request, resource, key string) (withdrawalScope, error) {
	scope := withdrawalScope{resource: resource, id: r.PathValue("id")}
	if !validID(scope.id) {
		return scope, ErrNotFound
	}
	if key == "" {
		if r.URL.RawQuery != "" {
			return scope, ErrInvalid
		}
		return scope, nil
	}
	name, err := reviewinbox.Scope(r, key)
	if err != nil {
		return scope, ErrInvalid
	}
	if key == "organization" {
		scope.organization = name
	} else {
		scope.institution = name
	}
	return scope, nil
}

// requestWithdrawalHTTP 校验发起版本和必须提供的撤回原因。
// 参数 w、r 为响应与请求，scope 绑定操作单位。
// 返回值 可见的撤回回执，或字段、状态及业务错误。
func (s *Service) requestWithdrawalHTTP(w http.ResponseWriter, r *http.Request,
	scope withdrawalScope,
) (withdrawalView, error) {
	var input withdrawalChange
	if err := httpx.Decode(w, r, &input); err != nil {
		return withdrawalView{}, ErrInvalid
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.ExpectedVersion < 1 || input.Reason == "" || !validReviewComment(input.Reason) {
		return withdrawalView{}, ErrInvalid
	}
	return s.requestWithdrawal(r.Context(), scope, input)
}

// decideWithdrawalHTTP 校验指定申请的版本、决定和必填监管意见。
// 参数 w、r 为响应与请求，scope 指定待处理单据。
// 返回值 可见撤回回执，或字段及状态错误。
func (s *Service) decideWithdrawalHTTP(w http.ResponseWriter, r *http.Request,
	scope withdrawalScope,
) (withdrawalView, error) {
	var input withdrawalDecision
	if err := httpx.Decode(w, r, &input); err != nil {
		return withdrawalView{}, ErrInvalid
	}
	input.Comment = strings.TrimSpace(input.Comment)
	if !validID(input.WithdrawalID) || input.ExpectedVersion < 1 || input.Comment == "" ||
		!validReviewComment(input.Comment) || (input.Decision != approve && input.Decision != "reject") {
		return withdrawalView{}, ErrInvalid
	}
	return s.decideWithdrawal(r.Context(), scope, input)
}
