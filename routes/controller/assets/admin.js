const ADMIN_STORAGE_KEYS = {
  sessionUser: "crux_session_user",
  sessionOrg: "crux_session_org",
};

const adminState = {
  busy: false,
  session: null,
  users: [],
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
  }
}

function writeSession(session) {
  adminState.session = session;
  writeStorage(
    ADMIN_STORAGE_KEYS.sessionUser,
    JSON.stringify((session && session.user) || {}),
  );
  writeStorage(
    ADMIN_STORAGE_KEYS.sessionOrg,
    JSON.stringify((session && session.organization) || {}),
  );
}

function clearSession() {
  writeStorage(ADMIN_STORAGE_KEYS.sessionUser, "");
  writeStorage(ADMIN_STORAGE_KEYS.sessionOrg, "");
  adminState.session = null;
}

function setStatus(text, isError = false) {
  const status = $("status");
  status.textContent = text;
  status.dataset.tone = isError ? "error" : "info";
}

function setLandingNotice(message) {
  try {
    if (message) {
      window.sessionStorage.setItem("crux_redirect_notice", message);
    } else {
      window.sessionStorage.removeItem("crux_redirect_notice");
    }
  } catch (error) {
  }
}

function redirectToLanding(message) {
  setLandingNotice(message);
  clearSession();
  window.location.replace("/");
}

function redirectToDashboard(message) {
  setLandingNotice(message);
  window.location.replace("/dashboard");
}

async function requestJSON(url, options = {}, fallbackMessage = "Request failed.") {
  const response = await fetch(url, Object.assign({ credentials: "same-origin" }, options));
  const rawBody = await response.text();

  let envelope = null;
  if (rawBody) {
    try {
      envelope = JSON.parse(rawBody);
    } catch (error) {
      const unreadable = new Error(response.ok ? "The server returned an unreadable response." : fallbackMessage);
      unreadable.status = response.status;
      throw unreadable;
    }
  }

  if (!response.ok || !envelope || envelope.code !== 0) {
    const failure = new Error((envelope && (envelope.msg || envelope.message)) || fallbackMessage);
    failure.status = response.status;
    throw failure;
  }

  return envelope.data || {};
}

function isUnauthorized(error) {
  return !!(error && (error.status === 401 || error.status === 403));
}

function syncHeader() {
  const session = adminState.session || {};
  const user = session.user || {};
  const organization = session.organization || {};
  $("topBarUser").textContent = user.name || user.email || "-";
  $("topBarOrg").textContent = organization.name || organization.id || "-";
}

function formatDateTime(value) {
  if (!value) {
    return "Not yet";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return String(value);
  }
  return date.toLocaleString();
}

function pill(label, tone) {
  return `<span class="pill ${toneClass(tone)}">${escapeHTML(label)}</span>`;
}

function toneClass(tone) {
  switch (tone) {
    case "good":
      return "pill-good";
    case "warn":
      return "pill-warn";
    case "danger":
      return "pill-danger";
    default:
      return "pill-sky";
  }
}

function statusTone(status) {
  switch (String(status || "").toLowerCase()) {
    case "active":
      return "good";
    case "disabled":
      return "warn";
    case "deleted":
      return "danger";
    default:
      return "sky";
  }
}

function roleTone(role) {
  return String(role || "").toLowerCase() === "admin" ? "sky" : "good";
}

function sourceTone(source) {
  switch (String(source || "").toLowerCase()) {
    case "demo":
      return "warn";
    case "bootstrap":
      return "sky";
    case "managed":
      return "good";
    default:
      return "sky";
  }
}

function emptyState(title, body) {
  return `
    <div class="item">
      <div class="item-title">${escapeHTML(title)}</div>
      <div class="admin-empty-note">${escapeHTML(body)}</div>
    </div>
  `;
}

