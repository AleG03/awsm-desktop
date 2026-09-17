// Package awsm drives the awsm command line tool.
//
// Everything this application knows about AWS comes through here. awsm already
// resolves credentials, refreshes SSO sessions and builds console sign-in URLs;
// reimplementing any of that would mean two things to keep correct instead of
// one. A call costs about five milliseconds, which is cheap enough to make the
// question uninteresting.
package awsm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The two bounds an invocation can have.
//
// One number could not serve both. Reading the status happens on a fifteen
// second ticker and has to give up long before the next tick; switching a
// profile whose SSO session has lapsed makes awsm open a browser and wait for a
// person to choose an account, type a code and approve it, which routinely runs
// past a minute and a half. The old single bound of ninety seconds killed that
// sign-in underneath the user while they were completing it.
//
// The long bound is not a deadline anyone should reach: it is there so a wedged
// child process cannot live forever. Cancelling is the real way out.
const (
	quickTimeout = 30 * time.Second
	longTimeout  = 10 * time.Minute
)

// Client runs a specific awsm binary.
type Client struct {
	bin string

	// Fields rather than constants so a test can shrink them to something it
	// can afford to wait for.
	quick time.Duration
	long  time.Duration
}

// ErrNotFound reports that no awsm binary could be located.
var ErrNotFound = errors.New("awsm executable not found")

// searchPaths are the usual install locations, tried before PATH.
//
// PATH alone is not enough: a GUI application launched at login inherits a
// minimal environment that does not include what a shell profile would add.
var searchPaths = []string{
	"/usr/local/bin/awsm",
	"/opt/homebrew/bin/awsm",
	"/usr/bin/awsm",
}

// toolPaths are where command line tools install, and where the PATH a GUI
// application is given does not look.
//
// Finding awsm was solved by the list above, which was enough right up until
// awsm needed to run something itself: `awsm sso login` shells out to the AWS
// CLI, which installs to /usr/local/bin. Launched from the Finder or at login
// this application gets launchd's PATH -- /usr/bin:/bin:/usr/sbin:/sbin and
// nothing a shell profile would have added -- and hands it to awsm, which then
// cannot find aws and fails the renewal saying so.
//
// Only directories that exist are added, which is what keeps this harmless on
// the platforms where these paths mean nothing.
var toolPaths = []string{
	"/usr/local/bin",
	"/opt/homebrew/bin",
	"/opt/local/bin",
}

// New locates the awsm binary and returns a client for it.
//
// AWSM_BIN overrides the search entirely, which is what makes it possible to
// point the panel at a development build.
func New() (*Client, error) {
	if override := os.Getenv("AWSM_BIN"); override != "" {
		if err := executable(override); err != nil {
			return nil, fmt.Errorf("AWSM_BIN=%s: %w", override, err)
		}
		return newClient(override), nil
	}

	for _, candidate := range searchPaths {
		if executable(candidate) == nil {
			return newClient(candidate), nil
		}
	}

	found, err := exec.LookPath("awsm")
	if err != nil {
		return nil, ErrNotFound
	}
	abs, err := filepath.Abs(found)
	if err != nil {
		abs = found
	}
	return newClient(abs), nil
}

func newClient(bin string) *Client {
	return &Client{bin: bin, quick: quickTimeout, long: longTimeout}
}

// SearchPaths is where New looks before falling back to PATH, so that a
// failure can name them rather than leave the user guessing.
func SearchPaths() []string { return append([]string(nil), searchPaths...) }

// NewWithBinary returns a client for an explicit binary, for tests.
func NewWithBinary(path string) *Client { return newClient(path) }

// Binary is the resolved path, shown in the panel so that "it did not do what
// I expected" can be answered by looking at which build ran.
func (c *Client) Binary() string { return c.bin }

func executable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() || info.Mode()&0111 == 0 {
		return fmt.Errorf("%s is not executable", path)
	}
	return nil
}

