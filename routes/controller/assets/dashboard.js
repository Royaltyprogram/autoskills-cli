const STORAGE_KEYS = {
  sessionUser: "autoskills_session_user",
  sessionOrg: "autoskills_session_org",
  onboardingDone: "autoskills_onboarding_done",
};
(function migrateStorageKeys() {
  const migrations = [
    ["crux_session_user", "autoskills_session_user"],
    ["crux_session_org", "autoskills_session_org"],
    ["crux_onboarding_done", "autoskills_onboarding_done"],
  ];
  for (const [oldKey, newKey] of migrations) {
    try {
      const val = localStorage.getItem(oldKey);
      if (val !== null && localStorage.getItem(newKey) === null) {
        localStorage.setItem(newKey, val);
        localStorage.removeItem(oldKey);
      }
    } catch (_) {}
  }
})();
const DEFAULT_SERVER_ORIGIN = "https://useautoskills.com";

const state = {
  busy: false,
  selectedProjectID: "",
  skillSetBundle: null,
  tokenImpact: null,
  session: null,
  settingsOpen: false,
  overview: null,
};

const $ = (id) => document.getElementById(id);

function escapeHTML(value) {
  return String(value == null ? "" : value).replace(
    /[&<>"']/g,
    (char) =>
      ({
        "&": "&amp;",
        "<": "&lt;",
        ">": "&gt;",
        '"': "&quot;",
        "'": "&#39;",
      })[char],
  );
}

function escapeAttr(value) {
  return escapeHTML(value);
}

function readStorage(key, fallback = "") {
  try {
    const value = window.localStorage.getItem(key);
    return value == null ? fallback : value;
  } catch (error) {
    return fallback;
  }
}

function writeStorage(key, value) {
  try {
    if (value == null || value === "") {
      window.localStorage.removeItem(key);
      return;
    }
    window.localStorage.setItem(key, value);
  } catch (error) {
    // Storage is optional for the dashboard; ignore browser restrictions.
  }
}

function readJSONStorage(key, fallback = null) {
  const value = readStorage(key);
  if (!value) {
    return fallback;
  }
  try {
    return JSON.parse(value);
  } catch (error) {
    return fallback;
  }
}

function setStatus(text, isError = false) {
  const el = $("status");
  el.textContent = text;
  el.dataset.tone = isError ? "error" : "info";
}

/* ── Wizard ── */

let onboardingPollTimer = null;
const ONBOARDING_POLL_MS = 5000;

function stopOnboardingPoll() {
  if (onboardingPollTimer != null) {
    clearInterval(onboardingPollTimer);
    onboardingPollTimer = null;
  }
}

function startOnboardingPoll() {
  if (onboardingPollTimer != null) {
    return;
  }
  onboardingPollTimer = window.setInterval(() => {
    const inline = $("onboardingInline");
    if (!inline || inline.hidden) {
      stopOnboardingPoll();
      return;
    }
    load();
  }, ONBOARDING_POLL_MS);
}

/* ── Progress poll ── */

let progressPollTimer = null;
const PROGRESS_POLL_MS = 4000;

function shouldProgressPoll(overview) {
  const job = overview?.active_import_job;
  const research = overview?.research_status;
  return (
    (job && ACTIVE_IMPORT_STATUSES.includes(job.status)) ||
    research?.state === "running"
  );
}

function startProgressPoll() {
  if (progressPollTimer != null) return;
  progressPollTimer = setInterval(() => {
    if (state.busy) return;
    load();
  }, PROGRESS_POLL_MS);
}

function stopProgressPoll() {
  if (progressPollTimer != null) {
    clearInterval(progressPollTimer);
    progressPollTimer = null;
  }
}

function showWizard() {
  $("onboardingInline").hidden = false;
  $("dashboardContent").hidden = true;
  renderConnectionStatus(null, null);
  setStatus("No workspace connected. Follow the steps below to get started.");
  startOnboardingPoll();
}

function hideWizard() {
  $("onboardingInline").hidden = true;
  $("dashboardContent").hidden = false;
  writeStorage(STORAGE_KEYS.onboardingDone, "1");
  stopOnboardingPoll();
}

function normalizeOrigin(value) {
  return String(value == null ? "" : value)
    .trim()
    .replace(/\/+$/, "");
}

function buildSetupCommand(origin = window.location.origin || "") {
  const normalizedOrigin = normalizeOrigin(origin);
  if (!normalizedOrigin || normalizedOrigin === DEFAULT_SERVER_ORIGIN) {
    return "autoskills setup";
  }
  return `autoskills setup --server ${normalizedOrigin}`;
}

/* ── Setup Progress ── */

const ACTIVE_IMPORT_STATUSES = ["receiving_chunks", "queued", "running"];

function deriveSetupStage(overview, skillSet) {
  const job = overview?.active_import_job;
  const research = overview?.research_status;
  const jobActive =
    job && ACTIVE_IMPORT_STATUSES.includes(job.status);
  const researchRunning = research?.state === "running";
  const hasReports = (overview?.active_reports || 0) > 0;
  const hasSkillSet = skillSet?.version != null;

  if (jobActive) return "uploading";
  if (researchRunning) return "researching";
  if (!hasReports) return "awaiting_report";
  if (!hasSkillSet) return "building_skill";
  return "ready";
}

function computeETA(job) {
  if (!job?.started_at || !job.processed_sessions) return null;
  const elapsed = (Date.now() - new Date(job.started_at).getTime()) / 1000;
  if (elapsed < 1) return null;
  const rate = job.processed_sessions / elapsed;
  const total = job.total_sessions || job.received_sessions || 0;
  const remaining = Math.max(0, total - job.processed_sessions);
  const etaSec = remaining / rate;
  if (etaSec < 5) return "almost done";
  if (etaSec < 60) return `~${Math.round(etaSec)}s remaining`;
  return `~${Math.round(etaSec / 60)}m remaining`;
}

let lastSetupStageKey = "";

function renderSetupProgress(overview, skillSet, stage) {
  const screen = $("setupProgressScreen");
  const dashboard = $("dashboardContent");
  if (!screen) return;

  screen.hidden = false;
  if (dashboard) dashboard.hidden = true;

  const job = overview?.active_import_job;
  const research = overview?.research_status;
  const totalSessions = overview?.total_sessions || 0;

  const stages = [
    {
      label: "STAGE 1",
      title: "Connected",
      state: "done",
      copy: "Your workspace is linked.",
      meta: "",
    },
    {
      label: "STAGE 2",
      title: "Uploading sessions",
      state: stage === "uploading" ? "active" : (totalSessions > 0 || stage !== "uploading") && stage !== "uploading" ? "done" : "pending",
      copy: "Codex session history is being imported.",
      meta: "",
    },
    {
      label: "STAGE 3",
      title: "Generating report",
      state: stage === "researching" ? "active" : stage === "uploading" ? "pending" : (overview?.active_reports || 0) > 0 ? "done" : "pending",
      copy: "AI analyzes your sessions to find patterns.",
      meta: "",
    },
    {
      label: "STAGE 4",
      title: "Building skill",
      state: stage === "building_skill" ? "active" : stage === "ready" ? "done" : "pending",
      copy: "Your personal skill bundle is being compiled.",
      meta: "",
    },
    {
      label: "STAGE 5",
      title: "Ready",
      state: stage === "ready" ? "done" : "pending",
      copy: "Your workspace is fully operational.",
      meta: "",
    },
  ];

  // Compute meta for upload stage
  if (job && ACTIVE_IMPORT_STATUSES.includes(job.status)) {
    const total = job.total_sessions || job.received_sessions || 0;
    const processed = job.processed_sessions || 0;
    const pct = total > 0 ? Math.round((processed / total) * 100) : 0;
    const eta = computeETA(job);
    let meta = `${processed} / ${total} sessions (${pct}%)`;
    if (eta) meta += ` &middot; ${escapeHTML(eta)}`;
    if (job.failed_sessions > 0) meta += ` &middot; ${job.failed_sessions} failed`;
    stages[1].meta = meta;
  } else if (totalSessions > 0) {
    stages[1].meta = `${totalSessions} sessions imported`;
  }

  // Compute meta for research stage
  if (research) {
    if (research.state === "running") {
      let meta = `Analyzing ${research.session_count || 0} sessions`;
      if (research.last_duration_ms > 0) {
        meta += ` &middot; ~${Math.round(research.last_duration_ms / 1000)}s estimated`;
      }
      stages[2].meta = meta;
    } else if (research.report_count > 0) {
      stages[2].meta = `${research.report_count} report(s) generated`;
    }
  }

  // Compute meta for skill stage
  if (skillSet?.version) {
    stages[3].meta = `Version: ${escapeHTML(skillSet.version)}`;
  }

  // Diff check to avoid unnecessary DOM writes
  const stageKey = stages.map((s) => `${s.state}:${s.meta}`).join("|");
  if (stageKey === lastSetupStageKey) return;
  lastSetupStageKey = stageKey;

  const stepper = $("setupStepper");
  if (!stepper) return;

  stepper.innerHTML = stages
    .map(
      (s) => `
    <div class="lifecycle-stage-card" data-state="${s.state}">
      <div class="lifecycle-stage-top">
        <span class="lifecycle-stage-index">${s.label}</span>
        <div class="lifecycle-stage-state"></div>
      </div>
      <div class="lifecycle-stage-title">${escapeHTML(s.title)}</div>
      <div class="lifecycle-stage-copy">${escapeHTML(s.copy)}</div>
      ${s.meta ? `<div class="lifecycle-stage-meta">${s.meta}</div>` : ""}
    </div>`,
    )
    .join("");
}

function hideSetupProgress() {
  const screen = $("setupProgressScreen");
  const dashboard = $("dashboardContent");
  if (screen) screen.hidden = true;
  if (dashboard) dashboard.hidden = false;
  lastSetupStageKey = "";
}

/* ── Session ── */

function cacheSession(session) {
  state.session = session;
  writeStorage(
    STORAGE_KEYS.sessionUser,
    JSON.stringify((session && session.user) || {}),
  );
  writeStorage(
    STORAGE_KEYS.sessionOrg,
    JSON.stringify((session && session.organization) || {}),
  );
}

function clearSession() {
  [STORAGE_KEYS.sessionUser, STORAGE_KEYS.sessionOrg].forEach((key) =>
    writeStorage(key, ""),
  );
  state.session = null;
  state.selectedProjectID = "";
}

function renderSessionContext() {
  const session = state.session || {
    user: readJSONStorage(STORAGE_KEYS.sessionUser, {}) || {},
    organization: readJSONStorage(STORAGE_KEYS.sessionOrg, {}) || {},
  };
  const user = session.user || {};
  const org = session.organization || {};

  const nameEl = $("workspaceName");
  if (nameEl) nameEl.textContent = org.name || org.id || "";

  renderSidebar();
}

function isUnauthorized(error) {
  return Boolean(error && typeof error === "object" && error.status === 401);
}

function redirectToLanding(message) {
  clearSession();
  if (message) {
    try {
      window.sessionStorage.setItem("autoskills_redirect_notice", message);
    } catch (error) {
      // Ignore sessionStorage failures and continue the redirect.
    }
  }
  window.location.replace("/login");
}

async function ensureSession() {
  try {
    const session = await requestJSON(
      "/api/v1/auth/me",
      {},
      "Failed to restore the signed-in session.",
    );
    cacheSession(session);
    renderSessionContext();
    return true;
  } catch (error) {
    if (isUnauthorized(error)) {
      redirectToLanding("Your session expired. Sign in again.");
      return false;
    }
    throw error;
  }
}

/* ── Request ── */

async function requestJSON(
  url,
  options = {},
  fallbackMessage = "Request failed.",
) {
  const headers = Object.assign({}, options.headers || {});
  const response = await fetch(
    url,
    Object.assign({ credentials: "same-origin" }, options, { headers }),
  );
  const rawBody = await response.text();

  let envelope = null;
  if (rawBody) {
    try {
      envelope = JSON.parse(rawBody);
    } catch (error) {
      const unreadable = new Error(
        response.ok
          ? "The server returned an unreadable response."
          : fallbackMessage,
      );
      unreadable.status = response.status;
      throw unreadable;
    }
  }

  if (!response.ok) {
    const failure = new Error(
      (envelope && (envelope.msg || envelope.message)) || fallbackMessage,
    );
    failure.status = response.status;
    throw failure;
  }
  if (!envelope || typeof envelope !== "object") {
    const unexpected = new Error("The server returned an unexpected response.");
    unexpected.status = response.status;
    throw unexpected;
  }
  if (envelope.code !== 0) {
    const applicationError = new Error(
      envelope.msg || envelope.message || fallbackMessage,
    );
    applicationError.status = response.status;
    throw applicationError;
  }

  return envelope.data || {};
}

/* ── Clipboard ── */

async function copyText(value) {
  const text = String(value || "").trim();
  if (!text) {
    return false;
  }

  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch (error) {
      // Fall back to a temporary textarea below.
    }
  }

  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "readonly");
  textarea.style.position = "absolute";
  textarea.style.left = "-9999px";
  document.body.appendChild(textarea);
  textarea.select();

  let copied = false;
  try {
    copied = document.execCommand("copy");
  } catch (error) {
    copied = false;
  }

  document.body.removeChild(textarea);
  return copied;
}

