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
	"slices"
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

	// Bindings are the keys and clicks the panel's actions answer to, by
	// action: {"firefox": ["CmdOrCtrl+Click", "CmdOrCtrl+F"]}. Only the
	// actions the user changed are here; the panel knows the defaults for the
	// rest, so a default that improves reaches everyone who never touched it.
	// An empty list is a choice too: that action has no binding at all.
	//
	// The actions and the meaning of each binding belong to the panel. Nothing
	// on this side reads them, so they are stored as given.
	Bindings map[string][]string `json:"bindings,omitempty"`
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
	s.Bindings = saneBindings(s.Bindings)
	return s
}

// saneBindings trims each binding and drops blanks and repeats.
//
// A fresh map, because the caller's is shared with whoever handed it over. An
// action left with nothing keeps its empty list, which means "unbound" rather
// than "use the default".
func saneBindings(bindings map[string][]string) map[string][]string {
	if len(bindings) == 0 {
		return nil
	}
	clean := make(map[string][]string, len(bindings))
	for action, list := range bindings {
		action = strings.TrimSpace(action)
		if action == "" {
			continue
		}
		kept := []string{}
		for _, binding := range list {
			binding = strings.TrimSpace(binding)
			if binding != "" && !slices.Contains(kept, binding) {
				kept = append(kept, binding)
			}
		}
		clean[action] = kept
	}
	if len(clean) == 0 {
		return nil
	}
	return clean
}
