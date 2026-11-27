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
