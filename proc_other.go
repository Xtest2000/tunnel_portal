//go:build !windows

package main

import (
	"os/exec"
	"runtime"
	"syscall"
)

func attachKillJob(mgr *Manager, cmd *exec.Cmd) {}

// openExternal 用系统默认程序打开网址（非 Windows 平台）。
// os/exec 的 argv 是直接传给进程的，不经过 shell，因此没有命令注入问题。
func openExternal(u string) error {
	bin := "xdg-open"
	if runtime.GOOS == "darwin" {
		bin = "open"
	}
	cmd := exec.Command(bin, u)
	hideConsole(cmd, true)
	return cmd.Start()
}

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
