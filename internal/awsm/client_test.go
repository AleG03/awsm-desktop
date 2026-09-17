package awsm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeAwsm writes an executable script standing in for the CLI and returns a
// client for it. The script is the contract under test: these tests are about
// how this package treats a command's streams and exit code, not about AWS.
func fakeAwsm(t *testing.T, script string) *Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "awsm")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	return NewWithBinary(path)
}

func TestProfilesReadsStdoutAndIgnoresStderr(t *testing.T) {
	// awsm prints spinners and ticks to stderr while writing JSON to stdout.
	// Mixing the two would make every call unparseable, so the separation is
	// worth a test rather than an assumption.
	c := fakeAwsm(t, `
echo "⏳ Loading profiles..." >&2
echo '[{"name":"work","type":"SSO","region":"eu-west-1","sso_session":"acme","is_active":true}]'
echo "✓ Loading profiles completed" >&2
`)

	profiles, err := c.Profiles(context.Background())
	if err != nil {
		t.Fatalf("Profiles: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("got %d profiles, want 1", len(profiles))
	}
	if profiles[0].Name != "work" || !profiles[0].IsActive {
		t.Errorf("profile parsed wrong: %+v", profiles[0])
	}
}

func TestFailureCarriesTheMessageFromStderr(t *testing.T) {
	// The exit status alone tells the user nothing. What awsm wrote is the
	// only thing worth putting in the panel.
	c := fakeAwsm(t, `
echo "✗ Error: profile 'nope' not found" >&2
exit 1
`)

	err := c.SetProfile(context.Background(), "nope")
	if err == nil {
		t.Fatal("expected an error")
	}
	var cmdErr *Error
	if !errors.As(err, &cmdErr) {
		t.Fatalf("got %T, want *awsm.Error", err)
	}
	if !strings.Contains(cmdErr.Stderr, "profile 'nope' not found") {
		t.Errorf("stderr lost the message: %q", cmdErr.Stderr)
	}
	// The decoration around the message should not survive into the panel.
	if strings.Contains(cmdErr.Stderr, "✗") {
		t.Errorf("stderr kept the decoration: %q", cmdErr.Stderr)
	}
}

func TestStatusParsesEveryFieldIncludingEmptyOnes(t *testing.T) {
	// A profile with no region yields an empty field in the middle. Splitting
	// on whitespace would silently shift every field after it by one.
	c := fakeAwsm(t, `echo 'work||SSO|47m41s|590184095346'`)

	status, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	want := Status{Profile: "work", Region: "", Type: "SSO", TTL: "47m41s", AccountID: "590184095346"}
	if status != want {
		t.Errorf("got %+v, want %+v", status, want)
	}
}

func TestStatusIsEmptyWhenNoProfileIsActive(t *testing.T) {
	c := fakeAwsm(t, `exit 0`)

	status, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Active() {
		t.Errorf("expected no active profile, got %+v", status)
	}
}

func TestConsolePassesTheContainerFlag(t *testing.T) {
	// The Firefox container is the reason several accounts can be open at
	// once, so the flag reaching the CLI is the whole feature.
	c := fakeAwsm(t, `echo "$@"`)

	if err := c.Console(context.Background(), "work", BrowserFirefox); err != nil {
		t.Fatalf("Console: %v", err)
	}

	out, err := c.run(context.Background(), "console", "-p", "work", "--firefox-container")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "--firefox-container") {
		t.Errorf("flag missing from %q", out)
	}
}

func TestACommandThatReadsStdinGetsEOFRatherThanWaiting(t *testing.T) {
	// awsm asks for an MFA code on stdin, and in a GUI process there is no
	// terminal behind it. The command must see end of input at once instead of
	// waiting: that is the difference between a panel that reports a problem
	// and one that spins forever.
	//
	// os/exec already gives a nil Stdin the null device, so this does not
	// prove the explicit assignment in run() fixed anything -- it is a guard
	// against someone later making the command inherit our own stdin, which
	// would reintroduce the wait.
	c := fakeAwsm(t, `
if read -r code; then echo "read:$code"; else echo "eof"; fi
`)

	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := c.run(context.Background(), "profile", "set", "mfa-profile")
		done <- result{out, err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("run: %v", r.err)
		}
		if got := strings.TrimSpace(string(r.out)); got != "eof" {
			t.Errorf("the command saw input where there should be none: %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call hung waiting on stdin")
	}
}

