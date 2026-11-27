package reporthistory

import (
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestOperatorAnnualTrendUsesLatestApprovedReportInShanghaiYear 核对年度边界和缺失数据语义。
// 参数 t 为隔离 PostgreSQL 和实际业务路由测试上下文。
// 返回值 无。
func TestOperatorAnnualTrendUsesLatestApprovedReportInShanghaiYear(t *testing.T) {
	pool, mux := historyFixture(t)
	filing := assessmentTestFiling(t, pool, "ANNUAL-01", "年度核对单位")
	insertHistoryReport(t, pool, filing, "年末旧报告", "approved", "high", "2023-12-31T15:00:00Z")
	insertHistoryReport(t, pool, filing, "跨年首份", "approved", "medium", "2023-12-31T18:00:00Z")
	latest := insertHistoryReport(t, pool, filing, "当年最新报告", "approved", "medium", "2024-12-30T18:00:00Z")
	insertHistoryReport(t, pool, filing, "在审不计入", "pending_final", "low", "2025-06-08T08:00:00Z")
	insertHistoryReport(t, pool, filing, "最新但未录入结论", "approved", "low", "2026-01-11T08:00:00Z")
	evidence := insertHistoryFile(t, pool, latest, "核验结论.pdf", "assessment_conclusion", "2024-12-30T18:00:00Z")
	postAssessmentResult(t, mux, "/api/v1/reports/"+latest+"/assessment-result", validAssessmentInput(evidence), 200)

	path := operatorHistoryTestPath + "/" + filing + "/trend?organization=" + url.QueryEscape("年度核对单位")
	result := readHistoryTest[struct {
		Items []struct {
			Year         int
			ReportNumber string
			Score        *float64
		}
	}](t, mux, path, 200)
	if len(result.Items) != 4 || result.Items[0].Year != 2023 ||
		result.Items[1].Year != 2024 || result.Items[1].ReportNumber != "当年最新报告" ||
		result.Items[1].Score == nil || *result.Items[1].Score != 88.5 ||
		result.Items[2].Year != 2025 || result.Items[2].ReportNumber != "" ||
		result.Items[3].Year != 2026 || result.Items[3].Score != nil {
		t.Fatalf("不正确的年度结果：%+v", result)
	}
	readHistoryTest[map[string]any](t, mux, path+"&limit=2", 400)
	readHistoryTest[map[string]any](t, mux, operatorHistoryTestPath+"/"+filing+"/trend?organization=Other", 404)
}

// TestOperatorIssueComparisonUsesOnlyReleasedApprovedRequirements 核对同归属的已批问题与原归属保护。
// 参数 t 为隔离库和 HTTP 测试上下文。
// 返回值 无。
func TestOperatorIssueComparisonUsesOnlyReleasedApprovedRequirements(t *testing.T) {
	pool, mux := historyFixture(t)
	filing := assessmentTestFiling(t, pool, "ANALYSIS-02", "问题核对单位")
	old := insertHistoryReport(t, pool, filing, "问题前期", "approved", "high", "2024-03-04T08:00:00Z")
	newer := insertHistoryReport(t, pool, filing, "问题后期", "approved", "low", "2025-03-04T08:00:00Z")
	absent := insertHistoryReport(t, pool, filing, "缺原件时期", "approved", "low", "2026-03-04T08:00:00Z")
	seedAnalyticsPlan(t, pool, old, "approved", `[
		{"description":"日志   留存不足","risk":"high"},{"description":"旧项已不再列出","risk":"medium"}]`)
	seedAnalyticsPlan(t, pool, newer, "approved", `[
		{"description":"日志 留存不足","risk":"low"},{"description":"新增的账号问题","risk":"medium"}]`)
	seedAnalyticsPlan(t, pool, newer, "draft", `[{"description":"不可见草稿","risk":"high"}]`)
	base := operatorHistoryTestPath + "/" + filing + "/issue-comparison?"
	unit := url.QueryEscape("问题核对单位")
	path := base + "organization=" + unit + "&from=" + old + "&to="
	type issueResult struct {
		FromAvailable, ToAvailable bool
		Items                      []struct{ Description, Change string }
	}
	result := readHistoryTest[issueResult](t, mux, path+newer, 200)
	changes := make(map[string]string)
	for _, item := range result.Items {
		changes[item.Description] = item.Change
	}
	if !result.FromAvailable || !result.ToAvailable || len(result.Items) != 3 ||
		changes["日志 留存不足"] != "recurring" || changes["旧项已不再列出"] != "not_listed" ||
		changes["新增的账号问题"] != "added" {
		t.Fatalf("不正确的整改问题对比：%+v", result)
	}
	result = readHistoryTest[issueResult](t, mux, path+absent, 200)
	if !result.FromAvailable || result.ToAvailable || result.Items == nil || len(result.Items) != 0 {
		t.Fatalf("缺少任一期清单时不得推断问题变化：%+v", result)
	}
	readHistoryTest[map[string]any](t, mux, base+"organization="+unit+"&from="+newer+"&to="+old, 400)
	if _, err := pool.Exec(t.Context(), "UPDATE business_cases SET organization_name='问题核对单位' WHERE filing_id=$1", filing); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE business_cases SET organization_name='已归属单位' WHERE id=$1", filing); err != nil {
		t.Fatal(err)
	}
	readHistoryTest[map[string]any](t, mux, base+"organization="+url.QueryEscape("已归属单位")+"&from="+old+"&to="+newer, 404)
}

