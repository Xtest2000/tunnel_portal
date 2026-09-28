//go:build !windows

package main

import (
	"log"
	"net/http"
	"time"
)

// runGUI 在非 Windows 平台上没有原生窗口可用，退回到"打开系统浏览器 +
// 前台跑 HTTP 服务"的老行为（同时保留它，方便在 Linux 上做逻辑自测）。
func runGUI(h http.Handler, addr, url string, open bool) {
	if open {
		go func() {
			time.Sleep(400 * time.Millisecond)
			_ = openBrowser(url)
		}()
	}
	if err := http.ListenAndServe(addr, h); err != nil {
		log.Fatalf("HTTP 服务启动失败: %v", err)
	}
}
