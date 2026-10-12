#!/usr/bin/env bash
#
# DoriyaMedia 一键安装 / 升级脚本
# 适用：Debian 12/13、Ubuntu 22.04/24.04 及其衍生版（x86_64 / arm64 / armv7）
#
#   curl -fsSL https://raw.githubusercontent.com/nekofinley/DoriyaMedia/main/scripts/install.sh | sudo bash
#
# 直连 GitHub 不通时会自动回退到加速镜像，一般不用手动指定；
# 想固定某个源就加：
#   sudo bash install.sh --base-url https://ghfast.top
#
# 装好后会同时安装 dm 管理命令（类似宝塔的 bt）：dm 进菜单，dm update 更新。
#
set -euo pipefail

REPO="nekofinley/DoriyaMedia"
SERVICE="doriyamedia"
BIN_PATH="/usr/local/bin/${SERVICE}"
DM_PATH="/usr/local/bin/dm"
DATA_DIR="/var/lib/doriyamedia"
CONFIG_PATH="${DATA_DIR}/config.json"
UNIT_PATH="/etc/systemd/system/${SERVICE}.service"
SERVICE_USER="doriyamedia"
PORT="5666"

# 官方地址。镜像是「前缀」形式：<前缀>/https://github.com/...
GH="https://github.com"
API="https://api.github.com"
if [ -n "${DORIYAMEDIA_API_BASE:-}" ]; then
  API="${DORIYAMEDIA_API_BASE}"
fi

# 按顺序尝试的下载源前缀；空串 = 直连官方。第一个成功的就是它。
MIRROR_PREFIXES=("" "https://ghfast.top" "https://ghproxy.net" "https://gh-proxy.com")
# --base-url / DORIYAMEDIA_GITHUB_BASE：固定用这个前缀，不再回退
BASE_URL_OPT="${DORIYAMEDIA_GITHUB_BASE:-}"

VERSION=""
INSTALL_FFMPEG=1
AUTOSTART=1
UNINSTALL=0
PURGE=0

# ---------------------------------------------------------------- 输出

if [ -t 1 ] && [ "${NO_COLOR:-}" = "" ]; then
  C_R=$'\033[0m'; C_B=$'\033[1m'; C_G=$'\033[32m'; C_Y=$'\033[33m'; C_E=$'\033[31m'; C_C=$'\033[36m'
else
  C_R=""; C_B=""; C_G=""; C_Y=""; C_E=""; C_C=""
fi

log()  { printf '%s==>%s %s\n' "$C_C$C_B" "$C_R" "$*"; }
ok()   { printf '%s  ✓%s %s\n' "$C_G" "$C_R" "$*"; }
warn() { printf '%s  !%s %s\n' "$C_Y" "$C_R" "$*" >&2; }
die()  { printf '%s  错误:%s %s\n' "$C_E$C_B" "$C_R" "$*" >&2; exit 1; }

usage() {
  cat <<EOF
${C_B}DoriyaMedia 安装脚本${C_R}

用法: sudo bash install.sh [选项]

选项:
  -v, --version TAG     安装指定版本（默认最新 Release，例如 v0.1.0）
  -p, --port N          监听端口（默认 ${PORT}）
      --data-dir PATH   数据目录（默认 ${DATA_DIR}）
      --user NAME       运行用户（默认 ${SERVICE_USER}）
      --base-url URL    固定下载源前缀，不再自动回退
                        默认顺序: 官方 GitHub → ghfast.top → ghproxy.net → gh-proxy.com
                        例: --base-url https://ghfast.top
      --no-ffmpeg       不自动安装 ffmpeg
      --no-start        只安装，不启动/不设开机自启
      --uninstall       卸载（保留数据目录）
      --purge           卸载并删除数据目录
  -h, --help            显示本帮助

环境变量:
  DORIYAMEDIA_GITHUB_BASE  同 --base-url
  DORIYAMEDIA_API_BASE     覆盖 GitHub API 地址
  NO_COLOR=1               关闭彩色输出

装好后在任意终端输入 ${C_B}dm${C_R} 即可进入管理菜单（更新/启停/日志/卸载）。
EOF
}

# ---------------------------------------------------------------- 参数

