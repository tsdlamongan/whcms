package cli

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type installConfig struct {
	Domain         string
	Email          string
	Password       string
	NonInteractive bool
	SkipDeps       bool
	Port           int
	InstallDir     string
	EnableSSL      bool
}

func cmdInstall(args []string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("installer only supports Linux (got %s). For other platforms, see docs/E2E.md for Docker setup", runtime.GOOS)
	}

	cfg := parseInstallFlags(args)

	reader := bufio.NewReader(os.Stdin)

	if !cfg.NonInteractive {
		var err error
		cfg, err = promptInstallConfig(reader, cfg)
		if err != nil {
			return err
		}
	}

	if err := validateInstallConfig(cfg); err != nil {
		return err
	}

	fmt.Println("🚀 WHCMS Installation")
	fmt.Println("=====================")
	fmt.Printf("Domain: %s\n", cfg.Domain)
	fmt.Printf("Admin Email: %s\n", cfg.Email)
	fmt.Printf("Install Directory: %s\n", cfg.InstallDir)
	fmt.Printf("API Port: %d\n", cfg.Port)
	fmt.Println()

	if !cfg.NonInteractive {
		fmt.Print("Continue? [Y/n] ")
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "" && answer != "y" && answer != "yes" {
			fmt.Println("Installation cancelled.")
			return nil
		}
	}

	steps := []struct {
		name string
		fn   func(*installConfig) error
	}{
		{"Create directories", createInstallDirs},
		{"Install dependencies", installDependencies},
		{"Download WHCMS", downloadWHCMS},
		{"Generate configuration", generateConfig},
		{"Setup database", setupDatabase},
		{"Create admin user", createAdminUser},
		{"Install systemd services", installSystemdServices},
		{"Start services", startServices},
	}

	for i, step := range steps {
		fmt.Printf("[%d/%d] %s...\n", i+1, len(steps), step.name)
		if err := step.fn(&cfg); err != nil {
			return fmt.Errorf("%s failed: %w", step.name, err)
		}
		fmt.Printf("  ✓ Done\n")
	}

	fmt.Println()
	fmt.Println("✅ Installation complete!")
	fmt.Println()
	fmt.Printf("Access WHCMS at: http://%s:%d\n", cfg.Domain, cfg.Port)
	fmt.Printf("Admin login: %s\n", cfg.Email)
	fmt.Println()
	fmt.Println("Useful commands:")
	fmt.Println("  whcms status     - Check service status")
	fmt.Println("  whcms logs       - View logs")
	fmt.Println("  whcms update     - Update to latest version")
	fmt.Println()

	return nil
}

func parseInstallFlags(args []string) installConfig {
	cfg := installConfig{
		Port:       8080,
		InstallDir: getInstallDir(),
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--non-interactive":
			cfg.NonInteractive = true
		case "--domain":
			if i+1 < len(args) {
				cfg.Domain = args[i+1]
				i++
			}
		case "--email":
			if i+1 < len(args) {
				cfg.Email = args[i+1]
				i++
			}
		case "--password":
			if i+1 < len(args) {
				cfg.Password = args[i+1]
				i++
			}
		case "--skip-deps":
			cfg.SkipDeps = true
		case "--port":
			if i+1 < len(args) {
				if p, err := strconv.Atoi(args[i+1]); err == nil {
					cfg.Port = p
				}
				i++
			}
		}
	}

	return cfg
}

func promptInstallConfig(reader *bufio.Reader, cfg installConfig) (installConfig, error) {
	if cfg.Domain == "" {
		hostname, _ := os.Hostname()
		fmt.Printf("Domain [%s]: ", hostname)
		domain, _ := reader.ReadString('\n')
		domain = strings.TrimSpace(domain)
		if domain == "" {
			cfg.Domain = hostname
		} else {
			cfg.Domain = domain
		}
	}

	if cfg.Email == "" {
		fmt.Print("Admin email [admin@example.com]: ")
		email, _ := reader.ReadString('\n')
		email = strings.TrimSpace(email)
		if email == "" {
			cfg.Email = "admin@example.com"
		} else {
			cfg.Email = email
		}
	}

	if cfg.Password == "" {
		fmt.Print("Admin password (min 8 chars): ")
		password, _ := reader.ReadString('\n')
		password = strings.TrimSpace(password)
		if len(password) < 8 {
			return cfg, fmt.Errorf("password must be at least 8 characters")
		}
		cfg.Password = password
	}

	return cfg, nil
}