// environment is what awsm runs in, with PATH repaired.
//
// See toolPaths for why. awsm's own directory goes on as well: whatever
// installed it is a reasonable guess at where its companions are.
func (c *Client) environment() []string {
	wanted := append([]string{filepath.Dir(c.bin)}, toolPaths...)

	environment := os.Environ()
	for i, entry := range environment {
		if after, found := strings.CutPrefix(entry, "PATH="); found {
			environment[i] = "PATH=" + extend(after, wanted)
			return environment
		}
	}
	return append(environment, "PATH="+extend("", wanted))
}

// extend appends the directories that are not on the path already and do exist.
//
// Appended rather than prepended: a PATH that does name a tool already is one
// somebody arranged on purpose, and this is here to fill a gap rather than to
// overrule them.
func extend(path string, wanted []string) string {
	var parts []string
	present := map[string]bool{}

	if path != "" {
		parts = strings.Split(path, ":")
		for _, part := range parts {
			present[part] = true
		}
	}

	for _, dir := range wanted {
		if dir == "" || dir == "." || present[dir] {
			continue
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		present[dir] = true
		parts = append(parts, dir)
	}
	return strings.Join(parts, ":")
}

// Error is a failed invocation, carrying what the command wrote to stderr.
//
// awsm puts machine-readable output on stdout and everything meant for a person
// on stderr, so stderr is the message worth showing.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("awsm %s: %s", strings.Join(e.Args, " "), e.Stderr)
	}
	return fmt.Sprintf("awsm %s: %v", strings.Join(e.Args, " "), e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// run invokes awsm and returns stdout.
//
// This is the bound for everything that only reads local files, which is nearly
// everything: it must fail fast, because the status bar asks on a timer.
func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	return c.runWithin(ctx, c.quick, args...)
}

// runLong is for the two or three commands that wait on a person.
func (c *Client) runLong(ctx context.Context, args ...string) ([]byte, error) {
	return c.runWithin(ctx, c.long, args...)
}

// runReadingStderr returns what the command wrote to a person.
//
// awsm's convention is machine-readable output on stdout and decoration on
// stderr, and every other call here relies on it. `daemon status` is the
// exception that proves it: it has no machine-readable form at all, so its
// whole table goes to stderr and stdout comes back empty. Reading it means
// saying so out loud rather than silently combining the streams everywhere.
func (c *Client) runReadingStderr(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.quick)
	defer cancel()

	cmd, cleanup, err := c.command(ctx, args...)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	var both strings.Builder
	cmd.Stdout = &both
	cmd.Stderr = &both

	if err := cmd.Run(); err != nil {
		return nil, &Error{Args: args, Stderr: cleanStderr(both.String()), Err: err}
	}
	return []byte(both.String()), nil
}

// runWithin does the work.
func (c *Client) runWithin(ctx context.Context, limit time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	cmd, cleanup, err := c.command(ctx, args...)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	var stderr strings.Builder
	cmd.Stderr = &stderr

	stdout, err := cmd.Output()
	if err != nil {
		return nil, &Error{Args: args, Stderr: cleanStderr(stderr.String()), Err: err}
	}
	return stdout, nil
}

// command builds an invocation with the parts every call needs.
//
// stdin is /dev/null on purpose: any prompt the CLI decides to raise then ends
// immediately in EOF rather than waiting on a terminal that does not exist.
func (c *Client) command(ctx context.Context, args ...string) (*exec.Cmd, func(), error) {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Env = c.environment()
	configure(cmd)

	// Even with the whole group killed, a process can take a moment to let go
	// of the pipes. This bounds the wait for that rather than the command
	// itself, which the context already bounds.
	cmd.WaitDelay = 2 * time.Second

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return nil, nil, err
	}
	cmd.Stdin = devNull

	return cmd, func() { _ = devNull.Close() }, nil
}

// cleanStderr trims the progress decoration awsm writes for a terminal, so the
// panel shows the message rather than the spinner frames around it.
func cleanStderr(s string) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "⏳✓✗•⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏ ")
		if line == "" || strings.HasSuffix(line, "completed") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "; ")
}

