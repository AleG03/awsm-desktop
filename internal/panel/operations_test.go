package panel

import (
	"awsm-desktop/internal/settings"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestConsoleCancellationDoesNotCancelTheConcurrentSwitch(t *testing.T) {
	s := newTestServer(t, "sleep 30\n")
	switchContext, finishSwitch := s.startLong("Switching…", "switch-id")
	defer finishSwitch()
	finished := make(chan error, 1)
	go func() { finished <- s.console("work", "default", "console-id") }()
	deadline := time.Now().Add(5 * time.Second)
	for !s.CancelOperation("console-id") {
		if time.Now().After(deadline) {
			t.Fatal("console never registered")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-finished:
		if !errors.Is(err, ErrCancelled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("console did not cancel")
	}
	if switchContext.Err() != nil {
		t.Fatal("console cancellation killed the switch")
	}
	if s.CancelOperation("console-id") {
		t.Fatal("finished console remained registered")
	}
	if s.CancelOperation("stale-id") {
		t.Fatal("unknown ID cancelled something else")
	}
	if !s.CancelOperation("switch-id") || switchContext.Err() == nil {
		t.Fatal("switch cannot be cancelled by its ID")
	}
}

func TestReplacementWaitsForThePreviousWriterToFinish(t *testing.T) {
	s := newTestServer(t, "exit 0\n")
	old, finishOld := s.startLong("old", "old-id")
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	finished := make(chan struct{})
	go func() { ctx, done := s.startLong("new", "new-id"); started <- ctx; <-release; done(); close(finished) }()
	select {
	case <-old.Done():
	case <-time.After(time.Second):
		t.Fatal("old operation not cancelled")
	}
	select {
	case <-started:
		t.Fatal("replacement started before old process exited")
	default:
	}
	finishOld()
	select {
	case ctx := <-started:
		if ctx.Err() != nil {
			t.Fatal("new operation was cancelled")
		}
	case <-time.After(time.Second):
		t.Fatal("replacement never started")
	}
	close(release)
	<-finished
	if s.busyNow() {
		t.Fatal("busy state stuck after replacement")
	}
}

func TestClearUsesTheAtomicCLIComparisonEvenAfterAnEarlyStatusCheck(t *testing.T) {
	args := filepath.Join(t.TempDir(), "arguments")
	s := newTestServer(t, fmt.Sprintf(`
case "$1" in
 prompt) echo 'work|eu-west-1|SSO|47m|123';;
 clear) printf '%%s\n' "$@" > %q; echo 'active profile changed' >&2; exit 1;;
esac
`, args))
	notified := false
	s.OnChanged(func() { notified = true })
	if err := s.ClearIfActive(t.Context(), "work"); err == nil {
		t.Fatal("stale clear reported success")
	}
	data, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "clear\n--if-profile\nwork\n" {
		t.Fatalf("unsafe clear arguments: %s", data)
	}
	if notified {
		t.Fatal("failed clear announced success")
	}
}

func TestClearRequiresTheProfileThePageDisplayed(t *testing.T) {
	s := newTestServer(t, "echo 'unexpected CLI call' >&2; exit 1\n")
	_, body := request(t, s, "POST", "/api/clear", `{}`)
	if body["error"] != "no profile selected" {
		t.Fatalf("unscoped clear not rejected: %v", body)
	}
}

func TestFailedSettingsWriteRestoresTheLiveShortcut(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	previous := settings.Settings{Shortcut: "CmdOrCtrl+Shift+A", Browser: "default", Theme: "light"}
	if err := settings.Save(previous); err != nil {
		t.Fatal(err)
	}
	path, err := settings.Path()
	if err != nil {
		t.Fatal(err)
	}
	// A directory at the temporary filename makes the write fail on all hosts.
	if err := os.Mkdir(path+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, "exit 0")
	live := previous
	s.OnSettings(func(next settings.Settings) error { live = next; return nil })
	_, body := request(t, s, "POST", "/api/settings", `{"shortcut":"CmdOrCtrl+Shift+B","browser":"default","theme":"dark"}`)
	if body["error"] == nil {
		t.Fatal("failed save was not reported")
	}
	if !reflect.DeepEqual(live, previous) || !reflect.DeepEqual(settings.Load(), previous) {
		t.Fatal("failed save left a different shortcut registered")
	}
}

func TestSettingsApplicationsCannotOverlap(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	s := newTestServer(t, "exit 0")
	entered := make(chan string, 2)
	release := make(chan struct{})
	s.OnSettings(func(next settings.Settings) error {
		entered <- next.Theme
		if next.Theme == "dark" {
			<-release
		}
		return nil
	})
	first := make(chan struct{})
	second := make(chan struct{})
	go func() { request(t, s, "POST", "/api/settings", `{"theme":"dark"}`); close(first) }()
	if theme := <-entered; theme != "dark" {
		t.Fatal(theme)
	}
	go func() { request(t, s, "POST", "/api/settings", `{"theme":"light"}`); close(second) }()
	select {
	case <-entered:
		t.Fatal("settings side effects overlapped")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	<-first
	<-second
	if settings.Load().Theme != "light" {
		t.Fatal("last settings change was lost")
	}
}

func TestPanelAndMenuBarUseTheResolvedSSOStatusImmediately(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".awsm"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".awsm", "daemon-state.json"), []byte(`{"blocked":"sso","blocked_profile":"work"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, `case "$1" in
 prompt) echo 'work|eu-west-1|SSO|59m|123|';;
 profile) echo '[{"name":"work","sso_session":"corp"}]';;
 esac`)
	status, err := s.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Blocked != "" || status.TTL != "59m" {
		t.Fatalf("menu bar still expired: %+v", status)
	}
	_, body := request(t, s, "GET", "/api/state", "")
	if body["blocked"] != "" || body["ttl"] != "59m" {
		t.Fatalf("panel still expired: %+v", body)
	}
}
