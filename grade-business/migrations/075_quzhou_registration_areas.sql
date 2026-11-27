ALTER TABLE operator_registrations DROP CONSTRAINT operator_registrations_area_code_check;

ALTER TABLE operator_registrations ADD CONSTRAINT operator_registrations_area_code_check
    CHECK (area_code IN ('330800','330802','330803','330822','330824','330825','330881',
                         '331000','331002','331003','331004','331022','331023','331024',
                         '331081','331082','331083'));
