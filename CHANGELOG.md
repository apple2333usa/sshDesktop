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

## 2026-10-20 22:04 +08:00 · 0.2.1 · 发布 v0.2.0、测试服务器验证与修正

- 简介：在 `3493273` 打 `v0.2.0` 标签，发布流程产出面板与 agent 的 amd64 / arm64 二进制和 `SHA256SUMS`。测试服务器（Debian 13 arm64，systemd 257）从 0.1.0 升级到 0.2.0 后经 SSH 隧道跑 e2e，并在真实 systemd 下实测 agent 一键安装，发现并修正以下问题：
  - agent 一键安装后“接口没有响应”：systemd 257 把动态用户的 `RuntimeDirectory` 放进只有 root 能进入的 `/run/private/sshdesktop`，`/run/sshdesktop` 只是指向它的符号链接，非 root 的 SSH 账号连不上 `agent.sock`。改为 systemd socket 激活：新增 `sshdesktop-agent.socket`（root 创建 `/run/sshdesktop/agent.sock`，0666），服务保留 `DynamicUser`、去掉 `RuntimeDirectory`，agent 支持从 `LISTEN_FDS` 接收 socket；安装 / 卸载会清理旧版留下的符号链接和 `/run/private/sshdesktop`，面板安装脚本停用独立 agent 时一并停用其 socket 单元。
  - `sshdesktop upgrade` 在线升级必定失败：下载的临时文件权限为 0600，版本自检报 permission denied。下载校验后改为 0755。
  - 登录后马上打开的窗口会消失：读取保存的桌面状态是异步的，`restore()` 会清掉读取完成前打开的窗口。改为保留这些窗口并置于最上层，同一单例应用以已打开的为准。
  - `sshdesktop config` 不能设置面板自身 agent 接口：新增 `agent-socket` 键，`config show` 列出全部键。
  - agent 装了一半时卡片菜单没有“卸载 agent”：来源为“无 agent”时也提供卸载。
- 对应文件：`internal/agent/activation.go`、`internal/agent/activation_test.go`、`internal/agent/server.go`、`cmd/sshdesktop-agent/main.go`、`internal/module/nodes/agent.go`、`scripts/install.sh`、`cmd/sshdesktop/upgrade.go`、`cmd/sshdesktop/cli.go`、`internal/config/config.go`、`web/src/stores/desktop.ts`、`web/src/stores/desktop.test.ts`、`web/src/apps/servers/ServersApp.vue`、`web/src/i18n/zh-CN.ts`、`VERSION`、`web/package.json`、`docs/design.md`、`README.md`、`docs/readme-history/README-v0.2.0-20261020-204500.md`
- 验证与限制：0.2.0 在测试服务器上 e2e 桌面 9、手机 1、多服务器 6 项通过（远程服务器为本机 sshd，用密码登录的临时 sudo 账号）；前两轮各有 1 项因上述窗口恢复问题偶发失败。修正后容器内 `go vet`、`go test ./...`（新增 socket 激活测试）、前端类型检查与 vitest 8 项（新增恢复竞争测试）通过。用 PR 构建的 0.2.1 在测试服务器上（`upgrade --file`）实测：普通账号经 sudo 一键安装成功，socket 与服务均 active，`/run/sshdesktop` 为 root 的 0755 目录、`agent.sock` 0666，普通用户可读接口，卡片来源变为 agent 0.2.1，约 6 分钟后流量统计出现数据；覆盖安装与卸载正常，卸载后只剩空的 `/run/sshdesktop` 目录。临时账号与配置已清理，面板临时管理员 `e2e-check` 无法用命令行删除，已改为随机密码并注销会话，待在“账号”应用中删除。窗口恢复的修正未在服务器上重跑 e2e；在线升级的修正要从 0.2.1 升到下一版时才能实测。

## 2026-10-21 00:05 +08:00 · 0.3.0 · M3：容器与应用

