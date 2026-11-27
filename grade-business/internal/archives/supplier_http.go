package archives

import (
	"net/http"

	"grade-business/internal/httpx"
)

const supplierPath = "/api/v1/archives/suppliers"

// listSuppliers 返回按关键词和合作状态查询的供应商主档页。
// 参数 w 为响应，r 携带查询条件和分页位置。
// 返回值 无。
func (s archiveService) listSuppliers(w http.ResponseWriter, r *http.Request) {
	values, err := strictQuery(r.URL.RawQuery, "q", "active", "cursor", "limit")
	if err != nil {
		writeRegistration(w, nil, false, err)
		return
	}
	var active *bool
	if values.Has("active") {
		flag := values.Get("active")
		if flag != "true" && flag != "false" {
			writeRegistration(w, nil, false, errInvalid)
			return
		}
		selected := flag == "true"
		active = &selected
		values.Del("active")
	}
	options, err := listParameters(values.Encode(), true)
	if err != nil {
		writeRegistration(w, nil, false, err)
		return
	}
	result, err := s.readSuppliers(r.Context(), options, active)
	writeRegistration(w, result, false, err)
}

// getSupplier 返回主档详情和具备来源证据的备案关联。
// 参数 w 为响应，r 携带主档标识及关联分页位置。
// 返回值 无。
func (s archiveService) getSupplier(w http.ResponseWriter, r *http.Request) {
	id, err := archivePathID(r, "id")
	if err != nil {
		writeRegistration(w, nil, false, err)
		return
	}
	options, err := registrationPage(r.URL.RawQuery)
	if err != nil {
		writeRegistration(w, nil, false, err)
		return
	}
	result, err := s.readSupplierProfile(r.Context(), id, options)
	writeRegistration(w, result, false, err)
}

// createSupplier 接收供应商主档登记请求并校验凭据。
// 参数 w 为响应，r 携带主档资料。
// 返回值 无。
func (s archiveService) createSupplier(w http.ResponseWriter, r *http.Request) {
	var input supplierInput
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil || validateSupplierFields(&input.supplierFields) != nil {
		writeRegistration(w, nil, false, errInvalid)
		return
	}
	result, created, err := s.createSupplierMaster(r.Context(), input)
	writeRegistration(w, result, created, err)
}

// patchSupplier 更新供应商主档基本资料及合作状态。
// 参数 w 为响应，r 携带主档标识、目标资料和 If-Match 版本。
// 返回值 无。
func (s archiveService) patchSupplier(w http.ResponseWriter, r *http.Request) {
	id, err := archivePathID(r, "id")
	if err != nil {
		writeRegistration(w, nil, false, err)
		return
	}
	var input supplierInput
	version, err := registrationVersion(r.Header.Values("If-Match"))
	if r.URL.RawQuery != "" || err != nil || httpx.Decode(w, r, &input) != nil ||
		input.Active == nil || validateSupplierFields(&input.supplierFields) != nil {
		writeRegistration(w, nil, false, errInvalid)
		return
	}
	result, err := s.updateSupplierMaster(r.Context(), id, version, input)
	writeRegistration(w, result, false, err)
}