func TestNewRejectsAnAwsmBinThatIsNotExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awsm")
	if err := os.WriteFile(path, []byte("not a program"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWSM_BIN", path)

	if _, err := New(); err == nil {
		t.Error("expected an error for a non-executable AWSM_BIN")
	}
}

func TestNewPrefersAnExplicitBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awsm")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWSM_BIN", path)

	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Binary() != path {
		t.Errorf("got %q, want %q", c.Binary(), path)
	}
}

func TestASignInIsNotKilledUnderTheUser(t *testing.T) {
	// The bug this replaces: one ninety second bound for every command. Reading
	// the status has to give up long before the next tick, but switching to a
	// profile whose SSO session has lapsed opens a browser and waits for a
	// person to pick an account, type a code and approve it. Ninety seconds
	// killed that sign-in while it was being completed.
	//
	// The same sleeping command is run both ways, so the only difference under
	// test is which bound applies.
	c := fakeAwsm(t, "sleep 1\n")
	c.quick = 50 * time.Millisecond
	c.long = 10 * time.Second

	if err := c.Clear(context.Background()); err == nil {
		t.Error("clear waited: a command on the status bar's path must give up quickly")
	}
	if err := c.SetProfile(context.Background(), "work"); err != nil {
		t.Errorf("SetProfile gave up on a sign-in that was still going: %v", err)
	}
	if err := c.SSOLogin(context.Background(), "acme"); err != nil {
		t.Errorf("SSOLogin gave up on a sign-in that was still going: %v", err)
	}
	if err := c.Console(context.Background(), "work", BrowserDefault); err != nil {
		t.Errorf("Console gave up while resolving credentials: %v", err)
	}
}

func TestTheCallerCanStopALongCommand(t *testing.T) {
	// The long bound is ten minutes, which is far too long to sit through. It
	// is a backstop against a wedged child process, not the way out: cancelling
	// the context is, and it has to actually reach the process.
	c := fakeAwsm(t, "sleep 30\n")

	ctx, cancel := context.WithCancel(context.Background())
	go cancel()

	start := time.Now()
	err := c.SetProfile(ctx, "work")
	if err == nil {
		t.Fatal("expected the cancelled call to fail")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the call took %v to notice it had been cancelled", elapsed)
	}
}

// uniqueDuration is a sleep length no other process on the machine is likely to
// be using, so a test can ask whether its own grandchild is still running.
//
// The obvious approach -- copying /bin/sleep under a unique name -- does not
// work: macOS kills a copied platform binary on sight, which made the first
// version of this test pass for the wrong reason, the grandchild never having
// run at all.
func uniqueDuration() string {
	return fmt.Sprintf("20.%06d", os.Getpid()%1000000)
}

func TestCancellingKillsWhatTheCommandStarted(t *testing.T) {
	// The failure this guards against was found by cancelling a switch in the
	// panel and watching nothing happen. awsm does not do the waiting itself:
	// `awsm profile set` on a lapsed session runs `aws sso login`, a grandchild
	// of this process. Killing only the direct child, which is what Go does by
	// default, leaves that grandchild alive holding the pipe being read here.
	//
	// Measured both ways on the script below:
	//
	//	default kill        returned in 2.3s, grandchild still running
	//	whole process group returned in 300ms, nothing left behind
	//
	// The second branch of the case is load bearing: with a single branch the
	// shell replaces itself with the sleep, and then there is no grandchild to
	// leave behind and nothing to test.
	duration := uniqueDuration()
	c := fakeAwsm(t, `
case "$1" in
  profile) echo "opening browser..." >&2; sleep `+duration+` ;;
  *) echo ok ;;
esac
`)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)

	start := time.Now()
	if err := c.SetProfile(ctx, "work"); err == nil {
		t.Fatal("expected the cancelled call to fail")
	}
	// A grandchild still holding standard output is what keeps this waiting:
	// with the default kill it returns only once Go gives up on the pipe.
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("the call took %v to return: something was still holding the pipe", elapsed)
	}

	// And the part that matters more than the timing. A process that outlives
	// the cancellation is one the user cannot see, cannot stop, and did not
	// ask for.
	time.Sleep(500 * time.Millisecond)
	if out, _ := exec.Command("pgrep", "-f", "sleep "+duration).Output(); len(out) > 0 {
		_ = exec.Command("pkill", "-f", "sleep "+duration).Run()
		t.Error("the grandchild is still running: cancelling did not reach it")
	}
}

