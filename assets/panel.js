"use strict";

// The panel keeps no state of its own beyond what is on screen and a short
// list of recently used profiles. Everything else is asked for when the panel
// opens, because awsm answers in about five milliseconds and a cache would
// only be a way to show a profile switch that already happened.

const RECENTS_KEY = "awsm.recents";
const RECENTS_MAX = 5;

const el = (id) => document.getElementById(id);

const ui = {
  main: el("main"),
  settings: el("settings"),

  dot: el("dot"),
  current: el("current"),
  currentName: el("currentName"),
  currentStatus: el("currentStatus"),
  currentMeta: el("currentMeta"),
  notice: el("notice"),
  expanded: el("expanded"),
  regionSelect: el("regionSelect"),
  identity: el("identity"),
  identityText: el("identityText"),
  identityButton: el("identityButton"),
  clearButton: el("clearButton"),

  search: el("search"),
  list: el("list"),
  hint: el("hint"),
  settingsButton: el("settingsButton"),

  backButton: el("backButton"),
  shortcutButton: el("shortcutButton"),
  shortcutClear: el("shortcutClear"),
  shortcutNote: el("shortcutNote"),
  themeSelect: el("themeSelect"),
  browserSelect: el("browserSelect"),
  loginCheckbox: el("loginCheckbox"),
  renewalState: el("renewalState"),
  renewalEnable: el("renewalEnable"),
  renewalNote: el("renewalNote"),
  versionState: el("versionState"),
  versionCheck: el("versionCheck"),
  versionOpen: el("versionOpen"),
  versionNote: el("versionNote"),
  binaryPath: el("binaryPath"),
  settingsPath: el("settingsPath"),
  logPath: el("logPath"),
  settingsHint: el("settingsHint"),
  quitButton: el("quitButton"),
};

let state = { profiles: [] };
let prefs = {};
let rows = [];
let selected = 0;
let recording = false;
let stateRequest = 0;
let activeAction = null;
let reloadAfterAction = false;
let identityRequest = 0;
let settingsQueue = Promise.resolve();
let pendingSettings = 0;
let settingsRequest = 0;

// --- talking to Go ---------------------------------------------------------

// get and post are separate on purpose. A single helper that chose the method
// by whether a body happened to be passed sent three POST routes as GET, and
// every one of them answered 404 -- a mistake the code could not show you,
// because it looked exactly like the calls that worked.
const get = (path) => send(path, { method: "GET" });

const post = (path, body) =>
  send(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body ?? {}),
  });

async function send(path, options) {
  const response = await fetch(path, options);
  if (!response.ok) {
    throw new Error(`${options.method} ${path} returned ${response.status}`);
  }
  const data = await response.json();
  // The Go side reports command failures in the body rather than as a status,
  // so that the message awsm printed survives intact.
  if (data && data.error) throw new Error(data.error);
  return data;
}

// dismiss closes the panel. Anything that sends the user to a browser or a
// terminal should get out of the way of where they were sent.
const dismiss = () => post("/api/hide").catch(() => {});

// --- theme -----------------------------------------------------------------

// A chosen theme is stamped on the root element, which is what the stylesheet
// keys off; "system" removes the stamp and lets the media query decide.
function applyTheme(theme) {
  if (theme === "light" || theme === "dark") {
    document.documentElement.dataset.theme = theme;
  } else {
    delete document.documentElement.dataset.theme;
  }
}

// --- recents ---------------------------------------------------------------

function loadRecents() {
  try {
    const stored = JSON.parse(localStorage.getItem(RECENTS_KEY));
    return Array.isArray(stored) ? stored : [];
  } catch {
    // Private windows and cleared site data both land here. Recents are a
    // convenience, so losing them must never stop the panel from opening.
    return [];
  }
}

function rememberRecent(name) {
  try {
    const next = [name, ...loadRecents().filter((n) => n !== name)].slice(0, RECENTS_MAX);
    localStorage.setItem(RECENTS_KEY, JSON.stringify(next));
  } catch {
    // Ignored for the same reason.
  }
}

// --- filtering -------------------------------------------------------------

// matches requires every term to appear somewhere in the profile, so "bes iso
// admin" finds besharp-besharp-isotopes-administratoraccess without having to
// remember the order the words come in.
function matches(profile, terms) {
  if (terms.length === 0) return true;
  const haystack = [
    profile.name,
    profile.sso_session,
    profile.region,
    profile.account_id,
    profile.sso_role_name,
  ]
    .join(" ")
    .toLowerCase();
  return terms.every((term) => haystack.includes(term));
}

