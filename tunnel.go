package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Forward is one -L local port-forward.
type Forward struct {
	LocalHost  string `json:"localHost"`
	LocalPort  int    `json:"localPort"`
	RemoteHost string `json:"remoteHost"`
	RemotePort int    `json:"remotePort"`
}

// Config is the on-disk configuration (config.json next to the exe).
type Config struct {
	Host         string    `json:"host"`
	Port         int       `json:"port"`
	User         string    `json:"user"`
	IdentityFile string    `json:"identityFile,omitempty"`
	SSHBinary    string    `json:"sshBinary,omitempty"`
	Forwards     []Forward `json:"forwards"`
	ExtraOptions []string  `json:"extraOptions,omitempty"`
	AutoRestart  bool      `json:"autoRestart"`
	BatchMode    bool      `json:"batchMode"`
}

func defaultConfig() Config {
	return Config{
		// 刻意留空：本工具是通用的，不内置任何跳板机地址。
		// 首次运行需要用户自己填写，见 Config.Configured()。
		Port:        22,
		AutoRestart: true,
		BatchMode:   true,
	}
}

// Configured 判断是否已经填好最基本的两项（跳板地址与登录用户）。
// 没配置时不自动建通道，界面会提示用户先去设置里填。
func (c Config) Configured() bool {
	return strings.TrimSpace(c.Host) != "" && strings.TrimSpace(c.User) != ""
}

// LogLine is one captured output line of the ssh process.
type LogLine struct {
	Seq  int    `json:"seq"`
	Time string `json:"time"`
	Text string `json:"text"`
}

// Manager supervises the ssh tunnel process.
type Manager struct {
	mu        sync.Mutex
	cfg       Config
	bin       string
	cmd       *exec.Cmd
	running   bool
	shouldRun bool
	startedAt time.Time
	restarts  int
	lastErr   string
	logs      []LogLine
	seq       int
	logFile   *os.File
	killJob   any
}

func NewManager(cfg Config, logPath string) *Manager {
	m := &Manager{cfg: cfg, bin: resolveSSH(cfg)}
	if logPath != "" {
		// 0600：日志里有完整命令行（跳板地址、用户名、内网目标），别让同机其它用户读走
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			m.logFile = f
		}
	}
	return m
}

