const STORAGE_KEYS = {
  sessionUser: "agentopt_session_user",
  sessionOrg: "agentopt_session_org",
  activeTab: "agentopt_dashboard_tab",
  onboardingDone: "agentopt_onboarding_done",
};
const TAB_IDS = ["overview", "trends", "rollouts", "sessions", "cli"];
const WIZARD_STEPS = 4;

const state = {
  busy: false,
  activeTab: "overview",
  selectedProjectID: "",
  recommendationIndex: new Map(),
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

function updateWizardCommands() {
  const origin = window.location.origin || "http://127.0.0.1:8082";
  const wizLogin = $("wizLoginCmd");
  if (wizLogin) {
    wizLogin.textContent = `agentopt login --server ${origin}`;
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
  ].forEach((key) => writeStorage(key, ""));
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

  $("topBarUser").textContent = user.name || user.email || "-";
  $("topBarOrg").textContent = org.name || org.id || "-";
}

function renderAgentStatus(overview, recommendations, applyItems, experiments) {
  const bar = $("agentStatusBar");
  const text = $("agentStatusText");
  const activeRecs = Number(overview.active_recommendations || 0);
  const totalSessions = Number(overview.total_sessions || 0);
  const research = overview && overview.research_status;
  const researchState = String((research && research.state) || "")
    .trim()
    .toLowerCase();
  const experimentItems = toArray(experiments);
  const measuringCount = experimentItems.filter(
    (item) => String(item.status || "").toLowerCase() === "measuring",
  ).length;
  const rollbackCount = experimentItems.filter(
    (item) => String(item.status || "").toLowerCase() === "rollback_requested",
  ).length;

  if (rollbackCount > 0) {
    bar.dataset.state = "suggestion";
    text.textContent = `Rollback requested — ${rollbackCount} experiment${rollbackCount > 1 ? "s" : ""} will restore the previous config on the next sync`;
  } else if (researchState === "running") {
    bar.dataset.state = "measuring";
    text.textContent =
      recommendationResearchNarrative(research) ||
      "Fetching recommendation suggestions from uploaded sessions";
  } else if (activeRecs > 0) {
    bar.dataset.state = "suggestion";
    text.textContent = `Suggestion ready \u2014 ${activeRecs} optimization${activeRecs > 1 ? "s" : ""} awaiting your review`;
  } else if (measuringCount > 0) {
    bar.dataset.state = "measuring";
    text.textContent = `Measuring impact of ${measuringCount} applied change${measuringCount > 1 ? "s" : ""} \u2014 collecting post-apply sessions`;
  } else if (
    researchState === "waiting_for_min_sessions" ||
    researchState === "disabled" ||
    researchState === "failed" ||
    researchState === "no_recommendations"
  ) {
    bar.dataset.state = "";
    text.textContent =
      recommendationResearchNarrative(research) ||
      `Observing \u2014 analyzed ${totalSessions} session${totalSessions > 1 ? "s" : ""}`;
  } else if (totalSessions === 0) {
    bar.dataset.state = "";
    text.textContent =
      "Waiting for sessions \u2014 connect a workspace to start observing";
  } else {
    bar.dataset.state = "";
    text.textContent = `Observing \u2014 analyzed ${totalSessions} session${totalSessions > 1 ? "s" : ""}, researching patterns`;
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
      window.sessionStorage.setItem("agentopt_redirect_notice", message);
    } catch (error) {
      // Ignore sessionStorage failures and continue the redirect.
    }
  }
  window.location.replace("/");
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

function applyTone(status) {
  const raw = String(status || "").toLowerCase();
  if (raw === "applied") {
    return "good";
  }
  if (raw === "rollback_confirmed" || raw === "failed" || raw === "rejected") {
    return "danger";
  }
  if (raw === "approved_for_local_apply" || raw === "awaiting_review") {
    return "warn";
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

function recommendationPlanSummary(item) {
  const steps = toArray(item.change_plan);
  if (!steps.length) {
    return "One settings update is ready to review.";
  }
  if (steps.length === 1) {
    return stepSummary(steps[0]);
  }
  return `${steps.length} coordinated updates will be applied together.`;
}

function patchPreviewSummary(items) {
  const steps = toArray(items);
  if (!steps.length) {
    return "One reviewed update is queued for this workspace.";
  }
  if (steps.length === 1) {
    return stepSummary(steps[0]);
  }
  return `${steps.length} reviewed updates will land together.`;
}

function recommendationTitle(recommendationID) {
  const recommendation = state.recommendationIndex.get(recommendationID);
  return recommendation ? recommendation.title : "Configuration update";
}

function recommendationSummary(recommendationID) {
  const recommendation = state.recommendationIndex.get(recommendationID);
  return recommendation
    ? recommendation.summary
    : "A reviewed configuration update for this workspace.";
}

function recommendationKind(recommendationID) {
  const recommendation = state.recommendationIndex.get(recommendationID);
  return recommendation ? String(recommendation.kind || "").trim() : "";
}

function recommendationRawSuggestion(recommendationID) {
  const recommendation = state.recommendationIndex.get(recommendationID);
  return recommendation
    ? String(recommendation.raw_suggestion || "").trim()
    : "";
}

function rawSuggestionBlock(rawSuggestion) {
  const text = String(rawSuggestion || "").trim();
  if (!text) {
    return "";
  }
  return `
        <details class="raw-suggestion">
          <summary>LLM raw suggestion</summary>
          <pre>${escapeHTML(text)}</pre>
        </details>
      `;
}

function suggestionDetailsBlock(item) {
  const sections = [];

  const reason = String(item.reason || "").trim();
  const explanation = String(item.explanation || "").trim();
  const expectedBenefit = String(item.expected_benefit || "").trim();
  const risk = String(item.risk || "").trim();
  const expectedImpact = String(item.expected_impact || "").trim();
  const score = Number(item.score || 0);
  const evidence = toArray(item.evidence).filter(Boolean);
  const changePlan = toArray(item.change_plan);
  const rawSuggestion = String(item.raw_suggestion || "").trim();

  const hasContent = reason || explanation || expectedBenefit || evidence.length || changePlan.length;
  if (!hasContent && !rawSuggestion) {
    return "";
  }

  if (reason || explanation || expectedBenefit || risk || expectedImpact || score > 0) {
    const metricsHTML = [];
    if (score > 0) {
      const pct = Math.round(score * 100);
      metricsHTML.push(`<span class="suggestion-metric">Score <span class="suggestion-score-track"><span class="suggestion-score-fill" style="width:${pct}%"></span></span> ${pct}%</span>`);
    }
    if (expectedImpact) {
      metricsHTML.push(`<span class="suggestion-metric">${escapeHTML(expectedImpact)}</span>`);
    }
    if (risk) {
      metricsHTML.push(`<span class="suggestion-metric">Risk: ${escapeHTML(risk)}</span>`);
    }
    if (metricsHTML.length) {
      sections.push(`<div class="suggestion-metrics-row">${metricsHTML.join("")}</div>`);
    }

    if (reason) {
      sections.push(`<div class="suggestion-field"><div class="suggestion-field-label">Reason</div><div class="suggestion-field-value">${escapeHTML(reason)}</div></div>`);
    }
    if (explanation) {
      sections.push(`<div class="suggestion-field"><div class="suggestion-field-label">Explanation</div><div class="suggestion-field-value">${escapeHTML(explanation)}</div></div>`);
    }
    if (expectedBenefit) {
      sections.push(`<div class="suggestion-field"><div class="suggestion-field-label">Expected benefit</div><div class="suggestion-field-value">${escapeHTML(expectedBenefit)}</div></div>`);
    }
  }

  if (evidence.length) {
    const evidenceItems = evidence.map((e) => `<div class="suggestion-evidence-item">${escapeHTML(e)}</div>`).join("");
    sections.push(`<div class="suggestion-field"><div class="suggestion-field-label">Evidence (${evidence.length})</div><div class="suggestion-evidence-list">${evidenceItems}</div></div>`);
  }

  if (changePlan.length) {
    const planSteps = changePlan.map((step) => {
      const stepType = String(step.type || step.action || "").trim();
      const stepFile = String(step.target_file || step.file_path || "").trim();
      const stepSummaryText = String(step.summary || "").trim();
      const stepPreview = String(step.content_preview || "").trim();

      let headerHTML = "";
      if (stepType) {
        headerHTML += `<span class="suggestion-plan-step-type">${escapeHTML(stepType)}</span>`;
      }
      if (stepFile) {
        headerHTML += `<span class="suggestion-plan-step-file">${escapeHTML(stepFile)}</span>`;
      }

      let stepHTML = "";
      if (headerHTML) {
        stepHTML += `<div class="suggestion-plan-step-header">${headerHTML}</div>`;
      }
      if (stepSummaryText) {
        stepHTML += `<div class="suggestion-plan-step-summary">${escapeHTML(stepSummaryText)}</div>`;
      }
      if (stepPreview) {
        stepHTML += `<div class="suggestion-plan-step-preview">${escapeHTML(stepPreview)}</div>`;
      }

      return `<div class="suggestion-plan-step">${stepHTML}</div>`;
    }).join("");
    sections.push(`<div class="suggestion-field"><div class="suggestion-field-label">Change plan (${changePlan.length} step${changePlan.length > 1 ? "s" : ""})</div><div class="suggestion-change-plan">${planSteps}</div></div>`);
  }

  if (rawSuggestion) {
    sections.push(rawSuggestionBlock(rawSuggestion));
  }

  if (!sections.length) {
    return "";
  }

  return `
    <div class="suggestion-detail">
      <button class="suggestion-detail-toggle" type="button" data-action="toggle-suggestion-detail"><span class="toggle-icon">&#9654;</span> View full suggestion details</button>
      <div class="suggestion-detail-body">${sections.join("")}</div>
    </div>
  `;
}

function recommendationKindLabel(kind) {
  const value = String(kind || "").toLowerCase();
  if (value.includes("instruction")) {
    return "Custom instruction";
  }
  if (value.includes("skill")) {
    return "Codex skill";
  }
  if (value.includes("mcp")) {
    return "MCP baseline";
  }
  if (value.includes("config")) {
    return "Agent config";
  }
  return "Suggestion";
}

function recommendationKindTone(kind) {
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

function experimentTone(status) {
  const value = String(status || "").toLowerCase();
  if (value === "completed" || value === "rolled_back") {
    return "good";
  }
  if (
    value === "rollback_requested" ||
    value === "rollback_failed" ||
    value === "failed"
  ) {
    return "danger";
  }
  if (
    value === "measuring" ||
    value === "awaiting_review" ||
    value === "queued_for_apply"
  ) {
    return "warn";
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
  const research = recommendationResearchNarrative(
    overview && overview.research_status,
  );
  const harness = harnessNarrative(overview);
  const input = Number(overview.avg_input_tokens_per_query || 0);
  const output = Number(overview.avg_output_tokens_per_query || 0);
  const tokenRead =
    input > 0 || output > 0
      ? input >= output
        ? " Prompt-side token usage is currently the larger share of each captured query."
        : " Response-side token usage is currently the larger share of each captured query."
      : "";
  const combined = `${action} ${outcome} ${research} ${harness}${tokenRead}`.trim();
  return (
    combined ||
    "AgentOpt is collecting enough setup and session context to produce steadier recommendations."
  );
}

function harnessNarrative(item) {
  const harnessRuns = Number((item && item.harness_run_count) || 0);
  if (harnessRuns <= 0) {
    return "";
  }
  const latestStatus = titleize(String((item && item.latest_harness_status) || "passed"));
  const latestName = String((item && item.latest_harness_name) || "").trim();
  const latestAt = item && item.latest_harness_at;
  const namePart = latestName ? ` for ${latestName}` : "";
  const timePart = latestAt ? ` at ${formatDateTime(latestAt)}` : "";
  return `Latest harness run ${latestStatus}${namePart}${timePart}.`;
}

function recommendationResearchNarrative(status) {
  const item = status || {};
  const state = String(item.state || "").trim().toLowerCase();
  const summary = String(item.summary || "").trim();
  if (summary) {
    return summary;
  }
  if (state === "waiting_for_min_sessions") {
    return `Recommendation research starts after ${formatCount(item.minimum_sessions || 0)} sessions.`;
  }
  if (state === "disabled") {
    return "OpenAI-backed recommendation research is disabled on this server.";
  }
  if (state === "running") {
    return "The server is currently fetching new recommendations.";
  }
  if (state === "succeeded" || state === "no_recommendations") {
    const duration = Number(item.last_duration_ms || 0);
    return duration > 0
      ? `The last recommendation refresh took ${formatLatency(duration)}.`
      : "The last recommendation refresh finished recently.";
  }
  if (state === "failed") {
    return "The last recommendation refresh failed.";
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

function sessionSummaryLines(item) {
  const queries = toArray(item.raw_queries);
  const engineSummary = sessionEngineSummary(item);
  const latestReply = sessionLatestReply(item);
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
      "Repeated phrasing here can be turned into a shared harness or workflow default.",
    );
  } else {
    lines.push(
      "More raw queries will make the next workflow recommendation more specific.",
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
        "No workspace sessions are visible yet. Enable the daemon with bootstrap to seed the charts from existing local sessions, then keep them current automatically.",
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

function recentRolloutCounts(recommendations, reviewQueue, applyItems) {
  return {
    activeSuggestions: recommendations.filter(
      (item) => String(item.status || "").toLowerCase() === "active",
    ).length,
    awaitingReview: reviewQueue.length,
    readyForSync: applyItems.filter(
      (item) =>
        String(item.status || "").toLowerCase() === "approved_for_local_apply",
    ).length,
    applied: applyItems.filter(
      (item) => String(item.status || "").toLowerCase() === "applied",
    ).length,
    rolledBack: applyItems.filter(
      (item) =>
        item.rolled_back ||
        String(item.status || "").toLowerCase() === "rollback_confirmed",
    ).length,
    rejected: applyItems.filter(
      (item) => String(item.status || "").toLowerCase() === "rejected",
    ).length,
  };
}

function average(values) {
  if (!values.length) {
    return null;
  }
  return values.reduce((sum, value) => sum + value, 0) / values.length;
}

function rolloutPulseStats(applyItems) {
  const approvalMinutes = [];
  const syncMinutes = [];
  const leadMinutes = [];
  let rollbackCount = 0;
  let completedCount = 0;

  applyItems.forEach((item) => {
    const status = String(item.status || "").toLowerCase();
    const approval = minutesBetween(item.requested_at, item.reviewed_at);
    const sync = minutesBetween(
      item.reviewed_at || item.requested_at,
      item.applied_at,
    );
    const lead = minutesBetween(item.requested_at, item.applied_at);
    if (approval != null) {
      approvalMinutes.push(approval);
    }
    if (sync != null) {
      syncMinutes.push(sync);
    }
    if (lead != null) {
      leadMinutes.push(lead);
    }
    if (
      status === "applied" ||
      status === "rollback_confirmed" ||
      item.rolled_back
    ) {
      completedCount++;
    }
    if (status === "rollback_confirmed" || item.rolled_back) {
      rollbackCount++;
    }
  });

  return {
    avgApprovalMinutes: average(approvalMinutes),
    avgSyncMinutes: average(syncMinutes),
    avgLeadMinutes: average(leadMinutes),
    rollbackRate: completedCount ? rollbackCount / completedCount : 0,
    completedCount,
  };
}

function rolloutReviewStageTone(item) {
  const status = String(item.status || "").toLowerCase();
  if (item.reviewed_at) {
    if (
      String(item.decision || "").toLowerCase() === "reject" ||
      status === "rejected"
    ) {
      return "danger";
    }
    return "done";
  }
  if (status === "awaiting_review") {
    return "warn";
  }
  return "pending";
}

function rolloutApplyStageTone(item) {
  const status = String(item.status || "").toLowerCase();
  if (status === "applied") {
    return "done";
  }
  if (
    status === "rollback_confirmed" ||
    status === "failed" ||
    status === "rejected" ||
    item.rolled_back
  ) {
    return "danger";
  }
  if (status === "approved_for_local_apply") {
    return "warn";
  }
  return "pending";
}

function auditTitle(item) {
  const type = String(item.type || "");
  if (type === "execution.result" && item.message) {
    return `Execution ${titleize(item.message)}`;
  }
  if (type === "harness.run" && item.message) {
    return `Harness ${titleize(item.message)}`;
  }
  return titleize(type || "activity");
}

/* ── Render ── */

function renderOverview(overview) {
  const activeRecs = Number(overview.active_recommendations || 0);
  const harnessRuns = Number(overview.harness_run_count || 0);
  const harnessFailures = Number(overview.harness_failure_count || 0);
  const latestHarnessStatus = String(overview.latest_harness_status || "").trim();
  const lastFailingHarness = String(
    overview.last_failing_harness_name || "",
  ).trim();
  const research = overview && overview.research_status;
  const researchState = String((research && research.state) || "")
    .trim()
    .toLowerCase();
  $("activeRecommendations").textContent = formatCount(activeRecs);
  $("totalSessions").textContent = formatCount(overview.total_sessions);
  $("avgInputTokensPerQuery").textContent = formatCount(
    Math.round(Number(overview.avg_input_tokens_per_query || 0)),
  );
  $("avgOutputTokensPerQuery").textContent = formatCount(
    Math.round(Number(overview.avg_output_tokens_per_query || 0)),
  );

  const totalSessions = Number(overview.total_sessions || 0);

  $("activeRecommendationsMeta").textContent =
    activeRecs === 0
      ? recommendationResearchNarrative(research) ||
        "No suggestions yet. Upload more sessions to generate recommendations."
      : `${formatCount(activeRecs)} configuration suggestion(s) from the analysis engine.`;
  $("totalSessionsMeta").textContent =
    totalSessions === 0
      ? "Upload sessions from the CLI to start tracking AI usage."
      : researchState === "waiting_for_min_sessions"
        ? `${formatCount(totalSessions)} AI usage session(s) collected so far. The first recommendation refresh starts at ${formatCount(research && research.minimum_sessions)} sessions.`
        : `${formatCount(totalSessions)} AI usage session(s) collected from the CLI so far.`;
  $("avgTokensMeta").textContent =
    `${formatCount(overview.total_input_tokens || 0)} input / ${formatCount(overview.total_output_tokens || 0)} output tokens uploaded so far.`;
  $("harnessPassRate").textContent =
    harnessRuns > 0 ? formatPercent(overview.harness_pass_rate || 0) : "None";
  $("harnessPassRateMeta").textContent =
    harnessRuns === 0
      ? "No repo-local harness runs uploaded yet."
      : latestHarnessStatus
        ? `${titleize(latestHarnessStatus)} on ${formatCount(harnessRuns)} run(s); ${formatCount(harnessFailures)} failing run(s)${lastFailingHarness ? ` · latest failing spec: ${lastFailingHarness}` : ""}.`
        : `${formatCount(harnessRuns)} harness run(s) uploaded so far.`;
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
      if (Number(item.approval_count || 0) > 0) {
        flags.push(
          `<span class="usage-flag approval" title="${escapeAttr(`${formatCount(item.approval_count)} approval(s)`)}"></span>`,
        );
      }
      if (Number(item.applied_count || 0) > 0) {
        flags.push(
          `<span class="usage-flag applied" title="${escapeAttr(`${formatCount(item.applied_count)} rollout(s) applied`)}"></span>`,
        );
      }
      if (Number(item.rollback_count || 0) > 0) {
        flags.push(
          `<span class="usage-flag rollback" title="${escapeAttr(`${formatCount(item.rollback_count)} rollback(s)`)}"></span>`,
        );
      }
      if (Number(item.snapshot_count || 0) > 0) {
        flags.push(
          `<span class="usage-flag snapshot" title="${escapeAttr(`${formatCount(item.snapshot_count)} config snapshot(s)`)}"></span>`,
        );
      }
      if (Number(item.harness_pass_count || 0) > 0) {
        flags.push(
          `<span class="usage-flag harness-pass" title="${escapeAttr(`${formatCount(item.harness_pass_count)} harness pass run(s)`)}"></span>`,
        );
      }
      if (Number(item.harness_fail_count || 0) > 0) {
        flags.push(
          `<span class="usage-flag harness-fail" title="${escapeAttr(`${formatCount(item.harness_fail_count)} harness fail run(s)`)}"></span>`,
        );
      }
      const meta = `${formatCount(item.session_count || 0)} sess`;
      const tooltip = `${item.day}: ${formatCount(input)} input / ${formatCount(output)} output / ${formatCount(total)} total tokens across ${formatCount(item.query_count || 0)} queries.`;
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
  const harnessRuns = Number((insights && insights.harness_run_count) || 0);
  const harnessFails = Number((insights && insights.harness_fail_count) || 0);
  const latestHarnessStatus = String(
    (insights && insights.latest_harness_status) || "",
  ).trim();
  const latestHarnessName = String(
    (insights && insights.latest_harness_name) || "",
  ).trim();
  const lastFailingHarness = String(
    (insights && insights.last_failing_harness_name) || "",
  ).trim();
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
    {
      label: "Harness pass rate",
      value:
        harnessRuns > 0
          ? formatPercent((insights && insights.harness_pass_rate) || 0)
          : "None",
      meta:
        harnessRuns > 0
          ? `${formatCount(harnessRuns - harnessFails)} pass / ${formatCount(harnessFails)} fail`
          : "No harness runs uploaded yet",
    },
    {
      label: "Latest harness",
      value:
        latestHarnessStatus && latestHarnessName
          ? `${titleize(latestHarnessStatus)}`
          : "None",
      meta:
        latestHarnessName
          ? `${latestHarnessName}${lastFailingHarness && latestHarnessStatus === "failed" ? ` · latest failing: ${lastFailingHarness}` : ""}`
          : "Repo-local harness state unavailable",
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

function renderRolloutFunnel(recommendations, reviewQueue, applyItems) {
  const counts = recentRolloutCounts(recommendations, reviewQueue, applyItems);
  const cards = [
    {
      label: "Active",
      value: counts.activeSuggestions,
      meta: "Suggestions still open for review.",
    },
    {
      label: "Needs Review",
      value: counts.awaitingReview,
      meta: "Change plans waiting on a decision.",
    },
    {
      label: "Ready",
      value: counts.readyForSync,
      meta: "Approved and ready for the next local sync.",
    },
    {
      label: "Applied",
      value: counts.applied,
      meta: "Changes that completed local execution.",
    },
    {
      label: "Rolled Back",
      value: counts.rolledBack,
      meta: "Rollouts that were later reversed.",
    },
    {
      label: "Rejected",
      value: counts.rejected,
      meta: "Plans explicitly declined in review.",
    },
  ];

  $("rolloutFunnel").innerHTML = cards
    .map(
      (item) => `
        <div class="funnel-card">
          <div class="funnel-label">${escapeHTML(item.label)}</div>
          <div class="funnel-value">${escapeHTML(formatCount(item.value))}</div>
          <div class="funnel-meta">${escapeHTML(item.meta)}</div>
        </div>
      `,
    )
    .join("");
}

function renderRolloutPulse(applyItems, impactItems) {
  const stats = rolloutPulseStats(applyItems);
  const summaryCards = [
    {
      label: "Avg review time",
      value: formatMinutesDuration(stats.avgApprovalMinutes),
      meta:
        stats.avgApprovalMinutes == null
          ? "No reviewed plans yet"
          : "Request to approval",
    },
    {
      label: "Avg sync time",
      value: formatMinutesDuration(stats.avgSyncMinutes),
      meta:
        stats.avgSyncMinutes == null
          ? "No local applies yet"
          : "Approval to local apply",
    },
    {
      label: "End-to-end",
      value: formatMinutesDuration(stats.avgLeadMinutes),
      meta:
        stats.avgLeadMinutes == null
          ? "Waiting for completed rollouts"
          : "Request to applied state",
    },
    {
      label: "Rollback rate",
      value: formatPercent(stats.rollbackRate),
      meta:
        stats.completedCount > 0
          ? `${formatCount(stats.completedCount)} completed rollout(s)`
          : "No completed rollouts yet",
    },
  ];

  $("rolloutPulseSummary").innerHTML = summaryCards
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

  if (!applyItems.length) {
    $("rolloutPulseTimeline").innerHTML = emptyState(
      "No rollout history yet",
      "Approve a suggestion to start tracking review and local apply timing.",
    );
    return;
  }

  const impactIndex = new Map(applyItems.map((item) => [item.apply_id, null]));
  impactItems.forEach((item) => impactIndex.set(item.apply_id, item));

  $("rolloutPulseTimeline").innerHTML = applyItems
    .slice(0, 5)
    .map((item) => {
      const impact = impactIndex.get(item.apply_id) || {};
      const reviewMinutes = minutesBetween(item.requested_at, item.reviewed_at);
      const syncMinutes = minutesBetween(
        item.reviewed_at || item.requested_at,
        item.applied_at,
      );
      const requestMeta = [
        item.scope ? titleize(item.scope) : "Workspace scope",
        item.requested_by ? `by ${item.requested_by}` : "",
      ]
        .filter(Boolean)
        .join(" · ");
      const reviewTone = rolloutReviewStageTone(item);
      const applyToneValue = rolloutApplyStageTone(item);
      const applyStatus =
        item.rolled_back ||
        String(item.status || "").toLowerCase() === "rollback_confirmed"
          ? "Rolled back"
          : titleize(item.status || "Pending");
      const laneMeta = [
        `Requested ${formatDateTime(item.requested_at)}`,
        Number(impact.sessions_after || 0) > 0
          ? `${formatCount(impact.sessions_after || 0)} post-apply session(s)`
          : "",
      ]
        .filter(Boolean)
        .join(" · ");
      return `
          <div class="rollout-lane">
            <div class="rollout-lane-top">
              <div class="rollout-lane-title">
                ${escapeHTML(recommendationTitle(item.recommendation_id))}
                <small>${escapeHTML(laneMeta)}</small>
              </div>
              ${pill(applyStatus, applyTone(item.status))}
            </div>
            <div class="rollout-stages">
              <div class="rollout-stage done">
                <div class="rollout-stage-label"><span class="rollout-stage-dot"></span>Requested</div>
                <div class="rollout-stage-value">${escapeHTML(formatDateTime(item.requested_at))}</div>
                <div class="rollout-stage-meta">${escapeHTML(requestMeta || "Queued for review")}</div>
              </div>
              <div class="rollout-stage ${escapeHTML(reviewTone)}">
                <div class="rollout-stage-label"><span class="rollout-stage-dot"></span>Reviewed</div>
                <div class="rollout-stage-value">${escapeHTML(item.reviewed_at ? formatMinutesDuration(reviewMinutes) : "Pending")}</div>
                <div class="rollout-stage-meta">${escapeHTML(item.reviewed_at ? `${titleize(item.decision || "approved")} ${formatDateTime(item.reviewed_at)}` : "Awaiting a decision")}</div>
              </div>
              <div class="rollout-stage ${escapeHTML(applyToneValue)}">
                <div class="rollout-stage-label"><span class="rollout-stage-dot"></span>Applied</div>
                <div class="rollout-stage-value">${escapeHTML(item.applied_at ? formatMinutesDuration(syncMinutes) : "Pending")}</div>
                <div class="rollout-stage-meta">${escapeHTML(item.applied_at ? `${applyStatus} ${formatDateTime(item.applied_at)}` : "Waiting for local sync")}</div>
              </div>
            </div>
          </div>
        `;
    })
    .join("");
}

function renderImpactItems(impactItems, applyItems) {
  if (!impactItems.length) {
    $("impactList").innerHTML = emptyState(
      "No rollout impact yet",
      "Approve and apply a change first, then upload follow-up sessions to compare before and after.",
    );
    return;
  }

  const applyIndex = new Map(applyItems.map((item) => [item.apply_id, item]));
  $("impactList").innerHTML = impactItems
    .slice(0, 6)
    .map((item) => {
      const apply = applyIndex.get(item.apply_id) || {};
      const title = recommendationTitle(item.recommendation_id);
      const approvalLag = formatDurationBetween(
        apply.requested_at,
        apply.reviewed_at,
      );
      const syncLag = formatDurationBetween(
        apply.reviewed_at || apply.requested_at,
        apply.applied_at,
      );
      return `
          <div class="item">
            <div class="item-top">
              <div class="item-title">
                ${escapeHTML(title)}
                <small>${escapeHTML(item.interpretation || "Waiting for post-apply sessions.")}</small>
              </div>
              ${pill(titleize(item.status || "pending"), applyTone(item.status))}
            </div>
            <div class="metric-strip">
              <div class="metric-block">
                <div class="metric-label">Tokens / query</div>
                <div class="metric-value">${escapeHTML(formatCount(Math.round(Number(item.avg_tokens_per_query_after || 0))))}</div>
                <div class="metric-subvalue">${escapeHTML(`Before ${formatCount(Math.round(Number(item.avg_tokens_per_query_before || 0)))} · After ${formatCount(Math.round(Number(item.avg_tokens_per_query_after || 0)))}`)}</div>
                <div class="metric-delta">${pill(`${formatSignedCount(item.tokens_per_query_delta)} tokens`, impactDeltaTone(item.tokens_per_query_delta))}</div>
              </div>
              <div class="metric-block">
                <div class="metric-label">Tokens / session</div>
                <div class="metric-value">${escapeHTML(formatCount(Math.round(Number(item.avg_tokens_per_session_after || 0))))}</div>
                <div class="metric-subvalue">${escapeHTML(`Before ${formatCount(Math.round(Number(item.avg_tokens_per_session_before || 0)))} · After ${formatCount(Math.round(Number(item.avg_tokens_per_session_after || 0)))}`)}</div>
                <div class="metric-delta">${pill(`${formatSignedCount(item.tokens_per_session_delta)} tokens`, impactDeltaTone(item.tokens_per_session_delta))}</div>
              </div>
              <div class="metric-block">
                <div class="metric-label">Rollout timing</div>
                <div class="metric-value">${escapeHTML(formatCount(item.sessions_after || 0))}</div>
                <div class="metric-subvalue">${escapeHTML(`Post-apply sessions: ${formatCount(item.sessions_after || 0)} · Queries: ${formatCount(item.queries_after || 0)}`)}</div>
                <div class="metric-subvalue">${escapeHTML(`Approved in ${approvalLag} · Synced in ${syncLag}`)}</div>
              </div>
            </div>
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
      "Approvals, uploads, and rollouts will appear here once activity starts.",
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

function renderOptimizationLoop(
  overview,
  recommendations,
  reviewQueue,
  applyItems,
  experiments,
) {
  const totalSessions = Number(overview.total_sessions || 0);
  const experimentItems = toArray(experiments);
  const activeExperiment =
    experimentItems.find((item) => {
      const status = String(item.status || "").toLowerCase();
      return (
        status === "awaiting_review" ||
        status === "queued_for_apply" ||
        status === "measuring" ||
        status === "rollback_requested"
      );
    }) ||
    experimentItems[0] ||
    null;

  const stageCounts = {
    observe: totalSessions,
    suggest: toArray(recommendations).length,
    approve: toArray(reviewQueue).length,
    measure: experimentItems.filter(
      (item) => String(item.status || "").toLowerCase() === "measuring",
    ).length,
    rollback: experimentItems.filter((item) => {
      const status = String(item.status || "").toLowerCase();
      return (
        status === "rollback_requested" ||
        status === "rolled_back" ||
        status === "rollback_failed"
      );
    }).length,
  };

  const activeStage = activeExperiment
    ? {
        awaiting_review: "approve",
        queued_for_apply: "approve",
        measuring: "measure",
        rollback_requested: "rollback",
        rolled_back: "rollback",
        completed: "measure",
        rollback_failed: "rollback",
        failed: "rollback",
      }[String(activeExperiment.status || "").toLowerCase()] || "suggest"
    : stageCounts.suggest > 0
      ? "suggest"
      : "observe";

  const stages = [
    {
      key: "observe",
      label: "Observe",
      value: formatCount(stageCounts.observe),
      meta:
        stageCounts.observe > 0
          ? `${formatCount(stageCounts.observe)} session(s) captured`
          : "Waiting for local sessions",
    },
    {
      key: "suggest",
      label: "Suggest",
      value: formatCount(stageCounts.suggest),
      meta:
        stageCounts.suggest > 0
          ? `${formatCount(stageCounts.suggest)} ranked suggestion(s)`
          : "No active suggestions yet",
    },
    {
      key: "approve",
      label: "Approve",
      value: formatCount(stageCounts.approve),
      meta:
        stageCounts.approve > 0
          ? `${formatCount(stageCounts.approve)} decision(s) waiting`
          : "No approval blockers",
    },
    {
      key: "measure",
      label: "Measure",
      value: formatCount(stageCounts.measure),
      meta:
        stageCounts.measure > 0
          ? `${formatCount(stageCounts.measure)} rollout(s) in measurement`
          : "No active measurement window",
    },
    {
      key: "rollback",
      label: "Roll back",
      value: formatCount(stageCounts.rollback),
      meta:
        stageCounts.rollback > 0
          ? `${formatCount(stageCounts.rollback)} rollback event(s)`
          : "No rollback pressure",
    },
  ];

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

  if (!activeExperiment) {
    $("loopSummary").textContent =
      totalSessions > 0
        ? "The loop is in observation mode. AgentOpt is digesting recent raw queries and config state before promoting the next change."
        : "The loop starts after the CLI uploads sessions and snapshots from your coding-agent workspace.";
    $("loopFocusCard").innerHTML = `
          <div class="loop-focus-empty">
            <strong>No experiment is running right now.</strong>
            <span>${escapeHTML(totalSessions > 0 ? "Approve the next suggestion to start a measured rollout." : "Connect the CLI and upload sessions to seed the first suggestion.")}</span>
          </div>
        `;
    return;
  }

  const activeRecommendation = state.recommendationIndex.get(
    activeExperiment.recommendation_id,
  );
  const recommendationTitleText = activeRecommendation
    ? activeRecommendation.title
    : recommendationTitle(activeExperiment.recommendation_id);
  const kind = activeRecommendation
    ? activeRecommendation.kind
    : recommendationKind(activeExperiment.recommendation_id);
  const focusBits = [
    `Baseline ${formatCount(activeExperiment.baseline_sessions || 0)} sessions / ${formatCount(activeExperiment.baseline_queries || 0)} queries`,
    `Post-apply ${formatCount(activeExperiment.post_apply_sessions || 0)} sessions / ${formatCount(activeExperiment.post_apply_queries || 0)} queries`,
  ];
  if (activeExperiment.scope) {
    focusBits.push(`${titleize(activeExperiment.scope)} scope`);
  }
  if (activeExperiment.requested_by) {
    focusBits.push(`Requested by ${activeExperiment.requested_by}`);
  }

  const decisionReason =
    String(activeExperiment.decision_reason || "").trim() ||
    "This experiment is waiting for the next transition.";

  $("loopSummary").textContent =
    String(activeExperiment.status || "").toLowerCase() === "rollback_requested"
      ? "The loop detected a regression and has queued an automatic rollback for the local CLI."
      : "Each approved change becomes an experiment with a baseline, a measurement window, and a rollback path if the metrics regress.";

  $("loopFocusCard").innerHTML = `
        <div class="loop-focus-top">
          <div>
            <div class="loop-focus-kicker">Current experiment</div>
            <div class="loop-focus-title">${escapeHTML(recommendationTitleText)}</div>
          </div>
          <div class="loop-focus-pills">
            ${pill(recommendationKindLabel(kind), recommendationKindTone(kind))}
            ${pill(titleize(activeExperiment.status || "pending"), experimentTone(activeExperiment.status))}
          </div>
        </div>
        <div class="loop-focus-body">
          <div class="loop-focus-reason">${escapeHTML(decisionReason)}</div>
          <div class="loop-focus-metrics">
            ${focusBits.map((item) => `<span>${escapeHTML(item)}</span>`).join("")}
          </div>
        </div>
      `;
}

function findActiveLifecycle(applyItems, experiments) {
  const experimentIndex = new Map(
    toArray(experiments).map((e) => [e.experiment_id, e]),
  );
  const statusPriority = [
    "rollback_requested",
    "approved_for_local_apply",
    "awaiting_review",
    "applied",
  ];

  let target = null;
  for (const s of statusPriority) {
    target = applyItems.find((a) => String(a.status || "").toLowerCase() === s);
    if (target) break;
  }

  if (!target) {
    const measuringExp = toArray(experiments).find(
      (e) => String(e.status || "").toLowerCase() === "measuring",
    );
    if (measuringExp) {
      target = applyItems.find(
        (a) => a.experiment_id === measuringExp.experiment_id,
      );
    }
  }

  if (!target) {
    const rbExp = toArray(experiments).find(
      (e) => String(e.status || "").toLowerCase() === "rollback_requested",
    );
    if (rbExp) {
      target = applyItems.find((a) => a.experiment_id === rbExp.experiment_id);
    }
  }

  if (!target && applyItems.length > 0) {
    target = applyItems[0];
  }

  return target
    ? {
        apply: target,
        experiment: experimentIndex.get(target.experiment_id) || null,
      }
    : null;
}

function lifecycleDescription(applyStatus, expStatus) {
  if (applyStatus === "awaiting_review")
    return "A suggestion is waiting for your review.";
  if (applyStatus === "rejected")
    return "This suggestion was declined during review.";
  if (applyStatus === "approved_for_local_apply")
    return "Approved and waiting for the local CLI to sync.";
  if (applyStatus === "failed") return "Local execution of this change failed.";
  if (applyStatus === "rollback_confirmed")
    return "Rollback completed \u2014 the previous configuration was restored.";
  if (applyStatus === "rollback_failed")
    return "Rollback execution failed \u2014 manual intervention may be needed.";
  if (expStatus === "measuring")
    return "Applied locally \u2014 collecting post-apply sessions to evaluate impact.";
  if (expStatus === "rollback_requested")
    return "Evaluation detected a regression. Rollback is queued for the next sync.";
  if (expStatus === "completed")
    return "Evaluation complete \u2014 this change was measured and kept.";
  if (expStatus === "rolled_back")
    return "This change was rolled back after evaluation detected a regression.";
  return "Tracking the lifecycle of the current optimization change.";
}

function renderLifecycle(applyItems, experiments) {
  const section = $("lifecycleSection");
  const lifecycle = findActiveLifecycle(applyItems, experiments);

  if (!lifecycle) {
    section.dataset.empty = "true";
    return;
  }

  section.dataset.empty = "false";
  const { apply, experiment } = lifecycle;
  const applyStatus = String(apply.status || "").toLowerCase();
  const expStatus = experiment
    ? String(experiment.status || "").toLowerCase()
    : "";
  const expDecision = experiment
    ? String(experiment.decision || "").toLowerCase()
    : "";

  $("lifecycleTitle").textContent = recommendationTitle(
    apply.recommendation_id,
  );
  $("lifecycleDesc").textContent = lifecycleDescription(applyStatus, expStatus);

  const steps = [];

  steps.push({
    label: "Suggested",
    state: "done",
    time: formatShortDate(apply.requested_at),
  });

  if (applyStatus === "rejected") {
    steps.push({
      label: "Rejected",
      state: "danger",
      time: formatShortDate(apply.reviewed_at),
    });
  } else if (apply.reviewed_at) {
    steps.push({
      label: "Approved",
      state: "done",
      time: formatShortDate(apply.reviewed_at),
    });
  } else if (applyStatus === "awaiting_review") {
    steps.push({ label: "Review", state: "active", time: "Waiting" });
  } else {
    steps.push({ label: "Review", state: "pending", time: "" });
  }

  if (applyStatus === "rejected") {
    steps.push({ label: "Sync", state: "skip", time: "" });
  } else if (apply.applied_at) {
    steps.push({ label: "Synced", state: "done", time: "" });
  } else if (applyStatus === "approved_for_local_apply") {
    steps.push({ label: "Syncing", state: "active", time: "Pending" });
  } else {
    steps.push({ label: "Sync", state: "pending", time: "" });
  }

  if (applyStatus === "rejected") {
    steps.push({ label: "Apply", state: "skip", time: "" });
  } else if (applyStatus === "failed") {
    steps.push({ label: "Failed", state: "danger", time: "" });
  } else if (apply.applied_at) {
    steps.push({
      label: "Applied",
      state: "done",
      time: formatShortDate(apply.applied_at),
    });
  } else {
    steps.push({ label: "Apply", state: "pending", time: "" });
  }

  if (expStatus === "measuring") {
    steps.push({
      label: "Measuring",
      state: "active",
      time: formatCount(experiment.post_apply_sessions || 0) + " sess",
    });
  } else if (
    [
      "completed",
      "rolled_back",
      "rollback_requested",
      "rollback_failed",
    ].includes(expStatus)
  ) {
    steps.push({ label: "Measured", state: "done", time: "" });
  } else {
    steps.push({ label: "Measure", state: "pending", time: "" });
  }

  if (expStatus === "completed") {
    steps.push({
      label: "Kept",
      state: "done",
      time: formatShortDate(experiment.resolved_at),
    });
  } else if (
    expStatus === "rolled_back" ||
    applyStatus === "rollback_confirmed"
  ) {
    steps.push({
      label: "Rolled back",
      state: "done",
      time: formatShortDate(
        apply.rolled_back_at || (experiment && experiment.resolved_at),
      ),
    });
  } else if (expStatus === "rollback_requested") {
    steps.push({ label: "Rollback", state: "active", time: "Queued" });
  } else if (
    expStatus === "rollback_failed" ||
    applyStatus === "rollback_failed"
  ) {
    steps.push({ label: "RB failed", state: "danger", time: "" });
  } else {
    steps.push({ label: "Outcome", state: "pending", time: "" });
  }

  const stepperParts = [];
  steps.forEach((step, i) => {
    if (i > 0) {
      const prevState = steps[i - 1].state;
      const curState = step.state;
      let lineState = "";
      if (
        prevState === "done" &&
        (curState === "done" || curState === "danger")
      )
        lineState = "done";
      else if (prevState === "done" && curState === "active")
        lineState = "active";
      stepperParts.push(
        `<div class="lc-step-line" data-state="${escapeAttr(lineState)}"></div>`,
      );
    }
    stepperParts.push(
      `<div class="lc-step" data-state="${escapeAttr(step.state)}">` +
        `<div class="lc-step-node"><div class="lc-step-inner"></div></div>` +
        `<div class="lc-step-label">${escapeHTML(step.label)}</div>` +
        (step.time
          ? `<div class="lc-step-time">${escapeHTML(step.time)}</div>`
          : "") +
        `</div>`,
    );
  });
  $("lifecycleStepper").innerHTML = stepperParts.join("");

  let panelsHTML = "";

  let approvalTone = "sky";
  let approvalLabel = "Pending";
  if (applyStatus === "rejected") {
    approvalTone = "danger";
    approvalLabel = "Rejected";
  } else if (applyStatus === "awaiting_review") {
    approvalTone = "warn";
    approvalLabel = "Waiting for review";
  } else if (apply.reviewed_at) {
    approvalTone = "good";
    approvalLabel = "Approved";
  }

  panelsHTML += `<div class="lifecycle-card">
        <div class="lc-card-header"><div class="lc-card-title">Approval</div>${pill(approvalLabel, approvalTone)}</div>
        <div class="lc-card-body">
          <div class="lc-status-row"><span class="lc-status-dot ${escapeAttr(approvalTone)}"></span>${escapeHTML(approvalLabel)}</div>
          ${apply.reviewed_by ? `<div class="lc-detail"><span class="lc-detail-label">Reviewed by</span><span class="lc-detail-value">${escapeHTML(apply.reviewed_by)}</span></div>` : ""}
          ${apply.reviewed_at ? `<div class="lc-detail"><span class="lc-detail-label">Reviewed</span><span class="lc-detail-value">${escapeHTML(formatDateTime(apply.reviewed_at))}</span></div>` : ""}
          ${apply.policy_mode ? `<div class="lc-detail"><span class="lc-detail-label">Policy</span><span class="lc-detail-value">${escapeHTML(titleize(apply.policy_mode))}</span></div>` : ""}
          ${apply.review_note ? `<div class="lc-card-reason">${escapeHTML(apply.review_note)}</div>` : ""}
        </div>
      </div>`;

  let localTone = "sky";
  let localLabel = "Pending";
  if (applyStatus === "applied" || applyStatus === "rollback_confirmed") {
    localTone = "good";
    localLabel =
      apply.rolled_back || applyStatus === "rollback_confirmed"
        ? "Rolled back"
        : "Applied";
  } else if (applyStatus === "failed" || applyStatus === "rollback_failed") {
    localTone = "danger";
    localLabel =
      applyStatus === "rollback_failed" ? "Rollback failed" : "Apply failed";
  } else if (applyStatus === "approved_for_local_apply") {
    localTone = "warn";
    localLabel = "Waiting for sync";
  } else if (applyStatus === "rejected") {
    localTone = "danger";
    localLabel = "Not applied";
  }

  panelsHTML += `<div class="lifecycle-card">
        <div class="lc-card-header"><div class="lc-card-title">Local apply</div>${pill(localLabel, localTone)}</div>
        <div class="lc-card-body">
          <div class="lc-detail"><span class="lc-detail-label">Requested</span><span class="lc-detail-value">${escapeHTML(formatDateTime(apply.requested_at))}</span></div>
          <div class="lc-detail"><span class="lc-detail-label">Reviewed</span><span class="lc-detail-value">${escapeHTML(apply.reviewed_at ? formatDateTime(apply.reviewed_at) : "Pending")}</span></div>
          <div class="lc-detail"><span class="lc-detail-label">Applied</span><span class="lc-detail-value">${escapeHTML(apply.applied_at ? formatDateTime(apply.applied_at) : "Pending")}</span></div>
          ${apply.rolled_back_at ? `<div class="lc-detail"><span class="lc-detail-label">Rolled back</span><span class="lc-detail-value">${escapeHTML(formatDateTime(apply.rolled_back_at))}</span></div>` : ""}
          ${apply.reviewed_at && apply.requested_at ? `<div class="lc-detail"><span class="lc-detail-label">Review time</span><span class="lc-detail-value">${escapeHTML(formatDurationBetween(apply.requested_at, apply.reviewed_at))}</span></div>` : ""}
          ${apply.applied_at && (apply.reviewed_at || apply.requested_at) ? `<div class="lc-detail"><span class="lc-detail-label">Sync time</span><span class="lc-detail-value">${escapeHTML(formatDurationBetween(apply.reviewed_at || apply.requested_at, apply.applied_at))}</span></div>` : ""}
        </div>
      </div>`;

  let evalTone = "sky";
  let evalLabel = "Not started";
  if (expStatus === "measuring") {
    evalTone = "warn";
    evalLabel = "Measuring";
  } else if (expStatus === "completed") {
    evalTone = "good";
    evalLabel = expDecision === "keep" ? "Kept" : "Completed";
  } else if (expStatus === "rollback_requested") {
    evalTone = "danger";
    evalLabel = "Regression detected";
  } else if (expStatus === "rolled_back") {
    evalTone = "danger";
    evalLabel = "Rolled back";
  } else if (expStatus === "rollback_failed") {
    evalTone = "danger";
    evalLabel = "Rollback failed";
  } else if (
    expStatus === "queued_for_apply" ||
    expStatus === "awaiting_review"
  ) {
    evalTone = "sky";
    evalLabel = "Queued";
  }

  const baselineSessions = experiment
    ? Number(experiment.baseline_sessions || 0)
    : 0;
  const baselineQueries = experiment
    ? Number(experiment.baseline_queries || 0)
    : 0;
  const postSessions = experiment
    ? Number(experiment.post_apply_sessions || 0)
    : 0;
  const postQueries = experiment
    ? Number(experiment.post_apply_queries || 0)
    : 0;
  const maxSessions = Math.max(baselineSessions, postSessions, 1);
  const decisionReason = experiment
    ? String(experiment.decision_reason || "").trim()
    : "";

  panelsHTML += `<div class="lifecycle-card">
        <div class="lc-card-header"><div class="lc-card-title">Evaluation</div>${pill(evalLabel, evalTone)}</div>
        <div class="lc-card-body">`;

  if (experiment) {
    const baselineWidth = Math.max(
      4,
      Math.round((baselineSessions / maxSessions) * 100),
    );
    const postWidth =
      postSessions > 0
        ? Math.max(4, Math.round((postSessions / maxSessions) * 100))
        : 0;

    panelsHTML += `<div class="lc-progress">
          <div class="lc-progress-row">
            <div class="lc-progress-header"><span>Baseline</span><span>${escapeHTML(formatCount(baselineSessions))} sess \u00b7 ${escapeHTML(formatCount(baselineQueries))} queries</span></div>
            <div class="lc-progress-track"><div class="lc-progress-fill baseline" style="width:${baselineWidth}%"></div></div>
          </div>
          <div class="lc-progress-row">
            <div class="lc-progress-header"><span>Post-apply</span><span>${escapeHTML(formatCount(postSessions))} sess \u00b7 ${escapeHTML(formatCount(postQueries))} queries</span></div>
            <div class="lc-progress-track"><div class="lc-progress-fill post-apply" style="width:${postWidth}%"></div></div>
          </div>
        </div>`;

    if (expStatus === "measuring" && !decisionReason) {
      panelsHTML += `<div class="lc-card-reason">Collecting evidence \u2014 more post-apply sessions are needed before the system can evaluate impact.</div>`;
    }

    if (decisionReason) {
      panelsHTML += `<div class="lc-card-reason">${escapeHTML(decisionReason)}</div>`;
    }

    if (experiment.last_observed_at) {
      panelsHTML += `<div class="lc-detail"><span class="lc-detail-label">Last observed</span><span class="lc-detail-value">${escapeHTML(formatDateTime(experiment.last_observed_at))}</span></div>`;
    }
  } else {
    panelsHTML += `<div class="lc-detail"><span class="lc-detail-label">Status</span><span class="lc-detail-value">No experiment linked</span></div>`;
  }

  panelsHTML += `</div></div>`;

  const hasRollback =
    ["rollback_requested", "rolled_back", "rollback_failed"].includes(
      expStatus,
    ) ||
    ["rollback_confirmed", "rollback_failed", "rollback_requested"].includes(
      applyStatus,
    ) ||
    apply.rolled_back;

  let rbTone = "sky";
  let rbLabel = "Not triggered";
  if (applyStatus === "rollback_confirmed" || expStatus === "rolled_back") {
    rbTone = "good";
    rbLabel = "Rollback completed";
  } else if (
    applyStatus === "rollback_failed" ||
    expStatus === "rollback_failed"
  ) {
    rbTone = "danger";
    rbLabel = "Rollback failed";
  } else if (
    expStatus === "rollback_requested" ||
    applyStatus === "rollback_requested"
  ) {
    rbTone = "warn";
    rbLabel = "Rollback requested";
  }

  panelsHTML += `<div class="lifecycle-card">
        <div class="lc-card-header"><div class="lc-card-title">Rollback</div>${pill(rbLabel, rbTone)}</div>
        <div class="lc-card-body">
          <div class="lc-status-row"><span class="lc-status-dot ${escapeAttr(rbTone === "sky" ? "muted" : rbTone)}"></span>${escapeHTML(rbLabel)}</div>`;

  if (hasRollback) {
    if (decisionReason) {
      panelsHTML += `<div class="lc-card-reason">${escapeHTML(decisionReason)}</div>`;
    }
    if (
      expStatus === "rollback_requested" ||
      applyStatus === "rollback_requested"
    ) {
      panelsHTML += `<div class="lc-detail"><span class="lc-detail-label">Execution</span><span class="lc-detail-value">Queued for next local sync</span></div>`;
    }
    if (apply.rolled_back_at) {
      panelsHTML += `<div class="lc-detail"><span class="lc-detail-label">Rolled back at</span><span class="lc-detail-value">${escapeHTML(formatDateTime(apply.rolled_back_at))}</span></div>`;
    }
  } else {
    const noRbReason =
      expStatus === "measuring"
        ? "Evaluation in progress"
        : expStatus === "completed"
          ? "Change kept \u2014 no rollback needed"
          : applyStatus === "rejected"
            ? "Change was rejected"
            : "Waiting for evaluation";
    panelsHTML += `<div class="lc-detail"><span class="lc-detail-label">Status</span><span class="lc-detail-value">${escapeHTML(noRbReason)}</span></div>`;
  }

  panelsHTML += `</div></div>`;

  $("lifecycleGrid").innerHTML = panelsHTML;
}

function renderActionItems(recommendations, reviewQueue) {
  const html = [];

  reviewQueue.forEach((item) => {
    const rec = state.recommendationIndex.get(item.recommendation_id) || {};
    const kind = String(rec.kind || "").trim();
    html.push(`
          <div class="item">
            <div class="item-top">
              <div class="item-title">
                ${escapeHTML(rec.title || "Configuration update")}
                <small>${escapeHTML(rec.summary || "A reviewed configuration update for this workspace.")}</small>
              </div>
              <div class="item-pill-row">
                ${pill(recommendationKindLabel(kind), recommendationKindTone(kind))}
                ${pill("Needs approval", "warn")}
              </div>
            </div>
            <div class="step-list">
              <div class="step-line">${escapeHTML(patchPreviewSummary(item.patch_preview))}</div>
            </div>
            ${suggestionDetailsBlock(rec)}
            <div class="action-row">
              <button class="primary-button" type="button" data-action="review-plan" data-apply-id="${escapeAttr(item.apply_id)}" data-decision="approve">Approve</button>
              <button class="secondary-button" type="button" data-action="review-plan" data-apply-id="${escapeAttr(item.apply_id)}" data-decision="reject">Decline</button>
            </div>
          </div>
        `);
  });

  const reviewedIDs = new Set(
    reviewQueue.map((item) => item.recommendation_id),
  );
  const newRecs = recommendations.filter((item) => !reviewedIDs.has(item.id));

  newRecs.slice(0, 5).forEach((item) => {
    html.push(`
          <div class="item">
            <div class="item-top">
              <div class="item-title">
                ${escapeHTML(item.title)}
                <small>${escapeHTML(item.summary || "")}</small>
              </div>
              <div class="item-pill-row">
                ${pill(recommendationKindLabel(item.kind), recommendationKindTone(item.kind))}
                ${pill(item.risk || "Low risk", riskTone(item.risk))}
              </div>
            </div>
            <div class="step-list">
              <div class="step-line">${escapeHTML(recommendationPlanSummary(item))}</div>
            </div>
            ${suggestionDetailsBlock(item)}
            <div class="action-row">
              <button class="primary-button" type="button" data-action="approve-recommendation" data-recommendation-id="${escapeAttr(item.id)}">Approve</button>
              <button class="secondary-button" type="button" data-action="decline-recommendation" data-recommendation-id="${escapeAttr(item.id)}">Decline</button>
            </div>
          </div>
        `);
  });

  const section = $("activeSuggestionSection");
  if (html.length) {
    section.dataset.empty = "false";
    $("actionItemList").innerHTML = html.join("");
  } else {
    section.dataset.empty = "true";
    $("actionItemList").innerHTML = "";
  }
}

function renderImpactTimeline(impactItems, applyItems, recommendations) {
  const target = $("impactTimelineList");
  const allApplied = applyItems.filter((item) => {
    const status = String(item.status || "").toLowerCase();
    return (
      status === "applied" ||
      status === "rollback_confirmed" ||
      item.rolled_back
    );
  });

  if (!allApplied.length) {
    target.innerHTML = emptyState(
      "No applied changes yet",
      "When you approve a suggestion, it will appear here with before-and-after metrics. The agent tracks whether each change improved your workflow.",
    );
    return;
  }

  const impactIndex = new Map(impactItems.map((item) => [item.apply_id, item]));
  target.innerHTML = allApplied
    .slice(0, 8)
    .map((item) => {
      const impact = impactIndex.get(item.apply_id) || {};
      const title = recommendationTitle(item.recommendation_id);
      const isRolledBack =
        item.rolled_back ||
        String(item.status || "").toLowerCase() === "rollback_confirmed";
      const statusLabel = isRolledBack ? "Rolled back" : "Improved";
      const statusTone = isRolledBack ? "danger" : "good";
      const tokenDelta = Number(impact.tokens_per_query_delta || 0);
      const tokenDeltaText =
        tokenDelta !== 0 ? `${formatSignedCount(tokenDelta)} tokens/query` : "";
      const interpretation =
        impact.interpretation ||
        (isRolledBack
          ? "Metrics declined after applying this change. The agent reverted it."
          : "Measuring impact...");
      const appliedDate = formatDateTime(item.applied_at);

      return `
          <div class="item">
            <div class="item-top">
              <div class="item-title">
                ${escapeHTML(title)}
                <small>${escapeHTML(interpretation)}</small>
              </div>
              ${pill(statusLabel, statusTone)}
            </div>
            <div class="step-list">
              ${tokenDeltaText ? `<div class="step-line">${escapeHTML(tokenDeltaText)} &middot; Applied ${escapeHTML(appliedDate)}</div>` : `<div class="step-line">Applied ${escapeHTML(appliedDate)}</div>`}
            </div>
          </div>
        `;
    })
    .join("");
}

function renderSessionSummaries(items) {
  state.sessionItems = items.slice(0, 10);

  if (!items.length) {
    $("sessionSummaryList").innerHTML = emptyState(
      "No sessions uploaded yet",
      "Run `agentopt session --recent 5` from the CLI to upload your recent AI usage sessions.",
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

function requireUser() {
  const userID =
    state.session && state.session.user ? state.session.user.id : "";
  if (!userID) {
    redirectToLanding("Sign in again to continue.");
  }
  return userID;
}

async function approveRecommendation(recommendationID) {
  const requestedBy = requireUser();
  if (!requestedBy) {
    return;
  }

  try {
    await withBusy(async () => {
      const data = await requestJSON(
        "/api/v1/recommendations/apply",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            recommendation_id: recommendationID,
            requested_by: requestedBy,
            scope: "user",
          }),
        },
        "Failed to create the change plan.",
      );

      if (data.policy_mode !== "auto_approved" && data.apply_id) {
        await requestJSON(
          "/api/v1/change-plans/review",
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              apply_id: data.apply_id,
              decision: "approve",
              reviewed_by: requestedBy,
              review_note: "dashboard approve",
            }),
          },
          "Failed to approve the plan.",
        );
      }

      await load({ skipBusy: true });
      setStatus(
        "Approved. The local CLI can pick up this change on the next sync.",
      );
    });
  } catch (error) {
    if (isUnauthorized(error)) {
      redirectToLanding("Your session expired. Sign in again.");
      return;
    }
    setStatus(
      error instanceof Error ? error.message : "Failed to approve.",
      true,
    );
  }
}

async function declineRecommendation(recommendationID) {
  const requestedBy = requireUser();
  if (!requestedBy) {
    return;
  }

  try {
    await withBusy(async () => {
      const data = await requestJSON(
        "/api/v1/recommendations/apply",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            recommendation_id: recommendationID,
            requested_by: requestedBy,
            scope: "user",
          }),
        },
        "Failed to create the change plan.",
      );

      if (data.apply_id) {
        await requestJSON(
          "/api/v1/change-plans/review",
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              apply_id: data.apply_id,
              decision: "reject",
              reviewed_by: requestedBy,
              review_note: "dashboard decline",
            }),
          },
          "Failed to decline the plan.",
        );
      }

      await load({ skipBusy: true });
      setStatus("Declined. The suggestion has been dismissed.");
    });
  } catch (error) {
    if (isUnauthorized(error)) {
      redirectToLanding("Your session expired. Sign in again.");
      return;
    }
    setStatus(
      error instanceof Error ? error.message : "Failed to decline.",
      true,
    );
  }
}

async function reviewPlan(applyID, decision) {
  const reviewer = requireUser();
  if (!reviewer) {
    return;
  }

  try {
    await withBusy(async () => {
      await requestJSON(
        "/api/v1/change-plans/review",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            apply_id: applyID,
            decision,
            reviewed_by: reviewer,
            review_note: `dashboard ${decision}`,
          }),
        },
        "Failed to update the review decision.",
      );

      await load({ skipBusy: true });
      setStatus(
        decision === "approve"
          ? "Approved. The local CLI can pick up this change on the next sync."
          : "Declined. The plan will stay out of the rollout queue.",
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
        : "Failed to update the review decision.",
      true,
    );
  }
}

async function issueCLIToken() {
  try {
    await withBusy(async () => {
      const data = await requestJSON(
        "/api/v1/auth/cli-tokens",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            label: "CLI login token",
          }),
        },
        "Failed to issue a CLI token.",
      );

      $("issuedCliToken").textContent = data.token || "Token was issued.";
      $("cliTokenMeta").textContent = data.expires_at
        ? `CLI token issued for ${data.label || "CLI login"} and expires ${formatDateTime(data.expires_at)}. Paste it into \`agentopt login\` on the machine you want to connect.`
        : "CLI token issued. Paste it into `agentopt login` on the machine you want to connect.";

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
        "CLI token issued. Paste it into `agentopt login` on the device you want to connect.",
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
  window.location.replace("/");
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
        "Failed to load the connected project.",
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

    state.recommendationIndex = new Map();

    let recommendations = [];
    let reviewQueue = [];
    let sessions = [];
    let insights = {};
    let impactItems = [];
    let applyItems = [];
    let experiments = [];
    let snapshots = [];
    let audits = [];

    if (projectID) {
      const [
        recommendationsData,
        reviewData,
        sessionData,
        insightsData,
        impactData,
        applyHistoryData,
        experimentData,
        snapshotData,
        auditData,
      ] = await Promise.all([
        requestJSON(
          `/api/v1/recommendations?project_id=${encodeURIComponent(projectID)}`,
          {},
          "Failed to load workspace suggestions.",
        ),
        requestJSON(
          `/api/v1/change-plans?project_id=${encodeURIComponent(projectID)}&status=awaiting_review`,
          {},
          "Failed to load the approval queue.",
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
          `/api/v1/impact?project_id=${encodeURIComponent(projectID)}`,
          {},
          "Failed to load rollout impact.",
        ),
        requestJSON(
          `/api/v1/applies?project_id=${encodeURIComponent(projectID)}`,
          {},
          "Failed to load rollout history.",
        ),
        requestJSON(
          `/api/v1/experiments?project_id=${encodeURIComponent(projectID)}`,
          {},
          "Failed to load experiment history.",
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
      ]);

      recommendations = toArray(recommendationsData.items);
      reviewQueue = toArray(reviewData.items);
      sessions = toArray(sessionData.items);
      insights = insightsData || {};
      impactItems = toArray(impactData.items);
      applyItems = toArray(applyHistoryData.items);
      experiments = toArray(experimentData.items);
      snapshots = toArray(snapshotData.items);
      audits = toArray(auditData.items);

      recommendations.forEach((item) => {
        state.recommendationIndex.set(item.id, item);
      });
    }

    renderAgentStatus(overview, recommendations, applyItems, experiments);
    renderOptimizationLoop(
      overview,
      recommendations,
      reviewQueue,
      applyItems,
      experiments,
    );
    renderLifecycle(applyItems, experiments);
    renderActionItems(recommendations, reviewQueue);
    renderImpactTimeline(impactItems, applyItems, recommendations);
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
    renderHeavySessions(sessions);
    renderRolloutFunnel(recommendations, reviewQueue, applyItems);
    renderRolloutPulse(applyItems, impactItems);
    renderImpactItems(impactItems, applyItems);
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
        "Connected project";
      setStatus(
        `Showing ${workspaceName}. Review your AI usage and approve recommended changes.`,
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

function toggleSuggestionDetail(button) {
  const detail = button.closest(".suggestion-detail");
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
    case "switch-tab":
      setActiveTab(button.dataset.tab || "overview");
      break;
    case "copy-command":
      copyCommand(
        button.dataset.copyTarget || "",
        button.dataset.copyLabel || "command",
      );
      break;
    case "approve-recommendation":
      approveRecommendation(button.dataset.recommendationId || "");
      break;
    case "decline-recommendation":
      declineRecommendation(button.dataset.recommendationId || "");
      break;
    case "review-plan":
      reviewPlan(button.dataset.applyId || "", button.dataset.decision || "");
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
    case "toggle-suggestion-detail":
      toggleSuggestionDetail(button);
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
  syncBusyUI();

  $("onboardingWizard").classList.add("is-hidden");
  $("mainDashboard").classList.add("is-visible");

  if (!(await ensureSession())) {
    return;
  }

  load();
}

boot();
