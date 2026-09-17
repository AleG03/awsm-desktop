// Package panel serves the status bar panel and the small API behind it.
//
// The panel talks to Go over HTTP rather than through generated bindings. The
// asset server is already an http.Handler, so routing a few JSON endpoints
// through it costs nothing, keeps a generated file out of the repository, and
// leaves the page written in terms every browser already understands.
package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"awsm-desktop/internal/awsm"
	"awsm-desktop/internal/logs"
	"awsm-desktop/internal/settings"
	"awsm-desktop/internal/terminal"
)

// Server answers the panel's requests.
type Server struct {
	client *awsm.Client
	assets fs.FS
	log    *slog.Logger

	// quit ends the application and hide dismisses the panel. They are fields
	// rather than direct calls into the framework so this package stays
	// independent of it and testable.
	quit    func()
	hide    func()
	changed func()

	// The login item and the global shortcut belong to the desktop framework,
	// which this package deliberately does not import. They arrive as
	// functions so the API here can be exercised without one.
	loginItem     func() bool
	setLoginItem  func(bool) error
	applySettings func(settings.Settings) error
	copyText      func(string) bool

	// busy reports that a long operation started or ended, so the status bar
	// can say so and the panel can refuse to hide while it runs.
	busy func(doing string)

	// running is the long operation currently in flight, if any. One at a
	// time: the panel dims itself while an action runs, and a second action
	// arriving from the context menu should replace the first rather than
	// leave two writers on ~/.aws/credentials.
	mu      sync.Mutex
	running *inFlight

	// static serves the page itself. It is replaceable because the desktop
	// framework has its own file server that also answers /wails/runtime.js,
	// and this package does not import that framework.
	static http.Handler
}

// New builds the handler for the panel.
func New(client *awsm.Client, assets fs.FS, log *slog.Logger, quit func()) *Server {
	return &Server{client: client, assets: assets, log: log, quit: quit}
}

// OnHide sets what dismisses the panel. An action that sends the user somewhere
// else -- a browser, a terminal -- should not leave the panel floating on top
// of where they were sent.
func (s *Server) OnHide(hide func()) { s.hide = hide }

// OnLoginItem wires reading and writing the "open at login" registration.
func (s *Server) OnLoginItem(read func() bool, write func(bool) error) {
	s.loginItem, s.setLoginItem = read, write
}

// OnSettings wires what to do when preferences change, which in practice means
// registering or releasing the global shortcut.
func (s *Server) OnSettings(apply func(settings.Settings) error) { s.applySettings = apply }

// UseAssetHandler replaces the handler that serves the page.
//
// It exists for one file. The right click menu is intercepted by the desktop
// framework's runtime script, which the page loads from /wails/runtime.js --
// and with a custom handler in place, the framework forwards that request here
// rather than answering it. Passing in its own file server is what makes the
// path resolve; a plain file server answers 404 and the native menu never
// opens.
func (s *Server) UseAssetHandler(handler http.Handler) { s.static = handler }

// OnCopy wires putting text on the system clipboard.
//
// The page cannot do this for itself: the browser clipboard API needs a secure
// context and a user gesture it can recognise, and neither survives the custom
// scheme the webview serves this panel from. Going through the platform is not
// a workaround, it is the only thing that works.
func (s *Server) OnCopy(copy func(string) bool) { s.copyText = copy }

// OnBusy sets what to call when a long operation starts and ends.
//
// An empty string means idle. It is a callback rather than a direct call into
// the framework for the same reason as the rest of this file: the package does
// not import it.
func (s *Server) OnBusy(busy func(doing string)) { s.busy = busy }

// ErrCancelled reports that the operation was stopped on purpose.
//
// Worth distinguishing from a failure: "cancelled" is an answer, and showing
// the user a red banner saying "signal: killed" for something they asked for
// would be the panel blaming them for its own obedience.
var ErrCancelled = errors.New("cancelled")

