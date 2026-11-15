// SPDX-License-Identifier: AGPL-3.0-or-later
"use strict";
(() => {
  const $ = id => document.getElementById(id);
  const MAX_FILES = 32, MAX_FILE = 1024 * 1024, MAX_TOTAL = 4 * MAX_FILE;
  let work = [], selectedFiles = [], preview = null, key = "", active = "", busy = false;
  let bridge = null, awaitingBridge = false, bridgeTimer = null, receiptTimer = null, generation = 0;
  let receiptRequest = 0, confirmedReceipt = "", loadingAssignments = false;
  const receiptRows = new Map();
  const parameters = new URLSearchParams(location.search);
  const requestedAssignment = parameters.get("submission");

  function status(message, error = false) {
    $("status").textContent = message;
    $("status").className = error ? "error" : "";
  }
  function signIn() {
    $("signin").hidden = false;
    status("Your Cairn session ended. Sign in again, then check sign-in to continue in this tab.", true);
  }
  function receiptRefreshFailed(error) {
    if (error.status === 401) { signIn(); return; }
    $("receipt-status").textContent = "Could not check submission status. Your receipt list may be out of date. Refresh receipts to try again.";
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
      $("notebook-source").hidden = true;
      $("source-origin").textContent = "Workspace address: " + bridge.origin;
    }
  } catch { /* The page shows how to reopen it from your notebook. */ }
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
    return validated;
  }
  function resetPreview() {
    preview = null;
    key = crypto.randomUUID();
    $("preview-section").hidden = true;
    $("confirm").disabled = false;
  }
  function setFiles(files) {
    selectedFiles = files;
    resetPreview();
    $("file-summary").textContent = selectedFiles.length + (selectedFiles.length === 1 ? " saved file ready to review: " : " saved files ready to review: ") + selectedFiles.map(f => f.path).join(", ");
    $("preview").disabled = false;
  }
  async function request(path, data) {
    const options = {credentials: "same-origin", cache: "no-store"};
    if (data !== undefined) Object.assign(options, {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify(data)});
    const response = await fetch(path, options);
    const result = await response.json().catch(() => ({}));
    if (!response.ok) { const error = new Error(result.error || "Request failed (" + response.status + ")."); error.status = response.status; throw error; }
    return result;
  }
  function endpoint() { return "/me/work/" + encodeURIComponent(active); }
  function lock(value) {
    busy = value;
    $("selection").disabled = value;
    $("sources").disabled = value || !submissionOpen();
    $("confirm").disabled = value || !submissionOpen();
  }
  function renderManifest(changes) {
    const tbody = $("manifest");
    const hashes = $("manifest-hashes");
    tbody.replaceChildren();
    hashes.replaceChildren();
    $("preview-checksums").open = false;
    for (const change of changes || []) {
      const row = document.createElement("tr");
      for (const value of [change.path, change.action === "replace" ? "Replace" : "Add", change.bytes]) {
        const cell = document.createElement("td");
        const text = document.createElement("code");
        text.textContent = String(value);
        cell.append(text); row.append(cell);
      }
      tbody.append(row);
      const path = document.createElement("dt");
      const hash = document.createElement("dd");
      const value = document.createElement("code");
      path.textContent = change.path;
      value.textContent = change.sha256;
      hash.append(value);
      hashes.append(path, hash);
    }
  }
  function renderPreview(receipt) {
    preview = receipt;
    const item = assignment();
    $("preview-context").textContent = item.classroom_name + " · " + item.assignment_title + ". Confirm by " + new Date(receipt.expires_at).toLocaleString() + ".";
    $("preview-base").textContent = "Branch: " + receipt.branch + ". Current commit: " + receipt.base_revision;
    renderManifest(receipt.changes);
    $("preview-section").hidden = false;
    $("preview-heading").focus();
  }
  const receiptStates = {
    preview: ["Preview saved", "Review and confirm to submit these files."],
    queued: ["Saving your submission", "Confirmation received. Keep this receipt to check when saving finishes."],
    preparing: ["Saving your submission", "Confirmation received. Keep this receipt to check when saving finishes."],
    publishing: ["Saving your submission", "Confirmation received. Keep this receipt to check when saving finishes."],
    committed: ["Submission received", "Your files were saved. You can check results in My work."],
    conflict: ["Files changed in the assignment repository", "Select your saved files again and review the replacements before confirming."],
    verification_needed: ["Submission needs verification", "Contact your instructor before trying another submission."],
    rejected: ["Submission was rejected", "Keep your files and contact your instructor for help."],
    refused: ["Submission was refused", "Assignment access or submission rules changed. Check the assignment or contact your instructor."],
    failed: ["Submission could not be saved", "Keep your files and contact your instructor before submitting again."],
    expired: ["Preview expired", "Select your saved files again to create a new preview."]
  };
  function receiptState(code) {
    return receiptStates[code] || ["Submission status unavailable", "Refresh receipts or contact your instructor before submitting again."];
  }
  function announceReceipt(description) {
    const message = description + " for " + assignment().assignment_title + ".";
    if ($("receipt-status").textContent !== message) $("receipt-status").textContent = message;
  }
  function createReceiptRow(receipt) {
    const article = document.createElement("article");
    const heading = document.createElement("h3");
    heading.tabIndex = -1;
    const context = document.createElement("p"), date = document.createElement("time");
    date.dateTime = receipt.created_at;
    date.textContent = new Date(receipt.created_at).toLocaleString();
    context.append(assignment().assignment_title + " · ", date);
    const files = document.createElement("ul");
    for (const file of receipt.changes || []) {
      const name = document.createElement("li");
      name.textContent = file.path;
      files.append(name);
    }
    const resume = document.createElement("button");
    resume.type = "button";
    resume.textContent = "Review this preview";
    const message = document.createElement("p");
    const details = document.createElement("details"), summary = document.createElement("summary"), record = document.createElement("pre");
    summary.textContent = "Technical details";
    record.style.whiteSpace = "pre-wrap"; record.style.overflowWrap = "anywhere";
    details.append(summary, record);
    article.append(heading, context, message, files, resume, details);
    return {article, heading, resume, message, record};
  }
  async function loadReceipts() {
    clearTimeout(receiptTimer);
    const current = ++receiptRequest;
    if (!active) return;
    let result;
    try { result = await request(endpoint() + "/submission-receipts"); }
    catch (error) {
      if (current === receiptRequest) throw error;
      return;
    }
    if (current !== receiptRequest) return;
    const receipts = result.receipts || [];
    const visible = new Set(receipts.map(receipt => receipt.id));
    for (const [id, row] of receiptRows) {
      if (visible.has(id)) continue;
      if (row.article.contains(document.activeElement)) $("receipts-heading").focus();
      row.article.remove();
      receiptRows.delete(id);
    }
    for (const [index, receipt] of receipts.entries()) {
      let row = receiptRows.get(receipt.id);
      if (!row) {
        row = createReceiptRow(receipt);
        receiptRows.set(receipt.id, row);
        $("receipts").insertBefore(row.article, $("receipts").children[index] || null);
      }
      const expiredPreview = receipt.status === "preview" && Date.parse(receipt.expires_at) <= Date.now();
      const [description, nextStep] = receiptState(expiredPreview ? "expired" : receipt.status);
      if (row.heading.textContent !== description) {
        if (row.heading.textContent || receipt.id === confirmedReceipt) announceReceipt(description);
        row.heading.textContent = description;
      }
      row.message.textContent = nextStep;
      const resumable = submissionOpen() && receipt.status === "preview" && Date.parse(receipt.expires_at) > Date.now();
      if (!resumable && document.activeElement === row.resume) row.heading.focus();
      row.resume.hidden = !resumable;
      row.resume.onclick = () => {
        if (busy) return;
        renderPreview(receipt);
        status("Saved preview reopened. Review the files before confirming.");
      };
      const technical = [
        "Receipt reference: " + receipt.id,
        receipt.branch && "Branch: " + receipt.branch,
        receipt.base_revision && "Base revision: " + receipt.base_revision,
        receipt.commit_revision && "Repository commit: " + receipt.commit_revision,
        ...(receipt.changes || []).map(file => file.path + " · SHA-256 " + file.sha256),
        receipt.message
      ].filter(Boolean).join("\n");
      if (row.record.textContent !== technical) row.record.textContent = technical;
    }
    $("empty-receipts").hidden = receipts.length > 0;
    if ($("receipt-status").textContent.startsWith("Could not check submission status.")) {
      $("receipt-status").textContent = "Receipts refreshed.";
    }
    if (receipts.some(r => ["queued", "preparing", "publishing"].includes(r.status))) {
      receiptTimer = setTimeout(() => loadReceipts().catch(receiptRefreshFailed), 3000);
    }
  }
  async function chooseAssignment() {
    generation++;
    confirmedReceipt = "";
    active = $("assignment").value;
    const locationQuery = new URLSearchParams();
    if (active) locationQuery.set("submission", active);
    history.replaceState(null, "", location.pathname + (active ? "?" + locationQuery : ""));
    selectedFiles = [];
    awaitingBridge = false;
    clearTimeout(bridgeTimer);
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
    receiptRows.clear();
    $("receipt-status").textContent = "";
    $("empty-receipts").hidden = true;
    await loadReceipts().catch(receiptRefreshFailed);
  }
  $("retry-load").onclick = () => load();
  async function load() {
    if (loadingAssignments || busy) return;
    loadingAssignments = true;
    $("retry-load").disabled = true;
    $("reload").disabled = true;
    try {
      const data = await request("/me/work");
      $("signin").hidden = true; $("retry-load").hidden = true;
      $("account").textContent = "Signed in to Cairn as " + data.username + ". Check that this is your course account.";
      work = (data.work || []).filter(item => item.submissions_enabled);
      $("assignment").replaceChildren(new Option("Choose an assignment", ""));
      for (const item of work) $("assignment").add(new Option(item.classroom_name + " — " + item.assignment_title, item.submission_id));
      $("selection").disabled = false;
      status(work.length ? "Choose the assignment you intend to submit to." : "No active assignments have saved-file submission enabled. You can still use the repository workflow.");
      if (assignment()) {
        $("assignment").value = active;
        $("sources").disabled = !submissionOpen();
        $("confirm").disabled = !submissionOpen();
        status(preview && submissionOpen() ? "Signed in. Review or retry confirmation for your saved preview." :
          "Signed in. Check your receipts or continue with saved files.");
        await loadReceipts().catch(receiptRefreshFailed);
      } else {
        if (work.some(item => item.submission_id === requestedAssignment)) $("assignment").value = requestedAssignment;
        await chooseAssignment();
      }
    } catch (error) {
      $("signin").hidden = error.status !== 401;
      $("retry-load").hidden = error.status === 401;
      status(error.status === 401 ? "Your session has ended. Sign in, then check sign-in again." : error.status === 403 ? "This account cannot access the course. Check your course account or contact your instructor." : "Assignments are temporarily unavailable. Your saved files are still in your workspace. Try loading assignments again.", true);
    } finally {
      loadingAssignments = false;
      $("retry-load").disabled = false;
      $("reload").disabled = false;
    }
  }
  $("files").addEventListener("change", async () => {
    const current = ++generation;
    awaitingBridge = false;
    clearTimeout(bridgeTimer);
    $("bridge-status").textContent = "";
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
      setFiles(value); status("Saved files selected. Choose Review saved files to continue.");
    } catch {
      if (current !== generation || busy) return;
      $("file-summary").textContent = "No valid files selected.";
      status("Check the filenames and size limits, then select the files again.", true);
    }
  });
  $("receive").addEventListener("click", () => {
    if (busy || !bridge || !submissionOpen() || !allowedOrigin(bridge.origin, assignment().workspace_url) || !window.opener || window.opener.closed) { status("Open this page again from your Waypoint workspace.", true); return; }
    if (awaitingBridge) return;
    awaitingBridge = true;
    clearTimeout(bridgeTimer);
    bridgeTimer = setTimeout(() => {
      awaitingBridge = false;
      $("bridge-status").textContent = "Cairn did not receive the files. Return to the notebook and reopen submission, or select files from this device.";
    }, 15000);
    $("bridge-status").textContent = "Waiting for your selected saved files…";
    window.opener.postMessage({type: "cairn-ready", version: 1, nonce: bridge.nonce}, bridge.origin);
  });
  window.addEventListener("message", async event => {
    if (!awaitingBridge || !bridge || busy || event.origin !== bridge.origin || event.source !== window.opener || !assignment() || !allowedOrigin(bridge.origin, assignment().workspace_url)) return;
    const data = event.data;
    if (!data || data.type !== "waypoint-files" || data.version !== 1 || data.nonce !== bridge.nonce) return;
    awaitingBridge = false;
    clearTimeout(bridgeTimer);
    const current = ++generation;
    try {
      const value = await validateFiles(data.files);
      if (current !== generation || busy) return;
      setFiles(value);
      $("bridge-status").textContent = "Received saved files. Nothing has been submitted yet.";
      event.source.postMessage({type: "cairn-received", version: 1, nonce: bridge.nonce}, bridge.origin);
      status("Choose Review saved files to continue.");
    } catch {
      if (current !== generation || busy) return;
      $("bridge-status").textContent = "Cairn could not read these files. Return to Waypoint, select the saved files again, and reopen submission.";
    }
  });
  $("preview").addEventListener("click", async () => {
    if (busy || !submissionOpen() || !selectedFiles.length) return;
    lock(true);
    status("Preparing your file preview…");
    try {
      const receipt = await request(endpoint() + "/submission-previews", {idempotency_key: key, files: selectedFiles});
      if (receipt.status === "preview") { renderPreview(receipt); status("Review the assignment and exact files before confirming."); }
      else { status("These files already have a receipt. Check its status below."); await loadReceipts().catch(receiptRefreshFailed); }
    } catch (error) {
      if (error.status === 401) signIn();
      else if (error.status === 400) status("The preview was rejected. Check the file names and size limits, then select the files again.", true);
      else if (error.status === 409) status("A preview could not be opened. Check the assignment and existing receipts before trying again.", true);
      else status("Could not open the preview. Your selected files are still here. Choose Review saved files again to recover it.", true);
    }
    finally {
      lock(false);
      if (document.activeElement === document.body) $("preview").focus();
    }
  });
  $("confirm").addEventListener("click", async () => {
    if (busy || !submissionOpen() || !preview) return;
    lock(true);
    status("Confirming your submission…");
    try {
      const receipt = await request(endpoint() + "/submission-previews/" + encodeURIComponent(preview.id) + "/confirm", {});
      confirmedReceipt = receipt.id;
      status("");
      announceReceipt(receiptState(receipt.status)[0]);
      $("preview-section").hidden = true;
      $("receipts-heading").focus();
      preview = null; selectedFiles = []; $("files").value = ""; $("preview").disabled = true;
      $("file-summary").textContent = "Check the receipt below for this submission.";
      await loadReceipts().catch(receiptRefreshFailed);
    } catch (error) {
      if (error.status === 401) signIn();
      else if (error.status === 410) {
        resetPreview();
        status("This preview expired before confirmation. Review your files again to create a new preview.", true);
        const next = selectedFiles.length ? $("preview") : $("files");
        lock(false);
        next.focus();
      } else if (error.status === 403 || error.status === 404 || error.status === 409) {
        status("Cairn could not confirm this preview. Refresh receipts to check its status. If you still cannot submit, contact your instructor.", true);
      } else {
        status("Confirmation was interrupted. Refresh receipts or retry this same confirmation. Retrying this preview will not create a second submission.", true);
      }
    }
    finally {
      lock(false);
      if (preview && document.activeElement === document.body) $("confirm").focus();
    }
  });
  $("assignment").addEventListener("change", chooseAssignment);
  $("reload").addEventListener("click", load);
  $("refresh").addEventListener("click", () => loadReceipts().catch(receiptRefreshFailed));
  load();
})();
