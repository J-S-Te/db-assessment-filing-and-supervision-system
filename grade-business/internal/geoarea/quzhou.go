package geoarea

import (
	"maps"
	"strings"
)

const QuzhouCityCode = "330800"

var quzhou = map[string]string{
	QuzhouCityCode: "衢州市",
	"330802":       "柯城区",
	"330803":       "衢江区",
	"330822":       "常山县",
	"330824":       "开化县",
	"330825":       "龙游县",
	"330881":       "江山市",
}

var taizhou = map[string]string{
	"331000": "台州市",
	"331002": "椒江区",
	"331003": "黄岩区",
	"331004": "路桥区",
	"331022": "三门县",
	"331023": "天台县",
	"331024": "仙居县",
	"331081": "温岭市",
	"331082": "临海市",
	"331083": "玉环市",
}

// RegulatorAreas 返回在用监管区划的标准代码和名称。
// 参数 无。
// 返回值 含衢州和台州区划的独立映射。
func RegulatorAreas() map[string]string {
	areas := maps.Clone(quzhou)
	maps.Copy(areas, taizhou)
	return areas
}

// RegulatorAreaCode 将在用监管区划的标准值和历史简称归一为区划代码。
// 参数 value 为名录中已登记或候选的区划。
// 返回值 已支持的监管区划代码，无法识别时为空。
func RegulatorAreaCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "台州" {
		return "331000"
	}
	for code, name := range taizhou {
		if value == code || value == name {
			return code
		}
	}
	return QuzhouAreaCode(value)
}

// QuzhouAreas 返回衢州市及六个区县的标准代码和名称。
// 参数 无。
// 返回值 可安全传给辖区查询使用的独立映射。
func QuzhouAreas() map[string]string {
	return maps.Clone(quzhou)
}

// QuzhouAreaCode 将衢州市或区县的标准名称和代码归一为区划代码。
// 参数 value 为已登记或拟提交的区划值。
// 返回值 衢州区划代码，其他区划或无法识别时为空。
func QuzhouAreaCode(value string) string {
	value = strings.TrimSpace(value)
	for code, name := range quzhou {
		if value == code || value == name {
			return code
		}
	}
	return ""
}