func TestTheDaemonStateIsReadFromWhereAwsmWritesIt(t *testing.T) {
	// The contract test caught this against the real CLI: `awsm daemon status`
	// prints its table to stderr, not stdout, so reading stdout the way every
	// other call here does returned nothing at all.
	c := fakeAwsm(t, `echo "  State:         enabled" >&2`)

	status := c.Daemon(context.Background())
	if !status.Known {
		t.Fatal("the state was not found: it is on stderr, not stdout")
	}
	if !status.Enabled {
		t.Errorf("got %q, want enabled", status.State)
	}
}

func TestAnUnreadableDaemonStateIsNotReportedAsOff(t *testing.T) {
	// Saying renewal is off when it cannot be determined would send the user to
	// fix something that is not broken, and would undermine the one warning
	// this panel exists to carry.
	for name, script := range map[string]string{
		"nothing at all":   `true`,
		"a changed format": `echo "  Renewal: running" >&2`,
		"a failure":        `echo "✗ not supported here" >&2; exit 1`,
	} {
		t.Run(name, func(t *testing.T) {
			status := fakeAwsm(t, script).Daemon(context.Background())
			if status.Known {
				t.Errorf("claimed to know the state (%+v) when it could not read one", status)
			}
			if status.Enabled {
				t.Error("claimed the daemon is enabled without evidence")
			}
		})
	}
}

func TestAwsmIsGivenAPathThatCanFindItsOwnTools(t *testing.T) {
	// The bug this fixes, as reported: renewing an SSO session failed saying
	// aws could not be found. Launched from the Finder or at login this
	// application gets launchd's PATH -- /usr/bin:/bin:/usr/sbin:/sbin -- and
	// passes it to awsm, which runs `aws sso login` and cannot find the AWS
	// CLI, because that installs to /usr/local/bin.
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")

	c := fakeAwsm(t, `echo "$PATH"`)
	out, err := c.run(t.Context(), "prompt")
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimSpace(string(out))

	if !strings.Contains(path, "/usr/local/bin") {
		t.Errorf("awsm would run with PATH=%s, which cannot find the AWS CLI", path)
	}
	// And what was already there has to survive: awsm runs /usr/bin things too.
	for _, want := range []string{"/usr/bin", "/bin"} {
		if !strings.Contains(path, want) {
			t.Errorf("PATH=%s lost %s", path, want)
		}
	}
}

func TestAPathThatAlreadyWorksIsLeftAlone(t *testing.T) {
	// Run from a terminal this inherits a real PATH, and somebody who arranged
	// their own tools first meant it. The repair fills a gap; it does not
	// overrule.
	t.Setenv("PATH", "/usr/local/bin:/usr/bin:/bin")

	c := fakeAwsm(t, `echo "$PATH"`)
	out, err := c.run(t.Context(), "prompt")
	if err != nil {
		t.Fatal(err)
	}

	path := strings.TrimSpace(string(out))
	if !strings.HasPrefix(path, "/usr/local/bin:/usr/bin:/bin") {
		t.Errorf("PATH=%s: the order somebody chose was not kept", path)
	}
	if strings.Count(path, "/usr/local/bin") != 1 {
		t.Errorf("PATH=%s has /usr/local/bin more than once", path)
	}
}

func TestOnlyDirectoriesThatExistAreAdded(t *testing.T) {
	// These paths mean nothing on Windows, and little on some Linuxes. Adding
	// a directory that is not there would be litter at best.
	missing := filepath.Join(t.TempDir(), "nowhere")
	if got := extend("/usr/bin", []string{missing}); got != "/usr/bin" {
		t.Errorf("got %q, want the path unchanged", got)
	}
}
