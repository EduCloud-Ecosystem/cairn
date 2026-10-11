-- Frozen SQLite schema from Cairn initial commit d2fa742.

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
    id            TEXT PRIMARY KEY,
    submission_id TEXT NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
    score         REAL NOT NULL,
    max_score     REAL NOT NULL,
    breakdown     BLOB,
    run_id        TEXT NOT NULL DEFAULT '',
    graded_at     TEXT NOT NULL
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
CREATE INDEX IF NOT EXISTS idx_assignments_classroom ON assignments (classroom_id);
CREATE INDEX IF NOT EXISTS idx_submissions_assignment ON submissions (assignment_id);
CREATE INDEX IF NOT EXISTS idx_jobs_status_scheduled ON provisioning_jobs (status, scheduled_at);
