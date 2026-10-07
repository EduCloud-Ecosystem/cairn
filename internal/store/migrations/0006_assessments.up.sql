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
