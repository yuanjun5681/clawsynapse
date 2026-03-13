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
INSTALL_DIR="${HOME}/.clawsynapse/bin"

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

# --- 检测用户 shell 配置文件 ---
detect_shell_rc() {
    local shell_name
    shell_name=$(basename "${SHELL:-/bin/bash}")

    case "$shell_name" in
        zsh)  echo "${HOME}/.zshrc" ;;
        bash)
            # macOS 用 .bash_profile，Linux 用 .bashrc
            if [ "$(uname -s)" = "Darwin" ]; then
                echo "${HOME}/.bash_profile"
            else
                echo "${HOME}/.bashrc"
            fi
            ;;
        fish) echo "${HOME}/.config/fish/config.fish" ;;
        *)    echo "${HOME}/.profile" ;;
    esac
}

# --- 配置 PATH ---
setup_path() {
    # 已经在 PATH 中则跳过
    case ":${PATH}:" in
        *":${INSTALL_DIR}:"*) return 0 ;;
    esac

    local shell_rc
    shell_rc=$(detect_shell_rc)
    local shell_name
    shell_name=$(basename "${SHELL:-/bin/bash}")

    info "将 ${INSTALL_DIR} 添加到 PATH..."

    local path_line
    if [ "$shell_name" = "fish" ]; then
        path_line="fish_add_path ${INSTALL_DIR}"
    else
        path_line="export PATH=\"${INSTALL_DIR}:\$PATH\""
    fi

    # 检查是否已写入过
    if [ -f "$shell_rc" ] && grep -qF "$INSTALL_DIR" "$shell_rc" 2>/dev/null; then
        info "PATH 配置已存在于 ${shell_rc}"
    else
        printf '\n# ClawSynapse CLI\n%s\n' "$path_line" >> "$shell_rc"
        info "已写入 ${shell_rc}"
    fi

    # 当前会话也生效
    export PATH="${INSTALL_DIR}:${PATH}"

    warn "新终端窗口自动生效，当前终端请执行: source ${shell_rc}"
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

    mkdir -p "$INSTALL_DIR"
    install -m 755 "$src" "$dest"

    # 配置 PATH
    setup_path

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
    rm -f "$dest"

    # 清理空目录
    rmdir "$INSTALL_DIR" 2>/dev/null || true
    rmdir "${HOME}/.clawsynapse" 2>/dev/null || true

    # 提示清理 shell 配置
    local shell_rc
    shell_rc=$(detect_shell_rc)
    if [ -f "$shell_rc" ] && grep -qF "$INSTALL_DIR" "$shell_rc" 2>/dev/null; then
        warn "请手动移除 ${shell_rc} 中的 PATH 配置:"
        warn "  # ClawSynapse CLI"
        warn "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    fi

    info "卸载完成"
    exit 0
}

# --- 主流程 ---
main() {
    if [ "${1:-}" = "--uninstall" ] || [ "${1:-}" = "uninstall" ]; then
        uninstall
    fi

    info "ClawSynapse CLI 安装程序"

    local platform
    platform=$(detect_platform)
    info "检测到平台: ${platform}"

    check_existing

    # 优先从本地 dist/ 安装，失败则尝试 GitHub
    if ! install_from_local "$platform"; then
        install_from_github "$platform"
    fi
}

main "$@"
