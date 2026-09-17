//go:build !windows

package awsm

import (
	"os/exec"
	"syscall"
)

// configure puts the command in its own process group and kills the whole group
// when the context is cancelled.
//
// This is not a refinement. awsm does not always do the waiting itself, and a
// process it starts is a grandchild of this one. Killing only the direct child
// leaves that grandchild alive, holding the pipe this process is reading -- so
// the call never returns, the panel stays dimmed, and something nobody can see
// keeps running until the machine is restarted. Cancelling has to mean the
// whole tree.
//
// The case that produced this was `awsm profile set` on a lapsed session, which
// ran `aws sso login` and waited for a browser. awsm signs in on its own now,
// so that particular grandchild is gone -- but the browsers it opens, and an
// older awsm still shelling out, are the same shape.
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