function renderUsers(items) {
  adminState.users = items.slice();
  $("listSummary").textContent = `${items.length} user account(s) in the current organization.`;

  if (!items.length) {
    $("userList").innerHTML = emptyState(
      "No users matched the current filters",
      "Create a managed user or broaden the filters to see more accounts.",
    );
    return;
  }

  const currentUserID = String((adminState.session && adminState.session.user && adminState.session.user.id) || "");
  $("userList").innerHTML = items.map((item) => {
    const isDeleted = String(item.status || "").toLowerCase() === "deleted";
    const isDisabled = String(item.status || "").toLowerCase() === "disabled";
    const isCurrent = String(item.id || "") === currentUserID;
    const actions = [];

    if (!isDeleted) {
      actions.push(`<button class="secondary-button" type="button" data-action="reset-password" data-user-id="${escapeHTML(item.id)}">Reset password</button>`);
      if (!isDisabled) {
        actions.push(`<button class="secondary-button" type="button" data-action="deactivate-user" data-user-id="${escapeHTML(item.id)}">Deactivate</button>`);
      }
      actions.push(`<button class="secondary-button" type="button" data-action="delete-user" data-user-id="${escapeHTML(item.id)}">Delete</button>`);
    }

    return `
      <div class="item">
        <div class="item-top">
          <div class="item-title">
            ${escapeHTML(item.name || item.email || item.id)}
            <small>${escapeHTML(item.email || item.id)}${isCurrent ? " · current session" : ""}</small>
          </div>
          <div class="item-pill-row">
            ${pill(item.role || "member", roleTone(item.role))}
            ${pill(item.status || "active", statusTone(item.status))}
            ${pill(item.source || "managed", sourceTone(item.source))}
          </div>
        </div>
        <div class="step-list">
          <div class="step-line">${escapeHTML(`User ID ${item.id}`)}</div>
          <div class="step-line">${escapeHTML(`Created ${formatDateTime(item.created_at)}`)}</div>
          <div class="step-line">${escapeHTML(`Password changed ${formatDateTime(item.password_changed_at)}`)}</div>
          <div class="step-line">${escapeHTML(`Last login ${formatDateTime(item.last_login_at)}`)}</div>
        </div>
        ${actions.length ? `<div class="action-row">${actions.join("")}</div>` : ""}
      </div>
    `;
  }).join("");
}

function currentFilters() {
  return {
    search: $("searchInput").value.trim(),
    role: $("roleFilter").value,
    status: $("statusFilter").value,
    includeDeleted: $("includeDeleted").checked,
  };
}

function buildFilterURL() {
  const params = new URLSearchParams();
  const filters = currentFilters();
  if (filters.search) {
    params.set("search", filters.search);
  }
  if (filters.role) {
    params.set("role", filters.role);
  }
  if (filters.status) {
    params.set("status", filters.status);
  }
  if (filters.includeDeleted) {
    params.set("include_deleted", "true");
  }
  const encoded = params.toString();
  return encoded ? `/api/v1/admin/users?${encoded}` : "/api/v1/admin/users";
}

async function withBusy(task) {
  if (adminState.busy) {
    return false;
  }
  adminState.busy = true;
  try {
    await task();
    return true;
  } finally {
    adminState.busy = false;
  }
}

async function loadUsers(message) {
  try {
    await withBusy(async () => {
      setStatus(message || "Loading users...");
      const data = await requestJSON(buildFilterURL(), {}, "Failed to load users.");
      renderUsers(Array.isArray(data.items) ? data.items : []);
      setStatus(`Loaded ${adminState.users.length} user account(s).`);
    });
  } catch (error) {
    if (isUnauthorized(error)) {
      redirectToLanding("Your admin session expired. Sign in again.");
      return;
    }
    setStatus(error instanceof Error ? error.message : "Failed to load users.", true);
  }
}

async function bootstrapSession() {
  try {
    const session = await requestJSON("/api/v1/auth/me", {}, "Failed to read the current session.");
    writeSession(session);
    syncHeader();

    if (String((session.user && session.user.role) || "").toLowerCase() !== "admin") {
      redirectToDashboard("Admin access is required for that page.");
      return false;
    }
    return true;
  } catch (error) {
    redirectToLanding("Sign in again to open the admin page.");
    return false;
  }
}

