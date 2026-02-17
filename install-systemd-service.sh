#!/bin/bash
#
# PicoClaw Systemd Service Installer
# 
# This script installs PicoClaw as a systemd service.
# It works for any user and auto-detects the configuration.
#
# Usage: ./install-systemd-service.sh [start|stop|restart|status|logs|uninstall]
#

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Get current user info
USER_NAME=$(whoami)
USER_HOME="$HOME"
SERVICE_NAME="picoclaw-gateway"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"

# Detect picoclaw binary location
detect_picoclaw() {
    if command -v picoclaw &> /dev/null; then
        PICOC_LAW_BIN=$(which picoclaw)
    elif [ -f "$USER_HOME/.local/bin/picoclaw" ]; then
        PICOC_LAW_BIN="$USER_HOME/.local/bin/picoclaw"
    elif [ -f "$USER_HOME/picoclaw-git/picoclaw" ]; then
        PICOC_LAW_BIN="$USER_HOME/picoclaw-git/picoclaw"
    else
        echo -e "${RED}❌ PicoClaw binary not found!${NC}"
        echo "Please install PicoClaw first or add it to your PATH"
        exit 1
    fi
}

# Check if config exists
check_config() {
    CONFIG_FILE="$USER_HOME/.picoclaw/config.json"
    if [ ! -f "$CONFIG_FILE" ]; then
        echo -e "${YELLOW}⚠️  Config file not found at $CONFIG_FILE${NC}"
        echo "Creating default configuration..."
        mkdir -p "$USER_HOME/.picoclaw/workspace"
        cat > "$CONFIG_FILE" << 'DEFAULTCONFIG'
{
  "agents": {
    "defaults": {
      "workspace": "~/.picoclaw/workspace",
      "restrict_to_workspace": true,
      "provider": "",
      "model": "glm-4.7",
      "max_tokens": 8192,
      "temperature": 0.7,
      "max_tool_iterations": 20
    }
  },
  "channels": {
    "telegram": {
      "enabled": false,
      "token": "",
      "proxy": "",
      "allow_from": []
    }
  },
  "providers": {},
  "gateway": {
    "host": "0.0.0.0",
    "port": 18790
  },
  "tools": {
    "web": {
      "duckduckgo": {
        "enabled": true,
        "max_results": 5
      }
    }
  },
  "heartbeat": {
    "enabled": true,
    "interval": 30
  }
}
DEFAULTCONFIG
        echo -e "${GREEN}✅ Default config created at $CONFIG_FILE${NC}"
        echo -e "${YELLOW}⚠️  Please edit the config file to add your API keys and enable channels${NC}"
    fi
}

# Create systemd service file
create_service() {
    echo -e "${BLUE}🔧 Creating systemd service...${NC}"
    
    # Create temp service file
    TEMP_SERVICE=$(mktemp)
    cat > "$TEMP_SERVICE" << EOF
[Unit]
Description=PicoClaw Gateway - Ultra-lightweight AI Assistant
Documentation=https://github.com/sipeed/picoclaw
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$USER_NAME
Group=$(id -gn)
WorkingDirectory=$USER_HOME
Environment="HOME=$USER_HOME"
Environment="USER=$USER_NAME"
Environment="PATH=$USER_HOME/.local/bin:/usr/local/bin:/usr/bin:/bin"
Environment="PICOCLAW_CONFIG=$USER_HOME/.picoclaw/config.json"
ExecStart=$PICOC_LAW_BIN gateway
ExecReload=/bin/kill -HUP \$MAINPID
Restart=on-failure
RestartSec=10
StandardOutput=journal
StandardError=journal
SyslogIdentifier=picoclaw-gateway

# Security hardening
NoNewPrivileges=false
ProtectSystem=no
ProtectHome=no

[Install]
WantedBy=multi-user.target
EOF

    # Move to systemd directory
    if [ "$EUID" -eq 0 ]; then
        # Running as root
        mv "$TEMP_SERVICE" "$SERVICE_FILE"
        chmod 644 "$SERVICE_FILE"
    else
        # Running as regular user, use sudo
        echo -e "${YELLOW}⚠️  Root privileges required to install systemd service${NC}"
        sudo mv "$TEMP_SERVICE" "$SERVICE_FILE"
        sudo chmod 644 "$SERVICE_FILE"
    fi
    
    # Reload systemd
    if [ "$EUID" -eq 0 ]; then
        systemctl daemon-reload
    else
        sudo systemctl daemon-reload
    fi
    
    echo -e "${GREEN}✅ Service file installed at $SERVICE_FILE${NC}"
}

