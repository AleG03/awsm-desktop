package terminal

import (
	"fmt"
	"os/exec"
)

// terminals are tried in order.
//
// x-terminal-emulator comes first because on Debian and its derivatives it is
// the user's registered choice rather than a guess. konsole is next: the only
// Linux this runs on is KDE Plasma, where it is the one already installed.
var terminals = [][]string{
	{"x-terminal-emulator", "-e"},
	{"konsole", "-e"},
	{"gnome-terminal", "--"},
	{"xfce4-terminal", "-e"},
	{"alacritty", "-e"},
	{"kitty", "-e"},
	{"xterm", "-e"},
}

// open runs the command in the first terminal emulator found.
//
// Unlike macOS there is no registry of the user's default, so this is a search.
// The command goes into a script rather than onto the emulator's own argument
// list because they disagree about how many arguments follow -e.
func open(name string, args []string) error {
	path, err := scriptFile(".sh", promptScript(shellCommand(name, args)))
	if err != nil {
		return err
	}

	for _, candidate := range terminals {
		program, flag := candidate[0], candidate[1]
		if _, err := exec.LookPath(program); err != nil {
			continue
		}
		if err := exec.Command(program, flag, path).Start(); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no terminal emulator found: tried %d of them", len(terminals))
}
