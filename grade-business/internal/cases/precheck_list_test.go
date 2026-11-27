package cases

import "testing"

// TestFilingListPrecheckStatus 核对备案列表只显示当前版本的预审任务状态。
// 参数 t 为隔离数据库和真实 HTTP 路由的测试上下文。
// 返回值 无。
func TestFilingListPrecheckStatus(t *testing.T) {
	service, handler := fixtureService(t)
	item := createFiling(t, handler, "QA-PRECHECK-LIST")
	path := filingsURL + "?q=QA-PRECHECK-LIST"
	postMaterial(t, handler, filingsURL+"/"+item.ID, "filing_form")
	_, err := service.db.Exec(t.Context(), `INSERT INTO filing_prechecks
		(filing_id, revision, materials, status) VALUES ($1, 0, '[]', 'succeeded')`, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertListPrecheck := func(want string) {
		t.Helper()
		type listItem struct {
			PrecheckStatus string `json:"precheckStatus"`
		}
		items := readResponse[itemResponse[listItem]](t, requestJSON(handler, "GET", path, nil), 200).Items
		if len(items) != 1 || items[0].PrecheckStatus != want {
			t.Fatalf("预审状态应为 %q，实际列表为 %+v", want, items)
		}
	}
	assertListPrecheck("")
	_, err = service.db.Exec(t.Context(), `INSERT INTO filing_prechecks
		(filing_id, revision, materials) VALUES ($1, 1, '[]')`, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertListPrecheck("queued")
	for _, status := range []string{"running", "succeeded", "failed"} {
		_, err := service.db.Exec(t.Context(), `UPDATE filing_prechecks
			SET status=$2 WHERE filing_id=$1 AND revision=1`, item.ID, status)
		if err != nil {
			t.Fatal(err)
		}
		assertListPrecheck(status)
	}
	if _, err := service.db.Exec(t.Context(), `UPDATE filing_material_policy SET version=version+1`); err != nil {
		t.Fatal(err)
	}
	assertListPrecheck("")
}
