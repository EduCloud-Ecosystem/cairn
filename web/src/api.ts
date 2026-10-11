// Typed client for the Cairn control-plane API. Field names match the server's
// snake_case JSON. The base URL is empty by default (same-origin); in dev the
// Vite proxy forwards API prefixes to the Go server.

export type Host = string;

export interface Classroom {
  id: string;
  name: string;
  host: Host;
  host_namespace: string;
  // "open" (any authenticated student on the host may self-enroll) or "roster"
  // (only usernames already on the roster). Decides what the invite link's
  // accompanying note tells the instructor.
  join_policy: string;
  created_by: string;
  created_at: string;
}

export interface TemplateRef {
  host: Host;
  namespace: string;
  name: string;
  ref?: string;
}

export interface Assignment {
  id: string;
  classroom_id: string;
  title: string;
  slug: string;
  template: TemplateRef;
  type: string;
  deadline?: string;
  grading_spec: string;
  created_at: string;
}

export interface RosterEntry {
  id: string;
  classroom_id: string;
  host: Host;
  host_username: string;
  email_hash?: string;
  status: string;
  claimed_at?: string;
}

export interface BulkRosterEntry {
  username: string;
  email_hash?: string;
}

export interface BulkRosterResult {
  username: string;
  status: "created" | "already_present" | "error";
  error?: string;
  entry?: RosterEntry;
}

export interface BulkRosterResponse {
  created: number;
  already_present: number;
  errors: number;
  results: BulkRosterResult[];
}

export interface RepoRef {
  host: Host;
  namespace: string;
  name: string;
}

export interface SubmissionView {
  id: string;
  roster_entry_id: string;
  username: string;
  repo: RepoRef;
  status: string;
  score?: number;
  max_score?: number;
  graded_at?: string;
}

export interface EnqueueResult {
  status: string;
  jobs_enqueued: number;
}

export interface Operator {
  id?: string;
  username: string;
  auth: "enabled" | "disabled";
}

const BASE = (import.meta.env.VITE_API_BASE as string | undefined) ?? "";

