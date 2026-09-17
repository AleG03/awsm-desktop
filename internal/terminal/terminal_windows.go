package terminal

import (
	"fmt"
	"os/exec"
	"strings"
)

// open runs the command in a console window.
//
// `start` is a builtin of cmd.exe rather than a program, so it has to go
// through cmd itself. The inner `cmd /k` keeps the window open after awsm
// exits, which is what makes an error message readable.
//
// The empty string after `start` is its title argument: without it a quoted
// command would be taken as the window title and nothing would run.
func open(name string, args []string) error {
	command := cmdCommand(name, args)
	if err := exec.Command("cmd", "/c", "start", "", "cmd", "/k", command).Start(); err != nil {
		return fmt.Errorf("could not open a console window: %w", err)
	}
	return nil
}

// cmdCommand renders an argument list for cmd.exe.
//
// cmd has no equivalent of POSIX single quotes: double quotes are the only
// grouping, and a literal double quote inside one is escaped by doubling it.
// Arguments without anything special are left bare, which keeps the command
// legible in the window that opens.
func cmdCommand(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	for _, part := range append([]string{name}, args...) {
		parts = append(parts, cmdQuote(part))
	}
	return strings.Join(parts, " ")
}

func cmdQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, ` "&|<>^()%!`) {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
