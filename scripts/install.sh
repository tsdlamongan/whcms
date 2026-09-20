#!/bin/bash
# WHCMS One-Command Installer
# Usage: curl -fsSL https://raw.githubusercontent.com/tsdlamongan/whcms/main/scripts/install.sh | bash
#
# Or with options:
#   curl -fsSL https://raw.githubusercontent.com/tsdlamongan/whcms/main/scripts/install.sh | bash -s -- --domain billing.example.com

set -euo pipefail

WHCMS_VERSION="${WHCMS_VERSION:-latest}"
WHCMS_HOME="${WHCMS_HOME:-$HOME/.whcms}"
GITHUB_REPO="tsdlamongan/whcms"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info()  { echo -e "${BLUE}[INFO]${NC}  $*"; }
ok()    { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

banner() {
    echo ""
    echo -e "${GREEN}"
    echo "  ╦ ╦┌─┐┌─┐┌┬┐┌─┐┌─┐╔═╗"
    echo "  ║║║├┤ ├─┘ ││├─┤└─┐║ ╦"
    echo "  ╚╩╝└─┘┴   ┴┴ ┴└─┘╚═╝"
    echo -e "${NC}"
    echo "  Open-Source Hosting Billing Platform"
    echo "  Installer v${WHCMS_VERSION}"
    echo ""
}

check_root() {
    if [ "$(id -u)" -eq 0 ]; then
        SUDO=""
    else
        if command -v sudo >/dev/null 2>&1; then
            SUDO="sudo"
        else
            error "This script requires root privileges. Run with sudo or as root."
        fi
    fi
}

check_os() {
    if [ ! -f /etc/os-release ]; then
        error "Cannot detect OS. Only Linux is supported by this installer."
    fi

    . /etc/os-release

    case "$ID" in
        ubuntu|debian)
            PKG_MANAGER="apt-get"
            PKG_INSTALL="install"
            ;;
        centos|rhel|rocky|almalinux)
            PKG_MANAGER="yum"
            PKG_INSTALL="install"
            ;;
        fedora)
            PKG_MANAGER="dnf"
            PKG_INSTALL="install"
            ;;
        *)
            error "Unsupported OS: $ID. Supported: Ubuntu, Debian, CentOS, RHEL, Rocky, Alma, Fedora."
            ;;
    esac

    info "Detected OS: $PRETTY_NAME"
}

check_arch() {
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64)  GOARCH="amd64" ;;
        aarch64) GOARCH="arm64" ;;
        armv7l)  GOARCH="arm" ;;
        *)       error "Unsupported architecture: $ARCH" ;;
    esac
    info "Architecture: $ARCH ($GOARCH)"
}

install_prerequisites() {
    info "Installing prerequisites..."

    case "$PKG_MANAGER" in
        apt-get)
            $SUDO apt-get update -qq
            $SUDO apt-get install -y -qq curl wget tar gzip ca-certificates apt-transport-https software-properties-common
            ;;
        yum|dnf)
            $SUDO $PKG_MANAGER install -y -q curl wget tar gzip ca-certificates
            ;;
    esac

    ok "Prerequisites installed"
}

install_docker() {
    if command -v docker >/dev/null 2>&1; then
        ok "Docker already installed: $(docker --version)"
        return
    fi

    info "Installing Docker..."

    case "$ID" in
        ubuntu|debian)
            curl -fsSL https://download.docker.com/linux/$ID/gpg | $SUDO gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg
            echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/$ID $(lsb_release -cs) stable" | $SUDO tee /etc/apt/sources.list.d/docker.list > /dev/null
            $SUDO apt-get update -qq
            $SUDO apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-compose-plugin
            ;;
        centos|rhel|rocky|almalinux|fedora)
            $SUDO $PKG_MANAGER install -y -q yum-utils 2>/dev/null || true
            $SUDO yum-config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo 2>/dev/null || \
            $SUDO dnf config-manager --add-repo https://download.docker.com/linux/fedora/docker-ce.repo
            $SUDO $PKG_MANAGER install -y -q docker-ce docker-ce-cli containerd.io docker-compose-plugin
            ;;
    esac

    $SUDO systemctl enable --now docker
    ok "Docker installed: $(docker --version)"
}