// Profile is one entry of `awsm profile list -j`.
type Profile struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Region       string `json:"region"`
	AccountID    string `json:"account_id"`
	SSOAccountID string `json:"sso_account_id"`
	SSORoleName  string `json:"sso_role_name"`
	SSOSession   string `json:"sso_session"`
	MFASerial    string `json:"mfa_serial"`
	IsActive     bool   `json:"is_active"`
}

// NeedsMFA reports whether switching to this profile will ask for a code.
func (p Profile) NeedsMFA() bool { return p.MFASerial != "" }

// Profiles lists every configured profile.
func (c *Client) Profiles(ctx context.Context) ([]Profile, error) {
	out, err := c.run(ctx, "profile", "list", "-j")
	if err != nil {
		return nil, err
	}
	var profiles []Profile
	if err := json.Unmarshal(out, &profiles); err != nil {
		return nil, fmt.Errorf("could not read the profile list: %w", err)
	}
	return profiles, nil
}

// Status is the offline snapshot shown in the status bar.
type Status struct {
	Profile   string `json:"profile"`
	Region    string `json:"region"`
	Type      string `json:"type"`
	TTL       string `json:"ttl"`
	AccountID string `json:"account_id"`
}

// Active reports whether a profile is currently set.
func (s Status) Active() bool { return s.Profile != "" }

// statusFormat asks for the fields separated by a character that cannot occur
// in any of them. `awsm prompt` collapses runs of whitespace, so a space
// separated format would not survive an empty field.
const statusFormat = "{profile}|{region}|{type}|{ttl}|{account}"

// Status reads the active profile without touching the network.
//
// This is the call made on a timer, so it has to stay in the milliseconds:
// `awsm prompt` reads the credentials file and the local cache and nothing
// else.
func (c *Client) Status(ctx context.Context) (Status, error) {
	out, err := c.run(ctx, "prompt", "--no-color", "--empty-on-none", "--format", statusFormat)
	if err != nil {
		return Status{}, err
	}

	line := strings.TrimSpace(string(out))
	if line == "" {
		return Status{}, nil
	}

	parts := strings.Split(line, "|")
	for len(parts) < 5 {
		parts = append(parts, "")
	}
	return Status{
		Profile:   parts[0],
		Region:    parts[1],
		Type:      parts[2],
		TTL:       parts[3],
		AccountID: parts[4],
	}, nil
}

// SetProfile writes the profile's credentials to the default profile.
//
// Long, because a lapsed SSO session turns this into a browser sign-in that
// waits for whoever is at the keyboard.
func (c *Client) SetProfile(ctx context.Context, name string) error {
	_, err := c.runLong(ctx, "profile", "set", name)
	return err
}

// Clear removes the active credentials.
func (c *Client) Clear(ctx context.Context) error {
	_, err := c.run(ctx, "clear")
	return err
}

// Browser selects where the console opens.
type Browser string

const (
	// BrowserDefault uses whatever the system opens https with.
	BrowserDefault Browser = "default"
	// BrowserFirefox opens a Firefox container named after the profile, which
	// is what keeps several accounts signed in at once.
	BrowserFirefox Browser = "firefox"
	// BrowserZen is the same idea in Zen Browser.
	BrowserZen Browser = "zen"
)

// Console opens the AWS console for a profile.
func (c *Client) Console(ctx context.Context, profile string, browser Browser) error {
	args := []string{"console", "-p", profile}
	switch browser {
	case BrowserFirefox:
		args = append(args, "--firefox-container")
	case BrowserZen:
		args = append(args, "--zen-container")
	}
	// Long for the same reason as SetProfile: resolving another profile's
	// credentials can itself need a sign-in.
	_, err := c.runLong(ctx, args...)
	return err
}

// Identity is the answer to `awsm whoami --json`.
//
// Unlike everything else here this one calls STS, so it takes about half a
// second and must never be on the path that opens the panel.
type Identity struct {
	Profile   string `json:"profile"`
	Type      string `json:"type"`
	Region    string `json:"region"`
	Account   string `json:"account"`
	Arn       string `json:"arn"`
	UserID    string `json:"user_id"`
	ExpiresAt string `json:"expires_at"`
	Static    bool   `json:"static"`
	CallError string `json:"caller_id_error"`
}

