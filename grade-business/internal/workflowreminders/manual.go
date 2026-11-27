package workflowreminders

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/httpx"
	"grade-business/internal/reviewinbox"
)

var errRetestUnavailable = errors.New("retest no longer available")

// Register 注册运营者工作台的复测催办操作。
// 参数 mux 为统一业务路由，pool 为业务数据库连接池。
// 返回值 无。
func Register(mux *http.ServeMux, pool *pgxpool.Pool) {
	mux.HandleFunc("POST /api/v1/operator/workbench/retest-reminders", retestReminder(pool))
}

// retestReminder 校验唯一的运营单位范围与隐患标识，并返回催办结果。
// 参数 pool 为业务数据库连接池。
// 返回值 处理运营者复测催办请求的 HTTP 处理器。
func retestReminder(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		owner, err := reviewinbox.Scope(r, "organization")
		var input map[string]string
		var id pgtype.UUID
		if err != nil || httpx.Decode(w, r, &input) != nil || len(input) != 1 ||
			id.Scan(input["id"]) != nil || !strings.EqualFold(id.String(), input["id"]) || !id.Valid {
			httpx.Error(w, http.StatusBadRequest, "invalid_input", "请指定运营单位和一条隐患记录。")
			return
		}
		var sent bool
		err = pgx.BeginFunc(r.Context(), pool, func(tx pgx.Tx) error {
			var sendError error
			sent, sendError = sendRetestReminder(r.Context(), tx, owner, id)
			return sendError
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			httpx.Error(w, http.StatusNotFound, "not_found", "未找到该隐患。")
		case errors.Is(err, errRetestUnavailable):
			httpx.Error(w, http.StatusConflict, "retest_unavailable", "此隐患当前无法催办机构复测。")
		case err != nil:
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "暂时无法发送复测催办。")
		default:
			httpx.JSON(w, http.StatusOK, struct {
				Sent bool `json:"sent"`
			}{Sent: sent})
		}
	}
}

// sendRetestReminder 在同一事务持锁核对归属及状态，并向当前复测机构催办。
// 参数 ctx 为请求上下文，tx 为投递事务，owner 与 id 表示运营范围和隐患。
// 返回值 本次请求是否实际投递，以及查询、状态检查或写入错误。
func sendRetestReminder(ctx context.Context, tx pgx.Tx, owner string, id pgtype.UUID) (bool, error) {
	var status, institution, number, system string
	var canRetest bool
	var version int64
	err := tx.QueryRow(ctx, `SELECT h.status,h.version,
		CASE WHEN hc.report_id IS NULL THEN h.retest_institution IS NOT NULL ELSE anchor.status='approved' END,
		COALESCE(h.retest_institution,anchor.assessment_org),
		CASE WHEN hc.report_id IS NULL THEN f.number ELSE anchor.number END,f.system_name
		FROM hazard_items h JOIN hazard_context hc ON hc.id=h.id
		JOIN business_cases f ON f.id=hc.filing_id
		JOIN business_cases anchor ON anchor.id=COALESCE(hc.report_id,hc.filing_id)
		WHERE h.id=$1 AND h.status<>'pending_dispatch' AND f.organization_name COLLATE "C"=$2
		FOR UPDATE OF h FOR SHARE OF anchor,f`, id, owner).
		Scan(&status, &version, &canRetest, &institution, &number, &system)
	if err != nil {
		return false, err
	}
	if status != "pending_retest" || !canRetest || strings.TrimSpace(institution) == "" {
		return false, errRetestUnavailable
	}
	result, err := tx.Exec(ctx, `INSERT INTO review_notices
		(audience,resource,resource_id,event_version,state,recipient,reference,subject,comment,workflow_day)
		VALUES ('institution','hazards',$1,$2,'retest_reminder',$3,$4,$5,'运营者催办，请及时完成隐患复测。',
		    timezone('Asia/Shanghai',clock_timestamp())::date)
		ON CONFLICT (resource_id,workflow_day) WHERE state='retest_reminder' DO NOTHING`,
		id, version, institution, number, system)
	return result.RowsAffected() == 1, err
}
