SELECT * FROM jobs WHERE stage=$1 AND status='pending' ORDER BY created_at ASC;
