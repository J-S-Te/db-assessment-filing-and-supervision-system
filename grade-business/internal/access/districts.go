package access

import (
	"context"
	"maps"
	"net/http"
	"slices"

	"grade-business/internal/geoarea"
	"grade-business/internal/httpx"
)

const quzhouCityCode = geoarea.QuzhouCityCode

var quzhouNames = geoarea.QuzhouAreas()
var districtNames = geoarea.RegulatorAreas()

var cityDistricts = map[string][]string{
	quzhouCityCode: slices.Sorted(maps.Keys(quzhouNames)),
	"331000":       {"331000", "331002", "331003", "331004", "331022", "331023", "331024", "331081", "331082", "331083"},
}

// loginAreas 按驾驶舱地图的市域范围提供市本级和区县选项。
// 参数 w 为匿名查询响应，忽略请求对象。
// 返回值 无。
func loginAreas(w http.ResponseWriter, _ *http.Request) {
	type option struct {
		Code  string `json:"code"`
		Label string `json:"label"`
	}
	codes := districtCodes(quzhouCityCode)
	items := make([]option, 0, len(codes))
	for _, code := range codes {
		label := districtNames[code]
		if code == quzhouCityCode {
			label += "本级"
		}
		items = append(items, option{Code: code, Label: label})
	}
	httpx.JSON(w, 200, struct {
		Items []option `json:"items"`
	}{Items: items})
}

type dashboardArea struct {
	Name      string   `json:"name"`
	Districts []string `json:"districts"`
}

// quzhouDashboard 返回衢州监管驾驶舱可展示的区划。
// 参数 areaCode 为账号绑定的行政区代码。
// 返回值 衢州市或区县的地图范围，其他账号返回 nil。
func quzhouDashboard(areaCode string) *dashboardArea {
	if !slices.Contains(cityDistricts[quzhouCityCode], areaCode) {
		return nil
	}
	area := &dashboardArea{Name: districtNames[areaCode]}
	for _, district := range districtCodes(areaCode) {
		area.Districts = append(area.Districts, districtNames[district])
	}
	return area
}

// districtCodes 返回市级账号所辖区划或区县账号的单一区划。
// 参数 areaCode 为账号绑定的行政区代码。
// 返回值 可见的区划代码，无法识别时返回空范围。
func districtCodes(areaCode string) []string {
	if codes := cityDistricts[areaCode]; len(codes) != 0 {
		return codes
	}
	if districtNames[areaCode] != "" {
		return []string{areaCode}
	}
	return nil
}

// RegulatorAreaNames 返回监管员可见区划的编码及标准名称。
// 参数 ctx 为带已验证会话的查询上下文。
// 返回值 非监管员返回 nil，监管员只返回授权区划，未知区划返回空映射。
func RegulatorAreaNames(ctx context.Context) map[string]string {
	actor, ok := Current(ctx)
	if !ok || actor.Role != Regulator {
		return nil
	}
	areas := make(map[string]string)
	for _, code := range districtCodes(actor.AreaCode) {
		areas[code] = districtNames[code]
	}
	return areas
}

// Districts 为辖区查询提供当前账号可访问的标准区县名称与编码。
// 参数 ctx 为带监管身份的请求上下文。
// 返回值 非监管员返回 nil，监管员返回编码、标准全称和受支持的历史简写。
func Districts(ctx context.Context) []string {
	areas := RegulatorAreaNames(ctx)
	if areas == nil {
		return nil
	}
	result := make([]string, 0, 2*len(areas))
	for code, name := range areas {
		result = append(result, code, name)
	}
	if areas["331000"] != "" {
		result = append(result, "台州")
	}
	return result
}