while [ $# -gt 0 ]; do
  case "$1" in
    -v|--version)  VERSION="${2:-}"; shift 2 ;;
    -p|--port)     PORT="${2:-}"; shift 2 ;;
    --data-dir)    DATA_DIR="${2:-}"; shift 2 ;;
    --user)        SERVICE_USER="${2:-}"; shift 2 ;;
    --base-url)    BASE_URL_OPT="${2:-}"; shift 2 ;;
    --no-ffmpeg)   INSTALL_FFMPEG=0; shift ;;
    --no-start)    AUTOSTART=0; shift ;;
    --uninstall)   UNINSTALL=1; shift ;;
    --purge)       UNINSTALL=1; PURGE=1; shift ;;
    -h|--help)     usage; exit 0 ;;
    *)             die "未知参数: $1（用 --help 查看用法）" ;;
  esac
done

CONFIG_PATH="${DATA_DIR}/config.json"

# ---------------------------------------------------------------- 基础检查

[ "$(id -u)" -eq 0 ] || die "需要 root 权限，请用 sudo 运行"

case "$(uname -s)" in
  Linux) : ;;
  *)     die "这个脚本只支持 Linux；$(uname -s) 请手动下载 Release 里的压缩包" ;;
esac

case "$(uname -m)" in
  x86_64|amd64)   ARCH="amd64" ;;
  aarch64|arm64)  ARCH="arm64" ;;
  armv7l|armv7)   ARCH="armv7" ;;
  *)              die "不支持的 CPU 架构: $(uname -m)" ;;
esac

if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  HAVE_SYSTEMD=1
else
  HAVE_SYSTEMD=0
fi

command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 \
  || die "需要 curl 或 wget，请先安装其中一个"

fetch_stdout() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 2 --connect-timeout 10 --max-time 60 "$1"
  else
    wget -qO- --tries=2 --timeout=10 "$1"
  fi
}

fetch_file() {
  if command -v curl >/dev/null 2>&1; then
    curl -fL --retry 1 --connect-timeout 8 --max-time 900 --progress-bar -o "$2" "$1"
  else
    wget -q --show-progress --tries=2 --timeout=8 -O "$2" "$1"
  fi
}

# ------------------------------------------------------- 下载源（含镜像回退）

# 空前缀 -> 原样返回官方 URL；非空前缀 -> <前缀>/<官方 URL>
join_url() {
  if [ -n "$1" ]; then printf '%s/%s' "${1%/}" "$2"; else printf '%s' "$2"; fi
}

host_of() {
  printf '%s' "$1" | sed -e 's|^[A-Za-z][A-Za-z0-9+.-]*://||' -e 's|/.*$||'
}

PREFIXES=()
build_prefixes() {
  if [ -n "$BASE_URL_OPT" ]; then
    PREFIXES=("$BASE_URL_OPT")
  else
    PREFIXES=("${MIRROR_PREFIXES[@]}")
  fi
}

# 把上次成功的源提到最前面，后续请求就不用再等官方超时
prefer_resolved() {
  [ -n "$RESOLVED_PREFIX" ] || return 0
  local out=("$RESOLVED_PREFIX") b
  for b in "${PREFIXES[@]}"; do
    [ "$b" = "$RESOLVED_PREFIX" ] || out+=("$b")
  done
  PREFIXES=("${out[@]}")
}

RESOLVED_PREFIX=""

# 依次尝试各下载源，成功则记下可用的前缀
fetch_asset() {  # $1 = 相对路径(owner/repo/...), $2 = 目标文件
  local rel="$1" dest="$2" p host
  for p in "${PREFIXES[@]}"; do
    host="$(host_of "$(join_url "$p" "$GH")")"
    if [ "${FETCH_QUIET:-0}" != "1" ]; then
      printf '   源: %s\n' "$host"
    fi
    if fetch_file "$(join_url "$p" "$GH/$rel")" "$dest" && [ -s "$dest" ]; then
      RESOLVED_PREFIX="$p"
      return 0
    fi
    if [ "${FETCH_QUIET:-0}" != "1" ]; then
      warn "${host} 不可用"
    fi
    rm -f "$dest"
  done
  return 1
}

# 解析最新 tag：先试 GitHub API，再退化为跟随 /releases/latest 的跳转
latest_tag() {
  local p json tag url
  for p in "${PREFIXES[@]}"; do
    printf '   源: %s\n' "$(host_of "$(join_url "$p" "$API")")" >&2
    json="$(fetch_stdout "$(join_url "$p" "$API/repos/$REPO/releases/latest")" 2>/dev/null || true)"
    tag="$(printf '%s' "$json" \
      | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' \
      | head -n1 | sed 's/.*"\([^"]*\)"$/\1/' || true)"
    if [ -z "$tag" ] && command -v curl >/dev/null 2>&1; then
      url="$(curl -sSL -o /dev/null -w '%{url_effective}' --max-time 25 \
        "$(join_url "$p" "$GH/$REPO/releases/latest")" 2>/dev/null || true)"
      case "$url" in *"/tag/"*) tag="${url##*/tag/}" ;; esac
    fi
    case "$tag" in
      v[0-9]*)
        RESOLVED_PREFIX="$p"
        printf '%s' "$tag"
        return 0
        ;;
    esac
  done
  return 1
}

