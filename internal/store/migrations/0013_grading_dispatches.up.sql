CREATE TABLE IF NOT EXISTS grading_dispatches (
 job_id TEXT PRIMARY KEY REFERENCES provisioning_jobs(id) ON DELETE RESTRICT,
 submission_id TEXT NOT NULL,
 target_sha256 TEXT NOT NULL,
 context_sha256 TEXT NOT NULL,
 claim_sha256 TEXT NOT NULL,
 run_id TEXT NOT NULL UNIQUE,
 runner TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('claimed','held','published')),
 grade_id TEXT NOT NULL DEFAULT '',
 result_sha256 TEXT NOT NULL DEFAULT ''
);
