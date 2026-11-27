package archives

import (
	"fmt"
	"net/http"
	"testing"
)

// TestSupplierMasterVersionedRegistry 验证供应商名称查重、来源、单位归属、查询和版本化状态。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestSupplierMasterVersionedRegistry(t *testing.T) {
	db := archiveTestPool(t)
	unit := archiveFixtureID(t, db, `INSERT INTO directory_parties(kind,scope_name,name,code)
	 VALUES('unit','用服务单位','用服务单位','913501001234567891') RETURNING id::text`)
	mux := http.NewServeMux()
	Register(mux, db)
	path := "/api/v1/archives/suppliers"
	body := fmt.Sprintf(`{"name":"Acme 云服","serviceType":"云平台","unitId":%q,
	 "startedOn":"2026-01-08","contactName":"张三","contactPhone":"123456789","source":"服务合同 001"}`, unit)
	first := inventoryRequest(t, mux, http.MethodPost, path, body, "", 201)
	id, _ := first["id"].(string)
	if id == "" || first["active"] != true || first["unitName"] != "用服务单位" || first["source"] != "服务合同 001" {
		t.Fatalf("供应商登记 = %+v", first)
	}
	retry := inventoryRequest(t, mux, http.MethodPost, path, body, "", 200)
	if retry["id"] != id || retry["version"] != float64(1) {
		t.Fatalf("相同登记重试 = %+v", retry)
	}
	inventoryRequest(t, mux, http.MethodPost, path,
		`{"name":"ACME 云服","serviceType":"技术运维","source":"运维合同"}`, "", 409)
	inventoryRequest(t, mux, http.MethodPost, path,
		`{"name":"无凭据企业","serviceType":"云平台"}`, "", 400)
	inventoryRequest(t, mux, http.MethodPost, path,
		`{"name":"坏日期企业","serviceType":"云平台","source":"证据","startedOn":"2026-02-29"}`, "", 400)

	updated := inventoryRequest(t, mux, http.MethodPatch, path+"/"+id,
		`{"name":"Acme 云服","serviceType":"云平台","source":"终止协议 008","active":false}`, `"1"`, 200)
	if updated["active"] != false || updated["source"] != "终止协议 008" || updated["version"] != float64(2) {
		t.Fatalf("终止后的主档 = %+v", updated)
	}
	inventoryRequest(t, mux, http.MethodPatch, path+"/"+id,
		`{"name":"Acme 云服","serviceType":"云平台","source":"服务合同 002","active":true}`, `"1"`, 409)
	matches := archiveGET[page[map[string]any]](t, mux, path+"?q=acme&active=false", 200)
	if len(matches.Items) != 1 || matches.Items[0]["id"] != id {
		t.Fatalf("停用供应商检索 = %+v", matches)
	}
	active := archiveGET[page[map[string]any]](t, mux, path+"?q=acme&active=true", 200)
	if len(active.Items) != 0 {
		t.Fatalf("停用供应商不应进入合作中候选：%+v", active)
	}
	var events int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM archive_registration_events
	 WHERE resource='suppliers' AND record_id=$1`, id).Scan(&events); err != nil || events != 2 {
		t.Fatalf("主档留痕数 = %d, %v", events, err)
	}
}

// TestSupplierMasterLinksRequireExplicitAssociation 验证旧文字登记由人工绑定，终止主档保留历史且禁止新增关联。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestSupplierMasterLinksRequireExplicitAssociation(t *testing.T) {
	db := archiveTestPool(t)
	first := insertArchiveFiling(t, db, "F-SUPPLIER-1", "供应商服务单位", "3", "approved", false)
	other := insertArchiveFiling(t, db, "F-SUPPLIER-2", "其他单位", "3", "approved", false)
	third := insertArchiveFiling(t, db, "F-SUPPLIER-3", "另一单位", "3", "approved", false)
	mux := http.NewServeMux()
	Register(mux, db)
	path := "/api/v1/archives/suppliers"
	assetPath := "/api/v1/archives/systems/" + first + "/assets"
	legacy := inventoryRequest(t, mux, http.MethodPost, assetPath,
		`{"kind":"supplier","identifier":"云服务厂商","source":"历史合同 A"}`, "", 201)
	legacyID, _ := legacy["id"].(string)

	master := inventoryRequest(t, mux, http.MethodPost, path,
		`{"name":"云服务厂商","serviceType":"云平台","source":"资质档案"}`, "", 201)
	id, _ := master["id"].(string)
	detail := archiveGET[struct{ Systems page[map[string]any] }](t, mux, path+"/"+id, 200)
	if len(detail.Systems.Items) != 0 {
		t.Fatalf("同名历史登记被隐式关联：%+v", detail)
	}

	body := fmt.Sprintf(`{"supplierId":%q,"kind":"supplier","source":"服务合同 B"}`, id)
	linked := inventoryRequest(t, mux, http.MethodPost, "/api/v1/archives/systems/"+other+"/assets", body, "", 201)
	if linked["supplierId"] != id || linked["supplierName"] != "云服务厂商" {
		t.Fatalf("供应商主档关联登记 = %+v", linked)
	}
	inventoryRequest(t, mux, http.MethodPost, assetPath,
		fmt.Sprintf(`{"supplierId":%q,"kind":"ip","identifier":"192.0.2.1","source":"清册"}`, id), "", 400)
	explicit := inventoryRequest(t, mux, http.MethodPatch, assetPath+"/"+legacyID,
		fmt.Sprintf(`{"supplierId":%q,"source":"历史合同 A","active":true}`, id), `"1"`, 200)
	if explicit["supplierId"] != id || explicit["identifier"] != "云服务厂商" || explicit["version"] != float64(2) {
		t.Fatalf("旧登记关联没有保留标识、证据或版本：%+v", explicit)
	}
	inventoryRequest(t, mux, http.MethodPost, assetPath, body, "", 409)

	inventoryRequest(t, mux, http.MethodPatch, assetPath+"/"+legacyID,
		`{"source":"历史合同 A","active":false}`, `"2"`, 200)
	inventoryRequest(t, mux, http.MethodPatch, path+"/"+id,
		`{"name":"云服务厂商","serviceType":"云平台","source":"终止合作协议","active":false}`, `"1"`, 200)
	inventoryRequest(t, mux, http.MethodPost, "/api/v1/archives/systems/"+third+"/assets", body, "", 409)
	inventoryRequest(t, mux, http.MethodPatch, assetPath+"/"+legacyID,
		`{"source":"历史合同 A","active":true}`, `"3"`, 409)

	detail = archiveGET[struct{ Systems page[map[string]any] }](t, mux, path+"/"+id+"?limit=1", 200)
	if len(detail.Systems.Items) != 1 || detail.Systems.Items[0]["filingId"] != other || detail.Systems.NextCursor == "" {
		t.Fatalf("历史关联首页 = %+v", detail)
	}
	detail = archiveGET[struct{ Systems page[map[string]any] }](t, mux,
		path+"/"+id+"?limit=1&cursor="+detail.Systems.NextCursor, 200)
	if len(detail.Systems.Items) != 1 || detail.Systems.Items[0]["filingId"] != first ||
		detail.Systems.Items[0]["active"] != false || detail.Systems.Items[0]["source"] != "历史合同 A" {
		t.Fatalf("停用的历史关联缺失：%+v", detail)
	}
}

// TestSystemAssetKindFilterScopesTopology 验证拓扑按登记系统、记录类型和分页位置读取。
// 参数 t 为隔离 PostgreSQL 测试上下文。
// 返回值 无。
func TestSystemAssetKindFilterScopesTopology(t *testing.T) {
	db := archiveTestPool(t)
	first := insertArchiveFiling(t, db, "F-TOPOLOGY-1", "拓扑单位甲", "3", "approved", false)
	other := insertArchiveFiling(t, db, "F-TOPOLOGY-2", "拓扑单位乙", "3", "approved", false)
	mux := http.NewServeMux()
	Register(mux, db)
	path := "/api/v1/archives/systems/" + first + "/assets"
	inventoryRequest(t, mux, http.MethodPost, path,
		`{"kind":"topology","identifier":"入口区","relatedEndpoint":"应用区","source":"拓扑扫描件 A"}`, "", 201)
	inventoryRequest(t, mux, http.MethodPost, path,
		`{"kind":"topology","identifier":"应用区","relatedEndpoint":"数据区","source":"拓扑扫描件 B"}`, "", 201)
	inventoryRequest(t, mux, http.MethodPost, path,
		`{"kind":"ip","identifier":"192.0.2.1","source":"资产清册"}`, "", 201)
	inventoryRequest(t, mux, http.MethodPost, "/api/v1/archives/systems/"+other+"/assets",
		`{"kind":"topology","identifier":"其他单位入口","relatedEndpoint":"其他单位应用","source":"拓扑扫描件 C"}`, "", 201)
	rows := archiveGET[page[systemAsset]](t, mux, path+"?kind=topology&limit=1", 200)
	if len(rows.Items) != 1 || rows.Items[0].Identifier != "应用区" || rows.Items[0].RelatedEndpoint != "数据区" || rows.NextCursor == "" {
		t.Fatalf("拓扑首页错误：%+v", rows)
	}
	more := archiveGET[page[systemAsset]](t, mux, path+"?kind=topology&limit=1&cursor="+rows.NextCursor, 200)
	if len(more.Items) != 1 || more.Items[0].Identifier != "入口区" || more.Items[0].Source != "拓扑扫描件 A" ||
		more.NextCursor != "" {
		t.Fatalf("拓扑尾页错误：%+v", more)
	}
	archiveGET[map[string]string](t, mux, path+"?kind=unknown", 400)
}
