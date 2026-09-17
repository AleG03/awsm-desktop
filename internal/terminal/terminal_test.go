//go:build darwin || linux

package terminal

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestShellCommandQuotesEveryArgument(t *testing.T) {
	got := shellCommand("/usr/local/bin/awsm", []string{"profile", "set", "work prod"})
	want := `'/usr/local/bin/awsm' 'profile' 'set' 'work prod'`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAQuoteInAProfileNameCannotStartASecondCommand(t *testing.T) {
	// Profile names come from the user's own config file, but this line is
	// handed to a shell, and "it is my own file" is exactly the assumption
	// that makes injection bugs live for years. The shell must see one
	// argument, whatever is in it.
	hostile := `x'; touch /tmp/awsm-injection-canary; echo '`

	line := shellCommand("/bin/echo", []string{hostile})

	canary := "/tmp/awsm-injection-canary"
	os.Remove(canary)
	t.Cleanup(func() { os.Remove(canary) })

	out, err := exec.Command("/bin/sh", "-c", line).Output()
	if err != nil {
		t.Fatalf("running the quoted line failed: %v", err)
	}
	if _, err := os.Stat(canary); err == nil {
		t.Fatal("the quoting let a second command run")
	}
	if got := strings.TrimSpace(string(out)); got != hostile {
		t.Errorf("the argument did not survive intact:\n got %q\nwant %q", got, hostile)
	}
}

func TestPromptScriptWaitsBeforeClosing(t *testing.T) {
	// Without the wait the window closes the instant awsm exits, taking any
	// error message with it -- which is the whole reason a terminal is being
	// opened rather than a command being run silently.
	script := promptScript("true")
	if !strings.Contains(script, "read -r _") {
		t.Error("the script does not wait before closing")
	}
	if !strings.Contains(script, "status=$?") {
		t.Error("the script does not capture the exit status")
	}
}
