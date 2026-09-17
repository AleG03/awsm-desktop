// Package logs decides where this application writes what it has to say.
//
// A status bar application has no terminal. Started from the Finder or as a
// login item, anything written to standard error goes nowhere at all -- which
// is how a failure to start became indistinguishable from an application that
// simply never appeared.
package logs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// maxSize is the point at which the log is started over.
//
// Rotation would be machinery for no benefit: nothing here is worth keeping
// across a megabyte of it, and an application that lives in the menu bar for
// weeks has no business growing a file nobody is watching.
const maxSize = 1 << 20

// Path is where the log is written.
//
// macOS has a place for this and Console.app knows how to open it. Elsewhere
// the cache directory is the closest thing to "files the user need not keep".
func Path() (string, error) {
	base, err := baseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "awsm", "desktop.log"), nil
}

func baseDir() (string, error) {
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Logs"), nil
	}
	return os.UserCacheDir()
}

// Open returns the log file, ready to be appended to.
func Open() (*os.File, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return open(path, maxSize)
}

// open is the part worth testing: the directory is created, an oversized file
// is started over rather than grown, and anything smaller is appended to.
func open(path string, max int64) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}

	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if info, err := os.Stat(path); err == nil && info.Size() > max {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}

	file, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// Writer returns where the logger should write, and a function to close it.
//
// Standard error is kept alongside the file so that running the binary from a
// terminal still behaves like a command line program.
func Writer() (io.Writer, func(), string) {
	file, err := Open()
	if err != nil {
		// Losing the log is not a reason to refuse to start. Say so on stderr,
		// which is at least there when someone is watching.
		fmt.Fprintf(os.Stderr, "could not open the log file: %v\n", err)
		return os.Stderr, func() {}, ""
	}
	path, _ := Path()
	return io.MultiWriter(os.Stderr, file), func() { _ = file.Close() }, path
}
