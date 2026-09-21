package awsm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStatusPathsRespectCustomAWSFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "custom-credentials"))
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "custom-config"))
	paths := statusPaths()
	if paths[0] != filepath.Join(dir, "custom-credentials") || paths[1] != filepath.Join(dir, "custom-config") {
		t.Fatal(paths)
	}
}

func TestFileChangesWakeTheUIWithoutWaitingForTheStatusTimer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	ticks := make(chan time.Time)
	changed := make(chan struct{}, 10)
	finished := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		watchStatusFiles(ctx, []string{path}, ticks, func() { changed <- struct{}{} })
		close(finished)
	}()
	tick := func() {
		t.Helper()
		select {
		case ticks <- time.Now():
		case <-time.After(time.Second):
			t.Fatal("watcher stopped")
		}
	}
	// Sending two ticks guarantees the first has been processed, including the
	// initial snapshot. The second may be scanning when the file changes.
	tick()
	tick()
	if err := os.WriteFile(path, []byte("fake"), 0600); err != nil {
		t.Fatal(err)
	}
	tick()
	tick()
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("creation did not refresh")
	}
	tick()
	tick()
	select {
	case <-changed:
		t.Fatal("unchanged file triggered another refresh")
	default:
	}
	replacement := path + ".new"
	if err := os.WriteFile(replacement, []byte("fake"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	tick()
	tick()
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("atomic replacement did not refresh")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	tick()
	tick()
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("clear did not refresh")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("watcher did not shut down")
	}
}