build_prefixes

# ---------------------------------------------------------------- 卸载

uninstall() {
  log "卸载 DoriyaMedia"
  if [ "$HAVE_SYSTEMD" -eq 1 ]; then
    systemctl stop "$SERVICE" 2>/dev/null || true
    systemctl disable "$SERVICE" 2>/dev/null || true
  fi
  rm -f "$UNIT_PATH"
  if [ "$HAVE_SYSTEMD" -eq 1 ]; then
    systemctl daemon-reload || true
  fi
  rm -f "$BIN_PATH" "$DM_PATH"
  rm -rf /etc/doriyamedia
  ok "已移除服务与二进制"

  if [ "$PURGE" -eq 1 ]; then
    rm -rf "$DATA_DIR"
    ok "已删除数据目录 $DATA_DIR"
    id -u "$SERVICE_USER" >/dev/null 2>&1 && userdel "$SERVICE_USER" 2>/dev/null || true
  elif [ -d "$DATA_DIR" ]; then
    warn "数据目录保留在 $DATA_DIR（要一并删除请用 --purge）"
  fi
  exit 0
}

[ "$UNINSTALL" -eq 1 ] && uninstall

# ---------------------------------------------------------------- 解析版本

if [ -z "$VERSION" ]; then
  log "查询最新版本"
  VERSION="$(latest_tag || true)"
  [ -n "$VERSION" ] || die "查询最新版本失败：官方和镜像都没连上
  可以跳过这一步直接指定版本： --version v0.1.0"
fi

case "$VERSION" in v*) : ;; *) VERSION="v${VERSION}" ;; esac

NAME="linux_${ARCH}"
ARCHIVE="${SERVICE}_${VERSION}_${NAME}.tar.gz"

log "安装 ${C_B}DoriyaMedia ${VERSION}${C_R} (${ARCH})"

# ---------------------------------------------------------------- 下载

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

prefer_resolved
tried=""
for p in "${PREFIXES[@]}"; do
  tried="${tried} $(host_of "$(join_url "$p" "$GH")")"
done

log "下载 ${ARCHIVE}"
if ! fetch_asset "${REPO}/releases/download/${VERSION}/${ARCHIVE}" "${TMP}/${ARCHIVE}"; then
  die "所有下载源都失败，已尝试：${tried}
  可以手动指定镜像： --base-url https://ghfast.top
  或在能联网的机器上下载后拷过来：
    ${GH}/${REPO}/releases/download/${VERSION}/${ARCHIVE}"
fi
ok "已从 $(host_of "$(join_url "$RESOLVED_PREFIX" "$GH")") 获取"