- 简介：实现设计文档里程碑 M3。后端：
  - Docker：直接调用 Engine API（不再用官方 SDK，见设计文档 3.3），管理容器、镜像、网络、存储卷和 Compose 项目，一键安装 Docker，终端可进入容器。
  - 应用商店：兼容 1Panel 格式，支持索引、Git、本机目录三种应用源，默认使用 1Panel 索引。支持安装、启停、重建、修改参数、升级（失败自动还原）和卸载。端口可只绑定 127.0.0.1。
  - 计划任务：Shell、备份、URL、清理日志四种任务，写入 `/etc/cron.d/sshdesktop`，由新增的 `sshdesktop cron run/sync` 执行并记录输出，支持超时和防重入。系统 crontab 可以接管（停用标记、修改前备份），systemd 定时器只读列出。
  - 备份与恢复：
    - 支持本机、SFTP（经已添加的服务器）、S3、WebDAV 四种目标。
    - tar.gz 格式，加密与 OpenSSL 兼容，按目标分别执行保留策略。
    - 恢复前做快照，保留 10 份，可以撤销；目录可以恢复到其他位置。
    - 面板数据在重启时换入，备份中的配置另存为 `.from-backup`。
  - 新增权限点 `docker:*`、`appstore:*`、`cron:*`、`backup:*`，manage 级都标为等同 root。
  - 前端新增 Docker、应用商店、计划任务、备份与恢复四个应用，以及通用的任务日志窗口和日志流组件。
  - 端到端测试中发现并修正了三个问题：
    - 安装应用后的任务窗口一直显示“进行中”：接口返回的是 `{task, app}`。
    - 镜像仓库限流时安装失败：拉取失败后改为用本机已有的镜像继续启动。
    - 1Panel 的 9 个网站运行环境显示为可以安装：现在标为暂不支持，放到 M4 网站管理中。
  - CI 中发现：面板不以 root 运行时 Shell 任务进不了工作目录 `/root`、无法启动。现在进不去时改用自己的家目录。
  - 依赖其他应用的 52 个商店应用暂不支持，网站备份和安装后创建反向代理也放到 M4，已记入设计文档第 13 节。
- 对应文件：`internal/docker/`、`internal/backup/`、`internal/cronexpr/`、`internal/module/docker/`、`internal/module/appstore/`、`internal/module/cron/`、`internal/module/backup/`、`internal/module/nodes/nodes.go`、`internal/module/nodes/agent.go`、`internal/module/terminal/terminal.go`、`internal/node/node.go`、`internal/node/local_pty.go`、`internal/task/task.go`、`internal/store/models.go`、`internal/rbac/rbac.go`、`internal/api/api.go`、`internal/server/server.go`、`internal/server/m3_test.go`、`cmd/sshdesktop/cron.go`、`cmd/sshdesktop/main.go`、`cmd/sshdesktop/upgrade.go`、`go.mod`、`go.sum`、`web/src/apps/docker/`、`web/src/apps/appstore/`、`web/src/apps/cron/`、`web/src/apps/backup/`、`web/src/apps/terminal/TerminalApp.vue`、`web/src/apps/registry.ts`、`web/src/components/TaskLog.vue`、`web/src/components/LogStream.vue`、`web/src/components/icons.ts`、`web/src/api/types.ts`、`web/src/api/http.ts`、`web/src/i18n/zh-CN.ts`、`web/e2e/m3.spec.ts`、`web/e2e/shots.spec.ts`、`VERSION`、`web/package.json`、`web/package-lock.json`、`docs/design.md`、`README.md`、`docs/readme-history/README-v0.2.1-20261020-220400.md`
- 验证与限制：
  - 在云端容器（root，Docker 29.9.1，Compose 5.6.0）中运行：
    - `go vet`、`go test ./...` 通过。新增测试覆盖 cron 表达式、crontab 解析与改写、任务执行（超时、跳过、清理、保留条数）、应用索引解析、表单校验、端口改写，以及备份的加密往返、OpenSSL 互通、排除规则、路径穿越拒绝、本机 / WebDAV 目标、保留策略、恢复与快照撤销、面板备份与重启换入。集成测试在真实 Docker 上跑完容器、Compose、本机源安装 / 升级 / 卸载应用、应用备份与恢复，并用进程内 SSH 服务器测试 SFTP 目标和计划任务。
    - S3 目标用 rclone 模拟的 S3 服务测试通过，真实 1Panel 索引（264 个应用、17 个分类，61 个暂不支持）解析通过。
    - 前端类型检查、构建和 vitest 8 项通过。
    - Playwright e2e 通过：桌面 9 项、手机 1 项、M3 新增 4 项（`SD_NO_SYSTEMD=1`）。多服务器用例这次没有跑。
    - 计划任务测试另以非 root 用户运行通过。
  - 还没有在测试服务器上验证 Docker 一键安装、真实应用安装、S3 / WebDAV 云存储、系统 cron 定时触发和 systemd 下的面板恢复后自动重启。

## 2026-10-21 09:25 +08:00 · 0.3.1 · 0.3.0 服务器实测与面板恢复的两处修正

