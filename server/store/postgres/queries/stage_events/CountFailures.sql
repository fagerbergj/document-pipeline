SELECT COUNT(*) FROM stage_events WHERE document_id=$1 AND stage=$2 AND event_type='failed';
