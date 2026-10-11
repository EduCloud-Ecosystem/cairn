import { useEffect, useRef, useState } from "react";
import {
  api,
  type Calibration,
  type CalibrationExample,
  type Judgment,
  type Classroom,
  type Assignment,
  type SubmissionView,
  type AssessmentRubric,
  type CalibrationInputCheck,
  type CalibrationCoverage,
} from "../api";
import { Button, type Notify } from "./ui";
import { CalibrationEvidence } from "./CalibrationEvidence";

export function CalibrationPanel({
  assignmentID,
  canGenerate,
  notify,
  selected,
  onSelect,
}: {
  assignmentID: string;
  canGenerate: boolean;
  notify: Notify;
  selected: string;
  onSelect: (id: string) => void;
}) {
  const [profiles, setProfiles] = useState<Calibration[]>([]);
  const [record, setRecord] = useState<Calibration | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let live = true;
    api
      .listCalibrations(assignmentID)
      .then((v) => {
        if (live) setProfiles(v);
      })
      .catch((e) => {
        if (live) notify(String(e), "err");
      });
    return () => {
      live = false;
    };
  }, [assignmentID, notify]);
  async function run(action: () => Promise<Calibration | null>) {
    setBusy(true);
    try {
      const next = await action();
      setRecord(next);
      setProfiles(await api.listCalibrations(assignmentID));
      if (next?.status === "ready" && !next.document?.section)
        onSelect(next.id);
    } catch (e) {
      notify(String(e), "err");
      if (record) {
        try {
          setRecord(await api.calibration(record.id));
        } catch {
          setRecord(null);
          onSelect("");
        }
      }
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label="Your instructor calibration" className="card">
      <h4>Your instructor calibration</h4>
      <p>
        Use representative past work to compare proposed feedback with your own
        judgment. Your profiles belong to you and this assignment rubric.
        Historical examples never publish grades.
      </p>
      <label>
        Calibration for new assessments
        <select
          className="input"
          value={selected}
          disabled={busy}
          onChange={(e) => onSelect(e.target.value)}
        >
          <option value="">No instructor calibration selected</option>
          {profiles
            .filter((p) => p.status === "ready" && !p.section_title)
            .map((p) => (
              <option key={p.id} value={p.id}>
                Reviewed profile {p.id.slice(0, 8)} · revision {p.revision}
              </option>
            ))}
        </select>
      </label>
      <p>
        Changing the rubric or model requires a fresh profile. Existing
        assessments retain their captured profile. Calibration guides proposals;
        you still review every live grade.
      </p>
      <Button
        disabled={busy}
        onClick={() =>
          void run(async () => {
            onSelect("");
            return api.createCalibration(assignmentID, selected);
          })
        }
      >
        {selected
          ? "Test selected profile on another historical sample"
          : "Start a calibration"}
      </Button>
      <SectionStart
        assignmentID={assignmentID}
        busy={busy}
        run={run}
        notify={notify}
      />
      <CoverageReview
        key={profiles.map((p) => `${p.id}:${p.revision}`).join(",")}
        assignmentID={assignmentID}
        profiles={profiles}
        notify={notify}
      />
      {record?.status === "ready" && record.document?.section ? (
        <Button
          disabled={busy}
          onClick={() =>
            void run(() => api.createCalibration(assignmentID, record.id))
          }
        >
          Test this section on reserved examples
        </Button>
      ) : null}
      <label>
        Open a saved calibration
        <select
          className="input"
          value={record?.id || ""}
          disabled={busy}
          onChange={(e) => {
            const id = e.target.value;
            void run(() => (id ? api.calibration(id) : Promise.resolve(null)));
          }}
        >
          <option value="">Choose a profile</option>
          {profiles.map((p) => (
            <option key={p.id} value={p.id}>
              {p.section_title || p.id.slice(0, 8)} · {p.status} · revision{" "}
              {p.revision}
            </option>
          ))}
        </select>
      </label>
      {busy ? (
        <p role="status">
          Saving or generating… model requests may take up to one minute.
        </p>
      ) : null}
      {record?.document ? (
        <CalibrationEditor
          key={`${record.id}:${record.revision}`}
          record={record}
          busy={busy}
          canGenerate={canGenerate}
          run={run}
          notify={notify}
        />
      ) : null}
      {record ? (
        <Button
          disabled={busy}
          onClick={() => {
            if (
              window.confirm(
                "Delete this calibration and its historical examples? Existing assessments keep their captured guidance. Usage reservations remain. This cannot be undone.",
              )
            ) {
              void run(async () => {
                await api.deleteCalibration(record.id);
                if (selected === record.id) onSelect("");
                return null;
              });
            }
          }}
        >
          Delete calibration and historical examples
        </Button>
      ) : null}
    </section>
  );
}

