package access

// FilingDistrictScope 用备案的可信单位标识约束当前监管辖区。
// 参数 filingIDSQL 和 districtParameter 为固定的 SQL 标识表达式与辖区参数，lock 指示写事务需要稳定的备案和单位归属。
// 返回值 可直接加入 WHERE 的条件，NULL 范围表示非监管席位，空数组表示无授权辖区。
func FilingDistrictScope(filingIDSQL, districtParameter string, lock bool) string {
	lockingClause := ""
	if lock {
		lockingClause = " FOR SHARE OF scope_filing,scope_unit"
	}
	return `(` + districtParameter + `::text[] IS NULL OR EXISTS (
		SELECT 1 FROM business_cases scope_filing JOIN directory_parties scope_unit
		  ON scope_unit.id=scope_filing.directory_unit_id AND scope_unit.kind='unit'
		WHERE scope_filing.id=` + filingIDSQL + ` AND scope_filing.kind='filing'
		  AND btrim(scope_unit.district)=ANY(` + districtParameter + `::text[])` + lockingClause + `))`
}