const searchTerms = () => ui.search.value.toLowerCase().split(/\s+/).filter(Boolean);

function visibleProfiles() {
  const terms = searchTerms();
  return state.profiles.filter((p) => matches(p, terms));
}

// --- the list --------------------------------------------------------------

function render() {
  const found = visibleProfiles();

  ui.list.replaceChildren();
  rows = [];

  if (found.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = state.profiles.length ? "No match." : "No profiles found.";
    ui.list.append(empty);
    ui.search.removeAttribute("aria-activedescendant");
    updateFooter(0);
    return;
  }

  // Every match is rendered: three hundred rows is nothing for a browser to
  // lay out, and the point of the panel is that everything is reachable.
  const recents = loadRecents();

  if (searchTerms().length === 0) {
    const recent = recents.map((name) => found.find((p) => p.name === name)).filter(Boolean);
    appendGroup("Recent", recent);
    for (const [name, profiles] of bySession(found.filter((p) => !recents.includes(p.name)))) {
      appendGroup(name, profiles);
    }
  } else {
    // Grouped while searching too. The headings do break the results up, but
    // the session a profile belongs to is most of what tells two similarly
    // named ones apart -- which is exactly the moment a search leaves you
    // looking at several of them. Recents are left out here: a search has its
    // own idea of what is relevant.
    for (const [name, profiles] of bySession(found)) {
      appendGroup(name, profiles);
    }
  }

  selected = Math.max(0, Math.min(selected, rows.length - 1));
  markSelected();
  updateFooter(found.length);
}

function bySession(profiles) {
  const groups = new Map();
  for (const profile of profiles) {
    const key = profile.sso_session || "Other";
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(profile);
  }
  return [...groups.entries()].sort((a, b) => a[0].localeCompare(b[0]));
}

function appendGroup(title, profiles) {
  if (profiles.length === 0) return;
  if (title) {
    const heading = document.createElement("div");
    heading.className = "group";
    heading.textContent = title;
    ui.list.append(heading);
  }
  for (const profile of profiles) {
    const row = buildRow(profile);
    ui.list.append(row);
    rows.push({ element: row, profile });
  }
}

function buildRow(profile) {
  const row = document.createElement("div");
  row.className = "row";
  row.id = `profile-${rows.length}`;
  row.setAttribute("role", "option");
  row.setAttribute("aria-label", [profile.name, profile.name === state.profile && "Active", profile.sso_session, profile.region].filter(Boolean).join(" · "));
  row.title = [profile.name, profile.sso_session, profile.account_id, profile.type]
    .filter(Boolean)
    .join(" · ");

  const name = document.createElement("div");
  name.className = "row-name";
  name.textContent = profile.name;
  if (profile.name === state.profile) name.append(badge("Active"));
  if (profile.mfa_serial) {
    // Switching to this one cannot happen in the panel, so say so before the
    // click rather than after it.
    const mfa = badge("MFA");
    mfa.title = "Needs a code typed in a terminal";
    name.append(mfa);
  }

  const region = document.createElement("div");
  region.className = "row-region";
  region.textContent = profile.region || "";

  row.append(name, region);

  // Wails reads these two custom properties when the right button goes down,
  // and hands the data to the native menu. Because the row itself carries the
  // name, a right click acts on the row under the pointer whatever happens to
  // be selected.
  //
  // A name containing a semicolon or a closing brace is not a value CSS can
  // hold: setProperty rejects it silently, and the menu would then open with
  // nothing to act on and do nothing at all. Better to leave the row without a
  // menu than to give it one that quietly does nothing.
  if (carries(row, profile.name)) {
    row.style.setProperty("--custom-contextmenu", "profile");
  }

  row.addEventListener("mouseenter", () => {
    selected = rows.findIndex((r) => r.element === row);
    markSelected();
  });
  row.addEventListener("click", () => activate(profile));
  return row;
}

// carries sets the data the native menu is opened with, and reports whether it
// survived being written.
function carries(element, name) {
  element.style.setProperty("--custom-contextmenu-data", name);
  return element.style.getPropertyValue("--custom-contextmenu-data") === name;
}

function badge(text) {
  const span = document.createElement("span");
  span.className = "badge";
  span.textContent = text;
  return span;
}

function markSelected() {
  rows.forEach((row, index) => {
    row.element.classList.toggle("selected", index === selected);
    row.element.setAttribute("aria-selected", String(index === selected));
  });
  if (rows[selected]) ui.search.setAttribute("aria-activedescendant", rows[selected].element.id);
  rows[selected]?.element.scrollIntoView({ block: "nearest" });
}

