package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"awsm-desktop/internal/awsm"
	"awsm-desktop/internal/panel"
	"awsm-desktop/internal/settings"
	"awsm-desktop/internal/trayicon"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestAHealthySessionIsADotAndNothingElse(t *testing.T) {
	// A wide status bar item is the first thing macOS drops when the menu bar
	// fills up, and the profile name is forty characters. The dot says the one
	// thing worth saying at a glance; the name is one click away.
	got := label(panel.StatusOnly{
		Profile: "besharp-besharp-isotopes-administratoraccess",
		Region:  "eu-west-1",
		TTL:     "47m41s",
	}, "")

	if got != "●" {
		t.Errorf("got %q, want just the dot", got)
	}
	if colour := dot(panel.StatusOnly{Profile: "work", TTL: "47m41s"}); colour != dotGreen {
		t.Errorf("the dot is %q, want green", colour)
	}
}

func TestTheStatusBarSpeaksUpWhenAPersonIsNeeded(t *testing.T) {
	// Blocked means nothing will renew until someone acts, so the label earns
	// its width by saying which kind of acting -- and the dot turns amber, so
	// the words are not the only thing carrying it.
	got := label(panel.StatusOnly{Profile: "work", TTL: "2m", Blocked: "MFA"}, "")
	if want := "● MFA"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTheCountdownAppearsOnlyNearTheEnd(t *testing.T) {
	cases := map[string]string{
		"47m41s":  "●",         // plenty of time: the dot and nothing else
		"2h15m":   "●",         // likewise
		"9m59s":   "● 9m59s",   // inside the threshold
		"30s":     "● 30s",     // nearly gone
		"expired": "● expired", // already gone
		"static":  "●",         // these never expire
		"":        "●",         // nothing cached to report
	}
	for ttl, want := range cases {
		got := label(panel.StatusOnly{Profile: "work", TTL: ttl}, "")
		if got != want {
			t.Errorf("TTL %q gave label %q, want %q", ttl, got, want)
		}
	}
}

func TestNothingActiveMeansNoDotAtAll(t *testing.T) {
	// The mark on its own. An icon that always carried a dot would be saying
	// something about a session that does not exist.
	if got := label(panel.StatusOnly{}, ""); got != "" {
		t.Errorf("got %q, want nothing beside the mark", got)
	}
	if got := dot(panel.StatusOnly{}); got != "" {
		t.Errorf("got dot %q with no profile set", got)
	}
}

func TestAnUnreadableTTLDoesNotRaiseAnAlarm(t *testing.T) {
	// The value is parsed out of another program's output. If awsm ever prints
	// something unexpected, the right response is silence, not a warning icon
	// that cannot be dismissed.
	if panel.ExpiringSoon("who knows") {
		t.Error("an unparseable TTL should not count as urgent")
	}
	if needsAttention(panel.StatusOnly{Profile: "work", TTL: "who knows"}) {
		t.Error("an unparseable TTL should not change the icon")
	}
}

func TestTheDotTurnsAmberExactlyWhenThereAreWordsToRead(t *testing.T) {
	// The two signals have to agree. An amber dot with nothing to explain it,
	// or a warning in words next to a green dot, would leave the user guessing
	// which to believe -- and the words are what someone who cannot tell the
	// two colours apart is reading.
	for _, status := range []panel.StatusOnly{
		{Profile: "work", TTL: "47m41s"},
		{Profile: "work", TTL: "3m"},
		{Profile: "work", TTL: "expired"},
		{Profile: "work", TTL: "47m41s", Blocked: "SSO"},
		{Profile: "work", TTL: "static"},
	} {
		amber := dot(status) == dotAmber
		words := strings.TrimSpace(strings.TrimPrefix(label(status, ""), dotGlyph))
		if amber != (words != "") {
			t.Errorf("the dot and the words disagree for %+v: dot %q, label %q",
				status, dot(status), label(status, ""))
		}
	}
}

func TestNoProfileMeansNoLabel(t *testing.T) {
	// There is an icon now, so an empty label still leaves something to click.
	if got := label(panel.StatusOnly{}, ""); got != "" {
		t.Errorf("got %q, want no text", got)
	}
}

func TestTheTooltipKeepsWhatTheLabelNoLongerShows(t *testing.T) {
	// With the name gone from the menu bar, hovering is how you check which
	// profile is active without opening the panel.
	full := "besharp-besharp-isotopes-administratoraccess"
	got := tooltip(panel.StatusOnly{Profile: full, Region: "eu-west-1", TTL: "47m"}, "")

	for _, want := range []string{full, "eu-west-1", "47m"} {
		if !strings.Contains(got, want) {
			t.Errorf("tooltip %q is missing %q", got, want)
		}
	}
}

func TestSomethingWaitingOnTheUserGetsSaidInTheMenuBar(t *testing.T) {
	// This is the one thing that outranks a healthy session's silence. When
	// awsm opens a browser for an SSO sign-in, the browser takes the focus and
	// what the user is looking at is no longer the panel -- so the menu bar has
	// to carry the message.
	healthy := panel.StatusOnly{Profile: "work", Region: "eu-west-1", TTL: "47m41s"}

	if got := label(healthy, "Switching…"); got != "● Switching…" {
		t.Errorf("got %q, want the dot and the activity", got)
	}
	// Even over a warning: the warning describes a state, the activity is
	// happening now and will change that state.
	blocked := panel.StatusOnly{Profile: "work", TTL: "2m", Blocked: "SSO"}
	if got := label(blocked, "Signing in…"); got != "● Signing in…" {
		t.Errorf("got %q, want the activity to win over the warning", got)
	}
	// And the words go away again, leaving the dot.
	if got := label(healthy, ""); got != "●" {
		t.Errorf("got %q, want just the dot once nothing is running", got)
	}
}

func TestTheTooltipKeepsTheProfileWhileSomethingRuns(t *testing.T) {
	// Losing which profile is being switched to would make the tooltip less
	// useful exactly when the label is at its least specific.
	got := tooltip(panel.StatusOnly{Profile: "work", Region: "eu-west-1"}, "Switching…")

	for _, want := range []string{"Switching…", "work", "eu-west-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("tooltip %q is missing %q", got, want)
		}
	}
}

