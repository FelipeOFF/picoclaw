#!/bin/bash
#
# PicoClaw Update Script
# Build, install and restart the service
#

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Paths
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BINARY_NAME="picoclaw"
INSTALL_PATH="/usr/local/bin/${BINARY_NAME}"
SERVICE_NAME="picoclaw-gateway"
PID_FILE="/tmp/picoclaw.pid"

echo -e "${BLUE}🚀 PicoClaw Updater${NC}"
echo "========================"

# Function to check if running with sudo
check_sudo() {
    if [ "$EUID" -ne 0 ]; then 
        echo -e "${YELLOW}⚠️  Some operations require sudo privileges${NC}"
        SUDO_CMD="sudo"
    else
        SUDO_CMD=""
    fi
}

# Function to find and kill existing process
kill_existing() {
    echo -e "${BLUE}🔍 Checking for existing process...${NC}"
    
    # Try to find by pid file first
    if [ -f "$PID_FILE" ]; then
        PID=$(cat "$PID_FILE" 2>/dev/null)
        if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
            echo -e "${YELLOW}🛑 Stopping process PID: $PID${NC}"
            kill "$PID" 2>/dev/null || ${SUDO_CMD} kill "$PID" 2>/dev/null || true
            sleep 2
        fi
    fi
    
    # Find by process name
    PIDS=$(pgrep -f "${BINARY_NAME}" 2>/dev/null || true)
    if [ -n "$PIDS" ]; then
        echo -e "${YELLOW}🛑 Stopping ${BINARY_NAME} processes: $PIDS${NC}"
        echo "$PIDS" | xargs -r kill 2>/dev/null || ${SUDO_CMD} kill $PIDS 2>/dev/null || true
        sleep 2
    fi
    
    # Force kill if still running
    PIDS=$(pgrep -f "${BINARY_NAME}" 2>/dev/null || true)
    if [ -n "$PIDS" ]; then
        echo -e "${RED}💀 Force killing processes...${NC}"
        echo "$PIDS" | xargs -r kill -9 2>/dev/null || ${SUDO_CMD} kill -9 $PIDS 2>/dev/null || true
        sleep 1
    fi
    
    echo -e "${GREEN}✅ Process stopped${NC}"
}

# Function to build the binary
build() {
    echo -e "${BLUE}🔨 Building ${BINARY_NAME}...${NC}"
    cd "$SCRIPT_DIR"
    
    # Check if Go is installed
    if ! command -v go &> /dev/null; then
        echo -e "${RED}❌ Go is not installed!${NC}"
        exit 1
    fi
    
    # Build
    go build -o "${BINARY_NAME}" ./cmd/picoclaw/main.go
    
    if [ ! -f "${BINARY_NAME}" ]; then
        echo -e "${RED}❌ Build failed!${NC}"
        exit 1
    fi
    
    echo -e "${GREEN}✅ Build successful${NC}"
}

# Function to install the binary
install() {
    echo -e "${BLUE}📦 Installing to ${INSTALL_PATH}...${NC}"
    
    # Copy binary
    ${SUDO_CMD} cp "${SCRIPT_DIR}/${BINARY_NAME}" "${INSTALL_PATH}"
    ${SUDO_CMD} chmod +x "${INSTALL_PATH}"
    
    echo -e "${GREEN}✅ Installed successfully${NC}"
}

