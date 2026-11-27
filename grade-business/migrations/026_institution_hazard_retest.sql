ALTER TABLE hazard_items DROP CONSTRAINT hazard_items_status_check,
    ADD CONSTRAINT hazard_items_status_check
        CHECK (status IN ('open','pending_retest','pending_review','returned','closed'));

ALTER TABLE hazard_attachments DROP CONSTRAINT hazard_attachments_kind_check,
    ADD CONSTRAINT hazard_attachments_kind_check CHECK (kind IN ('evidence','retest_report'));

-- 保留上线前直接进入监管复核的提交事件；新提交由业务服务进入机构复测。
ALTER TABLE hazard_events DROP CONSTRAINT hazard_events_transition_check,
    DROP CONSTRAINT hazard_events_comment_check,
    ADD CONSTRAINT hazard_events_transition_check CHECK (
        (action = 'submit' AND from_status IN ('open','returned') AND to_status IN ('pending_retest','pending_review'))
        OR (action = 'retest_pass' AND from_status = 'pending_retest' AND to_status = 'pending_review')
        OR (action = 'retest_return' AND from_status = 'pending_retest' AND to_status = 'returned')
        OR (action = 'return_for_correction' AND from_status = 'pending_review' AND to_status = 'returned')
        OR (action = 'close' AND from_status = 'pending_review' AND to_status = 'closed')
        OR (action = 'extend_deadline' AND from_status IN ('open','returned','pending_retest','pending_review')
            AND from_status = to_status)
    ),
    ADD CONSTRAINT hazard_events_comment_check CHECK (
        (action = 'extend_deadline' AND length(comment) BETWEEN 1 AND 1040)
        OR (action <> 'extend_deadline' AND length(comment) <= 1000
            AND (action IN ('close','retest_pass') OR length(comment) > 0))
    );

CREATE INDEX hazard_events_retest_round ON hazard_events(hazard_id,version DESC) WHERE action='submit';
