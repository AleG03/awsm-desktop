package panel

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"awsm-desktop/internal/awsm"
	"awsm-desktop/internal/settings"
)

// newTestServer builds a server backed by a script standing in for awsm.
func newTestServer(t *testing.T, script string) *Server {
	t.Helper()
	path := filepath.Join(t.TempDir(), "awsm")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	assets := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<h1>panel</h1>")}}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(awsm.NewWithBinary(path), assets, quiet, func() {})
}

func request(t *testing.T, s *Server, method, path, body string) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	var payload map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("response is not JSON: %q", rec.Body.String())
		}
	}
	return rec.Code, payload
}

func TestStateCarriesTheProfileListAndTheActiveProfile(t *testing.T) {
	s := newTestServer(t, `
case "$1" in
  prompt)  echo 'work|eu-west-1|SSO|47m41s|590184095346' ;;
  profile) echo '[{"name":"work","type":"SSO","region":"eu-west-1","sso_session":"acme","is_active":true}]' ;;
esac
`)

	code, body := request(t, s, "GET", "/api/state", "")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if body["profile"] != "work" || body["ttl"] != "47m41s" {
		t.Errorf("active profile lost: %+v", body)
	}
	profiles, ok := body["profiles"].([]any)
	if !ok || len(profiles) != 1 {
		t.Fatalf("profile list lost: %+v", body["profiles"])
	}
}

func TestStateStillAnswersWhenTheProfileListFails(t *testing.T) {
	// A panel that renders nothing because one call failed is a panel that
	// cannot tell you why. The header should still appear, with the reason.
	s := newTestServer(t, `
case "$1" in
  prompt)  echo 'work|eu-west-1|SSO|47m41s|590184095346' ;;
  profile) echo "✗ Error: config file is unreadable" >&2; exit 1 ;;
esac
`)

	code, body := request(t, s, "GET", "/api/state", "")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if body["profile"] != "work" {
		t.Errorf("the active profile should survive a failed listing: %+v", body)
	}
	message, _ := body["error"].(string)
	if !strings.Contains(message, "config file is unreadable") {
		t.Errorf("the reason was not passed on: %q", message)
	}
}

func TestACommandFailureIsReportedWithItsMessage(t *testing.T) {
	// Failures come back with 200 and an error field on purpose: the status
	// code cannot carry what awsm printed, and that text is the only thing
	// worth showing.
	s := newTestServer(t, `echo "✗ Error: profile 'ghost' not found" >&2; exit 1`)

	code, body := request(t, s, "POST", "/api/profile/set", `{"name":"ghost"}`)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	message, _ := body["error"].(string)
	if !strings.Contains(message, "profile 'ghost' not found") {
		t.Errorf("got %q", message)
	}
}

func TestConsolePassesTheChosenBrowser(t *testing.T) {
	s := newTestServer(t, `echo "$@" > "$TEST_ARGS_FILE"`)
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TEST_ARGS_FILE", argsFile)

	code, body := request(t, s, "POST", "/api/console", `{"profile":"work","browser":"firefox"}`)
	if code != http.StatusOK || body["error"] != nil {
		t.Fatalf("status %d body %+v", code, body)
	}

	recorded, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("the command did not run: %v", err)
	}
	if !strings.Contains(string(recorded), "--firefox-container") {
		t.Errorf("the container flag never reached awsm: %q", recorded)
	}
	if !strings.Contains(string(recorded), "-p work") {
		t.Errorf("the profile never reached awsm: %q", recorded)
	}
}

func TestMalformedRequestIsRejected(t *testing.T) {
	s := newTestServer(t, `exit 0`)

	code, _ := request(t, s, "POST", "/api/profile/set", `{not json`)
	if code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", code)
	}
}

func TestThePageIsServedFromTheEmbeddedAssets(t *testing.T) {
	s := newTestServer(t, `exit 0`)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "panel") {
		t.Errorf("the page was not served: %q", rec.Body.String())
	}
}

