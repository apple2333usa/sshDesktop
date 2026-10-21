#!/usr/bin/env bash
# 在各支持发行版的 systemd 容器里测试安装、命令行、升级和卸载（需要 Docker，容器以 --privileged 运行）。
#
#   scripts/test-distros.sh <amd64 二进制> [发行版...]
#
# 发行版取下面 IMAGES 中的名字，默认全部。--docker 另外测试面板的一键安装 Docker（经接口，需要外网）。
# 镜像取自 mirror.gcr.io 和 quay.io，避开 Docker Hub 的拉取次数限制。
# 网络出口会替换 HTTPS 证书时，用 SDT_CA=<CA 证书文件> 把它装进测试容器。
# 宿主机是 cgroup v1 时，systemd 258 起（Ubuntu 26.04）无法在容器里启动，只能在 cgroup v2 的机器上测。
set -euo pipefail

declare -A IMAGES=(
  [ubuntu22]=mirror.gcr.io/library/ubuntu:22.04
  [ubuntu24]=mirror.gcr.io/library/ubuntu:24.04
  [ubuntu26]=mirror.gcr.io/library/ubuntu:26.04
  [debian12]=mirror.gcr.io/library/debian:12
  [debian13]=mirror.gcr.io/library/debian:13
  [rocky9]=quay.io/rockylinux/rockylinux:9
  [rocky10]=quay.io/rockylinux/rockylinux:10
  [alma9]=quay.io/almalinuxorg/almalinux:9
  [alma10]=quay.io/almalinuxorg/almalinux:10
)
ORDER=(ubuntu22 ubuntu24 ubuntu26 debian12 debian13 rocky9 rocky10 alma9 alma10)

DOCKER_TEST=0
ARGS=()
for a in "$@"; do
  case "$a" in
    --docker) DOCKER_TEST=1 ;;
    *) ARGS+=("$a") ;;
  esac