- 简介：发布 0.3.0 并在测试服务器上实测 M3，修正实测中发现的两个问题：
  - 面板数据恢复后，systemd 重启面板时日志里误报“重启面板失败”（`err="signal: terminated"`）。原因是重启任务排队后 systemd 立即停止服务，同一 cgroup 里还没退出的 `systemctl` 也被 SIGTERM 结束。现在把这种情况视为正常。
  - 面板数据恢复后，恢复前快照、所恢复的备份记录、恢复任务都看不到了（文件还在，但记录只在被换下的数据库里），备份之后的审计日志也随之回到备份时的状态，与“审计日志不能删除”相违背。现在换库时把这些信息写入 `restore-carry.json`，打开新数据库后从 `restore-old-<时间>/` 的原数据库带过来：快照记录、所恢复的记录（目标仍是同一个时）、恢复任务及其输出，以及新数据库最后一条之后的审计日志。
- 对应文件：`internal/server/server.go`、`internal/backup/restore.go`、`internal/backup/backup_test.go`、`internal/module/backup/backup.go`、`cmd/sshdesktop/main.go`、`docs/design.md`、`VERSION`、`web/package.json`、`web/package-lock.json`、`README.md`、`docs/readme-history/README-v0.3.0-20261021-000500.md`
- 验证与限制：
  - 0.3.0 的发布与实测（测试服务器 Debian 13 aarch64，Docker 29.8.0 / Compose 5.5.1，从本地电脑经 SSH 操作）：
    - 推送 v0.3.0 标签后 Release 工作流约 3 分钟完成，5 个文件齐全。
    - `sshdesktop upgrade` 在线升级 0.2.1 → 0.3.0 约 10 秒，服务正常、日志无错误，0.2.1 对在线升级的修正得到验证。这台服务器没有装面板自身的 agent socket，未检查。
    - 通过接口实测：
      - Docker：服务器原本装有 Docker，一键安装未测。容器创建 / 停止 / 删除、Compose 项目创建 / 删除正常。
      - 应用商店：同步 1Panel 默认源 264 个应用；安装 memos 0.31.0（只监听本机）6 秒完成，访问返回 200；停止和带数据卸载正常。
      - 备份：本机、WebDAV、S3（后两者用 rclone 容器模拟）加密备份与恢复内容一致，排除规则生效，恢复前快照生成，缺少确认密码时被拒绝。SFTP 需要临时建 Linux 账号作为节点，这次没有测（集成测试中已用进程内 SSH 服务器覆盖）。
      - 计划任务：每分钟一次的 Shell 任务 2.5 分钟内由系统 cron 自动执行 3 次，均成功；删除后 `/etc/cron.d/sshdesktop` 中的条目一并移除。
      - 面板恢复：备份后的改动在恢复后消失，systemd 自动重启面板，停机约 0.1 秒；恢复后需要重新登录。上面两个问题就是在这一步发现的。
    - 测试用的容器、应用、备份、计划任务和临时管理员（用 sqlite3 删除）都已清理；0.2.1 遗留的 `e2e-check` 已不在。服务器上留下了测试拉取的镜像（rclone、memos、alpine）和为测试安装的 jq、sqlite3。
  - 0.3.1 的修正：容器内 `go vet`、`go test ./...` 通过。面板恢复测试新增了对带入快照记录、备份记录、恢复任务、审计日志和不重复带入的检查。systemd 重启时的误报修正需要在服务器上实测。

## 2026-10-21 09:29 +08:00 · 0.3.1 · 0.3.1 服务器实测

- 简介：发布 0.3.1，在测试服务器上复测面板恢复的两处修正，并补测 SFTP 备份目标。只补记验证结果，代码没有改动。
- 对应文件：`CHANGELOG.md`
- 验证与限制：
  - 推送 v0.3.1 标签后，Release 工作流约 2.5 分钟完成，5 个文件齐全。
  - `sshdesktop upgrade` 在线升级 0.3.0 → 0.3.1 成功，日志中没有错误和警告。
  - 面板恢复（带快照）：
    - systemd 自动重启，停机约 0.2 秒，日志里不再出现“重启面板失败”。
    - 启动时带过 2 条备份记录（恢复前快照和被恢复的记录）、1 个恢复任务（8 行输出完整）和 4 条备份之后的审计日志（含恢复操作本身），`restore-carry.json` 处理后已删除。
    - 备份之后的改动按预期消失。
  - 再从恢复前快照恢复（撤销）同样正常：改动回来了，记录和审计日志也都带过来了。
  - SFTP 目标（经用户同意，临时建了无 sudo 的 Linux 账号，用一次性密钥作为节点连到本机 sshd）：
    - 添加节点时核对主机指纹一致，节点在线。
    - 加密目录备份写到 SFTP 一侧，文件权限 600，面板里能列出目标文件。
    - 恢复到其他目录后内容一致，排除规则生效。
    - 保留 1 份时第二次备份删除了旧文件。
    - 测试后节点、凭据和主机密钥记录已删除，Linux 账号用 `userdel -r` 删除。
  - 测试数据、`restore-old-*` 目录、恢复出的 `config.yaml.from-backup` 和临时管理员都已清理，用户列表只剩 admin。

