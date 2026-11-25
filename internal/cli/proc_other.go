//go:build !unix

package cli

import (
	"os"
	"os/exec"
)

func ownGroup(*exec.Cmd) {}

func forward(cmd *exec.Cmd, s os.Signal) {
	if cmd.Process != nil {
		_ = cmd.Process.Signal(s)
	}
}
