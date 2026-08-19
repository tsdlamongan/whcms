package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type serviceStatus struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	PID     int    `json:"pid,omitempty"`
	Uptime  string `json:"uptime,omitempty"`
	Memory  string `json:"memory,omitempty"`
	Message string `json:"message,omitempty"`
}

func cmdStatus(args []string) error {
	jsonOutput := false
	for _, arg := range args {
		if arg == "--json" {
			jsonOutput = true
		}
	}

	installDir := getInstallDir()
	statuses := []serviceStatus{}

	services := []struct {
		name string
		unit string
	}{
		{"WHCMS API", "whcms-api"},
		{"WHCMS Worker", "whcms-worker"},
		{"PostgreSQL", "postgres"},
		{"Redis", "redis"},
		{"RustFS", "rustfs"},
	}

	for _, svc := range services {
		status := getServiceStatus(svc.unit)
		statuses = append(statuses, status)
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(statuses)
	}

	fmt.Println("WHCMS Service Status")
	fmt.Println("====================")
	fmt.Printf("Install Directory: %s\n\n", installDir)

	for _, status := range statuses {
		icon := "✓"
		if status.Status != "active" {
			icon = "✗"
		}

		fmt.Printf("%s %s: %s\n", icon, status.Name, status.Status)
		if status.PID > 0 {
			fmt.Printf("  PID: %d\n", status.PID)
		}
		if status.Uptime != "" {
			fmt.Printf("  Uptime: %s\n", status.Uptime)
		}
		if status.Memory != "" {
			fmt.Printf("  Memory: %s\n", status.Memory)
		}
		if status.Message != "" {
			fmt.Printf("  %s\n", status.Message)
		}
		fmt.Println()
	}

	return nil
}

func getServiceStatus(unit string) serviceStatus {
	status := serviceStatus{Name: unit}

	if !commandExists("systemctl") {
		if unit == "whcms-api" || unit == "whcms-worker" {
			cmd := exec.Command("pgrep", "-f", unit)
			if output, err := cmd.Output(); err == nil && len(output) > 0 {
				status.Status = "active"
				status.Message = "Running (no systemd)"
			} else {
				status.Status = "inactive"
				status.Message = "Not running (no systemd)"
			}
		} else {
			cmd := exec.Command("docker", "ps", "--filter", fmt.Sprintf("name=%s", unit), "--format", "{{.Status}}")
			if output, err := cmd.Output(); err == nil && len(output) > 0 {
				status.Status = "active"
				status.Message = strings.TrimSpace(string(output))
			} else {
				status.Status = "inactive"
				status.Message = "Not running"
			}
		}
		return status
	}

	cmd := exec.Command("systemctl", "is-active", unit)
	output, _ := cmd.Output()
	status.Status = strings.TrimSpace(string(output))

	if status.Status == "active" {
		cmd = exec.Command("systemctl", "show", unit, "--property=MainPID,ActiveEnterTimestamp,MemoryCurrent")
		if output, err := cmd.Output(); err == nil {
			parseSystemctlShow(&status, string(output))
		}
	}

	return status
}

// parseSystemctlShow fills in PID/Uptime/Memory on status from the
// `KEY=VALUE`-per-line output of `systemctl show --property=...`. Split out
// from getServiceStatus so this pure parsing logic can be unit-tested with
// fabricated output, independent of whether a real systemd is available.
func parseSystemctlShow(status *serviceStatus, output string) {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := parts[0], parts[1]
		switch key {
		case "MainPID":
			_, _ = fmt.Sscanf(value, "%d", &status.PID)
		case "ActiveEnterTimestamp":
			if t, err := time.Parse("2006-01-02 15:04:05 MST", value); err == nil {
				status.Uptime = time.Since(t).Round(time.Second).String()
			}
		case "MemoryCurrent":
			var bytes int64
			if _, err := fmt.Sscanf(value, "%d", &bytes); err == nil && bytes > 0 {
				status.Memory = formatBytes(bytes)
			}
		}
	}
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func cmdLogs(args []string) error {
	installDir := getInstallDir()
	follow := false
	service := "all"

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-f", "--follow":
			follow = true
		case "--api":
			service = "api"
		case "--worker":
			service = "worker"
		}
	}

	if commandExists("journalctl") {
		journalArgs := []string{"-u"}
		switch service {
		case "api":
			journalArgs = append(journalArgs, "whcms-api")
		case "worker":
			journalArgs = append(journalArgs, "whcms-worker")
		default:
			journalArgs = append(journalArgs, "whcms-api", "-u", "whcms-worker")
		}

		if follow {
			journalArgs = append(journalArgs, "-f")
		} else {
			journalArgs = append(journalArgs, "-n", "100")
		}

		cmd := exec.Command("journalctl", journalArgs...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	logDir := filepath.Join(installDir, "logs")
	switch service {
	case "api":
		return tailLog(filepath.Join(logDir, "api.log"), follow)
	case "worker":
		return tailLog(filepath.Join(logDir, "worker.log"), follow)
	default:
		fmt.Println("=== API Logs ===")
		if err := tailLog(filepath.Join(logDir, "api.log"), false); err != nil {
			return err
		}
		fmt.Println("\n=== Worker Logs ===")
		return tailLog(filepath.Join(logDir, "worker.log"), follow)
	}
}

func tailLog(logPath string, follow bool) error {
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		fmt.Printf("Log file not found: %s\n", logPath)
		return nil
	}

	if follow {
		cmd := exec.Command("tail", "-f", logPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	cmd := exec.Command("tail", "-n", "50", logPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
