# 变更记录

按时间正序记录，最新条目在底部。

## 2026-10-20 14:38 +08:00 · 未设版本 · 初版：创建仓库

- 简介：在 GitHub 创建私有仓库 `apple2333usa/sshDesktop`，由 GitHub 生成初始说明文件（仅含项目名和一句话简介）。
- 对应文件：`README.md`（原样归档于 `docs/readme-history/README-未设版本-archived-20261020-163024.md`）
- 验证与限制：无代码。时间取自初始提交 `f1278a8`。

## 2026-10-20 16:30 +08:00 · 未设版本 · 需求讨论与设计文档

- 简介：完成五轮需求讨论，撰写设计文档初稿（架构、节点与连接模型、前后端设计、17 个功能模块、环境体检与接管、安全、部署、开发流程、4 个里程碑）；确定以 GPL-3.0 开源；README 改写为项目当前状态。
- 对应文件：`docs/requirements.md`、`docs/design.md`、`README.md`、`LICENSE`、`CHANGELOG.md`、`docs/readme-history/README-未设版本-archived-20261020-163024.md`
- 验证与限制：仅文档变更，无代码可测试。设计文档已由用户确认（PR #1 合并），仓库同时改为公开；文中依赖库版本为 2026-10 查询结果，开工时再核对。

## 2026-10-20 17:58 +08:00 · 0.1.0 · M1：单机桌面

- 简介：实现设计文档里程碑 M1。后端（Go + Gin + SQLite）：启动配置与命令行（server、info、user、config、upgrade、uninstall、version）、登录会话与 CSRF、二级确认、暴力破解封禁、仅本机访问与 Host 校验、账号角色与功能权限、操作审计、后台任务与 WebSocket 推送，以及监控、终端、文件（含压缩、权限、回收站）、进程、系统服务、系统设置模块。前端（Vue 3 + Naive UI）：Win11 桌面外观（任务栏、开始菜单、多窗口、通知、深浅色）、管理后台外观，以及监控、终端、文件资源管理器、编辑器、预览、进程、服务、账号、审计、设置应用；中文界面。另有一键安装脚本、systemd 单元、Makefile、CI 与发布流程。README 改写为当前状态。
- 对应文件：`cmd/sshdesktop/`、`internal/`、`embed.go`、`web/`、`scripts/install.sh`、`Makefile`、`VERSION`、`.github/workflows/ci.yml`、`.github/workflows/release.yml`、`.gitignore`、`README.md`、`docs/readme-history/README-未设版本-20261020-163000.md`
- 验证与限制：容器内 `go vet`、`go test ./...`（访问控制、登录与 CSRF、封禁、权限、文件流程、压缩往返与 zip-slip、回收站、最后一个管理员保护、配置、密码、服务解析）通过；前端类型检查与 vitest 7 项通过；Playwright e2e 桌面 9 项与手机 1 项通过（容器无 systemd，服务列表断言以 `SD_NO_SYSTEMD` 跳过）。测试服务器部署结果待补充。尚未在真实浏览器中人工验证与操作系统之间的拖放、PDF 预览和关机重启。

## 2026-10-20 18:18 +08:00 · 0.1.0 · 测试服务器验证与隧道提示修正

- 简介：在测试服务器（Debian 13 arm64，root，SSH 端口非默认）用 `install.sh --binary` 安装 M1 并跑 e2e。发现安装脚本和 `sshdesktop info` 打印的 SSH 隧道命令有误：安装脚本按 `sshd` 子串匹配到了面板自己的进程（`sshdesktop`），把面板端口当成 SSH 端口，且给出云服务器网卡上的内网 IP；`info` 用主机名作地址且不带 SSH 端口。现改为：SSH 端口优先取 `sshd -T` 的生效配置（安装脚本再以精确匹配 `"sshd"` 的 `ss` 结果兜底），地址只给公网 IPv4，网卡上只有内网地址（含 CGNAT）时给 `<服务器IP>` 占位。另把 `upgrade.go` 按 gofmt 格式化。
- 对应文件：`scripts/install.sh`、`cmd/sshdesktop/cli.go`、`cmd/sshdesktop/upgrade.go`
- 验证与限制：服务器上安装成功，健康检查、systemd 自启、仅监听 127.0.0.1:33333、日志无报错；经 SSH 隧道跑 Playwright e2e 11 项（含截图）全部通过，服务列表断言在真实 systemd 下通过。修正后容器内 `go vet`、`go test ./...` 通过，端口与内网地址判断用样例输出验证；修正后的输出还需在服务器上复核。

## 2026-10-20 18:38 +08:00 · 0.1.0 · 发布 v0.1.0 与安装脚本版本变量修正

