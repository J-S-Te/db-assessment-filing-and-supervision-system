ALTER TABLE remediation_plans DROP CONSTRAINT remediation_plans_status_check,
    ADD CONSTRAINT remediation_plans_status_check
        CHECK (status IN ('draft','pending_review','approved','returned','withdrawn'));

ALTER TABLE remediation_review_events DROP CONSTRAINT remediation_review_events_action_check,
    DROP CONSTRAINT remediation_review_events_to_status_check,
    ADD CONSTRAINT remediation_review_events_action_check
        CHECK (action IN ('submit','approve','return_for_correction','withdraw')),
    ADD CONSTRAINT remediation_review_events_to_status_check
        CHECK (to_status IN ('pending_review','approved','returned','withdrawn'));

ALTER TABLE remediation_attachments DROP CONSTRAINT remediation_attachments_kind_check,
    ADD CONSTRAINT remediation_attachments_kind_check
        CHECK (kind IN ('plan','responsibility','schedule','high_risk_evidence'));

CREATE TABLE remediation_filing_review_slots (
    filing_id uuid PRIMARY KEY REFERENCES business_cases(id),
    pending_count bigint NOT NULL CHECK (pending_count > 0)
);

-- 历史重复待审保持原样。直到同一备案下最后一份历史规划退出待审，才允许新增。
INSERT INTO remediation_filing_review_slots(filing_id,pending_count)
SELECT r.filing_id,count(*) FROM remediation_plans p JOIN business_cases r ON r.id=p.report_id
WHERE p.status='pending_review' GROUP BY r.filing_id;

CREATE FUNCTION maintain_remediation_review_slot() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    old_filing uuid;
    new_filing uuid;
    pending bigint;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        IF OLD.status='pending_review' THEN
            SELECT filing_id INTO STRICT old_filing FROM business_cases WHERE id=OLD.report_id;
        END IF;
    END IF;
    IF TG_OP <> 'DELETE' THEN
        IF NEW.status='pending_review' THEN
            SELECT filing_id INTO STRICT new_filing FROM business_cases WHERE id=NEW.report_id FOR SHARE;
            IF new_filing IS NULL THEN
                RAISE EXCEPTION 'pending remediation requires a filing';
            END IF;
        END IF;
    END IF;

    IF old_filing IS NOT DISTINCT FROM new_filing THEN
        RETURN NULL;
    END IF;
    IF new_filing IS NOT NULL THEN
        INSERT INTO remediation_filing_review_slots(filing_id,pending_count) VALUES(new_filing,1)
            ON CONFLICT (filing_id) DO NOTHING;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'filing already has a remediation pending review'
                USING ERRCODE='unique_violation', CONSTRAINT='remediation_filing_in_review';
        END IF;
    END IF;
    IF old_filing IS NOT NULL THEN
        SELECT pending_count INTO STRICT pending FROM remediation_filing_review_slots
            WHERE filing_id=old_filing FOR UPDATE;
        IF pending = 1 THEN
            DELETE FROM remediation_filing_review_slots WHERE filing_id=old_filing;
        ELSE
            UPDATE remediation_filing_review_slots SET pending_count=pending-1 WHERE filing_id=old_filing;
        END IF;
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER remediation_review_slot
AFTER INSERT OR UPDATE OF status,report_id OR DELETE ON remediation_plans
FOR EACH ROW EXECUTE FUNCTION maintain_remediation_review_slot();
