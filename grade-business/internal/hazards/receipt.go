package hazards

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/httpx"
)

const hazardReceiptEvent = `(e.action='submit' OR (e.action='close' AND e.to_status='closed'))`

var errReceiptUnavailable = errors.New("hazard has no receipt event")

// handleOperatorReceipt 附件形式发送经归属校验的真实整改提交或销项回执。
// 参数 w 为下载响应，r 携带运营单位与隐患标识。
// 返回值 无。
func (s *Service) handleOperatorReceipt(w http.ResponseWriter, r *http.Request) {
	body, err := s.operatorReceipt(r.Context(), r.PathValue("id"))
	if errors.Is(err, errReceiptUnavailable) {
		httpx.Error(w, http.StatusConflict, "receipt_unavailable", "提交复核后可下载整改回执。")
		return
	}
	if err != nil {
		respondError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=hazard-receipt.txt")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, body)
}

// operatorReceipt 读取调用方最近一条回执及其已有的原备案快照。
// 参数 ctx 含已验证运营单位，id 为隐患标识。
// 返回值 回执文字，或归属、事件缺失及数据库错误。
func (s *Service) operatorReceipt(ctx context.Context, id string) (string, error) {
	var number, system, action string
	var at *time.Time
	var version *int64
	err := s.db.QueryRow(ctx, `SELECT COALESCE(event.merge_history_number,c.filing_number),
		COALESCE(event.merge_history_system_name,c.system_name),COALESCE(event.action,''),event.occurred_at,event.version`+
		hazardJoin+` LEFT JOIN LATERAL (SELECT e.action,e.occurred_at,e.version,e.merge_history_number,
			e.merge_history_system_name FROM hazard_events e
		WHERE e.hazard_id=h.id AND COALESCE(e.merge_history_scope,c.organization_name)=$2
		  AND `+hazardReceiptEvent+` ORDER BY e.version DESC LIMIT 1) event ON true
		WHERE h.id=$1 AND h.status<>'pending_dispatch' AND (c.organization_name=$2 OR event.version IS NOT NULL)`, id, operatorOrganization(ctx)).
		Scan(&number, &system, &action, &at, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errNotFound
	}
	if err != nil {
		return "", err
	}
	if at == nil || version == nil {
		return "", errReceiptUnavailable
	}
	title := "整改提交回执"
	if action == actionClose {
		title = "隐患销项回执"
	}
	return fmt.Sprintf("%s\n隐患编号：%s\n备案编号：%s\n系统名称：%s\n办理版本：%d\n记录时间：%s\n",
		title, id, strings.Join(strings.Fields(number), " "), strings.Join(strings.Fields(system), " "),
		*version, at.Format(time.RFC3339)), nil
}