# Function to start the service
start() {
    echo -e "${BLUE}▶️  Starting ${BINARY_NAME}...${NC}"
    
    # Check if systemd service exists and use it
    if ${SUDO_CMD} systemctl is-active --quiet "${SERVICE_NAME}" 2>/dev/null; then
        echo -e "${YELLOW}🔄 Restarting systemd service...${NC}"
        ${SUDO_CMD} systemctl restart "${SERVICE_NAME}"
        sleep 2
        if ${SUDO_CMD} systemctl is-active --quiet "${SERVICE_NAME}"; then
            echo -e "${GREEN}✅ Service restarted successfully${NC}"
            show_status
            return
        else
            echo -e "${YELLOW}⚠️  Systemd service failed, falling back to manual start${NC}"
        fi
    fi
    
    # Check if systemd service file exists but not running
    if [ -f "/etc/systemd/system/${SERVICE_NAME}.service" ]; then
        echo -e "${YELLOW}🔄 Starting systemd service...${NC}"
        ${SUDO_CMD} systemctl daemon-reload
        ${SUDO_CMD} systemctl start "${SERVICE_NAME}"
        ${SUDO_CMD} systemctl enable "${SERVICE_NAME}" 2>/dev/null || true
        sleep 2
        if ${SUDO_CMD} systemctl is-active --quiet "${SERVICE_NAME}"; then
            echo -e "${GREEN}✅ Service started successfully${NC}"
            show_status
            return
        fi
    fi
    
    # Manual start as fallback
    echo -e "${YELLOW}⚠️  Starting manually (no systemd)...${NC}"
    nohup "${INSTALL_PATH}" gateway > /tmp/picoclaw.log 2>&1 &
    NEW_PID=$!
    echo $NEW_PID > "$PID_FILE"
    
    sleep 2
    if kill -0 "$NEW_PID" 2>/dev/null; then
        echo -e "${GREEN}✅ Started with PID: $NEW_PID${NC}"
        echo -e "${YELLOW}📝 Logs: tail -f /tmp/picoclaw.log${NC}"
    else
        echo -e "${RED}❌ Failed to start! Check logs: /tmp/picoclaw.log${NC}"
        exit 1
    fi
}

# Function to show status
show_status() {
    echo ""
    echo -e "${BLUE}📊 Status:${NC}"
    
    # Check systemd
    if ${SUDO_CMD} systemctl is-active --quiet "${SERVICE_NAME}" 2>/dev/null; then
        echo -e "${GREEN}● Service: running (systemd)${NC}"
        ${SUDO_CMD} systemctl status "${SERVICE_NAME}" --no-pager -l | grep -E "Active:|PID:|Memory:|CPU:" || true
    else
        # Check manual process
        PID=$(pgrep -f "${BINARY_NAME}" 2>/dev/null | head -1)
        if [ -n "$PID" ]; then
            echo -e "${GREEN}● Process: running (PID: $PID)${NC}"
        else
            echo -e "${RED}● Not running${NC}"
        fi
    fi
    
    # Show version
    if "${INSTALL_PATH}" version 2>/dev/null; then
        : # version command exists
    fi
}

# Function to show logs
show_logs() {
    local lines="${1:-50}"
    local follow="${2:-false}"
    
    echo ""
    if [ "$follow" = "true" ]; then
        echo -e "${BLUE}📜 Showing last $lines lines and following...${NC}"
        echo -e "${YELLOW}⚠️  Press Ctrl+C to exit${NC}"
        if ${SUDO_CMD} journalctl -u "${SERVICE_NAME}" -n "$lines" -f --no-pager 2>/dev/null; then
            : # systemd logs with follow
        else
            tail -f -n "$lines" /tmp/picoclaw.log 2>/dev/null || echo "No logs found"
        fi
    else
        echo -e "${BLUE}📜 Last $lines lines (use './update.sh logs -f' to follow):${NC}"
        if ${SUDO_CMD} journalctl -u "${SERVICE_NAME}" --no-pager -n "$lines" 2>/dev/null; then
            : # systemd logs
        else
            tail -n "$lines" /tmp/picoclaw.log 2>/dev/null || echo "No logs found"
        fi
    fi
}

# Main execution
main() {
    check_sudo
    
    case "${1:-}" in
        build)
            build
            ;;
        install)
            install
            ;;
        start|restart)
            kill_existing
            start
            ;;
        stop)
            kill_existing
            ;;
        status)
            show_status
            ;;
        logs)
            # Check for -f flag
            if [ "${2:-}" = "-f" ] || [ "${2:-}" = "--follow" ]; then
                show_logs 100 true
            else
                show_logs 50 false
            fi
            ;;
        full|all|"")
            # Full update: build + install + restart
            kill_existing
            build
            install
            start
            echo ""
            echo -e "${GREEN}🎉 Update completed successfully!${NC}"
            show_status
            ;;
        *)
            echo "Usage: $0 [build|install|start|stop|status|logs|full]"
            echo ""
            echo "Commands:"
            echo "  build     - Build the binary only"
            echo "  install   - Install the binary only"
            echo "  start     - Start/restart the service"
            echo "  stop      - Stop the service"
            echo "  status    - Show service status"
            echo "  logs      - Show last 50 lines (use 'logs -f' to follow)"
            echo "  full      - Full update (build + install + restart) [default]"
            exit 1
            ;;
    esac
}

main "$@"
