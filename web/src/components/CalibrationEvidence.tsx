import { useEffect, useRef, useState } from "react";
import {
  api,
  type Calibration,
  type CalibrationExample,
  type Judgment,
} from "../api";
import { Button } from "./ui";

export function CalibrationEvidence({
  record,
  example,
  busy,
  run,
  onUse,
}: {
  record: Calibration;
  example: CalibrationExample;
  busy: boolean;
  run: (fn: () => Promise<Calibration | null>) => Promise<void>;
  onUse: (judgments: Judgment[]) => void;
}) {
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const readVersion = useRef(0);
  useEffect(
    () => () => {
      readVersion.current++;
    },
    [],
  );
  const frozen =
    record.status === "ready" || !!example.review || !!example.exclusion_note;
  const records = example.review_evidence || [];
  function importPacket() {
    try {
      if (new TextEncoder().encode(text).length > 256 * 1024)
        throw new Error("Review packet exceeds 256 KiB.");
      const packet: unknown = JSON.parse(text);
      setError("");
      void run(() =>
        api.importCalibrationEvidence(
          record.id,
          example.id,
          record.revision,
          packet,
        ),
      );
    } catch (e) {
      setError(String(e));
    }
  }
  return (
    <section aria-label="Supporting review evidence">
      <h5>Supporting review evidence</h5>
      <p>
        Attach local check results or attributed adjudications. Cairn matches
        their source and rubric; it does not independently verify who authored
        an imported judgment or ran an imported check. Attachments are not sent
        to the model.
      </p>
      {records.map((v) => (
        <article
          className="card"
          style={{ padding: "12px" }}
          key={v.packet_digest}
        >
          <h6 style={{ fontSize: "1rem", margin: "0 0 12px" }}>
            {v.authorship} · claimed author: {v.author}
          </h6>
          <p>
            Imported by {v.imported_by} at {v.imported_at}. Source and rubric
            matched.
          </p>
          <p>{v.note}</p>
          {v.execution ? (
            <>
              <p>
                Imported execution report · reported at{" "}
                {v.execution.reported_at}. Execution not independently verified.
              </p>
              <table>
                <thead>
                  <tr>
                    <th>Criterion</th>
                    <th>Reported outcome</th>
                    <th>Detail</th>
                  </tr>
                </thead>
                <tbody>
                  {v.execution.sample.tests.map((t) => (
                    <tr key={t.criterion_id}>
                      <td>{t.criterion_id}</td>
                      <td>{t.status.replace(/_/g, " ")}</td>
                      <td>{t.detail || "—"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <p>
                Unchecked criteria:{" "}
                {(v.execution.unchecked_criteria || []).join(", ") ||
                  "None in this report"}
                . Passing a bounded check does not establish full correctness;
                failures do not assign points.
              </p>
              <details>
                <summary>Execution provenance</summary>
                <p style={{ overflowWrap: "anywhere" }}>
                  Report: {v.execution.report_digest}
                  <br />
                  Checks: {v.execution.checks_sha256}
                  <br />
                  Image: {v.execution.image}
                </p>
              </details>
            </>
          ) : null}
          {v.judgments?.map((j) => (
            <section key={j.criterion_id}>
              <p>
                <strong>
                  {j.criterion_id}: {j.points ?? "Unassessable (null)"}
                </strong>{" "}
                · attributed judgment, not a published grade
              </p>
              <p>{j.feedback}</p>
              <p>{j.uncertainty}</p>
              {(j.citations || []).map((c, i) => (
                <details key={i}>
                  <summary>
                    {c.path} · {c.location} · matched source quote
                  </summary>
                  <blockquote
                    style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
                  >
                    {c.quote}
                  </blockquote>
                </details>
              ))}
            </section>
          ))}
          {v.judgments?.length ? (
            <Button
              disabled={busy || frozen || !example.document.proposal}
              onClick={() => onUse(v.judgments!)}
            >
              Use adjudication in instructor review form
            </Button>
          ) : null}
          {v.judgments?.length && !example.document.proposal ? (
            <p>
              You can use these judgments in your review form after generating
              feedback. They cannot be saved as an independent reference.
            </p>
          ) : null}
          <details>
            <summary>Source binding and attachment identity</summary>
            <pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
              {JSON.stringify(
                {
                  packet: v.packet_digest,
                  rubric: v.rubric_digest,
                  sources: v.source_sha256,
                },
                null,
                2,
              )}
            </pre>
          </details>
        </article>
      ))}
      {!frozen && records.length < 4 ? (
        <details>
          <summary>Attach a review packet</summary>
          <p>
            Use a review-packet.json from cairn calibration-review-packet.
            Adjudications remain attributed supporting material. Attaching them
            before your own reference closes the independent-reference path for
            this example; section feedback can proceed as assisted calibration.
          </p>
          <label>
            Review packet JSON file
            <input
              type="file"
              accept=".json,application/json"
              disabled={busy}
              onChange={async (ev) => {
                const version = ++readVersion.current;
                const file = ev.target.files?.[0];
                setText("");
                if (!file) return;
                if (file.size > 256 * 1024) {
                  setError("Review packet exceeds 256 KiB.");
                  setText("");
                  return;
                }
                try {
                  const next = await file.text();
                  if (version === readVersion.current) {
                    setText(next);
                    setError("");
                  }
                } catch (e) {
                  if (version === readVersion.current) setError(String(e));
                }
              }}
            />
          </label>
          <label>
            Review packet JSON
            <textarea
              className="input"
              rows={6}
              value={text}
              disabled={busy}
              onChange={(ev) => {
                readVersion.current++;
                setText(ev.target.value);
              }}
            />
          </label>
          {error ? <p role="alert">{error}</p> : null}
          <Button disabled={busy || !text.trim()} onClick={importPacket}>
            Attach attributed supporting material
          </Button>
        </details>
      ) : null}
    </section>
  );
}
