-- 旧管理端备案缺少单位外键，按名录保留的稳定名称补绑可核对归属的记录。
UPDATE business_cases f
SET directory_unit_id=u.id
FROM directory_parties u
WHERE f.kind='filing'
  AND f.directory_unit_id IS NULL
  AND NOT f.operator_origin
  AND f.discarded_at IS NULL
  AND f.status IN ('draft','pending_initial','pending_final','returned','approved')
  AND (f.directory_snapshot IS NULL OR NOT (f.directory_snapshot ? 'unit'))
  AND u.kind='unit' AND u.status='active' AND u.merged_into_id IS NULL
  AND u.scope_name COLLATE "C"=f.organization_name COLLATE "C";
