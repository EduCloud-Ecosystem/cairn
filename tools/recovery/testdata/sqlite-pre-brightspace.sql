-- Frozen SQLite schema from Cairn 1854e01 (pre-Brightspace / immutable roster identity).
CREATE TABLE IF NOT EXISTS calibration_sources (
 calibration_id TEXT NOT NULL REFERENCES calibrations(id) ON DELETE CASCADE,
 submission_id TEXT NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
 PRIMARY KEY (calibration_id,submission_id)
);
CREATE TABLE IF NOT EXISTS calibrations (
 id TEXT PRIMARY KEY,
 owner_id TEXT NOT NULL,
 assignment_id TEXT NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
 rubric_digest TEXT NOT NULL,
 revision INTEGER NOT NULL,
 status TEXT NOT NULL CHECK (status IN ('draft','ready')),
 document TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_calibration_owner_assignment ON calibrations(owner_id,assignment_id);
CREATE TABLE IF NOT EXISTS assessment_provider_control (
 id INTEGER PRIMARY KEY CHECK (id=1), paused INTEGER NOT NULL DEFAULT 0
);
INSERT INTO assessment_provider_control (id,paused) VALUES (1,0) ON CONFLICT (id) DO NOTHING;
CREATE TABLE IF NOT EXISTS assessment_generations (
 assessment_id TEXT PRIMARY KEY,
 learner_id TEXT NOT NULL,
 day TEXT NOT NULL,
 reserved_units INTEGER NOT NULL,
 status TEXT NOT NULL,
 error_code TEXT NOT NULL DEFAULT '',
 input_tokens INTEGER NOT NULL DEFAULT 0,
 output_tokens INTEGER NOT NULL DEFAULT 0,
 response_id TEXT NOT NULL DEFAULT '',
 model TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_generation_day ON assessment_generations(day);
CREATE TABLE IF NOT EXISTS assessment_rubrics (
 assignment_id TEXT PRIMARY KEY REFERENCES assignments(id) ON DELETE CASCADE,
 digest TEXT NOT NULL,
 document TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS assessments (
 id TEXT PRIMARY KEY,
 submission_id TEXT NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
 revision TEXT NOT NULL,
 rubric_digest TEXT NOT NULL,
 submission_activity TEXT NOT NULL,
 grade_id TEXT REFERENCES grades(id) ON DELETE CASCADE,
 status TEXT NOT NULL CHECK (status IN ('collected','blocked','pending','approved','rejected')),
 document TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_assessment_submission ON assessments(submission_id);

CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    host          TEXT NOT NULL,
    host_user_id  TEXT NOT NULL,
    host_username TEXT NOT NULL,
    email         TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    UNIQUE (host, host_user_id)
);

CREATE TABLE IF NOT EXISTS classrooms (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    host           TEXT NOT NULL,
    host_namespace TEXT NOT NULL,
    join_policy    TEXT NOT NULL DEFAULT 'open',
    created_by     TEXT REFERENCES users(id),
    created_at     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS assignments (
    id            TEXT PRIMARY KEY,
    classroom_id  TEXT NOT NULL REFERENCES classrooms(id) ON DELETE CASCADE,
    title         TEXT NOT NULL,
    slug          TEXT NOT NULL,
    template_host TEXT NOT NULL,
    template_ns   TEXT NOT NULL,
    template_name TEXT NOT NULL,
    template_ref  TEXT NOT NULL DEFAULT '',
    type          TEXT NOT NULL DEFAULT 'individual',
    deadline      TEXT,
    grading_spec  TEXT NOT NULL DEFAULT 'grading.json',
    access_policy TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    UNIQUE (classroom_id, slug)
);

CREATE TABLE IF NOT EXISTS roster_entries (
    id            TEXT PRIMARY KEY,
    classroom_id  TEXT NOT NULL REFERENCES classrooms(id) ON DELETE CASCADE,
    host          TEXT NOT NULL,
    host_username TEXT NOT NULL,
    email_hash    TEXT,
    status        TEXT NOT NULL DEFAULT 'invited',
    claimed_at    TEXT,
    UNIQUE (classroom_id, host, host_username)
);

CREATE TABLE IF NOT EXISTS submissions (
    id               TEXT PRIMARY KEY,
    assignment_id    TEXT NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    roster_entry_id  TEXT NOT NULL REFERENCES roster_entries(id) ON DELETE CASCADE,
    repo_host        TEXT NOT NULL DEFAULT '',
    repo_namespace   TEXT NOT NULL DEFAULT '',
    repo_name        TEXT NOT NULL DEFAULT '',
    latest_commit    TEXT NOT NULL DEFAULT '',
    last_activity_at TEXT,
    status           TEXT NOT NULL DEFAULT '',
    last_error       TEXT NOT NULL DEFAULT '',
    UNIQUE (assignment_id, roster_entry_id)
);

CREATE TABLE IF NOT EXISTS grades (
    id                  TEXT PRIMARY KEY,
    submission_id       TEXT NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
    score               REAL NOT NULL,
    max_score           REAL NOT NULL,
    breakdown           BLOB,
    run_id              TEXT NOT NULL DEFAULT '',
    graded_at           TEXT NOT NULL,
    export_confirmed_at TEXT
);

CREATE TABLE IF NOT EXISTS provisioning_jobs (
    id              TEXT PRIMARY KEY,
    type            TEXT NOT NULL,
    target_ref      TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    attempts        INTEGER NOT NULL DEFAULT 0,
    idempotency_key TEXT NOT NULL UNIQUE,
    last_error      TEXT NOT NULL DEFAULT '',
    scheduled_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS grading_runs (
    id            TEXT PRIMARY KEY,
    submission_id TEXT NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
    status        TEXT NOT NULL DEFAULT 'pending',
    runner        TEXT NOT NULL DEFAULT '',
    started_at    TEXT,
    finished_at   TEXT,
    result        BLOB,
    logs_ref      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_roster_classroom      ON roster_entries (classroom_id);
CREATE INDEX IF NOT EXISTS idx_roster_host_username  ON roster_entries (host, host_username);
CREATE INDEX IF NOT EXISTS idx_assignments_classroom ON assignments (classroom_id);
CREATE INDEX IF NOT EXISTS idx_submissions_assignment ON submissions (assignment_id);
CREATE INDEX IF NOT EXISTS idx_submissions_repo       ON submissions (repo_host, repo_namespace, repo_name);
CREATE INDEX IF NOT EXISTS idx_jobs_status_scheduled ON provisioning_jobs (status, scheduled_at);
