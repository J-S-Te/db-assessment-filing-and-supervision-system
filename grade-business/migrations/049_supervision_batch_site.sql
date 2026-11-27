-- 批次锁定核验过的模板版本与收件快照，子任务仅引用原材料。
CREATE TABLE supervision_dispatch_batches (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    preview_token text NOT NULL UNIQUE CHECK (preview_token ~ '^[a-f0-9]{64}$'),
    input_hash text NOT NULL CHECK (input_hash ~ '^[a-f0-9]{64}$'),
    template_task_id uuid NOT NULL REFERENCES supervision_tasks(id),
    template_version bigint NOT NULL CHECK(template_version > 0),
    district text NOT NULL,
    levels text[] NOT NULL,
    title text NOT NULL,
    due_on date NOT NULL,
    actor text NOT NULL,
    result jsonb CHECK (jsonb_typeof(result)='object'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

ALTER TABLE supervision_tasks ADD COLUMN dispatch_batch_id uuid
    REFERENCES supervision_dispatch_batches(id);
ALTER TABLE supervision_attachments ADD CONSTRAINT supervision_attachment_id_kind UNIQUE(id,kind);

CREATE TABLE supervision_dispatched_templates (
    task_id uuid NOT NULL REFERENCES supervision_tasks(id),
    kind text NOT NULL CHECK (kind IN ('inspection_template','summary_template')),
    source_attachment_id uuid NOT NULL,
    PRIMARY KEY(task_id,kind),
    FOREIGN KEY(source_attachment_id,kind) REFERENCES supervision_attachments(id,kind)
);

-- 现场检查保留自己版本，与上报自查材料的状态机、版本互不消耗。
CREATE TABLE supervision_site_inspections (
    task_id uuid PRIMARY KEY REFERENCES supervision_tasks(id),
    status text NOT NULL CHECK(status IN (
        'scheduled','recorded','following_up','follow_up_submitted','closed','cancelled'
    )),
    version bigint NOT NULL CHECK(version > 0),
    visit_at timestamptz NOT NULL,
    location text NOT NULL CHECK(length(location) BETWEEN 1 AND 200),
    inspectors text NOT NULL CHECK(length(inspectors) BETWEEN 1 AND 200),
    outcome text CHECK(outcome IN ('pass','follow_up_required')),
    follow_up_due_on date
);

CREATE TABLE supervision_site_attachments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id uuid NOT NULL REFERENCES supervision_site_inspections(task_id),
    kind text NOT NULL CHECK(kind IN ('inspection_evidence','follow_up_evidence')),
    file_name text NOT NULL CHECK(length(file_name) BETWEEN 1 AND 150),
    file_key text NOT NULL UNIQUE CHECK(file_key ~ '^[0-9a-f]{48}$'),
    digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
    size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 1 AND 20971520),
    version bigint NOT NULL CHECK(version > 1),
    uploaded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(task_id,id),
    UNIQUE(task_id,version)
);

CREATE INDEX supervision_site_materials_latest
    ON supervision_site_attachments(task_id,kind,version DESC);

CREATE TABLE supervision_site_events (
    task_id uuid NOT NULL REFERENCES supervision_site_inspections(task_id),
    version bigint NOT NULL CHECK(version > 0),
    action text NOT NULL CHECK(action IN (
        'schedule','record','follow_up','submit_follow_up','return_follow_up','close','cancel'
    )),
    status text NOT NULL,
    actor text NOT NULL,
    comment text NOT NULL DEFAULT '',
    input_hash text NOT NULL CHECK (input_hash ~ '^[a-f0-9]{64}$'),
    evidence_id uuid,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(task_id,version),
    FOREIGN KEY(task_id,evidence_id) REFERENCES supervision_site_attachments(task_id,id),
    CHECK((evidence_id IS NOT NULL) = (action IN ('record','submit_follow_up')))
);