// startLong registers the operation the user is waiting on.
//
// The context is derived from Background rather than from whatever the caller
// holds. A profile switch that opens a browser has to outlive the request that
// started it, the context menu callback that returned, and the page reloading
// itself -- none of which mean the user changed their mind.
//
// Only one of these runs at a time and a new one replaces the old, because they
// are the operations that write credentials and two writers on
// ~/.aws/credentials would be a bug nobody could reproduce. Everything else
// that merely takes a while -- opening a console, above all -- stays out of
// here: cancelling a sign-in because somebody looked at another account was
// worse than any race it was protecting against.
func (s *Server) startLong(doing string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	operation := &inFlight{cancel: cancel}

	s.mu.Lock()
	previous := s.running
	s.running = operation
	s.mu.Unlock()

	if previous != nil {
		previous.cancel()
	}
	s.notifyBusy(doing)

	return ctx, func() {
		s.mu.Lock()
		// Ours only if nothing has replaced it. Comparing the operation rather
		// than the function is deliberate: two closures made from the same
		// literal are indistinguishable by pointer.
		current := s.running == operation
		if current {
			s.running = nil
		}
		s.mu.Unlock()

		cancel()

		// Only the operation still being waited on may say the waiting is over.
		// A superseded one announcing idle would take the panel's hold off, and
		// wipe the status bar, while its replacement was still running.
		if current {
			s.notifyBusy("")
		}
	}
}

// inFlight is one long operation, identified by the address of this value.
type inFlight struct {
	cancel context.CancelFunc
}

// Cancel stops whatever long operation is running, and reports whether there
// was one.
func (s *Server) Cancel() bool {
	s.mu.Lock()
	operation := s.running
	s.mu.Unlock()

	if operation == nil {
		return false
	}

	// Left in the registry on purpose. The operation's own release clears it,
	// and that release is also what tells the panel the waiting is over --
	// taking the entry out from under it means the release no longer recognises
	// itself as the current one, and the status bar goes on saying "Switching…"
	// for good.
	operation.cancel()
	return true
}

// busyNow reports whether an operation is registered, for tests that need to
// wait for one to start.
func (s *Server) busyNow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running != nil
}

func (s *Server) notifyBusy(doing string) {
	if s.busy != nil {
		s.busy(doing)
	}
}

// cancelled turns the error from a stopped command into ErrCancelled.
//
// What the command itself reports is "signal: killed", which is true and
// useless.
func cancelled(ctx context.Context, err error) error {
	if err != nil && errors.Is(ctx.Err(), context.Canceled) {
		return ErrCancelled
	}
	return err
}

// OnChanged sets what to call after an action that alters the active session.
//
// Without it the status bar would only catch up on its next tick, and a menu
// bar that lags a click by fifteen seconds reads as broken rather than as
// eventually consistent.
func (s *Server) OnChanged(changed func()) { s.changed = changed }

// notifyChanged reports that the active session may have changed.
func (s *Server) notifyChanged() {
	if s.changed != nil {
		s.changed()
	}
}

// Handler routes the API and falls back to the embedded page.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/profile/set", s.handleSetProfile)
	mux.HandleFunc("POST /api/console", s.handleConsole)
	mux.HandleFunc("POST /api/clear", s.handleClear)
	mux.HandleFunc("POST /api/whoami", s.handleWhoami)
	mux.HandleFunc("POST /api/sso/login", s.handleSSOLogin)
	mux.HandleFunc("POST /api/region", s.handleRegion)
	mux.HandleFunc("POST /api/terminal", s.handleTerminal)
	mux.HandleFunc("GET /api/regions", s.handleRegions)
	mux.HandleFunc("GET /api/settings", s.handleReadSettings)
	mux.HandleFunc("POST /api/settings", s.handleWriteSettings)
	mux.HandleFunc("POST /api/copy", s.handleCopy)
	mux.HandleFunc("POST /api/daemon/enable", s.handleEnableDaemon)
	mux.HandleFunc("POST /api/cancel", s.handleCancel)
	mux.HandleFunc("POST /api/hide", s.handleHide)
	mux.HandleFunc("POST /api/quit", s.handleQuit)

	static := s.static
	if static == nil {
		static = http.FileServer(http.FS(s.assets))
	}
	mux.Handle("/", noStore(static))
	return mux
}

// noStore stops the webview caching the page.
//
// The files are local, so caching them saves nothing measurable, and a stale
// stylesheet served after an edit is a confusing way to spend an afternoon.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// Urgent is the remaining life below which credentials are worth worrying
// about. It matches the threshold awsm's own refresh daemon uses to decide it
// is time to renew.
const Urgent = 10 * time.Minute

// ExpiringSoon parses the remaining life awsm prints.
//
// The value is a duration such as "47m41s", or one of two words: "static" for
// credentials that never expire, and "expired" for ones that already have.
//
// It lives here rather than beside the status bar because two places ask the
// same question -- the menu bar, deciding whether to show a countdown, and the
// panel, deciding whether an idle refresh daemon is worth mentioning -- and two
// thresholds that drifted apart would contradict each other on screen.
func ExpiringSoon(ttl string) bool {
	switch ttl {
	case "", "static":
		return false
	case "expired":
		return true
	}
	remaining, err := time.ParseDuration(ttl)
	if err != nil {
		// An unparseable value is not a reason to cry wolf in the menu bar.
		return false
	}
	return remaining < Urgent
}

