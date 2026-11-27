ALTER TABLE access_accounts DROP CONSTRAINT access_accounts_check;

ALTER TABLE access_accounts ADD CONSTRAINT access_accounts_role_scope_check CHECK (
    (role_code='ROLE_SYS_ADMIN' AND person_key IS NOT NULL AND area_code IS NULL AND subject_id IS NULL)
    OR (role_code='ROLE_REGULATOR' AND person_key IS NOT NULL AND area_code IS NOT NULL
        AND area_code IN ('330800','330802','330803','330822','330824','330825','330881',
                          '331000','331002','331003','331004','331022','331023','331024',
                          '331081','331082','331083') AND subject_id IS NULL)
    OR (role_code IN ('ROLE_ORG_ADMIN','ROLE_OPERATOR') AND person_key IS NULL
        AND area_code IS NULL AND subject_id IS NOT NULL)
);
