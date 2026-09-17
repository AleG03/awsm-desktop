package awsm

import "os/exec"

// configure has nothing to add on Windows.
//
// Killing a process tree there needs a job object rather than a process group,
// which is a good deal of machinery for a platform this application is only
// expected to run on. The WaitDelay set by the caller is what stops a surviving
// grandchild from holding the call open forever.
func configure(cmd *exec.Cmd) {}
