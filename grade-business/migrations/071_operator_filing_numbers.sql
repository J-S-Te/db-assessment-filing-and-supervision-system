-- 待分配编号的运营者首报可以并存；已分配编号继续保持大小写不敏感的唯一性。
DROP INDEX business_cases_kind_number;
CREATE UNIQUE INDEX business_cases_kind_number ON business_cases(kind,lower(number))
    WHERE discarded_at IS NULL AND status<>'withdrawn' AND number<>'';