// seedAnalyticsPlan 带入由同一受控报告关联的已批或草稿测试清单。
// 参数 t、pool 为测试上下文，report 为报告标识，status 和 risks 为待登记的样例。
// 返回值 无。
func seedAnalyticsPlan(t *testing.T, pool *pgxpool.Pool, report, status, risks string) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `INSERT INTO remediation_plans
		(report_id,deadline,risks,status,submission,submitted_at)
		VALUES ($1,'2027-01-01',$3::jsonb,$2,CASE WHEN $2='draft' THEN 0 ELSE 1 END,
		CASE WHEN $2='draft' THEN NULL ELSE clock_timestamp() END)`, report, status, risks)
	if err != nil {
		t.Fatal(err)
	}
}

// TestComplexMergeKeepsOperatorReportArchiveInOriginalScope 检验已冻结报告的列表、原件和派生分析范围。
// 参数 t 为隔离 PostgreSQL 与实际历史接口的测试上下文。
// 返回值 无。
func TestComplexMergeKeepsOperatorReportArchiveInOriginalScope(t *testing.T) {
	pool, mux := historyFixture(t)
	filing := assessmentTestFiling(t, pool, "归并前编号", "主单位")
	old := insertHistoryReport(t, pool, filing, "归并前报告", "approved", "medium", "2024-11-02T08:00:00Z")
	newer := insertHistoryReport(t, pool, filing, "归并后报告", "approved", "low", "2026-11-02T08:00:00Z")
	insertHistoryFile(t, pool, old, "原单位报告正文.pdf", "report_file", "2024-11-02T08:00:00Z")
	if _, err := pool.Exec(t.Context(), `UPDATE business_cases SET merge_history_scope='原单位',
		merge_history_number='归并前编号',merge_history_system_name='归并前系统' WHERE id=$1`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE business_cases SET number='归并后编号',system_name='归并后系统' WHERE id=$1`,
		filing); err != nil {
		t.Fatal(err)
	}
	archive := operatorHistoryTestPath + "/" + filing
	orig := "?organization=" + url.QueryEscape("原单位")
	main := "?organization=" + url.QueryEscape("主单位")
	expected := []struct {
		query, report, name, number string
	}{
		{orig, old, "归并前系统", "归并前编号"},
		{main, newer, "归并后系统", "归并后编号"},
	}
	for _, subject := range expected {
		view := readHistoryTest[operatorHistoryPage](t, mux, archive+subject.query, 200)
		if len(view.Items) != 1 || view.Items[0].ID != subject.report ||
			view.System.SystemName != subject.name || view.System.Number != subject.number {
			t.Fatalf("归并前后报告或母备案信息混入另一范围：%+v", view)
		}
		systems := readHistoryTest[operatorSystemsPage](t, mux, operatorHistoryTestPath+"/systems"+subject.query, 200)
		if len(systems.Items) != 1 || systems.Items[0] != view.System {
			t.Fatalf("归并前后各自的原归属系统入口不可见：%+v", systems)
		}
	}
	seedAnalyticsPlan(t, pool, old, "approved", `[{"description":"主单位交接后方案","risk":"low"}]`)
	sourcePath := operatorMaterialsTestPath(filing, old) + orig
	view := readHistoryTest[operatorReportMaterials](t, mux, sourcePath, 200)
	if len(view.Items) != 1 || view.Requirements != nil {
		t.Fatalf("旧报告原件可查，主单位新规划不得混入：%+v", view)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE remediation_plans SET frozen_organization_name='原单位' WHERE report_id=$1", old); err != nil {
		t.Fatal(err)
	}
	view = readHistoryTest[operatorReportMaterials](t, mux, sourcePath, 200)
	if view.Requirements == nil || len(view.Requirements.Items) != 1 {
		t.Fatalf("原范围冻结的已批整改清单应保留：%+v", view)
	}
	readHistoryTest[map[string]any](t, mux, operatorMaterialsTestPath(filing, old)+main, 404)
	readHistoryTest[map[string]any](t, mux, operatorMaterialsTestPath(filing, newer)+orig, 404)
	timeline := readHistoryTest[assessmentTrend](t, mux, archive+"/trend"+orig, 200)
	if len(timeline.Items) != 1 || timeline.Items[0].Year != 2024 {
		t.Fatalf("原单位只应查询自己的年度指标：%+v", timeline)
	}
	readHistoryTest[map[string]any](t, mux, archive+"/issue-comparison"+main+"&from="+old+"&to="+newer, 404)
}

// TestComplexMergeDoesNotInheritUnreleasedReportSummaries 验证未获释放的旧报告不会因归并出现在主单位摘要中。
// 参数 t 为隔离 PostgreSQL 及 HTTP 测试上下文。
// 返回值 无。
func TestComplexMergeDoesNotInheritUnreleasedReportSummaries(t *testing.T) {
	pool, mux := historyFixture(t)
	filing := assessmentTestFiling(t, pool, "不继承历史备案", "主承办单位")
	inaccessible := insertHistoryReport(t, pool, filing, "旧机构内部报告", "approved", "high", "2024-01-01T08:00:00Z")
	current := insertHistoryReport(t, pool, filing, "新归属终审报告", "approved", "low", "2026-01-01T08:00:00Z")
	if _, err := pool.Exec(t.Context(), `WITH target AS (
		INSERT INTO directory_parties(kind,scope_name,name,code)
		VALUES('unit','主承办单位','主承办单位','913300001000001081') RETURNING id
	)
	INSERT INTO directory_unit_merges(target_id,preview_token,plan,actor,merged_at)
	SELECT id,repeat('c',64),jsonb_build_object('mode','complex','moves',
		jsonb_build_array(jsonb_build_object('id',$1::text))),'监管','2025-01-01' FROM target`, filing); err != nil {
		t.Fatal(err)
	}
	query := "?organization=" + url.QueryEscape("主承办单位")
	path := operatorHistoryTestPath + "/" + filing
	result := readHistoryTest[operatorHistoryPage](t, mux, path+query, 200)
	if len(result.Items) != 1 || result.Items[0].ID != current {
		t.Fatalf("当前列表不得继承从未开放的旧资料：%+v", result)
	}
	readHistoryTest[map[string]any](t, mux, path+"/comparison"+query+"&from="+inaccessible+"&to="+current, 404)
}