function CalibrationEditor({
  record,
  busy,
  canGenerate,
  run,
  notify,
}: {
  record: Calibration;
  busy: boolean;
  canGenerate: boolean;
  run: (fn: () => Promise<Calibration | null>) => Promise<void>;
  notify: Notify;
}) {
  const d = record.document!;
  const [inputChecks, setInputChecks] = useState<
    CalibrationInputCheck[] | null
  >(null);
  const [checking, setChecking] = useState(false);
  const [files, setFiles] = useState<Record<string, string>>({});
  const [guidance, setGuidance] = useState(d.guidance);
  const ready = record.status === "ready";
  const reviewed = d.examples
    .filter((e) => e.review && !e.exclusion_note)
    .flatMap((e) =>
      e.review!.criteria.map((j) => ({
        actual: j.points,
        proposed: e.document.proposal?.criteria.find(
          (p) => p.criterion_id === j.criterion_id,
        )?.points,
      })),
    );
  const matches = reviewed.filter((j) => j.actual === j.proposed).length;
  return (
    <div>
      <p>
        Profile {record.id.slice(0, 8)} · revision {record.revision} ·{" "}
        {record.status}. Model: {d.model}.
      </p>
      <p>
        Agreement with your reviewed points: {matches} / {reviewed.length}{" "}
        criteria. Excluded examples:{" "}
        {d.examples.filter((e) => e.exclusion_note).length}. Review completion
        does not establish accuracy on new work.
      </p>
      <p>
        Independent references: {d.examples.filter((e) => e.reference).length}.
        Examples with assisted judgments:{" "}
        {
          d.examples.filter((e) =>
            e.review_evidence?.some((v) => v.judgments?.length),
          ).length
        }
        . Assisted review is not blind validation.
      </p>
      {d.bundle_digest ? (
        <p>
          Imported {d.sample_purpose} sample. Membership is frozen; each example
          still needs source inspection, generation and your review.
        </p>
      ) : null}
      <details>
        <summary>Captured rubric</summary>
        {d.rubric.criteria.map((c) => (
          <p key={c.id}>
            <strong>
              {c.id} ({c.max_points} points):
            </strong>{" "}
            {c.description}
          </p>
        ))}
      </details>
      {!ready && !d.bundle_digest ? (
        <fieldset disabled={busy || d.examples.length >= 9}>
          <legend>Add past course work</legend>
          <p>
            Use work you are permitted to reuse. Remove names and other
            identifying details before upload. Source text is not automatically
            redacted. Start with a small range of strong, partial and incorrect
            work; at most nine examples.
          </p>
          {d.rubric.paths.map((path) => (
            <label key={path}>
              Historical file for {path}
              <input
                type="file"
                accept=".txt,.md,.qmd,.rmd,.Rmd,.py,.r,.R,.ipynb"
                onChange={async (e) => {
                  const file = e.target.files?.[0];
                  setFiles((prev) => {
                    const next = { ...prev };
                    delete next[path];
                    return next;
                  });
                  if (!file) return;
                  try {
                    if (file.size > 256 * 1024)
                      throw Error(
                        "Each historical file must be at most 256 KiB.",
                      );
                    const source = new TextDecoder("utf-8", {
                      fatal: true,
                    }).decode(await file.arrayBuffer());
                    setFiles((prev) => ({ ...prev, [path]: source }));
                  } catch (err) {
                    notify(String(err), "err");
                  }
                }}
              />
            </label>
          ))}
          <Button
            disabled={d.rubric.paths.some((p) => files[p] === undefined)}
            onClick={() =>
              void run(() =>
                api.addCalibrationExample(record.id, record.revision, files),
              )
            }
          >
            Save uploaded example without sending to model
          </Button>
          <HistoricalSourcePicker record={record} run={run} notify={notify} />
          {d.examples.length === 0 ? (
            <BundleImport record={record} run={run} notify={notify} />
          ) : null}
        </fieldset>
      ) : null}
      {d.examples.map((e, index) => (
        <ExampleReview
          key={e.id}
          record={record}
          example={e}
          index={index}
          busy={busy}
          canGenerate={canGenerate}
          run={run}
        />
      ))}
      <label>
        Reusable instructor guidance
        <textarea
          className="input"
          rows={5}
          maxLength={2000}
          disabled={busy || ready || checking}
          value={guidance}
          onChange={(e) => {
            setGuidance(e.target.value);
            setInputChecks(null);
          }}
          aria-describedby={`guidance-${record.id}`}
        />
      </label>
      <p id={`guidance-${record.id}`}>
        Summarize what your corrections mean for future feedback: expected
        reasoning, useful explanations, or rubric interpretation. Do not include
        student details or copy past submissions. Only this approved guidance
        accompanies future work; model weights are not trained.
      </p>
      {!ready ? (
        <>
          <Button
            disabled={busy || checking}
            onClick={() => {
              setChecking(true);
              void api
                .preflightCalibration(record.id, record.revision, guidance)
                .then(setInputChecks)
                .catch((e) => notify(String(e), "err"))
                .finally(() => setChecking(false));
            }}
          >
            Check example sizes with this guidance
          </Button>
          <p>
            This checks included examples only. Reserved and future work must
            also be checked with the approved guidance.
          </p>
          {inputChecks?.map((c) => (
            <p key={c.example_id}>
              {c.fits ? "Fits" : "Blocked"}: {c.input_bytes} / {c.limit_bytes}{" "}
              bytes. {c.issue}
            </p>
          ))}
        </>
      ) : null}
      {!ready ? (
        <Button
          disabled={
            busy ||
            checking ||
            !inputChecks ||
            inputChecks.some((c) => !c.fits) ||
            !guidance.trim() ||
            d.examples.length === 0 ||
            d.examples.every((e) => !!e.exclusion_note) ||
            d.examples.some((e) => !e.exclusion_note && !e.review)
          }
          onClick={() =>
            void run(() =>
              api.approveCalibration(record.id, record.revision, guidance),
            )
          }
        >
          {d.section
            ? "Approve this section for evaluation"
            : "Approve profile for new assessments"}
        </Button>
      ) : (
        <p>
          This reviewed profile is frozen. Start a fresh calibration to revise
          your guidance and review another sample.
        </p>
      )}
    </div>
  );
}

