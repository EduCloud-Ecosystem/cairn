import { useEffect, useState } from "react";
import {
  api,
  type AssessmentRecord,
  type AssessmentRubric,
  type Judgment,
  type SubmissionView,
} from "../api";
import { Button, type Notify } from "./ui";

const initial: AssessmentRubric = {
  title: "Coursework assessment",
  paths: ["response.md"],
  criteria: [
    {
      id: "reasoning",
      description: "Explain and support the reasoning with evidence.",
      max_points: 10,
    },
  ],
};

export function AssessmentPanel({
  assignmentID,
  submissions,
  notify,
  refreshSubmissions,
}: {
  assignmentID: string;
  submissions: SubmissionView[];
  notify: Notify;
  refreshSubmissions: () => Promise<void>;
}) {
  const [enabled, setEnabled] = useState(false);
  const [rubric, setRubric] = useState<AssessmentRubric>(initial);
  const [paths, setPaths] = useState(initial.paths.join("\n"));
  const [sub, setSub] = useState("");
  const [records, setRecords] = useState<AssessmentRecord[]>([]);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  useEffect(() => {
    let live = true;
    void api
      .assessmentCapabilities()
      .then(async (c) => {
        if (!live) return;
        setEnabled(c.review);
        if (!c.review) return;
        try {
          const r = await api.assessmentRubric(assignmentID);
          if (live) {
            setRubric(r.document);
            setPaths(r.document.paths.join("\n"));
            setSaved(true);
          }
        } catch {
          /* A new assignment has no saved assessment rubric. */
        }
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [assignmentID]);
  useEffect(() => {
    let live = true;
    setRecords([]);
    if (sub)
      void api
        .assessments(sub)
        .then((r) => {
          if (live) setRecords(r);
        })
        .catch((e) => notify(String(e), "err"));
    return () => {
      live = false;
    };
  }, [sub, notify]);
  async function run(fn: () => Promise<unknown>) {
    setBusy(true);
    try {
      await fn();
    } catch (e) {
      notify(e instanceof Error ? e.message : String(e), "err");
    } finally {
      setBusy(false);
    }
  }
  async function refresh() {
    setRecords(await api.assessments(sub));
    await refreshSubmissions();
  }
  if (!enabled) return null;
  return (
    <section className="assessment-panel" aria-label="Assessment review">
      <h3>Feedback and proposed scores</h3>
      <p>
        Review pilot: import a fixture or an instructor-prepared proposal. No
        model provider is connected. Publishing requires your review and creates
        a new recorded grade.
      </p>
      <fieldset disabled={busy}>
        <legend>Assessment rubric</legend>
        <label>
          Rubric title
          <input
            className="input"
            value={rubric.title}
            onChange={(e) => {
              setSaved(false);
              setRubric({ ...rubric, title: e.target.value });
            }}
          />
        </label>
        <label htmlFor={`artifact-paths-${assignmentID}`}>
          Required files in the submission repository
        </label>
        <textarea
          id={`artifact-paths-${assignmentID}`}
          className="input"
          rows={3}
          value={paths}
          aria-describedby={`artifact-help-${assignmentID}`}
          onChange={(e) => {
            setSaved(false);
            setPaths(e.target.value);
          }}
        />
        <p id={`artifact-help-${assignmentID}`} className="muted small">
          Each line becomes a separate file path. Press Enter or Return to add
          another. Text, Markdown, Python, R and notebook source are supported.
          All listed files are required; missing or unsupported files block
          assessment.
        </p>
        {rubric.criteria.map((c, i) => (
          <fieldset key={i}>
            <legend>Criterion {i + 1}</legend>
            <label>
              Identifier
              <input
                className="input"
                value={c.id}
                onChange={(e) => {
                  setSaved(false);
                  setRubric({
                    ...rubric,
                    criteria: rubric.criteria.map((v, j) =>
                      j === i ? { ...v, id: e.target.value } : v,
                    ),
                  });
                }}
              />
            </label>
            <label>
              Description
              <textarea
                className="input"
                value={c.description}
                onChange={(e) => {
                  setSaved(false);
                  setRubric({
                    ...rubric,
                    criteria: rubric.criteria.map((v, j) =>
                      j === i ? { ...v, description: e.target.value } : v,
                    ),
                  });
                }}
              />
            </label>
            <label>
              Maximum points
              <input
                className="input"
                type="number"
                min="0.01"
                max="10000"
                step="any"
                value={c.max_points}
                onChange={(e) => {
                  setSaved(false);
                  setRubric({
                    ...rubric,
                    criteria: rubric.criteria.map((v, j) =>
                      j === i
                        ? { ...v, max_points: Number(e.target.value) }
                        : v,
                    ),
                  });
                }}
              />
            </label>
            <Button
              small
              disabled={rubric.criteria.length === 1}
              onClick={() => {
                setSaved(false);
                setRubric({
                  ...rubric,
                  criteria: rubric.criteria.filter((_, j) => j !== i),
                });
              }}
            >
              Remove criterion {i + 1}
            </Button>
          </fieldset>
        ))}
        <Button
          small
          disabled={rubric.criteria.length >= 32}
          onClick={() => {
            setSaved(false);
            setRubric({
              ...rubric,
              criteria: [
                ...rubric.criteria,
                { id: "", description: "", max_points: 10 },
              ],
            });
          }}
        >
          Add criterion
        </Button>{" "}
        <Button
          small
          onClick={() =>
            void run(async () => {
              await api.saveAssessmentRubric(assignmentID, {
                ...rubric,
                paths: paths
                  .split("\n")
                  .map((v) => v.trim())
                  .filter(Boolean),
              });
              setSaved(true);
              notify(
                "Assessment rubric saved. Existing proposals must match this version.",
              );
            })
          }
        >
          Save assessment rubric
        </Button>
      </fieldset>
      <label>
        Submission
        <select
          className="input"
          disabled={busy}
          value={sub}
          onChange={(e) => setSub(e.target.value)}
        >
          <option value="">Choose a submission</option>
          {submissions.map((s) => (
            <option key={s.id} value={s.id}>
              {s.username || s.repo.name}
            </option>
          ))}
        </select>
      </label>
      <Button
        disabled={busy || !sub || !saved}
        onClick={() =>
          void run(async () => {
            await api.captureAssessment(sub);
            await refresh();
            notify("Submission evidence captured");
          })
        }
      >
        Capture current submission
      </Button>
      {records.map((r) => (
        <ReviewCard
          key={r.id}
          record={r}
          busy={busy}
          run={run}
          refresh={refresh}
        />
      ))}
    </section>
  );
}

function ReviewCard({
  record: r,
  busy,
  run,
  refresh,
}: {
  record: AssessmentRecord;
  busy: boolean;
  run: (fn: () => Promise<unknown>) => Promise<void>;
  refresh: () => Promise<void>;
}) {
  const [criteria, setCriteria] = useState<Judgment[]>([]);
  const [note, setNote] = useState("");
  useEffect(() => {
    setCriteria(
      r.document.proposal?.criteria.map((c) => ({
        ...c,
        citations: [...c.citations],
      })) || [],
    );
  }, [r]);
  const terminal = r.status === "approved" || r.status === "rejected";
  function change(i: number, patch: Partial<Judgment>) {
    setCriteria(criteria.map((c, j) => (i === j ? { ...c, ...patch } : c)));
  }
  return (
    <article className="card">
      <h4>Assessment: {r.status}</h4>
      <p>
        Submission commit: <code>{r.revision}</code>
      </p>
      <details>
        <summary>Captured evidence</summary>
        {r.document.artifacts.map((a) => (
          <section key={a.path}>
            <h4>{a.path}</h4>
            {a.issue ? (
              <p role="alert">{a.issue}</p>
            ) : (
              <>
                <p className="muted small">
                  SHA-256: <code>{a.sha256}</code>
                </p>
                {a.segments.map((s) => (
                  <div key={s.location}>
                    <b>{s.location}</b>
                    <pre
                      style={{
                        whiteSpace: "pre-wrap",
                        overflowWrap: "anywhere",
                      }}
                    >
                      {s.text}
                    </pre>
                  </div>
                ))}
              </>
            )}
          </section>
        ))}
      </details>
      {r.status === "collected" && (
        <label>
          Import proposal JSON
          <input
            type="file"
            accept="application/json,.json"
            disabled={busy}
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file)
                void run(async () => {
                  if (file.size > 1048576)
                    throw new Error("Proposal exceeds 1 MiB");
                  await api.importAssessment(
                    r.id,
                    JSON.parse(await file.text()),
                  );
                  await refresh();
                });
              e.target.value = "";
            }}
          />
        </label>
      )}
      {r.document.proposal && (
        <p>
          Proposal source: {r.document.proposal.source}; model label:{" "}
          {r.document.proposal.model}. Imported provenance is supplied by the
          instructor.
        </p>
      )}
      {r.document.proposal && (
        <details>
          <summary>Original proposed feedback</summary>
          {r.document.proposal.criteria.map((c) => (
            <p key={c.criterion_id}>
              {c.criterion_id}: {c.points ?? "Unassessable"} — {c.feedback}
            </p>
          ))}
        </details>
      )}
      {r.status === "pending" && (
        <fieldset disabled={busy}>
          <legend>Review proposed scores and feedback</legend>
          <p>
            Check each citation against the captured evidence. Valid citations
            and score ranges do not establish that the judgment is correct.
          </p>
          {criteria.map((c, i) => {
            const criterion = r.document.rubric.criteria.find(
              (v) => v.id === c.criterion_id,
            );
            return (
              <fieldset key={c.criterion_id}>
                <legend>{criterion?.description}</legend>
                <p>
                  Original proposed score:{" "}
                  {r.document.proposal?.criteria[i]?.points ?? "Unassessable"} /{" "}
                  {criterion?.max_points}
                </p>
                <label>
                  Reviewed points
                  <input
                    className="input"
                    type="number"
                    min="0"
                    max={criterion?.max_points}
                    step="any"
                    value={c.points ?? ""}
                    onChange={(e) =>
                      change(i, {
                        points:
                          e.target.value === "" ? null : Number(e.target.value),
                      })
                    }
                  />
                </label>
                <label>
                  Feedback
                  <textarea
                    className="input"
                    rows={3}
                    value={c.feedback}
                    onChange={(e) => change(i, { feedback: e.target.value })}
                  />
                </label>
                <label>
                  Uncertainty or limitations
                  <textarea
                    className="input"
                    value={c.uncertainty}
                    onChange={(e) => change(i, { uncertainty: e.target.value })}
                  />
                </label>
                <ul>
                  {c.citations.map((e, j) => (
                    <li key={j}>
                      {e.path} · {e.location}{" "}
                      <button
                        className="btn btn-sm"
                        onClick={() =>
                          change(i, {
                            citations: c.citations.filter((_, k) => j !== k),
                          })
                        }
                      >
                        Remove citation
                      </button>
                    </li>
                  ))}
                </ul>
                <EvidencePicker
                  record={r}
                  add={(e) => change(i, { citations: [...c.citations, e] })}
                />
              </fieldset>
            );
          })}
        </fieldset>
      )}
      {!terminal && (
        <>
          <label>
            Instructor review note
            <textarea
              className="input"
              value={note}
              onChange={(e) => setNote(e.target.value)}
            />
          </label>
          {r.status === "pending" && (
            <Button
              disabled={busy || !note.trim()}
              onClick={() =>
                void run(async () => {
                  await api.reviewAssessment(r.id, "approve", note, criteria);
                  await refresh();
                })
              }
            >
              Approve and publish grade
            </Button>
          )}{" "}
          <Button
            disabled={busy || !note.trim()}
            onClick={() =>
              void run(async () => {
                await api.reviewAssessment(r.id, "reject", note, []);
                await refresh();
              })
            }
          >
            Reject assessment
          </Button>
        </>
      )}
      {r.document.review && (
        <>
          <p>
            Reviewed by {r.document.review.reviewer}: {r.document.review.note}
          </p>
          {r.document.review.criteria?.map((c) => (
            <p key={c.criterion_id}>
              {c.criterion_id}: {c.points} — {c.feedback}
            </p>
          ))}
        </>
      )}
    </article>
  );
}
function EvidencePicker({
  record,
  add,
}: {
  record: AssessmentRecord;
  add: (e: Judgment["citations"][number]) => void;
}) {
  const [file, setFile] = useState("");
  const [loc, setLoc] = useState("");
  const artifact = record.document.artifacts.find((a) => a.path === file);
  return (
    <div>
      <label>
        Evidence file
        <select
          className="input"
          value={file}
          onChange={(e) => {
            setFile(e.target.value);
            setLoc("");
          }}
        >
          <option value="">Choose file</option>
          {record.document.artifacts.map((a) => (
            <option key={a.path}>{a.path}</option>
          ))}
        </select>
      </label>
      <label>
        Source location
        <select
          className="input"
          value={loc}
          onChange={(e) => setLoc(e.target.value)}
        >
          <option value="">Choose location</option>
          {artifact?.segments.map((s) => (
            <option key={s.location} value={s.location}>
              {s.location}: {s.text.slice(0, 80)}
            </option>
          ))}
        </select>
      </label>
      <Button
        small
        disabled={!artifact || !loc}
        onClick={() => {
          if (artifact)
            add({
              path: artifact.path,
              sha256: artifact.sha256,
              location: loc,
            });
        }}
      >
        Add evidence citation
      </Button>
    </div>
  );
}
