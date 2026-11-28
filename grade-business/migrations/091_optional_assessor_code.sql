-- 新测评师不登记工号，历史非空工号继续按机构去重。
ALTER TABLE directory_people
    ALTER COLUMN code SET DEFAULT '',
    DROP CONSTRAINT directory_people_code_check,
    ADD CONSTRAINT directory_people_code_check CHECK (length(code) <= 80);

DROP INDEX directory_people_institution_code;
CREATE UNIQUE INDEX directory_people_institution_code
    ON directory_people (institution_id, lower(code)) WHERE code <> '';
