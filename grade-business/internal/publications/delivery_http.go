package publications

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"grade-business/internal/httpx"
)

// handleRecipients 获取可作为公告收件方的当前有效单位或机构。
// 参数 w 为响应对象，r 为带角色与搜索条件的请求。
// 返回值 无。
func (s *Service) handleRecipients(w http.ResponseWriter, r *http.Request) {
	values, err := publicationQuery(r.URL.RawQuery, "audience", "q")
	if err != nil {
		respond(w, 0, nil, err)
		return
	}
	audience, query, err := parseRecipientSearch(values, true)
	if err != nil {
		respond(w, 0, nil, err)
		return
	}
	out, err := s.readRecipients(r.Context(), audience, query)
	respond(w, http.StatusOK, out, err)
}

// handleDeliveries 返回指定公告某次发布的监管送达和回执。
// 参数 w 为响应对象，r 为带版本与筛选条件的请求，id 为公告标识。
// 返回值 无。
func (s *Service) handleDeliveries(w http.ResponseWriter, r *http.Request, id pgtype.UUID) {
	filter, err := parseDeliveryFilter(r.URL.RawQuery)
	if err != nil {
		respond(w, 0, nil, err)
		return
	}
	out, err := s.readDeliveries(r.Context(), id, filter)
	respond(w, http.StatusOK, out, err)
}

// handleReceipt 生成一项门户已读或明确签收的操作处理器。
// 参数 audience 为门户角色，ack 为是否明确签收。
// 返回值 接受公告标识的处理器。
func (s *Service) handleReceipt(audience string, ack bool) func(http.ResponseWriter, *http.Request, pgtype.UUID) {
	return func(w http.ResponseWriter, r *http.Request, id pgtype.UUID) {
		scope, err := s.readerScope(r, audience, true)
		if err != nil {
			respond(w, 0, nil, err)
			return
		}
		var input receiptInput
		if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
			respond(w, 0, nil, errInvalid)
			return
		}
		out, err := s.updateReceipt(r.Context(), id, input.ExpectedVersion, scope, ack)
		respond(w, http.StatusOK, out, err)
	}
}

// handleUnreadCount 生成当前收件方未读计数的读取处理器。
// 参数 audience 为门户角色。
// 返回值 查阅该门户未读数量的处理器。
func (s *Service) handleUnreadCount(audience string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, err := s.readerScope(r, audience, true)
		if err != nil {
			respond(w, 0, nil, err)
			return
		}
		count, err := s.readUnreadCount(r.Context(), scope)
		respond(w, http.StatusOK, map[string]int64{"count": count}, err)
	}
}

// handleReadAll 生成当前收件方全部有效公告的批量已读处理器。
// 参数 audience 为门户角色。
// 返回值 执行该门户批量已读的处理器。
func (s *Service) handleReadAll(audience string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, err := s.readerScope(r, audience, true)
		if err != nil {
			respond(w, 0, nil, err)
			return
		}
		if r.ContentLength != 0 && httpx.Decode(w, r, &struct{}{}) != nil {
			respond(w, 0, nil, errInvalid)
			return
		}
		updated, err := s.markAllRead(r.Context(), scope)
		respond(w, http.StatusOK, map[string]int64{"updated": updated}, err)
	}
}

// readerScope 解析门户传入的范围，并接受目录中同一对象的当前展示名称。
// 参数 r 为访问请求，audience 为角色，required 表示必须指定范围。
// 返回值 收件业务范围，或输入及目录查询错误。
func (s *Service) readerScope(r *http.Request, audience string, required bool) (recipient, error) {
	scope, err := parseScope(r.URL.RawQuery, audience, required)
	if err != nil {
		return recipient{}, err
	}
	return s.canonicalRecipient(r.Context(), scope)
}
