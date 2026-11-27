-- 匿名受理不判断账号和已开户状态，开通仍按单位信用代码唯一。
CREATE UNIQUE INDEX operator_registrations_approved_code
    ON operator_registrations(credit_code) WHERE status='approved';
DROP INDEX operator_registrations_open_code;

-- 空辖区表示暂不能可信分派，只在全域管理员的审核清单展示。
ALTER TABLE operator_registrations DROP CONSTRAINT operator_registrations_area_code_check;
ALTER TABLE operator_registrations ADD CONSTRAINT operator_registrations_area_code_check
    CHECK (area_code IN ('','330800','330802','330803','330822','330824','330825','330881',
                         '331000','331002','331003','331004','331022','331023','331024',
                         '331081','331082','331083'));
