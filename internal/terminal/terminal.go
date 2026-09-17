// Package terminal hands a command to the user's terminal, already typed.
//
// It exists for one case: a profile that needs an MFA code. awsm reads the code
// from stdin, and a window in the status bar has no stdin to offer, so the only
// honest thing to do is put the user in front of the command with a terminal
// around it.
package terminal

import (
	"fmt"
	"os"
	"path/filepath"
)

// Open launches a command in the user's terminal.
//
// The command arrives as an argument list rather than a string so that a
// profile name containing a space, or anything a shell would treat specially,
// cannot become a second command. Each platform quotes it for its own shell.
func Open(name string, args ...string) error {
	if name == "" {
		return fmt.Errorf("no command to run")
	}
	return open(name, args)
}

// scriptFile writes a script to a temporary file and makes it executable.
//
// 0700 matters: the file names a profile and is about to be run, and the
// temporary directory is shared with every other user on the machine.
func scriptFile(extension, contents string) (string, error) {
	file, err := os.CreateTemp("", "awsm-*"+extension)
	if err != nil {
		return "", err
	}
	path := file.Name()

	if _, err := file.WriteString(contents); err != nil {
		file.Close()
		os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	if err := os.Chmod(path, 0700); err != nil {
		os.Remove(path)
		return "", err
	}
	return filepath.Clean(path), nil
}
