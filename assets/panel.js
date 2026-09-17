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
  row.title = [profile.name, profile.sso_session, profile.account_id, profile.type]
    .filter(Boolean)
    .join(" · ");

  const name = document.createElement("div");
  name.className = "row-name";
  name.textContent = profile.name;
  if (profile.is_active) name.append(badge("active"));
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
  rows.forEach((row, index) => row.element.classList.toggle("selected", index === selected));
  rows[selected]?.element.scrollIntoView({ block: "nearest" });
}

const current = () => rows[selected]?.profile;

const KEY_HINT = "↵ switch · ⌘↵ console · ⌘C copy · right click for more";

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
  if (active) ui.dot.classList.add(state.blocked ? "warn" : "on");
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
          run(() => post("/api/sso/login", { session: name }), {
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
  identityFor = null;
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
  ui.identityText.textContent = "Asking AWS…";

  try {
    // This one calls STS, about half a second against five milliseconds for
    // everything else, so it is never part of opening the panel.
    const id = await post("/api/whoami");

    // The profile can change while STS is answering, and the answer would then
    // be about an account nobody asked about.
    if (state.profile !== asked) return;

    identityFor = asked;
    // Deliberately not the expiry: the header already shows a countdown, and
    // this one is measured differently, so two disagreeing times would sit on
    // the same panel.
    ui.identityText.textContent = [id.account && `Account ${id.account}`, id.arn, id.caller_id_error]
      .filter(Boolean)
      .join(" · ");
    ui.identityButton.textContent = "Check again";
  } catch (error) {
    if (state.profile !== asked) return;
    ui.identityText.textContent = String(error.message || error);
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

// run performs an action, saying what it is doing while it runs and turning any
// failure into a message rather than a silence.
async function run(action, { refresh = true, doing = "", stoppable = false } = {}) {
  document.body.classList.add("busy");
  if (doing) showProgress(`${doing}…`, stoppable);

  const explain = doing
    ? setTimeout(() => {
        showProgress(`${doing}… awsm may have opened a browser for you to sign in.`, stoppable);
      }, SLOW)
    : undefined;

  // Go answers a stopped command with this rather than with an error: being
  // cancelled is an outcome, not a failure.
  let stopped = false;

  try {
    const result = await action();
    if (refresh) await load();
    stopped = Boolean(result && result.cancelled);
  } catch (error) {
    state.error = String(error.message || error);
    renderNotice();
  } finally {
    clearTimeout(explain);
    document.body.classList.remove("busy");
    // Anything that finished without reloading has to clear its own message;
    // load() repaints the notice by itself.
    if (doing && !refresh) renderNotice();
    // Last, so neither of the two lines above wipes it.
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
  if (stoppable) ui.notice.append(noticeButton("Cancel", cancelRunning));
  ui.notice.className = "notice progress";
  ui.notice.hidden = false;
}

// flash says something for a moment and then goes back to whatever the panel
// was showing.
let flashTimer;
function flash(message) {
  ui.notice.replaceChildren(noticeText(message));
  ui.notice.className = "notice progress";
  ui.notice.hidden = false;
  clearTimeout(flashTimer);
  flashTimer = setTimeout(renderNotice, 2500);
}

function cancelRunning() {
  post("/api/cancel").catch(() => {});
}

function activate(profile) {
  if (!profile) return;
  if (profile.mfa_serial) {
    toTerminal(profile.name);
    return;
  }
  run(
    async () => {
      const result = await post("/api/profile/set", { name: profile.name });
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
    async () => {
      // An empty browser means "whatever the settings say", which keeps the
      // preference in one place instead of two.
      const result = await post("/api/console", {
        profile: profile.name,
        browser: browser ?? "",
      });
      dismiss();
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

async function load() {
  state = await get("/api/state");
  state.profiles = state.profiles || [];
  applyTheme(state.theme);
  renderCurrent();
  render();
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
  try {
    prefs = await get("/api/settings");
  } catch (error) {
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
  "Asks GitHub whether a newer release exists. It only looks: nothing is " +
  "downloaded and nothing here is replaced.";

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

async function saveSettings(changes) {
  const next = {
    shortcut: prefs.shortcut || "",
    browser: prefs.browser || "default",
    theme: prefs.theme || "system",
    openAtLogin: Boolean(prefs.openAtLogin),
    ...changes,
  };
  // Applied before the round trip so the panel repaints at once; the reload
  // below puts it back if the write failed.
  if (changes.theme) applyTheme(changes.theme);

  try {
    prefs = await post("/api/settings", next);
    ui.settingsHint.textContent = "";
  } catch (error) {
    ui.settingsHint.textContent = String(error.message || error);
    // The system refused it, so show what is actually in force rather than
    // what was asked for.
    await loadSettings();
    return;
  }
  applyTheme(prefs.theme);
  ui.themeSelect.value = prefs.theme || "system";
  ui.browserSelect.value = prefs.browser || "default";
  ui.loginCheckbox.checked = Boolean(prefs.openAtLogin);
  renderShortcut();
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
    event.preventDefault();
    return;
  }

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

  const profile = current();
  const command = event.metaKey || event.ctrlKey;

  // Clear is the one action that has nothing to do with the selection: it acts
  // on whatever is active, which is what the panel shows at the top.
  if (command && event.key.toLowerCase() === "k") {
    event.preventDefault();
    if (state.profile) {
      run(() => post("/api/clear"), { doing: "Clearing the active profile" });
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
  run(() => post("/api/clear"), { doing: "Clearing the active profile" }),
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
    if (document.body.classList.contains("busy")) return;
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
