import { useEffect, useState } from "react";
import { api, type LTIPassbackStatus, type SubmissionView } from "../api";
import { Button, type Notify } from "./ui";

export function PassbackPanel({ submissions, notify }: { submissions: SubmissionView[]; notify: Notify }) {
  const [enabled, setEnabled] = useState(false);
  const [submission, setSubmission] = useState("");
  const [status, setStatus] = useState<LTIPassbackStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  useEffect(() => {
    let live = true;
    void api.ltiCapabilities().then((c) => { if (live) setEnabled(c.enabled); }).catch(() => {});
    return () => { live = false; };
  }, []);
  useEffect(() => {
    let live = true;
    setStatus(null); setMessage("");
    if (submission) void api.ltiStatus(submission).then((value) => {
      if (live) setStatus(value);
    }).catch(() => { if (live) setMessage("Connect this assignment and learner from Brightspace, then publish a reviewed grade in Cairn."); });
    return () => { live = false; };
  }, [submission]);
  if (!enabled) return null;
  async function refresh() {
    setBusy(true);
    try { setStatus(await api.ltiStatus(submission)); setMessage(""); }
    catch (error) { notify(String(error), "err"); }
    finally { setBusy(false); }
  }
  async function send() {
    const grade = status?.grade;
    if (!grade) return;
    setBusy(true);
    try {
      const delivered = await api.ltiDeliver(submission, grade.id);
      setStatus(await api.ltiStatus(submission));
      notify(delivered.grade_id !== grade.id ? "Previous delivery reconciled; send the current approved grade explicitly." : "Brightspace delivery checked. Review its status below.");
    } catch (error) {
      notify(String(error), "err");
      try { setStatus(await api.ltiStatus(submission)); } catch { setStatus(null); }
    } finally { setBusy(false); }
  }
  const delivery = status?.delivery;
  const sameGrade = delivery?.grade_id === status?.grade?.id;
  const retry = delivery?.status === "uncertain" || (delivery?.status === "sending" && Date.parse(delivery.lease_until) <= Date.now());
  const canSend = status?.ready && (!delivery || (delivery.status === "verified" && !sameGrade) || retry);
  return <section className="assessment-panel" aria-label="Brightspace grade delivery">
    <h3>Brightspace grade delivery</h3>
    <p>Launch this resource in Brightspace to connect the assignment and each learner. Only a published, instructor-reviewed numeric grade can be sent. Feedback stays in Cairn.</p>
    <label>Submission <select disabled={busy} value={submission} onChange={(event) => setSubmission(event.target.value)}>
      <option value="">Choose a learner</option>
      {submissions.map((s) => <option key={s.id} value={s.id}>{s.username || s.id}</option>)}
    </select></label>
    {message && <p role="status">{message}</p>}
    {status && <div aria-live="polite">
      <p>{status.grade ? `Published grade: ${status.grade.score} / ${status.grade.maximum}.` : "No eligible reviewed grade."} {status.ready ? "Ready for delivery." : "Connection or published review required."}</p>
      <p>Delivery{delivery && !sameGrade ? " for a previous grade" : ""}: {delivery?.status ?? "not sent"}{delivery ? ` (${delivery.attempts} attempt${delivery.attempts === 1 ? "" : "s"})` : ""}.</p>
      {delivery?.verified_at && <p>Readback confirmed: {new Date(delivery.verified_at).toLocaleString()}.</p>}
      {delivery?.status === "conflict" && <p>Brightspace has a different score. Resolve this with the instructor; Cairn will not overwrite it automatically.</p>}
      {delivery?.status === "uncertain" && <p>The request outcome is uncertain. Retry checks Brightspace before sending again.</p>}
      <Button disabled={busy || !canSend} onClick={() => void send()}>{retry ? (delivery && delivery.attempts >= 3 ? "Check delivery" : "Check and retry delivery") : "Send to Brightspace"}</Button>
    </div>}
    {submission && <Button disabled={busy} onClick={() => void refresh()}>Refresh delivery status</Button>}
  </section>;
}
