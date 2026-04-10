-- name: Append
INSERT INTO stage_events (document_id, timestamp, stage, event_type) VALUES (?, ?, ?, ?);

-- name: CountFailures
SELECT COUNT(*) FROM stage_events WHERE document_id=? AND stage=? AND event_type='failed';
