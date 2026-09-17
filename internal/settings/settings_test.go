package settings

import (
	"os"
	"path/filepath"
	"testing"
)

// isolate points the settings at a temporary configuration directory, so a
// test never reads or overwrites the real preferences.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir) // honoured by os.UserConfigDir on Linux
	t.Setenv("HOME", dir)            // and on macOS, via Library/Application Support
	return dir
}

func TestRoundTrip(t *testing.T) {
	isolate(t)

	want := Settings{Shortcut: "CmdOrCtrl+Shift+A", Browser: "firefox", Theme: "dark"}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := Load(); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestMissingFileGivesTheDefaults(t *testing.T) {
	isolate(t)

	if got := Load(); got != Default() {
		t.Errorf("got %+v, want the defaults %+v", got, Default())
	}
	if Default().Shortcut != "" {
		t.Error("no shortcut should be registered until the user asks for one")
	}
}

func TestACorruptFileDoesNotStopTheApplication(t *testing.T) {
	isolate(t)

	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{ not json"), 0600); err != nil {
		t.Fatal(err)
	}

	if got := Load(); got != Default() {
		t.Errorf("got %+v, want the defaults", got)
	}
}

func TestAnUnknownThemeFallsBackToFollowingTheSystem(t *testing.T) {
	// An empty or nonsense theme must not leave the panel unstyled: following
	// the system is the one choice that is always right.
	isolate(t)

	path, _ := Path()
	os.MkdirAll(filepath.Dir(path), 0700)
	if err := os.WriteFile(path, []byte(`{"theme":"sepia"}`), 0600); err != nil {
		t.Fatal(err)
	}

	if got := Load().Theme; got != "system" {
		t.Errorf("got %q, want %q", got, "system")
	}
}

func TestAnUnknownBrowserFallsBackToTheDefaultOne(t *testing.T) {
	// The file is editable by hand, and a browser nobody implements should
	// degrade here rather than fail somewhere far away.
	isolate(t)

	path, _ := Path()
	os.MkdirAll(filepath.Dir(path), 0700)
	if err := os.WriteFile(path, []byte(`{"browser":"netscape"}`), 0600); err != nil {
		t.Fatal(err)
	}

	if got := Load().Browser; got != "default" {
		t.Errorf("got %q, want %q", got, "default")
	}
}

func TestSettingsAreNotWorldReadable(t *testing.T) {
	// Nothing secret lives here today, but a file in the user's configuration
	// directory should not be the exception that teaches otherwise.
	isolate(t)

	if err := Save(Settings{Shortcut: "CmdOrCtrl+Shift+A"}); err != nil {
		t.Fatal(err)
	}
	path, _ := Path()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode&0077 != 0 {
		t.Errorf("settings are mode %o, want no group or other access", mode)
	}
}

func TestSavingLeavesNoTemporaryFileBehind(t *testing.T) {
	isolate(t)

	if err := Save(Settings{Browser: "zen"}); err != nil {
		t.Fatal(err)
	}
	path, _ := Path()
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("the temporary file used for the atomic write was left behind")
	}
}