async function copyCommand(targetID, label, triggerBtn) {
  const target = $(targetID);
  if (!target) {
    setStatus("The requested command block is missing.", true);
    return;
  }

  const copied = await copyText(target.textContent);
  if (!copied) {
    setStatus(`Failed to copy the ${label || "command"}.`, true);
    return;
  }

  if (triggerBtn && triggerBtn.classList.contains("copy-icon-btn")) {
    const copyIcon = triggerBtn.querySelector(".copy-icon");
    const checkIcon = triggerBtn.querySelector(".check-icon");
    if (copyIcon && checkIcon) {
      copyIcon.style.display = "none";
      checkIcon.style.display = "";
      triggerBtn.classList.add("is-copied");
      setTimeout(() => {
        copyIcon.style.display = "";
        checkIcon.style.display = "none";
        triggerBtn.classList.remove("is-copied");
      }, 1500);
    }
  }

  setStatus(`Copied the ${label || "command"}.`);
}

/* ── Formatting utilities ── */

function toArray(value) {
  return Array.isArray(value) ? value : [];
}

function formatCount(value) {
  return new Intl.NumberFormat("en-US").format(Number(value || 0));
}

function formatDateTime(value) {
  if (!value) {
    return "Not yet";
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return String(value);
  }

  return new Intl.DateTimeFormat("en-US", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

function formatShortDate(value) {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return String(value);
  }
  return new Intl.DateTimeFormat("en-US", {
    month: "short",
    day: "numeric",
  }).format(date);
}

function shortHash(value, length = 12) {
  const text = String(value || "").trim();
  if (!text) {
    return "";
  }
  return text.length <= length ? text : text.slice(0, length);
}

function formatCompactCount(value) {
  return new Intl.NumberFormat("en-US", {
    notation: "compact",
    maximumFractionDigits: 1,
  }).format(Number(value || 0));
}

function formatPercent(value) {
  return `${Math.round(Number(value || 0) * 100)}%`;
}

function formatFixedNumber(value, digits = 2) {
  const number = Number(value || 0);
  if (!Number.isFinite(number)) {
    return Number(0).toFixed(digits);
  }
  return number.toFixed(digits);
}

function formatRate(value) {
  const number = Number(value || 0);
  if (!Number.isFinite(number) || number <= 0) {
    return "0";
  }
  return number >= 10 ? String(Math.round(number)) : number.toFixed(1);
}

function formatSignedCount(value) {
  const rounded = Math.round(Number(value || 0));
  if (rounded > 0) {
    return `+${formatCount(rounded)}`;
  }
  if (rounded < 0) {
    return `-${formatCount(Math.abs(rounded))}`;
  }
  return "0";
}

function formatDurationBetween(startValue, endValue) {
  if (!startValue || !endValue) {
    return "Not yet";
  }

  const start = new Date(startValue);
  const end = new Date(endValue);
  if (
    Number.isNaN(start.getTime()) ||
    Number.isNaN(end.getTime()) ||
    end <= start
  ) {
    return "Not yet";
  }

  const minutes = Math.round((end.getTime() - start.getTime()) / 60000);
  if (minutes < 60) {
    return `${minutes}m`;
  }

  const hours = Math.floor(minutes / 60);
  const restMinutes = minutes % 60;
  if (hours < 24) {
    return restMinutes > 0 ? `${hours}h ${restMinutes}m` : `${hours}h`;
  }

  const days = Math.floor(hours / 24);
  const restHours = hours % 24;
  return restHours > 0 ? `${days}d ${restHours}h` : `${days}d`;
}

function formatLatency(value) {
  const ms = Math.round(Number(value || 0));
  if (!ms || ms < 0) {
    return "Not captured";
  }
  if (ms < 1000) {
    return `${ms}ms`;
  }
  if (ms < 60000) {
    const seconds = ms / 1000;
    return `${seconds >= 10 ? Math.round(seconds) : seconds.toFixed(1)}s`;
  }
  const minutes = Math.floor(ms / 60000);
  const seconds = Math.round((ms % 60000) / 1000);
  return seconds > 0 ? `${minutes}m ${seconds}s` : `${minutes}m`;
}

function parseDateValue(value) {
  if (!value) {
    return null;
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return null;
  }
  return date;
}

function minutesBetween(startValue, endValue) {
  const start = parseDateValue(startValue);
  const end = parseDateValue(endValue);
  if (!start || !end || end <= start) {
    return null;
  }
  return Math.round((end.getTime() - start.getTime()) / 60000);
}

function formatMinutesDuration(value) {
  if (value == null) {
    return "Not yet";
  }
  const minutes = Number(value);
  if (!Number.isFinite(minutes) || minutes < 0) {
    return "Not yet";
  }
  if (minutes < 1) {
    return "<1m";
  }
  if (minutes < 60) {
    return `${Math.round(minutes)}m`;
  }
  const hours = Math.floor(minutes / 60);
  const restMinutes = Math.round(minutes % 60);
  if (hours < 24) {
    return restMinutes > 0 ? `${hours}h ${restMinutes}m` : `${hours}h`;
  }
  const days = Math.floor(hours / 24);
  const restHours = hours % 24;
  return restHours > 0 ? `${days}d ${restHours}h` : `${days}d`;
}

function titleize(value) {
  return String(value || "")
    .replace(/[_-]+/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .replace(/\b\w/g, (char) => char.toUpperCase());
}

function baseName(path) {
  const parts = String(path || "").split(/[\\/]/);
  return parts[parts.length - 1] || "workspace settings";
}

function emptyState(title, description) {
  return `
        <div class="item empty">
          <div class="item-title">${escapeHTML(title)}</div>
          <div class="meta">${escapeHTML(description)}</div>
        </div>
      `;
}

function pill(label, tone) {
  const safeTone = tone ? ` pill-${tone}` : "";
  return `<span class="pill${safeTone}">${escapeHTML(label)}</span>`;
}

function tokenTone(status) {
  const raw = String(status || "").toLowerCase();
  if (raw === "active") {
    return "good";
  }
  if (raw === "expired") {
    return "warn";
  }
  if (raw === "revoked") {
    return "danger";
  }
  return "sky";
}

function truncateText(value, maxLength = 96) {
  const text = String(value || "").trim();
  if (text.length <= maxLength) {
    return text;
  }
  return `${text.slice(0, maxLength - 1)}...`;
}

function normalizeInlineText(value) {
  return String(value || "")
    .replace(/\r\n/g, "\n")
    .replace(/\s+/g, " ")
    .trim();
}

function previewBlockText(value, maxLength = 560) {
  const text = String(value || "").trim();
  if (text.length <= maxLength) {
    return text;
  }
  return `${text.slice(0, maxLength - 1)}...`;
}

/* ── Skill set utilities ── */

function skillSetStatusLabel(status) {
  const raw = String(status || "")
    .trim()
    .toLowerCase();
  if (raw === "ready") {
    return "Ready";
  }
  if (raw === "no_reports") {
    return "Awaiting reports";
  }
  if (raw === "no_candidate") {
    return "Awaiting candidate";
  }
  if (raw === "unsupported") {
    return "Unsupported";
  }
  if (!raw) {
    return "Unknown";
  }
  return titleize(raw.replace(/_/g, " "));
}

function skillSetStatusTone(status) {
  const raw = String(status || "")
    .trim()
    .toLowerCase();
  if (raw === "ready") {
    return "good";
  }
  if (raw === "no_reports" || raw === "no_candidate") {
    return "warn";
  }
  if (raw === "unsupported") {
    return "danger";
  }
  return "sky";
}

function skillSetSyncLabel(status) {
  const raw = String(status || "")
    .trim()
    .toLowerCase();
  if (!raw) {
    return "Not synced yet";
  }
  return titleize(raw.replace(/_/g, " "));
}

function skillSetSyncTone(status) {
  const raw = String(status || "")
    .trim()
    .toLowerCase();
  if (raw === "synced" || raw === "unchanged" || raw === "rolled_back") {
    return "good";
  }
  if (raw === "paused" || raw === "blocked") {
    return "warn";
  }
  if (raw === "failed" || raw === "conflict") {
    return "danger";
  }
  return "sky";
}

function skillSetDeploymentLabel(eventType) {
  const raw = String(eventType || "")
    .trim()
    .toLowerCase();
  if (raw === "skillset_deployed") {
    return "Deployed";
  }
  if (raw === "skillset_sync_succeeded") {
    return "Synced";
  }
  if (raw === "skillset_sync_failed") {
    return "Sync failed";
  }
  if (raw === "skillset_rolled_back") {
    return "Rolled back";
  }
  if (raw === "skillset_paused") {
    return "Paused";
  }
  if (raw === "skillset_resumed") {
    return "Resumed";
  }
  if (raw === "skillset_auto_blocked") {
    return "Blocked";
  }
  return (
    titleize(raw.replace(/^skillset_/, "").replace(/_/g, " ")) || "Updated"
  );
}

function skillSetDeploymentTone(eventType, syncStatus) {
  const raw = String(eventType || "")
    .trim()
    .toLowerCase();
  if (
    raw === "skillset_deployed" ||
    raw === "skillset_sync_succeeded" ||
    raw === "skillset_resumed" ||
    raw === "skillset_rolled_back"
  ) {
    return "good";
  }
  if (raw === "skillset_paused" || raw === "skillset_auto_blocked") {
    return "warn";
  }
  if (raw === "skillset_sync_failed") {
    return "danger";
  }
  return skillSetSyncTone(syncStatus);
}

function skillSetDecisionLabel(decision) {
  const raw = String(decision || "")
    .trim()
    .toLowerCase();
  if (!raw) {
    return "Pending";
  }
  if (raw === "shadow") {
    return "Awaiting sync";
  }
  if (raw === "blocked") {
    return "Blocked";
  }
  if (raw === "rolled_back") {
    return "Rolled back";
  }
  if (raw === "deployed") {
    return "Deployed";
  }
  return titleize(raw.replace(/_/g, " "));
}

function skillSetDecisionTone(decision) {
  const raw = String(decision || "")
    .trim()
    .toLowerCase();
  if (raw === "deployed") {
    return "good";
  }
  if (raw === "shadow") {
    return "sky";
  }
  if (raw === "blocked" || raw === "rolled_back") {
    return "warn";
  }
  return "sky";
}

function skillSetShadowGuardrailLabel(guardrail) {
  const raw = String(guardrail || "")
    .trim()
    .toLowerCase();
  if (!raw) {
    return "Pending";
  }
  if (raw === "passed") {
    return "Passed";
  }
  if (raw === "low_confidence") {
    return "Low confidence";
  }
  if (raw === "low_score") {
    return "Low score";
  }
  if (raw === "high_churn") {
    return "High churn";
  }
  return titleize(raw);
}

function skillSetShadowLines(evaluation) {
  const record =
    evaluation && typeof evaluation === "object" ? evaluation : null;
  if (!record) {
    return [];
  }
  const score = Number(record.score || 0);
  const averageConfidence = Number(record.average_confidence || 0);
  const changedDocuments = Number(record.changed_document_count || 0);
  const addedRules = Number(record.added_rule_count || 0);
  const removedRules = Number(record.removed_rule_count || 0);
  const ruleChurn = Number(record.rule_churn || 0);
  const guardrail = skillSetShadowGuardrailLabel(record.guardrail || "");
  return [
    `Shadow score ${formatFixedNumber(score)} with ${formatPercent(averageConfidence)} average report confidence.`,
    `${formatCount(changedDocuments)} changed document${changedDocuments === 1 ? "" : "s"} and ${formatCount(ruleChurn)} rule change${ruleChurn === 1 ? "" : "s"} (${formatCount(addedRules)} added / ${formatCount(removedRules)} removed).`,
    `Guardrail: ${guardrail}.`,
  ];
}

function skillSetDeploymentSummary(item) {
  const record = item && typeof item === "object" ? item : {};
  const summary = normalizeInlineText(record.summary || "");
  if (summary) {
    return summary;
  }
  const currentVersion = normalizeInlineText(record.applied_version || "");
  const previousVersion = normalizeInlineText(record.previous_version || "");
  const rawType = String(record.event_type || "")
    .trim()
    .toLowerCase();
  if (rawType === "skillset_deployed" && currentVersion && previousVersion) {
    return `Deployed ${currentVersion} after replacing ${previousVersion}.`;
  }
  if (rawType === "skillset_deployed" && currentVersion) {
    return `Deployed ${currentVersion} to the connected workspace.`;
  }
  if (rawType === "skillset_rolled_back" && currentVersion) {
    return previousVersion
      ? `Rolled back from ${previousVersion} to ${currentVersion}.`
      : `Rolled back to ${currentVersion}.`;
  }
  if (rawType === "skillset_sync_failed") {
    return normalizeInlineText(
      record.last_error || "Managed bundle sync failed.",
    );
  }
  return "Managed bundle state changed.";
}

function renderSkillSetDeploymentHistory(items) {
  const history = toArray(items)
    .filter((item) => item && typeof item === "object")
    .slice(0, 6);
  if (!history.length) {
    return `
      <div class="skillset-history-empty">
        History will appear here after the connected CLI reports its first managed bundle state transition.
      </div>
    `;
  }
  return `
    <div class="skillset-history-list">
      ${history
        .map((item) => {
          const occurredAt = item.occurred_at
            ? formatDateTime(item.occurred_at)
            : "Unknown time";
          const label = skillSetDeploymentLabel(item.event_type);
          const tone = skillSetDeploymentTone(
            item.event_type,
            item.sync_status,
          );
          const summary = skillSetDeploymentSummary(item);
          const version = normalizeInlineText(item.applied_version || "");
          return `
            <article class="skillset-history-item" data-tone="${escapeAttr(tone)}">
              <div class="skillset-history-top">
                ${pill(label, tone)}
                <div class="skillset-history-time">${escapeHTML(occurredAt)}</div>
              </div>
              <div class="skillset-history-summary">${escapeHTML(summary)}</div>
              ${
                version
                  ? `<div class="skillset-history-meta">Version ${escapeHTML(version)}</div>`
                  : ""
              }
            </article>
          `;
        })
        .join("")}
    </div>
  `;
}

function renderSkillSetLatestDiff(diff) {
  const record = diff && typeof diff === "object" ? diff : {};
  const changedFiles = toArray(record.changed_files)
    .filter((item) => item && typeof item === "object")
    .slice(0, 4);
  if (!changedFiles.length) {
    return "";
  }
  return `
    <details class="skillset-details">
      <summary>View latest diff</summary>
      <div class="skillset-diff-list">
        ${changedFiles
          .map((item) => {
            const path = normalizeInlineText(item.path || "");
            const added = toArray(item.added)
              .map((entry) => normalizeInlineText(entry))
              .filter(Boolean)
              .slice(0, 4);
            const removed = toArray(item.removed)
              .map((entry) => normalizeInlineText(entry))
              .filter(Boolean)
              .slice(0, 4);
            return `
              <article class="skillset-diff-file">
                <div class="skillset-diff-path">${escapeHTML(path || "changed-file")}</div>
                ${
                  added.length
                    ? `<div class="skillset-diff-block">
                        <div class="skillset-diff-label">Added</div>
                        <ul class="skillset-diff-lines skillset-diff-lines-add">${added
                          .map((entry) => `<li>${escapeHTML(entry)}</li>`)
                          .join("")}</ul>
                      </div>`
                    : ""
                }
                ${
                  removed.length
                    ? `<div class="skillset-diff-block">
                        <div class="skillset-diff-label">Removed</div>
                        <ul class="skillset-diff-lines skillset-diff-lines-remove">${removed
                          .map((entry) => `<li>${escapeHTML(entry)}</li>`)
                          .join("")}</ul>
                      </div>`
                    : ""
                }
              </article>
            `;
          })
          .join("")}
      </div>
    </details>
  `;
}

function skillSetReportLines(bundle, reports) {
  const reportIDs = new Set(
    toArray(bundle && bundle.based_on_report_ids)
      .map((item) => String(item || "").trim())
      .filter(Boolean),
  );
  return toArray(reports)
    .filter((item) => reportIDs.has(String((item && item.id) || "").trim()))
    .map((item) =>
      normalizeInlineText(
        (item && (item.reason || item.summary || item.title)) || "",
      ),
    )
    .filter(Boolean)
    .slice(0, 4);
}

/* ── Action lane helper (used by AutoSkills hero) ── */

function actionLane(label, items, emptyText, tone) {
  const entries = toArray(items)
    .map((item) => normalizeInlineText(item))
    .filter(Boolean)
    .slice(0, 4);
  const body = entries.length
    ? `<ul class="action-lane-list">${entries
        .map((entry) => `<li>${escapeHTML(entry)}</li>`)
        .join("")}</ul>`
    : `<div class="action-lane-empty">${escapeHTML(emptyText)}</div>`;
  return `
    <div class="action-lane" data-tone="${escapeAttr(tone)}">
      <div class="action-lane-label">${escapeHTML(label)}</div>
      <div class="action-lane-body">${body}</div>
    </div>
  `;
}

/* ── AutoSkills hero (formerly renderAutoSkillsTab) ── */

function renderAutoSkillsHero(skillSet, reports) {
  const bannerEl = $("skillReportBanner");
  const changeEl = $("skillChangeReport");
  const bundleEl = $("skillBundlePreview");
  const timelineEl = $("skillDeploymentTimeline");
  const descEl = $("skillChangeDesc");
  if (!bannerEl || !changeEl || !bundleEl || !timelineEl) {
    return;
  }

  const bundle = skillSet && typeof skillSet === "object" ? skillSet : {};
  const clientState =
    bundle.client_state && typeof bundle.client_state === "object"
      ? bundle.client_state
      : {};
  const status = String(bundle.status || "")
    .trim()
    .toLowerCase();
  const version = String(bundle.version || "").trim();
  const appliedVersion = String(clientState.applied_version || "").trim();
  const syncStatus = String(clientState.sync_status || "")
    .trim()
    .toLowerCase();
  const syncMode = String(clientState.mode || "")
    .trim()
    .toLowerCase();
  const files = toArray(bundle.files);
  const mdFiles = files.filter((item) =>
    String((item && item.path) || "")
      .trim()
      .toLowerCase()
      .endsWith(".md"),
  );
  const generatedDocs = mdFiles
    .map((item) => String((item && item.path) || "").trim())
    .filter((path) => path && path !== "SKILL.md")
    .slice(0, 6);
  const summaryItems = toArray(bundle.summary)
    .map((item) => normalizeInlineText(item))
    .filter(Boolean)
    .slice(0, 4);
  const deploymentHistory = toArray(bundle.deployment_history);
  const versionHistory = toArray(bundle.version_history);
  const latestVersionRecord =
    versionHistory[0] && typeof versionHistory[0] === "object"
      ? versionHistory[0]
      : {};
  const deploymentDecision = String(
    latestVersionRecord.deployment_decision || "",
  )
    .trim()
    .toLowerCase();
  const decisionReason = normalizeInlineText(
    latestVersionRecord.decision_reason || "",
  );
  const shadowEvaluation =
    latestVersionRecord.shadow_evaluation &&
    typeof latestVersionRecord.shadow_evaluation === "object"
      ? latestVersionRecord.shadow_evaluation
      : null;
  const shadowGuardrailRaw = String(
    (shadowEvaluation && shadowEvaluation.guardrail) || "",
  )
    .trim()
    .toLowerCase();
  const shadowGuardrail = skillSetShadowGuardrailLabel(shadowGuardrailRaw);
  const shadowLines = skillSetShadowLines(shadowEvaluation);
  const latestDiff =
    bundle.latest_diff && typeof bundle.latest_diff === "object"
      ? bundle.latest_diff
      : {};
  const latestDiffSummary = toArray(latestDiff.summary)
    .map((item) => normalizeInlineText(item))
    .filter(Boolean)
    .slice(0, 4);
  const reportLines = skillSetReportLines(bundle, reports);
  const generatedAt = bundle.generated_at
    ? formatDateTime(bundle.generated_at)
    : "Not yet";
  const compiledHash = shortHash(bundle.compiled_hash || "");
  const reportCount = toArray(bundle.based_on_report_ids).length;
  const previousVersion = normalizeInlineText(
    latestDiff.from_version ||
      (versionHistory[1] && versionHistory[1].version) ||
      "",
  );
  const lastSyncedAt = clientState.last_synced_at
    ? formatDateTime(clientState.last_synced_at)
    : "Not reported yet";
  const updatedAt = clientState.updated_at
    ? formatDateTime(clientState.updated_at)
    : "Not reported yet";
  const clientHash = shortHash(clientState.applied_hash || "");
  const syncError = normalizeInlineText(clientState.last_error || "");

  /* ── Banner ── */
  if (!state.selectedProjectID) {
    bannerEl.innerHTML = `
      <div class="skill-banner" data-tone="empty">
        <div class="skill-banner-content">
          <div class="skill-banner-headline">No connected workspace</div>
          <div class="skill-banner-copy">Run <code>autoskills setup</code> in a repository and upload sessions. The first managed bundle will appear after reports are generated.</div>
        </div>
      </div>`;
    changeEl.innerHTML = "";
    bundleEl.innerHTML = "";
    timelineEl.innerHTML = "";
    return;
  }

  if (status !== "ready") {
    const statusLabel = skillSetStatusLabel(status || "no_reports");
    const statusTone = skillSetStatusTone(status || "no_reports");
    const headline =
      status === "no_candidate"
        ? "Reports exist, but the managed bundle is still being composed."
        : status === "unsupported"
          ? "This server does not expose managed skill bundles yet."
          : "The managed bundle will appear after enough reports are available.";
    const copy =
      status === "no_candidate"
        ? "AutoSkills has report evidence, but the latest analysis pass has not produced a stable multi-file bundle yet."
        : status === "unsupported"
          ? "Update the deployed server/dashboard pair before relying on automatic bundle visibility in the UI."
          : "Keep uploading sessions. Once recent workflow reports accumulate, AutoSkills will compile a canonical bundle here.";

    bannerEl.innerHTML = `
      <div class="skill-banner" data-tone="${escapeAttr(statusTone)}">
        <div class="skill-banner-pills">${pill(statusLabel, statusTone)}${reportLines.length ? pill(`${formatCount(reportLines.length)} evidence lane${reportLines.length === 1 ? "" : "s"}`, "sky") : ""}</div>
        <div class="skill-banner-content">
          <div class="skill-banner-headline">${escapeHTML(headline)}</div>
          <div class="skill-banner-copy">${escapeHTML(copy)}</div>
        </div>
      </div>`;
    if (descEl) {
      descEl.textContent =
        "AutoSkills will explain every automatic update once the first managed bundle is compiled.";
    }
    changeEl.innerHTML = `
      <div class="action-lane-group">
        ${actionLane("Observed", reportLines, "No report-level evidence is available yet.", "warn")}
        ${actionLane("Next", ["Upload more sessions", "Refresh the dashboard after the next analysis pass"], "The next report refresh will populate this section.", "accent")}
      </div>`;
    bundleEl.innerHTML = emptyState(
      "No bundle yet",
      "The managed bundle will appear here after the first candidate is compiled.",
    );
    timelineEl.innerHTML = emptyState(
      "No deployments yet",
      "Deployment events will appear here once the first bundle is synced to the CLI.",
    );
    return;
  }

  /* ── Conflict resolve banner ── */
  const isConflict = syncStatus === "conflict";

  if (isConflict) {
    const conflictHeadline =
      "Local modifications detected — resolve this on the CLI";
    const conflictCopy =
      "Dashboard-triggered conflict resolution is disabled for this MVP. Run one of the commands below on the connected machine to inspect or fix the managed bundle.";
    const resolveCommands = [
      {
        id: "skillResolveInspectCmd",
        title: "Inspect the conflict",
        note: "Review the local diff and available actions before changing anything.",
        command: "autoskills skills resolve",
        copyLabel: "inspect conflict command",
      },
      {
        id: "skillResolveAcceptCmd",
        title: "Accept remote",
        note: "Discard local edits and restore the managed bundle from the server.",
        command: "autoskills skills resolve --action accept-remote",
        copyLabel: "accept remote command",
      },
      {
        id: "skillResolveKeepLocalCmd",
        title: "Keep local",
        note: "Pause auto-sync and keep the local edits on this machine.",
        command: "autoskills skills resolve --action keep-local",
        copyLabel: "keep local command",
      },
      {
        id: "skillResolveBackupCmd",
        title: "Backup and sync",
        note: "Back up the local edits first, then replace them with the remote bundle.",
        command: "autoskills skills resolve --action backup-and-sync",
        copyLabel: "backup and sync command",
      },
    ];
    const resolveCommandsHTML = resolveCommands
      .map(
        (item) => `
        <div class="step-list">
          <div class="step-line"><strong>${escapeHTML(item.title)}</strong></div>
          <div class="step-line">${escapeHTML(item.note)}</div>
        </div>
        <div class="action-row">
          <button class="copy-icon-btn" type="button" data-action="copy-command" data-copy-target="${escapeAttr(item.id)}" data-copy-label="${escapeAttr(item.copyLabel)}" aria-label="Copy">
            <svg class="copy-icon" width="14" height="14" viewBox="0 0 16 16" fill="none"><rect x="5" y="5" width="9" height="9" rx="1.5" stroke="currentColor" stroke-width="1.3"/><path d="M11 5V3.5A1.5 1.5 0 0 0 9.5 2h-6A1.5 1.5 0 0 0 2 3.5v6A1.5 1.5 0 0 0 3.5 11H5" stroke="currentColor" stroke-width="1.3"/></svg>
            <svg class="check-icon" width="14" height="14" viewBox="0 0 16 16" fill="none" style="display:none"><path d="M3.5 8.5l3 3 6-7" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
          </button>
        </div>
        <div class="command-shell" id="${escapeAttr(item.id)}">${escapeHTML(item.command)}</div>
      `,
      )
      .join("");

    bannerEl.innerHTML = `
      <div class="skill-banner" data-tone="warn">
        <div class="skill-banner-pills">
          ${pill("Conflict", "warn")}
          ${syncMode ? pill(titleize(syncMode), syncMode === "frozen" ? "warn" : "sky") : ""}
        </div>
        <div class="skill-banner-content">
          <div class="skill-banner-headline">${escapeHTML(conflictHeadline)}</div>
          <div class="skill-banner-copy">${conflictCopy}</div>
          ${resolveCommandsHTML}
          ${syncError ? `<div class="skill-banner-error">${escapeHTML(syncError)}</div>` : ""}
        </div>
      </div>`;
  } else {
    /* ── Status banner (ready state) ── */
    const bannerHeadline =
      deploymentDecision === "blocked"
        ? "Candidate generated but deployment is blocked"
        : previousVersion
          ? `${previousVersion} \u2192 ${version || "latest"}`
          : version
            ? `Version ${version} active`
            : "Skill bundle active";

    bannerEl.innerHTML = `
    <div class="skill-banner" data-tone="good">
      <div class="skill-banner-pills">
        ${pill("Autobuild ready", "good")}
        ${syncStatus ? pill(skillSetSyncLabel(syncStatus), skillSetSyncTone(syncStatus)) : ""}
        ${deploymentDecision ? pill(skillSetDecisionLabel(deploymentDecision), skillSetDecisionTone(deploymentDecision)) : ""}
        ${syncMode ? pill(titleize(syncMode), syncMode === "frozen" ? "warn" : "sky") : ""}
      </div>
      <div class="skill-banner-content">
        <div class="skill-banner-headline">${escapeHTML(bannerHeadline)}</div>
        <div class="skill-banner-stats">
          <span class="skill-banner-stat"><strong>Applied</strong> ${escapeHTML(appliedVersion || version || "Pending")}</span>
          <span class="skill-banner-stat"><strong>Synced</strong> ${escapeHTML(lastSyncedAt)}</span>
          <span class="skill-banner-stat"><strong>Reports merged</strong> ${escapeHTML(formatCount(reportCount))}</span>
        </div>
      </div>
      <div class="skill-banner-aside">
        <div class="skill-banner-cli-note">Manage via CLI: <code>autoskills skills status</code> · <code>autoskills skills pause</code> · <code>autoskills skills rollback</code></div>
      </div>
    </div>`;
  }

  /* ── Change report ── */
  if (descEl) {
    descEl.textContent = syncError
      ? syncError
      : summaryItems[0]
        ? summaryItems[0]
        : "AutoSkills merged the latest workflow evidence into a canonical skill bundle for this workspace.";
  }

  changeEl.innerHTML = `
    <div class="skill-change-card">
      <div class="action-lane-group">
        ${actionLane("Changed because", reportLines, "Recent report evidence will appear here once the next bundle is compiled.", "warn")}
        ${actionLane("Decision", decisionReason ? [decisionReason] : [], "Deployment decision reasoning will appear after the first managed bundle transition is evaluated.", deploymentDecision === "blocked" || deploymentDecision === "rolled_back" ? "warn" : "sky")}
        ${actionLane("Shadow evaluation", shadowLines, "Structured shadow metrics will appear after the first candidate is evaluated.", deploymentDecision === "blocked" || (shadowGuardrailRaw && shadowGuardrailRaw !== "passed") ? "warn" : "sky")}
        ${actionLane("Latest diff", latestDiffSummary, "The first stored bundle version does not have a previous diff yet.", "sky")}
        ${actionLane("Expected impact", summaryItems, "Expected impact will appear here once the bundle includes report-backed changes.", "good")}
      </div>
      ${renderSkillSetLatestDiff(latestDiff)}
    </div>`;

  /* ── Bundle preview ── */
  if (!files.length) {
    bundleEl.innerHTML = emptyState(
      "No files in bundle",
      "Generated documents will appear here once the compiler emits the bundle.",
    );
  } else {
    const docTabs = files.slice(0, 8);
    bundleEl.innerHTML = `
      <div class="skill-bundle-strip">
        ${docTabs
          .map((file, idx) => {
            const path = String((file && file.path) || "").trim();
            return `<button class="skill-bundle-tab${idx === 0 ? " is-active" : ""}" type="button" data-action="switch-skill-doc" data-skill-doc-index="${idx}">${escapeHTML(path || "file-" + idx)}</button>`;
          })
          .join("")}
      </div>
      <div class="skill-bundle-panels">
        ${docTabs
          .map((file, idx) => {
            const path = String((file && file.path) || "").trim();
            const content = previewBlockText(
              file && file.content ? file.content : "",
            );
            const bytes = Number((file && file.bytes) || 0);
            return `
              <div class="skill-bundle-panel${idx === 0 ? " is-active" : ""}" data-skill-doc-panel="${idx}">
                <div class="skill-bundle-panel-head">
                  <span class="skill-bundle-panel-path">${escapeHTML(path || "generated-file")}</span>
                  <span class="skill-bundle-panel-meta">${escapeHTML(formatCount(bytes))} bytes</span>
                </div>
                <pre class="skill-bundle-panel-body">${escapeHTML(content || "No preview available.")}</pre>
              </div>`;
          })
          .join("")}
      </div>`;
  }

  /* ── Deployment timeline ── */
  if (!deploymentHistory.length) {
    timelineEl.innerHTML = emptyState(
      "No deployment events yet",
      "Version transitions will appear here as the bundle is synced and updated.",
    );
  } else {
    timelineEl.innerHTML = `
      <div class="skill-timeline">
        ${deploymentHistory
          .slice(0, 12)
          .map((item) => {
            const label = skillSetDeploymentLabel(item.event_type);
            const tone = skillSetDeploymentTone(
              item.event_type,
              item.sync_status,
            );
            const summary = skillSetDeploymentSummary(item);
            const occurredAt = item.occurred_at
              ? formatDateTime(item.occurred_at)
              : "";
            const ver = String(item.applied_version || "").trim();
            return `
              <div class="skill-timeline-item" data-tone="${escapeAttr(tone)}">
                <div class="skill-timeline-dot"></div>
                <div class="skill-timeline-body">
                  <div class="skill-timeline-top">
                    ${pill(label, tone)}
                    ${ver ? `<span class="skill-timeline-version">${escapeHTML(ver)}</span>` : ""}
                    <span class="skill-timeline-time">${escapeHTML(occurredAt)}</span>
                  </div>
                  <div class="skill-timeline-summary">${escapeHTML(summary)}</div>
                </div>
              </div>`;
          })
          .join("")}
      </div>`;
  }
}

/* ── Token summary helper (used by CLI tokens) ── */

function tokenSummary(item) {
  if (item.last_used_at) {
    return `Last used ${formatDateTime(item.last_used_at)}.`;
  }
  if (item.status === "revoked" && item.revoked_at) {
    return `Revoked ${formatDateTime(item.revoked_at)}.`;
  }
  if (item.expires_at) {
    return `Expires ${formatDateTime(item.expires_at)}.`;
  }
  return "Ready to authenticate a local CLI install.";
}

/* ── CLI Tokens (settings panel) ── */

function renderCLITokens(items) {
  if (!items.length) {
    $("cliTokenList").innerHTML = emptyState(
      "No CLI tokens issued yet",
      "Create a CLI token when you want a new local machine to authenticate.",
    );
    return;
  }

  $("cliTokenList").innerHTML = items
    .map((item) => {
      const canRevoke = String(item.status || "").toLowerCase() === "active";
      const identity = item.label || item.token_prefix || item.token_id;
      return `
          <div class="item">
            <div class="item-top">
              <div class="item-title">
                ${escapeHTML(identity)}
                <small>${escapeHTML(item.token_prefix || item.token_id)}</small>
              </div>
              ${pill(titleize(item.status || "active"), tokenTone(item.status))}
            </div>
            <div class="step-list">
              <div class="step-line">${escapeHTML(`Issued ${formatDateTime(item.created_at)}.`)}</div>
              <div class="step-line">${escapeHTML(tokenSummary(item))}</div>
            </div>
            ${
              canRevoke
                ? `
              <div class="action-row">
                <button class="secondary-button small-btn" type="button" data-action="revoke-cli-token" data-token-id="${escapeAttr(item.token_id)}">Revoke</button>
              </div>
            `
                : ""
            }
          </div>
        `;
    })
    .join("");
}

/* ── Full-text modal ── */

function openFullTextModal(title, bodyHTML) {
  $("fullTextTitle").textContent = title;
  $("fullTextBody").innerHTML = bodyHTML;
  $("fullTextOverlay").hidden = false;
  document.body.style.overflow = "hidden";
}

function closeFullTextModal() {
  $("fullTextOverlay").hidden = true;
  $("fullTextBody").innerHTML = "";
  document.body.style.overflow = "";
}

/* ── Collapsible sections ── */

function toggleSection(targetID) {
  const section = $(targetID);
  if (!section) {
    return;
  }
  section.classList.toggle("is-collapsed");
}

/* ── NEW: Connection status ── */

function renderConnectionStatus(project, overview) {
  const dot = $("connectionDot");
  const label = $("connectionLabel");
  const nameEl = $("workspaceName");

  if (!project) {
    dot.className = "connection-dot disconnected";
    label.textContent = "Not connected";
    label.className = "";
    nameEl.textContent = "";
    return;
  }

  nameEl.textContent = project.name || project.repo_path || "";

  const lastIngested = overview?.last_ingested_at
    ? new Date(overview.last_ingested_at)
    : null;
  const now = new Date();
  const hoursAgo = lastIngested
    ? (now - lastIngested) / (1000 * 60 * 60)
    : Infinity;

  if (hoursAgo < 24) {
    dot.className = "connection-dot connected";
    label.textContent = "Connected";
    label.className = "connected";
  } else if (lastIngested) {
    dot.className = "connection-dot stale";
    label.textContent =
      "Last upload " + formatDurationBetween(lastIngested, now) + " ago";
    label.className = "stale";
  } else {
    dot.className = "connection-dot stale";
    label.textContent = "Connected, awaiting first upload";
    label.className = "stale";
  }
}

/* ── NEW: Token impact card ── */

function renderTokenImpact(data) {
  const el = $("tokenImpactCard");
  if (!data || !data.has_deployment) {
    el.innerHTML = `
      <div class="metric-card-header">Token Impact</div>
      ${emptyState("No deployment yet", "Token impact will appear after your first AutoSkills deployment.")}
    `;
    return;
  }

  const before = Math.round(data.before_avg_tokens_per_session);
  const after = Math.round(data.after_avg_tokens_per_session);
  const pct = data.change_percent;
  const direction = pct < -1 ? "down" : pct > 1 ? "up" : "neutral";
  const arrow =
    direction === "down" ? "\u2193" : direction === "up" ? "\u2191" : "\u2192";
  const maxVal = Math.max(before, after, 1);
  const beforeH = Math.round((before / maxVal) * 40);
  const afterH = Math.round((after / maxVal) * 40);

  el.innerHTML = `
    <div class="metric-card-header">Token Impact</div>
    <div class="token-impact-bar-wrap">
      <div class="token-impact-bar before" style="height:${beforeH}px" title="Before: ${formatCount(before)}"></div>
      <div class="token-impact-bar after" style="height:${afterH}px" title="After: ${formatCount(after)}"></div>
    </div>
    <div class="token-impact-row">
      <div class="token-impact-group">
        <div class="token-impact-label">Before</div>
        <div class="token-impact-value">${formatCompactCount(before)}</div>
      </div>
      <div class="token-impact-group">
        <div class="token-impact-label">After</div>
        <div class="token-impact-value">${formatCompactCount(after)}</div>
      </div>
    </div>
    <div class="token-impact-delta" data-direction="${direction}">${arrow} ${Math.abs(pct).toFixed(1)}%</div>
    <div style="margin-top:8px;font-size:12px;color:var(--muted)">
      ${formatCount(data.before_session_count)} sessions before \u00b7 ${formatCount(data.after_session_count)} sessions after
    </div>
  `;
}

/* ── NEW: Deploy frequency card ── */

function renderDeployFrequency(skillSet) {
  const el = $("deployFreqCard");
  const history = skillSet?.deployment_history || [];

  if (history.length === 0) {
    el.innerHTML = `
      <div class="metric-card-header">Deploy Frequency</div>
      ${emptyState("No deployments", "Deployment frequency will appear after your first skill set sync.")}
    `;
    return;
  }

  const now = new Date();
  const thirtyDaysAgo = new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000);
  const recentDeploys = history.filter(
    (e) => new Date(e.occurred_at) >= thirtyDaysAgo,
  ).length;
  const perWeek = Math.round((recentDeploys / 30) * 7 * 10) / 10;

  el.innerHTML = `
    <div class="metric-card-header">Deploy Frequency</div>
    <div class="deploy-freq-value">${recentDeploys}</div>
    <div class="deploy-freq-unit">deploys in last 30 days</div>
    <div style="margin-top:8px;font-size:12px;color:var(--muted)">~${perWeek}/week</div>
  `;
}

/* ── NEW: Top modified rules card ── */

function renderTopModifiedRules(skillSet) {
  const el = $("topRulesCard");
  const diff = skillSet?.latest_diff;

  if (!diff || !diff.changed_files || diff.changed_files.length === 0) {
    el.innerHTML = `
      <div class="metric-card-header">Latest Changes</div>
      ${emptyState("No changes yet", "Rule changes will appear after the first skill set update.")}
    `;
    return;
  }

  const fileItems = diff.changed_files
    .map((f) => {
      const added = (f.added || []).length;
      const removed = (f.removed || []).length;
      return `<div class="rule-change-item">
      <span class="rule-change-file">${escapeHTML(baseName(f.path))}</span>
      <span>
        ${added ? `<span class="rule-change-count added">+${added}</span>` : ""}
        ${removed ? `<span class="rule-change-count removed"> -${removed}</span>` : ""}
      </span>
    </div>`;
    })
    .join("");

  el.innerHTML = `
    <div class="metric-card-header">Latest Changes</div>
    <div style="font-size:12px;color:var(--muted);margin-bottom:10px">${escapeHTML(diff.from_version || "?")} \u2192 ${escapeHTML(diff.to_version || "?")}</div>
    <div class="rule-change-list">${fileItems}</div>
  `;
}

/* ── NEW: Recent activity card ── */

function renderRecentActivity(skillSet) {
  const el = $("recentActivityCard");
  const history = (skillSet?.deployment_history || []).slice(0, 5);

  if (history.length === 0) {
    el.innerHTML = `
      <div class="bottom-card-header">Recent Activity</div>
      ${emptyState("No activity", "Deployment events will appear here.")}
    `;
    return;
  }

  const items = history
    .map(
      (evt) => `
    <div class="activity-item">
      <div class="activity-dot"></div>
      <div>
        <div class="activity-text">${escapeHTML(skillSetDeploymentSummary(evt))}</div>
        <div class="activity-time">${formatDateTime(evt.occurred_at)}</div>
      </div>
    </div>
  `,
    )
    .join("");

  el.innerHTML = `
    <div class="bottom-card-header">Recent Activity</div>
    <div class="activity-list">${items}</div>
  `;
}

/* ── NEW: Workspace members card ── */

function renderWorkspaceMembers() {
  const el = $("workspaceMembersCard");
  const user = readJSONStorage(STORAGE_KEYS.sessionUser);

  if (!user) {
    el.innerHTML = `
      <div class="bottom-card-header">Workspace Members</div>
      ${emptyState("No member info", "")}
    `;
    return;
  }

  const initial = (user.name || user.email || "?")[0].toUpperCase();

  el.innerHTML = `
    <div class="bottom-card-header">Workspace Members</div>
    <div class="member-item">
      <div class="member-avatar">${escapeHTML(initial)}</div>
      <div class="member-info">
        <div class="member-name">${escapeHTML(user.name || "Unknown")}</div>
        <div class="member-email">${escapeHTML(user.email || "")}</div>
      </div>
    </div>
  `;
}

/* ── NEW: CLI token card (compact, for bottom card) ── */

function renderCLITokenCard(items) {
  const el = $("cliTokenCard");
  const tokens = toArray(items);
  const active = tokens.filter(
    (t) => t.status === "active" || (!t.revoked_at && !t.consumed_at),
  );

  if (active.length === 0) {
    el.innerHTML = `
      <div class="bottom-card-header">CLI Tokens</div>
      ${emptyState("No active tokens", "Create a CLI token in Settings to connect your device.")}
    `;
    return;
  }

  const latest = active[0];
  const lastUsed = latest.last_used_at
    ? formatDateTime(latest.last_used_at)
    : "never";

  el.innerHTML = `
    <div class="bottom-card-header">CLI Tokens</div>
    <div style="font-size:14px;color:var(--ink);font-weight:600;margin-bottom:4px">${escapeHTML(latest.token_prefix)}***</div>
    <div style="font-size:12px;color:var(--muted)">Last used: ${escapeHTML(lastUsed)}</div>
    <div style="font-size:12px;color:var(--muted);margin-top:2px">${active.length} active token${active.length !== 1 ? "s" : ""}</div>
  `;
}

/* ── NEW: Sidebar footer ── */

function renderSidebar() {
  const user = readJSONStorage(STORAGE_KEYS.sessionUser);
  const el = $("sidebarFooter");
  if (!user) {
    el.innerHTML = "";
    return;
  }

  el.innerHTML = `
    <div class="sidebar-user">
      <div class="sidebar-user-name">${escapeHTML(user.name || "User")}</div>
      <div>${escapeHTML(user.email || "")}</div>
    </div>
  `;
}

/* ── NEW: Settings toggle ── */

function toggleSettings() {
  state.settingsOpen = !state.settingsOpen;
  const overlay = $("settingsOverlay");
  if (state.settingsOpen) {
    overlay.hidden = false;
  } else {
    overlay.hidden = true;
  }
}

/* ── Busy UI ── */

function syncBusyUI() {
  // Simple version - no tab or report panel UI to manage
}

/* ── Preferences ── */

function restorePreferences() {
  // Simplified - just check onboarding
  const done = readStorage(STORAGE_KEYS.onboardingDone);
  if (done === "1") {
    hideWizard();
  }
}

/* ── Actions ── */

async function withBusy(task) {
  if (state.busy) {
    return false;
  }

  state.busy = true;
  syncBusyUI();

  try {
    await task();
    return true;
  } finally {
    state.busy = false;
    syncBusyUI();
  }
}

async function issueCLIToken() {
  try {
    await withBusy(async () => {
      const setupCommand = buildSetupCommand();
      const data = await requestJSON(
        "/api/v1/auth/cli-tokens",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            label: "CLI setup token",
          }),
        },
        "Failed to issue a CLI token.",
      );

      $("issuedCliToken").textContent = data.token || "Token was issued.";
      $("cliTokenMeta").textContent = data.expires_at
        ? `CLI token issued for ${data.label || "CLI setup"} and expires ${formatDateTime(data.expires_at)}. Paste it into \`${setupCommand}\` on the machine you want to connect.`
        : `CLI token issued. Paste it into \`${setupCommand}\` on the machine you want to connect.`;

      const tokens = await requestJSON(
        "/api/v1/auth/cli-tokens",
        {},
        "Failed to refresh issued CLI tokens.",
      );
      renderCLITokens(toArray(tokens.items));
      setStatus(
        `CLI token issued. Paste it into \`${setupCommand}\` on the device you want to connect.`,
      );
    });
  } catch (error) {
    if (isUnauthorized(error)) {
      redirectToLanding("Your session expired. Sign in again.");
      return;
    }
    setStatus(
      error instanceof Error ? error.message : "Failed to issue a CLI token.",
      true,
    );
  }
}

