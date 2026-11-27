package access

import (
	"context"
	"maps"
	"slices"

	"grade-business/internal/geoarea"
)

const quzhouCityCode = geoarea.QuzhouCityCode

var quzhouNames = geoarea.QuzhouAreas()
var districtNames = regulatorDistrictNames()

// regulatorDistrictNames 构建可为监管员登记账号的区划名录。
// 参数 无。
// 返回值 区划代码和名称，包含仍有在用账号的台州区划。
func regulatorDistrictNames() map[string]string {
	names := maps.Clone(quzhouNames)
	maps.Copy(names, map[string]string{
		"331000": "台州市", "331002": "椒江区", "331003": "黄岩区", "331004": "路桥区",
		"331022": "三门县", "331023": "天台县", "331024": "仙居县",
		"331081": "温岭市", "331082": "临海市", "331083": "玉环市",
	})
	return names
}

var cityDistricts = map[string][]string{
	quzhouCityCode: slices.Sorted(maps.Keys(quzhouNames)),
	"331000":       {"331000", "331002", "331003", "331004", "331022", "331023", "331024", "331081", "331082", "331083"},
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
// 返回值 非监管员返回 nil，监管员返回可比对的编码和标准全称。
func Districts(ctx context.Context) []string {
	areas := RegulatorAreaNames(ctx)
	if areas == nil {
		return nil
	}
	result := make([]string, 0, 2*len(areas))
	for code, name := range areas {
		result = append(result, code, name)
	}
	return result
}
