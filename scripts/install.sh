#!/usr/bin/env bash
#
# DoriyaMedia 一键安装 / 升级脚本
# 适用：Debian 12/13、Ubuntu 22.04/24.04 及其衍生版（x86_64 / arm64 / armv7）
#
#   curl -fsSL https://raw.githubusercontent.com/nekofinley/DoriyaMedia/main/scripts/install.sh | sudo bash
#
# 国内网络下载慢时，可以套一层镜像前缀：
#   sudo bash install.sh --base-url https://ghfast.top/https://github.com
#
set -euo pipefail

REPO="nekofinley/DoriyaMedia"
SERVICE="doriyamedia"
BIN_PATH="/usr/local/bin/${SERVICE}"
DATA_DIR="/var/lib/doriyamedia"
CONFIG_PATH="${DATA_DIR}/config.json"
UNIT_PATH="/etc/systemd/system/${SERVICE}.service"
SERVICE_USER="doriyamedia"
PORT="5666"

GITHUB_BASE="${DORIYAMEDIA_GITHUB_BASE:-https://github.com}"
API_BASE="${DORIYAMEDIA_API_BASE:-https://api.github.com}"

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
      --base-url URL    GitHub 下载前缀，用于镜像加速
                        例: --base-url https://ghfast.top/https://github.com
      --no-ffmpeg       不自动安装 ffmpeg
      --no-start        只安装，不启动/不设开机自启
      --uninstall       卸载（保留数据目录）
      --purge           卸载并删除数据目录
  -h, --help            显示本帮助

环境变量:
  DORIYAMEDIA_GITHUB_BASE  同 --base-url
  DORIYAMEDIA_API_BASE     覆盖 GitHub API 地址
  NO_COLOR=1               关闭彩色输出
EOF
}

# ---------------------------------------------------------------- 参数

while [ $# -gt 0 ]; do
  case "$1" in
    -v|--version)  VERSION="${2:-}"; shift 2 ;;
    -p|--port)     PORT="${2:-}"; shift 2 ;;
    --data-dir)    DATA_DIR="${2:-}"; shift 2 ;;
    --user)        SERVICE_USER="${2:-}"; shift 2 ;;
    --base-url)    GITHUB_BASE="${2:-}"; shift 2 ;;
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
    curl -fsSL --retry 3 --connect-timeout 15 "$1"
  else
    wget -qO- --tries=3 --timeout=15 "$1"
  fi
}

fetch_file() {
  if command -v curl >/dev/null 2>&1; then
    curl -fL --retry 3 --connect-timeout 15 --progress-bar -o "$2" "$1"
  else
    wget -q --show-progress --tries=3 --timeout=15 -O "$2" "$1"
  fi
}

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
  rm -f "$BIN_PATH"
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
  api="${API_BASE}/repos/${REPO}/releases/latest"
  json="$(fetch_stdout "$api")" \
    || die "查询最新版本失败：$api
  如果是网络问题，可改用 --version v0.1.0 直接指定版本，或用 --base-url 走镜像"
  VERSION="$(printf '%s' "$json" \
    | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' \
    | head -n1 | sed 's/.*"\([^"]*\)"$/\1/' || true)"
  [ -n "$VERSION" ] || die "没能从 GitHub API 解析出版本号"
fi

case "$VERSION" in v*) : ;; *) VERSION="v${VERSION}" ;; esac

NAME="linux_${ARCH}"
ARCHIVE="${SERVICE}_${VERSION}_${NAME}.tar.gz"
URL="${GITHUB_BASE}/${REPO}/releases/download/${VERSION}/${ARCHIVE}"
SUMS_URL="${GITHUB_BASE}/${REPO}/releases/download/${VERSION}/SHA256SUMS"

log "安装 ${C_B}DoriyaMedia ${VERSION}${C_R} (${ARCH})"

# ---------------------------------------------------------------- 下载

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

log "下载 ${ARCHIVE}"
fetch_file "$URL" "${TMP}/${ARCHIVE}" \
  || die "下载失败：$URL
  国内网络可试： --base-url https://ghfast.top/https://github.com"

if fetch_file "$SUMS_URL" "${TMP}/SHA256SUMS" 2>/dev/null && [ -s "${TMP}/SHA256SUMS" ]; then
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
install -m 0755 "$src_bin" "$BIN_PATH"

cur="$("$BIN_PATH" -version 2>/dev/null || echo 'doriyamedia ?')"
ok "已安装: ${cur}"

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
  管理      systemctl {status|restart|stop} ${SERVICE}
  日志      journalctl -u ${SERVICE} -f

  打开      ${C_B}http://${ip}:${PORT}${C_R}   首次进入会让你创建管理员账号

${C_Y}下一步建议${C_R}
  1. 刮削需要 TMDB API Key：网页「设置 → 刮削与播放」里填。
     TMDB 默认直连；国内网络需要在同一页勾选「走本地代理」并填你自己的代理地址。
  2. Bangumi 已预置为直连。若你的网络对 api.bgm.tv 有 DNS 污染，
     在同一页勾选代理并填地址。
  3. 端口/数据目录改 ${CONFIG_PATH} 后 systemctl restart ${SERVICE}。

EOF
