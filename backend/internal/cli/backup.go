package cli

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func cmdBackup(args []string) error {
	installDir := getInstallDir()
	outputPath := ""
	noStorage := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--output":
			if i+1 < len(args) {
				outputPath = args[i+1]
				i++
			}
		case "--no-storage":
			noStorage = true
		}
	}

	if outputPath == "" {
		timestamp := time.Now().Format("20060102-150405")
		outputPath = fmt.Sprintf("whcms-backup-%s.tar.gz", timestamp)
	}

	fmt.Println("WHCMS Backup")
	fmt.Println("============")
	fmt.Printf("Output: %s\n\n", outputPath)

	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("cannot create output file: %w", err)
	}
	defer out.Close()

	gw := gzip.NewWriter(out)
	defer func() {
		if err := gw.Close(); err != nil {
			fmt.Printf("Warning: failed to close gzip writer: %v\n", err)
		}
	}()

	tw := tar.NewWriter(gw)
	defer func() {
		if err := tw.Close(); err != nil {
			fmt.Printf("Warning: failed to close tar writer: %v\n", err)
		}
	}()

	fmt.Println("[1/3] Backing up database...")
	dbDumpPath := filepath.Join(os.TempDir(), "whcms-backup-db.sql")
	if err := dumpDatabase(installDir, dbDumpPath); err != nil {
		return fmt.Errorf("database backup failed: %w", err)
	}
	defer os.Remove(dbDumpPath)

	if err := addFileToTar(tw, dbDumpPath, "database.sql"); err != nil {
		return fmt.Errorf("failed to add database dump to archive: %w", err)
	}
	fmt.Println("  ✓ Database backed up")

	fmt.Println("[2/3] Backing up configuration...")
	configDir := filepath.Join(installDir, "config")
	if err := addDirToTar(tw, configDir, "config"); err != nil {
		fmt.Println("  Warning: Could not backup config directory")
	} else {
		fmt.Println("  ✓ Configuration backed up")
	}

	if !noStorage {
		fmt.Println("[3/3] Backing up file storage...")
		storageDir := filepath.Join(installDir, "data", "storage")
		if _, err := os.Stat(storageDir); err == nil {
			if err := addDirToTar(tw, storageDir, "storage"); err != nil {
				fmt.Println("  Warning: Could not backup storage directory")
			} else {
				fmt.Println("  ✓ File storage backed up")
			}
		} else {
			fmt.Println("  (no local storage directory found)")
		}
	} else {
		fmt.Println("[3/3] Skipping file storage (--no-storage)")
	}

	info, err := os.Stat(outputPath)
	if err == nil {
		fmt.Printf("\n✓ Backup complete: %s (%s)\n", outputPath, formatBytes(info.Size()))
	}

	return nil
}

func cmdRestore(args []string) error {
	inputPath := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--input":
			if i+1 < len(args) {
				inputPath = args[i+1]
				i++
			}
		}
	}

	if inputPath == "" {
		return fmt.Errorf("--input <path> is required")
	}

	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		return fmt.Errorf("backup file not found: %s", inputPath)
	}

	installDir := getInstallDir()

	fmt.Println("WHCMS Restore")
	fmt.Println("=============")
	fmt.Printf("Backup: %s\n", inputPath)
	fmt.Printf("Target: %s\n\n", installDir)

	fmt.Print("This will overwrite current data. Continue? [y/N] ")
	var answer string
	if _, err := fmt.Scanln(&answer); err != nil {
		fmt.Println("Restore cancelled.")
		return nil
	}
	if strings.ToLower(answer) != "y" && strings.ToLower(answer) != "yes" {
		fmt.Println("Restore cancelled.")
		return nil
	}

	fmt.Println("\n[1/3] Stopping services...")
	stopServices()

	fmt.Println("[2/3] Extracting backup...")
	tmpDir, err := os.MkdirTemp("", "whcms-restore-*")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			fmt.Printf("Warning: failed to remove temp directory: %v\n", err)
		}
	}()

	if err := extractTarGz(inputPath, tmpDir); err != nil {
		return fmt.Errorf("failed to extract backup: %w", err)
	}

	fmt.Println("[3/3] Restoring data...")

	dbDump := filepath.Join(tmpDir, "database.sql")
	if _, err := os.Stat(dbDump); err == nil {
		fmt.Println("  Restoring database...")
		if err := restoreDatabase(installDir, dbDump); err != nil {
			return fmt.Errorf("database restore failed: %w", err)
		}
		fmt.Println("  ✓ Database restored")
	}

	configDir := filepath.Join(tmpDir, "config")
	if _, err := os.Stat(configDir); err == nil {
		targetConfig := filepath.Join(installDir, "config")
		if err := os.MkdirAll(targetConfig, 0755); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}
		if err := copyDir(configDir, targetConfig); err != nil {
			return fmt.Errorf("failed to copy config: %w", err)
		}
		fmt.Println("  ✓ Configuration restored")
	}

	storageDir := filepath.Join(tmpDir, "storage")
	if _, err := os.Stat(storageDir); err == nil {
		targetStorage := filepath.Join(installDir, "data", "storage")
		if err := os.MkdirAll(targetStorage, 0755); err != nil {
			return fmt.Errorf("failed to create storage directory: %w", err)
		}
		if err := copyDir(storageDir, targetStorage); err != nil {
			return fmt.Errorf("failed to copy storage: %w", err)
		}
		fmt.Println("  ✓ File storage restored")
	}

	fmt.Println("\nStarting services...")
	if err := startServices(nil); err != nil {
		fmt.Printf("Warning: failed to start services: %v\n", err)
	}

	fmt.Println("\n✓ Restore complete")
	return nil
}