func TestTheContextMenuRuntimeIsReachable(t *testing.T) {
	// The native right click menu is opened by a script the page loads from
	// /wails/runtime.js. With a custom handler in place, Wails forwards that
	// request to us rather than answering it, so a plain file server returns
	// 404 and the menu silently falls back to the webview's own -- which
	// offers Reload and nothing else. This asserts the wiring that fixes it.
	assets, err := fs.Sub(assets, "assets")
	if err != nil {
		t.Fatal(err)
	}
	server := panel.New(
		awsm.NewWithBinary(filepath.Join(t.TempDir(), "awsm")),
		assets,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		func() {},
	)
	server.UseAssetHandler(application.BundledAssetFileServer(assets))

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/wails/runtime.js", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("/wails/runtime.js returned %d: the context menu cannot work", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "custom-contextmenu") {
		t.Error("the script served is not the runtime that reads --custom-contextmenu")
	}
}

func TestThePageAsksForTheRuntime(t *testing.T) {
	// The handler can serve it and the page can still forget to load it.
	page, err := assets.ReadFile("assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "/wails/runtime.js") {
		t.Error("index.html does not load the runtime, so right click will show the webview's own menu")
	}
}

func TestTheBundleIdentifierMatchesTheOneThatIsPackaged(t *testing.T) {
	// Three things are named after this: the single instance lock, the login
	// item registration, and where macOS files this application's permissions.
	// If the constant here and the identifier in the packaging script drift
	// apart, all three break quietly -- a second copy starts, and the login
	// item points at something that is no longer there.
	script, err := os.ReadFile("build/package.sh")
	if err != nil {
		t.Fatal(err)
	}
	if want := `identifier="` + bundleID + `"`; !strings.Contains(string(script), want) {
		t.Errorf("build/package.sh does not contain %s", want)
	}
}

func TestTheDotAgreesWithTheRestOfTheStatusBar(t *testing.T) {
	// Three signals sit on the same item -- the dot, the text and the tooltip
	// -- and a green dot next to "⚠ MFA" would leave the user deciding which
	// of them to believe.
	cases := map[string]struct {
		status panel.StatusOnly
		want   trayicon.Status
	}{
		"nothing set":      {panel.StatusOnly{}, trayicon.Idle},
		"working normally": {panel.StatusOnly{Profile: "work", TTL: "47m41s"}, trayicon.Active},
		"keys that do not expire": {
			panel.StatusOnly{Profile: "work", TTL: "static"}, trayicon.Active,
		},
		"nearly expired":  {panel.StatusOnly{Profile: "work", TTL: "3m"}, trayicon.Attention},
		"already expired": {panel.StatusOnly{Profile: "work", TTL: "expired"}, trayicon.Attention},
		"blocked on MFA": {
			panel.StatusOnly{Profile: "work", TTL: "47m41s", Blocked: "MFA"}, trayicon.Attention,
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := iconStatus(c.status); got != c.want {
				t.Errorf("icon is %v, want %v", got, c.want)
			}
			// The emoji in the label and the picture drawn for the other
			// platforms have to say the same thing.
			wantDot := map[trayicon.Status]string{
				trayicon.Idle: "", trayicon.Active: dotGreen, trayicon.Attention: dotAmber,
			}[c.want]
			if got := dot(c.status); got != wantDot {
				t.Errorf("dot is %q, want %q", got, wantDot)
			}
		})
	}
}