const current = () => rows[selected]?.profile;

const KEY_HINT = "↵ Switch · ⌘↵ Console · Right click for more";

function updateFooter(shown) {
  clearTimeout(hintTimer);
  ui.hint.textContent =
    shown < state.profiles.length ? `${shown} of ${state.profiles.length} · ${KEY_HINT}` : KEY_HINT;
}

// hint borrows the footer for a moment and then gives it back.
//
// It used to just write over the keys and leave them written over, so the line
// that tells you what the keys do stayed stuck on "Copied 590184095346" until
// something else happened to redraw the list.
let hintTimer;
const HINT_TIME = 2000;

function hint(message) {
  clearTimeout(hintTimer);
  ui.hint.textContent = message;
  hintTimer = setTimeout(() => updateFooter(visibleProfiles().length), HINT_TIME);
}

// --- the active profile ----------------------------------------------------

function renderCurrent() {
  const active = Boolean(state.profile);

  // Wails reads these when the right button goes down and hands the data to
  // the native menu. Removed when nothing is set, so a right click on "No
  // profile" offers nothing rather than actions that cannot work.
  if (active && carries(ui.current, state.profile)) {
    ui.current.style.setProperty("--custom-contextmenu", "active-profile");
  } else {
    ui.current.style.removeProperty("--custom-contextmenu");
    ui.current.style.removeProperty("--custom-contextmenu-data");
  }

  ui.currentName.textContent = active ? state.profile : "No profile";
  ui.currentMeta.textContent = active
    ? [state.region, state.ttl, state.accountId].filter(Boolean).join(" · ")
    : "Pick a profile below to switch to it";

  ui.dot.className = "dot";
  if (active) ui.dot.classList.add(state.blocked || expiringSoon(state.ttl) ? "warn" : "on");
  ui.current.setAttribute("aria-expanded", String(!ui.expanded.hidden && active));
  ui.currentStatus.textContent = active ? (state.blocked || expiringSoon(state.ttl) ? "Needs attention" : "Active profile") : "No active session";
  if (!active) setExpanded(false);

  // Everything below the header is about the active profile, so everything
  // below the header has to be redrawn when it changes. Both of these were
  // filled only when the details were opened, which left them describing the
  // profile you had just switched away from -- the picker disagreeing with the
  // region printed two lines above it, out of the same state.
  refreshIdentity();
  if (!ui.expanded.hidden) fillRegions();

  renderNotice();
}

function renderNotice() {
  if (activeAction?.doing) {
    const suffix = activeAction.slow ? "… waiting for awsm. Complete any browser sign-in, or cancel." : "…";
    showProgress(activeAction.doing + suffix, activeAction.stoppable);
    return;
  }
  ui.notice.replaceChildren();

  if (state.error) {
    ui.notice.hidden = false;
    ui.notice.className = "notice error";
    ui.notice.append(noticeText(state.error));
    return;
  }
  if (!state.blocked) {
    // Not running, and the credentials on screen are close enough to expiry
    // that its absence is about to be felt. At any other moment this would be
    // a panel nagging about a setting.
    if (state.renewalAtRisk) {
      ui.notice.hidden = false;
      ui.notice.className = "notice";
      ui.notice.append(
        noticeText("These credentials expire soon and nothing is renewing them."),
        noticeButton("Turn on renewal", () =>
          run(() => post("/api/daemon/enable"), { doing: "Turning on renewal" }),
        ),
      );
      return;
    }
    ui.notice.hidden = true;
    return;
  }

  ui.notice.hidden = false;
  ui.notice.className = "notice";

  if (state.blocked === "sso") {
    // An expired SSO session needs a browser, which awsm opens by itself, so
    // nothing here has to leave the panel.
    ui.notice.append(noticeText(`The SSO session for ${state.profile} has expired.`));
    const name = state.profiles.find((p) => p.name === state.profile)?.sso_session;
    if (name) {
      ui.notice.append(
        noticeButton("Log in", () =>
          run((operationId) => post("/api/sso/login", { session: name, operationId }), {
            doing: `Signing in to ${name}`,
            stoppable: true,
          }),
        ),
      );
    }
  } else if (state.blocked === "mfa") {
    // An MFA code is read from standard input, which a panel has none of.
    ui.notice.append(noticeText(`${state.profile} needs an MFA code.`));
    ui.notice.append(noticeButton("Terminal", () => toTerminal(state.profile)));
  } else {
    ui.notice.append(noticeText(`awsm cannot renew ${state.profile} on its own.`));
  }
}

