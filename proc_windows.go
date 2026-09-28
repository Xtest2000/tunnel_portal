//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// openExternal 用系统默认程序打开一个网址。
//
// 走 ShellExecuteW，不经过任何命令解释器：
// 因此 URL 里的特殊字符不会被当成命令分隔符（早期版本用 cmd /c start，存在命令注入）。
func openExternal(u string) error {
	verb, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	target, err := syscall.UTF16PtrFromString(u)
	if err != nil {
		return err
	}
	proc := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")
	const swShowNormal = 1
	ret, _, _ := proc.Call(0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(target)),
		0, 0, swShowNormal)
	if ret <= 32 { // ShellExecuteW 约定：返回值 <= 32 表示失败
		return fmt.Errorf("系统未能打开该地址（ShellExecuteW 返回 %d）", ret)
	}
	return nil
}

// hideConsole 让子进程不分配控制台窗口。
//
// 为什么需要它：本程序是 GUI 子系统（-extldflags "-Wl,--subsystem,windows"），
// 自身没有控制台。而 ssh.exe 是控制台程序——父进程没有控制台时，Windows 会
// 给它新分配一个，表现就是每建立一次通道就弹出一个黑色终端窗口。
// 加上 CREATE_NO_WINDOW 后，子进程照常跑，但不会再开窗口。
//
// 例外：关闭 BatchMode 时不能隐藏，否则 ssh 要你输密码/私钥口令的提示
// 会打在看不见的窗口里，表现为"卡住不动"。
func hideConsole(cmd *exec.Cmd, hide bool) {
	if !hide {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}

// attachKillJob puts the child into a Job Object configured with
// KILL_ON_JOB_CLOSE, so the ssh process dies when this app exits
// (even on a hard kill or console close).
func attachKillJob(mgr *Manager, cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return
	}
	h, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		windows.CloseHandle(job)
		return
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		windows.CloseHandle(job)
		return
	}
	mgr.killJob = job // keep handle open for the lifetime of the app
}

func prepareCmd(cmd *exec.Cmd) {}

func terminate(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