if FETCH_QUIET=1 fetch_asset "${REPO}/releases/download/${VERSION}/SHA256SUMS" "${TMP}/SHA256SUMS" 2>/dev/null \
   && [ -s "${TMP}/SHA256SUMS" ]; then
  # 兼容 sha256sum 的三种写法: "hash  name"、"./name" 和二进制模式的 "hash *name"
  expected="$(awk -v f="$ARCHIVE" '
      { n = $NF; sub(/^\*/, "", n); sub(/^\.\//, "", n); if (n == f) { print $1; exit } }
    ' "${TMP}/SHA256SUMS" 2>/dev/null || true)"
  if [ -n "$expected" ]; then
    actual="$(sha256sum "${TMP}/${ARCHIVE}" | awk '{print $1}' || true)"
    [ "$expected" = "$actual" ] || die "校验和不匹配，下载可能被篡改或损坏：
  期望 $expected
  实际 $actual"
    ok "SHA256 校验通过"
  else
    warn "SHA256SUMS 里没有 ${ARCHIVE}，跳过校验"
  fi
else
  warn "没取到 SHA256SUMS，跳过校验"
fi

log "解包"
tar -xzf "${TMP}/${ARCHIVE}" -C "$TMP"
src_bin="$(find "$TMP" -maxdepth 3 -type f -name "$SERVICE" | head -n1 || true)"
[ -n "$src_bin" ] || die "压缩包里没找到 $SERVICE 可执行文件"

# ---------------------------------------------------------------- ffmpeg

install_ffmpeg() {
  if command -v ffmpeg >/dev/null 2>&1 && command -v ffprobe >/dev/null 2>&1; then
    ok "ffmpeg / ffprobe 已就绪"
    return
  fi
  if [ "$INSTALL_FFMPEG" -eq 0 ]; then
    warn "按 --no-ffmpeg 跳过；没有 ffmpeg 时只有 302 直连可用，转封装/转码/字幕会失败"
    return
  fi
  if ! command -v apt-get >/dev/null 2>&1; then
    warn "没有 apt-get，请自行安装 ffmpeg，否则转封装/转码不可用"
    return
  fi
  log "安装 ffmpeg（转封装 / HLS / 字幕需要）"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq || warn "apt-get update 失败，继续尝试安装"
  apt-get install -y -qq ffmpeg || warn "ffmpeg 安装失败，可稍后手动 apt install ffmpeg"
  command -v ffmpeg >/dev/null 2>&1 && ok "ffmpeg 安装完成" || warn "ffmpeg 仍不可用"
}

# ---------------------------------------------------------------- 安装文件

log "安装二进制到 ${BIN_PATH}"
# 先写临时文件再原子替换：直接覆盖正在运行的二进制会报 Text file busy
install -m 0755 "$src_bin" "${BIN_PATH}.new"
mv -f "${BIN_PATH}.new" "$BIN_PATH"

cur="$("$BIN_PATH" -version 2>/dev/null || echo 'doriyamedia ?')"
ok "已安装: ${cur}"

# ---------------------------------------------------------------- dm 管理命令

# 生成 dm（类似宝塔的 bt）：菜单 + update/check/status/log/uninstall。
# dm.env 记录本次安装的实际路径，dm update 时原样传回，避免自定义
# --data-dir/--user 的机器更新后指向默认路径。
write_dm() {
  local no_ffmpeg=0
  if [ "$INSTALL_FFMPEG" -eq 0 ]; then no_ffmpeg=1; fi

  mkdir -p /etc/doriyamedia
  cat > /etc/doriyamedia/dm.env <<DORIYA_DM_ENV
# 由 install.sh 生成，供 dm 命令读取；删除后 dm 会退回默认路径
DATA_DIR="${DATA_DIR}"
SERVICE_USER="${SERVICE_USER}"
NO_FFMPEG=${no_ffmpeg}
DORIYA_DM_ENV

  cat > "${DM_PATH}.new" <<'DORIYA_DM_SCRIPT'
#!/usr/bin/env bash
#
# DoriyaMedia 管理命令（由 install.sh 生成，卸载时一并移除）
#
#   dm               进入管理菜单
#   dm update        更新到最新版；dm update v0.2.1 更新到指定版本
#   dm check         只检查有没有新版本
#   dm status        服务状态    dm start / stop / restart  启停
#   dm log           跟随日志    dm version   已装版本
#   dm uninstall     卸载（保留数据）    dm purge   卸载并删数据
#
set -u

SERVICE="doriyamedia"
REPO="nekofinley/DoriyaMedia"
BIN_PATH="/usr/local/bin/doriyamedia"
DATA_DIR="/var/lib/doriyamedia"
SERVICE_USER="doriyamedia"
NO_FFMPEG=0

ENV_FILE="/etc/doriyamedia/dm.env"
if [ -f "$ENV_FILE" ]; then
  . "$ENV_FILE"
fi
CONFIG_PATH="${DATA_DIR}/config.json"
UNIT_PATH="/etc/systemd/system/${SERVICE}.service"

GH="https://github.com"
RAW="https://raw.githubusercontent.com"
API="https://api.github.com"
PREFIXES=("" "https://ghfast.top" "https://ghproxy.net" "https://gh-proxy.com")

if [ -t 1 ] && [ "${NO_COLOR:-}" = "" ]; then
  C_R=$'\033[0m'; C_B=$'\033[1m'; C_G=$'\033[32m'; C_Y=$'\033[33m'; C_E=$'\033[31m'; C_C=$'\033[36m'
else
  C_R=""; C_B=""; C_G=""; C_Y=""; C_E=""; C_C=""
fi
log()  { printf '%s==>%s %s\n' "$C_C$C_B" "$C_R" "$*"; }
ok()   { printf '%s  ✓%s %s\n' "$C_G" "$C_R" "$*"; }
warn() { printf '%s  !%s %s\n' "$C_Y" "$C_R" "$*" >&2; }
die()  { printf '%s  错误:%s %s\n' "$C_E$C_B" "$C_R" "$*" >&2; exit 1; }

HAVE_SYSTEMD=0
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  HAVE_SYSTEMD=1
fi

installed_version() {
  [ -x "$BIN_PATH" ] || return 0
  "$BIN_PATH" -version 2>/dev/null | awk '{print $2}'
}

svc_state() {
  if [ "$HAVE_SYSTEMD" -eq 1 ]; then
    if systemctl is-active --quiet "$SERVICE" 2>/dev/null; then echo "running"; else echo "inactive"; fi
  elif pgrep -x "$SERVICE" >/dev/null 2>&1; then
    echo "running"
  else
    echo "stopped"
  fi
}

config_port() {
  grep -o '"port"[[:space:]]*:[[:space:]]*[0-9][0-9]*' "$CONFIG_PATH" 2>/dev/null \
    | grep -o '[0-9][0-9]*$' | head -n1
}

# $1=url  $2=目标文件（留空则打到 stdout）
http_get() {
  if command -v curl >/dev/null 2>&1; then
    if [ -n "$2" ]; then
      curl -fsSL --retry 1 --connect-timeout 8 --max-time 300 "$1" -o "$2"
    else
      curl -fsSL --retry 2 --connect-timeout 10 --max-time 30 "$1"
    fi
  elif [ -n "$2" ]; then
    wget -q --tries=2 --timeout=15 -O "$2" "$1"
  else
    wget -qO- --tries=2 --timeout=15 "$1"
  fi
}

join_prefix() {
  if [ -n "$1" ]; then printf '%s/%s' "${1%/}" "$2"; else printf '%s' "$2"; fi
}

latest_version() {
  local p json tag url
  for p in "${PREFIXES[@]}"; do
    json="$(http_get "$(join_prefix "$p" "$API")/repos/$REPO/releases/latest" "" 2>/dev/null || true)"
    tag="$(printf '%s' "$json" \
      | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' \
      | head -n1 | sed 's/.*"\([^"]*\)"$/\1/' || true)"
    if [ -z "$tag" ] && command -v curl >/dev/null 2>&1; then
      url="$(curl -sSL -o /dev/null -w '%{url_effective}' --max-time 25 \
        "$(join_prefix "$p" "$GH/$REPO/releases/latest")" 2>/dev/null || true)"
      case "$url" in *"/tag/"*) tag="${url##*/tag/}" ;; esac
    fi
    case "$tag" in
      v[0-9]*) printf '%s' "$tag"; return 0 ;;
    esac
  done
  return 1
}

fetch_installer() {  # $1 = 目标文件
  local p
  for p in "${PREFIXES[@]}"; do
    if http_get "$(join_prefix "$p" "$RAW")/$REPO/main/scripts/install.sh" "$1" 2>/dev/null \
       && [ -s "$1" ]; then
      return 0
    fi
    rm -f "$1"
  done
  return 1
}

cmd_check() {
  local cur latest
  cur="$(installed_version)"
  [ -n "$cur" ] || die "没找到已安装的 $SERVICE"
  log "当前版本 ${cur}，查询最新版..."
  latest="$(latest_version || true)"
  if [ -z "$latest" ]; then
    warn "查询失败：官方和镜像都不通，稍后再试"
    return 1
  fi
  if [ "$latest" = "$cur" ]; then
    ok "已是最新版 ${cur}"
  else
    printf '  有新版本: %s%s%s   （dm update 更新）\n' "$C_G$C_B" "$latest" "$C_R"
  fi
}

cmd_update() {
  local want="${1:-}" cur latest
  cur="$(installed_version)"
  [ -n "$cur" ] || die "没找到已安装的 $SERVICE（先跑安装脚本）"
  if [ -z "$want" ]; then
    log "查询最新版本"
    latest="$(latest_version || true)"
    if [ -z "$latest" ]; then
      warn "查询失败：官方和镜像都不通。稍后再试，或 dm update <tag> 指定版本"
      return 1
    fi
    if [ "$latest" = "$cur" ]; then
      ok "已是最新版 ${cur}"
      return 0
    fi
    want="$latest"
  fi
  log "更新: ${cur} → ${want}"
  # 注意用全局 DM_TMP：EXIT trap 在函数返回后触发，局部变量已出栈
  DM_TMP="$(mktemp -d)"
  trap 'rm -rf "$DM_TMP"' EXIT
  fetch_installer "$DM_TMP/install.sh" || { warn "下载安装脚本失败：官方和镜像都不通"; return 1; }
  local args=(--version "$want" --data-dir "$DATA_DIR" --user "$SERVICE_USER")
  if [ "$NO_FFMPEG" -eq 1 ]; then
    args+=(--no-ffmpeg)
  fi
  if ! bash "$DM_TMP/install.sh" "${args[@]}"; then
    rm -rf "$DM_TMP"
    die "更新失败，旧版本未受影响（二进制是原子替换，服务配置也没动）"
  fi
  rm -rf "$DM_TMP"
  trap - EXIT
  ok "更新完成，现在版本: $(installed_version)"
}

cmd_status() {
  local ver state port
  ver="$(installed_version)"
  state="$(svc_state)"
  port="$(config_port)"
  [ -n "$ver" ] || ver="未安装"
  [ -n "$port" ] || port="-"
  if [ "$state" = "running" ]; then
    state="${C_G}运行中${C_R}"
  else
    state="${C_E}已停止${C_R}"
  fi
  printf '%s服务%s   %b\n' "$C_B" "$C_R" "$state"
  printf '%s版本%s   %s\n' "$C_B" "$C_R" "$ver"
  printf '%s端口%s   %s\n' "$C_B" "$C_R" "$port"
  printf '%s程序%s   %s\n' "$C_B" "$C_R" "$BIN_PATH"
  printf '%s数据%s   %s\n' "$C_B" "$C_R" "$DATA_DIR"
  if [ "$HAVE_SYSTEMD" -eq 1 ]; then
    printf '\n'
    systemctl status "$SERVICE" --no-pager -l 2>/dev/null | head -n 12 || true
  fi
}

bg_start() {
  cd "$DATA_DIR" 2>/dev/null || true
  if command -v runuser >/dev/null 2>&1; then
    runuser -u "$SERVICE_USER" -- nohup "$BIN_PATH" -config "$CONFIG_PATH" >/dev/null 2>&1 &
  else
    su -s /bin/sh -c "nohup '$BIN_PATH' -config '$CONFIG_PATH' >/dev/null 2>&1 &" "$SERVICE_USER"
  fi
}

cmd_start() {
  if [ "$HAVE_SYSTEMD" -eq 1 ]; then
    systemctl start "$SERVICE" && ok "已启动"
  else
    bg_start
    ok "已在后台启动（前台运行: sudo -u $SERVICE_USER $BIN_PATH -config $CONFIG_PATH）"
  fi
}

cmd_stop() {
  if [ "$HAVE_SYSTEMD" -eq 1 ]; then
    systemctl stop "$SERVICE" && ok "已停止"
  elif pkill -x "$SERVICE" 2>/dev/null; then
    ok "已停止"
  else
    warn "没有运行中的进程"
  fi
}

cmd_restart() {
  if [ "$HAVE_SYSTEMD" -eq 1 ]; then
    systemctl restart "$SERVICE" && ok "已重启"
  else
    pkill -x "$SERVICE" 2>/dev/null || true
    sleep 1
    bg_start
    ok "已重启"
  fi
}

cmd_log() {
  if [ "$HAVE_SYSTEMD" -eq 1 ]; then
    exec journalctl -u "$SERVICE" -n 200 -f
  fi
  warn "无 systemd，没有持久日志；前台运行可以直接看到输出"
}

cmd_log_brief() {
  if [ "$HAVE_SYSTEMD" -eq 1 ]; then
    journalctl -u "$SERVICE" -n 50 --no-pager 2>/dev/null || true
  else
    warn "无 systemd，没有持久日志"
  fi
}

do_uninstall() {  # $1 = 是否连数据一起删
  log "卸载 DoriyaMedia"
  if [ "$HAVE_SYSTEMD" -eq 1 ]; then
    systemctl stop "$SERVICE" 2>/dev/null || true
    systemctl disable "$SERVICE" 2>/dev/null || true
    rm -f "$UNIT_PATH"
    systemctl daemon-reload || true
  else
    pkill -x "$SERVICE" 2>/dev/null || true
  fi
  rm -f "$BIN_PATH"
  if [ "$1" -eq 1 ]; then
    rm -rf "$DATA_DIR"
    id -u "$SERVICE_USER" >/dev/null 2>&1 && userdel "$SERVICE_USER" 2>/dev/null || true
    ok "已删除数据目录 $DATA_DIR"
  elif [ -d "$DATA_DIR" ]; then
    warn "数据目录保留在 $DATA_DIR（要一并删除用 dm purge）"
  fi
  rm -rf /etc/doriyamedia
  ok "已卸载，dm 命令一并移除"
  rm -f -- "$0"
  exit 0
}

menu() {
  local c ver state port sc ans
  while :; do
    clear 2>/dev/null || true
    ver="$(installed_version)"; [ -n "$ver" ] || ver="未安装"
    port="$(config_port)";        [ -n "$port" ] || port="-"
    if [ "$(svc_state)" = "running" ]; then sc="${C_G}● 运行中${C_R}"; else sc="${C_E}○ 已停止${C_R}"; fi
    printf '\n'
    printf '  %s──────────────────────────────────────%s\n' "$C_C" "$C_R"
    printf '  %s DoriyaMedia 管理面板%s\n' "$C_B$C_C" "$C_R"
    printf '  %s──────────────────────────────────────%s\n' "$C_C" "$C_R"
    printf '   服务 %s   版本 %s   端口 %s\n' "$sc" "$ver" "$port"
    printf '  %s──────────────────────────────────────%s\n' "$C_C" "$C_R"
    printf '    1) 检查更新          2) 更新到最新版\n'
    printf '    3) 重启服务          4) 启动服务\n'
    printf '    5) 停止服务          6) 查看日志\n'
    printf '    7) 服务详情          8) 卸载\n'
    printf '    0) 退出\n'
    printf '  %s──────────────────────────────────────%s\n' "$C_C" "$C_R"
    printf '  请输入: '
    read -r c || exit 0
    case "$c" in
      1) cmd_check || true ;;
      2) cmd_update || true ;;
      3) cmd_restart || true ;;
      4) cmd_start || true ;;
      5) cmd_stop || true ;;
      6) cmd_log_brief ;;
      7) cmd_status ;;
      8) printf '  确定卸载？数据会保留；输入 y 确认: '
         read -r ans || exit 0
         if [ "$ans" = "y" ]; then do_uninstall 0; fi ;;
      0) exit 0 ;;
      *) ;;
    esac
    printf '\n  按回车返回菜单...'
    read -r _ || exit 0
  done
}