function noticeText(content) {
  const span = document.createElement("span");
  span.className = "notice-text";
  span.textContent = content;
  return span;
}

function noticeButton(label, onClick) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "quiet";
  button.textContent = label;
  button.addEventListener("click", onClick);
  return button;
}

function setExpanded(open) {
  ui.expanded.hidden = !open;
  ui.current.setAttribute("aria-expanded", String(open));
  if (open) fillRegions();
}

// knownRegions is the list awsm gave us, kept so it is asked for once.
//
// This used to be a boolean meaning "already fetched" -- and since the function
// below starts by emptying the select, every opening after the first reset the
// picker to a single option and then returned, leaving it that way.
let knownRegions = [];

async function fillRegions() {
  if (knownRegions.length === 0) {
    try {
      const regions = await get("/api/regions");
      if (Array.isArray(regions)) knownRegions = regions;
    } catch {
      // The picker falls back to the current region below, which is honest
      // about what it can offer.
    }
  }

  const current = state.region || "";
  const options = [...knownRegions];
  // The active region belongs in the list whether or not awsm listed it:
  // a picker that cannot show where you are is worse than one missing a row.
  if (current && !options.includes(current)) options.unshift(current);
  if (options.length === 0) options.push(current || "unknown");

  ui.regionSelect.replaceChildren(...options.map((r) => new Option(r, r)));
  ui.regionSelect.value = current;
}

// identityFor is the profile the answer on screen belongs to.
//
// Without it the panel goes on showing one account's ARN under another
// profile's name after a switch. That is not merely out of date: it is wrong,
// in the one place someone looks to be certain which account they are about to
// act in.
let identityFor = null;

function clearIdentity() {
  identityRequest++;
  identityFor = null;
  ui.identityButton.disabled = false;
  ui.identityText.textContent = "";
  ui.identityButton.textContent = "Check";
}

// refreshIdentity keeps the answer honest when the profile changes underneath
// it.
//
// Asked again rather than merely cleared, but only when it is on screen and had
// been asked before: this is the one call that goes to AWS rather than to a
// local file, and half a second per switch is not worth spending on a line
// nobody is looking at.
function refreshIdentity() {
  if (identityFor === null || identityFor === state.profile) return;
  clearIdentity();
  if (!ui.expanded.hidden && state.profile) loadIdentity();
}

async function loadIdentity() {
  const asked = state.profile;
  if (!asked) return;
  const request = ++identityRequest;
  identityFor = asked;
  ui.identityButton.disabled = true;
  ui.identityText.textContent = "Asking AWS…";
  try {
    const id = await post("/api/whoami", { profile: asked });
    if (request !== identityRequest || state.profile !== asked) return;
    if (id.profile !== asked) throw new Error("The profile changed. Check again.");
    ui.identityText.textContent = [id.account && `Account ${id.account}`, id.arn, id.caller_id_error]
      .filter(Boolean).join(" · ");
    ui.identityButton.textContent = "Check again";
  } catch (error) {
    if (request !== identityRequest || state.profile !== asked) return;
    ui.identityText.textContent = String(error.message || error);
  } finally {
    if (request === identityRequest) ui.identityButton.disabled = false;
  }
}

// --- actions ---------------------------------------------------------------

// SLOW is how long an action may take before the panel explains itself.
//
// Switching to a profile whose SSO session has lapsed makes awsm open a browser
// and wait for the sign-in, which can take as long as a person takes. A dimmed
// panel and no words is indistinguishable from a panel that has silently done
// nothing -- which is exactly how it was read.
const SLOW = 2000;

// getRandomValues also works in the desktop webview's custom URL scheme;
// randomUUID is restricted to secure contexts in some WebKit versions.
function operationID() {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, "0")).join("");
}

