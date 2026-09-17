// Package settings stores the handful of preferences this application has.
//
// It is a single JSON file under the user's configuration directory. There is
// no migration story and no schema version: unknown fields are ignored and
// missing ones take their zero value, which for a file this small is the whole
// of what a version number would have bought.
package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Settings is everything the user can change.
type Settings struct {
	// Shortcut is a system wide accelerator that opens the panel, in the form
	// "CmdOrCtrl+Shift+A". Empty means none is registered, which is the
	// default: claiming a key combination across the whole machine without
	// being asked is a good way to break somebody else's application.
	Shortcut string `json:"shortcut"`

	// Browser is where the AWS console opens: "default", "firefox" or "zen".
	Browser string `json:"browser"`

	// Theme is "system", "light" or "dark".
	//
	// Following the system is the default and the only one that can keep the
	// window's translucent material: macOS fixes a window's appearance when it
	// is created, so a panel told to be light on a dark desktop has to paint an
	// opaque background of its own instead.
	Theme string `json:"theme"`
}

// Default is what a machine with no settings file behaves like.
func Default() Settings {
	return Settings{Browser: "default", Theme: "system"}
}

// Path is the settings file's location, shown in the interface so that a
// preference that will not stick can be investigated rather than guessed at.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("could not find the configuration directory: %w", err)
	}
	return filepath.Join(dir, "awsm-desktop", "settings.json"), nil
}

// Load reads the settings, falling back to the defaults.
//
// A missing or unreadable file is not an error. Preferences are a convenience,
// and refusing to start over a malformed one would trade a small annoyance for
// a large one.
func Load() Settings {
	path, err := Path()
	if err != nil {
		return Default()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Default()
	}

	loaded := Default()
	if err := json.Unmarshal(data, &loaded); err != nil {
		return Default()
	}
	return loaded.sane()
}

// Save writes the settings, creating the directory if needed.
func Save(s Settings) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s.sane(), "", "  ")
	if err != nil {
		return err
	}
	// Written through a temporary file so an interrupted save leaves the
	// previous settings intact rather than a truncated file.
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// sane repairs values that would otherwise reach the rest of the program.
//
// The file is editable by hand, and a browser nobody implements or an
// accelerator with stray whitespace should degrade to something harmless
// rather than fail somewhere far away from here.
func (s Settings) sane() Settings {
	s.Shortcut = strings.TrimSpace(s.Shortcut)
	switch s.Browser {
	case "default", "firefox", "zen":
	default:
		s.Browser = "default"
	}
	switch s.Theme {
	case "system", "light", "dark":
	default:
		s.Theme = "system"
	}
	return s
}