install_go() {
    if command -v go >/dev/null 2>&1; then
        GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
        ok "Go already installed: $GO_VERSION"
        return
    fi

    info "Installing Go 1.26..."

    GO_TARBALL="go1.26.0.linux-${GOARCH}.tar.gz"
    GO_URL="https://go.dev/dl/${GO_TARBALL}"

    curl -fsSL "$GO_URL" -o /tmp/go.tar.gz
    $SUDO rm -rf /usr/local/go
    $SUDO tar -C /usr/local -xzf /tmp/go.tar.gz
    rm -f /tmp/go.tar.gz

    export PATH="/usr/local/go/bin:$PATH"

    if [ ! -f /etc/profile.d/golang.sh ]; then
        echo 'export PATH="/usr/local/go/bin:$PATH"' | $SUDO tee /etc/profile.d/golang.sh > /dev/null
    fi

    ok "Go installed: $(go version)"
}

download_binaries() {
    info "Downloading WHCMS binaries..."

    if [ "$WHCMS_VERSION" = "latest" ]; then
        RELEASE_URL="https://github.com/${GITHUB_REPO}/releases/latest/download"
    else
        RELEASE_URL="https://github.com/${GITHUB_REPO}/releases/download/${WHCMS_VERSION}"
    fi

    ARCHIVE_NAME="whcms-${WHCMS_VERSION}-${GOARCH}.tar.gz"
    DOWNLOAD_URL="${RELEASE_URL}/${ARCHIVE_NAME}"

    info "Downloading ${ARCHIVE_NAME}..."
    curl -fsSL "$DOWNLOAD_URL" -o /tmp/whcms.tar.gz

    info "Extracting binaries..."
    tar -xzf /tmp/whcms.tar.gz -C "$WHCMS_HOME/bin"
    rm /tmp/whcms.tar.gz

    chmod +x "$WHCMS_HOME/bin/"*
    ok "WHCMS binaries downloaded"
}

download_and_build() {
    if [ "$WHCMS_VERSION" != "source" ]; then
        download_binaries
        return
    fi

    info "Building from source..."

    BUILD_DIR=$(mktemp -d)
    cd "$BUILD_DIR"

    if [ "$WHCMS_VERSION" = "latest" ]; then
        curl -fsSL "https://github.com/${GITHUB_REPO}/archive/refs/heads/main.tar.gz" -o whcms.tar.gz
    else
        curl -fsSL "https://github.com/${GITHUB_REPO}/archive/refs/tags/${WHCMS_VERSION}.tar.gz" -o whcms.tar.gz
    fi

    tar -xzf whcms.tar.gz --strip-components=1
    rm whcms.tar.gz

    info "Building WHCMS binaries..."
    cd backend

    export PATH="/usr/local/go/bin:$PATH"
    go build -o "$WHCMS_HOME/bin/whcms-api"    ./cmd/api
    go build -o "$WHCMS_HOME/bin/whcms-worker" ./cmd/worker
    go build -o "$WHCMS_HOME/bin/whcms-cli"    ./cmd/cli

    cd "$BUILD_DIR/frontend"
    if command -v node >/dev/null 2>&1; then
        info "Building frontend..."
        npm install --silent
        npm run build
        cp -r build "$WHCMS_HOME/bin/frontend"
    else
        warn "Node.js not found, skipping frontend build"
    fi

    cd /
    rm -rf "$BUILD_DIR"

    chmod +x "$WHCMS_HOME/bin/"*
    ok "WHCMS binaries built from source"
}

setup_directories() {
    info "Setting up directories..."

    mkdir -p "$WHCMS_HOME"/{bin,config,data,logs,backups}
    ok "Directories created"
}

generate_secrets() {
    info "Generating secrets..."

    JWT_SECRET=$(openssl rand -base64 32)
    ENC_KEY=$(openssl rand -base64 32)
    DB_PASS=$(openssl rand -base64 16 | tr -d '/+=')
    REDIS_PASS=$(openssl rand -base64 16 | tr -d '/+=')
    RUSTFS_SECRET=$(openssl rand -base64 16 | tr -d '/+=')

    ok "Secrets generated"
}