- 简介：在 `dee3141` 打 `v0.1.0` 标签，发布流程产出 `sshdesktop-linux-amd64`、`sshdesktop-linux-arm64`、`SHA256SUMS`。在测试服务器用一键安装命令复核时下载失败：安装脚本直接 `source /etc/os-release`，其中的 `VERSION`（如 `13 (trixie)`）覆盖了脚本自己的 `VERSION`，拼出非法下载地址；主流发行版都带该变量，`--version` 参数同样会被覆盖。现改为在子 shell 中读取 `ID`、`VERSION_ID`。
- 对应文件：`scripts/install.sh`
- 验证与限制：修正后的脚本在测试服务器（Debian 13 arm64）上走真实下载与 SHA-256 校验，安装成功，沿用原配置和账号；健康检查正常，服务 active；安装输出与 `sshdesktop info` 的隧道命令均为 `-p 5522 root@<服务器IP>`。v0.1.0 标签内的 `install.sh` 仍是旧版，一键安装命令取 main 分支，不受影响。

## 2026-10-20 20:45 +08:00 · 0.2.0 · M2：多服务器

- 简介：实现设计文档里程碑 M2。后端：SSH 远程节点（一条长连接复用终端、SFTP、agent 通道和命令，指数退避重连，每节点 32 个通道、8 个文件传输上限）；节点管理（凭据 AES-256-GCM 加密，首次连接确认主机指纹，指纹变化拒绝连接，二级确认后接受新指纹；删除为二级确认）；独立只读 agent 与经 SFTP + systemd 的一键安装（含 sudo），面板自身在 `/run/sshdesktop/agent.sock` 提供同样接口；无 agent 时经 SSH 每 5 秒采集；多节点监控总览；按月流量（重置日、额度）；跨节点复制 / 移动；批量命令（并发、超时、按节点输出、执行记录、命令片段）；服务监控（HTTP / TCP / Ping / SSL，保存 30 天）。新增权限点 `nodes:manage`、`batch:run`、`probe:view`、`probe:manage`。前端：服务器卡片墙与添加向导、流量统计、批量命令、服务监控四个应用；监控可切换节点；资源管理器“网络”位置与跨服务器复制 / 拖放；终端可在不同服务器上开标签页和分屏，同步输入跨服务器；窗口标题栏节点彩色标签；任务栏托盘与小组件显示节点状态；节点断线 / 恢复、指纹变化、服务监控异常进入通知中心。安装脚本在已有独立 agent 时停用它。
- 对应文件：`cmd/sshdesktop-agent/`、`internal/agent/`、`internal/agentbin/`、`internal/metric/`、`internal/mountinfo/`、`internal/node/`（含 `sshtest/`）、`internal/secret/`、`internal/vfs/sftp.go`、`internal/module/nodes/`、`internal/module/monitor/`、`internal/module/traffic/`、`internal/module/batch/`、`internal/module/probe/`、`internal/module/files/`、`internal/store/models.go`、`internal/rbac/rbac.go`、`internal/api/api.go`、`internal/config/config.go`、`internal/server/`、`web/src/apps/servers/`、`web/src/apps/traffic/`、`web/src/apps/batch/`、`web/src/apps/probes/`、`web/src/apps/explorer/`、`web/src/apps/terminal/`、`web/src/apps/monitor/MonitorApp.vue`、`web/src/stores/nodes.ts`、`web/src/components/NodePicker.vue`、`web/src/components/BarChart.vue`、`web/src/shells/`、`web/src/i18n/zh-CN.ts`、`web/e2e/servers.spec.ts`、`scripts/install.sh`、`Makefile`、`.github/workflows/release.yml`、`VERSION`、`web/package.json`、`docs/design.md`、`README.md`、`docs/readme-history/README-v0.1.0-20261020-175500.md`
- 验证与限制：容器内 `go vet`、`go test ./...`（含 `-race` 的节点测试：指纹扫描与不一致拒绝、密码与私钥认证、命令与脚本、超时、断线重连、SFTP、经 SSH 读 agent；服务器测试：节点添加 / 修改 / 删除、凭据加密、远程文件、跨节点移动、批量命令与超时、SSH 采集监控、权限、服务监控）通过；前端类型检查与 vitest 通过；Playwright e2e 在容器内用本机 sshd（127.0.0.1:2222，密码登录的普通用户）作远程服务器，桌面 9 项、手机 1 项（菜单新增“服务监控”后改为精确匹配“监控”）与多服务器 6 项通过。e2e 中修正了两处问题：批量命令在订阅前推送的输出会丢失（推送片段加序号，查询时补齐执行中的输出），以及首次打开远程“此电脑”时家目录取成 `/root`。容器没有 systemd，agent 一键安装没有在容器里实测，待在测试服务器上验证。