async function createUser(event) {
  event.preventDefault();

  try {
    await withBusy(async () => {
      const payload = {
        name: $("createName").value.trim(),
        email: $("createEmail").value.trim(),
        role: $("createRole").value,
        password: $("createPassword").value,
      };
      setStatus("Creating user...");
      const data = await requestJSON(
        "/api/v1/admin/users",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload),
        },
        "Failed to create the user.",
      );

      $("createUserForm").reset();
      $("searchInput").value = data.user && data.user.email ? data.user.email : "";
      setStatus(`Created ${data.user && data.user.email ? data.user.email : "the user"}.`);
      await loadUsers();
    });
  } catch (error) {
    if (isUnauthorized(error)) {
      redirectToLanding("Your admin session expired. Sign in again.");
      return;
    }
    setStatus(error instanceof Error ? error.message : "Failed to create the user.", true);
  }
}

async function resetPassword(userID) {
  const nextPassword = window.prompt("Enter a new password for this user.");
  if (!nextPassword) {
    return;
  }

  try {
    await withBusy(async () => {
      setStatus("Resetting password...");
      await requestJSON(
        "/api/v1/admin/users/reset-password",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ user_id: userID, password: nextPassword }),
        },
        "Failed to reset the password.",
      );
      setStatus("Password reset complete. Existing sessions were revoked.");
      await loadUsers();
    });
  } catch (error) {
    if (isUnauthorized(error)) {
      redirectToLanding("Your admin session expired. Sign in again.");
      return;
    }
    setStatus(error instanceof Error ? error.message : "Failed to reset the password.", true);
  }
}

async function deactivateUser(userID) {
  if (!window.confirm("Deactivate this user? Existing sessions will be revoked.")) {
    return;
  }

  try {
    await withBusy(async () => {
      setStatus("Deactivating user...");
      await requestJSON(
        "/api/v1/admin/users/deactivate",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ user_id: userID }),
        },
        "Failed to deactivate the user.",
      );
      setStatus("User deactivated.");
      await loadUsers();
    });
  } catch (error) {
    if (isUnauthorized(error)) {
      redirectToLanding("Your admin session expired. Sign in again.");
      return;
    }
    setStatus(error instanceof Error ? error.message : "Failed to deactivate the user.", true);
  }
}

async function deleteUser(userID) {
  if (!window.confirm("Soft-delete this user? The account will disappear from the default list.")) {
    return;
  }

  try {
    await withBusy(async () => {
      setStatus("Deleting user...");
      await requestJSON(
        "/api/v1/admin/users/delete",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ user_id: userID }),
        },
        "Failed to delete the user.",
      );
      setStatus("User deleted.");
      await loadUsers();
    });
  } catch (error) {
    if (isUnauthorized(error)) {
      redirectToLanding("Your admin session expired. Sign in again.");
      return;
    }
    setStatus(error instanceof Error ? error.message : "Failed to delete the user.", true);
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
      setStatus(error instanceof Error ? error.message : "Failed to sign out.", true);
      return;
    }
  }

  clearSession();
  window.location.replace("/");
}

function bindActions() {
  $("filterForm").addEventListener("submit", (event) => {
    event.preventDefault();
    loadUsers();
  });
  $("createUserForm").addEventListener("submit", createUser);
  $("refreshBtn").addEventListener("click", () => loadUsers("Refreshing users..."));
  $("signOutBtn").addEventListener("click", signOut);
  $("userList").addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLElement)) {
      return;
    }
    const userID = target.dataset.userId;
    if (!userID) {
      return;
    }
    switch (target.dataset.action) {
      case "reset-password":
        resetPassword(userID);
        break;
      case "deactivate-user":
        deactivateUser(userID);
        break;
      case "delete-user":
        deleteUser(userID);
        break;
      default:
        break;
    }
  });
}

async function init() {
  bindActions();
  const ready = await bootstrapSession();
  if (!ready) {
    return;
  }
  await loadUsers();
}

init();
