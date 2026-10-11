-- Synthetic records only. No provider requests, real identities or credentials.
INSERT INTO users VALUES ('teacher','github','100','teacher-fixture','teacher@example.invalid','2026-10-01T12:00:00Z');
INSERT INTO classrooms (id,name,host,host_namespace,created_by,created_at) VALUES ('course','Recovery fixture','github','fixture','teacher','2026-10-01T12:00:00Z');
INSERT INTO assignments (id,classroom_id,title,slug,template_host,template_ns,template_name,type,created_at) VALUES ('assignment','course','Python/R coursework','lab','github','fixture','template','individual','2026-10-01T12:00:00Z');
INSERT INTO roster_entries (id,classroom_id,host,host_username,status,claimed_at) VALUES ('learner','course','github','learner-fixture','active','2026-10-01T12:00:00Z');
INSERT INTO submissions (id,assignment_id,roster_entry_id,repo_host,repo_namespace,repo_name,latest_commit,status,last_activity_at) VALUES ('submission','assignment','learner','github','fixture','lab','commit-fixture','active','2026-10-01T12:00:00Z');
INSERT INTO grades (id,submission_id,score,max_score,breakdown,run_id,graded_at,export_confirmed_at) VALUES ('grade','submission',8,10,'{"criterion":8}','run','2026-10-01T12:00:00Z','2026-10-01T13:00:00Z');
INSERT INTO grading_runs (id,submission_id,status,runner,started_at,finished_at,result) VALUES ('run','submission','completed','docker','2026-10-01T12:00:00Z','2026-10-01T12:01:00Z','{"score":8}');
INSERT INTO provisioning_jobs (id,type,target_ref,status,attempts,idempotency_key,scheduled_at) VALUES ('job','create_repo','submission','in_progress',1,'fixture-job','2026-10-01T12:00:00Z');
INSERT INTO assessment_rubrics VALUES ('assignment','rubric-fixture','{"criteria":["reasoning"]}');
INSERT INTO assessments VALUES ('assessment','submission','commit-fixture','rubric-fixture','2026-10-01T12:00:00Z','grade','approved','{"review":{"decision":"approve","reviewer_id":"teacher"},"feedback":"Explain the variance."}');
INSERT INTO calibrations VALUES ('calibration','teacher','assignment','rubric-fixture',2,'ready','{"examples":[{"source":"historical-upload","score":8}],"decision":"instructor-approved"}');
INSERT INTO calibration_sources VALUES ('calibration','submission');
INSERT INTO assessment_generations VALUES ('assessment','learner','2026-10-01',100,'completed','',30,20,'fixture-response','fixture-model');
UPDATE assessment_provider_control SET paused=1;
