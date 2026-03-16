const STORAGE_KEYS = {
  sessionUser: "crux_session_user",
  sessionOrg: "crux_session_org",
  activeTab: "crux_dashboard_tab",
  activeReportPanel: "crux_dashboard_report_panel",
  activeReportID: "crux_dashboard_report_id",
  onboardingDone: "crux_onboarding_done",
};
const TAB_IDS = ["overview", "trends", "sessions", "cli"];
const REPORT_PANEL_IDS = ["actions", "history", "all"];
const WIZARD_STEPS = 4;
const DEFAULT_SERVER_ORIGIN = "https://cruxai.ai";

const state = {
  busy: false,
  activeTab: "overview",
  activeReportPanel: "actions",
  activeReportID: "",
  failedImportJobItems: [],
  importJobItems: [],
  selectedProjectID: "",
  reportIndex: new Map(),
  reportItems: [],
  sessionItemsFull: [],
  session: null,
  wizardStep: 0,
  sessionItems: [],
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

function showWizard() {
  $("onboardingWizard").classList.remove("is-hidden");
  $("mainDashboard").classList.remove("is-visible");
  setWizardStep(0);
}

function hideWizard() {
  $("onboardingWizard").classList.add("is-hidden");
  $("mainDashboard").classList.add("is-visible");
  writeStorage(STORAGE_KEYS.onboardingDone, "1");
}

function setWizardStep(step) {
  state.wizardStep = Math.max(0, Math.min(step, WIZARD_STEPS - 1));
  const dots = $("wizardProgress").children;
  const steps = document.querySelectorAll("[data-wizard-step]");

  for (let i = 0; i < dots.length; i++) {
    dots[i].classList.toggle("is-done", i < state.wizardStep);
    dots[i].classList.toggle("is-active", i === state.wizardStep);
  }

  steps.forEach((el) => {
    el.classList.toggle(
      "is-active",
      Number(el.dataset.wizardStep) === state.wizardStep,
    );
  });
}

function normalizeOrigin(value) {
  return String(value == null ? "" : value).trim().replace(/\/+$/, "");
}

function buildSetupCommand(origin = window.location.origin || "") {
  const normalizedOrigin = normalizeOrigin(origin);
  if (!normalizedOrigin || normalizedOrigin === DEFAULT_SERVER_ORIGIN) {
    return "crux setup";
  }
  return `crux setup --server ${normalizedOrigin}`;
}

function updateWizardCommands() {
  const setupCommand = buildSetupCommand();
  const wizSetup = $("wizSetupCmd");
  if (wizSetup) {
    wizSetup.textContent = setupCommand;
  }
}

/* ── Tabs ── */

function setActiveTab(nextTab) {
  const tab = TAB_IDS.includes(nextTab) ? nextTab : "overview";
  state.activeTab = tab;
  writeStorage(STORAGE_KEYS.activeTab, tab);

  document.querySelectorAll("[data-tab]").forEach((button) => {
    const active = button.dataset.tab === tab;
    button.classList.toggle("is-active", active);
    button.setAttribute("aria-pressed", active ? "true" : "false");
  });

  document.querySelectorAll("[data-tab-panel]").forEach((panel) => {
    const active = panel.dataset.tabPanel === tab;
    panel.classList.toggle("is-active", active);
    panel.hidden = !active;
  });
}

function setActiveReportPanel(nextPanel) {
  const panel = REPORT_PANEL_IDS.includes(nextPanel) ? nextPanel : "actions";
  state.activeReportPanel = panel;
  writeStorage(STORAGE_KEYS.activeReportPanel, panel);

  document.querySelectorAll("[data-report-panel]").forEach((section) => {
    const active = section.dataset.reportPanel === panel;
    section.classList.toggle("is-active", active);
    section.hidden = !active;
  });
  syncReportPanelToggle();
}

function syncReportPanelToggle() {
  const button = $("reportPanelToggleBtn");
  const allBtn = $("reportPanelAllBtn");
  if (!button) {
    return;
  }
  const current = state.activeReportPanel;
  const viewingHistory = current === "history";
  button.textContent = viewingHistory ? "Summary" : "History";
  button.dataset.targetPanel = viewingHistory ? "actions" : "history";
  button.setAttribute(
    "aria-label",
    viewingHistory ? "Go back to report summary" : "Open report history",
  );
  if (allBtn) {
    allBtn.classList.toggle("is-active", current === "all");
  }
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
  [
    STORAGE_KEYS.sessionUser,
    STORAGE_KEYS.sessionOrg,
    STORAGE_KEYS.activeTab,
    STORAGE_KEYS.activeReportPanel,
    STORAGE_KEYS.activeReportID,
  ].forEach((key) => writeStorage(key, ""));
  state.session = null;
  state.selectedProjectID = "";
  state.activeReportID = "";
  state.reportItems = [];
  state.sessionItemsFull = [];
}

function renderSessionContext() {
  const session = state.session || {
    user: readJSONStorage(STORAGE_KEYS.sessionUser, {}) || {},
    organization: readJSONStorage(STORAGE_KEYS.sessionOrg, {}) || {},
  };
  const user = session.user || {};
  const org = session.organization || {};

  $("topBarUser").textContent = user.name || user.email || "-";
  $("topBarOrg").textContent = org.name || org.id || "-";
  const adminLink = $("adminLink");
  if (adminLink) {
    adminLink.hidden = String(user.role || "").toLowerCase() !== "admin";
  }
}

function renderAgentStatus(overview, reports) {
  const bar = $("agentStatusBar");
  const text = $("agentStatusText");
  const activeReports = Number(overview.active_reports || 0);
  const totalSessions = Number(overview.total_sessions || 0);
  const research = overview && overview.research_status;
  const importJob = overview && overview.active_import_job;
  const importState = String((importJob && importJob.status) || "")
    .trim()
    .toLowerCase();
  const researchState = String((research && research.state) || "")
    .trim()
    .toLowerCase();
  if (importState === "running" || importState === "queued" || importState === "receiving_chunks") {
    bar.dataset.state = "";
    text.textContent =
      sessionImportNarrative(importJob) ||
      "A backfill import is running on uploaded Codex sessions";
  } else if (researchState === "running") {
    bar.dataset.state = "report";
    text.textContent =
      reportResearchNarrative(research) ||
      "Analyzing Codex traces to build the next analysis report";
  } else if (activeReports > 0) {
    bar.dataset.state = "report";
    text.textContent = `Feedback ready \u2014 ${activeReports} report${activeReports > 1 ? "s" : ""} available to review`;
  } else if (
    researchState === "waiting_for_min_sessions" ||
    researchState === "disabled" ||
    researchState === "failed" ||
    researchState === "no_reports"
  ) {
    bar.dataset.state = "";
    text.textContent =
      reportResearchNarrative(research) ||
      `Observing \u2014 analyzed ${totalSessions} session${totalSessions > 1 ? "s" : ""}`;
  } else if (totalSessions === 0) {
    bar.dataset.state = "";
    text.textContent =
      "Waiting for sessions \u2014 run `crux setup` to register the repo and upload local Codex sessions";
  } else {
    bar.dataset.state = "";
    text.textContent = `Observing \u2014 analyzed ${totalSessions} session${totalSessions > 1 ? "s" : ""}, researching usage patterns`;
  }
}

function syncBusyUI() {
  const busy = state.busy;
  const loadBtn = $("loadBtn");
  const issueTokenBtn = $("issueTokenBtn");

  loadBtn.disabled = busy;
  loadBtn.textContent = busy ? "Refreshing..." : "Refresh";
  issueTokenBtn.disabled = busy;
  issueTokenBtn.textContent = busy ? "Creating token..." : "Create CLI token";

  document.querySelectorAll("button[data-action]").forEach((button) => {
    button.disabled = busy;
  });
}

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

function restorePreferences() {
  state.activeTab = readStorage(STORAGE_KEYS.activeTab, "overview");
  state.activeReportPanel = readStorage(
    STORAGE_KEYS.activeReportPanel,
    "actions",
  );
  state.activeReportID = readStorage(STORAGE_KEYS.activeReportID, "");
  state.session = {
    user: readJSONStorage(STORAGE_KEYS.sessionUser, {}) || {},
    organization: readJSONStorage(STORAGE_KEYS.sessionOrg, {}) || {},
  };
  renderSessionContext();
}

function isUnauthorized(error) {
  return Boolean(error && typeof error === "object" && error.status === 401);
}

function redirectToLanding(message) {
  clearSession();
  if (message) {
    try {
      window.sessionStorage.setItem("crux_redirect_notice", message);
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

async function copyCommand(targetID, label) {
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

  setStatus(`Copied the ${label || "command"}.`);
}

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

function formatCompactCount(value) {
  return new Intl.NumberFormat("en-US", {
    notation: "compact",
    maximumFractionDigits: 1,
  }).format(Number(value || 0));
}

function formatPercent(value) {
  return `${Math.round(Number(value || 0) * 100)}%`;
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

function riskTone(risk) {
  const raw = String(risk || "").toLowerCase();
  if (raw.includes("high")) {
    return "danger";
  }
  if (raw.includes("medium")) {
    return "warn";
  }
  return "good";
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

function impactDeltaTone(value) {
  const numeric = Number(value || 0);
  if (numeric < 0) {
    return "good";
  }
  if (numeric > 0) {
    return "warn";
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

function extractUserRequest(raw) {
  let text = String(raw || "").trim();
  if (!text) {
    return "";
  }

  const requestMarkers = [
    "## My request for Codex:",
    "## My request for Codex",
    "My request for Codex:",
  ];
  for (const marker of requestMarkers) {
    if (text.includes(marker)) {
      return normalizeInlineText(
        text.slice(text.lastIndexOf(marker) + marker.length),
      );
    }
  }

  text = text
    .replace(/<environment_context>[\s\S]*?<\/environment_context>/gi, " ")
    .replace(/# AGENTS\.md instructions[\s\S]*?<\/INSTRUCTIONS>/gi, " ");

  const lines = text.replace(/\r\n/g, "\n").split("\n");
  const cleaned = [];
  let skipInstructions = false;
  let skipOpenTabs = false;

  for (const rawLine of lines) {
    const line = rawLine.trim();
    if (!line) {
      continue;
    }
    if (/^# AGENTS\.md instructions/i.test(line)) {
      skipInstructions = true;
      continue;
    }
    if (skipInstructions) {
      if (/^<\/INSTRUCTIONS>$/i.test(line)) {
        skipInstructions = false;
      }
      continue;
    }
    if (/^# Context from my IDE setup:?$/i.test(line)) {
      continue;
    }
    if (/^## Open tabs:?$/i.test(line)) {
      skipOpenTabs = true;
      continue;
    }
    if (/^## My request for Codex:?$/i.test(line)) {
      skipOpenTabs = false;
      continue;
    }
    if (skipOpenTabs) {
      if (/^##\s+/.test(line)) {
        skipOpenTabs = false;
      } else {
        continue;
      }
    }
    if (/^<image>$/i.test(line) || /^<\/image>$/i.test(line)) {
      continue;
    }
    cleaned.push(line);
  }

  return normalizeInlineText(cleaned.join(" "));
}

function sessionTotalTokens(item) {
  return Number(item.token_in || 0) + Number(item.token_out || 0);
}

function sessionPrimaryRequest(item) {
  const queries = toArray(item.raw_queries);
  for (const query of queries) {
    const request = extractUserRequest(query);
    if (request) {
      return request;
    }
  }
  return "";
}

function sessionFollowUps(item) {
  const queries = toArray(item.raw_queries);
  const normalized = queries
    .map((query) => extractUserRequest(query) || normalizeInlineText(query))
    .filter(Boolean);
  if (normalized.length <= 1) {
    return [];
  }
  return normalized.slice(1);
}

function stepSummary(step) {
  if (step && step.summary) {
    return String(step.summary);
  }
  if (step && step.operation && String(step.operation).includes("append")) {
    return `Add guidance to ${baseName(step.file_path)}`;
  }
  if (step && step.target_file) {
    return `Update ${baseName(step.target_file)}`;
  }
  if (step && step.file_path) {
    return `Update ${baseName(step.file_path)}`;
  }
  return "Update workspace configuration";
}

function reportSummaryLine(item) {
  const frictions = toArray(item.frictions).filter(Boolean);
  const nextSteps = toArray(item.next_steps).filter(Boolean);
  if (frictions.length > 0) {
    return frictions[0];
  }
  if (nextSteps.length > 0) {
    return nextSteps[0];
  }
  return "A trace analysis report is ready to review.";
}

function latestSessionRequestSummary(items) {
  for (const item of toArray(items)) {
    const request = sessionPrimaryRequest(item);
    if (request) {
      return request;
    }
  }
  return "";
}

function latestReasoningTraceSummary(items) {
  for (const item of toArray(items)) {
    const summaries = sessionFullReasoningSummaries(item);
    if (summaries.length > 0) {
      return truncateText(summaries.slice(-2).join(" "), 320);
    }
  }
  return "";
}

function resolveActiveReport(reports) {
  const items = toArray(reports);
  if (!items.length) {
    return null;
  }
  const selected = items.find(
    (item) => String(item && item.id ? item.id : "") === state.activeReportID,
  );
  return selected || items[0];
}

function setActiveReportID(reportID) {
  state.activeReportID = String(reportID || "");
  writeStorage(STORAGE_KEYS.activeReportID, state.activeReportID);
}

function rawReportOutputBlock(rawOutput) {
  const text = String(rawOutput || "").trim();
  if (!text) {
    return "";
  }
  return `
        <details class="report-raw-output">
          <summary>LLM raw output</summary>
          <pre>${escapeHTML(text)}</pre>
        </details>
      `;
}

function reportDetailsBlock(item) {
  const sections = [];

  const userIntent = String(item.user_intent || "").trim();
  const modelInterpretation = String(item.model_interpretation || "").trim();
  const reason = String(item.reason || "").trim();
  const explanation = String(item.explanation || "").trim();
  const expectedBenefit = String(item.expected_benefit || "").trim();
  const risk = String(item.risk || "").trim();
  const expectedImpact = String(item.expected_impact || "").trim();
  const confidence = String(item.confidence || "").trim();
  const score = Number(item.score || 0);
  const evidence = toArray(item.evidence).filter(Boolean);
  const strengths = toArray(item.strengths).filter(Boolean);
  const frictions = toArray(item.frictions).filter(Boolean);
  const nextSteps = toArray(item.next_steps).filter(Boolean);
  const rawOutput = String(item.raw_suggestion || "").trim();

  const hasContent =
    userIntent ||
    modelInterpretation ||
    reason ||
    explanation ||
    risk ||
    expectedBenefit ||
    expectedImpact ||
    evidence.length ||
    strengths.length ||
    frictions.length ||
    nextSteps.length;
  if (!hasContent && !rawOutput) {
    return "";
  }

  const metricsHTML = [];
  if (score > 0) {
    const pct = Math.round(score * 100);
    metricsHTML.push(`<span class="report-metric">Score <span class="report-score-track"><span class="report-score-fill" style="width:${pct}%"></span></span> ${pct}%</span>`);
  }
  if (confidence) {
    metricsHTML.push(`<span class="report-metric">Confidence: ${escapeHTML(titleize(confidence))}</span>`);
  }
  if (expectedImpact) {
    metricsHTML.push(`<span class="report-metric">${escapeHTML(expectedImpact)}</span>`);
  }
  if (metricsHTML.length) {
    sections.push(`<div class="report-metrics-row">${metricsHTML.join("")}</div>`);
  }

  if (userIntent || modelInterpretation) {
    const intentCard = userIntent
      ? `<div class="report-comparison-card report-comparison-card--intent">
          <div class="report-comparison-label">What you intended</div>
          <div class="report-comparison-text">${escapeHTML(userIntent)}</div>
        </div>`
      : "";
    const modelCard = modelInterpretation
      ? `<div class="report-comparison-card report-comparison-card--model">
          <div class="report-comparison-label">What the model understood</div>
          <div class="report-comparison-text">${escapeHTML(modelInterpretation)}</div>
        </div>`
      : "";
    sections.push(`<div class="report-comparison">${intentCard}${modelCard}</div>`);
  }

  const analysisText = [reason, explanation].filter(Boolean).join(" ");
  if (analysisText) {
    sections.push(`<div class="report-analysis-section">
      <div class="report-analysis-label">Why this happened</div>
      <div class="report-analysis-text">${escapeHTML(analysisText)}</div>
    </div>`);
  }

  if (expectedBenefit || risk) {
    const combined = [expectedBenefit, risk].filter(Boolean).join(" &mdash; ");
    sections.push(`<div class="report-analysis-section">
      <div class="report-analysis-label">What to expect</div>
      <div class="report-analysis-text">${combined}</div>
    </div>`);
  }

  if (strengths.length) {
    const items = strengths
      .map((entry) => `<div class="report-evidence-item report-evidence-item--good">${escapeHTML(entry)}</div>`)
      .join("");
    sections.push(`<div class="report-list-section"><div class="report-list-label report-list-label--good">What worked well</div><div class="report-evidence-list">${items}</div></div>`);
  }

  if (frictions.length) {
    const items = frictions
      .map((entry) => `<div class="report-evidence-item report-evidence-item--warn">${escapeHTML(entry)}</div>`)
      .join("");
    sections.push(`<div class="report-list-section"><div class="report-list-label report-list-label--warn">Where it went wrong</div><div class="report-evidence-list">${items}</div></div>`);
  }

  if (nextSteps.length) {
    const items = nextSteps
      .map((entry) => `<div class="report-evidence-item report-evidence-item--next">${escapeHTML(entry)}</div>`)
      .join("");
    sections.push(`<div class="report-list-section"><div class="report-list-label report-list-label--next">What to try next</div><div class="report-evidence-list">${items}</div></div>`);
  }

  if (evidence.length) {
    const evidenceItems = evidence
      .map((e) => `<div class="report-evidence-item report-evidence-item--evidence">${escapeHTML(e)}</div>`)
      .join("");
    sections.push(`<div class="report-list-section"><div class="report-list-label report-list-label--evidence">Evidence from sessions (${evidence.length})</div><div class="report-evidence-list">${evidenceItems}</div></div>`);
  }

  if (rawOutput) {
    sections.push(rawReportOutputBlock(rawOutput));
  }

  if (!sections.length) {
    return "";
  }

  return `
    <div class="report-detail">
      <button class="report-detail-toggle" type="button" data-action="toggle-report-detail"><span class="toggle-icon">&#9654;</span> View full analysis</button>
      <div class="report-detail-body">${sections.join("")}</div>
    </div>
  `;
}

function reportKindLabel(kind) {
  const value = String(kind || "").toLowerCase();
  if (value.includes("instruction")) {
    return "Prompt pattern";
  }
  if (value.includes("skill")) {
    return "Skill usage";
  }
  if (value.includes("mcp")) {
    return "Tooling signal";
  }
  if (value.includes("config")) {
    return "Config signal";
  }
  return "Report";
}

function reportKindTone(kind) {
  const value = String(kind || "").toLowerCase();
  if (value.includes("instruction")) {
    return "warn";
  }
  if (value.includes("skill")) {
    return "good";
  }
  if (value.includes("mcp")) {
    return "sky";
  }
  if (value.includes("config")) {
    return "good";
  }
  return "sky";
}

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

function workloadNarrative(overview) {
  const action = String(overview.action_summary || "").trim();
  const outcome = String(overview.outcome_summary || "").trim();
  const research = reportResearchNarrative(
    overview && overview.research_status,
  );
  const importProgress = sessionImportNarrative(
    overview && overview.active_import_job,
  );
  const importMetrics = sessionImportMetricsNarrative(
    overview && overview.import_job_metrics,
  );
  const input = Number(overview.avg_input_tokens_per_query || 0);
  const output = Number(overview.avg_output_tokens_per_query || 0);
  const tokenRead =
    input > 0 || output > 0
      ? input >= output
        ? " Prompt-side token usage is currently the larger share of each captured query."
        : " Response-side token usage is currently the larger share of each captured query."
      : "";
  const combined = `${importProgress} ${importMetrics} ${action} ${outcome} ${research}${tokenRead}`.trim();
  return (
    combined ||
    "Crux is collecting enough Codex session traces to produce its first analysis report."
  );
}

function sessionImportNarrative(job) {
  const item = job || {};
  const state = String(item.status || "").trim().toLowerCase();
  if (!state) {
    return "";
  }
  const processed = Number(item.processed_sessions || 0);
  const received = Number(item.received_sessions || 0);
  const total = Number(item.total_sessions || 0);
  const target = total > 0 ? total : received;
  if (state === "receiving_chunks") {
    return `A backfill import is staging ${formatCount(received)} of ${formatCount(target)} sessions before processing starts.`;
  }
  if (state === "queued") {
    return `A backfill import is queued with ${formatCount(received)} staged session${received === 1 ? "" : "s"}.`;
  }
  if (state === "running") {
    return `A backfill import is processing ${formatCount(processed)} of ${formatCount(target)} staged sessions right now.`;
  }
  return "";
}

function sessionImportMetricsNarrative(metrics) {
  const item = metrics || {};
  const created = Number(item.created_jobs || 0);
  if (created <= 0) {
    return "";
  }
  const active =
    Number(item.receiving_jobs || 0) +
    Number(item.queued_jobs || 0) +
    Number(item.running_jobs || 0);
  const succeeded = Number(item.succeeded_jobs || 0);
  const degraded =
    Number(item.partially_failed_jobs || 0) +
    Number(item.failed_jobs || 0) +
    Number(item.canceled_jobs || 0);
  const throughput = Number(item.throughput_per_minute || 0);
  const failureRate = Number(item.failure_rate || 0);
  const avgDurationMS = Number(item.avg_duration_ms || 0);

  const parts = [`Import history shows ${formatCount(created)} job${created === 1 ? "" : "s"} created`];
  if (active > 0) {
    parts.push(`${formatCount(active)} still active`);
  }
  if (succeeded > 0 || degraded > 0) {
    parts.push(`${formatCount(succeeded)} succeeded`);
    if (degraded > 0) {
      parts.push(`${formatCount(degraded)} degraded`);
    }
  }
  if (throughput > 0) {
    parts.push(`${formatCount(Math.round(throughput))} sessions per minute`);
  }
  if (failureRate > 0) {
    parts.push(`${Math.round(failureRate * 100)}% session failure rate`);
  }
  if (avgDurationMS > 0) {
    parts.push(`${formatLatency(avgDurationMS)} average run time`);
  }
  return `${parts.join(", ")}.`;
}

function importJobTone(status) {
  const raw = String(status || "").trim().toLowerCase();
  if (raw === "succeeded") {
    return "good";
  }
  if (raw === "partially_failed" || raw === "queued") {
    return "warn";
  }
  if (raw === "failed" || raw === "canceled") {
    return "danger";
  }
  return "sky";
}

function importJobProgressSummary(item) {
  const status = String(item.status || "").trim().toLowerCase();
  const total = Number(item.total_sessions || 0);
  const received = Number(item.received_sessions || 0);
  const processed = Number(item.processed_sessions || 0);
  const uploaded =
    Number(item.uploaded_sessions || 0) + Number(item.updated_sessions || 0);
  const failed = Number(item.failed_sessions || 0);
  const target = Math.max(total, received);

  if (status === "receiving_chunks") {
    return `${formatCount(received)} of ${formatCount(target)} sessions staged.`;
  }
  if (status === "queued") {
    return `${formatCount(received)} staged session${received === 1 ? "" : "s"} waiting to run.`;
  }
  if (status === "running") {
    return `${formatCount(processed)} of ${formatCount(target)} sessions processed so far.`;
  }
  if (status === "succeeded") {
    return `${formatCount(uploaded)} of ${formatCount(target || uploaded)} sessions uploaded successfully.`;
  }
  if (status === "partially_failed") {
    return `${formatCount(processed || target)} processed, ${formatCount(uploaded)} uploaded, ${formatCount(failed)} failed.`;
  }
  if (status === "failed") {
    return `${formatCount(processed)} of ${formatCount(target)} sessions processed before the job failed.`;
  }
  if (status === "canceled") {
    return `${formatCount(processed)} of ${formatCount(target)} sessions processed before cancellation.`;
  }
  return `${formatCount(processed)} of ${formatCount(target)} sessions processed.`;
}

function importJobFailureDetails(item) {
  const failures = toArray(item.failures);
  const latest = failures.length ? failures[failures.length - 1] : null;
  if (latest) {
    const parts = [];
    if (latest.session_id) {
      parts.push(latest.session_id);
    }
    if (latest.error) {
      parts.push(latest.error);
    }
    if (Number(latest.http_status || 0) > 0) {
      parts.push(`HTTP ${Number(latest.http_status)}`);
    }
    if (Number(latest.api_error_code || 0) > 0) {
      parts.push(`API ${Number(latest.api_error_code)}`);
    }
    if (parts.length) {
      return parts.join(" - ");
    }
  }
  return String(item.last_error || "").trim();
}

function importJobFailureLine(item) {
  const parts = [];
  if (item && item.session_id) {
    parts.push(item.session_id);
  }
  if (item && item.error) {
    parts.push(item.error);
  }
  if (Number((item && item.http_status) || 0) > 0) {
    parts.push(`HTTP ${Number(item.http_status)}`);
  }
  if (Number((item && item.api_error_code) || 0) > 0) {
    parts.push(`API ${Number(item.api_error_code)}`);
  }
  return parts.join(" - ");
}

function canCancelImportJob(status) {
  const raw = String(status || "").trim().toLowerCase();
  return raw === "receiving_chunks" || raw === "queued" || raw === "running";
}

function reportResearchNarrative(status) {
  const item = status || {};
  const state = String(item.state || "").trim().toLowerCase();
  const summary = String(item.summary || "").trim();
  if (summary) {
    return summary;
  }
  if (state === "waiting_for_min_sessions") {
    return `Feedback analysis starts after ${formatCount(item.minimum_sessions || 0)} sessions.`;
  }
  if (state === "disabled") {
    return "OpenAI-backed feedback analysis is disabled on this server.";
  }
  if (state === "running") {
    return "Analyzing Codex session traces. The next report will be ready soon.";
  }
  if (
    state === "succeeded" ||
    state === "no_reports"
  ) {
    const duration = Number(item.last_duration_ms || 0);
    return duration > 0
      ? `The last report refresh took ${formatLatency(duration)}.`
      : "The last report refresh finished recently.";
  }
  if (state === "failed") {
    return "The last report refresh failed.";
  }
  return "";
}

function sessionTone(item) {
  const queries = toArray(item.raw_queries).length;
  const tokens = Number(item.token_in || 0) + Number(item.token_out || 0);
  if (queries >= 3 && tokens >= 6000) {
    return "warn";
  }
  if (queries > 0) {
    return "good";
  }
  return "sky";
}

function sessionLabel(item) {
  const tone = sessionTone(item);
  if (tone === "good") {
    return "Captured";
  }
  if (tone === "warn") {
    return "Heavy prompt setup";
  }
  return "Small sample";
}

function sessionModelSummary(item) {
  const models = toArray(item.models)
    .map((value) => String(value || "").trim())
    .filter(Boolean);
  if (!models.length) {
    return "";
  }
  if (models.length === 1) {
    return models[0];
  }
  return `${models[0]} +${models.length - 1}`;
}

function sessionProviderSummary(item) {
  return String(item.model_provider || "").trim();
}

function sessionEngineSummary(item) {
  const parts = [];
  const model = sessionModelSummary(item);
  const provider = sessionProviderSummary(item);
  if (model) {
    parts.push(model);
  }
  if (provider) {
    parts.push(titleize(provider));
  }
  return parts.join(" · ");
}

function sessionLatencySummary(item) {
  const latency = Number(item.first_response_latency_ms || 0);
  if (latency <= 0) {
    return "";
  }
  return formatLatency(latency);
}

function sessionDurationSummary(item) {
  const duration = Number(item.session_duration_ms || 0);
  if (duration <= 0) {
    return "";
  }
  return formatLatency(duration);
}

function sessionToolSummary(item) {
  const functionCalls = Number(item.function_call_count || 0);
  const toolErrors = Number(item.tool_error_count || 0);
  if (functionCalls <= 0 && toolErrors <= 0) {
    return "";
  }
  const parts = [];
  if (functionCalls > 0) {
    parts.push(
      `${formatCount(functionCalls)} tool call${functionCalls === 1 ? "" : "s"}`,
    );
  }
  if (toolErrors > 0) {
    parts.push(
      `${formatCount(toolErrors)} error${toolErrors === 1 ? "" : "s"}`,
    );
  }
  return parts.join(" · ");
}

function sessionToolMixSummary(item) {
  const toolCalls =
    item && typeof item.tool_calls === "object" && item.tool_calls
      ? item.tool_calls
      : {};
  const rows = Object.entries(toolCalls)
    .map(([tool, count]) => [String(tool || "").trim(), Number(count || 0)])
    .filter(([tool, count]) => tool && count > 0)
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  if (!rows.length) {
    return "";
  }
  return rows
    .slice(0, 3)
    .map(([tool, count]) => `${tool} ${formatCount(count)}`)
    .join(" · ");
}

function sessionToolErrorMixSummary(item) {
  const toolErrors =
    item && typeof item.tool_errors === "object" && item.tool_errors
      ? item.tool_errors
      : {};
  const rows = Object.entries(toolErrors)
    .map(([tool, count]) => [String(tool || "").trim(), Number(count || 0)])
    .filter(([tool, count]) => tool && count > 0)
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  if (!rows.length) {
    return "";
  }
  return rows
    .slice(0, 3)
    .map(([tool, count]) => `${tool} ${formatCount(count)}`)
    .join(" · ");
}

function sessionToolWallTimeSummary(item) {
  const toolWallTimes =
    item &&
    typeof item.tool_wall_times_ms === "object" &&
    item.tool_wall_times_ms
      ? item.tool_wall_times_ms
      : {};
  const rows = Object.entries(toolWallTimes)
    .map(([tool, value]) => [String(tool || "").trim(), Number(value || 0)])
    .filter(([tool, value]) => tool && value > 0)
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  if (!rows.length) {
    return "";
  }
  return rows
    .slice(0, 3)
    .map(([tool, value]) => `${tool} ${formatLatency(value)}`)
    .join(" · ");
}

function sessionLatestReply(item) {
  const responses = toArray(item.assistant_responses)
    .map((value) => normalizeInlineText(value))
    .filter(Boolean);
  if (!responses.length) {
    return "";
  }
  return truncateText(responses[responses.length - 1], 220);
}

function sessionFullPrompts(item) {
  return toArray(item.raw_queries)
    .map((q) => extractUserRequest(q) || normalizeInlineText(q))
    .filter(Boolean);
}

function sessionFullResponses(item) {
  return toArray(item.assistant_responses)
    .map((v) => normalizeInlineText(v))
    .filter(Boolean);
}

function sessionFullReasoningSummaries(item) {
  return toArray(item.reasoning_summaries)
    .map((v) => normalizeInlineText(v))
    .filter(Boolean);
}

function isPromptTruncated(item) {
  const primary = sessionPrimaryRequest(item);
  return primary.length > 84 || toArray(item.raw_queries).length > 1;
}

function isResponseTruncated(item) {
  const responses = toArray(item.assistant_responses)
    .map((v) => normalizeInlineText(v))
    .filter(Boolean);
  if (!responses.length) {
    return false;
  }
  return responses.some((r) => r.length > 220) || responses.length > 1;
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

function showFullPrompt(sessionIndex) {
  const item = state.sessionItems[sessionIndex];
  if (!item) {
    return;
  }
  const prompts = sessionFullPrompts(item);
  const bodyHTML = prompts
    .map((text, i) => {
      const label =
        prompts.length === 1
          ? "User prompt"
          : `Prompt ${i + 1} of ${prompts.length}`;
      return `<span class="full-text-section-label">${escapeHTML(label)}</span><div class="full-text-block">${escapeHTML(text)}</div>`;
    })
    .join("");
  openFullTextModal(
    "Full prompt",
    bodyHTML || "<em>No prompt text captured.</em>",
  );
}

function showFullResponse(sessionIndex) {
  const item = state.sessionItems[sessionIndex];
  if (!item) {
    return;
  }
  const responses = sessionFullResponses(item);
  const bodyHTML = responses
    .map((text, i) => {
      const label =
        responses.length === 1
          ? "Assistant response"
          : `Response ${i + 1} of ${responses.length}`;
      return `<span class="full-text-section-label">${escapeHTML(label)}</span><div class="full-text-block">${escapeHTML(text)}</div>`;
    })
    .join("");
  openFullTextModal(
    "Full response",
    bodyHTML || "<em>No response text captured.</em>",
  );
}

function showFullReasoning(sessionIndex) {
  const item = state.sessionItems[sessionIndex];
  if (!item) {
    return;
  }
  const summaries = sessionFullReasoningSummaries(item);
  const bodyHTML = summaries
    .map((text, i) => {
      const label =
        summaries.length === 1
          ? "Reasoning summary"
          : `Reasoning summary ${i + 1} of ${summaries.length}`;
      return `<span class="full-text-section-label">${escapeHTML(label)}</span><div class="full-text-block">${escapeHTML(text)}</div>`;
    })
    .join("");
  openFullTextModal(
    "Reasoning summaries",
    `<p><em>These are summary-level reasoning hints captured from local Codex logs. Full reasoning traces are not available here.</em></p>${bodyHTML || "<em>No reasoning summaries captured.</em>"}`,
  );
}

function sessionSummaryLines(item) {
  const queries = toArray(item.raw_queries);
  const engineSummary = sessionEngineSummary(item);
  const latestReply = sessionLatestReply(item);
  const reasoningSummaries = sessionFullReasoningSummaries(item);
  const followUps = sessionFollowUps(item);
  const inputTokens = Number(item.token_in || 0);
  const outputTokens = Number(item.token_out || 0);
  const cachedInputTokens = Number(item.cached_input_tokens || 0);
  const reasoningOutputTokens = Number(item.reasoning_output_tokens || 0);
  const functionCalls = Number(item.function_call_count || 0);
  const toolErrors = Number(item.tool_error_count || 0);
  const totalTokens = inputTokens + outputTokens;
  const latencySummary = sessionLatencySummary(item);
  const durationSummary = sessionDurationSummary(item);
  const toolMixSummary = sessionToolMixSummary(item);
  const toolErrorMixSummary = sessionToolErrorMixSummary(item);
  const toolWallTimeSummary = sessionToolWallTimeSummary(item);
  const primaryRequest = sessionPrimaryRequest(item);
  const lines = [];

  if (engineSummary) {
    lines.push(`Model context: ${engineSummary}.`);
  }
  if (latencySummary) {
    lines.push(`First assistant response arrived in ${latencySummary}.`);
  }
  if (durationSummary) {
    lines.push(`Session span: ${durationSummary}.`);
  }
  lines.push(
    `${formatCount(queries.length)} raw quer${queries.length === 1 ? "y" : "ies"} captured from the CLI.`,
  );

  if (inputTokens > 0 || outputTokens > 0) {
    lines.push(
      `${formatCount(inputTokens)} input and ${formatCount(outputTokens)} output tokens were uploaded for this session.`,
    );
  } else if (totalTokens > 0) {
    lines.push(
      `${formatCount(totalTokens)} total tokens were uploaded for this session.`,
    );
  }
  if (cachedInputTokens > 0 || reasoningOutputTokens > 0) {
    const tokenBits = [];
    if (cachedInputTokens > 0) {
      tokenBits.push(`${formatCount(cachedInputTokens)} cached input`);
    }
    if (reasoningOutputTokens > 0) {
      tokenBits.push(`${formatCount(reasoningOutputTokens)} reasoning output`);
    }
    lines.push(
      `Raw token breakdown captured ${tokenBits.join(" and ")} tokens.`,
    );
  }
  if (functionCalls > 0 || toolErrors > 0) {
    const toolLine = `${formatCount(functionCalls)} function call${functionCalls === 1 ? "" : "s"} captured`;
    lines.push(
      toolErrors > 0
        ? `${toolLine}, with ${formatCount(toolErrors)} non-zero tool exit${toolErrors === 1 ? "" : "s"}.`
        : `${toolLine}.`,
    );
  }
  if (toolMixSummary) {
    lines.push(`Tool mix: ${toolMixSummary}.`);
  }
  if (toolErrorMixSummary) {
    lines.push(`Tool errors: ${toolErrorMixSummary}.`);
  }
  if (toolWallTimeSummary) {
    lines.push(`Tool runtime: ${toolWallTimeSummary}.`);
  }
  if (reasoningSummaries.length > 0) {
    lines.push(
      `Captured ${formatCount(reasoningSummaries.length)} reasoning ${reasoningSummaries.length === 1 ? "summary" : "summaries"} from the model.`,
    );
    lines.push(
      `Latest reasoning summary: ${truncateText(reasoningSummaries[reasoningSummaries.length - 1], 160)}`,
    );
  }
  if (followUps.length > 0) {
    lines.push(
      `Latest follow-up: ${truncateText(followUps[followUps.length - 1], 160)}`,
    );
  }
  if (latestReply) {
    lines.push(`Latest reply: ${latestReply}`);
  }
  if (primaryRequest) {
    lines.push(
      followUps.length > 0
        ? `${formatCount(followUps.length)} follow-up request${followUps.length === 1 ? "" : "s"} were captured in this session.`
        : "This session captured a single user request.",
    );
  } else if (queries[0]) {
    lines.push(
      `User request: ${truncateText(normalizeInlineText(queries[0]))}`,
    );
  }
  if (queries.length >= 2) {
    lines.push(
      "Repeated phrasing here may belong in a reusable prompt template or workspace note.",
    );
  } else {
    lines.push(
      "More session traces will make the next analysis report more specific.",
    );
  }

  return lines;
}

function trendPoints(insights) {
  return toArray(insights && insights.days).slice(-12);
}

function totalInsightSessionCount(insights) {
  const byDays = toArray(insights && insights.days).reduce(
    (sum, item) => sum + Number(item.session_count || 0),
    0,
  );
  if (byDays > 0) {
    return byDays;
  }
  return Math.max(
    Number((insights && insights.known_model_sessions) || 0) +
      Number((insights && insights.unknown_model_sessions) || 0),
    Number((insights && insights.known_provider_sessions) || 0) +
      Number((insights && insights.unknown_provider_sessions) || 0),
    Number((insights && insights.known_latency_sessions) || 0) +
      Number((insights && insights.unknown_latency_sessions) || 0),
    Number((insights && insights.known_duration_sessions) || 0) +
      Number((insights && insights.unknown_duration_sessions) || 0),
    0,
  );
}

function totalInsightInputTokens(insights) {
  return toArray(insights && insights.days).reduce(
    (sum, item) => sum + Number(item.input_tokens || 0),
    0,
  );
}

function totalInsightOutputTokens(insights) {
  return toArray(insights && insights.days).reduce(
    (sum, item) => sum + Number(item.output_tokens || 0),
    0,
  );
}

function usageTrendNarrative(insights) {
  const days = trendPoints(insights);
  if (!days.length) {
    return "Daily token flow will appear after sessions are uploaded from the CLI.";
  }

  const latest = days[days.length - 1];
  const previous = days.length > 1 ? days[days.length - 2] : null;
  const dayLabel = formatShortDate(latest.day);
  const deltaText = previous
    ? `${latest.total_tokens >= previous.total_tokens ? "up" : "down"} ${formatCompactCount(Math.abs(latest.total_tokens - previous.total_tokens))} from ${formatShortDate(previous.day)}`
    : "first captured day";
  return `${days.length} day(s) of usage are visible. ${dayLabel} carried ${formatCompactCount(latest.total_tokens)} total tokens across ${formatCount(latest.session_count)} session(s), ${deltaText}.`;
}

function modelCoverageNarrative(insights) {
  const known = Number((insights && insights.known_model_sessions) || 0);
  const unknown = Number((insights && insights.unknown_model_sessions) || 0);
  const total = known + unknown;
  if (!total) {
    return "Model capture coverage appears after the collector uploads model context.";
  }
  if (!known) {
    return "Uploaded sessions exist, but none currently include model names.";
  }
  return `${formatPercent(known / total)} of uploaded sessions include model names. ${formatCount(unknown)} session(s) are still missing that field.`;
}

function providerCoverageNarrative(insights) {
  const known = Number((insights && insights.known_provider_sessions) || 0);
  const unknown = Number((insights && insights.unknown_provider_sessions) || 0);
  const total = known + unknown;
  if (!total) {
    return "Provider coverage appears after the collector uploads provider context from local sessions.";
  }
  if (!known) {
    return "Uploaded sessions exist, but none currently include provider labels.";
  }
  return `${formatPercent(known / total)} of uploaded sessions include provider labels. ${formatCount(unknown)} session(s) are still missing that field.`;
}

function latencyTrendNarrative(insights) {
  const known = Number((insights && insights.known_latency_sessions) || 0);
  const unknown = Number((insights && insights.unknown_latency_sessions) || 0);
  const avg = Number((insights && insights.avg_first_response_latency_ms) || 0);
  if (!known && !unknown) {
    return "Latency tracking appears after the collector captures both the first prompt and the first assistant reply.";
  }
  if (!known) {
    return `${formatCount(unknown)} uploaded session(s) exist, but none currently include first-response latency.`;
  }
  const base = `Average first response is ${formatLatency(avg)} across ${formatCount(known)} captured session(s).`;
  if (!unknown) {
    return base;
  }
  return `${base} ${formatCount(unknown)} session(s) are still missing that measurement.`;
}

function tokenDetailNarrative(insights) {
  const inputTokens = totalInsightInputTokens(insights);
  const outputTokens = totalInsightOutputTokens(insights);
  const cachedInputTokens = Number(
    (insights && insights.total_cached_input_tokens) || 0,
  );
  const reasoningOutputTokens = Number(
    (insights && insights.total_reasoning_output_tokens) || 0,
  );
  const totalSessions = totalInsightSessionCount(insights);
  if (!totalSessions || (!inputTokens && !outputTokens)) {
    return "Cached input and reasoning output details appear after expanded session summaries are uploaded from the CLI.";
  }

  const cachedShare =
    inputTokens > 0 ? formatPercent(cachedInputTokens / inputTokens) : "0%";
  const reasoningShare =
    outputTokens > 0
      ? formatPercent(reasoningOutputTokens / outputTokens)
      : "0%";
  return `${formatCount(cachedInputTokens)} cached input tokens (${cachedShare} of input) and ${formatCount(reasoningOutputTokens)} reasoning tokens (${reasoningShare} of output) are visible across ${formatCount(totalSessions)} session(s).`;
}

function toolExecutionNarrative(insights) {
  const totalSessions = totalInsightSessionCount(insights);
  const functionCalls = Number(
    (insights && insights.total_function_calls) || 0,
  );
  const toolErrors = Number((insights && insights.total_tool_errors) || 0);
  const toolWallTime = Number(
    (insights && insights.total_tool_wall_time_ms) || 0,
  );
  const sessionsWithCalls = Number(
    (insights && insights.sessions_with_function_calls) || 0,
  );
  const knownDuration = Number(
    (insights && insights.known_duration_sessions) || 0,
  );
  const unknownDuration = Number(
    (insights && insights.unknown_duration_sessions) || 0,
  );
  const avgDuration = Number(
    (insights && insights.avg_session_duration_ms) || 0,
  );
  if (!totalSessions) {
    return "Tool activity appears after local sessions are uploaded from the CLI.";
  }

  const parts = [];
  if (functionCalls > 0) {
    parts.push(
      `${formatCount(functionCalls)} function call(s) across ${formatCount(sessionsWithCalls)} session(s)`,
    );
  } else {
    parts.push("No tool calls captured yet");
  }
  if (toolErrors > 0) {
    parts.push(`${formatCount(toolErrors)} non-zero tool exit(s)`);
  }
  if (toolWallTime > 0) {
    parts.push(`${formatLatency(toolWallTime)} total tool wall time`);
  }
  if (avgDuration > 0) {
    parts.push(`average session span ${formatLatency(avgDuration)}`);
  }
  if (unknownDuration > 0) {
    parts.push(
      `${formatCount(unknownDuration)} session(s) still missing duration`,
    );
  } else if (knownDuration > 0) {
    parts.push(`${formatCount(knownDuration)} session(s) include duration`);
  }
  const topErrorTool = topInsightErrorTool(insights);
  if (topErrorTool && Number(topErrorTool.error_count || 0) > 0) {
    parts.push(
      `${String(topErrorTool.tool || "unknown").trim()} is the current top failing tool`,
    );
  }
  const topSlowTool = topInsightSlowTool(insights);
  if (topSlowTool && Number(topSlowTool.wall_time_ms || 0) > 0) {
    parts.push(
      `${String(topSlowTool.tool || "unknown").trim()} has the heaviest tool runtime`,
    );
  }
  return `${parts.join(". ")}.`;
}

function topInsightErrorTool(insights) {
  return (
    toArray(insights && insights.tools)
      .slice()
      .sort((a, b) => {
        const errorDelta =
          Number((b && b.error_count) || 0) - Number((a && a.error_count) || 0);
        if (errorDelta !== 0) {
          return errorDelta;
        }
        const rateDelta =
          Number((b && b.error_rate) || 0) - Number((a && a.error_rate) || 0);
        if (rateDelta !== 0) {
          return rateDelta;
        }
        return String((a && a.tool) || "").localeCompare(
          String((b && b.tool) || ""),
        );
      })[0] || null
  );
}

function topInsightSlowTool(insights) {
  return (
    toArray(insights && insights.tools)
      .slice()
      .sort((a, b) => {
        const runtimeDelta =
          Number((b && b.wall_time_ms) || 0) -
          Number((a && a.wall_time_ms) || 0);
        if (runtimeDelta !== 0) {
          return runtimeDelta;
        }
        const avgDelta =
          Number((b && b.avg_wall_time_ms) || 0) -
          Number((a && a.avg_wall_time_ms) || 0);
        if (avgDelta !== 0) {
          return avgDelta;
        }
        return String((a && a.tool) || "").localeCompare(
          String((b && b.tool) || ""),
        );
      })[0] || null
  );
}

function topInsightBusyTool(insights) {
  return (
    toArray(insights && insights.tools)
      .slice()
      .sort((a, b) => {
        const callDelta =
          Number((b && b.call_count) || 0) - Number((a && a.call_count) || 0);
        if (callDelta !== 0) {
          return callDelta;
        }
        const shareDelta =
          Number((b && b.share) || 0) - Number((a && a.share) || 0);
        if (shareDelta !== 0) {
          return shareDelta;
        }
        return String((a && a.tool) || "").localeCompare(
          String((b && b.tool) || ""),
        );
      })[0] || null
  );
}

function totalKnownSessionDurationMS(insights) {
  const knownDuration = Number(
    (insights && insights.known_duration_sessions) || 0,
  );
  const avgDuration = Number(
    (insights && insights.avg_session_duration_ms) || 0,
  );
  if (knownDuration <= 0 || avgDuration <= 0) {
    return 0;
  }
  return knownDuration * avgDuration;
}

function hotspotTools(insights) {
  const tools = toArray(insights && insights.tools).filter((item) => {
    return (
      Number((item && item.call_count) || 0) > 0 ||
      Number((item && item.error_count) || 0) > 0 ||
      Number((item && item.wall_time_ms) || 0) > 0
    );
  });
  if (!tools.length) {
    return [];
  }
  const maxAvgWallTime = Math.max(
    ...tools.map((item) => Number(item.avg_wall_time_ms || 0)),
    1,
  );
  return tools
    .map((item) => {
      const share = Number((item && item.share) || 0);
      const errorRate = Math.min(1, Number((item && item.error_rate) || 0));
      const avgWallTime = Number((item && item.avg_wall_time_ms) || 0);
      const score =
        share * 0.48 + errorRate * 0.34 + (avgWallTime / maxAvgWallTime) * 0.18;
      return Object.assign({}, item, { hotspot_score: score });
    })
    .sort((a, b) => {
      const scoreDelta =
        Number(b.hotspot_score || 0) - Number(a.hotspot_score || 0);
      if (scoreDelta !== 0) {
        return scoreDelta;
      }
      const runtimeDelta =
        Number(b.wall_time_ms || 0) - Number(a.wall_time_ms || 0);
      if (runtimeDelta !== 0) {
        return runtimeDelta;
      }
      return String(a.tool || "").localeCompare(String(b.tool || ""));
    });
}

function hotspotTone(item, insights) {
  const errorRate = Number((item && item.error_rate) || 0);
  const share = Number((item && item.share) || 0);
  const avgWallTime = Number((item && item.avg_wall_time_ms) || 0);
  const avgToolWallTime = Math.max(
    Number((insights && insights.avg_tool_wall_time_ms) || 0),
    1,
  );
  if (errorRate >= 0.2 || avgWallTime >= avgToolWallTime * 2 || share >= 0.55) {
    return "danger";
  }
  if (errorRate > 0 || avgWallTime >= avgToolWallTime * 1.35 || share >= 0.3) {
    return "warn";
  }
  return "good";
}

function hotspotTags(item, insights) {
  const tags = [];
  const errorCount = Number((item && item.error_count) || 0);
  const errorRate = Number((item && item.error_rate) || 0);
  const share = Number((item && item.share) || 0);
  const avgWallTime = Number((item && item.avg_wall_time_ms) || 0);
  const avgToolWallTime = Math.max(
    Number((insights && insights.avg_tool_wall_time_ms) || 0),
    1,
  );
  const totalSessions = Math.max(totalInsightSessionCount(insights), 1);
  const sessionCount = Number((item && item.session_count) || 0);

  if (errorRate >= 0.2) {
    tags.push(pill("High error", "danger"));
  } else if (errorCount > 0) {
    tags.push(pill("Has failures", "warn"));
  }
  if (avgWallTime >= avgToolWallTime * 2) {
    tags.push(pill("Slow avg", "danger"));
  } else if (avgWallTime >= avgToolWallTime * 1.35) {
    tags.push(pill("Slow avg", "warn"));
  }
  if (share >= 0.55) {
    tags.push(pill("Heavy share", "danger"));
  } else if (share >= 0.3) {
    tags.push(pill("Heavy share", "warn"));
  }
  if (sessionCount >= Math.max(2, Math.round(totalSessions * 0.7))) {
    tags.push(pill("Everywhere", "sky"));
  }

  return tags.slice(0, 3);
}

function toolTrendNarrative(insights) {
  const points = trendPoints(insights).filter(
    (item) =>
      Number(item.function_call_count || 0) > 0 ||
      Number(item.tool_error_count || 0) > 0,
  );
  if (!points.length) {
    return "Daily tool activity appears after the collector uploads function-call and tool-output events.";
  }
  const latest = points[points.length - 1];
  const calls = Number(latest.function_call_count || 0);
  const errors = Number(latest.tool_error_count || 0);
  const wallTime = Number(latest.tool_wall_time_ms || 0);
  return `${formatCount(points.length)} recent day(s) include tool activity. ${formatShortDate(latest.day)} logged ${formatCount(calls)} function call(s), ${formatCount(errors)} non-zero exit(s), and ${wallTime > 0 ? formatLatency(wallTime) : "no visible"} tool wall time.`;
}

function timeCompositionNarrative(insights) {
  const knownDuration = Number(
    (insights && insights.known_duration_sessions) || 0,
  );
  const toolWallTime = Number(
    (insights && insights.total_tool_wall_time_ms) || 0,
  );
  const totalCapturedDuration = totalKnownSessionDurationMS(insights);
  const sessionsWithCalls = Number(
    (insights && insights.sessions_with_function_calls) || 0,
  );
  if (!knownDuration && !toolWallTime) {
    return "Tool wall time can be compared against captured session span after local sessions include first and last event timestamps.";
  }
  if (!totalCapturedDuration) {
    return `${formatCount(sessionsWithCalls)} tool-using session(s) exist, but captured session span is still missing.`;
  }
  if (!toolWallTime) {
    return `${formatCount(knownDuration)} session(s) include duration, but no tool wall time has been captured yet.`;
  }
  return `Tools account for roughly ${formatPercent(toolWallTime / totalCapturedDuration)} of captured session span across ${formatCount(knownDuration)} duration-tracked session(s).`;
}

function toolHotspotNarrative(insights) {
  const tools = hotspotTools(insights);
  if (!tools.length) {
    return "Tool hotspots appear after local sessions upload named function calls, failures, and wall time.";
  }
  const busiest = topInsightBusyTool(insights);
  const slowest = topInsightSlowTool(insights);
  const noisiest = topInsightErrorTool(insights);
  const parts = [];
  if (busiest && Number(busiest.call_count || 0) > 0) {
    parts.push(
      `${String(busiest.tool || "unknown").trim()} drives ${formatPercent(busiest.share || 0)} of captured tool calls`,
    );
  }
  if (slowest && Number(slowest.avg_wall_time_ms || 0) > 0) {
    parts.push(
      `${String(slowest.tool || "unknown").trim()} is slowest on average at ${formatLatency(slowest.avg_wall_time_ms || 0)} per call`,
    );
  }
  if (noisiest && Number(noisiest.error_count || 0) > 0) {
    parts.push(
      `${String(noisiest.tool || "unknown").trim()} has the highest visible failure rate`,
    );
  }
  return `${parts.join(". ")}.`;
}

function durationTrendNarrative(insights) {
  const points = trendPoints(insights).filter(
    (item) => Number(item.duration_session_count || 0) > 0,
  );
  if (!points.length) {
    return "Session span appears after the collector uploads first and last event timestamps for local sessions.";
  }
  const latest = points[points.length - 1];
  return `${formatCount(points.length)} recent day(s) include session span capture. ${formatShortDate(latest.day)} averaged ${formatLatency(latest.avg_session_duration_ms)} across ${formatCount(latest.duration_session_count || 0)} session(s).`;
}

function coverageActionState(insights) {
  const knownModels = Number((insights && insights.known_model_sessions) || 0);
  const unknownModels = Number(
    (insights && insights.unknown_model_sessions) || 0,
  );
  const knownProviders = Number(
    (insights && insights.known_provider_sessions) || 0,
  );
  const unknownProviders = Number(
    (insights && insights.unknown_provider_sessions) || 0,
  );
  const knownLatency = Number(
    (insights && insights.known_latency_sessions) || 0,
  );
  const unknownLatency = Number(
    (insights && insights.unknown_latency_sessions) || 0,
  );
  const total = Math.max(
    knownModels + unknownModels,
    knownProviders + unknownProviders,
    knownLatency + unknownLatency,
  );
  if (!total) {
    return {
      visible: true,
      summary:
        "No workspace sessions are visible yet. Run setup on the target machine first. If setup did not enroll background collection, use the manual fallback below to seed the charts from existing local sessions and keep them current automatically.",
    };
  }
  if (unknownModels <= 0 && unknownProviders <= 0 && unknownLatency <= 0) {
    return {
      visible: false,
      summary: "",
    };
  }
  return {
    visible: true,
    summary: `${formatCount(Math.max(unknownModels, unknownProviders, unknownLatency))} uploaded session(s) are still missing model, provider, or first-response timing metadata. Re-collect recent local sessions once to backfill those signals.`,
  };
}

function topInsightProvider(insights) {
  const providers = toArray(insights && insights.providers);
  if (!providers.length) {
    return null;
  }
  return providers[0];
}

function heavySessions(items) {
  return items
    .slice()
    .sort((a, b) => sessionTotalTokens(b) - sessionTotalTokens(a))
    .slice(0, 5);
}

function auditTitle(item) {
  const type = String(item.type || "");
  if (type === "execution.result" && item.message) {
    return `Execution ${titleize(item.message)}`;
  }
  return titleize(type || "activity");
}

/* ── Render ── */

function renderOverview(overview) {
  const activeReports = Number(overview.active_reports || 0);
  const research = overview && overview.research_status;
  const researchState = String((research && research.state) || "")
    .trim()
    .toLowerCase();
  $("activeReports").textContent = formatCount(activeReports);
  $("totalSessions").textContent = formatCount(overview.total_sessions);
  $("avgInputTokensPerQuery").textContent = formatCount(
    Math.round(Number(overview.avg_input_tokens_per_query || 0)),
  );
  $("avgOutputTokensPerQuery").textContent = formatCount(
    Math.round(Number(overview.avg_output_tokens_per_query || 0)),
  );

  const totalSessions = Number(overview.total_sessions || 0);

  $("activeReportsMeta").textContent =
    activeReports === 0
      ? reportResearchNarrative(research) ||
        "No reports yet. Upload more Codex sessions to generate trace analysis."
      : `${formatCount(activeReports)} trace analysis report(s) from your Codex sessions.`;
  $("totalSessionsMeta").textContent =
    totalSessions === 0
      ? "Upload sessions from the CLI to start tracking AI usage."
      : researchState === "waiting_for_min_sessions"
        ? `${formatCount(totalSessions)} AI usage session(s) collected so far. The first report refresh starts at ${formatCount(research && research.minimum_sessions)} sessions.`
        : `${formatCount(totalSessions)} AI usage session(s) collected from the CLI so far.`;
  $("avgTokensMeta").textContent =
    `${formatCount(overview.total_input_tokens || 0)} input / ${formatCount(overview.total_output_tokens || 0)} output tokens uploaded so far.`;
  $("overviewNarrative").textContent = workloadNarrative(overview);
}

function renderUsageTrend(insights) {
  $("usageTrendSummary").textContent = usageTrendNarrative(insights);
  const points = trendPoints(insights);
  if (!points.length) {
    $("usageTrendChart").innerHTML =
      `<div class="usage-column-empty">No daily usage yet. Upload sessions from the CLI to start the trend line.</div>`;
    return;
  }

  const maxTotal = Math.max(
    ...points.map((item) => Number(item.total_tokens || 0)),
    1,
  );
  $("usageTrendChart").innerHTML = points
    .map((item) => {
      const total = Number(item.total_tokens || 0);
      const input = Number(item.input_tokens || 0);
      const output = Number(item.output_tokens || 0);
      const scaledHeight = Math.max(
        18,
        Math.round(150 * Math.sqrt(total / maxTotal)),
      );
      const outputHeight =
        total > 0
          ? Math.max(
              output > 0 ? 2 : 0,
              Math.round(scaledHeight * (output / total)),
            )
          : 0;
      const inputHeight = Math.max(
        total > 0 && input > 0 ? 2 : 0,
        scaledHeight - outputHeight,
      );
      const flags = [];
      const activityNotes = [];
      if (Number(item.report_count || 0) > 0) {
        flags.push(
          `<span class="usage-flag report" title="${escapeAttr(`${formatCount(item.report_count)} analysis report(s)`)}"></span>`,
        );
        activityNotes.push(`${formatCount(item.report_count)} report(s)`);
      }
      if (Number(item.snapshot_count || 0) > 0) {
        flags.push(
          `<span class="usage-flag snapshot" title="${escapeAttr(`${formatCount(item.snapshot_count)} config snapshot(s)`)}"></span>`,
        );
        activityNotes.push(`${formatCount(item.snapshot_count)} snapshot(s)`);
      }
      const meta = `${formatCount(item.session_count || 0)} sess`;
      const tooltip = `${item.day}: ${formatCount(input)} input / ${formatCount(output)} output / ${formatCount(total)} total tokens across ${formatCount(item.query_count || 0)} queries.${activityNotes.length ? ` Signals: ${activityNotes.join(", ")}.` : ""}`;
      return `
          <div class="usage-column">
            <div class="usage-column-flags">${flags.join("")}</div>
            <div class="usage-bar-wrap">
              <div class="usage-bar-stack" style="--bar-height:${scaledHeight}px" title="${escapeAttr(tooltip)}">
                <div class="usage-segment output" style="height:${Math.max(0, outputHeight)}px"></div>
                <div class="usage-segment input" style="height:${Math.max(0, inputHeight)}px"></div>
              </div>
            </div>
            <div class="usage-column-day">${escapeHTML(formatShortDate(item.day))}</div>
            <div class="usage-column-meta">${escapeHTML(meta)}<br>${escapeHTML(formatCompactCount(total))}</div>
          </div>
        `;
    })
    .join("");
}

function renderModelCoverage(insights) {
  $("modelCoverageSummary").textContent = modelCoverageNarrative(insights);
  const known = Number((insights && insights.known_model_sessions) || 0);
  const unknown = Number((insights && insights.unknown_model_sessions) || 0);
  const total = known + unknown;
  const rows = toArray(insights && insights.models)
    .slice(0, 5)
    .map(
      (item) => `
        <div class="model-row">
          <div class="model-row-top">
            <strong>${escapeHTML(item.model || "Unknown")}</strong>
            <span>${escapeHTML(`${formatCount(item.session_count || 0)} session(s) · ${formatPercent(item.share || 0)}`)}</span>
          </div>
          <div class="model-track">
            <div class="model-fill" style="width:${Math.max(6, Math.round(Number(item.share || 0) * 100))}%"></div>
          </div>
        </div>
      `,
    );

  if (unknown > 0 && total > 0) {
    rows.push(`
          <div class="model-row">
            <div class="model-row-top">
              <strong>Model missing</strong>
              <span>${escapeHTML(`${formatCount(unknown)} session(s) · ${formatPercent(unknown / total)}`)}</span>
            </div>
            <div class="model-track">
              <div class="model-fill" style="width:${Math.max(6, Math.round((unknown / total) * 100))}%"></div>
            </div>
          </div>
        `);
  }

  if (!rows.length) {
    $("modelCoverageList").innerHTML = emptyState(
      "No model names captured yet",
      "The current collector is still missing model labels for these sessions.",
    );
    return;
  }

  $("modelCoverageList").innerHTML =
    `${rows.join("")}<div class="coverage-note">Sessions can only be grouped by model when the local collector includes the model field in the uploaded summary.</div>`;
}

function renderProviderCoverage(insights) {
  $("providerCoverageSummary").textContent =
    providerCoverageNarrative(insights);
  const known = Number((insights && insights.known_provider_sessions) || 0);
  const unknown = Number((insights && insights.unknown_provider_sessions) || 0);
  const total = known + unknown;
  const rows = toArray(insights && insights.providers)
    .slice(0, 5)
    .map(
      (item) => `
        <div class="model-row">
          <div class="model-row-top">
            <strong>${escapeHTML(titleize(item.provider || "Unknown"))}</strong>
            <span>${escapeHTML(`${formatCount(item.session_count || 0)} session(s) · ${formatPercent(item.share || 0)}`)}</span>
          </div>
          <div class="model-track">
            <div class="model-fill" style="width:${Math.max(6, Math.round(Number(item.share || 0) * 100))}%"></div>
          </div>
        </div>
      `,
    );

  if (unknown > 0 && total > 0) {
    rows.push(`
          <div class="model-row">
            <div class="model-row-top">
              <strong>Provider missing</strong>
              <span>${escapeHTML(`${formatCount(unknown)} session(s) · ${formatPercent(unknown / total)}`)}</span>
            </div>
            <div class="model-track">
              <div class="model-fill" style="width:${Math.max(6, Math.round((unknown / total) * 100))}%"></div>
            </div>
          </div>
        `);
  }

  if (!rows.length) {
    $("providerCoverageList").innerHTML = emptyState(
      "No provider labels captured yet",
      "Provider distribution will appear after the collector uploads provider metadata from local sessions.",
    );
    return;
  }

  $("providerCoverageList").innerHTML =
    `${rows.join("")}<div class="coverage-note">Provider labels help separate OpenAI or future multi-provider traffic before you compare cost and latency trends.</div>`;
}

function renderTrendCoverage(insights) {
  const knownModels = Number((insights && insights.known_model_sessions) || 0);
  const unknownModels = Number(
    (insights && insights.unknown_model_sessions) || 0,
  );
  const knownProviders = Number(
    (insights && insights.known_provider_sessions) || 0,
  );
  const unknownProviders = Number(
    (insights && insights.unknown_provider_sessions) || 0,
  );
  const knownLatency = Number(
    (insights && insights.known_latency_sessions) || 0,
  );
  const unknownLatency = Number(
    (insights && insights.unknown_latency_sessions) || 0,
  );
  const total = Math.max(
    knownModels + unknownModels,
    knownProviders + unknownProviders,
    knownLatency + unknownLatency,
    0,
  );
  const topProvider = topInsightProvider(insights);
  const badges = [
    {
      label: "Model coverage",
      value: total ? formatPercent(knownModels / total) : "0%",
      meta: total
        ? `${formatCount(knownModels)} of ${formatCount(total)} session(s)`
        : "No uploaded sessions yet",
    },
    {
      label: "Latency coverage",
      value: total ? formatPercent(knownLatency / total) : "0%",
      meta: total
        ? `${formatCount(knownLatency)} of ${formatCount(total)} session(s)`
        : "No uploaded sessions yet",
    },
    {
      label: "Top provider",
      value: topProvider ? titleize(topProvider.provider) : "None",
      meta: topProvider
        ? `${formatCount(topProvider.session_count || 0)} session(s) tagged`
        : "Provider labels not captured yet",
    },
    {
      label: "Avg first reply",
      value:
        Number((insights && insights.avg_first_response_latency_ms) || 0) > 0
          ? formatLatency(insights.avg_first_response_latency_ms)
          : "None",
      meta:
        knownLatency > 0
          ? `${formatCount(knownLatency)} captured session(s)`
          : "Waiting for latency capture",
    },
  ];

  $("trendCoverageStrip").innerHTML = badges
    .map(
      (item) => `
        <div class="trend-badge">
          <div class="trend-badge-label">${escapeHTML(item.label)}</div>
          <div class="trend-badge-value">${escapeHTML(item.value)}</div>
          <div class="trend-badge-meta">${escapeHTML(item.meta)}</div>
        </div>
      `,
    )
    .join("");
}

function renderLatencyTrend(insights) {
  $("latencySummary").textContent = latencyTrendNarrative(insights);
  const points = trendPoints(insights).filter(
    (item) => Number(item.latency_session_count || 0) > 0,
  );
  if (!points.length) {
    $("latencyList").innerHTML = emptyState(
      "No latency coverage yet",
      "The collector needs both a meaningful prompt and an assistant reply timestamp before latency can be charted.",
    );
    return;
  }

  const recentPoints = points.slice(-6);
  const maxLatency = Math.max(
    ...recentPoints.map((item) =>
      Number(item.avg_first_response_latency_ms || 0),
    ),
    1,
  );
  $("latencyList").innerHTML = recentPoints
    .map((item) => {
      const avgLatency = Number(item.avg_first_response_latency_ms || 0);
      const width = Math.max(8, Math.round((avgLatency / maxLatency) * 100));
      return `
          <div class="latency-row">
            <div class="latency-row-top">
              <strong>${escapeHTML(formatShortDate(item.day))}</strong>
              <span>${escapeHTML(formatLatency(avgLatency))}</span>
            </div>
            <div class="latency-track">
              <div class="latency-fill" style="width:${width}%"></div>
            </div>
            <div class="latency-row-meta">${escapeHTML(`${formatCount(item.latency_session_count || 0)} session(s) with latency capture`)}</div>
          </div>
        `;
    })
    .join("");
}

function renderAssistantToolDetails(insights) {
  const inputTokens = totalInsightInputTokens(insights);
  const outputTokens = totalInsightOutputTokens(insights);
  const cachedInputTokens = Number(
    (insights && insights.total_cached_input_tokens) || 0,
  );
  const reasoningOutputTokens = Number(
    (insights && insights.total_reasoning_output_tokens) || 0,
  );
  const functionCalls = Number(
    (insights && insights.total_function_calls) || 0,
  );
  const toolErrors = Number((insights && insights.total_tool_errors) || 0);
  const toolWallTime = Number(
    (insights && insights.total_tool_wall_time_ms) || 0,
  );
  const avgToolWallTime = Number(
    (insights && insights.avg_tool_wall_time_ms) || 0,
  );
  const sessionsWithCalls = Number(
    (insights && insights.sessions_with_function_calls) || 0,
  );
  const sessionsWithErrors = Number(
    (insights && insights.sessions_with_tool_errors) || 0,
  );
  const knownDuration = Number(
    (insights && insights.known_duration_sessions) || 0,
  );
  const totalSessions = totalInsightSessionCount(insights);
  const avgDuration = Number(
    (insights && insights.avg_session_duration_ms) || 0,
  );
  const topErrorTool = topInsightErrorTool(insights);
  const topSlowTool = topInsightSlowTool(insights);

  $("tokenDetailSummary").textContent = tokenDetailNarrative(insights);
  $("toolExecutionSummary").textContent = toolExecutionNarrative(insights);

  const tokenCards = [
    {
      label: "Cached input",
      value: formatCount(cachedInputTokens),
      meta:
        inputTokens > 0
          ? `${formatPercent(cachedInputTokens / inputTokens)} of ${formatCount(inputTokens)} input tokens`
          : "No input tokens uploaded yet",
    },
    {
      label: "Reasoning output",
      value: formatCount(reasoningOutputTokens),
      meta:
        outputTokens > 0
          ? `${formatPercent(reasoningOutputTokens / outputTokens)} of ${formatCount(outputTokens)} output tokens`
          : "No output tokens uploaded yet",
    },
    {
      label: "Prompt tokens",
      value: formatCount(inputTokens),
      meta:
        cachedInputTokens > 0
          ? `${formatCount(Math.max(inputTokens - cachedInputTokens, 0))} fresh input token(s)`
          : "No cached split captured yet",
    },
    {
      label: "Response tokens",
      value: formatCount(outputTokens),
      meta:
        reasoningOutputTokens > 0
          ? `${formatCount(Math.max(outputTokens - reasoningOutputTokens, 0))} non-reasoning output token(s)`
          : "No reasoning split captured yet",
    },
  ];

  const toolCards = [
    {
      label: "Function calls",
      value: formatCount(functionCalls),
      meta:
        functionCalls > 0
          ? `${formatCount(sessionsWithCalls)} session(s) used tools · ${toolWallTime > 0 ? `${formatLatency(toolWallTime)} total wall time` : "No wall-time capture yet"}`
          : "No tool calls captured yet",
    },
    {
      label: "Tool errors",
      value: formatCount(toolErrors),
      meta:
        functionCalls > 0
          ? `${formatPercent(toolErrors / functionCalls)} of function calls returned non-zero exits`
          : "Waiting for tool call data",
    },
    {
      label: "Avg session span",
      value: avgDuration > 0 ? formatLatency(avgDuration) : "None",
      meta:
        knownDuration > 0
          ? `${formatCount(knownDuration)} of ${formatCount(totalSessions)} session(s) captured`
          : "No duration captured yet",
    },
    {
      label: "Top failing tool",
      value:
        topErrorTool && Number(topErrorTool.error_count || 0) > 0
          ? String(topErrorTool.tool || "").trim() || "Unknown"
          : "None",
      meta:
        topErrorTool && Number(topErrorTool.error_count || 0) > 0
          ? `${formatCount(topErrorTool.error_count || 0)} error(s) · ${formatPercent(topErrorTool.error_rate || 0)} error rate`
          : totalSessions > 0
            ? `${formatCount(sessionsWithErrors)} session(s) with errors`
            : "No uploaded sessions yet",
    },
    {
      label: "Top slow tool",
      value:
        topSlowTool && Number(topSlowTool.wall_time_ms || 0) > 0
          ? String(topSlowTool.tool || "").trim() || "Unknown"
          : "None",
      meta:
        topSlowTool && Number(topSlowTool.wall_time_ms || 0) > 0
          ? `${formatLatency(topSlowTool.wall_time_ms || 0)} total · ${formatLatency(topSlowTool.avg_wall_time_ms || 0)} avg`
          : avgToolWallTime > 0
            ? `${formatLatency(avgToolWallTime)} avg wall time per call`
            : "No wall-time capture yet",
    },
  ];

  $("tokenDetailList").innerHTML = tokenCards
    .map(
      (item) => `
        <div class="trend-badge">
          <div class="trend-badge-label">${escapeHTML(item.label)}</div>
          <div class="trend-badge-value">${escapeHTML(item.value)}</div>
          <div class="trend-badge-meta">${escapeHTML(item.meta)}</div>
        </div>
      `,
    )
    .join("");

  $("toolExecutionList").innerHTML = toolCards
    .map(
      (item) => `
        <div class="trend-badge">
          <div class="trend-badge-label">${escapeHTML(item.label)}</div>
          <div class="trend-badge-value">${escapeHTML(item.value)}</div>
          <div class="trend-badge-meta">${escapeHTML(item.meta)}</div>
        </div>
      `,
    )
    .join("");

  const toolRows = toArray(insights && insights.tools)
    .slice(0, 5)
    .map(
      (item) => `
        <div class="model-row">
          <div class="model-row-top">
            <strong>${escapeHTML(String(item.tool || "").trim() || "Unknown tool")}</strong>
            <span>${escapeHTML(`${formatCount(item.call_count || 0)} call(s) · ${formatPercent(item.share || 0)}`)}</span>
          </div>
          <div class="model-track">
            <div class="model-fill" style="width:${Math.max(6, Math.round(Number(item.share || 0) * 100))}%"></div>
          </div>
          <div class="model-row-meta">${escapeHTML(
            [
              `${formatCount(item.session_count || 0)} session(s) used this tool`,
              Number(item.error_count || 0) > 0
                ? `${formatCount(item.error_count || 0)} error(s)`
                : "",
              Number(item.error_count || 0) > 0
                ? `${formatPercent(item.error_rate || 0)} error rate`
                : "",
              Number(item.wall_time_ms || 0) > 0
                ? `${formatLatency(item.wall_time_ms || 0)} total wall time`
                : "",
              Number(item.avg_wall_time_ms || 0) > 0
                ? `${formatLatency(item.avg_wall_time_ms || 0)} avg`
                : "",
            ]
              .filter(Boolean)
              .join(" · "),
          )}</div>
        </div>
      `,
    );
  $("toolMixList").innerHTML = toolRows.length
    ? toolRows.join("")
    : emptyState(
        "No named tools captured yet",
        "Tool mix will appear after local sessions include function_call names in uploaded summaries.",
      );
}

function renderTimeComposition(insights) {
  $("timeCompositionSummary").textContent = timeCompositionNarrative(insights);

  const totalCapturedDuration = totalKnownSessionDurationMS(insights);
  const toolWallTime = Number(
    (insights && insights.total_tool_wall_time_ms) || 0,
  );
  const sessionsWithCalls = Number(
    (insights && insights.sessions_with_function_calls) || 0,
  );
  const knownDuration = Number(
    (insights && insights.known_duration_sessions) || 0,
  );
  const functionCalls = Number(
    (insights && insights.total_function_calls) || 0,
  );
  const avgToolWallTime = Number(
    (insights && insights.avg_tool_wall_time_ms) || 0,
  );
  const topSlowTool = topInsightSlowTool(insights);
  const topBusyTool = topInsightBusyTool(insights);

  if (!totalCapturedDuration && !toolWallTime && !functionCalls) {
    $("timeCompositionList").innerHTML = emptyState(
      "No captured session span yet",
      "Expanded session summaries will compare tool wall time against end-to-end session duration after the next local collect.",
    );
    return;
  }

  const toolShare =
    totalCapturedDuration > 0
      ? Math.min(toolWallTime / totalCapturedDuration, 1)
      : 0;
  const nonToolShare =
    totalCapturedDuration > 0 ? Math.max(0, 1 - toolShare) : 0;
  const avgRuntimePerToolSession =
    sessionsWithCalls > 0 ? Math.round(toolWallTime / sessionsWithCalls) : 0;
  const avgCallsPerToolSession =
    sessionsWithCalls > 0 ? functionCalls / sessionsWithCalls : 0;

  $("timeCompositionList").innerHTML = `
        <div class="time-balance-track">
          <div class="time-balance-bar" aria-hidden="true">
            <div class="time-balance-fill is-tool" style="width:${Math.max(0, Math.round(toolShare * 100))}%"></div>
            <div class="time-balance-fill is-other" style="width:${Math.max(0, Math.round(nonToolShare * 100))}%"></div>
          </div>
          <div class="time-balance-legend">
            <div class="time-balance-legend-item">
              <span class="time-balance-swatch is-tool"></span>
              <span>${escapeHTML(`Tool wall time ${toolWallTime > 0 ? `${formatLatency(toolWallTime)} · ${formatPercent(toolShare)}` : "Not captured"}`)}</span>
            </div>
            <div class="time-balance-legend-item">
              <span class="time-balance-swatch is-other"></span>
              <span>${escapeHTML(`Non-tool span ${totalCapturedDuration > 0 ? `${formatLatency(Math.max(totalCapturedDuration - toolWallTime, 0))} · ${formatPercent(nonToolShare)}` : "Not captured"}`)}</span>
            </div>
          </div>
        </div>
        <div class="time-balance-metrics">
          <div class="time-metric">
            <div class="time-metric-label">Captured session span</div>
            <div class="time-metric-value">${escapeHTML(totalCapturedDuration > 0 ? formatLatency(totalCapturedDuration) : "Not captured")}</div>
            <div class="time-metric-meta">${escapeHTML(knownDuration > 0 ? `${formatCount(knownDuration)} duration-tracked session(s)` : "Waiting for duration coverage")}</div>
          </div>
          <div class="time-metric">
            <div class="time-metric-label">Avg runtime per tool session</div>
            <div class="time-metric-value">${escapeHTML(avgRuntimePerToolSession > 0 ? formatLatency(avgRuntimePerToolSession) : "None")}</div>
            <div class="time-metric-meta">${escapeHTML(sessionsWithCalls > 0 ? `${formatCount(sessionsWithCalls)} session(s) used tools` : "No tool sessions yet")}</div>
          </div>
          <div class="time-metric">
            <div class="time-metric-label">Calls per tool session</div>
            <div class="time-metric-value">${escapeHTML(sessionsWithCalls > 0 ? formatRate(avgCallsPerToolSession) : "0")}</div>
            <div class="time-metric-meta">${escapeHTML(functionCalls > 0 ? `${formatCount(functionCalls)} total function call(s)` : "No function calls captured yet")}</div>
          </div>
          <div class="time-metric">
            <div class="time-metric-label">Primary time sink</div>
            <div class="time-metric-value">${escapeHTML(topSlowTool && Number(topSlowTool.wall_time_ms || 0) > 0 ? String(topSlowTool.tool || "").trim() || "Unknown" : "None")}</div>
            <div class="time-metric-meta">${escapeHTML(
              topSlowTool && Number(topSlowTool.wall_time_ms || 0) > 0
                ? `${formatLatency(topSlowTool.wall_time_ms || 0)} total · ${formatLatency(topSlowTool.avg_wall_time_ms || 0)} avg`
                : topBusyTool && Number(topBusyTool.call_count || 0) > 0
                  ? `${String(topBusyTool.tool || "").trim() || "Unknown"} drives ${formatPercent(topBusyTool.share || 0)} of calls`
                  : avgToolWallTime > 0
                    ? `${formatLatency(avgToolWallTime)} avg runtime per call`
                    : "No tool runtime captured yet",
            )}</div>
          </div>
        </div>
      `;
}

function renderToolHotspots(insights) {
  $("toolHotspotSummary").textContent = toolHotspotNarrative(insights);
  const rows = hotspotTools(insights).slice(0, 4);
  if (!rows.length) {
    $("toolHotspotList").innerHTML = emptyState(
      "No tool hotspots yet",
      "Named function calls, tool failures, and wall time will create a ranked hotspot view after the next local collect.",
    );
    return;
  }

  $("toolHotspotList").innerHTML = rows
    .map((item) => {
      const tone = hotspotTone(item, insights);
      const tags = hotspotTags(item, insights);
      const scoreWidth = Math.max(
        10,
        Math.round(Number(item.hotspot_score || 0) * 100),
      );
      const scoreLabel = `${Math.round(Number(item.hotspot_score || 0) * 100)} hotspot`;
      const meta = [
        `${formatCount(item.call_count || 0)} call(s)`,
        `${formatPercent(item.share || 0)} share`,
        Number(item.avg_wall_time_ms || 0) > 0
          ? `${formatLatency(item.avg_wall_time_ms || 0)} avg`
          : "",
        Number(item.error_count || 0) > 0
          ? `${formatCount(item.error_count || 0)} error(s)`
          : "",
      ]
        .filter(Boolean)
        .join(" · ");
      return `
          <div class="hotspot-row">
            <div class="hotspot-row-top">
              <div class="hotspot-title-block">
                <strong>${escapeHTML(String(item.tool || "").trim() || "Unknown tool")}</strong>
                <div class="hotspot-tags">${tags.join("")}</div>
              </div>
              <span class="hotspot-score">${escapeHTML(scoreLabel)}</span>
            </div>
            <div class="hotspot-track">
              <div class="hotspot-fill hotspot-fill-${escapeAttr(tone)}" style="width:${scoreWidth}%"></div>
            </div>
            <div class="hotspot-meta">${escapeHTML(meta)}</div>
          </div>
        `;
    })
    .join("");
}

function renderToolTrend(insights) {
  $("toolTrendSummary").textContent = toolTrendNarrative(insights);
  const points = trendPoints(insights).filter(
    (item) =>
      Number(item.function_call_count || 0) > 0 ||
      Number(item.tool_error_count || 0) > 0,
  );
  if (!points.length) {
    $("toolTrendList").innerHTML = emptyState(
      "No tool activity yet",
      "Function calls and tool failures will appear here after the collector uploads response_item tool events.",
    );
    return;
  }

  const recentPoints = points.slice(-6);
  const maxCalls = Math.max(
    ...recentPoints.map((item) => Number(item.function_call_count || 0)),
    1,
  );
  $("toolTrendList").innerHTML = recentPoints
    .map((item) => {
      const calls = Number(item.function_call_count || 0);
      const errors = Number(item.tool_error_count || 0);
      const wallTime = Number(item.tool_wall_time_ms || 0);
      const cached = Number(item.cached_input_tokens || 0);
      const reasoning = Number(item.reasoning_output_tokens || 0);
      const width = Math.max(
        8,
        Math.round((Math.max(calls, 1) / maxCalls) * 100),
      );
      const metaBits = [
        `${formatCount(errors)} non-zero exit${errors === 1 ? "" : "s"}`,
        wallTime > 0 ? `${formatLatency(wallTime)} wall time` : "",
        cached > 0 ? `${formatCount(cached)} cached input` : "",
        reasoning > 0 ? `${formatCount(reasoning)} reasoning output` : "",
      ].filter(Boolean);
      return `
          <div class="latency-row">
            <div class="latency-row-top">
              <strong>${escapeHTML(formatShortDate(item.day))}</strong>
              <span>${escapeHTML(`${formatCount(calls)} call${calls === 1 ? "" : "s"}`)}</span>
            </div>
            <div class="latency-track">
              <div class="latency-fill" style="width:${width}%"></div>
            </div>
            <div class="latency-row-meta">${escapeHTML(metaBits.join(" · "))}</div>
          </div>
        `;
    })
    .join("");
}

function renderDurationTrend(insights) {
  $("durationTrendSummary").textContent = durationTrendNarrative(insights);
  const points = trendPoints(insights).filter(
    (item) => Number(item.duration_session_count || 0) > 0,
  );
  if (!points.length) {
    $("durationTrendList").innerHTML = emptyState(
      "No session span yet",
      "Average session duration will appear here after the collector uploads first and last event timestamps.",
    );
    return;
  }

  const recentPoints = points.slice(-6);
  const maxDuration = Math.max(
    ...recentPoints.map((item) => Number(item.avg_session_duration_ms || 0)),
    1,
  );
  $("durationTrendList").innerHTML = recentPoints
    .map((item) => {
      const avgDuration = Number(item.avg_session_duration_ms || 0);
      const sessions = Number(item.duration_session_count || 0);
      const calls = Number(item.function_call_count || 0);
      const errors = Number(item.tool_error_count || 0);
      const width = Math.max(8, Math.round((avgDuration / maxDuration) * 100));
      const metaBits = [
        `${formatCount(sessions)} session${sessions === 1 ? "" : "s"}`,
        calls > 0
          ? `${formatCount(calls)} function call${calls === 1 ? "" : "s"}`
          : "No tool calls",
        errors > 0
          ? `${formatCount(errors)} error${errors === 1 ? "" : "s"}`
          : "",
      ].filter(Boolean);
      return `
          <div class="latency-row">
            <div class="latency-row-top">
              <strong>${escapeHTML(formatShortDate(item.day))}</strong>
              <span>${escapeHTML(formatLatency(avgDuration))}</span>
            </div>
            <div class="latency-track">
              <div class="latency-fill" style="width:${width}%"></div>
            </div>
            <div class="latency-row-meta">${escapeHTML(metaBits.join(" · "))}</div>
          </div>
        `;
    })
    .join("");
}

function renderCoverageActions(insights) {
  const state = coverageActionState(insights);
  $("coverageActionsSection").hidden = !state.visible;
  if (!state.visible) {
    return;
  }
  $("coverageActionSummary").textContent = state.summary;
}

function renderHeavySessions(items) {
  const heavy = heavySessions(items);
  if (!heavy.length) {
    $("heavySessionList").innerHTML = emptyState(
      "No recent sessions yet",
      "Recent sessions will appear here after the collector uploads them.",
    );
    return;
  }

  $("heavySessionList").innerHTML = heavy
    .map((item) => {
      const totalTokens = sessionTotalTokens(item);
      const queryCount = toArray(item.raw_queries).length;
      const engine = sessionEngineSummary(item);
      const latency = sessionLatencySummary(item);
      const duration = sessionDurationSummary(item);
      const tools = sessionToolSummary(item);
      const reasoningSummaries = sessionFullReasoningSummaries(item);
      const toolMix = sessionToolMixSummary(item);
      const toolErrorMix = sessionToolErrorMixSummary(item);
      const toolWallTime = Number(item.tool_wall_time_ms || 0);
      const toolWallTimeMix = sessionToolWallTimeSummary(item);
      const meta = [
        `${formatCount(totalTokens)} total tokens`,
        `${formatCount(queryCount)} quer${queryCount === 1 ? "y" : "ies"}`,
      ];
      if (engine) {
        meta.push(engine);
      }
      if (latency) {
        meta.push(`First response ${latency}`);
      }
      if (duration) {
        meta.push(`Span ${duration}`);
      }
      if (tools) {
        meta.push(tools);
      }
      if (toolMix) {
        meta.push(toolMix);
      }
      if (toolErrorMix) {
        meta.push(`Errors ${toolErrorMix}`);
      }
      if (toolWallTime > 0) {
        meta.push(`Tool runtime ${formatLatency(toolWallTime)}`);
      }
      if (toolWallTimeMix) {
        meta.push(toolWallTimeMix);
      }
      return `
          <div class="timeline-row">
            <div class="timeline-row-top">
              <div class="timeline-row-title">${escapeHTML(truncateText(sessionPrimaryRequest(item) || "Recent session", 74))}</div>
              <div class="timeline-row-time">${escapeHTML(formatDateTime(item.timestamp))}</div>
            </div>
            <div class="timeline-row-meta">${escapeHTML(meta.join(" · "))}</div>
          </div>
        `;
    })
    .join("");
}

function renderSnapshots(items) {
  if (!items.length) {
    $("snapshotList").innerHTML = emptyState(
      "No snapshots yet",
      "Config snapshots will appear here after the collector captures local settings.",
    );
    return;
  }

  $("snapshotList").innerHTML = items
    .slice(0, 6)
    .map((item) => {
      const instructionFiles = toArray(item.instruction_files).filter(Boolean);
      const summary = [
        `${item.hooks_enabled ? "Hooks on" : "Hooks off"}`,
        `${formatCount(item.enabled_mcp_count || 0)} MCP`,
        instructionFiles.length
          ? instructionFiles.join(", ")
          : "No instruction files",
      ].join(" · ");
      return `
          <div class="timeline-row">
            <div class="timeline-row-top">
              <div class="timeline-row-title">${escapeHTML(item.profile_id || "default profile")}</div>
              <div class="timeline-row-time">${escapeHTML(formatDateTime(item.captured_at))}</div>
            </div>
            <div class="timeline-row-meta">${escapeHTML(summary)}</div>
          </div>
        `;
    })
    .join("");
}

function renderActivityTimeline(items) {
  if (!items.length) {
    $("activityTimeline").innerHTML = emptyState(
      "No recent events",
      "Uploads, report refreshes, and workspace activity will appear here once activity starts.",
    );
    return;
  }

  $("activityTimeline").innerHTML = items
    .slice(0, 8)
    .map(
      (item) => `
        <div class="timeline-row">
          <div class="timeline-row-top">
            <div class="timeline-row-title">${escapeHTML(auditTitle(item))}</div>
            <div class="timeline-row-time">${escapeHTML(formatDateTime(item.created_at))}</div>
          </div>
          <div class="timeline-row-meta">${escapeHTML(item.message || "Recent workspace activity.")}</div>
        </div>
      `,
    )
    .join("");
}

function renderOptimizationLoop(overview, reports) {
  const totalSessions = Number(overview.total_sessions || 0);
  const reportCount = toArray(reports).length;
  const research = overview && overview.research_status;
  const researchState = String((research && research.state) || "")
    .trim()
    .toLowerCase();

  const stages = [
    {
      key: "observe",
      label: "Observe",
      value: formatCount(totalSessions),
      meta:
        totalSessions > 0
          ? `${formatCount(totalSessions)} session(s) captured`
          : "Waiting for local sessions",
    },
    {
      key: "analyze",
      label: "Analyze",
      value: formatCount(researchState === "running" ? 1 : 0),
      meta:
        researchState === "running"
          ? "The research engine is reading recent sessions"
          : "Waiting for the next refresh",
    },
    {
      key: "report",
      label: "Report",
      value: formatCount(reportCount),
      meta:
        reportCount > 0
          ? `${formatCount(reportCount)} analysis report(s) ready`
          : "No report published yet",
    },
    {
      key: "improve",
      label: "Improve",
      value: formatCount(reportCount),
      meta:
        reportCount > 0
          ? "Use the report to adjust prompts and workflow habits"
          : "Next improvements will appear after a report is generated",
    },
  ];

  const activeStage =
    researchState === "running"
      ? "analyze"
      : reportCount > 0
        ? "report"
        : "observe";

  $("loopStageGrid").innerHTML = stages
    .map(
      (stage) => `
        <div class="loop-stage${stage.key === activeStage ? " is-active" : ""}">
          <div class="loop-stage-label">${escapeHTML(stage.label)}</div>
          <div class="loop-stage-value">${escapeHTML(stage.value)}</div>
          <div class="loop-stage-meta">${escapeHTML(stage.meta)}</div>
        </div>
      `,
    )
    .join("");

  if (reportCount === 0) {
    $("loopSummary").textContent =
      totalSessions > 0
        ? "The loop is in observation mode. Crux is digesting recent raw queries and response patterns before publishing the next report."
        : "The loop starts after the CLI uploads sessions and snapshots from your coding-agent workspace.";
    $("loopFocusCard").innerHTML = `
      <div class="loop-focus-empty">
        <strong>No report is published yet.</strong>
        <span>${escapeHTML(totalSessions > 0 ? "Keep uploading sessions to give the research engine enough evidence." : "Connect the CLI and upload sessions to seed the first report.")}</span>
      </div>
    `;
    return;
  }

  const activeReport = toArray(reports)[0] || null;
  const focusBits = [
    `${formatCount(totalSessions)} session(s) observed`,
    `${formatCount(Number(overview.total_tokens || 0))} total tokens captured`,
  ];
  if (research && research.last_duration_ms) {
    focusBits.push(`Last refresh ${formatLatency(research.last_duration_ms)}`);
  }

  $("loopSummary").textContent =
    researchState === "running"
      ? "A new analysis pass is running. The report will refresh after the server finishes reading the latest sessions."
      : "Each report analyzes Codex session traces to explain what the model understood, where it went wrong, and what you can do differently.";

  $("loopFocusCard").innerHTML = `
    <div class="loop-focus-top">
      <div>
        <div class="loop-focus-kicker">Current report</div>
        <div class="loop-focus-title">${escapeHTML(activeReport ? activeReport.title : "Workflow feedback")}</div>
      </div>
      <div class="loop-focus-pills">
        ${pill(reportKindLabel(activeReport ? activeReport.kind : ""), reportKindTone(activeReport ? activeReport.kind : ""))}
        ${pill(titleize(activeReport && activeReport.confidence ? activeReport.confidence : "low"), "sky")}
      </div>
    </div>
    <div class="loop-focus-body">
      <div class="loop-focus-reason">${escapeHTML(activeReport ? activeReport.summary : "The latest report is ready to review.")}</div>
      <div class="loop-focus-metrics">
        ${focusBits.map((item) => `<span>${escapeHTML(item)}</span>`).join("")}
      </div>
    </div>
  `;
}

function confidenceTone(value) {
  const raw = String(value || "").trim().toLowerCase();
  if (raw.includes("high")) {
    return "good";
  }
  if (raw.includes("low")) {
    return "warn";
  }
  return "sky";
}

function lifecycleStat(label, value) {
  return `
    <div class="lifecycle-stat">
      <div class="lifecycle-stat-label">${escapeHTML(label)}</div>
      <div class="lifecycle-stat-value">${escapeHTML(value)}</div>
    </div>
  `;
}

function lifecycleBullets(items, emptyText) {
  const entries = toArray(items)
    .map((item) => normalizeInlineText(item))
    .filter(Boolean)
    .slice(0, 4);
  if (!entries.length) {
    return `<div class="lifecycle-empty-note">${escapeHTML(emptyText)}</div>`;
  }
  return `<ul class="lifecycle-bullet-list">${entries.map((entry) => `<li>${escapeHTML(entry)}</li>`).join("")}</ul>`;
}

function lifecycleListBlock(label, items, emptyText, tone) {
  const toneAttr = tone ? ` data-tone="${escapeAttr(tone)}"` : "";
  return `
    <div class="lifecycle-list-block"${toneAttr}>
      <div class="lifecycle-list-label">${escapeHTML(label)}</div>
      ${lifecycleBullets(items, emptyText)}
    </div>
  `;
}

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

function lifecycleChipList(items, emptyText) {
  const entries = toArray(items)
    .map((item) => normalizeInlineText(item))
    .filter(Boolean)
    .slice(0, 6);
  if (!entries.length) {
    return `<div class="lifecycle-empty-note">${escapeHTML(emptyText)}</div>`;
  }
  return `<div class="lifecycle-chip-list">${entries.map((entry) => `<span class="lifecycle-chip">${escapeHTML(entry)}</span>`).join("")}</div>`;
}

function lifecycleStageCard(stage) {
  return `
    <article class="lifecycle-stage-card" data-state="${escapeAttr(stage.state)}">
      <div class="lifecycle-stage-top">
        <div class="lifecycle-stage-index">${escapeHTML(stage.step)}</div>
        <div class="lifecycle-stage-state" aria-hidden="true"></div>
      </div>
      <div class="lifecycle-stage-label">${escapeHTML(stage.label)}</div>
      <div class="lifecycle-stage-title">${escapeHTML(stage.title)}</div>
      <div class="lifecycle-stage-copy">${escapeHTML(stage.copy)}</div>
      <div class="lifecycle-stage-meta">${escapeHTML(stage.meta)}</div>
    </article>
  `;
}

function renderLifecycle(overview) {
  const section = $("lifecycleSection");
  const reports = Array.from(state.reportIndex.values());
  const latest = reports[0] || null;
  const totalSessions = Number((overview && overview.total_sessions) || 0);
  const reportCount = reports.length;
  const research = overview && overview.research_status;
  const researchState = String((research && research.state) || "")
    .trim()
    .toLowerCase();
  const lifecycleNarrative = reportResearchNarrative(research);

  $("lifecycleTitle").textContent = "Current analysis cycle";
  $("lifecycleDesc").textContent = latest
    ? "Read the latest report at a glance, then review what the model understood and what should change next."
    : "Follow the path from captured sessions to the first readable report. This view stays visible even before the first report exists.";

  const activeStep =
    totalSessions === 0
      ? "capture"
      : researchState === "running" || !latest
        ? "analyze"
        : "revisit";
  const latestTitle = String((latest && latest.title) || "").trim();
  const latestSummary = normalizeInlineText(latest && latest.summary);
  const firstNextStep = normalizeInlineText(
    latest && toArray(latest.next_steps).find(Boolean),
  );

  const stages = [
    {
      step: "01",
      label: "Capture",
      state:
        totalSessions > 0
          ? "done"
          : activeStep === "capture"
            ? "active"
            : "pending",
      title:
        totalSessions > 0
          ? `${formatCount(totalSessions)} session${totalSessions > 1 ? "s" : ""} captured`
          : "Waiting for session uploads",
      copy:
        totalSessions > 0
          ? "Recent Codex traces are available for review."
          : "Run the CLI inside a workspace to start the cycle.",
      meta:
        totalSessions > 0
          ? `${formatCount(totalSessions)} session trace${totalSessions > 1 ? "s are" : " is"} already in the dataset`
          : "The first upload unlocks analysis.",
    },
    {
      step: "02",
      label: "Analyze",
      state:
        latest && researchState !== "running"
          ? "done"
          : activeStep === "analyze"
            ? "active"
            : "pending",
      title:
        researchState === "running"
          ? "Analysis pass is running"
          : latest
            ? "Latest pass finished"
            : "Queued for analysis",
      copy:
        researchState === "running"
          ? "The research engine is reading recent sessions now."
          : latest
            ? "The server finished the last pass and produced the report below."
            : "The next pass starts after enough grounded evidence is available.",
      meta:
        researchState === "running"
          ? lifecycleNarrative || "A fresh report is being assembled."
          : latest && research && research.last_duration_ms
            ? `Last refresh took ${formatLatency(research.last_duration_ms)}`
            : latest
              ? "The last analysis pass completed successfully."
              : "The first pass waits on enough recent session evidence.",
    },
    {
      step: "03",
      label: "Report",
      state: latest ? "done" : "pending",
      title: latest ? "Latest report published" : "No report published yet",
      copy: latest
        ? "The current workflow report is ready to review."
        : "The first report appears here after the analysis pass finishes.",
      meta: latest
        ? `${latestTitle || "Workflow report"} · ${formatDateTime(latest.created_at)}`
        : "This card turns live as soon as the first report is created.",
    },
    {
      step: "04",
      label: "Revisit",
      state: activeStep === "revisit" ? "active" : "pending",
      title: latest
        ? "Use the feedback in the next session"
        : "Follow-up starts after the first report",
      copy: latest
        ? "Apply the guidance, then upload more traces to refresh the cycle."
        : "The next loop will compare future sessions against the first report.",
      meta: latest
        ? firstNextStep || "The next refresh will look for changes in behavior."
        : "New sessions and snapshots will make the next cycle more specific.",
    },
  ];

  $("lifecycleStepper").innerHTML = stages.map(lifecycleStageCard).join("");

  if (!latest) {
    section.dataset.empty = "true";

    const emptyHeadline =
      researchState === "running"
        ? "The first report is being assembled from recent sessions."
        : totalSessions > 0
          ? "The system is still building enough evidence for the first report."
          : "No session traces have been uploaded yet.";
    const emptySummary =
      lifecycleNarrative ||
      (totalSessions > 0
        ? "Keep uploading Codex sessions so Crux can compare user intent, model interpretation, and repeated friction."
        : "Connect the CLI and upload sessions from a workspace to start the analysis cycle.");
    const phaseLabel =
      researchState === "running"
        ? "Analyzing"
        : totalSessions > 0
          ? "Observing"
          : "Setup";

    $("lifecycleOverview").innerHTML = `
      <article class="lifecycle-overview-card is-empty">
        <div class="lifecycle-overview-main">
          <div class="lifecycle-pill-row">
            ${pill(phaseLabel, researchState === "running" ? "sky" : totalSessions > 0 ? "sky" : "warn")}
            ${reportCount > 0 ? pill(`${formatCount(reportCount)} report${reportCount > 1 ? "s" : ""}`, "good") : ""}
          </div>
          <div class="lifecycle-overview-headline">${escapeHTML(emptyHeadline)}</div>
          <div class="lifecycle-overview-copy">${escapeHTML(emptySummary)}</div>
        </div>
        <div class="lifecycle-overview-meta">
          ${lifecycleStat("Sessions captured", formatCount(totalSessions))}
          ${lifecycleStat("Reports ready", formatCount(reportCount))}
          ${lifecycleStat("Current phase", phaseLabel)}
          ${lifecycleStat("Next milestone", totalSessions > 0 ? "First report" : "First upload")}
        </div>
      </article>
    `;

    $("lifecycleGrid").innerHTML = `
      <article class="lifecycle-panel lifecycle-panel-wide">
        <div class="lifecycle-panel-head">
          <div>
            <div class="lifecycle-panel-kicker">What this section will show</div>
            <div class="lifecycle-panel-title">The first report will make the intent gap visible.</div>
          </div>
          ${pill("Coming next", "sky")}
        </div>
        <div class="lifecycle-compare">
          <div class="lifecycle-compare-card" data-tone="intent">
            <div class="lifecycle-compare-label">What you intended</div>
            <div class="lifecycle-empty-note">The report will summarize what you were actually trying to accomplish across recent sessions.</div>
          </div>
          <div class="lifecycle-compare-card" data-tone="model">
            <div class="lifecycle-compare-label">What the model understood</div>
            <div class="lifecycle-empty-note">The report will explain how the model appears to have framed the request and where it drifted.</div>
          </div>
        </div>
      </article>
      <article class="lifecycle-panel">
        <div class="lifecycle-panel-head">
          <div>
            <div class="lifecycle-panel-kicker">What Crux needs</div>
            <div class="lifecycle-panel-title">Grounded evidence, not generic advice</div>
          </div>
          ${pill("Evidence first", "warn")}
        </div>
        <div class="lifecycle-panel-copy">Reports only become useful when they are anchored to recent raw sessions and repeated patterns.</div>
        <div class="lifecycle-chip-list">
          <span class="lifecycle-chip">Recent sessions</span>
          <span class="lifecycle-chip">Raw query evidence</span>
          <span class="lifecycle-chip">Repeated patterns</span>
        </div>
      </article>
      <article class="lifecycle-panel">
        <div class="lifecycle-panel-head">
          <div>
            <div class="lifecycle-panel-kicker">What happens after that</div>
            <div class="lifecycle-panel-title">Readable feedback instead of logs</div>
          </div>
          ${pill("First report", "good")}
        </div>
        <div class="lifecycle-list-grid lifecycle-list-grid-triple">
          ${lifecycleListBlock("Strengths", [], "Good habits will be highlighted once the first report is published.", "good")}
          ${lifecycleListBlock("Frictions", [], "Repeated confusion and missed intent will show up here.", "warn")}
          ${lifecycleListBlock("Next steps", [], "The report will suggest concrete prompting changes to try next.", "accent")}
        </div>
      </article>
    `;
    return;
  }

  section.dataset.empty = "false";

  const evidence = toArray(latest.evidence).filter(Boolean);
  const strengths = toArray(latest.strengths).filter(Boolean);
  const frictions = toArray(latest.frictions).filter(Boolean);
  const nextSteps = toArray(latest.next_steps).filter(Boolean);
  const leadStrength = normalizeInlineText(strengths[0]);
  const leadFriction = normalizeInlineText(frictions[0]);
  const leadNextStep = normalizeInlineText(nextSteps[0]);
  const reason = String(latest.reason || "").trim();
  const explanation = String(latest.explanation || "").trim();
  const expectedBenefit = String(latest.expected_benefit || "").trim();
  const expectedImpact = String(latest.expected_impact || "").trim();
  const risk = String(latest.risk || "").trim();
  const userIntent = String(latest.user_intent || "").trim();
  const modelInterpretation = String(latest.model_interpretation || "").trim();
  const score = Number(latest.score || 0);
  const analysisText = [reason, explanation]
    .filter(Boolean)
    .join(" ");
  const rationaleText = expectedBenefit
    ? [analysisText, `Expected benefit: ${expectedBenefit}`]
        .filter(Boolean)
        .join(" ")
    : analysisText;
  const insightMetrics = [];
  if (score > 0) {
    insightMetrics.push(`Score ${Math.round(score * 100)}%`);
  }
  if (expectedImpact) {
    insightMetrics.push(expectedImpact);
  }
  if (risk) {
    insightMetrics.push(risk);
  }

  $("lifecycleOverview").innerHTML = `
    <article class="lifecycle-overview-card">
      <div class="lifecycle-overview-main">
        <div class="lifecycle-pill-row">
          ${pill("Report ready", "good")}
          ${pill(reportKindLabel(latest.kind), reportKindTone(latest.kind))}
          ${pill(titleize(latest.confidence || "low"), confidenceTone(latest.confidence))}
        </div>
        <div class="lifecycle-overview-headline">${escapeHTML(latestTitle || "Latest workflow report")}</div>
        <div class="lifecycle-overview-copy">${escapeHTML(latestSummary || "The latest report summarizes how recent Codex sessions behaved.")}</div>
        ${
          reason
            ? `<div class="lifecycle-overview-note">${escapeHTML(reason)}</div>`
            : ""
        }
        <div class="lifecycle-overview-caption">${escapeHTML(lifecycleNarrative || (researchState === "running" ? "A fresh analysis pass is already running on the newest sessions." : "Upload more sessions to refresh this report."))}</div>
      </div>
      <div class="lifecycle-overview-meta">
        ${lifecycleStat("Published", formatDateTime(latest.created_at))}
        ${lifecycleStat("Sessions observed", formatCount(totalSessions))}
        ${lifecycleStat("Evidence captured", formatCount(evidence.length))}
        ${lifecycleStat("Next refresh", researchState === "running" ? "In progress" : "After more sessions")}
      </div>
    </article>
  `;

  $("lifecycleGrid").innerHTML = `
    <article class="lifecycle-panel lifecycle-panel-wide">
      <div class="lifecycle-panel-head">
        <div>
          <div class="lifecycle-panel-kicker">Intent read</div>
          <div class="lifecycle-panel-title">How the report read the request</div>
        </div>
        ${pill("Intent gap", "sky")}
      </div>
      <div class="lifecycle-compare">
        <div class="lifecycle-compare-card" data-tone="intent">
          <div class="lifecycle-compare-label">What you intended</div>
          ${
            userIntent
              ? `<div class="lifecycle-compare-text">${escapeHTML(userIntent)}</div>`
              : `<div class="lifecycle-empty-note">This report did not call out the user intent explicitly.</div>`
          }
        </div>
        <div class="lifecycle-compare-card" data-tone="model">
          <div class="lifecycle-compare-label">What the model understood</div>
          ${
            modelInterpretation
              ? `<div class="lifecycle-compare-text">${escapeHTML(modelInterpretation)}</div>`
              : `<div class="lifecycle-empty-note">This report did not describe the model interpretation explicitly.</div>`
          }
        </div>
      </div>
    </article>
    <article class="lifecycle-panel">
      <div class="lifecycle-panel-head">
        <div>
          <div class="lifecycle-panel-kicker">Report rationale</div>
          <div class="lifecycle-panel-title">Why this cycle matters</div>
        </div>
        ${pill(`${formatCount(evidence.length)} evidence`, "sky")}
      </div>
      <div class="lifecycle-panel-copy">${escapeHTML(rationaleText || "The report did not include additional explanation text.")}</div>
      ${
        insightMetrics.length
          ? `<div class="lifecycle-inline-metrics">${insightMetrics.map((item) => `<span class="lifecycle-inline-metric">${escapeHTML(item)}</span>`).join("")}</div>`
          : ""
      }
      <div class="lifecycle-subtitle">Evidence from sessions</div>
      ${lifecycleChipList(evidence, "Evidence snippets will appear here once the report captures grounded observations.")}
    </article>
    <article class="lifecycle-panel">
      <div class="lifecycle-panel-head">
        <div>
          <div class="lifecycle-panel-kicker">Action focus</div>
          <div class="lifecycle-panel-title">What should change next</div>
        </div>
        ${pill("Act on this", "warn")}
      </div>
      <div class="lifecycle-panel-copy">${escapeHTML(expectedImpact || "These are the most actionable parts of the current report.")}</div>
      <div class="lifecycle-summary-stack">
        <div class="lifecycle-summary-row" data-tone="good">
          <div class="lifecycle-summary-label">Keep</div>
          <div class="lifecycle-summary-value">${escapeHTML(leadStrength || "No specific strength was highlighted in the latest report.")}</div>
        </div>
        <div class="lifecycle-summary-row" data-tone="warn">
          <div class="lifecycle-summary-label">Fix</div>
          <div class="lifecycle-summary-value">${escapeHTML(leadFriction || "No concrete friction point was highlighted in the latest report.")}</div>
        </div>
        <div class="lifecycle-summary-row" data-tone="accent">
          <div class="lifecycle-summary-label">Try next</div>
          <div class="lifecycle-summary-value">${escapeHTML(leadNextStep || "Upload more sessions to surface the next concrete experiment.")}</div>
        </div>
      </div>
      <div class="lifecycle-empty-note">Open the Summary tab for the full checklist from this report.</div>
    </article>
  `;
}

function renderActionItems(reports) {
  const html = reports.slice(0, 5).map((item) => {
    const isActive =
      String(item && item.id ? item.id : "") === String(state.activeReportID);
    return `
          <div class="item report-history-item${isActive ? " is-selected" : ""}">
            <div class="item-top">
              <div class="item-title">
                ${escapeHTML(item.title)}
                <small>${escapeHTML(item.summary || "")}</small>
              </div>
              <div class="item-pill-row">
                ${pill(reportKindLabel(item.kind), reportKindTone(item.kind))}
                ${pill(titleize(item.confidence || "low"), "sky")}
              </div>
            </div>
            <div class="step-list">
              <div class="step-line">${escapeHTML(reportSummaryLine(item))}</div>
            </div>
            <div class="action-row">
              <button class="expand-link" type="button" data-action="open-report" data-report-id="${escapeAttr(item.id)}">Open in Summary</button>
            </div>
            ${reportDetailsBlock(item)}
          </div>
        `;
  });

  const section = $("activeReportSection");
  if (html.length > 0) {
    section.dataset.empty = "false";
    $("actionItemList").innerHTML = html.join("");
  } else {
    section.dataset.empty = "true";
    $("actionItemList").innerHTML = emptyState(
      "No analysis reports yet",
      "Upload more Codex sessions from the CLI and the next trace analysis will appear here.",
    );
  }
}

function renderActionFocus(reports, sessions) {
  const target = $("actionFocusCard");
  const reportItems = toArray(reports);
  const activeReport = resolveActiveReport(reportItems);
  if (!activeReport) {
    target.innerHTML = emptyState(
      "No report summary yet",
      "Upload more Codex sessions and the latest report will summarize the user request, the model interpretation, and the reasoning path here.",
    );
    return;
  }

  const activeIndex = Math.max(
    0,
    reportItems.findIndex((item) => item === activeReport),
  );
  const previousReport = reportItems[activeIndex - 1] || null;
  const nextReport = reportItems[activeIndex + 1] || null;
  const summary =
    normalizeInlineText(activeReport.summary) || reportSummaryLine(activeReport);
  const reason = normalizeInlineText(activeReport.reason);
  const explanation = normalizeInlineText(activeReport.explanation);
  const expectedImpact = normalizeInlineText(activeReport.expected_impact);
  const expectedBenefit = normalizeInlineText(activeReport.expected_benefit);
  const strengths = toArray(activeReport.strengths).filter(Boolean);
  const frictions = toArray(activeReport.frictions).filter(Boolean);
  const nextSteps = toArray(activeReport.next_steps).filter(Boolean);
  const supportingNote = [reason, explanation].filter(Boolean).join(" ");
  const evidenceCount = toArray(activeReport.evidence).filter(Boolean).length;
  const userRequest =
    normalizeInlineText(activeReport.user_intent) ||
    latestSessionRequestSummary(sessions) ||
    summary;
  const modelInterpretation =
    normalizeInlineText(activeReport.model_interpretation) ||
    reason ||
    "The report did not spell out how the model framed the request.";
  const reasoningTrace =
    latestReasoningTraceSummary(sessions) ||
    explanation ||
    reason ||
    "No reasoning summary has been captured yet from recent sessions.";

  target.innerHTML = `
    <article class="action-focus-card">
      <div class="action-focus-nav">
        <div class="action-focus-nav-copy">
          <div class="action-focus-nav-label">Report navigation</div>
          <div class="action-focus-nav-meta">Showing report ${escapeHTML(String(activeIndex + 1))} of ${escapeHTML(String(reportItems.length))}${activeReport.created_at ? ` · ${escapeHTML(formatShortDate(activeReport.created_at))}` : ""}</div>
        </div>
        <div class="action-focus-nav-buttons">
          <button class="action-focus-nav-btn" type="button" data-action="cycle-report" data-direction="prev"${previousReport ? "" : " disabled"}>Previous</button>
          <button class="action-focus-nav-btn" type="button" data-action="cycle-report" data-direction="next"${nextReport ? "" : " disabled"}>Next</button>
        </div>
      </div>
      <div class="action-focus-report-strip">
        ${reportItems
          .map((item, index) => {
            const isActive = item === activeReport;
            const label = truncateText(item.title || `Report ${index + 1}`, 34);
            return `<button class="action-focus-report-chip${isActive ? " is-active" : ""}" type="button" data-action="select-report" data-report-id="${escapeAttr(item.id)}" aria-pressed="${isActive ? "true" : "false"}">${escapeHTML(label)}</button>`;
          })
          .join("")}
      </div>
      <div class="action-focus-head">
        <div class="action-focus-copy">
          <div class="action-focus-kicker">Selected report lens</div>
          <div class="action-focus-title">${escapeHTML(activeReport.title || "Current report")}</div>
          <div class="action-focus-summary">${escapeHTML(summary)}</div>
        </div>
        <div class="action-focus-pills">
          ${pill(reportKindLabel(activeReport.kind), reportKindTone(activeReport.kind))}
          ${pill(titleize(activeReport.confidence || "low"), confidenceTone(activeReport.confidence))}
          ${evidenceCount > 0 ? pill(`${formatCount(evidenceCount)} evidence`, "sky") : ""}
        </div>
      </div>
      <div class="action-focus-insight-grid">
        <article class="action-focus-insight-card" data-tone="request">
          <div class="action-focus-insight-label">User requested problem-solving</div>
          <div class="action-focus-insight-text">${escapeHTML(userRequest)}</div>
        </article>
        <article class="action-focus-insight-card" data-tone="model">
          <div class="action-focus-insight-label">How the model understood it</div>
          <div class="action-focus-insight-text">${escapeHTML(modelInterpretation)}</div>
        </article>
        <article class="action-focus-insight-card" data-tone="reasoning">
          <div class="action-focus-insight-label">How the model tried to solve it</div>
          <div class="action-focus-insight-text">${escapeHTML(reasoningTrace)}</div>
        </article>
      </div>
      ${
        expectedImpact || expectedBenefit
          ? `<div class="action-focus-highlight">${escapeHTML(expectedImpact || expectedBenefit)}</div>`
          : ""
      }
      <div class="action-lane-group">
        ${actionLane("Keep", strengths, "No strengths were highlighted in this report yet.", "good")}
        ${actionLane("Fix", frictions, "No concrete friction points were highlighted in this report yet.", "warn")}
        ${actionLane("Try next", nextSteps, "Upload more sessions to surface the next concrete experiment.", "accent")}
      </div>
      ${
        supportingNote
          ? `<div class="action-focus-footnote">${escapeHTML(supportingNote)}</div>`
          : ""
      }
    </article>
  `;
}

function renderImpactTimeline(reports) {
  const target = $("impactTimelineList");
  const reportItems = toArray(reports);
  if (!reportItems.length) {
    target.innerHTML = emptyState(
      "No reports yet",
      "When the analysis engine finishes reading your Codex traces, the latest reports will appear here.",
    );
    return;
  }

  target.innerHTML = reportItems
    .slice(0, 8)
    .map((item) => {
      const createdAt = formatDateTime(item.created_at);
      const confidence = String(item.confidence || "").trim();
      const firstNextStep = toArray(item.next_steps).find(Boolean) || "";
      const isActive =
        String(item && item.id ? item.id : "") === String(state.activeReportID);

      return `
          <div class="item report-history-item${isActive ? " is-selected" : ""}">
            <div class="item-top">
              <div class="item-title">
                ${escapeHTML(item.title || "Feedback report")}
                <small>${escapeHTML(item.summary || firstNextStep || "A new workflow report is ready.")}</small>
              </div>
              ${pill(titleize(confidence || "low"), "sky")}
            </div>
            <div class="step-list">
              <div class="step-line">${escapeHTML(`Generated ${createdAt}`)}</div>
              ${firstNextStep ? `<div class="step-line">${escapeHTML(firstNextStep)}</div>` : ""}
            </div>
            <div class="action-row">
              <button class="expand-link" type="button" data-action="open-report" data-report-id="${escapeAttr(item.id)}">Open in Summary</button>
            </div>
          </div>
        `;
    })
    .join("");
}

function renderAllReports(reports) {
  const target = $("allReportGrid");
  if (!target) {
    return;
  }
  const items = toArray(reports);
  const countEl = $("allReportCount");
  if (countEl) {
    countEl.textContent = items.length + (items.length === 1 ? " report" : " reports");
  }
  if (!items.length) {
    target.innerHTML = emptyState(
      "No reports yet",
      "Upload Codex sessions and reports will appear here once the analysis engine finishes.",
    );
    return;
  }

  target.innerHTML = items
    .map((item, idx) => {
      const isActive =
        String(item && item.id ? item.id : "") === String(state.activeReportID);
      const isSuperseded = item.status === "superseded";
      const date = formatDateTime(item.created_at);
      const summary = String(item.summary || item.explanation || item.reason || "").trim();
      const confidence = String(item.confidence || "").trim();
      const confidencePill = confidence ? pill(titleize(confidence), "sky") : "";
      const statusPill = isSuperseded ? pill("Superseded", "warn") : "";
      return `
        <button class="report-list-card${isActive ? " is-selected" : ""}${isSuperseded ? " is-superseded" : ""}"
                type="button" data-action="open-report" data-report-id="${escapeAttr(item.id)}">
          <span class="report-list-card-index">${idx + 1}</span>
          <span class="report-list-card-body">
            <span class="report-list-card-title">${escapeHTML(item.title || "Feedback report")}</span>
            ${summary ? `<span class="report-list-card-summary">${escapeHTML(summary)}</span>` : ""}
            <span class="report-list-card-footer">
              <span class="report-list-card-date">${escapeHTML(date)}</span>
              ${confidencePill}${statusPill}
            </span>
          </span>
          <span class="report-list-card-right">
            <svg class="report-list-card-arrow" width="16" height="16" viewBox="0 0 16 16" fill="none">
              <path d="M6 3l5 5-5 5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
          </span>
        </button>
      `;
    })
    .join("");
}

function rerenderReportViews() {
  renderActionFocus(state.reportItems, state.sessionItemsFull);
  renderActionItems(state.reportItems);
  renderImpactTimeline(state.reportItems);
  renderAllReports(state.reportItems);
}

function renderSessionSummaries(items) {
  state.sessionItems = items.slice(0, 10);

  if (!items.length) {
    $("sessionSummaryList").innerHTML = emptyState(
      "No sessions uploaded yet",
      "Run `crux session --recent 5` from the CLI to upload your recent AI usage sessions.",
    );
    return;
  }

  $("sessionSummaryList").innerHTML = state.sessionItems
    .map((item, idx) => {
      const expandLinks = [];
      const detailBits = [`Recorded ${formatDateTime(item.timestamp)}`];
      const engine = sessionEngineSummary(item);
      const latency = sessionLatencySummary(item);
      const duration = sessionDurationSummary(item);
      const tools = sessionToolSummary(item);
      const reasoningSummaries = sessionFullReasoningSummaries(item);
      const toolMix = sessionToolMixSummary(item);
      const toolErrorMix = sessionToolErrorMixSummary(item);
      const toolWallTime = Number(item.tool_wall_time_ms || 0);
      const toolWallTimeMix = sessionToolWallTimeSummary(item);
      if (engine) {
        detailBits.push(engine);
      }
      if (latency) {
        detailBits.push(`First response ${latency}`);
      }
      if (duration) {
        detailBits.push(`Span ${duration}`);
      }
      if (tools) {
        detailBits.push(tools);
      }
      if (reasoningSummaries.length > 0) {
        detailBits.push(`Reasoning summary ${formatCount(reasoningSummaries.length)}`);
      }
      if (toolMix) {
        detailBits.push(toolMix);
      }
      if (toolErrorMix) {
        detailBits.push(`Errors ${toolErrorMix}`);
      }
      if (toolWallTime > 0) {
        detailBits.push(`Tool runtime ${formatLatency(toolWallTime)}`);
      }
      if (toolWallTimeMix) {
        detailBits.push(toolWallTimeMix);
      }
      if (isPromptTruncated(item)) {
        expandLinks.push(
          `<button class="expand-link" type="button" data-action="show-full-prompt" data-session-index="${idx}">Show full prompt</button>`,
        );
      }
      if (isResponseTruncated(item)) {
        expandLinks.push(
          `<button class="expand-link" type="button" data-action="show-full-response" data-session-index="${idx}">Show full response</button>`,
        );
      }
      if (reasoningSummaries.length > 0) {
        expandLinks.push(
          `<button class="expand-link" type="button" data-action="show-full-reasoning" data-session-index="${idx}">Show reasoning summaries</button>`,
        );
      }
      const expandRow = expandLinks.length
        ? `<div class="action-row">${expandLinks.join("")}</div>`
        : "";

      return `
          <div class="item">
            <div class="item-top">
              <div class="item-title">
                ${escapeHTML(truncateText(sessionPrimaryRequest(item) || (toArray(item.raw_queries).length > 0 ? "User request" : "Recent work"), 84))}
                <small>${escapeHTML(detailBits.join(" · "))}</small>
              </div>
              ${pill(sessionLabel(item), sessionTone(item))}
            </div>
            <div class="step-list">${sessionSummaryLines(item)
              .slice(0, 7)
              .map((line) => `<div class="step-line">${escapeHTML(line)}</div>`)
              .join("")}</div>
            ${expandRow}
          </div>
        `;
    })
    .join("");
}

function renderImportJobs(overview, items) {
  state.importJobItems = items.slice();

  const summary =
    sessionImportMetricsNarrative(overview && overview.import_job_metrics) ||
    sessionImportNarrative(overview && overview.active_import_job) ||
    (items.length
      ? `Showing ${formatCount(items.length)} recent async import job${items.length === 1 ? "" : "s"}.`
      : "Recent async import jobs and the latest failure summaries for this workspace.");
  $("importHistorySummary").textContent = summary;

  if (!items.length) {
    $("importHistoryList").innerHTML = emptyState(
      "No import jobs yet",
      "Run `crux collect --reset-sessions` when you want to backfill local Codex sessions.",
    );
    return;
  }

  $("importHistoryList").innerHTML = items
    .map((item) => {
      const status = String(item.status || "").trim();
      const uploaded =
        Number(item.uploaded_sessions || 0) + Number(item.updated_sessions || 0);
      const failed = Number(item.failed_sessions || 0);
      const startedAt = item.started_at || item.created_at;
      const duration = item.completed_at
        ? formatDurationBetween(startedAt, item.completed_at)
        : item.started_at
          ? formatDurationBetween(item.started_at, new Date())
          : "";
      const timelineBits = [`Created ${formatDateTime(item.created_at)}`];
      if (item.completed_at) {
        timelineBits.push(`Completed ${formatDateTime(item.completed_at)}`);
        if (duration && duration !== "Not yet") {
          timelineBits.push(`Ran ${duration}`);
        }
      } else if (item.started_at) {
        timelineBits.push(`Started ${formatDateTime(item.started_at)}`);
        if (duration && duration !== "Not yet") {
          timelineBits.push(`Running for ${duration}`);
        }
      }

      const lines = [
        importJobProgressSummary(item),
        `Uploaded ${formatCount(uploaded)} · Failed ${formatCount(failed)}`,
        timelineBits.join(" · "),
      ];
      const failure = importJobFailureDetails(item);
      if (failure) {
        lines.push(`Latest failure: ${failure}`);
      }
      const actionRow = canCancelImportJob(status)
        ? `
            <div class="action-row">
              <button class="secondary-button" type="button" data-action="cancel-import-job" data-job-id="${escapeAttr(item.job_id || "")}">Cancel job</button>
            </div>
          `
        : "";

      return `
          <div class="item">
            <div class="item-top">
              <div class="item-title">
                ${escapeHTML(item.job_id || "Import job")}
                <small>${escapeHTML(`Project backfill job · ${formatCount(Math.max(Number(item.total_sessions || 0), Number(item.received_sessions || 0)))} sessions targeted`)}</small>
              </div>
              ${pill(titleize(status || "pending"), importJobTone(status))}
            </div>
            <div class="step-list">${lines
              .filter(Boolean)
              .map((line) => `<div class="step-line">${escapeHTML(line)}</div>`)
              .join("")}</div>
            ${actionRow}
          </div>
        `;
    })
    .join("");
}

function renderImportJobFailures(items) {
  state.failedImportJobItems = items.slice();

  const failureJobs = items.length;
  const failureSessions = items.reduce(
    (sum, item) => sum + Number(item.failed_sessions || 0),
    0,
  );
  $("importFailureSummary").textContent = failureJobs
    ? `Showing ${formatCount(failureJobs)} recent degraded import job${failureJobs === 1 ? "" : "s"} covering ${formatCount(failureSessions)} failed session${failureSessions === 1 ? "" : "s"}.`
    : "No recent failed imports. Recent degraded jobs will show their failed sessions here.";

  if (!items.length) {
    $("importFailureList").innerHTML = emptyState(
      "No recent failed imports",
      "When a backfill import degrades or fails, the failed sessions and API errors will show up here.",
    );
    return;
  }

  $("importFailureList").innerHTML = items
    .map((item) => {
      const failures = toArray(item.failures);
      const preview = failures
        .slice(0, 3)
        .map((failure) => importJobFailureLine(failure))
        .filter(Boolean);
      const extraFailures = Math.max(failures.length - preview.length, 0);
      const detailBits = [
        `${formatCount(Number(item.failed_sessions || failures.length || 0))} failed`,
      ];
      const uploaded =
        Number(item.uploaded_sessions || 0) + Number(item.updated_sessions || 0);
      if (uploaded > 0) {
        detailBits.push(`${formatCount(uploaded)} uploaded`);
      }
      if (item.completed_at) {
        detailBits.push(`Completed ${formatDateTime(item.completed_at)}`);
      } else {
        detailBits.push(`Created ${formatDateTime(item.created_at)}`);
      }

      const lines = [
        importJobProgressSummary(item),
        ...preview,
      ];
      if (!preview.length) {
        const lastError = importJobFailureDetails(item);
        if (lastError) {
          lines.push(lastError);
        }
      }
      if (extraFailures > 0) {
        lines.push(`+${formatCount(extraFailures)} more failed session${extraFailures === 1 ? "" : "s"}`);
      }

      return `
          <div class="item">
            <div class="item-top">
              <div class="item-title">
                ${escapeHTML(item.job_id || "Import job")}
                <small>${escapeHTML(detailBits.join(" · "))}</small>
              </div>
              ${pill(titleize(item.status || "failed"), importJobTone(item.status))}
            </div>
            <div class="step-list">${lines
              .filter(Boolean)
              .map((line) => `<div class="step-line">${escapeHTML(line)}</div>`)
              .join("")}</div>
          </div>
        `;
    })
    .join("");
}

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
                <button class="secondary-button" type="button" data-action="revoke-cli-token" data-token-id="${escapeAttr(item.token_id)}">Revoke token</button>
              </div>
            `
                : ""
            }
          </div>
        `;
    })
    .join("");
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

      const wizOutput = $("wizTokenOutput");
      if (wizOutput) {
        wizOutput.textContent = data.token || "Token was issued.";
      }

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

async function cancelImportJob(jobID) {
  const selectedJobID = String(jobID || "").trim();
  if (!selectedJobID) {
    return;
  }

  try {
    await withBusy(async () => {
      await requestJSON(
        `/api/v1/session-import-jobs/${encodeURIComponent(selectedJobID)}/cancel`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({}),
        },
        "Failed to cancel the import job.",
      );
      await load({ skipBusy: true });
      setStatus(`Canceled import job ${selectedJobID}.`);
    });
  } catch (error) {
    if (isUnauthorized(error)) {
      redirectToLanding("Your session expired. Sign in again.");
      return;
    }
    setStatus(
      error instanceof Error
        ? error.message
        : "Failed to cancel the import job.",
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

async function load(options = {}) {
  const manageBusy = !options.skipBusy;
  if (manageBusy && state.busy) {
    return;
  }

  const orgID =
    state.session && state.session.organization
      ? state.session.organization.id
      : "";
  if (!orgID) {
    redirectToLanding("Sign in again to open the dashboard.");
    return;
  }

  if (manageBusy) {
    state.busy = true;
    syncBusyUI();
  }

  setStatus("Loading workspace signals...");

  try {
    const [overview, projectsData, cliTokensData] = await Promise.all([
      requestJSON(
        `/api/v1/dashboard/overview?org_id=${encodeURIComponent(orgID)}`,
        {},
        "Failed to load the dashboard overview.",
      ),
      requestJSON(
        `/api/v1/projects?org_id=${encodeURIComponent(orgID)}`,
        {},
        "Failed to load the shared workspace.",
      ),
      requestJSON(
        "/api/v1/auth/cli-tokens",
        {},
        "Failed to load issued CLI tokens.",
      ),
    ]);

    const projects = toArray(projectsData.items);
    const projectID = projects[0] ? projects[0].id : "";
    state.selectedProjectID = projectID;
    renderSessionContext();

    renderOverview(overview);
    renderCLITokens(toArray(cliTokensData.items));

    state.reportIndex = new Map();

    let reports = [];
    let sessions = [];
    let insights = {};
    let snapshots = [];
    let audits = [];
    let failedImports = [];
    let imports = [];

    if (projectID) {
      const [
        reportsData,
        sessionData,
        insightsData,
        snapshotData,
        auditData,
        failedImportsData,
        importsData,
      ] = await Promise.all([
        requestJSON(
          `/api/v1/reports?project_id=${encodeURIComponent(projectID)}`,
          {},
          "Failed to load workspace reports.",
        ),
        requestJSON(
          `/api/v1/session-summaries?project_id=${encodeURIComponent(projectID)}&limit=10`,
          {},
          "Failed to load recent sessions.",
        ),
        requestJSON(
          `/api/v1/dashboard/project-insights?project_id=${encodeURIComponent(projectID)}`,
          {},
          "Failed to load usage trends.",
        ),
        requestJSON(
          `/api/v1/config-snapshots?project_id=${encodeURIComponent(projectID)}`,
          {},
          "Failed to load config snapshots.",
        ),
        requestJSON(
          `/api/v1/audits?org_id=${encodeURIComponent(orgID)}&project_id=${encodeURIComponent(projectID)}`,
          {},
          "Failed to load workspace activity.",
        ),
        requestJSON(
          `/api/v1/session-import-jobs?project_id=${encodeURIComponent(projectID)}&failed_only=true&limit=6`,
          {},
          "Failed to load recent failed import jobs.",
        ),
        requestJSON(
          `/api/v1/session-import-jobs?project_id=${encodeURIComponent(projectID)}&limit=6`,
          {},
          "Failed to load recent import jobs.",
        ),
      ]);

      reports = toArray(reportsData.items);
      sessions = toArray(sessionData.items);
      insights = insightsData || {};
      snapshots = toArray(snapshotData.items);
      audits = toArray(auditData.items);
      failedImports = toArray(failedImportsData.items);
      imports = toArray(importsData.items);

      reports.forEach((item) => {
        state.reportIndex.set(item.id, item);
      });
    }

    state.reportItems = reports.slice();
    state.sessionItemsFull = sessions.slice();
    if (!reports.length) {
      setActiveReportID("");
    } else if (
      !state.activeReportID ||
      !reports.some(
        (item) => String(item && item.id ? item.id : "") === state.activeReportID,
      )
    ) {
      setActiveReportID(reports[0].id);
    }

    renderAgentStatus(overview, reports);
    renderOptimizationLoop(overview, reports);
    renderActionFocus(reports, sessions);
    renderActionItems(reports);
    renderImpactTimeline(reports);
    renderAllReports(reports);
    renderUsageTrend(insights);
    renderTrendCoverage(insights);
    renderModelCoverage(insights);
    renderProviderCoverage(insights);
    renderLatencyTrend(insights);
    renderAssistantToolDetails(insights);
    renderTimeComposition(insights);
    renderToolHotspots(insights);
    renderToolTrend(insights);
    renderDurationTrend(insights);
    renderCoverageActions(insights);
    renderImportJobs(overview, imports);
    renderImportJobFailures(failedImports);
    renderHeavySessions(sessions);
    renderSnapshots(snapshots);
    renderActivityTimeline(audits);
    renderSessionSummaries(sessions);

    const shouldShowWizard =
      !projectID && readStorage(STORAGE_KEYS.onboardingDone) !== "1";
    if (shouldShowWizard) {
      showWizard();
    } else {
      hideWizard();
    }

    if (projectID) {
      const workspaceName =
        projects.find((item) => item.id === projectID)?.name ||
        "Shared workspace";
      setStatus(
        `Showing ${workspaceName}. Review the latest Codex trace analysis reports.`,
      );
    } else {
      setStatus(
        "No workspace connected yet. Run the CLI to start uploading sessions.",
      );
    }
  } catch (error) {
    if (isUnauthorized(error)) {
      redirectToLanding("Your session expired. Sign in again.");
      return;
    }
    setStatus(
      error instanceof Error ? error.message : "Failed to load the dashboard.",
      true,
    );
  } finally {
    if (manageBusy) {
      state.busy = false;
      syncBusyUI();
    }
  }
}

/* ── Collapsible sections ── */

function toggleSection(targetID) {
  const section = $(targetID);
  if (!section) {
    return;
  }
  section.classList.toggle("is-collapsed");
}

function toggleReportDetail(button) {
  const detail = button.closest(".report-detail");
  if (!detail) {
    return;
  }
  detail.classList.toggle("is-open");
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
    case "toggle-section":
      toggleSection(button.dataset.target || "");
      break;
    case "refresh-dashboard":
      load();
      break;
    case "issue-cli-token":
    case "wizard-issue-token":
      issueCLIToken();
      break;
    case "revoke-cli-token":
      revokeCLIToken(button.dataset.tokenId || "");
      break;
    case "cancel-import-job":
      cancelImportJob(button.dataset.jobId || "");
      break;
    case "switch-tab":
      setActiveTab(button.dataset.tab || "overview");
      break;
    case "switch-report-panel":
      setActiveReportPanel(button.dataset.reportPanel || "actions");
      break;
    case "switch-report-view": {
      const view = button.dataset.view || "list";
      const grid = $("allReportGrid");
      if (grid) {
        grid.classList.toggle("is-grid-view", view === "grid");
      }
      document.querySelectorAll("[data-action='switch-report-view']").forEach(function (b) {
        b.classList.toggle("is-active", b.dataset.view === view);
      });
      break;
    }
    case "toggle-report-panel":
      setActiveReportPanel(button.dataset.targetPanel || "history");
      break;
    case "select-report":
      setActiveReportID(button.dataset.reportId || "");
      rerenderReportViews();
      break;
    case "open-report":
      setActiveReportID(button.dataset.reportId || "");
      setActiveReportPanel("actions");
      rerenderReportViews();
      break;
    case "cycle-report": {
      const reports = state.reportItems;
      const activeReport = resolveActiveReport(reports);
      const activeIndex = Math.max(
        0,
        reports.findIndex((item) => item === activeReport),
      );
      const nextIndex =
        button.dataset.direction === "prev" ? activeIndex - 1 : activeIndex + 1;
      const nextReport = reports[nextIndex] || null;
      if (nextReport) {
        setActiveReportID(nextReport.id);
        rerenderReportViews();
      }
      break;
    }
    case "copy-command":
      copyCommand(
        button.dataset.copyTarget || "",
        button.dataset.copyLabel || "command",
      );
      break;
    case "sign-out":
      signOut();
      break;
    case "wizard-next":
      setWizardStep(state.wizardStep + 1);
      break;
    case "wizard-back":
      setWizardStep(state.wizardStep - 1);
      break;
    case "show-full-prompt":
      showFullPrompt(Number(button.dataset.sessionIndex || 0));
      break;
    case "show-full-response":
      showFullResponse(Number(button.dataset.sessionIndex || 0));
      break;
    case "show-full-reasoning":
      showFullReasoning(Number(button.dataset.sessionIndex || 0));
      break;
    case "toggle-report-detail":
      toggleReportDetail(button);
      break;
    case "close-full-text":
      closeFullTextModal();
      break;
    case "wizard-done":
    case "skip-wizard":
      hideWizard();
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
});

async function boot() {
  restorePreferences();
  updateWizardCommands();
  setActiveTab(state.activeTab);
  setActiveReportPanel(state.activeReportPanel);
  syncBusyUI();

  $("onboardingWizard").classList.add("is-hidden");
  $("mainDashboard").classList.add("is-visible");

  if (!(await ensureSession())) {
    return;
  }

  load();
}

boot();