// run performs an action, saying what it is doing while it runs and turning any
// failure into a message rather than a silence.
async function run(action, { refresh = true, doing = "", stoppable = false } = {}) {
  if (activeAction) return;
  const operation = { id: operationID(), doing, stoppable, slow: false, cancelling: false };
  activeAction = operation;
  ++stateRequest; // Responses requested before this action cannot restore old state.
  document.body.classList.add("busy");
  ui.list.setAttribute("aria-busy", "true");
  renderNotice();
  const explain = doing ? setTimeout(() => {
    if (activeAction !== operation) return;
    operation.slow = true;
    renderNotice();
  }, SLOW) : undefined;
  let stopped = false;
  let failure = "";
  try {
    const result = await action(operation.id);
    stopped = Boolean(result?.cancelled);
  } catch (error) {
    failure = String(error.message || error);
  } finally {
    if (refresh || reloadAfterAction) {
      try {
        do {
          reloadAfterAction = false;
          await load({ duringAction: true });
        } while (reloadAfterAction);
      } catch (error) { failure ||= String(error.message || error); }
    }
    if (failure) state.error = failure;
    clearTimeout(explain);
    activeAction = null;
    reloadAfterAction = false;
    document.body.classList.remove("busy");
    ui.list.setAttribute("aria-busy", "false");
    renderNotice();
    if (stopped) flash("Cancelled.");
  }
}

// showProgress writes into the same strip the warnings use, so there is one
// place to look rather than two.
//
// Cancel appears only for the actions Go actually registered as stoppable --
// the ones that can wait on a person. Offering it on a local file read would be
// a button that does nothing, which is worse than no button.
function showProgress(message, stoppable = false) {
  ui.notice.replaceChildren(noticeText(message));
  if (stoppable) {
    const button = noticeButton(activeAction?.cancelling ? "Cancelling…" : "Cancel", cancelRunning);
    button.disabled = Boolean(activeAction?.cancelling);
    ui.notice.append(button);
  }
  ui.notice.className = "notice progress";
  ui.notice.hidden = false;
}

// flash says something for a moment and then goes back to whatever the panel
// was showing.
let flashTimer;
function flash(message) {
  if (activeAction) return;
  ui.notice.replaceChildren(noticeText(message));
  ui.notice.className = "notice progress";
  ui.notice.hidden = false;
  clearTimeout(flashTimer);
  flashTimer = setTimeout(renderNotice, 2500);
}

async function cancelRunning() {
  const operation = activeAction;
  if (!operation?.stoppable || operation.cancelling) return;
  operation.cancelling = true;
  renderNotice();
  try {
    const result = await post("/api/cancel", { operationId: operation.id });
    if (!result.cancelled && activeAction === operation) {
      operation.cancelling = false;
      renderNotice();
    }
  }
  catch (error) {
    if (activeAction !== operation) return;
    operation.cancelling = false;
    operation.doing = `Could not cancel: ${error.message || error}`;
    renderNotice();
  }
}

function activate(profile) {
  if (!profile) return;
  if (profile.mfa_serial) {
    toTerminal(profile.name);
    return;
  }
  run(
    async (operationId) => {
      const result = await post("/api/profile/set", { name: profile.name, operationId });
      // A switch that was stopped halfway is not one worth offering again at
      // the top of the list.
      if (!result.cancelled) rememberRecent(profile.name);
      return result;
    },
    { doing: `Switching to ${profile.name}`, stoppable: true },
  );
}

function toTerminal(name) {
  run(
    async () => {
      await post("/api/terminal", { profile: name });
      dismiss();
    },
    { refresh: false, doing: `Opening a terminal for ${name}` },
  );
}

function openConsole(profile, browser) {
  if (!profile) return;
  run(
    async (operationId) => {
      // An empty browser means "whatever the settings say", which keeps the
      // preference in one place instead of two.
      const result = await post("/api/console", {
        profile: profile.name,
        browser: browser ?? "",
        operationId,
      });
      if (!result.cancelled) dismiss();
      return result;
    },
    { refresh: false, doing: `Opening the console for ${profile.name}`, stoppable: true },
  );
}

async function copy(text, what) {
  if (!text) {
    hint(`No ${what} to copy`);
    return;
  }
  try {
    // Through Go rather than navigator.clipboard: the browser API wants a
    // secure context and a gesture it recognises, and the custom scheme this
    // panel is served from satisfies neither.
    await post("/api/copy", { text });
    hint(`Copied ${text}`);
  } catch (error) {
    hint(String(error.message || error));
  }
}

async function load({ duringAction = false } = {}) {
  if (activeAction && !duringAction) { reloadAfterAction = true; return; }
  const request = ++stateRequest;
  let next;
  try { next = await get("/api/state"); }
  catch (error) { if (request !== stateRequest) return; throw error; }
  if (request !== stateRequest) return;
  // Keep the selected profile stable when the list changes under the pointer.
  const selectedName = current()?.name;
  state = { ...next, profiles: next.profiles || [] };
  applyTheme(pendingSettings ? prefs.theme : state.theme);
  renderCurrent();
  render();
  if (selectedName) {
    const index = rows.findIndex(row => row.profile.name === selectedName);
    if (index >= 0) { selected = index; markSelected(); }
  }
}