func TestActionsThatChangeTheSessionNotifyTheStatusBar(t *testing.T) {
	// Without this the menu bar only caught up on its next tick, up to fifteen
	// seconds after the click that caused the change.
	cases := map[string]struct{ path, body string }{
		"set profile": {"/api/profile/set", `{"name":"work"}`},
		"clear":       {"/api/clear", `{}`},
		"sso login":   {"/api/sso/login", `{"session":"acme"}`},
		"set region":  {"/api/region", `{"profile":"work","region":"eu-west-1"}`},
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			s := newTestServer(t, `exit 0`)
			notified := 0
			s.OnChanged(func() { notified++ })

			code, body := request(t, s, "POST", call.path, call.body)
			if code != http.StatusOK || body["error"] != nil {
				t.Fatalf("status %d body %+v", code, body)
			}
			if notified != 1 {
				t.Errorf("status bar notified %d times, want 1", notified)
			}
		})
	}
}

func TestAFailedActionDoesNotClaimSomethingChanged(t *testing.T) {
	s := newTestServer(t, `echo "✗ Error: nope" >&2; exit 1`)
	notified := 0
	s.OnChanged(func() { notified++ })

	request(t, s, "POST", "/api/profile/set", `{"name":"ghost"}`)

	if notified != 0 {
		t.Errorf("a failed command notified the status bar %d times", notified)
	}
}

func TestReadsDoNotNotifyTheStatusBar(t *testing.T) {
	// Opening the panel changes nothing, and a refresh per open would be work
	// for no reason.
	s := newTestServer(t, `
case "$1" in
  prompt)  echo 'work|eu-west-1|SSO|47m|123' ;;
  profile) echo '[]' ;;
esac
`)
	notified := 0
	s.OnChanged(func() { notified++ })

	request(t, s, "GET", "/api/state", "")

	if notified != 0 {
		t.Errorf("reading the state notified the status bar %d times", notified)
	}
}

func TestCopyGoesThroughThePlatform(t *testing.T) {
	// The page cannot use navigator.clipboard: the webview serves this panel
	// from a custom scheme, which is neither a secure context nor a source of
	// the user gesture the browser API insists on.
	s := newTestServer(t, `exit 0`)
	var copied string
	s.OnCopy(func(text string) bool {
		copied = text
		return true
	})

	code, body := request(t, s, "POST", "/api/copy", `{"text":"590184095346"}`)
	if code != http.StatusOK || body["error"] != nil {
		t.Fatalf("status %d body %+v", code, body)
	}
	if copied != "590184095346" {
		t.Errorf("clipboard got %q", copied)
	}
}

func TestCopyReportsARefusalRatherThanClaimingSuccess(t *testing.T) {
	s := newTestServer(t, `exit 0`)
	s.OnCopy(func(string) bool { return false })

	_, body := request(t, s, "POST", "/api/copy", `{"text":"x"}`)
	if body["error"] == nil {
		t.Error("a refused copy should say so, not report success")
	}
}

func TestCopyingNothingIsRefused(t *testing.T) {
	// Profiles without an account id exist, and silently copying an empty
	// string would look like it worked.
	s := newTestServer(t, `exit 0`)
	s.OnCopy(func(string) bool { return true })

	_, body := request(t, s, "POST", "/api/copy", `{"text":""}`)
	if body["error"] == nil {
		t.Error("copying an empty string should be refused")
	}
}

