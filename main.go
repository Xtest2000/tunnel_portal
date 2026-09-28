package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

func netDial(network, addr string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout(network, addr, timeout)
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		wd, _ := os.Getwd()
		return wd
	}
	return filepath.Dir(exe)
}

func loadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := defaultConfig()
			saveConfig(path, cfg)
			return cfg, nil
		}
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func saveConfig(path string, cfg Config) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	hideConsole(cmd, true) // Windows 上避免 cmd.exe 再闪一个黑框
	_ = cmd.Start()
}

func main() {
	var (
		listenFlag = flag.String("listen", "127.0.0.1:8760", "控制台监听地址")
		configFlag = flag.String("config", "", "配置文件路径 (默认 exe 同目录 config.json)")
		noBrowser  = flag.Bool("no-browser", false, "启动时不打开界面窗口（仅后台运行）")
		autoStart  = flag.Bool("autostart", true, "启动后自动建立通道")
	)
	flag.Parse()

	dir := exeDir()
	cfgPath := *configFlag
	if cfgPath == "" {
		cfgPath = filepath.Join(dir, "config.json")
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		log.Fatalf("读取配置失败: %v", err)
	}
	mgr := NewManager(cfg, filepath.Join(dir, "tunnel.log"))

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		mgr.Stop()
		os.Exit(0)
	}()

	if *autoStart {
		if cfg.Configured() {
			if err := mgr.Start(); err != nil {
				mgr.appendLog("自动建立失败: " + err.Error())
			}
		} else {
			mgr.appendLog("尚未配置跳板机，已跳过自动建通道。请在「设置」里填写地址与登录用户后保存。")
		}
	}

	mux := http.NewServeMux()
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, mgr.Status())
	})
	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		since, _ := strconv.Atoi(r.URL.Query().Get("since"))
		lines, next := mgr.Logs(since)
		writeJSON(w, map[string]any{"lines": lines, "next": next})
	})
	mux.HandleFunc("/api/start", func(w http.ResponseWriter, r *http.Request) {
		err := mgr.Start()
		writeJSON(w, map[string]any{"ok": err == nil, "error": errStr(err)})
	})
	mux.HandleFunc("/api/stop", func(w http.ResponseWriter, r *http.Request) {
		mgr.Stop()
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/restart", func(w http.ResponseWriter, r *http.Request) {
		err := mgr.Restart()
		writeJSON(w, map[string]any{"ok": err == nil, "error": errStr(err)})
	})
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, mgr.Config())
		case http.MethodPut, http.MethodPost:
			var cfg Config
			if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
				writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			if err := saveConfig(cfgPath, cfg); err != nil {
				writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			mgr.SetConfig(cfg)
			writeJSON(w, map[string]any{"ok": true})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	// 用系统默认浏览器打开一个地址——只服务于「快速访问」列表里某条转发。
	// 工具不预设任何"要打开的网址"：建通道与转发不等于要用网页访问。
	mux.HandleFunc("/api/open", func(w http.ResponseWriter, r *http.Request) {
		target := strings.TrimSpace(r.URL.Query().Get("url"))
		if target == "" && r.Method == http.MethodPost {
			var body struct {
				URL string `json:"url"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			target = strings.TrimSpace(body.URL)
		}
		if target == "" {
			writeJSON(w, map[string]any{"ok": false, "error": "缺少要打开的地址"})
			return
		}
		openBrowser(target)
		writeJSON(w, map[string]any{"ok": true, "url": target})
	})
	mux.HandleFunc("/api/forward/add", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Target     string `json:"target"`
			RemoteHost string `json:"remoteHost"`
			RemotePort int    `json:"remotePort"`
			LocalHost  string `json:"localHost"`
			LocalPort  int    `json:"localPort"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": "请求格式错误: " + err.Error()})
			return
		}
		host, port := strings.TrimSpace(body.RemoteHost), body.RemotePort
		if strings.TrimSpace(body.Target) != "" {
			var err error
			host, port, err = parseTarget(body.Target)
			if err != nil {
				writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
				return
			}
		}
		if host == "" || port < 1 || port > 65535 {
			writeJSON(w, map[string]any{"ok": false, "error": "请写成 主机:端口，例如 192.168.1.50:8080"})
			return
		}

		cfg := mgr.Config()
		for _, e := range cfg.Forwards {
			if e.RemoteHost == host && e.RemotePort == port {
				writeJSON(w, map[string]any{
					"ok": true, "forward": e, "existed": true,
					"note": fmt.Sprintf("该目标已有转发，本地端口 %d", e.LocalPort),
				})
				return
			}
		}

		lp := body.LocalPort
		if lp == 0 {
			lp = pickLocalPort(mgr, port)
			if lp == 0 {
				writeJSON(w, map[string]any{"ok": false, "error": "找不到可用的本地端口，请手动指定"})
				return
			}
		}
		for _, e := range cfg.Forwards {
			if e.LocalPort == lp {
				writeJSON(w, map[string]any{"ok": false, "error": fmt.Sprintf("本地端口 %d 已被现有转发占用", lp)})
				return
			}
		}
		lh := strings.TrimSpace(body.LocalHost)
		if lh == "" {
			lh = "127.0.0.1"
		}
		f := Forward{LocalHost: lh, LocalPort: lp, RemoteHost: host, RemotePort: port}

		nf := make([]Forward, 0, len(cfg.Forwards)+1)
		nf = append(nf, cfg.Forwards...)
		nf = append(nf, f)
		cfg.Forwards = nf
		if err := saveConfig(cfgPath, cfg); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": "写入配置失败: " + err.Error()})
			return
		}
		mgr.SetConfig(cfg)
		applied, aerr := applyConfig(mgr)
		writeJSON(w, map[string]any{
			"ok": true, "forward": f, "existed": false,
			"applied": applied, "applyError": errStr(aerr),
		})
	})
	mux.HandleFunc("/api/forward/remove", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			LocalPort int `json:"localPort"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": "请求格式错误: " + err.Error()})
			return
		}
		cfg := mgr.Config()
		nf := make([]Forward, 0, len(cfg.Forwards))
		found := false
		for _, e := range cfg.Forwards {
			if e.LocalPort == body.LocalPort {
				found = true
				continue
			}
			nf = append(nf, e)
		}
		if !found {
			writeJSON(w, map[string]any{"ok": false, "error": fmt.Sprintf("没有本地端口为 %d 的转发", body.LocalPort)})
			return
		}
		cfg.Forwards = nf
		if err := saveConfig(cfgPath, cfg); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": "写入配置失败: " + err.Error()})
			return
		}
		mgr.SetConfig(cfg)
		applied, aerr := applyConfig(mgr)
		writeJSON(w, map[string]any{"ok": true, "applied": applied, "applyError": errStr(aerr)})
	})
	mux.HandleFunc("/api/quit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
		// 先把响应发出去，再收尾退出；否则浏览器会看到一个断开的连接。
		go func() {
			time.Sleep(200 * time.Millisecond)
			mgr.appendLog("⏻ 退出程序")
			mgr.Stop()
			os.Exit(0)
		}()
	})

	addr := *listenFlag
	url := "http://" + addr + "/"
	mgr.appendLog("控制台已就绪: " + url)
	fmt.Println("Tunnel Portal 控制台:", url)
	fmt.Println("配置文件:", cfgPath)

	// 平台相关：Windows 走自带的 WebView2 窗口，其他平台回退到系统浏览器。
	// 两者都会阻塞，直到界面被关闭。
	runGUI(mux, addr, url, !*noBrowser)

	mgr.Stop()
	os.Exit(0)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// parseTarget 解析用户输入的 "主机:端口"。容忍带 scheme 或路径的写法。
func parseTarget(s string) (string, int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0, fmt.Errorf("目标地址为空")
	}
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	var host, portStr string
	if strings.HasPrefix(s, "[") { // IPv6: [::1]:8080
		j := strings.Index(s, "]")
		if j < 0 {
			return "", 0, fmt.Errorf("IPv6 地址缺少右括号")
		}
		host = s[1:j]
		rest := s[j+1:]
		if !strings.HasPrefix(rest, ":") {
			return "", 0, fmt.Errorf("请写成 主机:端口，例如 192.168.1.50:8080")
		}
		portStr = rest[1:]
	} else {
		j := strings.LastIndex(s, ":")
		if j < 0 {
			return "", 0, fmt.Errorf("请写成 主机:端口，例如 192.168.1.50:8080")
		}
		host, portStr = s[:j], s[j+1:]
	}
	host, portStr = strings.TrimSpace(host), strings.TrimSpace(portStr)
	if host == "" || portStr == "" {
		return "", 0, fmt.Errorf("请写成 主机:端口，例如 192.168.1.50:8080")
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p < 1 || p > 65535 {
		return "", 0, fmt.Errorf("端口无效: %s", portStr)
	}
	return host, p, nil
}

// localPortFree 通过实际绑定来判断本地端口是否可用。
func localPortFree(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// pickLocalPort 优先复用目标端口，被占则依次尝试 9000+ 段位，最后交给系统挑。
func pickLocalPort(mgr *Manager, remotePort int) int {
	used := map[int]bool{}
	for _, f := range mgr.Config().Forwards {
		used[f.LocalPort] = true
	}
	ok := func(p int) bool { return p >= 1024 && p <= 65535 && !used[p] && localPortFree(p) }
	if ok(remotePort) {
		return remotePort
	}
	for p := 9000; p <= 9999; p++ {
		if ok(p) {
			return p
		}
	}
	for p := 10000; p <= 20000; p++ {
		if ok(p) {
			return p
		}
	}
	if l, err := net.Listen("tcp", "127.0.0.1:0"); err == nil {
		defer func() { _ = l.Close() }()
		if a, isTCP := l.Addr().(*net.TCPAddr); isTCP {
			return a.Port
		}
	}
	return 0
}

// applyConfig 让新的转发配置立刻生效：运行中就重启，没运行就启动。
func applyConfig(mgr *Manager) (bool, error) {
	if running, _ := mgr.Status()["running"].(bool); running {
		return true, mgr.Restart()
	}
	return true, mgr.Start()
}