async function req<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const res = await fetch(BASE + path, {
    method,
    credentials: "same-origin",
    headers:
      body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`;
    try {
      const parsed = JSON.parse(text) as { error?: string };
      if (parsed.error) message = parsed.error;
    } catch {
      /* non-JSON error body */
    }
    throw new Error(message);
  }
  return text ? (JSON.parse(text) as T) : (undefined as T);
}

export interface RubricCriterion {
  id: string;
  description: string;
  max_points: number;
  evidence?: "" | "python_implementation" | "python_authored";
}
export interface AssessmentRubric {
  title: string;
  paths: string[];
  criteria: RubricCriterion[];
  starter_files?: Record<string, string>;
}
export interface Evidence {
  quote?: string;
  path: string;
  sha256: string;
  location: string;
}
export interface Judgment {
  criterion_id: string;
  points: number | null;
  feedback: string;
  uncertainty: string;
  citations: Evidence[];
}
export interface AssessmentCapabilities {
  calibration?: boolean;
  review: boolean;
  model_provider: boolean;
  paused: boolean;
  classrooms: string[];
}
export interface AssessmentRecord {
  generation?: {
    status: string;
    error_code?: string;
    model: string;
    input_tokens: number;
    output_tokens: number;
    reserved_units: number;
  };
  id: string;
  submission_id: string;
  revision: string;
  status: string;
  document: {
    calibration?: {
      id: string;
      owner_id: string;
      revision: number;
      digest: string;
      guidance: string;
    };
    policy_version?: string;
    rubric: AssessmentRubric;
    input_digest: string;
    artifacts: {
      path: string;
      sha256: string;
      issue?: string;
      segments: { location: string; text: string }[];
    }[];
    proposal?: {
      submission_status?: "relevant_work" | "no_relevant_work";
      source: string;
      model: string;
      prompt_version: string;
      criteria: Judgment[];
    };
    review?: {
      reviewer: string;
      reviewed_at: string;
      note: string;
      criteria?: Judgment[];
    };
  };
}
export interface CalibrationInputCheck {
  example_id: string;
  input_bytes: number;
  limit_bytes: number;
  fits: boolean;
  issue?: string;
}
export interface CalibrationCoverage {
  complete: boolean;
  missing: string[];
  overlapping: string[];
  covered_points: number;
  max_points: number;
}
export interface CalibrationReviewEvidence {
  packet_digest: string;
  imported_by: string;
  imported_at: string;
  sample_id: string;
  rubric_digest: string;
  source_sha256: Record<string, string>;
  authorship:
    | "assistant"
    | "instructor-assisted"
    | "external-reviewer"
    | "execution-only";
  author: string;
  note: string;
  judgments?: Judgment[];
  execution?: {
    report_digest: string;
    checks_sha256: string;
    bundle_sha256: string;
    reported_at: string;
    image: string;
    unchecked_criteria: string[];
    sample: {
      sample_id: string;
      status: string;
      source_sha256: Record<string, string>;
      tests: {
        criterion_id: string;
        status: string;
        exit_code?: number;
        detail?: string;
      }[];
    };
  };
}
export interface CalibrationExample {
  review_evidence?: CalibrationReviewEvidence[];
  reference?: {
    reviewer: string;
    reviewed_at: string;
    note: string;
    criteria: Judgment[];
  };
  sample_id?: string;
  exclusion_note?: string;
  id: string;
  source_submission_id?: string;
  document: AssessmentRecord["document"] & { revision: string };
  review?: {
    reviewer: string;
    reviewed_at: string;
    note: string;
    criteria: Judgment[];
  };
  generation?: AssessmentRecord["generation"];
}
export interface Calibration {
  section_title?: string;
  id: string;
  owner_id: string;
  assignment_id: string;
  rubric_digest: string;
  revision: number;
  status: "draft" | "ready";
  document?: {
    section?: boolean;
    bundle_digest?: string;
    sample_purpose?: string;
    rubric: AssessmentRubric;
    model: string;
    prompt_version: string;
    policy_version: string;
    guidance: string;
    examples: CalibrationExample[];
  };
}
export const api = {
  listCalibrations: (id: string) =>
    req<Calibration[]>("GET", `/assignments/${id}/calibrations`),
  createCalibration: (id: string, based_on = "", section?: AssessmentRubric) =>
    req<Calibration>("POST", `/assignments/${id}/calibrations`, {
      based_on,
      section,
    }),
  calibrationCoverage: (id: string, profile_ids: string[]) =>
    req<CalibrationCoverage>(
      "POST",
      `/assignments/${id}/calibrations/coverage`,
      { profile_ids },
    ),
  preflightCalibration: (id: string, revision: number, guidance: string) =>
    req<CalibrationInputCheck[]>("POST", `/calibrations/${id}/preflight`, {
      revision,
      guidance,
    }),
  importCalibrationEvidence: (
    id: string,
    example: string,
    revision: number,
    packet: unknown,
  ) =>
    req<Calibration>(
      "POST",
      `/calibrations/${id}/examples/${example}/evidence`,
      { revision, packet },
    ),
  referenceCalibration: (
    id: string,
    example: string,
    revision: number,
    criteria: Judgment[],
    note: string,
  ) =>
    req<Calibration>(
      "POST",
      `/calibrations/${id}/examples/${example}/reference`,
      { revision, criteria, note },
    ),
  calibration: (id: string) => req<Calibration>("GET", `/calibrations/${id}`),
  deleteCalibration: (id: string) =>
    req<unknown>("DELETE", `/calibrations/${id}`, {}),
  importCalibrationBundle: (id: string, revision: number, bundle: unknown) =>
    req<Calibration>("POST", `/calibrations/${id}/bundle`, {
      revision,
      bundle,
    }),
  addCalibrationExample: (
    id: string,
    revision: number,
    files: Record<string, string>,
  ) =>
    req<Calibration>("POST", `/calibrations/${id}/examples`, {
      revision,
      files,
    }),
  captureCalibrationExample: (
    id: string,
    revision: number,
    submission_id: string,
    commit: string,
  ) =>
    req<Calibration>("POST", `/calibrations/${id}/capture`, {
      revision,
      submission_id,
      commit,
    }),
  generateCalibration: (
    id: string,
    example: string,
    revision: number,
    input_digest: string,
  ) =>
    req<Calibration>(
      "POST",
      `/calibrations/${id}/examples/${example}/generate`,
      { revision, input_digest, source_reviewed: true },
    ),
  reviewCalibration: (
    id: string,
    example: string,
    revision: number,
    criteria: Judgment[],
    note: string,
  ) =>
    req<Calibration>("POST", `/calibrations/${id}/examples/${example}/review`, {
      revision,
      criteria,
      note,
    }),
  excludeCalibration: (
    id: string,
    example: string,
    revision: number,
    note: string,
  ) =>
    req<Calibration>(
      "POST",
      `/calibrations/${id}/examples/${example}/exclude`,
      { revision, note },
    ),
  approveCalibration: (id: string, revision: number, guidance: string) =>
    req<Calibration>("POST", `/calibrations/${id}/approve`, {
      revision,
      guidance,
    }),
  assessmentCapabilities: () =>
    req<AssessmentCapabilities>("GET", "/assessment-capabilities"),
  generateAssessment: (id: string, input_digest: string) =>
    req<AssessmentRecord>("POST", `/assessments/${id}/generate`, {
      input_digest,
    }),
  pauseAssessmentProvider: (paused: boolean) =>
    req<unknown>("POST", "/assessment-provider/control", { paused }),
  assessmentRubric: (id: string) =>
    req<{ document: AssessmentRubric }>(
      "GET",
      `/assignments/${id}/assessment-rubric`,
    ),
  saveAssessmentRubric: (id: string, r: AssessmentRubric) =>
    req<unknown>("PUT", `/assignments/${id}/assessment-rubric`, r),
  assessments: (id: string) =>
    req<AssessmentRecord[]>("GET", `/submissions/${id}/assessments`),
  captureAssessment: (id: string, calibration_id = "") =>
    req<AssessmentRecord>("POST", `/submissions/${id}/assessments`, {
      calibration_id,
    }),
  importAssessment: (id: string, p: unknown) =>
    req<AssessmentRecord>("POST", `/assessments/${id}/proposal`, p),
  reviewAssessment: (
    id: string,
    action: string,
    note: string,
    criteria: Judgment[],
  ) =>
    req<AssessmentRecord>("POST", `/assessments/${id}/review`, {
      action,
      note,
      criteria,
    }),
  health: () => req<{ status: string }>("GET", "/healthz"),

  me: () => req<Operator>("GET", "/auth/me"),
  logout: () => req<{ status: string }>("POST", "/auth/logout"),
  loginUrl: () => `${BASE}/auth/login`,

  listClassrooms: () => req<Classroom[]>("GET", "/classrooms"),
  createClassroom: (b: {
    name: string;
    host: string;
    host_namespace: string;
  }) => req<Classroom>("POST", "/classrooms", b),

  listAssignments: (classroomID: string) =>
    req<Assignment[]>("GET", `/classrooms/${classroomID}/assignments`),
  createAssignment: (
    classroomID: string,
    b: {
      title: string;
      slug: string;
      template: { namespace: string; name: string; ref?: string };
      type?: string;
      grading_spec?: string;
      deadline?: string;
    },
  ) => req<Assignment>("POST", `/classrooms/${classroomID}/assignments`, b),

  listRoster: (classroomID: string) =>
    req<RosterEntry[]>("GET", `/classrooms/${classroomID}/roster`),
  addRoster: (
    classroomID: string,
    b: { username: string; email_hash?: string },
  ) => req<RosterEntry>("POST", `/classrooms/${classroomID}/roster`, b),
  addRosterBulk: (classroomID: string, entries: BulkRosterEntry[]) =>
    req<BulkRosterResponse>("POST", `/classrooms/${classroomID}/roster/bulk`, {
      entries,
    }),
  // Irreversible: deletes the roster row and every dependent submission,
  // grade, and grading run. Not the same thing as dropping a student from the
  // active roster — see RosterPanel's confirm copy.
  deleteRosterEntry: (classroomID: string, entryID: string) =>
    req<void>("DELETE", `/classrooms/${classroomID}/roster/${entryID}`),

  // Starts the retention countdown for this classroom's grades — deliberately
  // separate from gradesCsvUrl's download so a page reload can't silently
  // start it.
  confirmExport: (classroomID: string) =>
    req<{ confirmed: number }>(
      "POST",
      `/classrooms/${classroomID}/grades/confirm-export`,
    ),

  listSubmissions: (assignmentID: string) =>
    req<SubmissionView[]>("GET", `/assignments/${assignmentID}/submissions`),

  setGradingPolicy: (
    assignmentID: string,
    template_commit: string,
    grading_spec: string,
  ) =>
    req<Assignment>("PATCH", `/assignments/${assignmentID}/grading-policy`, {
      template_commit,
      grading_spec,
    }),

  setDeadline: (assignmentID: string, deadline: string | null) =>
    req<Assignment>("PATCH", `/assignments/${assignmentID}/deadline`, {
      deadline,
    }),

  lock: (assignmentID: string) =>
    req<EnqueueResult>("POST", `/assignments/${assignmentID}/lock`),
  unlock: (assignmentID: string) =>
    req<EnqueueResult>("POST", `/assignments/${assignmentID}/unlock`),
  grade: (assignmentID: string) =>
    req<EnqueueResult>("POST", `/assignments/${assignmentID}/grade`),

  gradesCsvUrl: (classroomID: string) =>
    `${BASE}/classrooms/${classroomID}/grades.csv`,

  // The student-facing join URL for an assignment. Unlike gradesCsvUrl this is
  // absolute: it is copied and pasted into an LMS or email, so a same-origin
  // relative path would be useless to the student receiving it.
  inviteUrl: (assignmentID: string) =>
    `${window.location.origin}${BASE}/assignments/${assignmentID}/accept`,
};