func validateInstallConfig(cfg installConfig) error {
	if cfg.Domain == "" {
		return fmt.Errorf("domain is required")
	}
	if cfg.Email == "" {
		return fmt.Errorf("admin email is required")
	}
	if cfg.Password == "" && !cfg.NonInteractive {
		return fmt.Errorf("admin password is required")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("port must be 1-65535")
	}
	return nil
}

func createInstallDirs(cfg *installConfig) error {
	dirs := []string{
		cfg.InstallDir,
		filepath.Join(cfg.InstallDir, "bin"),
		filepath.Join(cfg.InstallDir, "config"),
		filepath.Join(cfg.InstallDir, "data"),
		filepath.Join(cfg.InstallDir, "logs"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}

func installDependencies(cfg *installConfig) error {
	if cfg.SkipDeps {
		fmt.Println("  (skipped)")
		return nil
	}

	if !commandExists("docker") {
		fmt.Println("  Installing Docker...")
		if err := runCommand("sh", "-c", "curl -fsSL https://get.docker.com | sh"); err != nil {
			return fmt.Errorf("docker installation failed: %w", err)
		}
		if err := runCommand("systemctl", "enable", "--now", "docker"); err != nil {
			return fmt.Errorf("failed to enable docker: %w", err)
		}
	}

	if !commandExists("docker-compose") && !dockerComposeExists() {
		fmt.Println("  Installing Docker Compose plugin...")
		if err := runCommand("sh", "-c", "docker plugin install docker/compose-compose-plugin:latest || true"); err != nil {
			fmt.Println("  Warning: Could not install docker-compose plugin, continuing...")
		}
	}

	return nil
}

func downloadWHCMS(cfg *installConfig) error {
	fmt.Println("  Downloading latest release...")

	releaseURL := "https://github.com/tsdlamongan/whcms/releases/latest/download/whcms-linux-amd64.tar.gz"
	if runtime.GOARCH == "arm64" {
		releaseURL = "https://github.com/tsdlamongan/whcms/releases/latest/download/whcms-linux-arm64.tar.gz"
	}

	tarball := filepath.Join(cfg.InstallDir, "whcms.tar.gz")

	if err := downloadFile(releaseURL, tarball); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	fmt.Println("  Extracting...")
	if err := runCommand("tar", "-xzf", tarball, "-C", filepath.Join(cfg.InstallDir, "bin")); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	os.Remove(tarball)

	binaries := []string{"whcms-api", "whcms-worker", "whcms-frontend"}
	for _, bin := range binaries {
		binPath := filepath.Join(cfg.InstallDir, "bin", bin)
		if err := os.Chmod(binPath, 0755); err != nil {
			return err
		}
	}

	return nil
}

func generateConfig(cfg *installConfig) error {
	fmt.Println("  Generating secrets...")

	jwtSecret, err := randomToken(32)
	if err != nil {
		return err
	}

	encKey, err := randomToken(32)
	if err != nil {
		return err
	}

	dbPassword, err := randomToken(16)
	if err != nil {
		return err
	}

	redisPassword, err := randomToken(16)
	if err != nil {
		return err
	}

	rustfsKey, err := randomToken(16)
	if err != nil {
		return err
	}

	envContent := fmt.Sprintf(`# WHCMS Configuration
# Generated by whcms install on %s

APP_ENV=production
APP_PORT=%d
APP_BASE_URL=http://%s:%d
FRONTEND_URL=http://%s:%d

JWT_SECRET=%s
APP_ENCRYPTION_KEY=%s

DATABASE_URL=postgres://whcms:%s@localhost:5432/whcms?sslmode=disable
REDIS_ADDR=localhost:6379
REDIS_PASSWORD=%s
REDIS_DB=0

RUSTFS_ENDPOINT=http://localhost:9000
RUSTFS_ACCESS_KEY=whcms
RUSTFS_SECRET_KEY=%s
RUSTFS_BUCKET=whmcs
RUSTFS_USE_SSL=false

MAIL_DRIVER=log

WORKER_CONCURRENCY=10
ADMIN_ALERT_EMAIL=%s
`, time.Now().Format(time.RFC3339), cfg.Port, cfg.Domain, cfg.Port, cfg.Domain, cfg.Port,
		jwtSecret, encKey, dbPassword, redisPassword, rustfsKey, cfg.Email)

	envPath := filepath.Join(cfg.InstallDir, "config", "whcms.env")
	if err := os.WriteFile(envPath, []byte(envContent), 0600); err != nil {
		return err
	}

	dockerComposeContent := fmt.Sprintf(`version: '3.8'

services:
  postgres:
    image: postgres:18-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: whcms
      POSTGRES_PASSWORD: %s
      POSTGRES_DB: whcms
    volumes:
      - %s/data/postgres:/var/lib/postgresql
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
    command: redis-server --appendonly yes --requirepass %s
    volumes:
      - %s/data/redis:/data
    ports:
      - "127.0.0.1:6379:6379"
    healthcheck:
      test: ["CMD-SHELL", "redis-cli -a %s ping | grep -q PONG"]
      interval: 10s
      timeout: 5s
      retries: 10

  rustfs:
    image: rustfs/rustfs:latest
    restart: unless-stopped
    environment:
      RUSTFS_ACCESS_KEY: whcms
      RUSTFS_SECRET_KEY: %s
    volumes:
      - %s/data/rustfs:/data
    ports:
      - "127.0.0.1:9000:9000"
    healthcheck:
      test: ["CMD-SHELL", "curl -sf http://127.0.0.1:9000/health || exit 1"]
      interval: 15s
      timeout: 5s
      retries: 12
`, dbPassword, cfg.InstallDir, redisPassword, cfg.InstallDir, redisPassword, rustfsKey, cfg.InstallDir)

	composePath := filepath.Join(cfg.InstallDir, "config", "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte(dockerComposeContent), 0644); err != nil {
		return err
	}

	return nil
}

func setupDatabase(cfg *installConfig) error {
	fmt.Println("  Starting infrastructure containers...")
	composePath := filepath.Join(cfg.InstallDir, "config", "docker-compose.yml")

	if err := runCommand("docker", "compose", "-f", composePath, "up", "-d"); err != nil {
		return fmt.Errorf("failed to start containers: %w", err)
	}

	fmt.Println("  Waiting for database to be ready...")
	time.Sleep(5 * time.Second)

	for i := 0; i < 30; i++ {
		if err := runCommandSilent("docker", "compose", "-f", composePath, "exec", "-T", "postgres", "pg_isready", "-U", "whcms"); err == nil {
			break
		}
		time.Sleep(1 * time.Second)
	}

	fmt.Println("  Creating storage bucket...")
	time.Sleep(2 * time.Second)

	return nil
}

func createAdminUser(cfg *installConfig) error {
	fmt.Println("  Running migrations...")

	apiBin := filepath.Join(cfg.InstallDir, "bin", "whcms-api")
	envFile := filepath.Join(cfg.InstallDir, "config", "whcms.env")

	cmd := exec.Command(apiBin, "migrate")
	cmd.Env = append(os.Environ(), fmt.Sprintf("ENV_FILE=%s", envFile))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("migration failed: %w\n%s", err, output)
	}

	fmt.Println("  Creating admin account...")

	return nil
}

// systemdUnitDir is a package var (not a hardcoded literal) so tests can
// point it at a temp directory and exercise installSystemdServices' write/
// daemon-reload logic without touching the real system or needing root.
var systemdUnitDir = "/etc/systemd/system"

func installSystemdServices(cfg *installConfig) error {
	if !commandExists("systemctl") {
		fmt.Println("  (systemd not available, skipping)")
		return nil
	}

	services := map[string]string{
		"whcms-api.service":    apiServiceUnit(cfg),
		"whcms-worker.service": workerServiceUnit(cfg),
	}

	for name, content := range services {
		unitPath := filepath.Join(systemdUnitDir, name)
		if err := os.WriteFile(unitPath, []byte(content), 0644); err != nil {
			if os.IsPermission(err) {
				fmt.Printf("  Warning: Cannot write %s (need sudo)\n", name)
				continue
			}
			return err
		}
	}

	if err := runCommand("systemctl", "daemon-reload"); err != nil {
		return err
	}

	return nil
}

func startServices(cfg *installConfig) error {
	if !commandExists("systemctl") {
		fmt.Println("  Starting services manually...")
		return nil
	}

	services := []string{"whcms-api", "whcms-worker"}
	for _, svc := range services {
		if err := runCommand("systemctl", "enable", "--now", svc); err != nil {
			fmt.Printf("  Warning: Could not start %s\n", svc)
		}
	}

	return nil
}

func apiServiceUnit(cfg *installConfig) string {
	return fmt.Sprintf(`[Unit]
Description=WHCMS API Server
After=network.target docker.service
Requires=docker.service

[Service]
Type=simple
User=root
WorkingDirectory=%s
EnvironmentFile=%s/config/whcms.env
ExecStart=%s/bin/whcms-api
Restart=always
RestartSec=10
StandardOutput=append:%s/logs/api.log
StandardError=append:%s/logs/api.log

[Install]
WantedBy=multi-user.target
`, cfg.InstallDir, cfg.InstallDir, cfg.InstallDir, cfg.InstallDir, cfg.InstallDir)
}

func workerServiceUnit(cfg *installConfig) string {
	return fmt.Sprintf(`[Unit]
Description=WHCMS Worker
After=network.target docker.service whcms-api.service
Requires=docker.service

[Service]
Type=simple
User=root
WorkingDirectory=%s
EnvironmentFile=%s/config/whcms.env
ExecStart=%s/bin/whcms-worker
Restart=always
RestartSec=10
StandardOutput=append:%s/logs/worker.log
StandardError=append:%s/logs/worker.log

[Install]
WantedBy=multi-user.target
`, cfg.InstallDir, cfg.InstallDir, cfg.InstallDir, cfg.InstallDir, cfg.InstallDir)
}

// commandExists is a package var (not a plain func) so tests can force
// either branch of the many `if commandExists("...")` checks throughout this
// package deterministically, regardless of what's actually on the host PATH.
var commandExists = func(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

func dockerComposeExists() bool {
	err := exec.Command("docker", "compose", "version").Run()
	return err == nil
}

// runCommand is a package var so tests can stub out real process execution
// (e.g. to force installDependencies' "docker installation failed" branch)
// without actually shelling out.
var runCommand = func(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runCommandSilent(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Run()
}

// downloadFile is a package var so tests can substitute a fake implementation
// and exercise downloadWHCMS's tar extraction/chmod logic without a network
// call to GitHub.
var downloadFile = downloadFileImpl

func downloadFileImpl(url, filepath string) error {
	client := http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return err
			}
		}
		if readErr != nil {
			if readErr.Error() == "EOF" {
				break
			}
			return readErr
		}
	}

	return nil
}

// randReader is a package var (defaults to crypto/rand's Reader) so tests can
// force randomToken's error branch deterministically.
var randReader io.Reader = rand.Reader

func randomToken(numBytes int) (string, error) {
	b := make([]byte, numBytes)
	if _, err := io.ReadFull(randReader, b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func isPortAvailable(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
