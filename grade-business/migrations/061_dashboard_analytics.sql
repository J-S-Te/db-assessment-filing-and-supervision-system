CREATE TABLE dashboard_classification_options (
    kind text NOT NULL CHECK (kind IN ('industry', 'issue')),
    code text NOT NULL CHECK (length(code) BETWEEN 1 AND 48),
    label text NOT NULL CHECK (length(label) BETWEEN 1 AND 80),
    PRIMARY KEY (kind, code),
    UNIQUE (kind, label)
);

INSERT INTO dashboard_classification_options(kind,code,label) VALUES
    ('industry','11','电信'),
    ('industry','21','铁路'),
    ('industry','22','银行'),
    ('industry','23','海关'),
    ('industry','27','证券'),
    ('industry','28','保险'),
    ('industry','73','电子政务'),
    ('industry','education','教育'),
    ('industry','energy','能源'),
    ('industry','healthcare','卫生健康'),
    ('industry','manufacturing','制造'),
    ('industry','transport','交通运输（铁路除外）'),
    ('industry','99','其他行业'),
    ('issue','access_control','身份鉴别与访问控制'),
    ('issue','audit_logging','日志与安全审计'),
    ('issue','backup_recovery','备份与恢复'),
    ('issue','data_protection','数据安全保护'),
    ('issue','malware_protection','恶意代码防范'),
    ('issue','network_protection','网络与边界防护'),
    ('issue','other','其他问题'),
    ('issue','physical_security','物理与环境安全'),
    ('issue','secure_configuration','安全配置'),
    ('issue','security_management','安全管理'),
    ('issue','vulnerability_patch','漏洞与补丁管理');

CREATE TABLE dashboard_filing_industries (
    filing_id uuid PRIMARY KEY REFERENCES business_cases(id) ON DELETE CASCADE,
    kind text GENERATED ALWAYS AS ('industry') STORED,
    code text,
    version bigint NOT NULL CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (kind, code) REFERENCES dashboard_classification_options(kind, code)
);

CREATE TABLE dashboard_hazard_issues (
    hazard_id uuid PRIMARY KEY REFERENCES hazard_items(id) ON DELETE CASCADE,
    kind text GENERATED ALWAYS AS ('issue') STORED,
    code text,
    version bigint NOT NULL CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (kind, code) REFERENCES dashboard_classification_options(kind, code)
);