## 2026-10-21 14:42 +08:00 · 0.4.0 · M4：网站与安全

- 简介：实现设计文档里程碑 M4，P0 的四个里程碑至此全部完成。
  - 网站：可以接管宿主机已有的 Nginx / OpenResty（只加一个 include，原有站点不变），或一键安装面板管理的 OpenResty 容器（`openresty/openresty:1.31.1.1-alpine`，主机网络）。
    - 支持反向代理和静态网站，选项有多域名、HTTPS、强制跳转、HTTP/2、HSTS、WebSocket 和附加配置。
    - 保存前用 `nginx -t` 校验，失败时还原。可以查看访问日志和错误日志。
    - 结构简单的已有站点可以接管为面板站点（原文件备份，可以还原），其他的直接编辑原文件。
    - 新增 Nginx 配置解析器 `internal/nginx`。
  - 证书：基于 lego v5。
    - CA 支持 Let's Encrypt（含测试环境）、ZeroSSL 和自定义 ACME 地址。
    - 验证方式支持 HTTP，或通过 Cloudflare、阿里云、腾讯云 DNSPod 做 DNS 验证（可签通配符）。
    - 到期前 30 天自动续期，也可以上传自有证书。
    - 识别 certbot 和 acme.sh 的证书，certbot 证书可以迁移为面板续期。
    - 下载私钥需要二级确认。
  - 面板访问方式：在设置中切换仅本机 / 公网 HTTPS（域名 + 证书模块中的证书）或修改端口。
    - 面板热切换监听，新地址带一次性令牌，在那里确认（不需要先登录），60 秒内没确认就恢复原配置。
    - 证书续期后面板自动换用新证书。
  - 防火墙：管理 ufw / firewalld 的全部规则。
    - 启用防火墙、删除规则、添加拒绝规则走 60 秒确认，到时自动撤销；放行端口直接生效。
    - SSH 和对外开放时的面板端口始终放行。
    - 没有防火墙时可以一键安装，只有手写 iptables / nftables 规则时只读展示。
    - 通用的“修改后限时确认”放在新增的 `internal/pending`，快照写入磁盘，面板在等待期间重启也会回滚。
  - SSH 设置：
    - 端口、密码登录、公钥登录和 root 登录写入 `sshd_config.d/00-sshdesktop.conf`。
    - 保存前用 `sshd -t` 校验，再进入 60 秒确认；改端口时新旧端口同时监听，并自动放行防火墙。
    - 可以管理各账号的授权密钥，查看登录记录和失败最多的来源。没有 systemd 时用 SIGHUP 重新加载。
  - 环境体检：
    - 检查 Web 服务、证书、Docker、防火墙、计划任务、SSH、端口和系统，每项可以直接打开对应应用。
    - 管理员第一次登录时自动打开并运行。
  - 补上 M3 留下的两项：
    - 网站备份与恢复：配置、证书和静态文件，恢复时证书导入证书模块；面板备份也包含证书目录。
    - 应用商店中为已安装应用一键创建反向代理网站，已安装列表显示关联的网站。
  - 新增权限点 `website:*`、`firewall:*`、`sshd:*`，其中 `website:manage`、`sshd:manage` 标为等同 root。新装的“运维”“只读”角色模板包含相应权限；已有的角色不会自动获得，需要在“账号与权限”中手动勾选。
  - 前端：
    - 新增网站（站点、证书、已有站点）、防火墙、SSH 设置、环境体检四个应用，以及通用的“等待确认”提示条。
    - 设置中加入“面板访问方式”，代替原来的命令行说明。
    - 备份计划可以选择网站。
    - 新增文案键完整性和可编译性的单元测试。
  - 实测中发现并修正：
    - 网站表的几个列名与 GORM 默认推断不一致，现在显式指定。
    - 证书 HTTP 验证在 Nginx 重新加载完成前就开始，偶尔 404。现在先自己访问一次验证文件，确认可以访问再提交。
    - 新地址没有登录状态，无法确认面板访问方式，因此改为一次性令牌。
    - 解析器遇到 Lua 块中的引号会出错，改为惰性词法分析。
    - 在未启用 HTTPS 的站点上打开 HTTPS 时，“强制 HTTPS”和 HTTP/2 默认是关闭的，现在默认打开。
    - 授权密钥提示文案中的 `@` 让 vue-i18n 编译失败，导致“添加密钥”弹窗打不开。
    - 备份计划表单打开后再加载应用和网站列表，会打断正在进行的选择，现在先加载再打开。
