package main

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// TestOperatorRegistrationSelectedAreaOnIsolatedDB 验证区划选项和辖区审批。
// 参数 t 为隔离数据库和真实注册路由测试。
// 返回值 无。
func TestOperatorRegistrationSelectedAreaOnIsolatedDB(t *testing.T) {
	db, handler, admin, kecheng, org := registrationFixture(t, "330802")
	hit := accessRequest(t, handler)
	var areas struct {
		Items []struct{ Code, Label string }
	}
	response := hit(nil, "GET", publicRegistrationPath+"/areas", nil, 200)
	if err := json.Unmarshal(response.Body.Bytes(), &areas); err != nil {
		t.Fatal(err)
	}
	want := []string{"330800 市本级", "330802 柯城区", "330803 衢江区", "330822 常山县",
		"330824 开化县", "330825 龙游县", "330881 江山市"}
	var got []string
	for _, area := range areas.Items {
		got = append(got, area.Code+" "+area.Label)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("区划选项与驾驶舱口径不一致：%v", got)
	}

	input := registrationInput("选择柯城区单位", "91330102MA1000001J", "chosen-kecheng")
	for _, invalid := range []string{"", "331002", "柯城区"} {
		input["areaCode"] = invalid
		hit(nil, "POST", publicRegistrationPath, input, 400)
	}
	input["areaCode"] = "330802"
	own := submitRegistration(t, hit, input)
	input = registrationInput("选择衢江区单位", "91330802MA1000001N", "chosen-qujiang")
	input["areaCode"] = "330803"
	other := submitRegistration(t, hit, input)
	assertSelectedRegistrationReview(t, hit, admin, kecheng, org, own, other)
	var district string
	err := db.QueryRow(t.Context(), `SELECT p.district FROM operator_registrations r
		JOIN directory_parties p ON p.id=r.subject_id WHERE r.id=$1`, own.ID).Scan(&district)
	if err != nil || district != "柯城区" {
		t.Fatalf("新单位主档未使用审批辖区：%q %v", district, err)
	}
}

// assertSelectedRegistrationReview 验证监管员和平台管理员的通过及驳回权限。
// 参数 t、hit 为测试与真实路由，admin、district、org 为不同角色会话，own、other 为两个辖区的申请。
// 返回值 无。
func assertSelectedRegistrationReview(t *testing.T, hit requestAccess, admin, district, org *http.Cookie,
	own, other registrationTestResult,
) {
	t.Helper()
	list := hit(district, "GET", registrationPath, nil, 200).Body.String()
	if !strings.Contains(list, own.ID) || strings.Contains(list, other.ID) {
		t.Fatal("区县监管员只能看到选定本辖区的申请")
	}
	approve := map[string]any{"decision": "approve", "verified": true}
	reject := map[string]any{"decision": "reject", "reason": "核对单位资料"}
	ownPath := registrationPath + "/" + own.ID + "/review-actions"
	otherPath := registrationPath + "/" + other.ID + "/review-actions"
	hit(district, "POST", otherPath, approve, 404)
	hit(district, "POST", otherPath, reject, 404)
	hit(org, "POST", ownPath, approve, 403)
	readRegistration(t, hit(district, "POST", ownPath, approve, 200), "approved")
	readRegistration(t, hit(admin, "POST", otherPath, reject, 200), "rejected")

	input := registrationInput("选择衢江区单位", "91330802MA1000001N", "chosen-qujiang-again")
	input["areaCode"] = "330803"
	other = submitRegistration(t, hit, input)
	readRegistration(t, hit(admin, "POST", registrationPath+"/"+other.ID+"/review-actions", approve, 200),
		"approved")
	input = registrationInput("选择柯城区待核对单位", "91330803MA10000015", "chosen-kecheng-reject")
	input["areaCode"] = "330802"
	own = submitRegistration(t, hit, input)
	readRegistration(t, hit(district, "POST", registrationPath+"/"+own.ID+"/review-actions", reject, 200),
		"rejected")
}

// TestOperatorRegistrationExistingAreaOverridesSelectionOnIsolatedDB 验证在册单位以已核定辖区办理。
// 参数 t 为隔离数据库和真实注册路由测试。
// 返回值 无。
func TestOperatorRegistrationExistingAreaOverridesSelectionOnIsolatedDB(t *testing.T) {
	db, handler, admin, qujiang, _ := registrationFixture(t, "330803")
	hit := accessRequest(t, handler)
	const name = "辖区已核定单位"
	const code = "91330803MA10000015"
	_, err := db.Exec(t.Context(), `INSERT INTO directory_parties(kind,scope_name,name,code,district)
		VALUES('unit',$1,$1,$2,'柯城区')`, name, code)
	if err != nil {
		t.Fatal(err)
	}
	input := registrationInput(name, code, "selected-other-area")
	input["areaCode"] = "330803"
	submitted := submitRegistration(t, hit, input)
	list := hit(admin, "GET", registrationPath, nil, 200).Body.String()
	if !strings.Contains(list, `"areaCode":"330802"`) || !strings.Contains(list, `"requestedDistrict":"衢江区"`) {
		t.Fatal("审核端应区分已核定辖区及申请填写的辖区")
	}
	path := registrationPath + "/" + submitted.ID + "/review-actions"
	decision := map[string]any{"decision": "approve", "verified": true}
	hit(qujiang, "POST", path, decision, 404)
	approved := readRegistration(t, hit(admin, "POST", path, decision, 200), "approved")
	if approved.AreaCode != "330802" {
		t.Fatalf("在册单位应维持已核定的柯城区归属：%s", approved.AreaCode)
	}
}
