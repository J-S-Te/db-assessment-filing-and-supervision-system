-- 保留申请人选择的衢州辖区，旧申请继续使用原先的审核辖区。
ALTER TABLE operator_registrations
    ADD COLUMN requested_area_code text NOT NULL DEFAULT '' CHECK (requested_area_code IN
        ('','330800','330802','330803','330822','330824','330825','330881'));