func resolveSSH(cfg Config) string {
	if cfg.SSHBinary != "" {
		return cfg.SSHBinary
	}
	if p, err := exec.LookPath("ssh"); err == nil {
		return p
	}
	for _, p := range []string{
		`C:\Windows\System32\OpenSSH\ssh.exe`,
		`C:\Program Files\OpenSSH\ssh.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "ssh"
}

func (m *Manager) buildArgs() []string {
	cfg := m.cfg
	args := []string{
		"-N", "-T",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new",
	}
	if cfg.BatchMode {
		args = append(args, "-o", "BatchMode=yes")
	}
	if cfg.Port > 0 {
		args = append(args, "-p", fmt.Sprintf("%d", cfg.Port))
	}
	if cfg.IdentityFile != "" {
		args = append(args, "-i", cfg.IdentityFile)
	}
	for _, f := range cfg.Forwards {
		lh := f.LocalHost
		if lh == "" {
			lh = "127.0.0.1"
		}
		args = append(args, "-L", fmt.Sprintf("%s:%d:%s:%d", lh, f.LocalPort, f.RemoteHost, f.RemotePort))
	}
	for _, o := range cfg.ExtraOptions {
		args = append(args, "-o", o)
	}
	args = append(args, fmt.Sprintf("%s@%s", cfg.User, cfg.Host))
	return args
}

var reKeyArg = regexp.MustCompile(`-i\s+\S+`)

// redactCommand 把命令行里的私钥路径替换掉。
// 命令行会进日志、也会显示在界面上，而日志经常被复制粘贴到 issue/聊天里求助。
func redactCommand(s string) string {
	return reKeyArg.ReplaceAllString(s, "-i <私钥路径已隐藏>")
}

func (m *Manager) commandString() string {
	return redactCommand(m.bin + " " + strings.Join(m.buildArgs(), " "))
}

func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return nil
	}
	if !m.cfg.Configured() {
		return fmt.Errorf("尚未配置跳板机：请在「设置」里填写地址与登录用户")
	}
	if len(m.cfg.Forwards) == 0 {
		return fmt.Errorf("未配置任何端口转发")
	}
	m.shouldRun = true
	m.restarts = 0
	return m.launchLocked()
}

func (m *Manager) launchLocked() error {
	args := m.buildArgs()
	cmd := exec.Command(m.bin, args...)
	prepareCmd(cmd)
	// BatchMode 下 ssh 不会有交互式提示，可以让它不分配控制台窗口；
	// 否则（可能需要在控制台输密码/口令）必须保留控制台，见 hideConsole 注释。
	hideConsole(cmd, m.cfg.BatchMode)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		m.lastErr = err.Error()
		m.appendLogLocked("启动失败: " + err.Error())
		m.shouldRun = false
		return err
	}
	m.cmd = cmd
	m.running = true
	m.startedAt = time.Now()
	m.lastErr = ""
	m.appendLogLocked("▶ 已启动: " + m.commandString())
	go m.pump(stdout)
	go m.pump(stderr)
	go m.wait(cmd)
	attachKillJob(m, cmd)
	return nil
}

func (m *Manager) pump(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		m.appendLog(sc.Text())
	}
}

func (m *Manager) wait(cmd *exec.Cmd) {
	err := cmd.Wait()
	uptime := time.Since(m.startedAt)
	m.mu.Lock()
	m.running = false
	m.cmd = nil
	if err != nil {
		m.lastErr = err.Error()
	} else {
		m.lastErr = "进程已退出"
	}
	m.appendLogLocked("■ 进程退出 (" + uptime.Round(time.Second).String() + "): " + errText(err))
	if uptime > 60*time.Second {
		m.restarts = 0
	}
	retry := m.shouldRun && m.cfg.AutoRestart
	n := m.restarts
	if retry {
		m.restarts++
	}
	m.mu.Unlock()

	if retry {
		delay := backoff(n)
		m.appendLog(fmt.Sprintf("↻ 将在 %s 后自动重连…", delay))
		time.Sleep(delay)
		m.mu.Lock()
		if m.shouldRun && !m.running {
			_ = m.launchLocked()
		}
		m.mu.Unlock()
	}
}

func backoff(n int) time.Duration {
	d := 3 * time.Second
	for i := 0; i < n && d < 30*time.Second; i++ {
		d *= 2
	}
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

func (m *Manager) Stop() {
	m.mu.Lock()
	wasRunning := m.running
	m.shouldRun = false
	cmd := m.cmd
	if wasRunning {
		m.appendLogLocked("⏹ 手动停止通道")
	}
	m.mu.Unlock()
	terminate(cmd)
	time.Sleep(250 * time.Millisecond)
}

func (m *Manager) Restart() error {
	m.Stop()
	return m.Start()
}

func (m *Manager) SetConfig(cfg Config) {
	m.mu.Lock()
	m.cfg = cfg
	m.bin = resolveSSH(cfg)
	m.mu.Unlock()
}

func (m *Manager) Config() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

func (m *Manager) localPortOpen() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.cfg.Forwards) == 0 {
		return false
	}
	f := m.cfg.Forwards[0]
	host := f.LocalHost
	if host == "" {
		host = "127.0.0.1"
	}
	c, err := netDial("tcp", fmt.Sprintf("127.0.0.1:%d", f.LocalPort), 800*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func (m *Manager) Status() map[string]any {
	m.mu.Lock()
	running := m.running
	pid := 0
	if m.cmd != nil && m.cmd.Process != nil {
		pid = m.cmd.Process.Pid
	}
	started := m.startedAt
	restarts := m.restarts
	lastErr := m.lastErr
	cmdStr := m.commandString()
	cfg := m.cfg
	m.mu.Unlock()

	uptime := 0
	if running {
		uptime = int(time.Since(started).Seconds())
	}
	return map[string]any{
		"running":    running,
		"pid":        pid,
		"uptimeSec":  uptime,
		"restarts":   restarts,
		"lastErr":    lastErr,
		"command":    cmdStr,
		"sshBinary":  m.bin,
		"localOpen":  m.localPortOpen(),
		"configured": cfg.Configured(),
		"config":     cfg,
	}
}

func (m *Manager) Logs(since int) ([]LogLine, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]LogLine, 0, 64)
	for _, l := range m.logs {
		if l.Seq > since {
			out = append(out, l)
		}
	}
	return out, m.seq
}

func (m *Manager) appendLog(text string) {
	m.mu.Lock()
	m.appendLogLocked(text)
	m.mu.Unlock()
}

func (m *Manager) appendLogLocked(text string) {
	m.seq++
	line := LogLine{Seq: m.seq, Time: time.Now().Format("15:04:05"), Text: text}
	m.logs = append(m.logs, line)
	if len(m.logs) > 800 {
		m.logs = m.logs[len(m.logs)-800:]
	}
	if m.logFile != nil {
		fmt.Fprintf(m.logFile, "%s %s\n", line.Time, text)
	}
}

func errText(err error) string {
	if err == nil {
		return "正常结束"
	}
	return err.Error()
}