- 对应文件：`internal/nginx/`、`internal/pending/`、`internal/module/website/`、`internal/module/cert/`、`internal/module/firewall/`、`internal/module/sshd/`、`internal/module/inspect/`、`internal/server/panel.go`、`internal/server/server.go`、`internal/config/config.go`、`internal/store/models.go`、`internal/rbac/rbac.go`、`internal/api/api.go`、`internal/backup/archive.go`、`internal/backup/engine.go`、`internal/backup/restore.go`、`internal/backup/backup_test.go`、`internal/module/backup/backup.go`、`internal/module/appstore/install.go`、`go.mod`、`go.sum`、`web/src/apps/websites/`、`web/src/apps/firewall/`、`web/src/apps/sshd/`、`web/src/apps/inspect/`、`web/src/apps/settings/PanelAccess.vue`、`web/src/apps/settings/SettingsApp.vue`、`web/src/apps/appstore/InstalledTab.vue`、`web/src/apps/backup/BackupApp.vue`、`web/src/apps/backup/RestoreDialog.vue`、`web/src/apps/backup/types.ts`、`web/src/apps/registry.ts`、`web/src/components/PendingBar.vue`、`web/src/components/AccessConfirm.vue`、`web/src/components/icons.ts`、`web/src/shells/common.ts`、`web/src/shells/desktop/DesktopShell.vue`、`web/src/shells/admin/AdminShell.vue`、`web/src/stores/notify.ts`、`web/src/App.vue`、`web/src/main.ts`、`web/src/api/types.ts`、`web/src/i18n/zh-CN.ts`、`web/src/i18n/keys.test.ts`、`web/e2e/m4.spec.ts`、`VERSION`、`web/package.json`、`web/package-lock.json`、`docs/design.md`、`README.md`、`docs/readme-history/README-v0.3.1-20261021-092500.md`
- 验证与限制：
  - 在云端容器（root，Ubuntu 24.04，nginx 1.24，ufw，firewalld，OpenSSH 9.6，Docker 29.9.1，用 pebble 作为本地 ACME 服务器）中运行：
    - `go vet`、`go test ./...` 通过。新增测试覆盖：
      - Nginx 解析
      - 限时确认（确认、撤销、超时、重启后恢复）
      - 站点配置生成（装有 nginx 时用 `nginx -t` 校验）
      - 防火墙规则解析与安全判断、sshd 配置改写
      - 证书导入去重、网站备份与恢复
    - 新增模块的测试另以非 root 用户运行也通过。
    - 全流程手工实测：
      - 两种 Web 服务方式都试过。
      - pebble 签发证书，HTTPS、HTTP/2 和 HSTS 生效。
      - 面板访问方式的切换、超时回滚和重启回滚。
      - ufw 与 firewalld 的规则和确认流程。
      - SSH 改端口与回滚、证书上传与 certbot 迁移、体检。
    - 前端类型检查、构建和 vitest 11 项通过。
    - Playwright e2e 在全新面板上运行（`SD_M3=1 SD_M4=1 SD_NO_SYSTEMD=1`）：
      - 桌面 9 项、手机 1 项、M3 4 项、M4 新增 5 项通过。
      - M4 的 5 项是：建站并启用 HTTPS、防火墙规则、SSH 改端口后撤销与授权密钥、体检、切换面板端口后在新地址确认再切回。
      - M3 的应用商店用例有一次因同步按钮与自动同步冲突而失败，单独重跑通过。它与本次改动无关，没有处理。
      - 多服务器用例这次没有跑。
  - DNS 验证没有用真实 DNS 服务商测试（按约定跳过）。还没有在测试服务器上实测，见下一条记录。

## 2026-10-21 16:26 +08:00 · 0.4.0 · 0.4.0 服务器实测

- 简介：发布 0.4.0，在测试服务器（Debian 13 arm64，已装 1Panel 和 fail2ban，没有 Nginx、ufw、firewalld）上实测 M4。只补记验证结果，发现的问题在 0.4.1 修复。
- 对应文件：`CHANGELOG.md`
- 验证与限制：
  - PR #11 的 CI 通过后合并，推送 v0.4.0 标签，Release 的 5 个文件齐全；`sshdesktop upgrade` 从 0.3.1 升级到 0.4.0 成功，日志没有新的错误。
  - 外网访问 TCP 30080 不通，数据包没有到达服务器（云服务商安全组），因此网站、证书、SSH、面板访问方式都在服务器本机测试，证书用上传的自签证书，DNS 验证按约定跳过。
  - 网站：安装面板管理的 OpenResty 容器，建反向代理站点，启用 HTTPS 后 HTTP/2 生效；网站备份与恢复（证书一并恢复）正常。
  - 防火墙：没有 ufw / firewalld，只读展示 iptables 规则。
  - SSH：改端口后撤销、超时自动回滚、增删授权密钥，全程 5522 端口没有断开。
  - 面板访问方式：换端口并在新地址确认、超时回滚、等待期间重启后回滚、切换到公网 HTTPS 再切回仅本机。
  - 发现的问题：保存站点后立即访问偶尔仍是旧配置；接口修改站点不带启用状态会把站点停用；OpenResty 容器启动时警告文件句柄不足；防火墙页面在 1Panel 管理 iptables 的机器上建议安装 ufw；切回仅本机后域名配置残留；没有命令行删除账号的办法；撤销和超时回滚的日志措辞相同。
  - 测试数据已清理：站点、证书、备份、OpenResty（容器、镜像、目录、设置）、临时管理员和服务器上的临时目录都已删除，残留的域名配置用 `sshdesktop config unset domain` 清除。SSH 配置、`authorized_keys` 和 iptables 与测试前一致，1Panel 和 fail2ban 没有改动。

