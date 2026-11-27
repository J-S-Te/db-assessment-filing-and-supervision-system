package alerts

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestAlertExtensionApprovalReflectsCurrentHazard 验证不可办的在审延期只有驳回可用且显示实际原因。
// 参数 t 为真实隔离数据库测试上下文。
// 返回值 无。
func TestAlertExtensionApprovalReflectsCurrentHazard(t *testing.T) {
	for _, scenario := range []struct{ name, mutation, reason string }{
		{"hazard_closed", `UPDATE hazard_items SET status='closed', closed_at=now() WHERE id=$1`, "已销项"},
		{"deadline_changed", `UPDATE hazard_items SET deadline=(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date+30 WHERE id=$1`, "当前整改期限"},
		{"requested_date_elapsed", `UPDATE local_alert_dispositions SET requested_deadline=(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date WHERE hazard_id=$1`, "已到或已过"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			db, router := newAlertFixture(t)
			plan := seedAlertPlan(t, db)
			hazard := addAlertHazard(t, db, plan, 1, "high", "open", 5)
			path := syncSingleAlert(t, router)
			callOperatorAlert(t, router, path[len(endpoint)+1:], extensionBody(alertTestDate(t, db, 30)), 200)
			if _, err := db.Exec(t.Context(), scenario.mutation, hazard); err != nil {
				t.Fatal(err)
			}
			item := readAlertTest[struct {
				AllowedActions        []string
				ApprovalBlockedReason string
			}](t, router, "GET", path, "", 200)
			if !slices.Equal(item.AllowedActions, []string{"reject"}) || !strings.Contains(item.ApprovalBlockedReason, scenario.reason) {
				t.Fatalf("已不可批准的延期仍可办理，或原因缺失：%+v", item)
			}
			conflict := readAlertTest[map[string]string](t, router, "POST", path+"/actions",
				`{"action":"approve","expectedVersion":2,"comment":"尝试通过已失效延期"}`, 409)
			if conflict["code"] != "conflict" || conflict["message"] != item.ApprovalBlockedReason {
				t.Fatalf("直接批准的冲突与详情不一致：%+v / %+v", conflict, item)
			}
			readAlertTest[dispositionResponse](t, router, "POST", path+"/actions",
				`{"action":"reject","expectedVersion":2,"comment":"申请已不适用。"}`, 200)
		})
	}
}

// TestAlertSyncSkipsUncommittedSourceChanges 验证同步略过正在延期、销项或待回滚的真实隐患。
// 参数 t 为真实隔离数据库测试上下文。
// 返回值 无。
func TestAlertSyncSkipsUncommittedSourceChanges(t *testing.T) {
	for _, scenario := range []struct {
		name, update string
		commit       bool
		want         int64
	}{
		{"deadline_extended", `UPDATE hazard_items SET deadline=(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date+30 WHERE id=$1`, true, 0},
		{"hazard_closed", `UPDATE hazard_items SET status='closed',closed_at=now() WHERE id=$1`, true, 0},
		{"update_rolled_back", `UPDATE hazard_items SET deadline=(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date+30 WHERE id=$1`, false, 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			db, _ := newAlertFixture(t)
			plan := seedAlertPlan(t, db)
			hazard := addAlertHazard(t, db, plan, 1, "medium", "open", -5)
			tx, err := db.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			if _, err := tx.Exec(t.Context(), scenario.update, hazard); err != nil {
				t.Fatal(err)
			}
			service := New(db, "测试监管员")
			assertSyncCreated(t, service, 0)
			if scenario.commit {
				err = tx.Commit(t.Context())
			} else {
				err = tx.Rollback(t.Context())
			}
			if err != nil {
				t.Fatal(err)
			}
			assertSyncCreated(t, service, scenario.want)
		})
	}
}

// assertSyncCreated 有界调用真实采集服务并核对新增条数。
// 参数 t 为测试上下文，service 为已连接隔离数据库的服务，want 为期望新增条数。
// 返回值 无。
func assertSyncCreated(t *testing.T, service *Service, want int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	result, err := service.sync(ctx)
	if err != nil || result.Created != want {
		t.Fatalf("实际采集新增条数 = %d，期望 %d，错误 %v", result.Created, want, err)
	}
}