done
[ ${#ARGS[@]} -ge 1 ] || { sed -n '2,7p' "$0"; exit 2; }
BIN=$(realpath "${ARGS[0]}")
LIST=("${ARGS[@]:1}")
[ ${#LIST[@]} -gt 0 ] || LIST=("${ORDER[@]}")
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# 测试镜像：在基础镜像里装好 systemd 等再 commit（不用 docker build，避免每次向镜像仓库查询元数据）
PREP='if command -v apt-get >/dev/null; then
  apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    systemd systemd-sysv dbus curl ca-certificates iproute2 procps openssh-server cron && rm -rf /var/lib/apt/lists/*
else
  dnf install -y --allowerasing systemd procps-ng iproute openssh-server cronie curl && dnf clean all
fi'

prepare() {
  local name=$1 img=${IMAGES[$1]}
  docker image inspect "sdt-$name" >/dev/null 2>&1 && return 0
  docker image inspect "$img" >/dev/null 2>&1 || docker pull -q "$img" >/dev/null
  docker rm -f "sdt-build-$name" >/dev/null 2>&1 || true
  local ca=() pre=''
  if [ -n "${SDT_CA:-}" ]; then # RHEL 系的软件源是 HTTPS，安装前就要信任这个 CA
    ca=(-v "$(realpath "$SDT_CA"):/sdt-ca.crt:ro")
    pre='if command -v update-ca-trust >/dev/null; then cp /sdt-ca.crt /etc/pki/ca-trust/source/anchors/ && update-ca-trust; fi; '
  fi
  docker run --name "sdt-build-$name" "${ca[@]}" "$img" sh -c "$pre$PREP" >"$WORK/prep.log" 2>&1 || { tail -20 "$WORK/prep.log"; return 1; }
  docker commit -c 'CMD ["/sbin/init"]' -c 'STOPSIGNAL SIGRTMIN+3' "sdt-build-$name" "sdt-$name" >/dev/null
  docker rm "sdt-build-$name" >/dev/null
}

PASS=()
FAIL=()

# gq 代替 grep -q：读完全部输入，避免前面的命令因 SIGPIPE 在 pipefail 下被判为失败
gq() { grep -c -- "$1" >/dev/null; }

# absent 要求命令失败（! 取反的命令不会触发 set -e）
absent() {
  if "$@" >/dev/null 2>&1; then
    echo "不应成功：$*"
    return 1
  fi
}

run_one() {
  local name=$1 img=${IMAGES[$1]} c="sdt-$1"
  echo "===== $name（$img）====="
  prepare "$name"
  docker rm -f "$c" >/dev/null 2>&1 || true
  docker run -d --name "$c" --privileged --cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
    --tmpfs /run --tmpfs /run/lock "sdt-$name" >/dev/null
  local x="docker exec $c"
  for _ in $(seq 1 60); do
    case "$($x systemctl is-system-running 2>/dev/null || true)" in running | degraded) break ;; esac
    sleep 1
  done
  if [ "$(docker inspect -f '{{.State.Running}}' "$c")" != true ]; then
    echo "容器里的 systemd 没有启动（宿主机为 cgroup v1 时，systemd 258 起不再支持）："
    docker logs "$c" 2>&1 | tail -5
    return 1
  fi
  if [ -n "${SDT_CA:-}" ]; then
    docker cp "$SDT_CA" "$c:/root/sdt-ca.crt"
    $x sh -c 'if command -v update-ca-trust >/dev/null; then cp /root/sdt-ca.crt /etc/pki/ca-trust/source/anchors/ && update-ca-trust
      else cp /root/sdt-ca.crt /usr/local/share/ca-certificates/sdt-ca.crt && update-ca-certificates >/dev/null; fi'
  fi
  docker cp "$BIN" "$c:/root/sshdesktop"
  docker cp "$ROOT/scripts/install.sh" "$c:/root/install.sh"

  # 1. 安装
  $x bash /root/install.sh --binary /root/sshdesktop --yes --port 33333 --user admin >"$WORK/install.log" 2>&1 ||
    { echo "install.sh 失败"; cat "$WORK/install.log"; return 1; }
  grep -q '安装完成' "$WORK/install.log" || { cat "$WORK/install.log"; return 1; }
  local pw
  pw=$(sed -n 's/.*初始密码：\([^ ]*\).*/\1/p' "$WORK/install.log" | head -1)
  [ -n "$pw" ] || { echo "安装输出中没有初始密码"; cat "$WORK/install.log"; return 1; }
  $x systemctl is-active sshdesktop >/dev/null
  $x curl -fsS http://127.0.0.1:33333/api/v1/health | gq '"ok"'
  # 只监听本机
  $x ss -lnt | gq '127.0.0.1:33333'

  # 2. 登录与接口（体检会读取系统里的 sshd、cron、防火墙等）
  login() {
    $x curl -fsS -c /root/jar -H 'X-Requested-With: x' -H 'Content-Type: application/json' \
      -d "{\"username\":\"admin\",\"password\":\"$pw\"}" http://127.0.0.1:33333/api/v1/auth/login >/dev/null
  }
  login
  local get="$x curl -fsS -b /root/jar http://127.0.0.1:33333/api/v1"
  for p in inspect sshd firewall cron/status cron/system docker/status services; do
    $get/nodes/local/$p | gq '"code":0' || { echo "接口 $p 失败"; return 1; }
  done
  $get/nodes/local/services | gq 'sshdesktop'

  # 3. 命令行
  $x sshdesktop info | gq '33333'
  $x sshdesktop user add sdt-tmp --random-password >/dev/null
  $x sshdesktop user list | gq 'sdt-tmp'
  $x sshdesktop user delete sdt-tmp -y >/dev/null
  absent sh -c "$x sshdesktop user list | grep -q sdt-tmp"
  $x sshdesktop config set port 33334 >/dev/null
  $x systemctl restart sshdesktop
  $x sh -c 'for i in $(seq 1 30); do curl -fsS http://127.0.0.1:33334/api/v1/health >/dev/null 2>&1 && exit 0; sleep 0.5; done; exit 1'
  $x sshdesktop config set port 33333 >/dev/null
  $x grep -q '^# sshDesktop 配置' /etc/sshdesktop/config.yaml || { echo "改端口后配置文件的注释丢失"; return 1; }
  $x systemctl restart sshdesktop

  # 4. 升级（本地文件）与重复安装提示
  $x sshdesktop upgrade --file /root/sshdesktop >"$WORK/upgrade.log" 2>&1 || { cat "$WORK/upgrade.log"; return 1; }
  grep -q '升级完成' "$WORK/upgrade.log" || { cat "$WORK/upgrade.log"; return 1; }
  $x bash /root/install.sh --yes 2>&1 | gq 'sshdesktop upgrade'

  # 5. 可选：一键安装 Docker
  if [ "$DOCKER_TEST" = 1 ]; then
    login
    local tid
    tid=$($x curl -fsS -b /root/jar -H 'X-Requested-With: x' -H 'Content-Type: application/json' -d '{"method":"script"}' \
      http://127.0.0.1:33333/api/v1/nodes/local/docker/install | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
    [ -n "$tid" ] || { echo "没有返回安装任务"; return 1; }
    local st=""
    for _ in $(seq 1 180); do
      st=$($get/tasks/"$tid" 2>&1 | sed -n 's/.*"task":{[^}]*"status":"\([a-z]*\)".*/\1/p' || true)
      case "$st" in success | failed | canceled) break ;; esac
      sleep 5
    done
    if [ "$st" != success ]; then
      echo "一键安装 Docker：${st:-面板没有响应}"
      $get/tasks/"$tid" | tail -c 3000 || true
      $x journalctl -u sshdesktop --no-pager -n 30 || true
      return 1
    fi
    $get/nodes/local/docker/status | gq '"running":true'
  fi

  # 6. 卸载
  $x sshdesktop uninstall --purge -y >/dev/null
  absent $x test -e /usr/local/bin/sshdesktop
  absent $x test -e /etc/sshdesktop
  absent $x test -e /var/lib/sshdesktop
  absent $x systemctl is-active sshdesktop
  return 0
}

for d in "${LIST[@]}"; do
  [ -n "${IMAGES[$d]:-}" ] || { echo "未知发行版 $d"; exit 2; }
  # 子 shell 不能放在 if 里，否则 set -e 失效
  set +e
  (
    set -eE
    trap 'echo "出错：第 $LINENO 行"' ERR
    run_one "$d"
  )
  rc=$?
  set -e
  if [ $rc = 0 ]; then
    PASS+=("$d")
    echo "通过：$d"
  else
    FAIL+=("$d")
    echo "失败：$d"
  fi
  docker rm -f "sdt-$d" >/dev/null 2>&1 || true
done

echo
echo "通过：${PASS[*]:-无}"
echo "失败：${FAIL[*]:-无}"
[ ${#FAIL[@]} -eq 0 ]