async function issueOnboardingToken() {
  const btn = $("onbIssueTokenBtn");
  if (btn) btn.disabled = true;
  try {
    const data = await requestJSON(
      "/api/v1/auth/cli-tokens",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ label: "CLI setup token" }),
      },
      "Failed to issue a CLI token.",
    );
    const block = $("onbTokenBlock");
    const output = $("onbTokenOutput");
    if (block) block.hidden = false;
    if (output) output.textContent = data.token || "Token was issued.";
    setStatus(
      "CLI token issued. Copy it and run autoskills setup in your repo.",
    );
  } catch (error) {
    if (isUnauthorized(error)) {
      redirectToLanding("Your session expired. Sign in again.");
      return;
    }
    setStatus(
      error instanceof Error ? error.message : "Failed to issue a CLI token.",
      true,
    );
  } finally {
    if (btn) btn.disabled = false;
  }
}

async function revokeCLIToken(tokenID) {
  try {
    await withBusy(async () => {
      await requestJSON(
        "/api/v1/auth/cli-tokens/revoke",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ token_id: tokenID }),
        },
        "Failed to revoke the CLI token.",
      );

      const tokens = await requestJSON(
        "/api/v1/auth/cli-tokens",
        {},
        "Failed to refresh issued CLI tokens.",
      );
      renderCLITokens(toArray(tokens.items));
      setStatus(
        "CLI token revoked. That token can no longer authenticate a local CLI install.",
      );
    });
  } catch (error) {
    if (isUnauthorized(error)) {
      redirectToLanding("Your session expired. Sign in again.");
      return;
    }
    setStatus(
      error instanceof Error
        ? error.message
        : "Failed to revoke the CLI token.",
      true,
    );
  }
}

