//go:build darwin || linux

package terminal

import "strings"

// shellCommand renders an argument list as one line for a POSIX shell.
func shellCommand(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	for _, part := range append([]string{name}, args...) {
		parts = append(parts, posixQuote(part))
	}
	return strings.Join(parts, " ")
}

// posixQuote wraps a string in single quotes, which a POSIX shell takes
// literally. A single quote in the input cannot be escaped inside such a run,
// so it is replaced by a sequence that closes the run, emits an escaped quote
// and opens a new one:
//
//	quote backslash quote quote
//
// Written out as a literal it is the familiar, unreadable four characters.
func posixQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// promptScript wraps a command so the window survives it.
//
// Without the wait the terminal closes the instant awsm exits, which on a
// failure takes the error message with it.
func promptScript(command string) string {
	return `#!/bin/sh
clear
echo "awsm needs your MFA code."
echo
` + command + `
status=$?
echo
if [ $status -eq 0 ]; then
  echo "Done. You can close this window."
else
  echo "The command failed with status $status."
fi
printf 'Press return to close. '
read -r _
`
}