write_config() {
    info "Writing configuration..."

    HOSTNAME=$(hostname -f 2>/dev/null || hostname)
    DOMAIN="${1:-$HOSTNAME}"

    cat > "$WHCMS_HOME/config/whcms.env" <<EOF
# WHCMS Configuration
# Generated by install.sh on $(date -Iseconds)

APP_ENV=production
APP_PORT=8080
APP_BASE_URL=http://${DOMAIN}:8080
FRONTEND_URL=http://${DOMAIN}:3000

JWT_SECRET=${JWT_SECRET}
APP_ENCRYPTION_KEY=${ENC_KEY}

DATABASE_URL=postgres://whcms:${DB_PASS}@localhost:5432/whcms?sslmode=disable
REDIS_ADDR=localhost:6379
REDIS_PASSWORD=${REDIS_PASS}
REDIS_DB=0

RUSTFS_ENDPOINT=http://localhost:9000
RUSTFS_ACCESS_KEY=whcms
RUSTFS_SECRET_KEY=${RUSTFS_SECRET}
RUSTFS_BUCKET=whmcs
RUSTFS_USE_SSL=false

MAIL_DRIVER=log

WORKER_CONCURRENCY=10
ADMIN_ALERT_EMAIL=admin@${DOMAIN}
EOF

    ok "Configuration written to $WHCMS_HOME/config/whcms.env"
}

write_compose() {
    info "Writing Docker Compose for infrastructure..."

    cat > "$WHCMS_HOME/config/docker-compose.yml" <<EOF
version: '3.8'

services:
  postgres:
    image: postgres:18-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: whcms
      POSTGRES_PASSWORD: ${DB_PASS}
      POSTGRES_DB: whcms
    volumes:
      - ${WHCMS_HOME}/data/postgres:/var/lib/postgresql
    ports:
      - "127.0.0.1:5432:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U whcms -d whcms"]
      interval: 10s
      timeout: 5s
      retries: 10

  redis:
    image: redis:7-alpine
    restart: unless-stopped
    command: redis-server --appendonly yes --requirepass ${REDIS_PASS}
    volumes:
      - ${WHCMS_HOME}/data/redis:/data
    ports:
      - "127.0.0.1:6379:6379"
    healthcheck:
      test: ["CMD-SHELL", "redis-cli -a ${REDIS_PASS} ping | grep -q PONG"]
      interval: 10s
      timeout: 5s
      retries: 10

  rustfs:
    image: rustfs/rustfs:latest
    restart: unless-stopped
    environment:
      RUSTFS_ACCESS_KEY: whcms
      RUSTFS_SECRET_KEY: ${RUSTFS_SECRET}
    volumes:
      - ${WHCMS_HOME}/data/rustfs:/data
    ports:
      - "127.0.0.1:9000:9000"
    healthcheck:
      test: ["CMD-SHELL", "curl -sf http://127.0.0.1:9000/health || exit 1"]
      interval: 15s
      timeout: 5s
      retries: 12
      start_period: 15s
EOF

    ok "Docker Compose written"
}

start_infrastructure() {
    info "Starting infrastructure (Postgres, Redis, RustFS)..."

    docker compose -f "$WHCMS_HOME/config/docker-compose.yml" up -d

    info "Waiting for services to be ready..."
    sleep 5

    for i in $(seq 1 30); do
        if docker compose -f "$WHCMS_HOME/config/docker-compose.yml" exec -T postgres pg_isready -U whcms >/dev/null 2>&1; then
            break
        fi
        sleep 1
    done

    ok "Infrastructure running"
}

run_migrations() {
    info "Running database migrations..."

    export ENV_FILE="$WHCMS_HOME/config/whcms.env"
    "$WHCMS_HOME/bin/whcms-api" &
    API_PID=$!

    sleep 3
    kill $API_PID 2>/dev/null || true
    wait $API_PID 2>/dev/null || true

    unset ENV_FILE
    ok "Migrations applied"
}

