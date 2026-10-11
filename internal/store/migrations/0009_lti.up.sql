CREATE TABLE IF NOT EXISTS lti_records (
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind IN ('link','binding','delivery')),
 unique_key TEXT NOT NULL UNIQUE,
 assignment_id TEXT NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
 roster_entry_id TEXT REFERENCES roster_entries(id) ON DELETE CASCADE,
 grade_id TEXT REFERENCES grades(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL,
 document TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_lti_roster ON lti_records(roster_entry_id);
CREATE INDEX IF NOT EXISTS idx_lti_grade ON lti_records(grade_id);
