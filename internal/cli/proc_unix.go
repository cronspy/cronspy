//go:build unix

package cli

import (
	"os"
	"os/exec"
	"syscall"
)

// ownGroup starts the job in its own process group, so a signal reaches it and its children.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// forward passes a signal to the job's process group.
func forward(cmd *exec.Cmd, s os.Signal) {
	if sig, ok := s.(syscall.Signal); ok && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, sig)
	}
}
