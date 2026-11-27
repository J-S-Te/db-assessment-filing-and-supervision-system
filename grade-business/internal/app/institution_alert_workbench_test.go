package app

import (
	"context"
	"testing"
)

// TestInstitutionWorkbenchIncludesAssignedAlert 验证预警仅进入实际承办机构的待办。
// 参数 t 为真实隔离数据库测试上下文。
// 返回值 无。
func TestInstitutionWorkbenchIncludesAssignedAlert(t *testing.T) {
	ctx := context.Background()
	db := openWorkbenchTestDB(t, ctx)
	var id string
	err := db.QueryRow(ctx, `WITH f AS (
		INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status)
		VALUES ('filing','ALERT-WB','预警系统','业务单位','当前备案机构','2','approved') RETURNING id
	), h AS (
		INSERT INTO hazard_items(registration_filing_id,ledger,title,source,discovered_on,severity,description,
		  measure,responsibility,deadline,status,retest_institution,dispatched_at)
		SELECT id,'major_risk','预警检查','manual',CURRENT_DATE,'high','需核实','提交结果','值班组',
		  CURRENT_DATE,'open','派发机构',clock_timestamp() FROM f RETURNING id
	)
	INSERT INTO local_alerts(hazard_id,kind,severity,system_name,organization_name,deadline)
	SELECT id,'high_risk','high','预警系统','业务单位',CURRENT_DATE FROM h RETURNING id::text`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := readScopedWorkbench(ctx, db, ReviewWindows{}, queueScope{"institution", "派发机构"})
	if err != nil || len(assigned.Tasks) != 1 || assigned.Tasks[0].ID != id || assigned.Counts.Pending != 1 {
		t.Fatalf("已分派机构工作台缺少预警 %s，待办：%+v，错误：%v", id, assigned.Tasks, err)
	}
	other, err := readScopedWorkbench(ctx, db, ReviewWindows{}, queueScope{"institution", "当前备案机构"})
	if err != nil || len(other.Tasks) != 0 {
		t.Fatalf("未分派机构可见其他机构预警，待办：%+v，错误：%v", other.Tasks, err)
	}
}
