package terminal

import "os/exec"

// open runs the command in whichever terminal the user has made their default.
//
// The mechanism is deliberate: a .command file opened with `open` is handed to
// whatever LaunchServices has registered for that extension, which is the
// user's own choice. Scripting Terminal.app or iTerm directly would work too,
// and would impose an application the user may have replaced years ago.
func open(name string, args []string) error {
	path, err := scriptFile(".command", promptScript(shellCommand(name, args)))
	if err != nil {
		return err
	}
	// The file is left behind on purpose: removing it here would race with
	// LaunchServices, which has not opened it yet. It lands in the system
	// temporary directory, which the system itself cleans.
	return exec.Command("/usr/bin/open", path).Run()
}
