// Package cli implements the `whcms` command-line tool for installing,
// updating, and managing a WHCMS deployment. It provides commands for
// one-command installation, auto-update from GitHub Releases, service
// status/logs, backup/restore, and admin password reset.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	Version   = "0.1.0"
	BuildDate = "dev"
	GitCommit = "dev"
)

func Run(args []string) error {
	if len(args) < 2 {
		return printUsage()
	}

	cmd := args[1]
	switch cmd {
	case "version", "--version", "-v":
		return cmdVersion()
	case "help", "--help", "-h":
		return printUsage()
	case "install":
		return cmdInstall(args[2:])
	case "update":
		return cmdUpdate(args[2:])
	case "status":
		return cmdStatus(args[2:])
	case "logs":
		return cmdLogs(args[2:])
	case "backup":
		return cmdBackup(args[2:])
	case "restore":
		return cmdRestore(args[2:])
	case "reset-admin":
		return cmdResetAdmin(args[2:])
	case "uninstall":
		return cmdUninstall(args[2:])
	default:
		return fmt.Errorf("unknown command: %s\nRun 'whcms help' for usage", cmd)
	}
}

func printUsage() error {
	fmt.Printf(`WHCMS CLI v%s (%s) - Open-source hosting billing platform

Usage:
  whcms <command> [options]

Commands:
  install       Install WHCMS (Postgres, Redis, RustFS, build binaries, setup services)
  update        Update WHCMS to the latest version
  status        Show status of all WHCMS services
  logs          Tail logs from WHCMS services
  backup        Backup database and storage
  restore       Restore from backup
  reset-admin   Reset admin password
  uninstall     Remove WHCMS installation
  version       Show version information
  help          Show this help message

Install Options:
  --non-interactive       Skip prompts, use defaults
  --domain <domain>       Set domain (e.g., billing.example.com)
  --email <email>         Admin email
  --password <password>   Admin password
  --skip-deps             Skip installing Postgres/Redis/RustFS (assume already installed)
  --port <port>           API port (default: 8080)

Update Options:
  --version <version>     Update to specific version (default: latest)
  --check                 Check for updates without installing

Status Options:
  --json                  Output in JSON format

Backup Options:
  --output <path>         Output file path (default: ./whcms-backup-<timestamp>.tar.gz)
  --no-storage            Skip file storage backup

Restore Options:
  --input <path>          Backup file to restore from

Examples:
  whcms install --domain billing.example.com --email admin@example.com
  whcms update
  whcms status
  whcms backup --output /backups/whcms-backup.tar.gz
  whcms reset-admin --email admin@example.com --password newpassword

Documentation: https://github.com/tsdlamongan/whcms
`, Version, shortCommit())
	return nil
}

func cmdVersion() error {
	fmt.Printf("WHCMS CLI v%s\n", Version)
	fmt.Printf("  Build date: %s\n", BuildDate)
	fmt.Printf("  Git commit: %s\n", GitCommit)
	fmt.Printf("  Go version: %s\n", runtime.Version())
	fmt.Printf("  OS/Arch:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
	return nil
}

func getInstallDir() string {
	if dir := os.Getenv("WHCMS_HOME"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".whcms")
}

func shortCommit() string {
	n := len(GitCommit)
	if n > 7 {
		n = 7
	}
	return GitCommit[:n]
}