// State is everything the panel needs to draw itself.
type State struct {
	Profile   string `json:"profile"`
	Region    string `json:"region"`
	Type      string `json:"type"`
	TTL       string `json:"ttl"`
	AccountID string `json:"accountId"`
	Blocked   string `json:"blocked"`
	Theme     string `json:"theme"`

	// RenewalAtRisk is the one case where an idle refresh daemon is worth
	// interrupting for: it is not running, and the credentials on screen are
	// close enough to expiry that its absence is about to be felt. Saying it
	// at any other time would be a panel nagging about a setting.
	RenewalAtRisk bool `json:"renewalAtRisk"`

	Profiles []awsm.Profile `json:"profiles"`
	Binary   string         `json:"binary"`
	Error    string         `json:"error,omitempty"`
}

// StatusOnly is the cheap half of State, for the status bar's timer.
type StatusOnly struct {
	Profile string `json:"profile"`
	Region  string `json:"region"`
	TTL     string `json:"ttl"`
	Blocked string `json:"blocked"`
}

// Status reads the active profile and the daemon's verdict on it.
//
// Both parts are local file reads, which is what lets the status bar refresh on
// a timer without becoming a background job that costs something.
func (s *Server) Status(ctx context.Context) (StatusOnly, error) {
	status, err := s.client.Status(ctx)
	if err != nil {
		return StatusOnly{}, err
	}
	blocked, _ := awsm.ReadDaemonState().BlockedFor(status.Profile)
	return StatusOnly{
		Profile: status.Profile,
		Region:  status.Region,
		TTL:     status.TTL,
		Blocked: blocked.Short(),
	}, nil
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	// Logged because it is the first thing the page does: seeing this line is
	// how you know the webview reached the handler at all.
	s.log.Info("panel opened")

	state := State{Binary: s.client.Binary(), Theme: settings.Load().Theme}

	status, err := s.client.Status(r.Context())
	if err != nil {
		// A failure here is worth showing rather than hiding: it usually means
		// the binary is missing or too old, and every other call is about to
		// fail the same way.
		state.Error = err.Error()
		writeJSON(w, http.StatusOK, state)
		return
	}
	state.Profile = status.Profile
	state.Region = status.Region
	state.Type = status.Type
	state.TTL = status.TTL
	state.AccountID = status.AccountID

	if blocked, ok := awsm.ReadDaemonState().BlockedFor(status.Profile); ok {
		state.Blocked = string(blocked)
	}

	// Asked on every open because it costs the same few milliseconds as the
	// status itself, and because the answer can change without this panel
	// being involved.
	daemon := s.client.Daemon(r.Context())
	state.RenewalAtRisk = daemon.Known && !daemon.Enabled && ExpiringSoon(status.TTL)

	profiles, err := s.client.Profiles(r.Context())
	if err != nil {
		state.Error = err.Error()
		writeJSON(w, http.StatusOK, state)
		return
	}
	state.Profiles = profiles

	writeJSON(w, http.StatusOK, state)
}

// --- operations ------------------------------------------------------------
//
// These are what the panel can do to a profile. The HTTP handlers below and the
// native context menu both call them, so a right click and a keystroke cannot
// drift into meaning different things.

// Switch makes a profile the active one.
//
// It takes no context. A switch onto a lapsed SSO session becomes a browser
// sign-in that waits for whoever is at the keyboard, which has to outlive the
// request that asked for it.
func (s *Server) Switch(name string) error {
	ctx, done := s.startLong("Switching…")
	defer done()

	if err := s.client.SetProfile(ctx, name); err != nil {
		return cancelled(ctx, err)
	}
	s.notifyChanged()
	return nil
}

// Console opens the AWS console for a profile without making it active.
//
// Not activating it is the point: looking at an account is not the same as
// working in it, and the old panel could do this before a redesign took it
// away.
func (s *Server) Console(name, browser string) error {
	// Deliberately not registered as the operation being waited on, and so not
	// cancelling one. Opening a console writes no credentials and dismisses the
	// panel rather than holding it open -- and registering it meant that
	// looking at another account while a sign-in was in progress killed the
	// sign-in, which reported itself as "Cancelled" by somebody who had
	// cancelled nothing.
	//
	// It still gets the long bound from the client: resolving another profile's
	// credentials can need a sign-in of its own.
	if browser == "" {
		browser = settings.Load().Browser
	}
	return s.client.Console(context.Background(), name, awsm.Browser(browser))
}

