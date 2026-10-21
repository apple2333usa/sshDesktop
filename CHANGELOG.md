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