## 2026-10-21 16:34 +08:00 · 0.4.1 · 0.4.0 实测问题修复

- 简介：修复 0.4.0 在测试服务器上发现的问题。
  - Nginx 重新加载只是向主进程发信号，命令返回时新工作进程可能还没起来。现在等到工作进程全部换新（最多 10 秒）再返回，保存站点后立即访问就是新配置。
  - 修改站点时保留原来的启用状态，启用 / 停用只通过专门的接口。
  - OpenResty 容器的文件句柄上限提高到 65535，消除 `worker_connections` 超过上限的警告。
  - 面板访问方式切回仅本机时清除域名和 HTTPS 配置；切换窗口提示原地址会在确认后约 2 秒关闭。
  - 防火墙识别 1Panel（`1PANEL_*`）和 fail2ban（`f2b-*`）的规则链：由 1Panel 管理时不再建议安装 ufw，并隐藏安装按钮；已有 ufw / firewalld 时提示只在一个面板中修改规则。体检识别已安装的 1Panel、宝塔面板并说明共存注意事项。
  - 网站或面板备份不加密时，表单提示备份包含证书私钥，建议加密。
  - 新增 `sshdesktop user delete <用户名>`：注销会话、删除桌面布局后删除账号，不能删除最后一个启用中的管理员，默认要输入 yes 确认（`-y` 跳过）。
  - 用户撤销修改时日志记为“修改已撤销”，与超时回滚区分。
  - 备份计划表单在取回列表后再决定是否默认选中唯一的备份目标；e2e 中 M3 备份用例因此偶发失败（默认选中后再点一次反而取消），用例改为未选中时才选择。M4 网站用例删除站点后改为按正文判断不再代理到面板（默认站点也可能返回 200）。
- 对应文件：`internal/nginx/runtime.go`、`internal/nginx/workers.go`、`internal/nginx/workers_test.go`、`internal/module/website/website.go`、`internal/module/website/container.go`、`internal/server/panel.go`、`internal/pending/pending.go`、`internal/module/firewall/firewall.go`、`internal/module/firewall/firewall_test.go`、`internal/module/inspect/inspect.go`、`internal/module/inspect/inspect_test.go`、`cmd/sshdesktop/cli.go`、`cmd/sshdesktop/cli_test.go`、`web/src/apps/settings/PanelAccess.vue`、`web/src/apps/firewall/FirewallApp.vue`、`web/src/apps/backup/BackupApp.vue`、`web/src/api/types.ts`、`web/src/i18n/zh-CN.ts`、`web/e2e/m3.spec.ts`、`web/e2e/m4.spec.ts`、`VERSION`、`web/package.json`、`web/package-lock.json`、`docs/design.md`、`README.md`、`docs/readme-history/README-v0.4.0-20261021-143400.md`
- 验证与限制：
  - 云端容器中 `go vet`、`go test ./...` 通过。新增测试：重新加载后立即请求得到新配置（临时 nginx 连续重新加载 5 次，去掉等待时失败）、1Panel / fail2ban 规则链识别、其他面板识别、命令行删除账号（含保护最后一个管理员）。
  - 前端类型检查、vitest 11 项通过；OpenResty 容器的 Compose 文件用 `docker compose config` 校验通过。
  - Playwright e2e（`SD_M3=1 SD_M4=1 SD_NO_SYSTEMD=1`）在全新面板上 19 项全部通过，多服务器用例没有跑。

## 2026-10-21 16:56 +08:00 · 0.4.1 · 0.4.1 服务器复测

