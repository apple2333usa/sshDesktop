#!/usr/bin/env bash
# sshDesktop 一键安装脚本
#
#   curl -fsSL https://raw.githubusercontent.com/apple2333usa/sshDesktop/main/scripts/install.sh | sudo bash
#
# 参数（均可选，用于无人值守安装）：
#   --port <端口>        面板端口，默认 33333
#   --user <用户名>      初始管理员用户名，默认 admin（密码随机生成并打印）
#   --version <版本>     安装指定版本，例如 v0.1.0，默认最新版
#   --mirror <前缀>      GitHub 下载加速前缀，例如 https://ghproxy.example.com
#   --binary <文件>      使用本地二进制安装（离线安装或测试用），跳过下载
#   --yes                不询问，全部使用默认值或参数
set -euo pipefail

REPO="apple2333usa/sshDesktop"
BIN=/usr/local/bin/sshdesktop
CONF_DIR=/etc/sshdesktop
CONF="$CONF_DIR/config.yaml"
DATA_DIR=/var/lib/sshdesktop
LOG_DIR=/var/log/sshdesktop
UNIT=/etc/systemd/system/sshdesktop.service

PORT=""
ADMIN=""
VERSION=""
MIRROR=""
LOCAL_BIN=""
ASSUME_YES=0

red() { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }
die() {
  red "错误：$*"
  exit 1
}

while [ $# -gt 0 ]; do
  case "$1" in
    --port) PORT="${2:-}"; shift 2 ;;
    --user) ADMIN="${2:-}"; shift 2 ;;
    --version) VERSION="${2:-}"; shift 2 ;;
    --mirror) MIRROR="${2%/}"; shift 2 ;;
    --binary) LOCAL_BIN="${2:-}"; shift 2 ;;
    --yes | -y) ASSUME_YES=1; shift ;;
    -h | --help) sed -n '2,13p' "$0"; exit 0 ;;
    *) die "未知参数 $1" ;;
  esac
done

# 通过管道执行时 stdin 不是终端，从 /dev/tty 读取输入
ask() {
  local prompt="$1" default="$2" answer=""
  if [ "$ASSUME_YES" = 1 ] || ! [ -r /dev/tty ]; then
    echo "$default"
    return
  fi
  read -r -p "$prompt [$default]: " answer </dev/tty || true
  echo "${answer:-$default}"
}

# ---------- 1. 环境检查 ----------
[ "$(id -u)" = 0 ] || die "请用 root 运行（sudo bash install.sh）"
command -v systemctl >/dev/null && [ -d /run/systemd/system ] || die "需要 systemd"

case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) die "不支持的架构 $(uname -m)，只支持 amd64 和 arm64" ;;
esac

OS_ID=unknown
OS_VER=""
if [ -r /etc/os-release ]; then
  # shellcheck disable=SC1091
  . /etc/os-release
  OS_ID="${ID:-unknown}"
  OS_VER="${VERSION_ID:-}"
fi
case "$OS_ID:${OS_VER%%.*}" in
  ubuntu:22 | ubuntu:24 | ubuntu:26 | debian:12 | debian:13 | rocky:9 | rocky:10 | almalinux:9 | almalinux:10) ;;
  *) yellow "提示：$OS_ID $OS_VER 不在官方支持列表中（Ubuntu 22.04+ / Debian 12+ / Rocky、Alma 9+），继续安装但可能有兼容问题。" ;;
esac

if [ -x "$BIN" ] && [ -z "$LOCAL_BIN" ] && [ -f "$CONF" ]; then
  yellow "检测到已安装 sshDesktop（$($BIN version 2>/dev/null || echo 未知版本)）。"
  yellow "升级请运行：sshdesktop upgrade；如需重新安装，请先运行 sshdesktop uninstall。"
  exit 0
fi

for cmd in curl sha256sum; do
  command -v "$cmd" >/dev/null || [ -n "$LOCAL_BIN" ] || die "缺少 $cmd，请先安装"
done

# ---------- 2. 参数 ----------
[ -n "$PORT" ] || PORT=$(ask "面板端口" 33333)
[[ "$PORT" =~ ^[0-9]+$ ]] && [ "$PORT" -ge 1 ] && [ "$PORT" -le 65535 ] || die "端口 $PORT 无效"
if command -v ss >/dev/null && ss -lnt "( sport = :$PORT )" | grep -q LISTEN; then
  die "端口 $PORT 已被占用，请用 --port 指定其他端口"
fi
[ -n "$ADMIN" ] || ADMIN=$(ask "初始管理员用户名" admin)
[[ "$ADMIN" =~ ^[A-Za-z0-9_.-]{2,32}$ ]] || die "用户名只能包含字母、数字、下划线、点和短横线（2 到 32 位）"

# ---------- 3. 下载并校验 ----------
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
if [ -n "$LOCAL_BIN" ]; then
  [ -f "$LOCAL_BIN" ] || die "找不到 $LOCAL_BIN"
  cp "$LOCAL_BIN" "$TMP/sshdesktop"