// Terminal hands the profile switch to a terminal, for the MFA case.
//
// Not tracked as a long operation: it launches the terminal and returns. The
// waiting happens in there, where the user can see it.
func (s *Server) Terminal(name string) error {
	return terminal.Open(s.client.Binary(), "profile", "set", name)
}

// ClearIfActive lets go of the active profile, if it is still the one asked
// about.
//
// awsm clear takes no argument: it clears whatever is set. The name is checked
// because a context menu can be opened on a header the panel has since
// redrawn, and clearing a profile the user is no longer looking at is not what
// they clicked.
func (s *Server) ClearIfActive(ctx context.Context, name string) error {
	status, err := s.client.Status(ctx)
	if err != nil {
		return err
	}
	if status.Profile != name {
		return fmt.Errorf("%s is no longer the active profile", name)
	}
	if err := s.client.Clear(ctx); err != nil {
		return err
	}
	s.notifyChanged()
	return nil
}

// Copy puts text on the system clipboard.
func (s *Server) Copy(text string) error {
	if text == "" {
		return errors.New("nothing to copy")
	}
	if s.copyText == nil || !s.copyText(text) {
		return errors.New("the clipboard refused the text")
	}
	return nil
}

// AccountFor finds a profile's account id.
//
// Looked up rather than passed in, so the context menu carries only a name and
// cannot act on a stale number.
func (s *Server) AccountFor(ctx context.Context, name string) (string, error) {
	profiles, err := s.client.Profiles(ctx)
	if err != nil {
		return "", err
	}
	for _, profile := range profiles {
		if profile.Name == name {
			if profile.AccountID == "" {
				return "", fmt.Errorf("%s has no account id in the config", name)
			}
			return profile.AccountID, nil
		}
	}
	return "", fmt.Errorf("no profile named %s", name)
}

// NeedsMFA reports whether switching to a profile will ask for a code.
func (s *Server) NeedsMFA(ctx context.Context, name string) bool {
	profiles, err := s.client.Profiles(ctx)
	if err != nil {
		return false
	}
	for _, profile := range profiles {
		if profile.Name == name {
			return profile.NeedsMFA()
		}
	}
	return false
}

// --- handlers --------------------------------------------------------------

func (s *Server) handleSetProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.Switch(body.Name); err != nil {
		s.report(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"profile": body.Name})
}

func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Profile string `json:"profile"`
		Browser string `json:"browser"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.Console(body.Profile, body.Browser); err != nil {
		s.report(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleClear(w http.ResponseWriter, r *http.Request) {
	if err := s.client.Clear(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	s.notifyChanged()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	// This one calls STS and takes about half a second, which is why the panel
	// asks for it separately instead of folding it into the state it opens on.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	identity, err := s.client.Whoami(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, identity)
}

func (s *Server) handleSSOLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Session string `json:"session"`
	}
	if !decode(w, r, &body) {
		return
	}
	// awsm opens the browser itself and waits for the flow, so this needs no
	// terminal and can be an ordinary button -- and for the same reason it
	// holds the panel open and is cancellable.
	ctx, done := s.startLong("Signing in…")
	defer done()

	if err := s.client.SSOLogin(ctx, body.Session); err != nil {
		s.report(w, cancelled(ctx, err))
		return
	}
	s.notifyChanged()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRegion(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Profile string `json:"profile"`
		Region  string `json:"region"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.client.SetRegion(r.Context(), body.Profile, body.Region); err != nil {
		s.fail(w, err)
		return
	}
	s.notifyChanged()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleTerminal hands the profile switch to a terminal.
