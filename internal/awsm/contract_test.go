package awsm

import (
	"os/exec"
	"strings"
	"testing"
)

// The tests in this file run against the awsm that is actually installed,
// rather than against a script standing in for it.
//
// They exist because of a real failure: the region picker called
// "awsm change-default-region", which is how the subcommand declares itself --
// but it is registered under the profile command, so the real path is
// "awsm profile change-default-region". Every test with a fake binary passed,
// because a fake binary accepts whatever it is given. Only the CLI itself can
// say whether a command exists.

// realAwsm returns a client for the installed binary, or skips.
func realAwsm(t *testing.T) *Client {
	t.Helper()
	client, err := New()
	if err != nil {
		t.Skipf("awsm is not installed: %v", err)
	}
	return client
}

// help runs a command's --help, which exercises the path without doing
// anything, and returns its output.
func help(t *testing.T, client *Client, args ...string) (string, bool) {
	t.Helper()
	out, err := exec.Command(client.Binary(), append(args, "--help")...).CombinedOutput()
	return string(out), err == nil
}

func TestEveryCommandThePanelUsesExists(t *testing.T) {
	client := realAwsm(t)

	// Exactly the command paths this package invokes.
	commands := [][]string{
		{"profile", "list"},
		{"profile", "set"},
		{"profile", "change-default-region"},
		{"prompt"},
		{"clear"},
		{"console"},
		{"whoami"},
		{"sso", "login"},
		{"region", "list"},
	}

	for _, command := range commands {
		name := strings.Join(command, " ")
		t.Run(name, func(t *testing.T) {
			if _, ok := help(t, client, command...); !ok {
				t.Errorf("awsm has no %q command", name)
			}
		})
	}
}

func TestEveryFlagThePanelUsesExists(t *testing.T) {
	client := realAwsm(t)

	// A command can exist and still have lost the flag being passed to it.
	cases := []struct {
		command []string
		flags   []string
	}{
		{[]string{"profile", "list"}, []string{"-j"}},
		{[]string{"prompt"}, []string{"--no-color", "--empty-on-none", "--format"}},
		{[]string{"console"}, []string{"--firefox-container", "--zen-container", "--profile"}},
		{[]string{"whoami"}, []string{"--json"}},
	}

	for _, c := range cases {
		name := strings.Join(c.command, " ")
		t.Run(name, func(t *testing.T) {
			out, ok := help(t, client, c.command...)
			if !ok {
				t.Fatalf("awsm has no %q command", name)
			}
			for _, flag := range c.flags {
				if !strings.Contains(out, flag) {
					t.Errorf("%q does not mention %s", name, flag)
				}
			}
		})
	}
}

func TestTheStatusFormatStillProducesFiveFields(t *testing.T) {
	// The status bar reads five values out of one line. If awsm ever drops a
	// placeholder, the fields after it shift and the panel shows a region
	// where it means to show a countdown.
	client := realAwsm(t)

	status, err := client.Status(t.Context())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Profile == "" {
		t.Skip("no active profile to read")
	}

	// A region is the one field with a shape worth checking: if the split
	// slipped, this is where it shows.
	if status.Region != "" && !strings.Contains(status.Region, "-") {
		t.Errorf("region %q does not look like a region: the fields may have shifted", status.Region)
	}
}

func TestTheDaemonStatusStillSaysWhatItsStateIs(t *testing.T) {
	// The panel offers to enable credential renewal when it is off. That rests
	// on reading a word out of output meant for a person, which is the same
	// weak contract that once had the region picker calling a command awsm does
	// not have.
	client := realAwsm(t)

	out, ok := help(t, client, "daemon", "status")
	if !ok {
		t.Fatal(`awsm has no "daemon status" command`)
	}
	if _, ok := help(t, client, "daemon", "enable"); !ok {
		t.Error(`awsm has no "daemon enable" command: the panel offers a button for it`)
	}
	_ = out

	// And the line itself is still there, in the real output rather than in
	// --help. A command that exists but no longer prints "State:" would leave
	// the panel quietly reporting "unknown" forever.
	status := client.Daemon(t.Context())
	if !status.Known {
		t.Error("could not read a state out of `awsm daemon status`: the output format changed")
	}
}
