import { AssessmentPanel } from "./AssessmentPanel";
import { useState } from "react";
import { api, type Assignment, type SubmissionView } from "../api";
import { Button, StatusChip, Empty, type Notify } from "./ui";

function toLocalInput(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function fmtDeadline(iso?: string): string {
  if (!iso) return "none";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

export function AssignmentCard({
  assignment,
  notify,
  joinPolicy,
}: {
  assignment: Assignment;
  notify: Notify;
  joinPolicy?: string;
}) {
  const [revision, setRevision] = useState(assignment.template.ref ?? "");
  const [policyPath, setPolicyPath] = useState(assignment.grading_spec || "grading.json");
  const [savedRevision, setSavedRevision] = useState(assignment.template.ref ?? "");
  const policyPinned = /^(?:[a-fA-F0-9]{40}|[a-fA-F0-9]{64})$/.test(savedRevision);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [deadline, setDeadline] = useState(toLocalInput(assignment.deadline));
  const [savedDeadline, setSavedDeadline] = useState(assignment.deadline);
  const [subs, setSubs] = useState<SubmissionView[] | null>(null);

  async function loadSubs() {
    try {
      setSubs(await api.listSubmissions(assignment.id));
    } catch (e) {
      notify(errMsg(e), "err");
    }
  }

  function toggle() {
    const next = !open;
    setOpen(next);
    if (next && subs === null) void loadSubs();
  }

  async function run(label: string, fn: () => Promise<{ jobs_enqueued: number }>) {
    setBusy(true);
    try {
      const res = await fn();
      notify(`${label}: ${res.jobs_enqueued} job(s) queued`);
      if (open) void loadSubs();
    } catch (e) {
      notify(errMsg(e), "err");
    } finally {
      setBusy(false);
    }
  }

  // Copy the student join URL. navigator.clipboard is unavailable outside a
  // secure context (plain http on a non-localhost host), so surface the URL in
  // the toast on failure rather than leaving the instructor with nothing.
  async function copyInvite() {
    const url = api.inviteUrl(assignment.id);
    try {
      await navigator.clipboard.writeText(url);
      notify("Invite link copied");
    } catch {
      notify(`Copy failed — the link is ${url}`, "err");
    }
  }

  async function savePolicy() {
    setBusy(true);
    try {
      const updated = await api.setGradingPolicy(assignment.id, revision.trim(), policyPath.trim());
      setSavedRevision(updated.template.ref ?? "");
      setRevision(updated.template.ref ?? "");
      setPolicyPath(updated.grading_spec);
      notify("Instructor grading version saved");
    } catch (e) {
      notify(errMsg(e), "err");
    } finally {
      setBusy(false);
    }
  }

  async function saveDeadline(clear: boolean) {
    setBusy(true);
    try {
      const value = clear ? null : deadline ? new Date(deadline).toISOString() : null;
      const updated = await api.setDeadline(assignment.id, value);
      setSavedDeadline(updated.deadline);
      setDeadline(toLocalInput(updated.deadline));
      notify(clear ? "Deadline cleared" : "Deadline saved");
    } catch (e) {
      notify(errMsg(e), "err");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="card assignment">
      <div className="assignment-top">
        <button className="btn btn-sm" onClick={toggle} aria-expanded={open} title="Show submissions">
          {open ? "▾" : "▸"}
        </button>

        <div className="assignment-main">
          <h3 className="assignment-title">{assignment.title || assignment.slug}</h3>
          <div className="assignment-sub">
            <span>
              <b>slug</b> {assignment.slug}
            </span>
            <span>
              <b>template</b> {assignment.template.namespace}/{assignment.template.name}
            </span>
            <span>
              <b>type</b> {assignment.type}
            </span>
            <span>
              <b>due</b> {fmtDeadline(savedDeadline)}
            </span>
          </div>
        </div>

        <div className="assignment-actions">
          <Button
            small
            onClick={() => void copyInvite()}
            title={
              joinPolicy === "roster"
                ? "Students must be on the roster first"
                : "Open to anyone with the link"
            }
          >
            Copy invite link
          </Button>
          <Button small disabled={busy || !policyPinned} title={policyPinned ? "Grade using the saved instructor version" : "Expand this assignment and save an instructor grading version first"} onClick={() => void run("Grade", () => api.grade(assignment.id))}>
            Grade
          </Button>
          <Button small disabled={busy} onClick={() => void run("Lock", () => api.lock(assignment.id))}>
            Lock
          </Button>
          <Button small disabled={busy} onClick={() => void run("Unlock", () => api.unlock(assignment.id))}>
            Unlock
          </Button>
        </div>
      </div>

      {!policyPinned && <p className="muted small">Expand this assignment to select an instructor grading version before grading.</p>}
      {open && (
        <div className="assignment-drawer">
          <p className="muted small">
            Invite link: <code>{api.inviteUrl(assignment.id)}</code>{" "}
            {joinPolicy === "roster"
              ? "— students must be on the roster first."
              : joinPolicy === "open"
                ? "— open to anyone with the link."
                : null}
          </p>

          <fieldset disabled={busy}>
            <legend>Instructor grading version</legend>
            <p id={`policy-help-${assignment.id}`} className="muted small">
              Paste the full commit ID from the instructor template. Grading uses that version's rules and tests.
              Learners' edits to grading.json do not change their scores. This also sets the template version for future submissions.
            </p>
            <label htmlFor={`policy-ref-${assignment.id}`}>Template commit</label>
            <input id={`policy-ref-${assignment.id}`} className="input" value={revision}
              aria-describedby={`policy-help-${assignment.id}`} onChange={(e) => setRevision(e.target.value)} />
            <label htmlFor={`policy-path-${assignment.id}`}>Grading file</label>
            <input id={`policy-path-${assignment.id}`} className="input" value={policyPath}
              onChange={(e) => setPolicyPath(e.target.value)} />
            <Button small disabled={busy} onClick={() => void savePolicy()}>Save grading version</Button>
            {policyPinned && <p className="muted small">Saved commit: <code>{savedRevision}</code></p>}
          </fieldset>

          <AssessmentPanel classroomID={assignment.classroom_id} assignmentID={assignment.id} submissions={subs || []} notify={notify} refreshSubmissions={loadSubs} />

          <div className="deadline-row">
            <label htmlFor={`dl-${assignment.id}`}>Deadline</label>
            <input
              id={`dl-${assignment.id}`}
              className="input"
              type="datetime-local"
              value={deadline}
              onChange={(e) => setDeadline(e.target.value)}
            />
            <Button small variant="primary" disabled={busy} onClick={() => void saveDeadline(false)}>
              Save
            </Button>
            <Button small variant="ghost" disabled={busy || !savedDeadline} onClick={() => void saveDeadline(true)}>
              Clear
            </Button>
          </div>

          {subs === null ? (
            <p className="muted small">Loading submissions…</p>
          ) : subs.length === 0 ? (
            <Empty>No submissions yet — students appear here after they accept.</Empty>
          ) : (
            <table className="table">
              <thead>
                <tr>
                  <th>Student</th>
                  <th>Repository</th>
                  <th>Status</th>
                  <th className="num">Score</th>
                </tr>
              </thead>
              <tbody>
                {subs.map((s) => (
                  <tr key={s.id}>
                    <td className="mono">{s.username || "—"}</td>
                    <td className="mono">{s.repo.name ? `${s.repo.namespace}/${s.repo.name}` : "—"}</td>
                    <td>
                      <StatusChip status={s.status} />
                    </td>
                    <td className="num">
                      {s.score === undefined ? "—" : `${s.score} / ${s.max_score ?? "?"}`}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </div>
  );
}

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