//
// This is the answer for a profile that needs an MFA code: awsm reads it from
// stdin, and a panel has none to give.
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Profile string `json:"profile"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.Terminal(body.Profile); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleRegions lists the regions offered by the region picker.
func (s *Server) handleRegions(w http.ResponseWriter, r *http.Request) {
	regions, err := s.client.Regions(r.Context())
	if err != nil || len(regions) == 0 {
		// The command has no machine-readable form, so its output is parsed.
		// Rather than fail the picker, fall back to the regions already in use
		// across the configured profiles, which is what the user is switching
		// between anyway.
		if err != nil {
			s.log.Debug("could not list regions", "error", err)
		}
		regions = s.regionsInUse(r.Context())
	}
	writeJSON(w, http.StatusOK, regions)
}

func (s *Server) regionsInUse(ctx context.Context) []string {
	profiles, err := s.client.Profiles(ctx)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var regions []string
	for _, profile := range profiles {
		if profile.Region != "" && !seen[profile.Region] {
			seen[profile.Region] = true
			regions = append(regions, profile.Region)
		}
	}
	sort.Strings(regions)
	return regions
}

// preferences is what the settings view reads and writes.
//
// It carries the login item alongside the stored settings even though the two
// live in different places -- one in a JSON file, one in the operating system's
// own registration -- because to the person looking at the panel they are the
// same list of switches.
type preferences struct {
	Shortcut     string `json:"shortcut"`
	Browser      string `json:"browser"`
	Theme        string `json:"theme"`
	OpenAtLogin  bool   `json:"openAtLogin"`
	Renewal      string `json:"renewal"`
	SettingsPath string `json:"settingsPath"`
	LogPath      string `json:"logPath"`
	Binary       string `json:"binary"`
}

func (s *Server) handleReadSettings(w http.ResponseWriter, _ *http.Request) {
	stored := settings.Load()
	path, _ := settings.Path()
	logPath, _ := logs.Path()

	out := preferences{
		Renewal:      renewal(s.client.Daemon(context.Background())),
		Shortcut:     stored.Shortcut,
		Browser:      stored.Browser,
		Theme:        stored.Theme,
		SettingsPath: path,
		LogPath:      logPath,
		Binary:       s.client.Binary(),
	}
	if s.loginItem != nil {
		out.OpenAtLogin = s.loginItem()
	}
	writeJSON(w, http.StatusOK, out)
}

// renewal names the daemon's state for the settings view.
//
// "unknown" is a real answer and has to stay one. The state is parsed out of
// output meant for a person, and telling someone their credentials are
// unprotected when they are is worse than admitting to not knowing.
func renewal(status awsm.DaemonStatus) string {
	if !status.Known {
		return "unknown"
	}
	if status.Enabled {
		return "enabled"
	}
	return "off"
}

// handleEnableDaemon starts awsm's background credential renewal.
func (s *Server) handleEnableDaemon(w http.ResponseWriter, r *http.Request) {
	if err := s.client.EnableDaemon(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	s.notifyChanged()
	writeJSON(w, http.StatusOK, map[string]string{"renewal": renewal(s.client.Daemon(r.Context()))})
}

func (s *Server) handleWriteSettings(w http.ResponseWriter, r *http.Request) {
	var body preferences
	if !decode(w, r, &body) {
		return
	}

	stored := settings.Settings{Shortcut: body.Shortcut, Browser: body.Browser, Theme: body.Theme}

	// Applied before saving, and not saved if the system refuses it. This used
	// to be the other way round, on the reasoning that the file should record
	// what the user asked for -- but a shortcut another application owns cannot
	// be registered on the next launch either, so the file recorded a
	// preference that silently left the panel with no shortcut at all, while
	// the settings view showed it as the one in force.
	if s.applySettings != nil {
		if err := s.applySettings(stored); err != nil {
			s.fail(w, err)
			return
		}
	}

	if err := settings.Save(stored); err != nil {
		s.fail(w, err)
		return
	}

	if s.setLoginItem != nil && s.loginItem != nil && body.OpenAtLogin != s.loginItem() {
		if err := s.setLoginItem(body.OpenAtLogin); err != nil {
			s.fail(w, err)
			return
		}
	}

	s.handleReadSettings(w, r)
}

func (s *Server) handleCopy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.Copy(body.Text); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"copied": body.Text})
}

// handleCancel stops the long operation in flight.
//
// The long bound on those commands is ten minutes, which is a backstop against
// a wedged process rather than something anyone should sit through. This is the
// way out.
func (s *Server) handleCancel(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": s.Cancel()})
}

func (s *Server) handleHide(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	if s.hide != nil {
		s.hide()
	}
}

func (s *Server) handleQuit(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	if s.quit != nil {
		// After the response, so the page is not left waiting on a connection
		// that dies with the process.
		go s.quit()
	}
}

// report answers the panel, distinguishing a cancellation from a failure.
//
// Being stopped on purpose is an outcome, not an error: showing a red banner
// saying "signal: killed" would be the panel blaming the user for doing as they
// asked.
func (s *Server) report(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrCancelled) {
		s.log.Info("cancelled")
		writeJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
		return
	}
	s.fail(w, err)
}

// fail reports an error to the panel, and logs it where it can be read later.
func (s *Server) fail(w http.ResponseWriter, err error) {
	s.log.Error("command failed", "error", err)
	writeJSON(w, http.StatusOK, map[string]string{"error": err.Error()})
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed request"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	// The panel is local and its data is not cacheable in any useful sense;
	// a cached profile list would show a switch that already happened.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