usage() {
  cat <<DM_HELP
DoriyaMedia 管理命令

用法: dm [命令]

命令:
                不带参数进入管理菜单
  update [TAG]  更新到最新版，或指定版本（如 dm update v0.2.1）
  check         只检查有没有新版本，不安装
  status        服务状态（版本/端口/路径/systemd 详情）
  start         启动服务            stop      停止服务
  restart       重启服务            log       跟随日志（Ctrl+C 退出）
  version       已安装版本
  uninstall     卸载（保留数据）    purge     卸载并删除数据
  help          显示本帮助

下载源和安装脚本一样支持自动镜像回退；也可用环境变量
DORIYAMEDIA_GITHUB_BASE / DORIYAMEDIA_API_BASE 覆盖。
DM_HELP
}

case "${1:-menu}" in
  menu|"")
    if [ "$(id -u)" -ne 0 ]; then
      command -v sudo >/dev/null 2>&1 || die "需要 root 权限，请用 sudo 运行"
      exec sudo bash "$0"
    fi
    menu ;;
  update|start|stop|restart|uninstall|purge)
    if [ "$(id -u)" -ne 0 ]; then exec sudo bash "$0" "$@"; fi
    case "$1" in
      update)    shift; cmd_update "$@" || exit 1 ;;
      start)     cmd_start ;;
      stop)      cmd_stop ;;
      restart)   cmd_restart ;;
      uninstall) do_uninstall 0 ;;
      purge)     do_uninstall 1 ;;
    esac ;;
  check)         cmd_check || exit 1 ;;
  status)        cmd_status ;;
  log)           cmd_log ;;
  version)       installed_version || echo "未安装" ;;
  help|-h|--help) usage ;;
  *)             die "未知命令: $1（dm help 查看用法）" ;;