function HistoricalSourcePicker({
  record,
  run,
  notify,
}: {
  record: Calibration;
  run: (fn: () => Promise<Calibration | null>) => Promise<void>;
  notify: Notify;
}) {
  const [classrooms, setClassrooms] = useState<Classroom[]>([]);
  const [assignments, setAssignments] = useState<Assignment[]>([]);
  const [submissions, setSubmissions] = useState<SubmissionView[]>([]);
  const [classroom, setClassroom] = useState("");
  const [assignment, setAssignment] = useState("");
  const [submission, setSubmission] = useState("");
  const [commit, setCommit] = useState("");
  useEffect(() => {
    let live = true;
    api
      .listClassrooms()
      .then((v) => {
        if (live) setClassrooms(v);
      })
      .catch((e) => {
        if (live) notify(String(e), "err");
      });
    return () => {
      live = false;
    };
  }, [notify]);
  useEffect(() => {
    let live = true;
    if (classroom)
      api
        .listAssignments(classroom)
        .then((v) => {
          if (live) setAssignments(v);
        })
        .catch((e) => {
          if (live) notify(String(e), "err");
        });
    return () => {
      live = false;
    };
  }, [classroom, notify]);
  useEffect(() => {
    let live = true;
    if (assignment)
      api
        .listSubmissions(assignment)
        .then((v) => {
          if (live) setSubmissions(v);
        })
        .catch((e) => {
          if (live) notify(String(e), "err");
        });
    return () => {
      live = false;
    };
  }, [assignment, notify]);
  return (
    <details>
      <summary>Or select an existing Cairn submission</summary>
      <label>
        Past classroom
        <select
          className="input"
          value={classroom}
          onChange={(e) => {
            setClassroom(e.target.value);
            setAssignment("");
            setSubmission("");
            setAssignments([]);
            setSubmissions([]);
          }}
        >
          <option value="">Choose a classroom</option>
          {classrooms.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
      </label>
      <label>
        Past assignment
        <select
          className="input"
          value={assignment}
          onChange={(e) => {
            setAssignment(e.target.value);
            setSubmission("");
            setSubmissions([]);
          }}
        >
          <option value="">Choose an assignment</option>
          {assignments.map((a) => (
            <option key={a.id} value={a.id}>
              {a.title}
            </option>
          ))}
        </select>
      </label>
      <label>
        Past submission
        <select
          className="input"
          value={submission}
          onChange={(e) => setSubmission(e.target.value)}
        >
          <option value="">Choose a submission</option>
          {submissions.map((s) => (
            <option key={s.id} value={s.id}>
              {s.username || s.id}
            </option>
          ))}
        </select>
      </label>
      <label>
        Historical commit (optional)
        <input
          className="input"
          value={commit}
          onChange={(e) => setCommit(e.target.value)}
          aria-describedby={`commit-${record.id}`}
        />
      </label>
      <p id={`commit-${record.id}`}>
        Leave empty to capture the recorded latest commit, or supply a full
        commit ID. The resulting copy is pinned. The current calibration
        rubric's required paths must exist at that commit.
      </p>
      <Button
        disabled={!submission}
        onClick={() =>
          void run(() =>
            api.captureCalibrationExample(
              record.id,
              record.revision,
              submission,
              commit.trim(),
            ),
          )
        }
      >
        Capture historical example without sending to model
      </Button>
    </details>
  );
}

function ExampleReview({
  record,
  example: e,
  index,
  busy,
  canGenerate,
  run,
}: {
  record: Calibration;
  example: CalibrationExample;
  index: number;
  busy: boolean;
  canGenerate: boolean;
  run: (fn: () => Promise<Calibration | null>) => Promise<void>;
}) {
  const [sourceReviewed, setSourceReviewed] = useState(false);
  const [criteria, setCriteria] = useState<Judgment[]>(
    () =>
      e.review?.criteria ||
      e.reference?.criteria ||
      e.document.proposal?.criteria ||
      e.document.rubric.criteria.map((c) => ({
        criterion_id: c.id,
        points: null,
        feedback: "",
        uncertainty: "Instructor reference; execution not verified.",
        citations: [],
      })),
  );
  const [note, setNote] = useState(e.review?.note || e.reference?.note || "");
  const [exclusion, setExclusion] = useState("");
  const ready = record.status === "ready";
  const assisted = !!e.review_evidence?.some((v) => v.judgments?.length);
  function change(index: number, patch: Partial<Judgment>) {
    setCriteria((old) =>
      old.map((c, i) => (i === index ? { ...c, ...patch } : c)),
    );
  }
  return (
    <article className="card">
      <h5>
        Historical example {index + 1}
        {e.sample_id ? ` · ${e.sample_id}` : ""}
      </h5>
      <p>
        Source:{" "}
        {e.source_submission_id
          ? `Cairn submission at ${e.document.revision}`
          : "uploaded files"}
        . {e.review ? "Instructor review saved." : "Instructor review pending."}
      </p>
      <details>
        <summary>Inspect captured source before sending</summary>
        {e.document.artifacts.map((a) => (
          <section key={a.path}>
            <h5>{a.path}</h5>
            {a.segments.map((s) => (
              <pre
                key={s.location}
                style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
              >
                {s.location}: {s.text}
              </pre>
            ))}
          </section>
        ))}
      </details>
      <CalibrationEvidence
        record={record}
        example={e}
        busy={busy}
        run={run}
        onUse={(judgments) =>
          setCriteria((old) =>
            old.map(
              (c) =>
                judgments.find((j) => j.criterion_id === c.criterion_id) || c,
            ),
          )
        }
      />
      {assisted ? (
        <p>
          Assisted review material is attached. It is not an independent
          instructor reference and does not count as a saved instructor review.
        </p>
      ) : null}
      {e.generation ? (
        <p>
          Model request: {e.generation.status}
          {e.generation.error_code ? ` (${e.generation.error_code})` : ""}.
          Failed or uncertain attempts are not automatically retried.
        </p>
      ) : null}
      {!ready && !e.exclusion_note && !e.document.proposal && !e.generation ? (
        <fieldset disabled={busy || !canGenerate}>
          <label>
            <input
              type="checkbox"
              checked={sourceReviewed}
              onChange={(ev) => setSourceReviewed(ev.target.checked)}
            />{" "}
            I reviewed this source and am authorized to send it to OpenAI for
            calibration.
          </label>
          <Button
            disabled={
              !sourceReviewed ||
              (!!record.document?.section && !e.reference && !assisted)
            }
            onClick={() =>
              void run(() =>
                api.generateCalibration(
                  record.id,
                  e.id,
                  record.revision,
                  e.document.input_digest,
                ),
              )
            }
          >
            Send historical source to OpenAI
          </Button>
        </fieldset>
      ) : null}
      {e.exclusion_note ? (
        <p>Excluded from calibration: {e.exclusion_note}</p>
      ) : null}
      {!ready && !e.exclusion_note ? (
        <details>
          <summary>Exclude this example from calibration</summary>
          <label>
            Reason for exclusion
            <textarea
              className="input"
              disabled={busy}
              value={exclusion}
              onChange={(ev) => setExclusion(ev.target.value)}
            />
          </label>
          <Button
            disabled={busy || !exclusion.trim()}
            onClick={() =>
              void run(() =>
                api.excludeCalibration(
                  record.id,
                  e.id,
                  record.revision,
                  exclusion,
                ),
              )
            }
          >
            Record exclusion and retain evidence
          </Button>
        </details>
      ) : null}
      {record.document?.section && !e.reference && !assisted ? (
        <p>
          Save your independent reference judgments before generating feedback
          for this section.
        </p>
      ) : null}
      {e.reference ? (
        <details>
          <summary>Frozen independent instructor reference</summary>
          <p>{e.reference.note}</p>
          {e.reference.criteria.map((c) => (
            <p key={c.criterion_id}>
              {c.criterion_id}: {c.points ?? "Unassessable"} — {c.feedback}
            </p>
          ))}
        </details>
      ) : null}
      <>
        {e.document.proposal ? (
          <details>
            <summary>Original proposed feedback and scores</summary>
            {e.document.proposal.criteria.map((c) => (
              <p key={c.criterion_id}>
                {c.criterion_id}: {c.points ?? "Unassessable"} — {c.feedback}{" "}
                {c.uncertainty}
              </p>
            ))}
          </details>
        ) : null}
        <fieldset
          disabled={
            busy ||
            ready ||
            !!e.exclusion_note ||
            (!e.document.proposal &&
              (!!e.reference || !!e.generation || assisted))
          }
        >
          <legend>
            {e.document.proposal
              ? "Your calibration judgments"
              : assisted
                ? "Independent reference unavailable after assisted judgments"
                : "Your independent reference judgments"}
          </legend>
          {criteria.map((c, i) => (
            <fieldset key={c.criterion_id}>
              <legend>{c.criterion_id}</legend>
              <p>
                {e.document.proposal ? (
                  <>
                    Proposed:{" "}
                    {e.document.proposal.criteria.find(
                      (j) => j.criterion_id === c.criterion_id,
                    )?.points ?? "Unassessable"}
                    .{" "}
                  </>
                ) : (
                  <>No model proposal has been generated. </>
                )}
                Maximum:{" "}
                {
                  e.document.rubric.criteria.find(
                    (j) => j.id === c.criterion_id,
                  )?.max_points
                }
                .
              </p>
              <label>
                Your points (empty means unassessable)
                <input
                  className="input"
                  type="number"
                  min="0"
                  max={
                    e.document.rubric.criteria.find(
                      (j) => j.id === c.criterion_id,
                    )?.max_points
                  }
                  step="any"
                  value={c.points ?? ""}
                  onChange={(ev) =>
                    change(i, {
                      points:
                        ev.target.value === "" ? null : Number(ev.target.value),
                    })
                  }
                />
              </label>
              <label>
                Your corrected feedback
                <textarea
                  className="input"
                  value={c.feedback}
                  onChange={(ev) => change(i, { feedback: ev.target.value })}
                />
              </label>
              <label>
                Uncertainty or limitations
                <textarea
                  className="input"
                  value={c.uncertainty}
                  onChange={(ev) => change(i, { uncertainty: ev.target.value })}
                />
              </label>
              <ul>
                {c.citations.map((citation, j) => (
                  <li key={j}>
                    {citation.path} · {citation.location}{" "}
                    {citation.quote ? (
                      <blockquote
                        style={{
                          whiteSpace: "pre-wrap",
                          overflowWrap: "anywhere",
                        }}
                      >
                        {citation.quote}
                      </blockquote>
                    ) : null}
                    <button
                      onClick={() =>
                        change(i, {
                          citations: c.citations.filter((_, n) => n !== j),
                        })
                      }
                    >
                      Remove citation
                    </button>
                  </li>
                ))}
              </ul>
              <label>
                Add supporting evidence
                <select
                  className="input"
                  value=""
                  onChange={(ev) => {
                    const [a, s] = ev.target.value.split(":").map(Number);
                    const artifact = e.document.artifacts[a];
                    const segment = artifact?.segments[s];
                    if (segment)
                      change(i, {
                        citations: [
                          ...c.citations,
                          {
                            path: artifact.path,
                            sha256: artifact.sha256,
                            location: segment.location,
                          },
                        ],
                      });
                  }}
                >
                  <option value="">Choose a source location</option>
                  {e.document.artifacts.flatMap((a, ai) =>
                    a.segments.map((s, si) => (
                      <option key={`${ai}:${si}`} value={`${ai}:${si}`}>
                        {a.path} · {s.location}
                      </option>
                    )),
                  )}
                </select>
              </label>
            </fieldset>
          ))}
          <label>
            Why you agree or what should change
            <textarea
              className="input"
              value={note}
              onChange={(ev) => setNote(ev.target.value)}
            />
          </label>
          {!ready ? (
            <Button
              disabled={!note.trim()}
              onClick={() =>
                void run(() =>
                  (e.document.proposal
                    ? api.reviewCalibration
                    : api.referenceCalibration)(
                    record.id,
                    e.id,
                    record.revision,
                    criteria,
                    note,
                  ),
                )
              }
            >
              {e.document.proposal
                ? "Save instructor calibration review"
                : "Save independent reference before generation"}
            </Button>
          ) : null}
        </fieldset>
      </>
    </article>
  );
}

function BundleImport({
  record,
  run,
  notify,
}: {
  record: Calibration;
  run: (fn: () => Promise<Calibration | null>) => Promise<void>;
  notify: Notify;
}) {
  const [bundle, setBundle] = useState<unknown>(null);
  const [name, setName] = useState("");
  const readVersion = useRef(0);
  useEffect(
    () => () => {
      readVersion.current++;
    },
    [],
  );
  return (
    <details>
      <summary>Import a prepared historical sample</summary>
      <p>
        A source-only bundle must match this saved rubric exactly. Import
        creates no proposals or grades and sends nothing to OpenAI. Use a fresh
        round based on an approved profile for a held-out sample.
      </p>
      <label>
        Calibration bundle JSON
        <input
          type="file"
          accept=".json,application/json"
          onChange={async (event) => {
            const version = ++readVersion.current;
            const file = event.target.files?.[0];
            setBundle(null);
            setName("");
            if (!file) return;
            try {
              if (file.size > 900 * 1024)
                throw Error("Bundle must be at most 900 KiB.");
              const value = JSON.parse(await file.text());
              if (version !== readVersion.current) return;
              setBundle(value);
              setName(file.name);
            } catch (error) {
              if (version === readVersion.current) notify(String(error), "err");
            }
          }}
        />
      </label>
      {name ? <p>Selected: {name}</p> : null}
      <Button
        disabled={!bundle}
        onClick={() =>
          void run(() =>
            api.importCalibrationBundle(record.id, record.revision, bundle),
          )
        }
      >
        Import source for inspection
      </Button>
    </details>
  );
}

function SectionStart({
  assignmentID,
  busy,
  run,
  notify,
}: {
  assignmentID: string;
  busy: boolean;
  run: (fn: () => Promise<Calibration | null>) => Promise<void>;
  notify: Notify;
}) {
  const [section, setSection] = useState<AssessmentRubric | null>(null);
  const readVersion = useRef(0);
  useEffect(
    () => () => {
      readVersion.current++;
    },
    [],
  );
  return (
    <details>
      <summary>Start a section of this assignment</summary>
      <p>
        Save the complete assignment rubric first. Choose the section rubric
        from a prepared packet. Section criteria must keep the assignment’s
        descriptions, points, evidence requirements and starter files for its paths.
        Each section stays attached to the same parent
        assignment.
      </p>
      <label>
        Section rubric file
        <input
          type="file"
          accept=".json,application/json"
          disabled={busy}
          onChange={async (e) => {
            const version = ++readVersion.current;
            setSection(null);
            const file = e.target.files?.[0];
            if (!file) return;
            try {
              if (file.size > 900 * 1024)
                throw Error("Section rubric must be at most 900 KiB.");
              const value = JSON.parse(await file.text()) as AssessmentRubric;
              if (version === readVersion.current) setSection(value);
            } catch (err) {
              if (version === readVersion.current) notify(String(err), "err");
            }
          }}
        />
      </label>
      <Button
        disabled={busy || !section}
        onClick={() =>
          void run(() => api.createCalibration(assignmentID, "", section!))
        }
      >
        Create private section draft
      </Button>
    </details>
  );
}

function CoverageReview({
  assignmentID,
  profiles,
  notify,
}: {
  assignmentID: string;
  profiles: Calibration[];
  notify: Notify;
}) {
  const [ids, setIDs] = useState<string[]>([]);
  const [coverage, setCoverage] = useState<CalibrationCoverage | null>(null);
  const [checking, setChecking] = useState(false);
  return (
    <details>
      <summary>Check combined rubric coverage</summary>
      <p>
        Select the approved profiles for one review round. This checks for
        missing and repeated criteria; it does not combine scores, publish a
        grade, or make section profiles usable for whole-assignment generation.
      </p>
      {profiles
        .filter((p) => p.status === "ready")
        .map((p) => (
          <label key={p.id}>
            <input
              type="checkbox"
              disabled={checking}
              checked={ids.includes(p.id)}
              onChange={(e) => {
                setCoverage(null);
                setIDs(
                  e.target.checked
                    ? [...ids, p.id]
                    : ids.filter((id) => id !== p.id),
                );
              }}
            />
            {p.section_title || `Whole rubric ${p.id.slice(0, 8)}`}
          </label>
        ))}
      <Button
        disabled={checking || ids.length === 0}
        onClick={() => {
          setChecking(true);
          void api
            .calibrationCoverage(assignmentID, ids)
            .then(setCoverage)
            .catch((e) => notify(String(e), "err"))
            .finally(() => setChecking(false));
        }}
      >
        Check selected profiles
      </Button>
      {coverage ? (
        <div role="status">
          <p>
            {coverage.complete
              ? "Every criterion is covered exactly once."
              : "Coverage needs attention."}{" "}
            Covered rubric weight: {coverage.covered_points} /{" "}
            {coverage.max_points}.
          </p>
          <p>
            Missing: {coverage.missing.join(", ") || "None"}. Repeated:{" "}
            {coverage.overlapping.join(", ") || "None"}.
          </p>
        </div>
      ) : null}
    </details>
  );
}