func TestThePageDeclaresTheMenusGoRegisters(t *testing.T) {
	// The native menu is chosen by a name the page writes into a CSS custom
	// property and Go registers under. Nothing connects the two but the string,
	// so a rename on either side leaves a right click showing the webview's own
	// menu -- which offers Reload and nothing else, and looks exactly like the
	// feature never worked.
	script, err := assets.ReadFile("assets/panel.js")
	if err != nil {
		t.Fatal(err)
	}

	for what, name := range map[string]string{
		"the profile rows":   rowMenu,
		"the active profile": activeMenu,
	} {
		if !strings.Contains(string(script), `"`+name+`"`) {
			t.Errorf("panel.js never mentions %q, so %s has no menu", name, what)
		}
	}

	// And they have to be different, or one would shadow the other.
	if rowMenu == activeMenu {
		t.Error("both menus are registered under the same name")
	}
}

func TestThePageListensForTheEventGoEmits(t *testing.T) {
	// Same shape of drift as the menus above, with a worse failure. A switch
	// from the right click menu happens entirely in Go: if the page is not
	// listening under the name Go emits, the panel simply goes on showing the
	// profile you switched away from, and it looks like the switch did not
	// happen at all.
	script, err := assets.ReadFile("assets/panel.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), `"`+sessionChanged+`"`) {
		t.Errorf("panel.js never listens for %q: the panel will not notice a switch made from the menu", sessionChanged)
	}

	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "app.Event.Emit(sessionChanged)") {
		t.Error("main never emits the event, so nothing tells the page to catch up")
	}
}

