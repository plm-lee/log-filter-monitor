#!/bin/sh
# log-filter-monitor 一键安装脚本
# 从 GitHub Release 下载二进制，配置 PATH，不立即启动
# 用法: curl -fsSL https://raw.githubusercontent.com/plm-lee/log-filter-monitor/main/install.sh | sh
# 指定版本: VERSION=v1.0.0 curl -fsSL ... | sh

set -e

REPO="${LOG_FILTER_MONITOR_REPO:-plm-lee/log-filter-monitor}"
CONFIG_DIR="${HOME}/.log-agent"
BIN_DIR="${CONFIG_DIR}/bin"
BINARY_NAME="log-filter-monitor"

# 检测 OS
detect_os() {
    case "$(uname -s)" in
        Darwin) echo "darwin" ;;
        Linux)  echo "linux" ;;
        *)      echo "不支持的操作系统: $(uname -s)" >&2; exit 1 ;;
    esac
}

# 检测架构
detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64) echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        *) echo "不支持的架构: $(uname -m)" >&2; exit 1 ;;
    esac
}

# 获取最新版本 tag
get_latest_version() {
    curl -s "https://api.github.com/repos/${REPO}/releases/latest" | \
        grep '"tag_name"' | cut -d'"' -f4
}

# 默认配置
create_default_config() {
    LOG_PATH="${CONFIG_DIR}/logs/app.log"
    cat > "${CONFIG_DIR}/config.yaml" << CONFIG_EOF
# log-filter-monitor 默认配置
# 请根据实际情况修改 api_url 和 log_file 路径

handler:
  type: http
  api_url: http://localhost:8888/log/manager/api/v1/logs
  timeout: 10s

metrics:
  enabled: true
  interval: 1m
  api_url: http://localhost:8888/log/manager/api/v1/metrics
  timeout: 10s

rules:
  - name: "错误日志"
    pattern: "ERROR|FATAL|CRITICAL|Exception"
    description: "匹配包含错误、致命错误、严重错误或异常的日志"
    log_file: ${LOG_PATH}
    tag: error
    metrics_enable: true
    report_mode: full

  - name: "警告日志"
    pattern: "WARN|WARNING"
    description: "匹配包含警告信息的日志"
    log_file: ${LOG_PATH}
    tag: warning
    metrics_enable: true
    report_mode: full
CONFIG_EOF
}

# 将 BIN_DIR 加入 PATH
add_to_path() {
    case ":$PATH:" in
        *":${BIN_DIR}:"*) return 0 ;;
    esac
    RC_FILE=""
    if [ -n "${SHELL}" ] && case "${SHELL}" in *zsh*) true;; *) false;; esac; then
        RC_FILE="${HOME}/.zshrc"
    elif [ -f "${HOME}/.zshrc" ]; then
        RC_FILE="${HOME}/.zshrc"
    elif [ -f "${HOME}/.bashrc" ]; then
        RC_FILE="${HOME}/.bashrc"
    else
        RC_FILE="${HOME}/.profile"
    fi
    if ! grep -q ".log-agent/bin" "${RC_FILE}" 2>/dev/null; then
        echo "" >> "${RC_FILE}"
        echo "# log-filter-monitor" >> "${RC_FILE}"
        echo "export PATH=\"\${HOME}/.log-agent/bin:\$PATH\"" >> "${RC_FILE}"
        echo "已添加至 PATH: ${RC_FILE}"
    fi
}

echo "=== log-filter-monitor 一键安装 ==="

# 确定版本
if [ -n "${VERSION}" ]; then
    echo "使用指定版本: ${VERSION}"
else
    echo "获取最新版本..."
    VERSION=$(get_latest_version) || {
        echo "错误: 无法获取最新版本，请设置 VERSION 环境变量，如 VERSION=v1.0.0"
        exit 1
    }
    echo "最新版本: ${VERSION}"
fi

# 检测平台
OS=$(detect_os)
ARCH=$(detect_arch)
BINARY_FILE="${BINARY_NAME}-${OS}-${ARCH}"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY_FILE}"

# 创建目录
echo "创建目录: ${BIN_DIR}"
mkdir -p "${BIN_DIR}"
mkdir -p "${CONFIG_DIR}/logs"

# 下载二进制
echo "下载: ${DOWNLOAD_URL}"
if ! curl -fSL -o "${BIN_DIR}/${BINARY_NAME}" "${DOWNLOAD_URL}"; then
    echo "错误: 下载失败。请检查："
    echo "  1) 网络连接"
    echo "  2) 版本 ${VERSION} 是否存在: https://github.com/${REPO}/releases"
    echo "  3) 当前平台 ${OS}-${ARCH} 是否有预编译包"
    exit 1
fi
chmod +x "${BIN_DIR}/${BINARY_NAME}"
echo "已安装: ${BIN_DIR}/${BINARY_NAME}"

# 创建默认配置
if [ ! -f "${CONFIG_DIR}/config.yaml" ]; then
    echo "创建默认配置: ${CONFIG_DIR}/config.yaml"
    create_default_config
    mkdir -p "${CONFIG_DIR}/logs"
    touch "${CONFIG_DIR}/logs/app.log"
    echo "提示: 请编辑 ${CONFIG_DIR}/config.yaml 设置 api_url 和 rules 中的 log_file"
else
    echo "配置文件已存在: ${CONFIG_DIR}/config.yaml"
fi

# 加入 PATH
add_to_path

# 安装完成提示
echo ""
echo "=== 安装完成 ==="
echo ""
echo "已安装: ${BIN_DIR}/${BINARY_NAME}"
echo "配置: ${CONFIG_DIR}/config.yaml"
echo ""
echo "PATH 已更新，请执行以下命令使 PATH 生效（或重新打开终端）："
echo "  source ~/.zshrc    # 使用 zsh 时"
echo "  或"
echo "  source ~/.bashrc   # 使用 bash 时"
echo ""
echo "后续操作："
echo "  1. 编辑配置文件，设置 api_url 和 rules 中的 log_file："
echo "     \${EDITOR:-vim} ${CONFIG_DIR}/config.yaml"
echo ""
echo "  2. 启动服务："
echo "     log-filter-monitor -config ${CONFIG_DIR}/config.yaml"
echo ""
echo "  3. 后台运行（可选）："
echo "     nohup log-filter-monitor -config ${CONFIG_DIR}/config.yaml > ${CONFIG_DIR}/logs/monitor.log 2>\&1 \&"
