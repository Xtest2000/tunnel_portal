# Tunnel Portal · 内网通道一键工具

一个 Windows 单文件 exe：双击后后台用 `ssh -N -L ...` 建立到内网节点的隧道，
并弹出**自带窗口界面**，用来 启动 / 断开 / 重连、看状态和日志、临时加转发。

本质就是把你手动敲的这条命令包起来、保活、可视化：

```bash
ssh -N -o ServerAliveInterval=15 -o ExitOnForwardFailure=yes \
    -L 3080:127.0.0.1:3080 -p 6003 <登录用户>@<跳板地址>
```

## 快速开始（Windows）

1. 把 `TunnelPortal.exe`（或 `TunnelPortal-gui.exe`，无控制台窗口版）拷到任意目录。
2. 双击运行。首次会在同目录生成一份**空配置** `config.json`。
3. 程序弹出**自带窗口界面**（内嵌 WebView2，不需要外部浏览器），并提示你填配置。
4. 在「设置」里填 **VPS 地址 / 端口 / 登录用户**（按需填私钥路径），点「保存并重连」。
5. 状态变绿（"通道已建立"）后，用「快速访问」把内网目标 `IP:端口` 映射到本机。
   想用浏览器访问就点该行的「打开网页」，想用别的客户端（ssh、数据库、RDP…）就点「复制」拿地址。

> **本工具不内置任何服务器地址**——这是个通用工具，装好后第一件事就是填你自己的跳板机。
> 没填之前不会自动建通道，顶部会有一条"首次使用"提示。

> **关于界面**：控制台跑在 `http://127.0.0.1:8760/`（仅监听回环地址），但**由程序自己的窗口承载**——
> 你不需要手动开浏览器。窗口关掉即退出程序（会一并停掉 ssh 子进程）。
> 依赖 **Microsoft Edge WebView2 Runtime**（Win10 1803+ / Win11 通常随 Edge 自带）；
> 若系统里没有，程序会弹框提示，并临时改用默认浏览器打开控制台。

> 依赖 **Windows 自带的 OpenSSH 客户端**（Win10 1809+ 默认自带 `C:\Windows\System32\OpenSSH\ssh.exe`）。
> 私钥默认走 `C:\Users\<你>\.ssh\`（或 ssh-agent）；如需指定，在设置里填 `identityFile`。

## 快速访问（临时加一条转发）

控制台上的「快速访问」卡片，可以在**不改配置文件**的前提下，把内网任意 `IP:端口` 映射到本机：

1. 填目标地址，例如 `192.168.1.50:8080`（也接受带 `http://` 前缀或带路径的写法）
2. 本地端口留空 = 自动分配（优先复用目标端口，被占用则从 9000 起找空闲端口）
3. 点「＋ 添加转发」→ 通道按新配置重建，成功后该行出现在下面的列表里

> 添加后**不会自动打开浏览器**——要用就点该行的「打开网页」，否则用「复制」把地址拿去给别的客户端。

下面的列表列出当前所有转发，每行可以 **打开网页 / 复制地址 / 删除**。改动会立刻写回 `config.json`。

> 本工具**不预设任何"要打开的网址"**——建通道、做转发跟"要不要用浏览器访问"是两件事。
> 「打开网页」只是列表里的一个便利按钮，仅对 HTTP 类服务有意义；转发 SSH、数据库、RDP 时忽略它即可。

> 注意：新增或删除转发会**重启一次 ssh 子进程**（约 1 秒），现有通道会闪断一下再恢复。
> 目标地址一律由**远端**（被登录的那台机器）解析，所以填的是内网视角的地址。

## 两个版本

| 文件 | 说明 |
|---|---|
| `TunnelPortal.exe` | 保留控制台窗口（显示启动信息与 panic），方便首次排错 |
| `TunnelPortal-gui.exe` | 无控制台窗口，只有自带界面，日常用这个 |

两者的界面完全一样，区别只是**背后那个黑框是否显示**。

## config.json 字段

```jsonc
{
  "host": "",                 // 跳板地址（必填）
  "port": 22,                 // 跳板端口，默认 22；填 0 则不加 -p
  "user": "",                 // 登录用户（必填）
  "identityFile": "",         // 私钥路径，留空用默认
  "sshBinary": "",            // ssh.exe 路径，留空自动探测
  "forwards": [],             // 可多条 -L，会被展开成 -L 本地:目标
  "autoRestart": true,        // 断线自动重连（指数退避，上限 30s）
  "batchMode": true           // 不弹密码/口令提示；密钥需免密或已加载到 ssh-agent
}
```

> `host` 与 `user` 都是空的时，程序视为"尚未配置"：不自动建通道，界面顶部显示填写提示。

## 命令行参数

```
-listen 127.0.0.1:8760   控制台监听地址
-config <path>           配置文件路径（默认 exe 同目录 config.json）
-no-browser              启动时不自动开浏览器
-autostart=false         启动后不自动建通道
```

## 行为说明

### 控制台的安全边界

控制台虽然只监听回环地址，但**回环端口不是按用户隔离的**——同一台机器上的其它进程、
其它用户，以及你浏览的任意网页，都可能够到它。所以从 v0.0.2 起加了三道闸门：