install_systemd_services() {
    if ! command -v systemctl >/dev/null 2>&1; then
        warn "systemd not available, skipping service installation"
        return
    fi

    info "Installing systemd services..."

    cat | $SUDO tee /etc/systemd/system/whcms-api.service > /dev/null <<EOF
[Unit]
Description=WHCMS API Server
After=network.target docker.service
Requires=docker.service

[Service]
Type=simple
User=$(whoami)
WorkingDirectory=${WHCMS_HOME}
EnvironmentFile=${WHCMS_HOME}/config/whcms.env
ExecStart=${WHCMS_HOME}/bin/whcms-api
Restart=always
RestartSec=10
StandardOutput=append:${WHCMS_HOME}/logs/api.log
StandardError=append:${WHCMS_HOME}/logs/api.log

[Install]
WantedBy=multi-user.target
EOF

    cat | $SUDO tee /etc/systemd/system/whcms-worker.service > /dev/null <<EOF
[Unit]
Description=WHCMS Worker
After=network.target docker.service whcms-api.service
Requires=docker.service

[Service]
Type=simple
User=$(whoami)
WorkingDirectory=${WHCMS_HOME}
EnvironmentFile=${WHCMS_HOME}/config/whcms.env
ExecStart=${WHCMS_HOME}/bin/whcms-worker
Restart=always
RestartSec=10
StandardOutput=append:${WHCMS_HOME}/logs/worker.log
StandardError=append:${WHCMS_HOME}/logs/worker.log

[Install]
WantedBy=multi-user.target
EOF

    $SUDO systemctl daemon-reload
    $SUDO systemctl enable whcms-api whcms-worker
    $SUDO systemctl start whcms-api whcms-worker

    ok "Systemd services installed and started"
}

install_cli() {
    info "Installing whcms CLI to /usr/local/bin/whcms..."

    $SUDO cp "$WHCMS_HOME/bin/whcms-cli" /usr/local/bin/whcms
    $SUDO chmod +x /usr/local/bin/whcms

    ok "CLI installed: whcms"
}

print_summary() {
    HOSTNAME=$(hostname -f 2>/dev/null || hostname)

    echo ""
    echo -e "${GREEN}╔══════════════════════════════════════════════════════╗${NC}"
    echo -e "${GREEN}║          WHCMS Installation Complete! 🎉            ║${NC}"
    echo -e "${GREEN}╠══════════════════════════════════════════════════════╣${NC}"
    echo -e "${GREEN}║${NC}                                                          ${GREEN}║${NC}"
    echo -e "${GREEN}║${NC}  Access WHCMS:                                           ${GREEN}║${NC}"
    echo -e "${GREEN}║${NC}    http://${HOSTNAME}:8080/install                       ${GREEN}║${NC}"
    echo -e "${GREEN}║${NC}                                                          ${GREEN}║${NC}"
    echo -e "${GREEN}║${NC}  Configuration: $WHCMS_HOME/config/whcms.env     ${GREEN}║${NC}"
    echo -e "${GREEN}║${NC}  Logs:          $WHCMS_HOME/logs/                ${GREEN}║${NC}"
    echo -e "${GREEN}║${NC}                                                          ${GREEN}║${NC}"
    echo -e "${GREEN}║${NC}  Useful commands:                                        ${GREEN}║${NC}"
    echo -e "${GREEN}║${NC}    whcms status       - Check service status             ${GREEN}║${NC}"
    echo -e "${GREEN}║${NC}    whcms logs         - View logs                        ${GREEN}║${NC}"
    echo -e "${GREEN}║${NC}    whcms update       - Update to latest version         ${GREEN}║${NC}"
    echo -e "${GREEN}║${NC}    whcms backup       - Backup database & files          ${GREEN}║${NC}"
    echo -e "${GREEN}║${NC}    whcms reset-admin  - Reset admin password             ${GREEN}║${NC}"
    echo -e "${GREEN}║${NC}                                                          ${GREEN}║${NC}"
    echo -e "${GREEN}║${NC}  Documentation: https://github.com/${GITHUB_REPO}        ${GREEN}║${NC}"
    echo -e "${GREEN}║${NC}                                                          ${GREEN}║${NC}"
    echo -e "${GREEN}╚══════════════════════════════════════════════════════╝${NC}"
    echo ""
}

main() {
    banner
    check_root
    check_os
    check_arch
    install_prerequisites
    install_docker
    
    if [ "$WHCMS_VERSION" = "source" ]; then
        install_go
    fi
    
    setup_directories
    generate_secrets
    write_config "${1:-}"
    write_compose
    download_and_build
    start_infrastructure
    run_migrations
    install_systemd_services
    install_cli
    print_summary
}

main "$@"
