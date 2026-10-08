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
CREATE TABLE IF NOT EXISTS calibration_sources (
 calibration_id TEXT NOT NULL REFERENCES calibrations(id) ON DELETE CASCADE,
 submission_id TEXT NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
 PRIMARY KEY (calibration_id,submission_id)
);