- 简介：发布 0.4.1，在测试服务器上复测 0.4.0 实测发现的问题。只补记验证结果，代码没有改动。
- 对应文件：`CHANGELOG.md`
- 验证与限制：
  - PR #12 的 CI 通过后合并，推送 v0.4.1 标签，Release 的 5 个文件齐全；`sshdesktop upgrade` 从 0.4.0 升级到 0.4.1 成功，之后没有新的警告和错误。
  - 防火墙：识别出 1Panel 和 fail2ban 的规则链，提示继续在 1Panel 中管理防火墙，不再建议安装 ufw。体检的防火墙项显示“由 1Panel 管理”，系统项显示“已安装 1Panel”并给出共存提示。
  - 网站（OpenResty 容器）：容器内文件句柄上限 65535，日志中没有 `worker_connections` 警告；连续 5 轮新建站点后立即访问都返回 200，删除后立即访问不再是面板页面；修改站点时不带 `enabled`，站点仍保持启用。
  - 面板访问方式：切换到公网 HTTPS 并确认，再切回仅本机后，`config.yaml` 与测试前逐字节一致，没有残留域名。
  - SSH 改端口后撤销，日志记为“修改已撤销”，5522 端口始终可用。
  - `sshdesktop user delete`：不存在的账号报错并以 1 退出；删除临时管理员后其会话立即失效。
  - 测试数据已清理：站点、证书、OpenResty（容器、镜像、目录、设置）、临时管理员和服务器上的临时目录都已删除，空的 `/var/www`、`/var/lib/sshdesktop/certs` 一并删除。SSH 配置、`authorized_keys`、`config.yaml` 与测试前一致，iptables 规则（去掉计数后）一致，1Panel 和 fail2ban 没有改动。

## 2026-10-21 20:20 +08:00 · 0.5.0 · M5：稳定化

- 简介：M5 做发布 v1.0.0 前的稳定化：补测试、补齐设计中尚未实现的面板内升级，并修复测试中发现的问题。本版本先在测试服务器上实测并由用户验收，通过后发布 v1.0.0。
  - 面板内一键升级：“设置 → 关于”显示当前版本和 GitHub 上的最新版本、更新说明，二级确认后升级。后端用 `systemd-run` 在独立的 `sshdesktop-upgrade` 单元里执行 `sshdesktop upgrade`，流程与命令行相同（下载 → 校验 → 备份 → 替换 → 重启 → 健康检查，失败自动回滚）。这样重启面板时，升级进程不会随面板的 cgroup 一起被结束。前端轮询健康检查，版本变化后刷新页面；升级没有完成时显示升级日志（`<数据目录>/upgrade.log`）。面板不是由 systemd 运行时提示改用命令行。
  - 管理员登录后，如果有新版本就提醒一次（每个版本、每个浏览器只提醒一次）。最新版本的查询结果缓存 6 小时。GitHub 接口限流时，改从发布页的重定向取版本号；命令行 `sshdesktop upgrade` 也用同样的办法。
  - 升级备份（`upgrade-backup/`）只保留最近 3 次的旧二进制和数据库，此前每次都会累积。
  - `sshdesktop config set/unset` 以及面板访问方式切换时，保留 `config.yaml` 里的注释和键顺序，只改动相应的值。
  - 安装脚本：结尾读取 SSH 端口时，`awk ... exit` 提前退出会让 `sshd -T` 收到 SIGPIPE，在 `pipefail` 下整个脚本中止，导致不打印初始管理员密码（面板已经装好并在运行）。这个问题偶发，取决于时序，在 Ubuntu 22.04 容器里能稳定复现。现在 awk 读完全部输入；获取最新版本号时也去掉了 `grep -m1` 的同类问题，并在版本号为空时报错。
  - 一键安装 Docker：Docker 官方脚本在 Rocky Linux 上改用专属软件源，目前那里没有 docker-ce，导致安装失败。在 RHEL 系上官方脚本失败时，改用 Docker 官方的 CentOS 软件源安装。
  - 应用商店：启动时会自动同步应用源；在同步进行中手动点“同步”会直接报“正在同步中”，这样刚添加的源不会被同步（e2e 偶发失败的原因）。现在手动同步会等正在进行的同步结束后再同步一次。
  - 新增发行版集成测试 `scripts/test-distros.sh`，并加入 CI，9 个发行版并行跑：
    - 在 Ubuntu 22.04 / 24.04 / 26.04、Debian 12 / 13、Rocky / Alma 9 / 10 的 systemd 容器里依次测试：安装脚本（`--binary`）、只监听本机、登录与各模块接口。
    - 命令行：增删账号、改端口并重启、配置注释保留。
    - 本地文件升级、重复运行安装脚本时的提示、一键安装 Docker（`--docker`），最后 `uninstall --purge`。
  - 依赖：quic-go 0.59.0 → 0.59.1（修复 GO-2026-5676，经 gin 间接引入）、golang.org/x/net 0.61.0、vite 8.3.5。govulncheck 不再报告受影响的调用；`npm audit` 为 0。
  - e2e：多服务器用例中“每月 15 日重置”的断言已经过时（没装 agent 的服务器不显示卡片），改为检查周期从 15 日开始；新增 `e2e/m5.spec.ts`（设置中的升级卡片）。
