# sshDesktop

> 版本：0.2.1 · 更新时间：2026-10-20 22:04 +08:00 · 状态：M2（多服务器）完成

sshDesktop 是一个装在 Linux 服务器上的开源 Web 管理面板，结合了 [1Panel](https://github.com/1Panel-dev/1Panel) 的主机管理能力和[哪吒探针](https://github.com/nezhahq/nezha)的多机监控能力。除了传统的后台界面，它还提供**桌面模式**：在浏览器里渲染一个 Windows 11 风格的桌面，有任务栏、开始菜单、可拖拽的窗口和文件资源管理器，让你用操作 PC 的方式管理服务器。

以 GPL-3.0 开源。

## 当前功能（0.2.1）

| 功能 | 说明 | 代码 |
| --- | --- | --- |
| 登录与会话 | 锁屏式登录、会话 Cookie、CSRF 防护、二级确认（危险操作需再次输入密码）、远程 IP 暴力破解封禁、本机按用户名指数延迟 | `internal/auth/`、`internal/server/`、`web/src/views/LoginView.vue` |
| 访问控制 | 未配置域名和 HTTPS 时只接受本机连接（通过 SSH 隧道访问）；对外开放后校验 Host | `internal/server/` |
| 账号、角色、审计 | 多账号、自定义角色与功能权限、在线会话踢出、封禁管理；所有写操作记审计，可筛选、分页、导出 CSV | `internal/rbac/`、`internal/audit/`、`internal/module/users/`、`web/src/apps/users/`、`web/src/apps/audit/` |
| Win11 桌面 | 任务栏、开始菜单（固定与全部应用）、可拖拽缩放的多窗口、贴靠、桌面图标与小组件、通知中心、深浅色、壁纸，窗口状态刷新后恢复 | `web/src/shells/desktop/`、`web/src/stores/desktop.ts` |
| 管理后台 | 传统侧边栏布局，手机上使用抽屉导航 | `web/src/shells/admin/` |
| 监控 | CPU、内存、负载、磁盘、网络、磁盘 IO 实时曲线与 1 小时到 30 天的历史，按核心与按磁盘明细；可切换到任意服务器 | `internal/module/monitor/`、`web/src/apps/monitor/` |
| 终端 | 多标签、每标签最多 4 分屏、同步输入、常用命令片段，断线自动重连并可重新附着到仍在运行的会话 | `internal/module/terminal/`、`web/src/apps/terminal/` |
| 文件资源管理器 | 前进后退、地址栏、搜索、多视图、拖放上传下载、复制剪切粘贴、重命名、权限与属主、压缩解压（zip、tar.gz、tar.xz 等）、回收站（还原、彻底删除） | `internal/module/files/`、`internal/vfs/`、`web/src/apps/explorer/` |
| 编辑器与预览 | CodeMirror 编辑器（按文件名和 shebang 识别语言，冲突检测，保留 CRLF），图片、视频、音频、PDF 预览 | `web/src/apps/editor/`、`web/src/apps/viewer/` |
| 进程 | 列表与树形视图、搜索、发送信号、打开的文件与网络连接 | `internal/module/process/`、`web/src/apps/processes/` |
| 系统服务 | systemd 单元列表与筛选、启动停止重启、开机自启、实时日志、查看单元文件 | `internal/module/service/`、`web/src/apps/services/` |
| 设置 | 个性化、终端、修改密码、面板运行参数、关于 | `internal/module/system/`、`web/src/apps/settings/` |
| 服务器管理 | 添加远程服务器（私钥或密码，凭据用主密钥 AES-256-GCM 加密；首次连接核对主机指纹，指纹变化拒绝连接并醒目提示，二级确认后才接受新指纹）、修改、删除（二级确认）、重新连接；每台服务器一条 SSH 长连接，终端、SFTP、agent 通道、命令复用，断线指数退避重连，每台最多 32 个通道、8 个文件传输。多服务器卡片墙总览，任务栏托盘与小组件显示在线状态，断线和恢复进入通知中心 | `internal/node/`、`internal/module/nodes/`、`internal/secret/`、`web/src/apps/servers/`、`web/src/stores/nodes.ts` |
| agent | 独立的只读 agent（systemd + DynamicUser 运行，socket `/run/sshdesktop/agent.sock` 由 `sshdesktop-agent.socket` 创建，保存 7 天分钟级指标和流量计数），面板经 SSH 通道读取；一键安装通过 SFTP 上传与对方架构匹配的二进制，远程服务器不需要外网，需要 root 或 sudo。面板自身在同一 socket 提供相同接口；没有 agent 时每 5 秒经 SSH 采集 | `cmd/sshdesktop-agent/`、`internal/agent/`、`internal/agentbin/`、`internal/module/nodes/agent.go`、`internal/module/monitor/remote.go` |
| 远程文件与终端 | 资源管理器“网络”下每台服务器一个网络位置（SFTP），跨服务器复制、剪切、拖放由面板中转并显示进度；终端可在任意服务器上开标签页或分屏，同步输入可同时发给多台服务器；窗口标题栏用彩色标签标出所属服务器 | `internal/vfs/sftp.go`、`internal/module/files/ops.go`、`web/src/apps/explorer/`、`web/src/apps/terminal/` |
| 按月流量 | 各服务器、各网卡的上行 / 下行 / 合计，每月重置日与额度（按合计、仅下行或仅上行计算），每日柱状图与最近 12 期；需要 agent 或面板 | `internal/module/traffic/`、`web/src/apps/traffic/` |
| 批量命令 | 选择多台服务器并发执行（默认并发 5，可设超时），按服务器分栏实时显示输出和退出码，执行记录与共享命令片段；一级确认列出全部目标 | `internal/module/batch/`、`web/src/apps/batch/` |
| 服务监控 | HTTP(S)（状态码、关键字）、TCP、Ping、SSL 证书到期，间隔最短 30 秒，结果保存 30 天，24 小时状态条、可用率与延迟曲线，异常和恢复进入通知中心 | `internal/module/probe/`、`web/src/apps/probes/` |
| 命令行 | `server`、`info`、`user add/reset-password/list`、`config show/get/set/unset`、`upgrade`、`uninstall`、`version` | `cmd/sshdesktop/` |

后续里程碑：容器与应用 → 网站与安全，详见 [docs/design.md](docs/design.md)。Docker、网站、防火墙等高权限功能只对本机开放。

## 安装

需要 systemd，支持 amd64 和 arm64；官方测试 Ubuntu 22.04+、Debian 12+、Rocky/Alma 9+。

```bash
curl -fsSL https://raw.githubusercontent.com/apple2333usa/sshDesktop/main/scripts/install.sh | sudo bash
```

脚本会下载对应架构的二进制并校验 SHA-256，写入 `/etc/sshdesktop/config.yaml`，注册 `sshdesktop` 系统服务，创建初始管理员并打印随机密码。服务器上已装有独立 agent 时会停用它，由面板接管同一个 socket。常用参数：`--port`、`--user`、`--version`、`--mirror`（GitHub 下载加速前缀）、`--binary`（离线安装本地文件）、`--yes`。

面板默认只监听 `127.0.0.1:33333`，在自己电脑上开 SSH 隧道后访问：

```bash
ssh -N -L 33333:127.0.0.1:33333 -p <SSH端口> <用户>@<服务器IP>
# 然后浏览器打开 http://127.0.0.1:33333
```

MobaXterm、Termius 等工具配置同样的本地端口转发即可。需要通过域名直接访问时：

```bash
sshdesktop config set domain panel.example.com
sshdesktop config set https.cert /path/fullchain.pem
sshdesktop config set https.key /path/privkey.pem
sshdesktop config set https true && systemctl restart sshdesktop
```

证书出问题进不去时，`sshdesktop config unset https` 可回到仅本机访问。忘记密码用 `sshdesktop user reset-password <用户名>`。

### 添加其他服务器

其他服务器不需要装面板。在桌面打开“服务器”→“添加服务器”，填地址、端口和 SSH 账号，点“获取指纹”并与服务器上 `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` 的输出核对，再填私钥或密码保存。之后可在卡片菜单里“安装 agent”获得完整的监控历史和按月流量；不装也能用文件、终端、批量命令和 5 秒一次的监控。远程服务器的凭据加密保存在面板数据目录，主密钥是 `<数据目录>/secret.key`（备份数据目录时一并备份）。`sshdesktop config set agent-socket <路径>` 可改面板自身 agent 接口的路径，设为 `-` 关闭。

## 开发

需要 Go 1.27+ 和 Node 22+。

```bash
make web        # 安装前端依赖并构建到 web/dist（会被嵌入二进制）
make build      # 构建 agent（嵌入面板，供一键安装）和 bin/sshdesktop
make test       # Go 测试 + 前端类型检查和单元测试
make dist       # 交叉编译 amd64、arm64 到 dist/，并生成 SHA256SUMS
```

本地运行：准备一个配置文件（`port`、`dataDir`、`logDir`），`./bin/sshdesktop -c <配置> user add <用户名>` 创建账号，再 `./bin/sshdesktop -c <配置> server`。前端热更新开发：`cd web && npm run dev`（代理到本地后端）。

端到端测试（Playwright）：

```bash
cd web
SD_URL=http://127.0.0.1:33333 SD_USER=<账号> SD_PASS=<密码> npx playwright test
```

在没有 systemd 的容器里加 `SD_NO_SYSTEMD=1` 跳过服务列表断言；`SD_SHOTS=1` 额外截图。多服务器用例（`e2e/servers.spec.ts`）需要一台可 SSH 登录的服务器，设置 `SD_SSH_HOST`、`SD_SSH_PORT`、`SD_SSH_USER`、`SD_SSH_PASS`（可以就是面板所在的机器），没设置时跳过。

Go 测试里的远程节点用进程内 SSH 服务器（`internal/node/sshtest/`，支持密码与私钥认证、exec、PTY、SFTP 和 Unix socket 转发），不需要真实的 sshd。

CI（`.github/workflows/ci.yml`）在推送 main 和 PR 时跑 Go vet 与测试、前端类型检查与单元测试、构建并冒烟启动；推送 `v*` 标签时 `release.yml` 构建并发布 GitHub Release。

## 目录

| 路径 | 内容 |
| --- | --- |
| `cmd/sshdesktop/` | 程序入口与命令行 |
| `cmd/sshdesktop-agent/`、`internal/agent/`、`internal/agentbin/` | 独立 agent、采集与 agent 接口、嵌入面板的 agent 二进制 |
| `internal/node/` | 节点抽象：本机与 SSH 远程节点（连接、重连、并发限制、SFTP），`sshtest/` 为测试用 SSH 服务器 |
| `internal/server/` | HTTP 服务、路由、中间件、访问控制 |
| `internal/module/` | 各功能模块的后端（节点、监控、流量、批量命令、服务监控、文件、终端、进程、服务、账号、系统、桌面） |
| `internal/auth/`、`internal/rbac/`、`internal/audit/` | 登录、权限、审计 |
| `internal/config/`、`internal/store/`、`internal/task/`、`internal/realtime/`、`internal/secret/` | 配置、SQLite 存储、后台任务、WebSocket 推送、凭据加密 |
| `web/` | 前端（Vue 3 + TypeScript + Naive UI），`web/e2e/` 为端到端测试 |
| `scripts/install.sh` | 一键安装脚本 |
| `docs/` | 设计文档、需求记录、旧版 README 归档 |

## 文档

| 文档 | 内容 |
| --- | --- |
| [docs/design.md](docs/design.md) | 设计文档：架构、模块、安全、部署、开发流程、里程碑 |
| [docs/requirements.md](docs/requirements.md) | 需求讨论记录 |
| [CHANGELOG.md](CHANGELOG.md) | 变更记录（按时间正序，最新在底部） |