async function signOut() {
  try {
    await requestJSON(
      "/api/v1/auth/logout",
      {
        method: "POST",
      },
      "Failed to sign out.",
    );
  } catch (error) {
    if (!isUnauthorized(error)) {
      setStatus(
        error instanceof Error ? error.message : "Failed to sign out.",
        true,
      );
      return;
    }
  }

  clearSession();
  window.location.replace("/login");
}

/* ── Load ── */

async function load() {
  if (state.busy) return;
  state.busy = true;
  syncBusyUI();
  setStatus("Loading workspace...");

  try {
    const session = await ensureSession();
    if (!session) return;

    const orgID =
      state.session && state.session.organization
        ? state.session.organization.id
        : "";
    if (!orgID) {
      setStatus("No organization found.", true);
      return;
    }

    const [overviewResp, projectsResp, cliTokensResp] = await Promise.all([
      requestJSON(
        `/api/v1/dashboard/overview?org_id=${encodeURIComponent(orgID)}`,
      ),
      requestJSON(`/api/v1/projects?org_id=${encodeURIComponent(orgID)}`),
      requestJSON("/api/v1/auth/cli-tokens"),
    ]);

    const overview = overviewResp;
    state.overview = overview;
    const projects = toArray(projectsResp?.items);
    const cliTokens = toArray(cliTokensResp?.items);

    const project = projects.length > 0 ? projects[0] : null;
    state.selectedProjectID = project?.id || "";

    if (!project) {
      showWizard();
      return;
    }

    hideWizard();

    // Fetch skill set and token impact in parallel
    let skillSet = null;
    let tokenImpact = null;

    try {
      [skillSet, tokenImpact] = await Promise.all([
        requestJSON(
          `/api/v1/skill-sets/latest?project_id=${encodeURIComponent(project.id)}`,
        ).catch(() => null),
        requestJSON(
          `/api/v1/dashboard/token-impact?project_id=${encodeURIComponent(project.id)}`,
        ).catch(() => null),
      ]);
    } catch (_) {}

    state.skillSetBundle = skillSet;
    state.tokenImpact = tokenImpact;

    // Check if setup is still in progress
    const setupStage = deriveSetupStage(overview, skillSet);
    if (setupStage !== "ready") {
      renderSetupProgress(overview, skillSet, setupStage);
      renderConnectionStatus(project, overview);
      setStatus("Setup in progress...");
    } else {
      hideSetupProgress();
      // Render all sections
      renderConnectionStatus(project, overview);
      renderAutoSkillsHero(skillSet, []);
      renderTokenImpact(tokenImpact);
      renderDeployFrequency(skillSet);
      renderTopModifiedRules(skillSet);
      renderRecentActivity(skillSet);
      renderWorkspaceMembers();
      renderCLITokenCard(cliTokens);
      renderCLITokens(cliTokens);
      renderSidebar();

      setStatus(
        `Workspace loaded. ${overview.total_sessions || 0} sessions collected.`,
      );
    }

    // Auto-poll while import or research is active
    if (shouldProgressPoll(overview)) {
      startProgressPoll();
    } else {
      stopProgressPoll();
    }
  } catch (error) {
    if (isUnauthorized(error)) {
      redirectToLanding("Your session has expired. Please sign in again.");
      return;
    }
    console.error("load error:", error);
    setStatus("Failed to load dashboard: " + (error.message || error), true);
  } finally {
    state.busy = false;
    syncBusyUI();
  }
}

