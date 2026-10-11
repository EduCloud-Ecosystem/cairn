CREATE TABLE IF NOT EXISTS reconciliation_audit (
 plan_id TEXT PRIMARY KEY,
 roster_id TEXT NOT NULL REFERENCES roster_entries(id) ON DELETE CASCADE,
 document TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_roster_bound_identity ON roster_entries(classroom_id, host, host_user_id) WHERE host_user_id <> '';
