package awsm

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

func statusPaths() []string {
	home, _ := os.UserHomeDir()
	credentials := os.Getenv("AWS_SHARED_CREDENTIALS_FILE")
	if credentials == "" {
		credentials = filepath.Join(home, ".aws", "credentials")
	}
	config := os.Getenv("AWS_CONFIG_FILE")
	if config == "" {
		config = filepath.Join(home, ".aws", "config")
	}
	return []string{credentials, config, filepath.Join(home, ".awsm", "daemon-state.json"), filepath.Join(home, ".aws", "sso", "cache")}
}

// WatchStatusFiles requests a refresh when another process changes session
// files. It never reads credentials or invokes the CLI on an unchanged tick.
func WatchStatusFiles(ctx context.Context, interval time.Duration, changed func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	watchStatusFiles(ctx, statusPaths(), ticker.C, changed)
}

func watchStatusFiles(ctx context.Context, paths []string, ticks <-chan time.Time, changed func()) {
	previous := make([]os.FileInfo, len(paths))
	read := func() bool {
		modified := false
		for i, path := range paths {
			info, err := os.Stat(path)
			if err != nil && !os.IsNotExist(err) {
				continue
			}
			old := previous[i]
			if (old == nil) != (info == nil) || (old != nil && info != nil &&
				(!os.SameFile(old, info) || !old.ModTime().Equal(info.ModTime()) || old.Size() != info.Size())) {
				modified = true
			}
			previous[i] = info
		}
		return modified
	}
	read()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ticks:
			if !ok {
				return
			}
			if read() {
				changed()
			}
		}
	}
}
