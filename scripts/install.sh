#!/usr/bin/env bash
#
# ClawSynapse CLI 一键安装脚本
#
# 用法:
#   本地安装（从项目 dist/ 目录）:
#     ./scripts/install.sh
#
#   远程安装（从 GitHub Release）:
#     curl -fsSL https://raw.githubusercontent.com/<OWNER>/clawsynapse/main/scripts/install.sh | bash
#
set -euo pipefail

BINARY_NAME="clawsynapse"
INSTALL_DIR="/usr/local/bin"

# --- 颜色 ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m'

info()  { printf "${GREEN}[info]${NC}  %s\n" "$*"; }
warn()  { printf "${YELLOW}[warn]${NC}  %s\n" "$*"; }
error() { printf "${RED}[error]${NC} %s\n" "$*" >&2; exit 1; }

# --- 检测平台 ---
detect_platform() {
    local os arch

    case "$(uname -s)" in
        Darwin) os="darwin" ;;
        Linux)  os="linux"  ;;
        *)      error "不支持的操作系统: $(uname -s)" ;;
    esac

    case "$(uname -m)" in
        x86_64|amd64)   arch="amd64" ;;
        arm64|aarch64)  arch="arm64" ;;
        *)              error "不支持的架构: $(uname -m)" ;;
    esac

    echo "${os}-${arch}"
}

# --- 确保有写权限 ---
ensure_writable() {
    if [ -w "$INSTALL_DIR" ]; then
        return 0
    fi

    if ! command -v sudo >/dev/null 2>&1; then
        error "无 ${INSTALL_DIR} 写权限且 sudo 不可用"
    fi

    info "需要 sudo 权限安装到 ${INSTALL_DIR}"
    # 后续命令通过 $SUDO 前缀调用
    SUDO="sudo"
}

# --- 检查已有安装 ---
check_existing() {
    if command -v "$BINARY_NAME" >/dev/null 2>&1; then
        local existing
        existing=$(command -v "$BINARY_NAME")
        warn "检测到已安装: ${existing}"
        warn "将覆盖安装"
    fi
}

# --- 从本地 dist/ 安装 ---
install_from_local() {
    local platform="$1"
    local src="dist/${BINARY_NAME}-${platform}"

    if [ ! -f "$src" ]; then
        return 1
    fi

    info "从本地找到: ${src}"
    do_install "$src"
    return 0
}

# --- 从 GitHub Release 下载安装 ---
install_from_github() {
    local platform="$1"
    local repo="${GITHUB_REPO:-}"

    if [ -z "$repo" ]; then
        error "未配置 GITHUB_REPO，且本地 dist/ 中无对应二进制文件。请先运行 'make dist' 构建"
    fi

    local version="${VERSION:-latest}"
    local url

    if [ "$version" = "latest" ]; then
        url="https://github.com/${repo}/releases/latest/download/${BINARY_NAME}-${platform}"
    else
        url="https://github.com/${repo}/releases/download/${version}/${BINARY_NAME}-${platform}"
    fi

    info "从 GitHub 下载: ${url}"

    local tmpfile
    tmpfile=$(mktemp)
    trap 'rm -f "$tmpfile"' EXIT

    if command -v curl >/dev/null 2>&1; then
        curl -fsSL -o "$tmpfile" "$url" || error "下载失败: ${url}"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO "$tmpfile" "$url" || error "下载失败: ${url}"
    else
        error "需要 curl 或 wget"
    fi

    do_install "$tmpfile"
}

# --- 执行安装 ---
do_install() {
    local src="$1"
    local dest="${INSTALL_DIR}/${BINARY_NAME}"

    ${SUDO:-} install -m 755 "$src" "$dest"

    # 验证
    if command -v "$BINARY_NAME" >/dev/null 2>&1; then
        info "安装成功: ${dest}"
        info "验证: $("$BINARY_NAME" health 2>&1 || echo '(daemon 未运行，但二进制可用)')"
    else
        info "已安装到 ${dest}"
    fi
}

# --- 卸载 ---
uninstall() {
    local dest="${INSTALL_DIR}/${BINARY_NAME}"
    if [ ! -f "$dest" ]; then
        error "${BINARY_NAME} 未安装在 ${dest}"
    fi

    info "卸载: ${dest}"
    ${SUDO:-} rm -f "$dest"

    info "卸载完成"
    exit 0
}

# --- 主流程 ---
main() {
    SUDO=""

    if [ "${1:-}" = "--uninstall" ] || [ "${1:-}" = "uninstall" ]; then
        ensure_writable
        uninstall
    fi

    info "ClawSynapse CLI 安装程序"

    local platform
    platform=$(detect_platform)
    info "检测到平台: ${platform}"

    check_existing
    ensure_writable

    # 优先从本地 dist/ 安装，失败则尝试 GitHub
    if ! install_from_local "$platform"; then
        install_from_github "$platform"
    fi
}

main "$@"
