CREATE TABLE IF NOT EXISTS submission_handoffs (
 id TEXT PRIMARY KEY,
 submission_id TEXT NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
 owner_id TEXT NOT NULL,
 idempotency_key TEXT NOT NULL,
 payload_digest TEXT NOT NULL,
 status TEXT NOT NULL,
 created_at BIGINT NOT NULL,
 expires_at BIGINT NOT NULL,
 version INTEGER NOT NULL,
 document TEXT NOT NULL,
 UNIQUE(submission_id, owner_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_handoff_submission ON submission_handoffs(submission_id,created_at);
CREATE TABLE IF NOT EXISTS submission_handoff_control (id INTEGER PRIMARY KEY);
INSERT INTO submission_handoff_control(id) VALUES(1) ON CONFLICT DO NOTHING;