# Start the service
start_service() {
    echo -e "${BLUE}🚀 Starting PicoClaw Gateway service...${NC}"
    if [ "$EUID" -eq 0 ]; then
        systemctl enable "$SERVICE_NAME"
        systemctl start "$SERVICE_NAME"
    else
        sudo systemctl enable "$SERVICE_NAME"
        sudo systemctl start "$SERVICE_NAME"
    fi
    sleep 2
    show_status
}

# Stop the service
stop_service() {
    echo -e "${BLUE}🛑 Stopping PicoClaw Gateway service...${NC}"
    if [ "$EUID" -eq 0 ]; then
        systemctl stop "$SERVICE_NAME" 2>/dev/null || true
    else
        sudo systemctl stop "$SERVICE_NAME" 2>/dev/null || true
    fi
    echo -e "${GREEN}✅ Service stopped${NC}"
}

# Restart the service
restart_service() {
    echo -e "${BLUE}🔄 Restarting PicoClaw Gateway service...${NC}"
    if [ "$EUID" -eq 0 ]; then
        systemctl restart "$SERVICE_NAME"
    else
        sudo systemctl restart "$SERVICE_NAME"
    fi
    sleep 2
    show_status
}

# Show service status
show_status() {
    echo -e "${BLUE}📊 Service Status:${NC}"
    echo "================================"
    if [ "$EUID" -eq 0 ]; then
        systemctl status "$SERVICE_NAME" --no-pager || true
    else
        sudo systemctl status "$SERVICE_NAME" --no-pager || true
    fi
}

# Show logs
show_logs() {
    echo -e "${BLUE}📜 Showing logs (press Ctrl+C to exit)...${NC}"
    if [ "$EUID" -eq 0 ]; then
        journalctl -u "$SERVICE_NAME" -f --no-pager
    else
        sudo journalctl -u "$SERVICE_NAME" -f --no-pager
    fi
}

# Uninstall service
uninstall_service() {
    echo -e "${YELLOW}⚠️  Uninstalling PicoClaw Gateway service...${NC}"
    stop_service
    if [ "$EUID" -eq 0 ]; then
        systemctl disable "$SERVICE_NAME" 2>/dev/null || true
        rm -f "$SERVICE_FILE"
        systemctl daemon-reload
    else
        sudo systemctl disable "$SERVICE_NAME" 2>/dev/null || true
        sudo rm -f "$SERVICE_FILE"
        sudo systemctl daemon-reload
    fi
    echo -e "${GREEN}✅ Service uninstalled${NC}"
}

# Show help
show_help() {
    cat << 'HELP'
╔══════════════════════════════════════════════════════════════════╗
║           PicoClaw Systemd Service Manager                       ║
╚══════════════════════════════════════════════════════════════════╝

Usage: ./install-systemd-service.sh [COMMAND]

Commands:
  install     Install and start the systemd service (default)
  start       Start the service
  stop        Stop the service
  restart     Restart the service
  status      Show service status
  logs        Show and follow logs
  uninstall   Remove the systemd service
  help        Show this help message

Examples:
  # Install and start the service
  ./install-systemd-service.sh

  # Or explicitly
  ./install-systemd-service.sh install

  # Check status
  ./install-systemd-service.sh status

  # View logs
  ./install-systemd-service.sh logs

  # Restart after config changes
  ./install-systemd-service.sh restart

╔══════════════════════════════════════════════════════════════════╗
║  Requirements:                                                   ║
  • systemd (most modern Linux distributions)                      ║
  • sudo access (for installing service file)                      ║
  • PicoClaw installed and configured                              ║
╚══════════════════════════════════════════════════════════════════╝
HELP
}

# Main function
main() {
    COMMAND="${1:-install}"
    
    case "$COMMAND" in
        install)
            detect_picoclaw
            check_config
            create_service
            start_service
            echo ""
            echo -e "${GREEN}╔════════════════════════════════════════════════════════════╗${NC}"
            echo -e "${GREEN}║  ✅ PicoClaw Gateway installed successfully!               ║${NC}"
            echo -e "${GREEN}╚════════════════════════════════════════════════════════════╝${NC}"
            echo ""
            echo "Useful commands:"
            echo "  ./install-systemd-service.sh status   # Check status"
            echo "  ./install-systemd-service.sh logs     # View logs"
            echo "  ./install-systemd-service.sh restart  # Restart service"
            echo ""
            ;;
        start)
            start_service
            ;;
        stop)
            stop_service
            ;;
        restart)
            restart_service
            ;;
        status)
            show_status
            ;;
        logs)
            show_logs
            ;;
        uninstall)
            uninstall_service
            ;;
        help|--help|-h)
            show_help
            ;;
        *)
            echo -e "${RED}❌ Unknown command: $COMMAND${NC}"
            show_help
            exit 1
            ;;
    esac
}

# Run main function
main "$@"
