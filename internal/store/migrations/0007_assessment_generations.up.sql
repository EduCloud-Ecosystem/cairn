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