1. **一次性访问令牌**：每次启动随机生成（只存在内存里，随程序退出失效），拼在程序打开窗口的
   URL 上。所有 `/api/*` 都必须带这个令牌，否则一律拒绝。
   想手动在浏览器里打开控制台，用启动时终端里打印的那条「带令牌地址」。
2. **Host / Origin 校验**：只接受回环 Host；带 `Origin` 的请求其来源必须是自身。
   这挡住的是 DNS rebinding —— 没有这道检查，你一边开隧道一边逛恶意网页，对方就能读走你的配置。
3. **`/api/open` 不再经过 shell**：改用系统 API（Windows 上 `ShellExecuteW`）打开，
   且只放行 `http`/`https`。早期版本用 `cmd /c start "" <url>`，
   URL 里的 `&` 会被 cmd.exe 当命令分隔符 → 命令注入。

另外两条：

- **转发只允许绑回环**：`/api/forward/add` 和界面上保存配置都会拒绝 `0.0.0.0` 之类的绑定，
  避免把内网服务误暴露到你所在的整个局域网。确实需要暴露，请直接编辑 `config.json`
  （程序仍会照做，但会在日志里给一条醒目告警）。
- **日志收紧了**：`tunnel.log` 权限改为 `0600`，命令行里的私钥路径会被替换成
  `<私钥路径已隐藏>`——日志经常被复制粘贴到 issue 或聊天里求助，不该带这些。

> 如果你用 `-listen` 绑到非回环地址，控制台就会**对网络开放**：Host 校验会相应放宽，
> 只剩令牌这一道防线，程序启动时会在日志里警告你。除非明确知道后果，否则别这么用。

- 断线自动重连，退避序列 3→6→12→24→30s；进程稳定运行 >60s 后重连计数归零。
- **不会弹出黑色终端窗口**：界面是程序自带窗口，拉起 `ssh.exe` 时带 `CREATE_NO_WINDOW`
  （否则 GUI 程序启动控制台子进程时，Windows 会给它新分配一个控制台窗口，每重连一次弹一个黑框）。
  只有在你**关掉 `batchMode`**（即需要 ssh 交互输入密码/口令）时才会保留控制台。
- **怎么退出**：控制台上的「⏻ 退出程序」按钮（`POST /api/quit`）。`TunnelPortal-gui.exe` 没有窗口，
  控制台是唯一入口；`TunnelPortal.exe` 也可以直接关控制台窗口或按 Ctrl+C。退出时会先停掉 ssh 子进程。
- 关闭程序（含被强杀 / 关控制台窗口）会连同 ssh 子进程一起结束，不留孤儿进程
  （Windows 用 Job Object 保证）。
- 日志同时写入同目录 `tunnel.log`。
- 控制台接口：`/api/status`、`/api/logs`、`/api/start`、`/api/stop`、`/api/restart`、
  `/api/config`（GET/PUT）、`/api/open`，以及快速访问用的 `/api/forward/add`、`/api/forward/remove`。

## 从源码构建

```bash
# 在 Windows 上原生编译
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/TunnelPortal.exe .
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '-s -w -extldflags "-Wl,--subsystem,windows"' -o dist/TunnelPortal-gui.exe .
```

### 从 Linux 交叉编译（本项目实际用法）

界面用了 WebView2，因此**必须开 cgo**，也就需要一个能编 Windows 的 C/C++ 工具链。
本机用 Zig 自带的那套（自包含、不需要 root）：

```bash
export PATH=/path/to/go/bin:$PATH      # Go 版本需 >= go.mod 里声明的版本
ZIG=/path/to/zig/zig                   # Zig 自带 mingw-w64，能当交叉编译器用，无需 root
export CC="$ZIG cc  -target x86_64-windows-gnu -fno-sanitize=undefined"
export CXX="$ZIG c++ -target x86_64-windows-gnu -fno-sanitize=undefined"
export CGO_CFLAGS="-I$PWD/wincompat"
export CGO_CXXFLAGS="$CGO_CFLAGS"
export CGO_ENABLED=1 GOOS=windows GOARCH=amd64

# 带控制台
go build -trimpath -ldflags "-s -w" -o dist/TunnelPortal.exe .
# 无控制台（注意：cgo 走外部链接，-H=windowsgui 会失效，得用 extldflags）
go build -trimpath -ldflags '-s -w -extldflags "-Wl,--subsystem,windows"' -o dist/TunnelPortal-gui.exe .
```

两个踩过的坑，改动时别踩回去：

- `wincompat/EventToken.h` 是补出来的垫片：WebView2.h 会 `#include "EventToken.h"`，
  而 mingw-w64 不提供这个 Windows SDK 头文件。**若该目录没进 `CGO_CXXFLAGS` 就会报
  `'EventToken.h' file not found`**（C 文件走 `CGO_CFLAGS`，C++ 文件走 `CGO_CXXFLAGS`，两个都要设）。
- `-H=windowsgui` 只在 Go 内部链接器下有效；开了 cgo 之后走外部链接，必须改用
  `-extldflags "-Wl,--subsystem,windows"`，否则 gui 版会退化成带黑框的控制台程序。

## 许可证

MIT，见 [LICENSE](LICENSE)；第三方组件（WebView2、webview_go、x/sys 等）见 [NOTICE](NOTICE)。

> `config.json`、`tunnel.log` 和 `dist/` 都在 `.gitignore` 里——它们包含你私有的跳板地址、
> 用户名与私钥路径，**永远不要提交**。