function expiringSoon(ttl) {
  if (ttl === "expired" || ttl === "0s") return true;
  if (!ttl || ttl === "static") return false;
  let seconds = 0;
  for (const [, amount, unit] of ttl.matchAll(/(\d+(?:\.\d+)?)(h|m|s)/g)) {
    seconds += Number(amount) * ({ h: 3600, m: 60, s: 1 }[unit]);
  }
  return seconds > 0 && seconds < 600;
}

// --- settings --------------------------------------------------------------

function showSettings(open) {
  ui.main.hidden = open;
  ui.settings.hidden = !open;
  if (open) {
    loadSettings();
  } else {
    stopRecording();
    ui.search.focus();
    // Settings can change what the main view has to say -- turning renewal on
    // is the reason this is here -- and coming back to a stale panel would
    // leave a warning on screen that has just been dealt with.
    load().catch(() => {});
  }
}

async function loadSettings() {
  if (pendingSettings) return;
  const request = ++settingsRequest;
  try {
    const next = await get("/api/settings");
    if (request !== settingsRequest) return;
    prefs = next;
  } catch (error) {
    if (request !== settingsRequest) return;
    ui.settingsHint.textContent = String(error.message || error);
    return;
  }
  ui.themeSelect.value = prefs.theme || "system";
  ui.browserSelect.value = prefs.browser || "default";
  ui.loginCheckbox.checked = Boolean(prefs.openAtLogin);
  renderRenewal();
  ui.binaryPath.textContent = prefs.binary || "";
  ui.settingsPath.textContent = prefs.settingsPath || "";
  ui.logPath.textContent = prefs.logPath || "";
  applyTheme(prefs.theme);
  renderShortcut();
}

// renderRenewal shows whether credentials are being renewed in the background.
//
// "unknown" stays on screen as itself. The state is read out of output meant
// for a person, and claiming renewal is off when it cannot be determined would
// send the user to fix something that is not broken.
const RENEWAL = {
  enabled: "Running",
  off: "Not running",
  unknown: "Unknown",
};

function renderRenewal() {
  const state = prefs.renewal || "unknown";
  ui.renewalState.textContent = RENEWAL[state] || RENEWAL.unknown;
  ui.renewalEnable.hidden = state !== "off";
}

function enableRenewal() {
  ui.renewalEnable.disabled = true;
  post("/api/daemon/enable")
    .then((result) => {
      prefs.renewal = result.renewal;
      renderRenewal();
    })
    .catch((error) => {
      ui.renewalNote.textContent = String(error.message || error);
    })
    .finally(() => {
      ui.renewalEnable.disabled = false;
    });
}

// The note under the Version row, restored when a check starts again.
const VERSION_NOTE =
  "Check for a newer release. Updates are installed manually.";

function checkForUpdate() {
  ui.versionCheck.disabled = true;
  ui.versionOpen.hidden = true;
  ui.versionState.textContent = "Checking…";
  ui.versionNote.textContent = VERSION_NOTE;

  post("/api/update/check")
    .then((found) => {
      if (found.newer) {
        ui.versionState.textContent = `${found.current} → ${found.latest}`;
        ui.versionOpen.hidden = false;
        ui.versionNote.textContent = `Version ${found.latest} has been released.`;
        return;
      }
      if (!found.comparable) {
        // Either this build carries no version, or the released tag is not one
        // this can read. Both are worth saying plainly rather than dressing up
        // as "up to date", which would be a claim nothing here established.
        ui.versionState.textContent = found.current || "development build";
        ui.versionOpen.hidden = false;
        ui.versionNote.textContent =
          `The latest release is ${found.latest}. This build cannot be compared against it.`;
        return;
      }
      ui.versionState.textContent = found.current;
      ui.versionNote.textContent = `The latest release is ${found.latest}. This is it.`;
    })
    .catch((error) => {
      ui.versionState.textContent = "—";
      ui.versionNote.textContent = String(error.message || error);
    })
    .finally(() => {
      ui.versionCheck.disabled = false;
    });
}

function openRelease() {
  post("/api/update/open").catch((error) => {
    ui.versionNote.textContent = String(error.message || error);
  });
}

function renderShortcut() {
  const set = Boolean(prefs.shortcut);
  ui.shortcutButton.textContent = set ? pretty(prefs.shortcut) : "Click and press keys";
  ui.shortcutButton.classList.toggle("set", set);
  ui.shortcutClear.hidden = !set;
}