esac
DORIYA_DM_SCRIPT
  chmod 0755 "${DM_PATH}.new"
  mv -f "${DM_PATH}.new" "$DM_PATH"
}

write_dm
ok "已安装管理命令 ${C_B}dm${C_R}（输入 dm 进菜单，dm update 更新）"

if [ "$INSTALL_FFMPEG" -eq 1 ]; then
  install_ffmpeg
elif command -v ffmpeg >/dev/null 2>&1; then
  ok "ffmpeg / ffprobe 已就绪"
fi

log "准备数据目录 ${DATA_DIR}"
mkdir -p "$DATA_DIR"
if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
  nologin="$(command -v nologin || echo /bin/false)"
  useradd --system --no-create-home --home-dir "$DATA_DIR" --shell "$nologin" "$SERVICE_USER" \
    || die "创建用户 $SERVICE_USER 失败"
  ok "已创建系统用户 ${SERVICE_USER}"
fi
chown -R "${SERVICE_USER}:${SERVICE_USER}" "$DATA_DIR"
chmod 0750 "$DATA_DIR"

# 首次安装写入一份最小配置。应用启动时会补齐其余默认项。
# bgm.use_proxy 默认是 true 且指向 127.0.0.1:7890（Windows 上的本地代理），
# 服务器上没有这个代理，所以这里预置为直连，否则 Bangumi 刮削必然超时。
if [ ! -f "$CONFIG_PATH" ]; then
  cat > "$CONFIG_PATH" <<EOF
{
  "port": ${PORT},
  "data_dir": "${DATA_DIR}",
  "bgm": {
    "use_proxy": false
  }
}
EOF
  chown "${SERVICE_USER}:${SERVICE_USER}" "$CONFIG_PATH"
  chmod 0640 "$CONFIG_PATH"
  ok "已生成配置 ${CONFIG_PATH}（端口 ${PORT}，Bangumi 直连）"
