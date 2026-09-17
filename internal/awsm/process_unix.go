//go:build !windows

package awsm

import (
	"os/exec"
	"syscall"
)

// configure puts the command in its own process group and kills the whole group
// when the context is cancelled.
//
// This is not a refinement. awsm does not do the waiting itself: `awsm profile
// set` on a lapsed session runs `aws sso login`, which is a grandchild of this
// process. Killing only the direct child leaves that grandchild alive, holding
// the pipe this process is reading -- so the call never returns, the panel
// stays dimmed, and an `aws sso login` nobody can see keeps running until the
// machine is restarted. Cancelling has to mean the whole tree.
func configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// A negative pid means the process group, which is the point.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