// dumpDatabase/restoreDatabase are package vars (not plain funcs) so tests
// can substitute a fake implementation and exercise cmdBackup/cmdRestore's
// surrounding orchestration (archive layout, prompts, service stop/start)
// without needing a real pg_dump/psql/docker toolchain.
var dumpDatabase = dumpDatabaseImpl

func dumpDatabaseImpl(installDir, outputPath string) error {
	envFile := filepath.Join(installDir, "config", "whcms.env")
	dbURL := getEnvValue(envFile, "DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://whcms:whcms@localhost:5432/whcms?sslmode=disable"
	}

	if commandExists("docker") {
		cmd := exec.Command("docker", "compose", "-f", filepath.Join(installDir, "config", "docker-compose.yml"),
			"exec", "-T", "postgres", "pg_dump", "-U", "whcms", "-d", "whcms")
		out, err := cmd.Output()
		if err != nil {
			return err
		}
		return os.WriteFile(outputPath, out, 0644)
	}

	cmd := exec.Command("pg_dump", dbURL)
	output, err := cmd.Output()
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, output, 0644)
}

var restoreDatabase = restoreDatabaseImpl

func restoreDatabaseImpl(installDir, dumpPath string) error {
	if commandExists("docker") {
		composePath := filepath.Join(installDir, "config", "docker-compose.yml")
		cmd := exec.Command("docker", "compose", "-f", composePath,
			"exec", "-T", "postgres", "psql", "-U", "whcms", "-d", "whcms")
		cmd.Stdin, _ = os.Open(dumpPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	envFile := filepath.Join(installDir, "config", "whcms.env")
	dbURL := getEnvValue(envFile, "DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://whcms:whcms@localhost:5432/whcms?sslmode=disable"
	}

	cmd := exec.Command("psql", dbURL)
	cmd.Stdin, _ = os.Open(dumpPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func extractTarGz(tarball, targetDir string) error {
	f, err := os.Open(tarball)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() {
		if err := gr.Close(); err != nil {
			fmt.Printf("Warning: failed to close gzip reader: %v\n", err)
		}
	}()

	tr := tar.NewReader(gr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		targetPath := filepath.Join(targetDir, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}

	return nil
}

func addFileToTar(tw *tar.Writer, filePath, name string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = name

	if err := tw.WriteHeader(header); err != nil {
		return err
	}

	_, err = io.Copy(tw, f)
	return err
}

func addDirToTar(tw *tar.Writer, srcDir, prefix string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		header.Name = filepath.Join(prefix, relPath)

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(tw, f)
		return err
	})
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		targetPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(targetPath, 0755)
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		dstFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer dstFile.Close()

		_, err = io.Copy(dstFile, srcFile)
		return err
	})
}

func getEnvValue(envFile, key string) string {
	data, err := os.ReadFile(envFile)
	if err != nil {
		return ""
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == key {
			return strings.TrimSpace(parts[1])
		}
	}

	return ""
}