else
  ok "沿用已有配置 ${CONFIG_PATH}（端口等配置以该文件为准）"
  warn "--port ${PORT} 未写入：配置已存在，请直接改 $CONFIG_PATH 或在网页「设置」里改"
fi

# ---------------------------------------------------------------- systemd

write_unit() {
  cat > "$UNIT_PATH" <<EOF
[Unit]
Description=DoriyaMedia streaming server
Documentation=https://github.com/${REPO}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_USER}
WorkingDirectory=${DATA_DIR}
ExecStart=${BIN_PATH} -config ${CONFIG_PATH}
Restart=on-failure
RestartSec=3

# HLS 分片 / 转码会频繁拉起 ffmpeg 子进程
LimitNOFILE=65535

# 加固：本机来源的媒体（/home、/mnt、/srv 等）仍需可读，所以 home 只读不隐藏
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=read-only

[Install]
WantedBy=multi-user.target
EOF
  chmod 0644 "$UNIT_PATH"
}

service_start() {
  log "启动服务"
  systemctl daemon-reload
  systemctl enable "$SERVICE" >/dev/null 2>&1 || true
  if systemctl restart "$SERVICE"; then
    sleep 1
    if systemctl is-active --quiet "$SERVICE"; then
      ok "服务运行中"
    else
      warn "服务没起来，看日志: journalctl -u ${SERVICE} -n 50 --no-pager"
    fi
  else
    warn "启动失败，看日志: journalctl -u ${SERVICE} -n 50 --no-pager"
  fi
}