/* ── Event delegation ── */

function handleActionClick(event) {
  if (!(event.target instanceof Element)) {
    return;
  }

  const button = event.target.closest("[data-action]");
  if (!button || button.disabled) {
    return;
  }

  switch (button.dataset.action) {
    case "refresh-dashboard":
      load();
      break;
    case "issue-cli-token":
      issueCLIToken();
      break;
    case "onboarding-issue-token":
      issueOnboardingToken();
      break;
    case "revoke-cli-token":
      revokeCLIToken(button.dataset.tokenId || "");
      break;
    case "switch-skill-doc": {
      const idx = Number(button.dataset.skillDocIndex || 0);
      document.querySelectorAll(".skill-bundle-tab").forEach(function (tab) {
        tab.classList.toggle(
          "is-active",
          Number(tab.dataset.skillDocIndex || 0) === idx,
        );
      });
      document
        .querySelectorAll(".skill-bundle-panel")
        .forEach(function (panel) {
          const active = Number(panel.dataset.skillDocPanel || 0) === idx;
          panel.classList.toggle("is-active", active);
        });
      break;
    }
    case "copy-command":
      copyCommand(
        button.dataset.copyTarget || "",
        button.dataset.copyLabel || "command",
        button,
      );
      break;
    case "sign-out":
      signOut();
      break;
    case "close-full-text":
      closeFullTextModal();
      break;
    case "toggle-settings":
      toggleSettings();
      break;
    default:
      break;
  }
}

document.addEventListener("click", handleActionClick);

$("fullTextOverlay").addEventListener("click", (event) => {
  if (event.target === $("fullTextOverlay")) {
    closeFullTextModal();
  }
});

document.addEventListener("keydown", (event) => {
  if (
    (event.key === " " || event.key === "Enter") &&
    event.target.closest(".section-toggle")
  ) {
    event.preventDefault();
    event.target.closest(".section-toggle").click();
  }
  if (event.key === "Escape" && !$("fullTextOverlay").hidden) {
    closeFullTextModal();
  }
  if (event.key === "Escape" && state.settingsOpen) {
    toggleSettings();
  }
});

/* ── Boot ── */

async function boot() {
  restorePreferences();
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState !== "visible") {
      return;
    }
    const inline = $("onboardingInline");
    if ((inline && !inline.hidden) || progressPollTimer != null) {
      load();
    }
  });
  await load();
}

boot();