- 对应文件：`internal/module/system/upgrade.go`、`internal/module/system/upgrade_test.go`、`internal/module/system/system.go`、`cmd/sshdesktop/upgrade.go`、`cmd/sshdesktop/cli_test.go`、`internal/config/config.go`、`internal/config/config_test.go`、`internal/module/appstore/appstore.go`、`internal/module/appstore/appstore_test.go`、`internal/module/docker/install.go`、`scripts/install.sh`、`scripts/test-distros.sh`、`.github/workflows/ci.yml`、`web/src/apps/settings/PanelUpgrade.vue`、`web/src/apps/settings/SettingsApp.vue`、`web/src/shells/common.ts`、`web/src/api/types.ts`、`web/src/i18n/zh-CN.ts`、`web/e2e/servers.spec.ts`、`web/e2e/m5.spec.ts`、`go.mod`、`go.sum`、`VERSION`、`web/package.json`、`web/package-lock.json`、`docs/design.md`、`README.md`、`docs/readme-history/README-v0.4.1-20261021-163400.md`
- 验证与限制：
  - 云端容器中 `go vet`、`go test ./...` 通过。新增的测试：
    - 版本比较、最新版本缓存，以及接口失败时改从重定向取版本号。
    - 升级备份清理、配置注释保留。
    - 同步进行中再次手动同步时会等待。
  - 前端类型检查和 vitest 11 项通过。
  - Playwright e2e（`SD_M3=1 SD_M4=1 SD_NO_SYSTEMD=1`，多服务器用例用本机 sshd）27 项全部通过。M3 的 Docker 用例第一次因 Docker Hub 限流（429）拉不到 `alpine:3`，换镜像源预先拉取后通过。
  - 面板内升级：在 Debian 13 systemd 容器里装了版本号标为 0.4.0 的本版构建，经接口一键升级到真实发布的 v0.4.1，升级日志完整，健康检查通过，备份目录生成。
  - 发行版集成测试（加 `--docker`）：Ubuntu 22.04 / 24.04、Debian 12 / 13、Rocky 9 / 10、Alma 9 / 10 全部通过（Docker 29.9.2；Rocky / Alma 走 CentOS 软件源的回退安装）。云端宿主机是 cgroup v1，Ubuntu 26.04 的 systemd 无法在容器里启动，只能在 CI 中测。
  - 还没有在测试服务器上实测。

## 2026-10-21 20:56 +08:00 · 1.0.0 · v0.5.0 服务器实测、浏览器验收，发布 v1.0.0

- 简介：v0.5.0 在两台测试服务器上实测并完成浏览器验收，没有发现需要修改代码的问题；版本号改为 1.0.0，作为正式版发布，并用面板内一键升级把测试服务器升级到 v1.0.0（结果见下一条）。
- 对应文件：`VERSION`、`web/package.json`、`web/package-lock.json`、`README.md`、`docs/design.md`、`docs/readme-history/README-v0.5.0-20261021-200500.md`
- 验证与限制：
  - 发布：PR #14 的 CI 全部通过，含 9 个发行版集成测试（Ubuntu 26.04 在 CI 的 cgroup v2 环境中通过）。`v0.5.0` 标签发布出 5 个文件。
  - 服务器 A（140.245.62.206，Debian 13 arm64）：`sshdesktop upgrade --version v0.5.0` 从 0.4.1 升级成功，服务、健康检查、`/run/sshdesktop/agent.sock` 正常，日志无报错。本次升级由旧版 0.4.1 的命令执行，所以没有清理旧备份（`upgrade-backup/` 现有 8 份）；清理从 0.5.0 起生效，在升级到 1.0.0 时验证。`/system/upgrade` 接口返回当前 0.5.0、最新 v0.5.0、可在面板内升级。
  - 节点 B（35.212.228.74，Debian 13 amd64）：用临时密钥经接口添加，主机指纹与 B 上的文件一致，状态在线。
  - 浏览器验收（经 SSH 隧道，临时管理员）：登录、桌面；监控（本机与 B）；终端（本机与 B，在其他服务器上新建标签页）；文件（新建文件夹、移入回收站，二级确认）；批量命令（两台都返回、退出码 0）；环境体检；在 B 上一键安装 agent（显示 agent 0.5.0）后再卸载；设置 → 关于的升级卡片（当前 0.5.0、已是最新、检查更新刷新时间）；应用商店同步（294 个应用）；操作审计记录了以上操作。浏览器控制台无报错。
