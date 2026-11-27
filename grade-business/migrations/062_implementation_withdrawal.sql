-- 已受理但仍在办的报备可能在测评日后撤销，保留原受理时间用于审计。
ALTER TABLE implementation_reports DROP CONSTRAINT implementation_reports_check4;