// pretty renders an accelerator the way a Mac menu would.
function pretty(accelerator) {
  if (!navigator.platform.startsWith("Mac")) return accelerator;
  return accelerator
    .replace("CmdOrCtrl", "⌘")
    .replace("Alt", "⌥")
    .replace("Shift", "⇧")
    .replace("Ctrl", "⌃")
    .replaceAll("+", "");
}

function startRecording() {
  recording = true;
  ui.shortcutButton.classList.add("recording");
  ui.shortcutButton.textContent = "Press a combination…";
  ui.shortcutNote.textContent = "Press Escape to cancel. A modifier is required.";
}

function stopRecording() {
  recording = false;
  ui.shortcutButton.classList.remove("recording");
  ui.shortcutNote.textContent =
    "Opens the panel from anywhere. None is set until you choose one.";
  renderShortcut();
}

// captureShortcut turns a key event into the accelerator Go expects.
//
// A shortcut without a modifier would swallow that key everywhere on the
// machine, so it is refused rather than registered and regretted.
function captureShortcut(event) {
  if (event.key === "Escape") {
    stopRecording();
    return;
  }
  if (["Shift", "Control", "Alt", "Meta"].includes(event.key)) return;

  const parts = [];
  if (event.metaKey || event.ctrlKey) parts.push("CmdOrCtrl");
  if (event.altKey) parts.push("Alt");
  if (event.shiftKey) parts.push("Shift");
  if (parts.length === 0) {
    ui.shortcutNote.textContent =
      "That would take the key from every other application. Add ⌘, ⌥ or ⌃.";
    return;
  }
  parts.push(event.key.length === 1 ? event.key.toUpperCase() : event.key);

  saveSettings({ shortcut: parts.join("+") });
  stopRecording();
}

function saveSettings(changes) {
  settingsRequest++;
  pendingSettings++;
  ui.settingsHint.textContent = "Saving…";
  // Build the next payload only after the preceding save has settled. Rapid
  // changes are merged into the last confirmed preferences, never a stale copy.
  settingsQueue = settingsQueue.then(async () => {
    const next = {
      shortcut: prefs.shortcut || "", browser: prefs.browser || "default",
      theme: prefs.theme || "system", openAtLogin: Boolean(prefs.openAtLogin), ...changes,
    };
    try {
      prefs = await post("/api/settings", next);
      ui.settingsHint.textContent = "Saved";
    } catch (error) {
      ui.settingsHint.textContent = String(error.message || error);
      try { prefs = await get("/api/settings"); } catch { /* Keep last confirmed preferences. */ }
    } finally {
      pendingSettings--;
      applyTheme(prefs.theme);
      ui.themeSelect.value = prefs.theme || "system";
      ui.browserSelect.value = prefs.browser || "default";
      ui.loginCheckbox.checked = Boolean(prefs.openAtLogin);
      renderShortcut();
    }
  });
  return settingsQueue;
}

function clearActive() {
  const profile = state.profile;
  if (profile) return run(() => post("/api/clear", { profile }), { doing: "Clearing the active profile" });
}

// --- keyboard --------------------------------------------------------------

