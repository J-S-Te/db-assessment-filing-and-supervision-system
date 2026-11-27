package hazards

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestExtendDeadlineUpdatesLedgerAndRollsBackWithHistory 验证延期、更改后的版本及原子留痕。
// 参数 t 为隔离数据库测试上下文。
// 返回值 无。
func TestExtendDeadlineUpdatesLedgerAndRollsBackWithHistory(t *testing.T) {
	s, router := ledgerFixture(t)
	path, closedPath, _ := syncedPaths(t, s, router)
	initial := ledgerReply[hazardDetail](t, ledgerCall(router, http.MethodGet, path, nil), 200)
	change := "2099-02-05"
	if err := attemptDeadline(t, s, initial.ID, initial.Deadline, "申请与当前期限相同"); !errors.Is(err, ErrDeadlineConflict) {
		t.Fatalf("same due date admitted: %v", err)
	}
	closed := ledgerReply[hazardDetail](t, ledgerCall(router, http.MethodGet, closedPath, nil), 200)
	if _, err := s.db.Exec(t.Context(), `UPDATE hazard_items SET status='closed',closed_at=now() WHERE id=$1`, closed.ID); err != nil {
		t.Fatal(err)
	}
	if err := attemptDeadline(t, s, closed.ID, change, "已办结事项申请延期"); !errors.Is(err, ErrDeadlineConflict) {
		t.Fatalf("closed hazard admitted: %v", err)
	}
	assertDeadlineAuditRollback(t, s, router, path, initial, change)
	reason := strings.Repeat("延", 1000)
	if err := attemptDeadline(t, s, initial.ID, change, reason); err != nil {
		t.Fatal(err)
	}
	updated := ledgerReply[hazardDetail](t, ledgerCall(router, http.MethodGet, path, nil), 200)
	operator := operatorBase + "/" + initial.ID + "/history?organization=" + url.QueryEscape("试点单位")
	history := ledgerReply[hazardCollection[hazardEvent]](t, ledgerCall(router, http.MethodGet, operator, nil), 200)
	if updated.Deadline != change || updated.Version != initial.Version+1 || len(history.Items) != 1 ||
		history.Items[0].Action != "extend_deadline" || history.Items[0].FromStatus != initial.Status ||
		history.Items[0].ToStatus != initial.Status || !strings.Contains(history.Items[0].Comment, change) ||
		!strings.HasSuffix(history.Items[0].Comment, reason) {
		t.Fatalf("invalid deadline/audit outcome: %+v %+v", updated, history)
	}
}

// attemptDeadline 通过延期服务的真实事务执行一次指定调整。
// 参数 t 为测试上下文，s 为隔离服务，id、date 与 reason 为隐患、日期及说明。
// 返回值 实际事务结果。
func attemptDeadline(t *testing.T, s *Service, id, date, reason string) error {
	t.Helper()
	ctx := t.Context()
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		return ExtendDeadline(ctx, tx, id, date, "延期测试席位", reason)
	})
}

// assertDeadlineAuditRollback 验证历史写入故障不会留下局部的延期状态。
// 参数 t 为测试上下文，s、router 为隔离服务和路由，path、initial 与 target 为原记录及新期限。
// 返回值 无。
func assertDeadlineAuditRollback(t *testing.T, s *Service, router http.Handler, path string, initial hazardDetail, target string) {
	t.Helper()
	if _, err := s.db.Exec(t.Context(), `ALTER TABLE hazard_events ADD CONSTRAINT test_stop_extension
		CHECK (action<>'extend_deadline')`); err != nil {
		t.Fatal(err)
	}
	if err := attemptDeadline(t, s, initial.ID, target, "测试留痕回滚"); err == nil || errors.Is(err, ErrDeadlineConflict) {
		t.Fatalf("did not observe audit insertion failure: %v", err)
	}
	current := ledgerReply[hazardDetail](t, ledgerCall(router, http.MethodGet, path, nil), 200)
	if current.Deadline != initial.Deadline || current.Version != initial.Version {
		t.Fatalf("partial deadline commit: %+v", current)
	}
	if _, err := s.db.Exec(t.Context(), `ALTER TABLE hazard_events DROP CONSTRAINT test_stop_extension`); err != nil {
		t.Fatal(err)
	}
}
