package logs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnOversizedLogIsStartedOverRatherThanGrown(t *testing.T) {
	// The whole point of the size limit: an application that sits in the menu
	// bar for weeks must not quietly fill a disk.
	path := filepath.Join(t.TempDir(), "awsm", "desktop.log")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 100)), 0600); err != nil {
		t.Fatal(err)
	}

	file, err := open(path, 50) // the file is twice the limit
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Errorf("the log still holds %d bytes; it should have been started over", info.Size())
	}
}

func TestASmallLogIsAppendedTo(t *testing.T) {
	// The other half. Truncating on every launch would throw away the lines
	// explaining why the previous launch failed, which is the only reason this
	// file exists.
	path := filepath.Join(t.TempDir(), "desktop.log")
	if err := os.WriteFile(path, []byte("earlier\n"), 0600); err != nil {
		t.Fatal(err)
	}

	file, err := open(path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("later\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != "earlier\nlater\n" {
		t.Errorf("got %q, want the earlier line kept and the later one appended", got)
	}
}

func TestTheDirectoryIsCreated(t *testing.T) {
	// ~/Library/Logs exists on any Mac, but the awsm folder inside it does not
	// until something makes it.
	path := filepath.Join(t.TempDir(), "not", "there", "yet", "desktop.log")

	file, err := open(path, 1<<20)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	file.Close()

	if _, err := os.Stat(path); err != nil {
		t.Errorf("the log was not created: %v", err)
	}
}

func TestTheLogHasAPlaceToGo(t *testing.T) {
	path, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("%q is not an absolute path", path)
	}
	if !strings.HasSuffix(path, filepath.Join("awsm", "desktop.log")) {
		t.Errorf("%q does not end in awsm/desktop.log", path)
	}
}
