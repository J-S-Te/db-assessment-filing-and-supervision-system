ALTER TABLE local_alert_events
    DROP CONSTRAINT local_alert_events_action_check,
    DROP CONSTRAINT local_alert_events_transition_check,
    ADD CONSTRAINT local_alert_events_action_check CHECK (action IN (
        'create','process','close','convert','request_extension','report_false_alarm',
        'submit_feedback','approve','reject'
    )),
    ADD CONSTRAINT local_alert_events_transition_check CHECK (
        (action='create' AND to_status='pending' AND version=1 AND comment='')
        OR (action='convert' AND from_status IN ('pending','processing') AND to_status='processing'
            AND version>1 AND length(comment)<=1000)
        OR (version>1 AND length(comment) BETWEEN 1 AND 1000 AND (
            (action='process' AND from_status='pending' AND to_status='processing')
            OR (action IN ('close','approve') AND to_status='closed')
            OR (action='submit_feedback' AND from_status='processing' AND to_status='processing')
            OR (action IN ('request_extension','report_false_alarm','approve','reject') AND from_status=to_status)
        ))
    );

ALTER TABLE local_alert_dispositions
    ADD COLUMN requester_audience text NOT NULL DEFAULT 'operator',
    DROP CONSTRAINT local_alert_dispositions_action_check,
    ADD CONSTRAINT local_alert_dispositions_requester_action CHECK (
        (requester_audience='operator' AND action IN ('convert','request_extension','report_false_alarm'))
        OR (requester_audience='institution' AND action='submit_feedback')
    );

ALTER TABLE review_notices
    DROP CONSTRAINT review_notices_audience_resource,
    ADD CONSTRAINT review_notices_audience_resource CHECK (
        (audience='institution' AND resource IN ('filings','filing-applications','implementations','hazards','alerts'))
        OR (audience='institution' AND resource='reports' AND workflow_day IS NOT NULL)
        OR (audience='operator' AND resource IN ('filings','filing-applications','implementations',
            'remediations','hazards','alerts'))
    );

-- 存量未关闭预警补送首条提示，仍按原报告或派发时的机构全称归属。
INSERT INTO review_notices(audience,resource,resource_id,event_version,state,recipient,reference,subject,comment)
SELECT 'institution','alerts',a.id,1,'pending',hc.assessment_org,hc.filing_number,hc.system_name,''
FROM local_alerts a
JOIN hazard_items h ON h.id=a.hazard_id
JOIN hazard_context hc ON hc.id=h.id
WHERE a.status<>'closed' AND h.status<>'pending_dispatch' AND hc.can_retest
  AND length(hc.assessment_org) BETWEEN 1 AND 200
ON CONFLICT DO NOTHING;