if [ "$HAVE_SYSTEMD" -eq 1 ]; then
  write_unit
  ok "已写入 ${UNIT_PATH}"
  if [ "$AUTOSTART" -eq 1 ]; then
    service_start
  else
    warn "按 --no-start 跳过启动；要启动请执行 systemctl start ${SERVICE}"
  fi
else
  warn "当前环境没有运行中的 systemd，已跳过服务注册"
  warn "可手动前台运行: sudo -u ${SERVICE_USER} ${BIN_PATH} -config ${CONFIG_PATH}"
fi

# ---------------------------------------------------------------- 收尾

ip="$(hostname -I 2>/dev/null | awk '{print $1}' || true)"
[ -n "$ip" ] || ip="<服务器IP>"

cat <<EOF

${C_G}${C_B}安装完成${C_R}

  版本      ${VERSION} (${ARCH})
  程序      ${BIN_PATH}
  数据      ${DATA_DIR}   （配置 ${CONFIG_PATH}）
  监听      ${PORT}
  管理      ${C_B}dm${C_R}（管理菜单：更新/启停/日志）或 systemctl {status|restart|stop} ${SERVICE}
  日志      journalctl -u ${SERVICE} -f

  打开      ${C_B}http://${ip}:${PORT}${C_R}   首次进入会让你创建管理员账号

${C_Y}下一步建议${C_R}
  1. 刮削需要 TMDB API Key：网页「设置 → 刮削与播放」里填。
     TMDB 默认直连；国内网络需要在同一页勾选「走本地代理」并填你自己的代理地址。
  2. Bangumi 已预置为直连。若你的网络对 api.bgm.tv 有 DNS 污染，
     在同一页勾选代理并填地址。
  3. 端口/数据目录改 ${CONFIG_PATH} 后 systemctl restart ${SERVICE}。

EOF