func TestTheIdentityAnswerCannotEatItsOwnButton(t *testing.T) {
	// The button used to live inside the element the answer was written into,
	// so the first check replaced it with text and there was no way to ask
	// again. Structural, because that is what the bug was: not a wrong value,
	// a wrong place to put it.
	page, err := assets.ReadFile("assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	script, err := assets.ReadFile("assets/panel.js")
	if err != nil {
		t.Fatal(err)
	}

	const answer = `id="identityText"`
	start := strings.Index(string(page), answer)
	if start < 0 {
		t.Fatal("the identity answer has no element of its own to go into")
	}
	end := strings.Index(string(page)[start:], "</span>")
	if end < 0 {
		t.Fatal(`the element holding the identity answer is never closed`)
	}
	if strings.Contains(string(page)[start:start+end], "<button") {
		t.Error("the button sits inside the element the answer is written into; writing the answer would delete it")
	}

	// And the script has to write into that element rather than the container
	// the button is a sibling in.
	if strings.Contains(string(script), "ui.identity.textContent") {
		t.Error("the script writes into the container, which replaces everything inside it including the button")
	}
}

func TestEverythingAboutTheActiveProfileIsRedrawnWithIt(t *testing.T) {
	// Two things under the header describe the active profile and were each
	// filled only when the details were opened: the identity and the region
	// picker. After a switch they went on describing the profile you had just
	// left -- the picker disagreeing with the region printed two lines above
	// it, out of the same state.
	//
	// A source check, because the thing worth pinning down is that the redraw
	// is wired at all. It is the kind of wiring that gets dropped by someone
	// tidying up a function they think does too much.
	script, err := assets.ReadFile("assets/panel.js")
	if err != nil {
		t.Fatal(err)
	}
	body, err := functionBody(string(script), "renderCurrent")
	if err != nil {
		t.Fatal(err)
	}

	for what, call := range map[string]string{
		"the identity":      "refreshIdentity()",
		"the region picker": "fillRegions()",
	} {
		if !strings.Contains(body, call) {
			t.Errorf("renderCurrent never calls %s, so %s keeps describing the previous profile", call, what)
		}
	}
}

// functionBody returns the source of a top level function, which in this file
// means everything up to the first closing brace in the first column.
func functionBody(script, name string) (string, error) {
	opening := "function " + name + "() {"
	start := strings.Index(script, opening)
	if start < 0 {
		return "", fmt.Errorf("no function %s in panel.js", name)
	}
	start += len(opening)

	end := strings.Index(script[start:], "\n}")
	if end < 0 {
		return "", fmt.Errorf("function %s is never closed", name)
	}
	return script[start : start+end], nil
}

func TestTheKeyboardIsIgnoredWhileAnActionRuns(t *testing.T) {
	// Dimming the panel stops the mouse and nothing else: pointer-events says
	// nothing about the keyboard. Pressing return again because the panel looked
	// stuck started a second switch, and a second switch cancels the first, so
	// impatience killed the sign-in it was waiting for.
	//
	// Escape is deliberately still allowed: getting the panel out of the way is
	// not an action on a profile.
	script, err := assets.ReadFile("assets/panel.js")
	if err != nil {
		t.Fatal(err)
	}
	const guard = `if (document.body.classList.contains("busy") && event.key !== "Escape")`
	if !strings.Contains(string(script), guard) {
		t.Error("the keydown handler does not stand down while an action runs")
	}
}

// fakeKeys stands in for the framework's shortcut manager.
type fakeKeys struct {
	held     map[string]bool
	refuse   map[string]bool
	attempts []string
}

func newFakeKeys(refuse ...string) *fakeKeys {
	keys := &fakeKeys{held: map[string]bool{}, refuse: map[string]bool{}}
	for _, accelerator := range refuse {
		keys.refuse[accelerator] = true
	}
	return keys
}

func (k *fakeKeys) Register(accelerator string, _ func()) error {
	k.attempts = append(k.attempts, accelerator)
	if k.refuse[accelerator] {
		return errors.New("already taken by another application")
	}
	k.held[accelerator] = true
	return nil
}

func (k *fakeKeys) Unregister(accelerator string) error {
	delete(k.held, accelerator)
	return nil
}

func TestARefusedShortcutDoesNotCostYouTheOneYouHad(t *testing.T) {
	// Changing a shortcut means releasing the old combination before claiming
	// the new one. When the new one is refused -- which is ordinary, most of
	// the good combinations are already owned by something -- the old one has
	// already been let go, and the user is left with no shortcut at all having
	// asked only to change it.
	keys := newFakeKeys("CmdOrCtrl+Space")
	s := &shortcut{keys: keys, open: func() {}}

	if err := s.apply(settings.Settings{Shortcut: "CmdOrCtrl+Shift+A"}); err != nil {
		t.Fatalf("registering the first shortcut: %v", err)
	}
	if !keys.held["CmdOrCtrl+Shift+A"] {
		t.Fatal("the first shortcut was never registered")
	}

	err := s.apply(settings.Settings{Shortcut: "CmdOrCtrl+Space"})
	if err == nil {
		t.Fatal("expected the refused combination to be reported")
	}

	if !keys.held["CmdOrCtrl+Shift+A"] {
		t.Error("the working shortcut was lost when the new one was refused")
	}
	if s.current != "CmdOrCtrl+Shift+A" {
		t.Errorf("the panel thinks %q is registered, want the one that actually is", s.current)
	}
}

func TestClearingTheShortcutReleasesIt(t *testing.T) {
	// The other direction: asking for none must leave none, not put the old one
	// back out of an over-eager recovery.
	keys := newFakeKeys()
	s := &shortcut{keys: keys, open: func() {}}

	if err := s.apply(settings.Settings{Shortcut: "CmdOrCtrl+Shift+A"}); err != nil {
		t.Fatal(err)
	}
	if err := s.apply(settings.Settings{Shortcut: ""}); err != nil {
		t.Fatal(err)
	}

	if len(keys.held) != 0 {
		t.Errorf("still holding %v after the shortcut was cleared", keys.held)
	}
	if s.current != "" {
		t.Errorf("the panel thinks %q is registered", s.current)
	}
}

func TestBothArchitecturesAreBuiltAndChecked(t *testing.T) {
	// A Mac is Apple silicon or Intel, and a bundle built for the other one
	// does not start at all. Both are cross compiled on the same runner, which
	// is exactly the arrangement where they quietly come out identical -- so
	// the workflow has to build two and then prove they are two.
	//
	// Nothing here can fail in CI: it fails when someone downloads the wrong
	// half and says it is broken.
	script, err := os.ReadFile("build/package.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), `arch="${ARCH:-$(go env GOARCH)}"`) {
		t.Error("build/package.sh no longer takes an architecture, so both builds would be the host's")
	}

	workflow, err := os.ReadFile(".github/workflows/build.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`for arch in arm64 amd64; do`,     // both are built
		`ARCH="$arch" ./build/package.sh`, // and built as that architecture
		`lipo -archs`,                     // and what came out is checked
		`dist/awsm-macos-*.zip`,           // and both are released
	} {
		if !strings.Contains(string(workflow), want) {
			t.Errorf("the workflow no longer contains %q", want)
		}
	}
}
