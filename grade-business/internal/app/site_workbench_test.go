package app

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestSiteFollowUpWorkbenchHandoff 验证独立现场待办、原单位范围和提交后的监管复核。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestSiteFollowUpWorkbenchHandoff(t *testing.T) {
	db := openWorkbenchTestDB(t, context.Background())
	id, due := seedSiteWorkbench(t, db)
	mux := http.NewServeMux()
	RegisterWorkbenchOverview(mux, db, ReviewWindows{})
	own := "?" + url.Values{"organization": {"原现场单位"}}.Encode()
	other := "?" + url.Values{"organization": {"其他现场单位"}}.Encode()
	operatorPath := "/api/v1/operator/workbench"
	adminPath := "/api/v1/workbench/overview"
	site := "/deadlines/supervision-site/" + id

	operator := readApplicantDeadline[scopedWorkbench](t, mux, operatorPath+own, 200)
	task, exists := findSiteTask(operator.Tasks, id)
	if !exists || task.Status != "following_up" || task.Deadline != due ||
		task.Urgency != "near" || task.CanRemind || operator.Counts.Pending != 2 {
		t.Fatalf("运营者的自查与现场待办须独立：%+v", operator)
	}
	assertSiteWorkbenchIsolation(t, mux, id, operatorPath, other)
	details := readApplicantDeadline[deadlineDetail](t, mux, operatorPath+site+own, 200)
	if details.Deadline != due || details.Label != "现场检查跟进" || details.WaitingFor != "" {
		t.Fatalf("时限详情与待办不一致：%+v", details)
	}
	setSiteWorkbenchPhase(t, db, id, "follow_up_submitted")
	regulator := readApplicantDeadline[overview](t, mux, adminPath, 200)
	review, exists := findSiteTask(regulator.Tasks, id)
	if !exists || review.Status != "follow_up_submitted" || review.Deadline != "" {
		t.Fatalf("提交后应进入监管复核，沿用实际办理口径：%+v", regulator.Tasks)
	}
	assertSiteWorkbenchIsolation(t, mux, id, operatorPath, own)
	setSiteWorkbenchPhase(t, db, id, "closed")
	regulator = readApplicantDeadline[overview](t, mux, adminPath, 200)
	if _, exists := findSiteTask(regulator.Tasks, id); exists {
		t.Fatalf("办结后仍有现场任务：%+v", regulator.Tasks)
	}
}

// seedSiteWorkbench 创建同一备案同时具有年度自查和现场跟进的正常数据。
// 参数 t 和 db 为隔离测试环境。
// 返回值 自查和现场共用的任务标识及现场截止日期。
func seedSiteWorkbench(t *testing.T, db *pgxpool.Pool) (string, string) {
	t.Helper()
	var id, due string
	err := db.QueryRow(t.Context(), `
	WITH f AS (
	 INSERT INTO business_cases(kind,number,system_name,organization_name,assessment_org,level,status)
	 VALUES ('filing','SITE-HANDOFF','现场待办系统','原现场单位','测试机构','3','approved') RETURNING *
	), d AS (SELECT timezone('Asia/Shanghai',CURRENT_TIMESTAMP)::date AS day),
	task AS (
	 INSERT INTO supervision_tasks(request_id,filing_id,filing_number,system_name,
	   organization_name,title,due_on,cycle_year,status,version,published_at)
	 SELECT gen_random_uuid(),id,number,system_name,organization_name,'自查任务',
	   day,EXTRACT(year FROM day),'open',4,clock_timestamp() FROM f CROSS JOIN d RETURNING id
	), site AS (
	 INSERT INTO supervision_site_inspections
	   (task_id,status,version,visit_at,location,inspectors,outcome,follow_up_due_on)
	 SELECT id,'following_up',7,clock_timestamp(),'现场','检查组','follow_up_required',day
	 FROM task CROSS JOIN d RETURNING task_id,follow_up_due_on
	)
	INSERT INTO supervision_site_events(task_id,version,action,status,actor,input_hash)
	SELECT task_id,6,'follow_up','following_up','监管',repeat('a',64) FROM site
	RETURNING task_id::text,(SELECT follow_up_due_on::text FROM site)
	`).Scan(&id, &due)
	if err != nil {
		t.Fatal(err)
	}
	return id, due
}

// setSiteWorkbenchPhase 将隔离夹具推进到已提供证据的复核阶段或办结阶段。
// 参数 t、db 为测试环境，id 为任务标识，state 为要检查的阶段。
// 返回值 无。
func setSiteWorkbenchPhase(t *testing.T, db *pgxpool.Pool, id, state string) {
	t.Helper()
	_, err := db.Exec(t.Context(), `WITH moved AS (
	 UPDATE supervision_site_inspections SET version=version+2,status=$2
	 WHERE task_id=$1 RETURNING task_id,version,status
	), evidence AS (
	 INSERT INTO supervision_site_attachments(task_id,kind,file_name,file_key,digest,size_bytes,version)
	 SELECT task_id,'follow_up_evidence','整改.pdf',repeat('a',48),repeat('a',64),128,version-1
	 FROM moved WHERE status='follow_up_submitted' RETURNING task_id,id
	)
	INSERT INTO supervision_site_events(task_id,version,action,status,actor,input_hash,evidence_id)
	SELECT m.task_id,m.version,CASE WHEN m.status='closed' THEN 'close' ELSE 'submit_follow_up' END,
	   m.status,'测试席位',repeat('a',64),e.id FROM moved m LEFT JOIN evidence e USING(task_id)`, id, state)
	if err != nil {
		t.Fatal(err)
	}
}

// assertSiteWorkbenchIsolation 核对当前查询不显示已移出或不属于当前单位的现场待办。
// 参数 t、mux、id、path、scope 分别为测试、路由、任务标识、查询入口和范围参数。
// 返回值 无。
func assertSiteWorkbenchIsolation(t *testing.T, mux http.Handler, id, path, scope string) {
	t.Helper()
	list := readApplicantDeadline[scopedWorkbench](t, mux, path+scope, 200)
	if _, exists := findSiteTask(list.Tasks, id); exists {
		t.Fatalf("当前范围看到了无权办理的现场待办：%+v", list.Tasks)
	}
	deadline := readApplicantDeadline[deadlineDetail](t, mux, path+"/deadlines/supervision-site/"+id+scope, 200)
	if deadline != (deadlineDetail{}) {
		t.Fatalf("当前范围看到了无权办理的期限：%+v", deadline)
	}
}

// findSiteTask 为测试返回某监督任务下独立的现场办理阶段。
// 参数 tasks 为统一待办列表，id 为现场所属监督任务。
// 返回值 找到的办理信息及是否有该待办。
func findSiteTask(tasks []overviewTask, id string) (overviewTask, bool) {
	for _, task := range tasks {
		if task.ID == id && task.Resource == "supervision-site" {
			return task, true
		}
	}
	return overviewTask{}, false
}
