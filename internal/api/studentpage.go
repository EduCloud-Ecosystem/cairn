// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
)

// handleStudentPage serves the lightweight, framework-free student page at /me.
// It renders whether or not the React dashboard is mounted, and is public: the
// page itself carries no data — its inline JS fetches /me/work, which enforces the
// session and own-data scoping. An unauthenticated visitor sees a sign-in link.
func (s *Server) handleStudentPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(studentPageHTML))
}

const studentPageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>My work — Cairn</title>
<style>
  :root { color-scheme: light dark; }
  body { font-family: system-ui, sans-serif; max-width: 820px; margin: 2rem auto; padding: 0 1rem; line-height: 1.5; color: #111; background: #fff; }
  h1 { font-size: 1.5rem; }
  .muted { color: #555; }
  .card { border: 1px solid #ccc; border-radius: 8px; padding: 1rem; margin: 1rem 0; }
  .row { display: flex; flex-wrap: wrap; gap: .5rem 1rem; align-items: baseline; }
  .title { font-weight: 600; font-size: 1.1rem; }
  .score { font-variant-numeric: tabular-nums; font-weight: 600; }
  .pill { display: inline-block; padding: .1em .5em; border-radius: 999px; font-size: .8rem; border: 1px solid currentColor; }
  .running { color: #8a5200; }
  .pass { color: #1a7f37; }
  .fail { color: #b3261e; }
  details { border: 1px solid #ccc; border-radius: 6px; padding: .25rem .5rem; margin-top: .5rem; }
  summary { cursor: pointer; font-weight: 600; }
  table { border-collapse: collapse; width: 100%; margin: .5rem 0; }
  th, td { text-align: left; padding: .25rem .5rem; border-bottom: 1px solid #ddd; font-size: .95rem; }
  ul.tests { list-style: none; padding: 0; }
  ul.tests li { padding: .25rem 0; border-bottom: 1px solid #eee; }
  code { overflow-wrap: anywhere; background: rgba(127,127,127,.15); padding: .05em .3em; border-radius: 3px; }
  @media (prefers-color-scheme: dark) {
    body { color: #eee; background: #161616; } a { color: #6ab0ff; }
    .card, details { border-color: #666; } .muted { color: #aaa; }
    .running { color: #ffc46b; } .pass { color: #74d98d; } .fail { color: #ff9990; }
  }
  :focus-visible { outline: 3px solid #347ec4; outline-offset: 3px; }
</style>
<link rel="stylesheet" href="/studio/theme.css"><script src="/studio/theme.js"></script>
<style>
body{background:var(--studio-paper);color:var(--studio-ink);border-top:3px solid #ab0520;padding-top:16px}a{color:var(--studio-link)}.muted{color:var(--studio-muted)}.error,.fail{color:var(--studio-mark)}.pass{color:var(--studio-good)}.running{color:var(--studio-warn)}fieldset,section.card,details{border-color:var(--studio-line)}input,select,button{background:var(--studio-paper);color:var(--studio-ink);border:1px solid var(--studio-control);border-radius:4px;padding:8px}button{min-height:36px}button#confirm,button#preview{background:#ab0520;color:#fff;border-color:var(--studio-mark)}button:disabled{background:var(--studio-soft);color:var(--studio-muted);border-color:var(--studio-line)}.scroll{max-width:100%}th,td{border-color:var(--studio-line)}:focus-visible{outline:3px solid var(--studio-focus);outline-offset:3px}.studio-appearance{justify-content:flex-end}main{min-width:0}body{overflow-wrap:anywhere}@media(pointer:coarse){button,select,summary{min-height:44px}}@media(forced-colors:active){button#confirm,button#preview{background:ButtonFace;color:ButtonText}}
</style></head>
<body>
<main><label class="studio-appearance">Theme <select data-studio-theme-picker aria-label="Appearance"><option value="light">Light</option><option value="dark">Dark</option><option value="system">System</option></select></label>
<h1>My work</h1>
<p id="status" class="muted" aria-live="polite">Loading…</p>
<div id="list"></div>
</main>

<script>
"use strict";
const $ = (sel, el) => (el || document).querySelector(sel);

function fmtDeadline(iso) {
  if (!iso) return "no deadline";
  try { return "due " + new Date(iso).toLocaleString(); } catch (e) { return "due " + iso; }
}
function fmtScore(g) {
  if (!g) return "not graded yet";
  return g.score + " / " + g.max_score;
}
function gradeLabel(g) { return g?.source === "instructor-reviewed" ? "Instructor-reviewed grade" : "Automated checks"; }
function gradingBadge(item) {
  if (item.grading_status === "running") return '<span class="pill running">grading…</span>';
  if (item.grading_status === "failed") return '<span class="pill fail">grading failed</span>';
  return "";
}

function renderTests(tests) {
  if (!tests || !tests.length) return '<p class="muted">No per-test results.</p>';
  let html = '<ul class="tests">';
  for (const t of tests) {
    const cls = t.passed ? "pass" : "fail";
    const mark = t.passed ? "✓" : "✗";
    html += '<li><span class="' + cls + '">' + mark + " " + escapeHtml(t.name) + "</span> " +
            '<span class="muted">(' + t.points + "/" + t.max_points + ")</span>";
    if (t.detail) html += '<br><code>' + escapeHtml(t.detail) + "</code>";
    html += "</li>";
  }
  return html + "</ul>";
}
function renderAssessments(items) {
  return (items || []).map(a => '<section><h3>Instructor-reviewed feedback</h3><p>Submission commit: <code>' + escapeHtml(a.submission_revision) + '</code></p>' + a.criteria.map(c => '<h4>' + escapeHtml(c.criterion_id) + ' — ' + escapeHtml(String(c.points)) + ' points</h4><p>' + escapeHtml(c.feedback) + '</p><p>Uncertainty: ' + escapeHtml(c.uncertainty) + '</p><ul>' + (c.citations || []).map(e => '<li>' + escapeHtml(e.path) + ' · ' + escapeHtml(e.location) + '</li>').join('') + '</ul>').join('') + '<p>Instructor note: ' + escapeHtml(a.review_note) + '</p><p>For a correction or review, contact your instructor and include the submission commit above.</p></section>').join('');
}
function renderHistory(history) {
  if (!history || !history.length) return "";
  let html = "<table><caption class='muted'>Attempt history</caption><thead><tr><th>When</th><th>Score</th><th>Assessment type</th><th>Submission commit</th></tr></thead><tbody>";
  for (const h of history) {
    let when = h.graded_at; try { when = new Date(h.graded_at).toLocaleString(); } catch (e) {}
    html += "<tr><td>" + escapeHtml(when) + "</td><td class='score'>" + h.score + " / " + h.max_score + "</td><td>" + escapeHtml(h.source === "instructor-reviewed" ? "Instructor-reviewed" : "Deterministic tests") + "</td><td><code>" + escapeHtml(h.submission_revision || "Not recorded") + "</code></td></tr>";
  }
  return html + "</tbody></table>";
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, c => ({ "&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;","'":"&#39;" }[c]));
}

async function loadDetail(id, container) {
  const restoreFocus = container.contains(document.activeElement);
  container.textContent = "Loading details…";
  try {
    const res = await fetch("/me/work/" + encodeURIComponent(id), { credentials: "same-origin" });
    if (!res.ok) throw Error("Could not load details.");
    const d = await res.json();
    container.innerHTML = '<h3 tabindex="-1">Results and history</h3>' + renderAssessments(d.assessments) + renderTests(d.tests) + '<div class="scroll">' + renderHistory(d.history) + '</div>';
    if (restoreFocus) container.querySelector("h3").focus();
    return true;
  } catch (e) {
    container.textContent = "Could not load details. Your submitted work is unchanged. ";
    const retry = document.createElement("button"); retry.type = "button"; retry.textContent = "Retry details";
    retry.onclick = () => loadDetail(id, container);
    container.append(retry);
    if (restoreFocus) retry.focus();
    return false;
  }
}

function card(item) {
  const el = document.createElement("section");
  el.className = "card";
  const repo = item.repo_web_url
    ? '<a href="' + encodeURI(item.repo_web_url) + '" target="_blank" rel="noopener">repo ↗</a>'
    : '<span class="muted">repo not ready</span>';
  el.innerHTML =
    '<div class="row"><span class="title">' + escapeHtml(item.assignment_title || item.assignment_slug || "Assignment") + "</span> " +
    '<span class="work-badge">' + gradingBadge(item) + "</span></div>" +
    '<div class="row muted">' + escapeHtml(item.classroom_name || "") + " · " + repo + " · " +
    escapeHtml(fmtDeadline(item.deadline)) + " · status: " + escapeHtml(item.status || "") + "</div>" +
    '<div class="row"><span class="score-label">' + gradeLabel(item.latest_grade) + ': </span><span class="score">' + escapeHtml(fmtScore(item.latest_grade)) + '</span></div><p class="review-state muted"></p>';
  el.updateItem = next => {
    el.querySelector('.score-label').textContent = gradeLabel(next.latest_grade) + ': ';
    el.querySelector('.score').textContent = fmtScore(next.latest_grade);
    el.querySelector('.work-badge').innerHTML = gradingBadge(next);
    el.querySelector('.review-state').textContent = next.latest_grade?.source === 'instructor-reviewed' ? 'Instructor review has been published.' : 'Automated results are not an instructor-reviewed grade. Open history for earlier reviews.';
  };
  el.updateItem(item);

  if (item.workspace_url) {
    const row = document.createElement("p");
    const link = document.createElement("a");
    link.href = item.workspace_url;
    link.textContent = "Open Python/R workspace";
    link.target = "_blank";
    link.rel = "noopener noreferrer";
    row.appendChild(link);
    row.appendChild(document.createTextNode(" · Sign in there to resume your saved work."));
    el.appendChild(row);
  }

  if (item.submissions_enabled) {
    const row = document.createElement("p");
    const link = document.createElement("a");
    link.href = "/me/submit?submission=" + encodeURIComponent(item.submission_id);
    link.textContent = "Submit saved files & view receipts";
    row.appendChild(link);
    el.appendChild(row);
  }

  const det = document.createElement("details");
  const sum = document.createElement("summary");
  sum.textContent = "Per-test results & history";
  const body = document.createElement("div");
  det.appendChild(sum); det.appendChild(body);
  let loaded = false;
  let loading = false;
  det.addEventListener("toggle", async () => { if (det.open && !loaded && !loading) { loading = true; loaded = await loadDetail(item.submission_id, body); loading = false; } });
  el.appendChild(det);
  return el;
}

let pollTimer = null;
const workRows = new Map();
async function load() {
  let res;
  try { res = await fetch("/me/work", { credentials: "same-origin" }); }
  catch (e) { $("#status").textContent = "Network error."; return; }

  if (res.status === 401) {
    $("#status").innerHTML = 'Please <a href="/student/login">sign in</a> to see your work.';
    $("#list").innerHTML = ""; workRows.clear();
    return;
  }
  if (!res.ok) { $("#status").textContent = "Could not load your work."; return; }

  const data = await res.json();
  const work = (data && data.work) || [];
  const list = $("#list");
  for (const [id, row] of workRows) if (!work.some(item => item.submission_id === id)) { row.remove(); workRows.delete(id); }
  if (!work.length) {
    $("#status").textContent = "No submissions yet. Accept an assignment to get started.";
    return;
  }
  $("#status").textContent = work.length + (work.length === 1 ? " submission" : " submissions");
  for (const item of work) {
    let row = workRows.get(item.submission_id);
    if (!row) { row = card(item); workRows.set(item.submission_id, row); list.appendChild(row); }
    else row.updateItem(item);
  }

  // If anything is grading, poll until it settles, then stop.
  const anyRunning = work.some(i => i.grading_status === "running");
  if (pollTimer) { clearTimeout(pollTimer); pollTimer = null; }
  if (anyRunning) pollTimer = setTimeout(load, 4000);
}

load();
</script>
</body>
</html>`