document.addEventListener("keydown", (event) => {
  if (recording) {
    event.preventDefault();
    captureShortcut(event);
    return;
  }
  if (!ui.settings.hidden) {
    if (event.key === "Escape") {
      event.preventDefault();
      showSettings(false);
    }
    return;
  }

  // The panel dims itself while an action runs, but dimming only stops the
  // mouse: pointer-events says nothing about the keyboard. Pressing return
  // again because the panel looked stuck started a second switch, and a second
  // switch cancels the first -- so impatience killed the sign-in it was waiting
  // for. Escape still works, because getting the panel out of the way is not
  // an action on a profile.
  if (document.body.classList.contains("busy") && event.key !== "Escape") {
    if (event.key !== "Tab" && !event.target.closest?.("#notice button")) event.preventDefault();
    return;
  }
  if (event.key !== "Escape" && event.target.closest?.("button, select, input:not(#search), textarea")) return;

  switch (event.key) {
    case "ArrowDown":
    case "ArrowUp": {
      event.preventDefault();
      if (!rows.length) return;
      selected = (selected + (event.key === "ArrowDown" ? 1 : -1) + rows.length) % rows.length;
      markSelected();
      updateFooter(rows.length === state.profiles.length ? state.profiles.length : rows.length);
      return;
    }
    case "Escape":
      event.preventDefault();
      if (ui.search.value) {
        ui.search.value = "";
        render();
      } else {
        dismiss();
      }
      return;
  }

  if (event.target.closest?.("button, select, input:not(#search), textarea")) return;
  const profile = current();
  const command = event.metaKey || event.ctrlKey;

  // Clear is the one action that has nothing to do with the selection: it acts
  // on whatever is active, which is what the panel shows at the top.
  if (command && event.key.toLowerCase() === "k") {
    event.preventDefault();
    if (state.profile) {
      clearActive();
    }
    return;
  }

  // Everything below acts on the selected profile. None of it activates the
  // profile except Enter on its own: opening a console for an account is not
  // the same as starting to work in it.

  if (profile && event.key === "Enter") {
    event.preventDefault();
    if (command) {
      openConsole(profile, event.shiftKey ? "firefox" : undefined);
    } else {
      activate(profile);
    }
    return;
  }

  if (profile && command) {
    switch (event.key.toLowerCase()) {
      case "c":
        event.preventDefault();
        if (event.shiftKey) {
          copy(profile.name, "profile name");
        } else {
          copy(profile.account_id, "account id");
        }
        return;
      case "t":
        event.preventDefault();
        toTerminal(profile.name);
        return;
      case "f":
        event.preventDefault();
        openConsole(profile, "firefox");
        return;
    }
  }

  // Any other key belongs in the search field, wherever the focus is.
  if (!command && document.activeElement !== ui.search && event.key.length === 1) {
    ui.search.focus();
  }
});

// --- wiring ----------------------------------------------------------------

ui.search.addEventListener("input", () => {
  selected = 0;
  render();
});

ui.current.addEventListener("click", () => {
  if (state.profile) setExpanded(ui.expanded.hidden);
});
ui.identityButton.addEventListener("click", loadIdentity);
ui.clearButton.addEventListener("click", () =>
  clearActive(),
);
ui.regionSelect.addEventListener("change", () => {
  const region = ui.regionSelect.value;
  if (region && region !== state.region) {
    run(() => post("/api/region", { profile: state.profile, region }), {
      doing: `Setting the region to ${region}`,
    });
  }
});

ui.settingsButton.addEventListener("click", () => showSettings(true));
ui.backButton.addEventListener("click", () => showSettings(false));
ui.shortcutButton.addEventListener("click", () => (recording ? stopRecording() : startRecording()));
ui.shortcutClear.addEventListener("click", () => saveSettings({ shortcut: "" }));
ui.themeSelect.addEventListener("change", () => saveSettings({ theme: ui.themeSelect.value }));
ui.browserSelect.addEventListener("change", () => saveSettings({ browser: ui.browserSelect.value }));
ui.loginCheckbox.addEventListener("change", () =>
  saveSettings({ openAtLogin: ui.loginCheckbox.checked }),
);
ui.renewalEnable.addEventListener("click", enableRenewal);
ui.versionCheck.addEventListener("click", checkForUpdate);
ui.versionOpen.addEventListener("click", openRelease);
ui.quitButton.addEventListener("click", () => post("/api/quit"));

// watchForChanges listens for the session changing without the page's
// involvement.
//
// The right click menu switches profiles entirely in Go, so nothing here runs
// and the panel goes on showing the profile you just switched away from.
//
// Registered on DOMContentLoaded rather than here and now, and that ordering is
// load bearing: the Wails runtime is a module script, which the browser defers
// until parsing is done, while this file is a plain script that runs the moment
// it is reached. window.wails does not exist yet at this point in the file.
function watchForChanges() {
  const events = window.wails?.Events;
  if (!events) {
    // No Wails behind the page, which is the case under ./devserver. The panel
    // still reloads whenever it is shown.
    return;
  }
  events.On("awsm:session-changed", () => {
    // An action started here reloads itself when it finishes. Reloading now as
    // well would wipe the progress message it is in the middle of showing.
    if (activeAction) { reloadAfterAction = true; return; }
    load().catch(() => {});
  });
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", watchForChanges);
} else {
  watchForChanges();
}

// The window is hidden rather than closed, so it comes back with whatever was
// left on screen. Reloading on show is what keeps it honest.
window.addEventListener("focus", () => {
  if (!ui.settings.hidden) return;
  ui.search.select();
  load().catch(() => {});
});

ui.search.focus();
load().catch((error) => {
  state = { profiles: [], error: String(error.message || error) };
  renderCurrent();
  render();
});