// Whoami verifies the active credentials against STS.
func (c *Client) Whoami(ctx context.Context) (Identity, error) {
	out, err := c.run(ctx, "whoami", "--json")
	if err != nil {
		return Identity{}, err
	}
	var id Identity
	if err := json.Unmarshal(out, &id); err != nil {
		return Identity{}, fmt.Errorf("could not read the identity: %w", err)
	}
	return id, nil
}

// SSOLogin starts the browser login for an SSO session.
//
// This one needs no terminal: awsm opens the browser and waits for the flow to
// complete, so the panel can offer it as an ordinary button.
func (c *Client) SSOLogin(ctx context.Context, session string) error {
	_, err := c.runLong(ctx, "sso", "login", session)
	return err
}

// SetRegion changes a profile's default region.
//
// Note the "profile" in front: the subcommand is declared as
// "change-default-region" but registered under the profile command, and
// reading the declaration without checking where it is attached produces a
// command awsm does not have.
func (c *Client) SetRegion(ctx context.Context, profile, region string) error {
	_, err := c.run(ctx, "profile", "change-default-region", profile, region)
	return err
}

// regionLine matches one entry of `awsm region list`, which is a bulleted list
// meant for a person rather than a program.
var regionLine = regexp.MustCompile(`^\s*[•*-]\s*([a-z]{2}-[a-z]+-\d+)\s*$`)

// Regions lists the regions awsm knows about.
//
// There is no JSON form of this command, so the human output is parsed. That is
// a weaker contract than everywhere else here, which is why the caller is
// expected to have a fallback rather than to treat an empty result as a
// failure.
func (c *Client) Regions(ctx context.Context) ([]string, error) {
	out, err := c.run(ctx, "region", "list")
	if err != nil {
		return nil, err
	}
	var regions []string
	for _, line := range strings.Split(string(out), "\n") {
		if match := regionLine.FindStringSubmatch(line); match != nil {
			regions = append(regions, match[1])
		}
	}
	return regions, nil
}

// DaemonStatus is what awsm's credential refresh daemon is doing.
//
// Known is the field that matters. There is no machine-readable form of this
// command, so the answer is parsed out of text meant for a person -- exactly
// the weak contract that once produced a region picker calling a command that
// did not exist. When the parse fails the honest answer is "I cannot tell",
// never "it is off": telling someone their credentials are unprotected when
// they are is its own kind of wrong.
type DaemonStatus struct {
	Known   bool
	Enabled bool
	State   string
}

// daemonState matches the line awsm prints, e.g. "  State:         enabled".
var daemonState = regexp.MustCompile(`(?m)^\s*State:\s*(\S+)`)

// Daemon reports whether credentials are being renewed in the background.
//
// The panel reads the daemon's state file to show what it decided, but nothing
// there says whether the daemon runs at all -- so a machine where it was never
// enabled shows no warnings and looks perfectly healthy.
func (c *Client) Daemon(ctx context.Context) DaemonStatus {
	out, err := c.runReadingStderr(ctx, "daemon", "status")
	if err != nil {
		return DaemonStatus{}
	}
	match := daemonState.FindSubmatch(out)
	if match == nil {
		return DaemonStatus{}
	}
	state := strings.ToLower(string(match[1]))
	return DaemonStatus{Known: true, Enabled: state == "enabled", State: state}
}

// EnableDaemon starts the background renewal.
//
// It writes a launch agent under the user's own Library and needs no
// privileges, which is what makes it something a panel can offer rather than
// an instruction to go and type.
func (c *Client) EnableDaemon(ctx context.Context) error {
	_, err := c.run(ctx, "daemon", "enable")
	return err
}

// Version reports the CLI version, used to tell the user plainly when the
// panel and the command line have drifted apart.
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "--version")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