func TestASlowSwitchSaysSoAndCanBeStopped(t *testing.T) {
	// Two things at once, because they are the same mechanism. A switch onto a
	// lapsed SSO session waits for a person: the status bar has to say so --
	// the panel may well be behind the browser awsm just opened -- and there
	// has to be a way out that is not waiting ten minutes.
	s := newTestServer(t, `
case "$1" in
  prompt)  echo 'work|eu-west-1|SSO|47m41s|590184095346' ;;
  profile) [ "$2" = "set" ] && sleep 30 || echo '[]' ;;
esac
`)

	busy := make(chan string, 4)
	s.OnBusy(func(doing string) { busy <- doing })

	done := make(chan error, 1)
	go func() { done <- s.Switch("work") }()

	// It announces itself before it finishes, which is the whole point. Bounded,
	// because the failure being guarded against is silence, and waiting on a
	// channel for silence is how a test hangs instead of failing.
	select {
	case doing := <-busy:
		if doing == "" {
			t.Fatal("the switch announced the end before it had begun")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the switch started without announcing itself")
	}

	if !s.Cancel() {
		t.Fatal("Cancel found nothing running")
	}

	select {
	case err := <-done:
		if !errors.Is(err, ErrCancelled) {
			t.Errorf("got %v, want ErrCancelled: being stopped on purpose is not a failure", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled switch never returned")
	}

	// And the status bar is told it is over, or it would say "Switching…"
	// forever.
	select {
	case doing := <-busy:
		if doing != "" {
			t.Errorf("got %q, want the empty string once the switch ended", doing)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the end of the switch was never announced")
	}
}

func TestCancellingReachesThePanelAsAnAnswerNotAnError(t *testing.T) {
	// The command reports "signal: killed", which is true and useless. A red
	// banner saying it would be the panel blaming the user for obeying them.
	s := newTestServer(t, `
case "$1" in
  prompt)  echo 'work|eu-west-1|SSO|47m41s|590184095346' ;;
  profile) [ "$2" = "set" ] && sleep 30 || echo '[]' ;;
esac
`)

	type answer struct {
		status  int
		payload map[string]any
	}
	got := make(chan answer, 1)
	go func() {
		status, payload := request(t, s, "POST", "/api/profile/set", `{"name":"work"}`)
		got <- answer{status, payload}
	}()

	// Wait for the operation to register before stopping it.
	deadline := time.Now().Add(10 * time.Second)
	for !s.Cancel() {
		if time.Now().After(deadline) {
			t.Fatal("the switch never registered itself as cancellable")
		}
	}

	select {
	case a := <-got:
		if _, isError := a.payload["error"]; isError {
			t.Errorf("a cancellation came back as an error: %v", a.payload)
		}
		if a.payload["cancelled"] != true {
			t.Errorf("got %v, want cancelled:true", a.payload)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the request never came back")
	}
}

func TestOpeningAConsoleDoesNotHoldThePanelOpen(t *testing.T) {
	// Console deliberately dismisses the panel: the point is to send the user
	// to a browser and get out of the way. Announcing it as activity would
	// suppress that hide and then leave the status bar talking about a console
	// that opened long ago.
	s := newTestServer(t, `echo ok`)

	var announced []string
	s.OnBusy(func(doing string) { announced = append(announced, doing) })

	if err := s.Console("work", "default"); err != nil {
		t.Fatalf("Console: %v", err)
	}
	if len(announced) != 0 {
		t.Errorf("Console announced %v; it must not hold the panel open", announced)
	}
}

func TestOpeningAConsoleDoesNotKillASignInInProgress(t *testing.T) {
	// Found by audit and reproduced: a switch onto a lapsed session waits for a
	// person, and opening a console for another account while it waited
	// cancelled it. The switch then reported itself "Cancelled" to somebody who
	// had cancelled nothing.
	//
	// Looking at an account writes no credentials, so it has no business
	// interrupting the one thing that does.
	s := newTestServer(t, `
case "$1" in
  prompt)  echo 'work|eu-west-1|SSO|47m41s|590184095346' ;;
  profile) [ "$2" = "set" ] && sleep 10 || echo '[]' ;;
  console) echo opened ;;
esac
`)

	switched := make(chan error, 1)
	go func() { switched <- s.Switch("work") }()

	// Let the switch register itself before the console goes anywhere near it.
	deadline := time.Now().Add(5 * time.Second)
	for !s.busyNow() {
		if time.Now().After(deadline) {
			t.Fatal("the switch never registered")
		}
	}

	if err := s.Console("other", "default"); err != nil {
		t.Fatalf("Console: %v", err)
	}

	select {
	case err := <-switched:
		t.Fatalf("the switch ended when the console opened: %v", err)
	case <-time.After(time.Second):
		// Still going, which is the point.
	}
	s.Cancel()
}

func TestASupersededOperationDoesNotAnnounceTheEnd(t *testing.T) {
	// Two switches in a row: the first is cancelled by the second, and its
	// release then said "idle" while the second was still running -- taking the
	// panel's hold off and wiping the status bar under it.
	s := newTestServer(t, `
case "$1" in
  prompt)  echo 'work|eu-west-1|SSO|47m41s|590184095346' ;;
  profile) [ "$2" = "set" ] && sleep 10 || echo '[]' ;;
esac
`)

	announced := make(chan string, 8)
	s.OnBusy(func(doing string) { announced <- doing })

	go func() { _ = s.Switch("first") }()
	if got := <-announced; got == "" {
		t.Fatal("the first switch announced the end before it began")
	}

	go func() { _ = s.Switch("second") }()

	// The first is now cancelled and will release. Whatever arrives next must
	// not be the empty string: the second switch is still running.
	deadline := time.After(5 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case doing := <-announced:
			if doing == "" {
				t.Fatal("a superseded switch reported the panel idle while its replacement was still running")
			}
		case <-deadline:
			return // nothing more was announced, which is also correct
		}
	}
	s.Cancel()
}

func TestAnIdleRenewalDaemonIsMentionedOnlyWhenItMatters(t *testing.T) {
	// The panel reads what the daemon decided, but nothing there says whether
	// the daemon runs at all -- so a machine where it was never enabled shows
	// no warnings and looks perfectly healthy. Saying so on every open would be
	// nagging about a setting; saying so when the credentials are nearly gone
	// is the one moment it is worth the space.
	script := func(ttl, state string) string {
		return `
case "$1" in
  prompt)  echo 'work|eu-west-1|SSO|` + ttl + `|590184095346' ;;
  profile) echo '[]' ;;
  daemon)  echo "  State:         ` + state + `" >&2 ;;
esac
`
	}

	cases := map[string]struct {
		ttl, state string
		want       bool
	}{
		"off and nearly expired":     {"3m", "disabled", true},
		"off but plenty of time":     {"47m41s", "disabled", false},
		"running and nearly expired": {"3m", "enabled", false},
		"unreadable and nearly gone": {"3m", "", false},
		"static credentials":         {"static", "disabled", false},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, payload := request(t, newTestServer(t, script(c.ttl, c.state)), "GET", "/api/state", "")
			if got := payload["renewalAtRisk"]; got != c.want {
				t.Errorf("renewalAtRisk is %v, want %v", got, c.want)
			}
		})
	}
}

func TestTheSettingsSayWhenTheDaemonStateCannotBeRead(t *testing.T) {
	// "unknown" has to survive all the way to the page. Rendering it as "off"
	// would send the user to fix something that may not be broken.
	s := newTestServer(t, `[ "$1" = "daemon" ] && exit 1; echo ok`)

	_, payload := request(t, s, "GET", "/api/settings", "")
	if got := payload["renewal"]; got != "unknown" {
		t.Errorf("renewal is %v, want unknown", got)
	}
}

func TestClearingOnlyTouchesTheProfileTheMenuWasOpenedOn(t *testing.T) {
	// `awsm clear` takes no argument: it lets go of whatever is set. A context
	// menu, though, is opened on a particular header, and the panel may have
	// redrawn since -- so clearing something the user is no longer looking at
	// is not what they clicked.
	cleared := filepath.Join(t.TempDir(), "cleared")
	s := newTestServer(t, `
case "$1" in
  prompt)  echo 'work|eu-west-1|SSO|47m41s|590184095346' ;;
  profile) echo '[]' ;;
  clear)   touch `+cleared+` ;;
esac
`)

	if err := s.ClearIfActive(t.Context(), "something-else"); err == nil {
		t.Error("cleared a profile that is not the active one")
	}
	if _, err := os.Stat(cleared); err == nil {
		t.Fatal("awsm clear ran for the wrong profile")
	}

	if err := s.ClearIfActive(t.Context(), "work"); err != nil {
		t.Fatalf("ClearIfActive on the active profile: %v", err)
	}
	if _, err := os.Stat(cleared); err != nil {
		t.Error("awsm clear never ran for the active profile")
	}
}

func TestASettingTheSystemRefusesIsNotWrittenDown(t *testing.T) {
	// The file used to be written first, so a shortcut another application owns
	// was recorded as the preference. It cannot be registered on the next
	// launch either, so that left the panel with no shortcut at all while the
	// settings view showed the refused combination as the one in force.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	s := newTestServer(t, `echo ok`)
	s.OnSettings(func(preferences settings.Settings) error {
		if preferences.Shortcut == "CmdOrCtrl+Space" {
			return errors.New("already taken by another application")
		}
		return nil
	})

	// One that works, so there is something to lose.
	if status, payload := request(t, s, "POST", "/api/settings",
		`{"shortcut":"CmdOrCtrl+Shift+A","browser":"default","theme":"system"}`); status != 200 {
		t.Fatalf("saving a working shortcut: %d %v", status, payload)
	}

	_, payload := request(t, s, "POST", "/api/settings",
		`{"shortcut":"CmdOrCtrl+Space","browser":"default","theme":"system"}`)
	if _, failed := payload["error"]; !failed {
		t.Fatal("the refused combination was accepted")
	}

	if got := settings.Load().Shortcut; got != "CmdOrCtrl+Shift+A" {
		t.Errorf("the file now says %q; the refused one was written down", got)
	}
}

// github stands in for the releases endpoint.
func github(t *testing.T, body string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestCheckingForAnUpdateReportsANewerRelease(t *testing.T) {
	s := newTestServer(t, "")
	s.updates.Endpoint = github(t, `{"tag_name":"v9.0.0","html_url":"https://example.com/releases/v9.0.0"}`)
	s.OnUpdates("0.4.1", func(string) error { return nil })

	code, body := request(t, s, "POST", "/api/update/check", "")
	if code != http.StatusOK {
		t.Fatalf("status %d: %+v", code, body)
	}
	if body["newer"] != true {
		t.Errorf("newer = %v, want true: %+v", body["newer"], body)
	}
	if body["latest"] != "9.0.0" || body["current"] != "0.4.1" {
		t.Errorf("versions lost: %+v", body)
	}
}

// TestABuildWithNoVersionIsNotCalledOutOfDate: `make app` compiles no version
// in, and comparing that against the releases would report an update on every
// press forever.
func TestABuildWithNoVersionIsNotCalledOutOfDate(t *testing.T) {
	s := newTestServer(t, "")
	s.updates.Endpoint = github(t, `{"tag_name":"v9.0.0","html_url":"https://example.com"}`)
	// Deliberately not calling OnUpdates: a build made by hand has no version.

	code, body := request(t, s, "POST", "/api/update/check", "")
	if code != http.StatusOK {
		t.Fatalf("status %d: %+v", code, body)
	}
	if body["newer"] != false {
		t.Errorf("newer = %v, want false for a development build", body["newer"])
	}
	if body["comparable"] != false {
		t.Errorf("comparable = %v, want false", body["comparable"])
	}
	if body["current"] != "" {
		t.Errorf("current = %q, want it left empty for the panel to name", body["current"])
	}
	if body["latest"] != "9.0.0" {
		t.Errorf("latest = %v: the newest release is still worth showing", body["latest"])
	}
}

// TestOpeningTheReleaseUsesTheURLFromTheCheck.
//
// The endpoint takes no URL. This server listens on localhost, but anything
// that can reach it could otherwise ask the application to open any address it
// liked, and opening pages on somebody's behalf is not a favour to hand out.
func TestOpeningTheReleaseUsesTheURLFromTheCheck(t *testing.T) {
	s := newTestServer(t, "")
	s.updates.Endpoint = github(t, `{"tag_name":"v9.0.0","html_url":"https://example.com/releases/v9.0.0"}`)

	var opened []string
	s.OnUpdates("0.4.1", func(url string) error {
		opened = append(opened, url)
		return nil
	})

	// Before any check there is nothing to open, and saying so beats opening
	// something arbitrary. Failures here travel in the body, not the status,
	// so that the message survives intact -- see fail().
	if _, body := request(t, s, "POST", "/api/update/open", ""); body["error"] == nil {
		t.Error("opened a release page before any check had been made")
	}
	if len(opened) != 0 {
		t.Fatalf("opened %v before any check", opened)
	}

	request(t, s, "POST", "/api/update/check", "")
	if code, body := request(t, s, "POST", "/api/update/open", ""); code != http.StatusOK {
		t.Fatalf("status %d: %+v", code, body)
	}

	if len(opened) != 1 || opened[0] != "https://example.com/releases/v9.0.0" {
		t.Errorf("opened %v, want the URL the check returned", opened)
	}
}

// TestTheOpenEndpointIgnoresAURLItIsHanded: passing one in the body must not
// steer it.
func TestTheOpenEndpointIgnoresAURLItIsHanded(t *testing.T) {
	s := newTestServer(t, "")
	s.updates.Endpoint = github(t, `{"tag_name":"v9.0.0","html_url":"https://example.com/real"}`)

	var opened []string
	s.OnUpdates("0.4.1", func(url string) error {
		opened = append(opened, url)
		return nil
	})

	request(t, s, "POST", "/api/update/check", "")
	request(t, s, "POST", "/api/update/open", `{"url":"https://evil.example/attack"}`)

	for _, url := range opened {
		if strings.Contains(url, "evil.example") {
			t.Errorf("opened %q: a URL from the request steered the browser", url)
		}
	}
}
