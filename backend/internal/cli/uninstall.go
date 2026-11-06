package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func cmdUninstall(args []string) error {
	force := false
	for _, arg := range args {
		if arg == "--force" || arg == "-y" {
			force = true
		}
	}

	installDir := getInstallDir()

	if !force {
		fmt.Println("WHCMS Uninstall")
		fmt.Println("===============")
		fmt.Printf("This will remove:\n")
		fmt.Printf("  - WHCMS binaries from %s\n", installDir)
		fmt.Printf("  - Systemd service files\n")
		fmt.Printf("  - Configuration files\n\n")
		fmt.Print("Are you sure? This cannot be undone. [y/N] ")

		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Println("Uninstall cancelled.")
			return nil
		}
	}

	fmt.Println("\n[1/4] Stopping services...")
	services := []string{"whcms-api", "whcms-worker"}
	for _, svc := range services {
		if err := exec.Command("systemctl", "stop", svc).Run(); err != nil {
			fmt.Printf("Warning: failed to stop %s: %v\n", svc, err)
		}
		if err := exec.Command("systemctl", "disable", svc).Run(); err != nil {
			fmt.Printf("Warning: failed to disable %s: %v\n", svc, err)
		}
	}

	fmt.Println("[2/4] Removing systemd services...")
	unitFiles := []string{"/etc/systemd/system/whcms-api.service", "/etc/systemd/system/whcms-worker.service"}
	for _, unit := range unitFiles {
		if err := os.Remove(unit); err != nil && !os.IsNotExist(err) {
			fmt.Printf("Warning: failed to remove %s: %v\n", unit, err)
		}
	}
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		fmt.Printf("Warning: failed to reload systemd: %v\n", err)
	}

	fmt.Println("[3/4] Stopping infrastructure containers...")
	composePath := filepath.Join(installDir, "config", "docker-compose.yml")
	if _, err := os.Stat(composePath); err == nil {
		if err := exec.Command("docker", "compose", "-f", composePath, "down").Run(); err != nil {
			fmt.Printf("Warning: failed to stop containers: %v\n", err)
		}
	}

	fmt.Println("[4/4] Removing installation directory...")
	if err := os.RemoveAll(installDir); err != nil {
		return fmt.Errorf("failed to remove %s: %w", installDir, err)
	}

	fmt.Println("\n✓ WHCMS has been uninstalled")
	fmt.Println("\nNote: Docker images and volumes were not removed.")
	fmt.Println("To clean up completely, run:")
	fmt.Println("  docker system prune -a")
	fmt.Println("  docker volume prune")

	return nil
}
