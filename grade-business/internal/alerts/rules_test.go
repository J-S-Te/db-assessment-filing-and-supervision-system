package alerts

import (
	"net/http"
	"testing"
)

const ruleTestPath = "/api/v1/alerts/rules"

// TestConfiguredAlertRulesRouteAndDedup 核对规则预览、过滤、重复保存和同一消息去重。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestConfiguredAlertRulesRouteAndDedup(t *testing.T) {
	db, mux := newAlertFixture(t)
	ids := seedOverdueRuleFilings(t, db)
	body := `{"name":"东城三级备案审核","trigger":"filing_review_overdue",
		"audience":"institution","district":"东城区","level":"3","enabled":true}`
	preview := readAlertTest[struct {
		Items []struct{ ResourceID, Recipient string }
	}](t, mux, http.MethodPost, ruleTestPath+"/preview", body, 200)
	if len(preview.Items) != 1 || preview.Items[0].ResourceID != ids[0] ||
		preview.Items[0].Recipient != "收件测评机构" {
		t.Fatalf("预警预览的范围不符：%+v", preview)
	}
	create := `{"requestId":"4d74067b-3dc9-411b-a43a-4672697529c1",` + body[1:]
	rule := readAlertTest[struct{ ID string }](t, mux, http.MethodPost, ruleTestPath, create, 201)
	replay := readAlertTest[struct{ ID string }](t, mux, http.MethodPost, ruleTestPath, create, 201)
	if rule.ID == "" || rule.ID != replay.ID {
		t.Fatalf("重复登记预警规则：%+v %+v", rule, replay)
	}
	readAlertTest[map[string]any](t, mux, http.MethodPost, ruleTestPath,
		`{"requestId":"e1a6395a-41dd-4755-a98b-71f817d3e6af","name":"东城同类规则",
		"trigger":"filing_review_overdue","audience":"institution","district":"东城区","level":"3","enabled":true}`, 201)
	if _, err := db.Exec(t.Context(), "EXPLAIN "+ruleSweepSQL, 3, 5); err != nil {
		t.Fatal(err)
	}
	assertRuleSweep(t, mux, 2, 1)
	assertRuleSweep(t, mux, 0, 0)
	var count int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM review_notices
		WHERE resource_id=$1 AND recipient='收件测评机构' AND state='deadline_overdue'`, ids[0]).Scan(&count); err != nil || count != 1 {
		t.Fatalf("同类规则没有合并站内投递：%d %v", count, err)
	}
}

// assertRuleSweep 核对本次新增命中和首次送达数。
// 参数 t、mux、matched、notified 为测试、路由及期望新增计数。
// 返回值 无。
func assertRuleSweep(t *testing.T, mux http.Handler, matched, notified int) {
	t.Helper()
	got := readAlertTest[struct{ Matched, Notified int }](t, mux, http.MethodPost, ruleTestPath+"/sync", "", 200)
	if got.Matched != matched || got.Notified != notified {
		t.Fatalf("规则扫描 = %+v，期望 %d 条命中、%d 条通知", got, matched, notified)
	}
}

// seedOverdueRuleFilings 生成同阶段不同区县的两个真实备案任务。
// 参数 t 为测试上下文，db 是该测试专用的业务数据库。
// 返回值 区县匹配和不匹配的备案标识。
func seedOverdueRuleFilings(t *testing.T, db summaryReader) [2]string {
	t.Helper()
	var ids [2]string
	for index, item := range []struct{ name, district, code, number string }{
		{"辖内单位", "东城区", "913300000000002001", "RULE-1"},
		{"旁辖单位", "西城区", "913300000000002002", "RULE-2"},
	} {
		err := db.QueryRow(t.Context(), `WITH entry AS (
			INSERT INTO directory_parties(kind,scope_name,name,code,district)
			VALUES ('unit',$1,$1,$2,$3) RETURNING id
		)
		INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,
			level,status,version,directory_unit_id,created_at)
		SELECT 'filing',$4,'预警范围测试系统',$1,'收件测评机构','3','pending_final',3,id,
			clock_timestamp()-interval '45 days' FROM entry RETURNING id::text`,
			item.name, item.code, item.district, item.number).Scan(&ids[index])
		if err != nil {
			t.Fatal(err)
		}
	}
	return ids
}
