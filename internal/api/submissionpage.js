// SPDX-License-Identifier: AGPL-3.0-or-later
"use strict";
(() => {
  const $ = id => document.getElementById(id);
  const MAX_FILES = 32, MAX_FILE = 1024 * 1024, MAX_TOTAL = 4 * MAX_FILE;
  let work = [], selectedFiles = [], preview = null, key = "", active = "", busy = false;
  let bridge = null, awaitingBridge = false, receiptTimer = null, generation = 0;
  const parameters = new URLSearchParams(location.search);
  const requestedAssignment = parameters.get("submission");

  function status(message, error = false) {
    $("status").textContent = message;
    $("status").className = error ? "error" : "";
  }
  function originURL(raw) {
    const value = new URL(raw);
    if (value.origin !== raw || value.username || value.password ||
        !(value.protocol === "https:" || (value.protocol === "http:" && ["127.0.0.1", "[::1]"].includes(value.hostname)))) {
      throw new Error("The workspace address is not an approved secure origin.");
    }
    return value;
  }
  function allowedOrigin(raw, workspace) {
    try {
      const source = originURL(raw), destination = new URL(workspace);
      if (source.origin === destination.origin) return true;
      // Institutional Hub servers live on one per-user DNS label. This does not
      // trust that source's contents or identity: Cairn still previews bytes and
      // authenticates the receiving learner independently.
      if (source.protocol !== "https:" || destination.protocol !== "https:" || source.port !== destination.port) return false;
      const suffix = "." + destination.hostname;
      if (!source.hostname.endsWith(suffix)) return false;
      return /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(source.hostname.slice(0, -suffix.length));
    } catch { return false; }
  }
  try {
    const raw = new URLSearchParams(location.hash.slice(1)).get("waypoint");
    if (raw) {
      if (raw.length > 2048) throw new Error("Invalid workspace handoff.");
      const value = JSON.parse(raw);
      if (value.version !== 1 || typeof value.nonce !== "string" || !/^[a-f0-9]{64}$/.test(value.nonce) || !window.opener) throw new Error("Invalid workspace handoff.");
      originURL(value.origin);
      bridge = value;
      $("bridge").hidden = false;
      $("source-origin").textContent = "Saved files will come from " + bridge.origin + ". Confirm this is your Waypoint workspace.";
    }
  } catch (error) { status(error.message, true); }
  // Nonce and source metadata stay in memory, never in navigation history.
  history.replaceState(null, "", location.pathname + location.search);

  function assignment() { return work.find(item => item.submission_id === active); }
  function submissionOpen() { return Boolean(assignment() && assignment().submission_open); }
  function pathValid(path) {
    return typeof path === "string" && path.length > 0 && path.length <= 240 &&
      path.split("/").every(part => /^[A-Za-z0-9][A-Za-z0-9_-]*(?:\.[A-Za-z0-9_-]+)*$/.test(part));
  }
  function base64(bytes) {
    let text = "";
    for (let i = 0; i < bytes.length; i += 8192) text += String.fromCharCode(...bytes.subarray(i, i + 8192));
    return btoa(text);
  }
  async function digest(bytes) {
    return Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", bytes)), v => v.toString(16).padStart(2, "0")).join("");
  }
  async function validateFiles(files) {
    if (!Array.isArray(files) || !files.length || files.length > MAX_FILES) throw new Error("Select between 1 and 32 files.");
    let total = 0;
    const paths = new Set(), validated = [];
    for (const file of files) {
      if (!file || !pathValid(file.path) || paths.has(file.path) || typeof file.content_base64 !== "string" ||
          file.content_base64.length > Math.ceil(MAX_FILE / 3) * 4 || !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(file.content_base64)) throw new Error("Invalid, duplicate, hidden or oversized file path/content.");
      const bytes = Uint8Array.from(atob(file.content_base64), c => c.charCodeAt(0));
      total += bytes.length;
      if (bytes.length > MAX_FILE || total > MAX_TOTAL || bytes.length !== file.size || await digest(bytes) !== file.sha256) throw new Error("File size or checksum differs; select the saved files again.");
      paths.add(file.path);
      validated.push({path: file.path, content: file.content_base64});
    }
    return {files: validated, total};
  }
  function resetPreview() {
    preview = null;
    key = crypto.randomUUID();
    $("preview-section").hidden = true;
    $("confirm").disabled = false;
  }
  function setFiles(value) {
    selectedFiles = value.files;
    resetPreview();
    $("file-summary").textContent = selectedFiles.length + " saved files selected (" + value.total + " bytes): " + selectedFiles.map(f => f.path).join(", ");
    $("preview").disabled = false;
  }
  async function request(path, data) {
    const options = {credentials: "same-origin", cache: "no-store"};
    if (data !== undefined) Object.assign(options, {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify(data)});
    const response = await fetch(path, options);
    const result = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(result.error || "Request failed (" + response.status + ").");
    return result;
  }
  function endpoint() { return "/me/work/" + encodeURIComponent(active); }
  function lock(value) {
    busy = value;
    $("selection").disabled = value;
    $("sources").disabled = value || !submissionOpen();
    $("confirm").disabled = value || !submissionOpen();
  }
  function renderManifest(changes, tbody) {
    tbody.replaceChildren();
    for (const change of changes || []) {
      const row = document.createElement("tr");
      for (const value of [change.path, change.action, change.bytes, change.sha256]) {
        const cell = document.createElement("td");
        const text = document.createElement("code");
        text.textContent = String(value);
        cell.append(text); row.append(cell);
      }
      tbody.append(row);
    }
  }
  function renderPreview(receipt) {
    preview = receipt;
    const item = assignment();
    $("preview-context").textContent = item.classroom_name + " · " + item.assignment_title + ". Preview expires " + new Date(receipt.expires_at).toLocaleString() + ".";
    $("preview-base").textContent = "Branch: " + receipt.branch + ". Current commit: " + receipt.base_revision;
    renderManifest(receipt.changes, $("manifest"));
    $("preview-section").hidden = false;
    $("confirm").focus();
  }
  const descriptions = {
    preview: "Preview only — not submitted",
    queued: "Received — waiting to commit",
    preparing: "Received — preparing commit",
    publishing: "Committing — awaiting repository verification",
    committed: "Committed — repository receipt verified",
    conflict: "Repository changed — select files and preview again",
    verification_needed: "Verification needed — contact your instructor before submitting again",
    rejected: "Submission could not be accepted",
    refused: "Assignment access or policy changed — submission refused",
    failed: "Submission could not be completed",
    expired: "Preview expired — select files and preview again"
  };
  async function loadReceipts() {
    clearTimeout(receiptTimer);
    if (!active) return;
    const selected = active;
    const result = await request(endpoint() + "/submission-receipts");
    if (selected !== active) return;
    $("receipts").replaceChildren();
    for (const receipt of result.receipts || []) {
      const article = document.createElement("article");
      const heading = document.createElement("h3");
      heading.textContent = descriptions[receipt.status] || receipt.status;
      const content = document.createElement("p");
      content.textContent = "Receipt " + receipt.id + " · " + new Date(receipt.created_at).toLocaleString();
      article.append(heading, content);
      if (submissionOpen() && receipt.status === "preview" && Date.parse(receipt.expires_at) > Date.now()) {
        const resume = document.createElement("button");
        resume.type = "button";
        resume.textContent = "Review this preview";
        resume.addEventListener("click", () => {
          if (busy) return;
          renderPreview(receipt);
          status("Recovered the saved preview. Review its exact files before confirming.");
        });
        article.append(resume);
      }
      if (receipt.commit_revision) {
        const commit = document.createElement("p");
        commit.textContent = "Commit: " + receipt.commit_revision;
        article.append(commit);
      }
      if (receipt.message) {
        const message = document.createElement("p"); message.textContent = receipt.message; article.append(message);
      }
      const details = document.createElement("details"), summary = document.createElement("summary"), record = document.createElement("pre");
      summary.textContent = "Exact receipt and file checksums";
      record.style.whiteSpace = "pre-wrap"; record.style.overflowWrap = "anywhere";
      record.textContent = JSON.stringify(receipt, null, 2);
      details.append(summary, record); article.append(details);
      $("receipts").append(article);
    }
    if (!(result.receipts || []).length) $("receipts").textContent = "No saved submission receipts for this assignment.";
    if ((result.receipts || []).some(r => ["queued", "preparing", "publishing"].includes(r.status))) {
      receiptTimer = setTimeout(() => loadReceipts().catch(e => status("Receipt refresh failed. " + e.message, true)), 3000);
    }
  }
  async function chooseAssignment() {
    generation++;
    active = $("assignment").value;
    const locationQuery = new URLSearchParams();
    if (active) locationQuery.set("submission", active);
    history.replaceState(null, "", location.pathname + (active ? "?" + locationQuery : ""));
    selectedFiles = [];
    awaitingBridge = false;
    resetPreview();
    $("files").value = "";
    $("file-summary").textContent = "No files selected.";
    $("preview").disabled = true;
    $("sources").disabled = !submissionOpen();
    $("receipts-section").hidden = !active;
    const item = assignment();
    $("assignment-details").textContent = item ? item.classroom_name + " · " + (item.deadline ? "Due " + new Date(item.deadline).toLocaleString() : "No deadline") + (submissionOpen() ? "" : " · New submissions are closed. Existing receipts remain available below.") : "";
    $("receive").disabled = !item || !bridge || !allowedOrigin(bridge.origin, item.workspace_url);
    $("bridge-status").textContent = item && bridge && $("receive").disabled ? "This workspace is not configured for the selected course. Choose the matching assignment, or use files saved on this device." : "";
    $("receipts").replaceChildren();
    await loadReceipts();
  }
  async function load() {
    try {
      const data = await request("/me/work");
      $("signin").hidden = true;
      $("account").textContent = "Signed in to Cairn as " + data.username + ". Check that this is your course account.";
      work = (data.work || []).filter(item => item.submissions_enabled);
      $("assignment").replaceChildren(new Option("Choose an assignment", ""));
      for (const item of work) $("assignment").add(new Option(item.classroom_name + " — " + item.assignment_title, item.submission_id));
      $("selection").disabled = false;
      status(work.length ? "Choose the assignment you intend to submit to." : "No active assignments have saved-file submission enabled. You can still use the repository workflow.");
      if (work.some(item => item.submission_id === requestedAssignment)) $("assignment").value = requestedAssignment;
      await chooseAssignment();
    } catch (error) { $("signin").hidden = false; status("Sign in to view your assignments. " + error.message, true); }
  }
  $("files").addEventListener("change", async () => {
    const current = ++generation;
    selectedFiles = [];
    resetPreview();
    $("preview").disabled = true;
    $("file-summary").textContent = "Checking selected files…";
    try {
      const files = Array.from($("files").files);
      if (!files.length || files.length > MAX_FILES || files.some(f => f.size > MAX_FILE) || files.reduce((n, f) => n + f.size, 0) > MAX_TOTAL) throw new Error("File selection exceeds the submission limits.");
      const values = [];
      for (const file of files) {
        const bytes = new Uint8Array(await file.arrayBuffer());
        values.push({path: file.name, content_base64: base64(bytes), sha256: await digest(bytes), size: bytes.length});
      }
      const value = await validateFiles(values);
      if (current !== generation || busy) return;
      setFiles(value); status("Saved files selected. Preview before confirming.");
    } catch (error) { $("file-summary").textContent = "No valid files selected."; status(error.message, true); }
  });
  $("receive").addEventListener("click", () => {
    if (!bridge || !assignment() || !allowedOrigin(bridge.origin, assignment().workspace_url) || !window.opener || window.opener.closed) { status("Open this page again from your Waypoint workspace.", true); return; }
    awaitingBridge = true;
    $("bridge-status").textContent = "Waiting for your selected saved files…";
    window.opener.postMessage({type: "cairn-ready", version: 1, nonce: bridge.nonce}, bridge.origin);
  });
  window.addEventListener("message", async event => {
    if (!awaitingBridge || !bridge || busy || event.origin !== bridge.origin || event.source !== window.opener || !assignment() || !allowedOrigin(bridge.origin, assignment().workspace_url)) return;
    const data = event.data;
    if (!data || data.type !== "waypoint-files" || data.version !== 1 || data.nonce !== bridge.nonce) return;
    awaitingBridge = false;
    const current = generation;
    try {
      const value = await validateFiles(data.files);
      if (current !== generation || busy) return;
      setFiles(value);
      $("bridge-status").textContent = "Received saved files. Nothing has been submitted yet.";
      event.source.postMessage({type: "cairn-received", version: 1, nonce: bridge.nonce}, bridge.origin);
      status("Saved files received. Preview and confirm them in Cairn.");
    } catch (error) { status(error.message, true); $("bridge-status").textContent = "Files were rejected. Select them again in Waypoint."; }
  });
  $("preview").addEventListener("click", async () => {
    if (busy || !selectedFiles.length) return;
    lock(true);
    try {
      const receipt = await request(endpoint() + "/submission-previews", {idempotency_key: key, files: selectedFiles});
      if (receipt.status === "preview") { renderPreview(receipt); status("Review the assignment and exact files before confirming."); }
      else { status("This upload already has a receipt. Check its status below."); await loadReceipts(); }
    } catch (error) { status(error.message + " Retry with the same files to recover an interrupted preview.", true); }
    finally { lock(false); }
  });
  $("confirm").addEventListener("click", async () => {
    if (busy || !preview) return;
    lock(true);
    try {
      const receipt = await request(endpoint() + "/submission-previews/" + encodeURIComponent(preview.id) + "/confirm", {});
      status(descriptions[receipt.status] || receipt.status);
      $("preview-section").hidden = true;
      preview = null; selectedFiles = []; $("files").value = ""; $("preview").disabled = true;
      $("file-summary").textContent = "Submission confirmed. Check the receipt below before submitting again.";
      await loadReceipts();
    } catch (error) { status(error.message + " Refresh receipts or retry confirmation; the same preview cannot create a second submission.", true); }
    finally { lock(false); }
  });
  $("assignment").addEventListener("change", () => chooseAssignment().catch(e => status(e.message, true)));
  $("reload").addEventListener("click", load);
  $("refresh").addEventListener("click", () => loadReceipts().catch(e => status(e.message, true)));
  load();
})();
