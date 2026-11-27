package archives

import (
	"net/http"

	"grade-business/internal/httpx"
)

const coveragePath = "/api/v1/archives/coverage-targets"

// listCoverage 返回明确登记的覆盖对象与当前备案关联状态。
// 参数 w 为响应，r 携带分页查询。
// 返回值 无。
func (s archiveService) listCoverage(w http.ResponseWriter, r *http.Request) {
	input, err := registrationPage(r.URL.RawQuery)
	if err != nil {
		writeRegistration(w, nil, false, err)
		return
	}
	result, err := s.readCoverageTargets(r.Context(), input)
	writeRegistration(w, result, false, err)
}

// createCoverage 按来源凭据登记一个可单独核验的覆盖对象。
// 参数 w 为响应，r 携带单位、系统、来源和可选备案标识。
// 返回值 无。
func (s archiveService) createCoverage(w http.ResponseWriter, r *http.Request) {
	var input coverageCreate
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil || validateCoverageCreate(&input) != nil {
		writeRegistration(w, nil, false, errInvalid)
		return
	}
	unit, unitErr := parseArchiveID(input.UnitID)
	filing, filingErr := optionalArchiveID(input.FilingID)
	if unitErr != nil || filingErr != nil {
		writeRegistration(w, nil, false, errInvalid)
		return
	}
	result, created, err := s.createCoverageTarget(r.Context(), unit, filing, input)
	writeRegistration(w, result, created, err)
}

// patchCoverage 按预期版本调整对象在册状态或匹配备案。
// 参数 w 为响应，r 携带目标标识、版本与业务变更。
// 返回值 无。
func (s archiveService) patchCoverage(w http.ResponseWriter, r *http.Request) {
	id, err := archivePathID(r, "id")
	if err != nil {
		writeRegistration(w, nil, false, err)
		return
	}
	var input coverageUpdate
	version, versionErr := registrationVersion(r.Header.Values("If-Match"))
	if r.URL.RawQuery != "" || versionErr != nil || httpx.Decode(w, r, &input) != nil || input.Active == nil {
		writeRegistration(w, nil, false, errInvalid)
		return
	}
	filing, err := optionalArchiveID(input.FilingID)
	if err != nil {
		writeRegistration(w, nil, false, err)
		return
	}
	result, err := s.updateCoverageTarget(r.Context(), id, filing, version, *input.Active)
	writeRegistration(w, result, false, err)
}
