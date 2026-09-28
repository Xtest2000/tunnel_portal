//go:build windows

package main

import (
	"net/http"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	webview "github.com/webview/webview_go"
	"golang.org/x/sys/windows/registry"
)

// Edge WebView2 Runtime 在注册表里的固定标识
const webView2ClientGUID = `{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`

// hasWebView2 检查系统有没有装 WebView2 运行时。
// Win10 1803+ 与 Win11 通常自带（随 Edge 一起），个别精简/企业机器可能没有。
func hasWebView2() bool {
	subs := []string{
		`SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\` + webView2ClientGUID,
		`SOFTWARE\Microsoft\EdgeUpdate\Clients\` + webView2ClientGUID,
	}
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		for _, sub := range subs {
			k, err := registry.OpenKey(root, sub, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			v, _, err := k.GetStringValue("pv")
			_ = k.Close()
			if err == nil && v != "" {
				return true
			}
		}
	}
	return false
}

func messageBox(title, text string, flags uintptr) {
	proc := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	t, err1 := syscall.UTF16PtrFromString(text)
	c, err2 := syscall.UTF16PtrFromString(title)
	if err1 != nil || err2 != nil {
		return
	}
	_, _, _ = proc.Call(0,
		uintptr(unsafe.Pointer(t)),
		uintptr(unsafe.Pointer(c)),
		flags)
}

// blockForever 让 main 不返回（HTTP 服务在后台继续跑）。
func blockForever() {
	for {
		time.Sleep(time.Hour)
	}
}

// runGUI 打开程序自带的窗口界面（WebView2），不再依赖外部浏览器。
// 窗口关闭后本函数返回，调用方随即退出程序。
func runGUI(mux *http.ServeMux, addr, url string, open bool) {
	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			messageBox("Tunnel Portal", "本地控制台启动失败：\n"+err.Error(), 0x10)
		}
	}()
	time.Sleep(300 * time.Millisecond)

	if !open {
		// -no-browser：只当后台服务跑，不开窗口
		blockForever()
	}
	if !hasWebView2() {
		messageBox("Tunnel Portal",
			"未检测到 WebView2 运行时，本次改用默认浏览器打开控制台。\n\n"+
				"想要独立窗口，请安装「Microsoft Edge WebView2 Runtime」后重新启动本程序。", 0x30)
		openBrowser(url)
		blockForever()
	}

	runtime.LockOSThread() // WebView2 的窗口/消息循环必须在同一个 OS 线程上
	w := webview.New(false)
	if w == nil {
		messageBox("Tunnel Portal", "界面初始化失败，改用默认浏览器打开控制台。", 0x30)
		openBrowser(url)
		blockForever()
	}
	defer w.Destroy()
	w.SetTitle("内网通道 · Tunnel Portal")
	w.SetSize(1000, 820, webview.HintNone)
	w.Navigate(url)
	w.Run() // 阻塞，直到窗口被关闭
}