else
  if [ -z "$VERSION" ]; then
    VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/') ||
      die "无法获取最新版本号，可用 --version 指定"
  fi
  BASE="https://github.com/$REPO/releases/download/$VERSION"
  [ -z "$MIRROR" ] || BASE="$MIRROR/$BASE"
  ASSET="sshdesktop-linux-$ARCH"
  echo "下载 $ASSET $VERSION ..."
  curl -fSL --retry 3 -o "$TMP/$ASSET" "$BASE/$ASSET" || die "下载失败，GitHub 访问不畅时可加 --mirror 参数"
  curl -fsSL --retry 3 -o "$TMP/SHA256SUMS" "$BASE/SHA256SUMS" || die "下载校验文件失败"
  (cd "$TMP" && grep " $ASSET\$" SHA256SUMS | sha256sum -c --status) || die "SHA-256 校验失败，文件可能被篡改或下载不完整"
  mv "$TMP/$ASSET" "$TMP/sshdesktop"
fi
chmod 755 "$TMP/sshdesktop"
"$TMP/sshdesktop" version >/dev/null || die "二进制无法运行（架构不匹配？）"

# ---------- 4. 安装、配置、注册服务 ----------
install -m 755 "$TMP/sshdesktop" "$BIN"
install -d -m 700 "$CONF_DIR" "$DATA_DIR"
install -d -m 750 "$LOG_DIR"
if [ ! -f "$CONF" ]; then
  cat >"$CONF" <<EOF
# sshDesktop 配置。修改后运行 systemctl restart sshdesktop 生效。
# 也可以用命令修改：sshdesktop config set port 33333
port: $PORT
dataDir: $DATA_DIR
logDir: $LOG_DIR
logLevel: info
EOF
  chmod 600 "$CONF"
fi

PASSWORD_LINE=""
if ! "$BIN" -c "$CONF" user list 2>/dev/null | grep -q "^[0-9]"; then
  OUT=$("$BIN" -c "$CONF" user add "$ADMIN" --random-password)
  PASSWORD_LINE=$(echo "$OUT" | grep '^密码：' || true)
fi

cat >"$UNIT" <<EOF
[Unit]
Description=sshDesktop 服务器管理面板
Documentation=https://github.com/$REPO
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$BIN -c $CONF server
Restart=on-failure
RestartSec=3
LimitNOFILE=65536
# 面板需要以 root 运行才能管理系统；终端与文件权限等同 root
User=root

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now sshdesktop >/dev/null 2>&1 || die "启动失败，请查看：journalctl -u sshdesktop -n 50"

for _ in $(seq 1 20); do
  curl -fsS "http://127.0.0.1:$PORT/api/v1/health" >/dev/null 2>&1 && break
  sleep 0.5
done
curl -fsS "http://127.0.0.1:$PORT/api/v1/health" >/dev/null 2>&1 || die "面板未能正常启动，请查看：journalctl -u sshdesktop -n 50"

# ---------- 5. 访问方式 ----------
# 内网地址（云服务器常见）从外面连不上，此时只给占位符
IP=$(hostname -I 2>/dev/null | awk '{print $1}')
case "$IP" in
  "" | 10.* | 192.168.* | 172.1[6-9].* | 172.2[0-9].* | 172.3[01].* | 100.6[4-9].* | 100.[7-9][0-9].* | 100.1[01][0-9].* | 100.12[0-7].*) IP="" ;;
esac
# 优先取 sshd 的生效配置；ss 里要精确匹配 "sshd"，否则会匹配到 sshdesktop 自己
SSH_PORT=$(sshd -T 2>/dev/null | awk '$1 == "port" {print $2; exit}')
[ -n "$SSH_PORT" ] || SSH_PORT=$(ss -lntp 2>/dev/null | awk '/"sshd"/ {n = split($4, a, ":"); print a[n]; exit}')
echo
green "sshDesktop 安装完成（$("$BIN" version)）"
echo
echo "  管理员账号：$ADMIN"
if [ -n "$PASSWORD_LINE" ]; then
  echo "  初始${PASSWORD_LINE}    （请登录后在“设置 > 账号”中修改）"
else
  echo "  使用已有账号登录；忘记密码可运行：sshdesktop user reset-password <用户名>"
fi
echo
echo "  面板默认只监听本机 127.0.0.1:$PORT，请通过 SSH 隧道访问。在你的电脑上运行："
echo
echo "    ssh -N -L $PORT:127.0.0.1:$PORT -p ${SSH_PORT:-22} root@${IP:-<服务器IP>}"
echo
echo "  然后在浏览器打开 http://127.0.0.1:$PORT"
echo "  （MobaXterm、Termius 等工具也可以配置同样的本地端口转发。）"
echo
echo "  需要通过域名直接访问时，在服务器上配置证书后开启 HTTPS："
echo "    sshdesktop config set domain panel.example.com"
echo "    sshdesktop config set https.cert /path/fullchain.pem"
echo "    sshdesktop config set https.key /path/privkey.pem"
echo "    sshdesktop config set https true && systemctl restart sshdesktop"
echo
echo "  常用命令：sshdesktop info | sshdesktop upgrade | sshdesktop uninstall"
