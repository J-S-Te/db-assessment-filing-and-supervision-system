package dashboard

import (
	"context"

	"grade-business/internal/access"
)

type districtScope struct {
	aliases     map[string]string
	requireUnit bool
}

// resolveDistrictScope 从服务端会话收窄驾驶舱范围并标准化区划名称。
// 参数 ctx 为带认证身份的请求上下文，requested 为指定区县，nil 表示账号的完整范围。
// 返回值 显示区划、查询范围及是否允许读取。
func resolveDistrictScope(ctx context.Context, requested *string) (*string, districtScope, bool) {
	areas := access.RegulatorAreaNames(ctx)
	if areas == nil {
		var scope districtScope
		if requested != nil {
			scope.aliases = map[string]string{*requested: *requested}
		}
		return requested, scope, true
	}

	scope := districtScope{aliases: make(map[string]string), requireUnit: true}
	for code, name := range areas {
		if requested != nil && *requested != code && *requested != name {
			continue
		}
		scope.aliases[code], scope.aliases[name] = name, name
	}
	if len(scope.aliases) == 2 {
		for _, name := range scope.aliases {
			return &name, scope, true
		}
	}
	return nil, scope, requested == nil && len(scope.aliases) > 0
}
