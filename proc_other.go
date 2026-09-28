//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func attachKillJob(mgr *Manager, cmd *exec.Cmd) {}

// hideConsole 是 Windows 专有行为，其他平台空实现。
func hideConsole(cmd *exec.Cmd, hide bool) {}

func prepareCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminate(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
