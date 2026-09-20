package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type githubRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Body    string `json:"body"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
	PublishedAt string `json:"published_at"`
}

func cmdUpdate(args []string) error {
	checkOnly := false
	targetVersion := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--check":
			checkOnly = true
		case "--version":
			if i+1 < len(args) {
				targetVersion = args[i+1]
				i++
			}
		}
	}

	fmt.Println("Checking for updates...")

	release, err := getLatestRelease()
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	currentVersion := Version

	fmt.Printf("Current version: %s\n", currentVersion)
	fmt.Printf("Latest version:  %s\n", latestVersion)

	if latestVersion == currentVersion && targetVersion == "" {
		fmt.Println("✓ Already up to date")
		return nil
	}

	if checkOnly {
		if latestVersion != currentVersion {
			fmt.Printf("\nUpdate available: %s -> %s\n", currentVersion, latestVersion)
			fmt.Println("Run 'whcms update' to install")
		}
		return nil
	}

	if targetVersion != "" {
		latestVersion = strings.TrimPrefix(targetVersion, "v")
		release, err = getReleaseByVersion(targetVersion)
		if err != nil {
			return fmt.Errorf("failed to find version %s: %w", targetVersion, err)
		}
	}

	fmt.Printf("\nUpdating to %s...\n", latestVersion)

	assetName := fmt.Sprintf("whcms-linux-%s.tar.gz", runtime.GOARCH)
	var downloadURL string
	var assetSize int64

	for _, asset := range release.Assets {
		if asset.Name == assetName {
			downloadURL = asset.BrowserDownloadURL
			assetSize = asset.Size
			break
		}
	}

	if downloadURL == "" {
		return fmt.Errorf("no release asset found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	installDir := getInstallDir()
	backupDir := filepath.Join(installDir, "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return err
	}

	timestamp := time.Now().Format("20060102-150405")
	backupPath := filepath.Join(backupDir, fmt.Sprintf("whcms-%s.tar.gz", timestamp))

	fmt.Println("Creating backup of current version...")
	if err := backupBinaries(installDir, backupPath); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	fmt.Printf("Downloading %s (%.1f MB)...\n", assetName, float64(assetSize)/1024/1024)
	tmpFile := filepath.Join(os.TempDir(), "whcms-update.tar.gz")
	if err := downloadFileWithProgress(downloadURL, tmpFile, assetSize); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(tmpFile)

	fmt.Println("Verifying checksum...")
	if err := verifyChecksum(release, assetName, tmpFile); err != nil {
		fmt.Printf("  Warning: %v\n", err)
	}

	fmt.Println("Stopping services...")
	stopServices()

	fmt.Println("Installing update...")
	if err := extractUpdate(tmpFile, installDir); err != nil {
		fmt.Println("Update failed, restoring backup...")
		if restoreErr := restoreBinaries(backupPath, installDir); restoreErr != nil {
			return fmt.Errorf("update failed: %w\nrestore also failed: %v", err, restoreErr)
		}
		startServicesAfterUpdate()
		return fmt.Errorf("update failed, rolled back: %w", err)
	}

	fmt.Println("Running migrations...")
	if err := runMigrations(installDir); err != nil {
		fmt.Printf("  Warning: migrations failed: %v\n", err)
	}

	fmt.Println("Starting services...")
	startServicesAfterUpdate()

	fmt.Printf("\n✓ Updated to %s\n", latestVersion)
	fmt.Printf("Backup saved to: %s\n", backupPath)

	return nil
}

// getLatestRelease/getReleaseByVersion are package vars so tests can
// substitute a fake GitHub release (pointing asset URLs at an httptest
// server) and exercise cmdUpdate's download/verify/extract/rollback
// orchestration without hitting the real GitHub API.
var getLatestRelease = getLatestReleaseImpl

func getLatestReleaseImpl() (*githubRelease, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/tsdlamongan/whcms/releases/latest", nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}

	return &release, nil
}

func verifyChecksum(release *githubRelease, assetName, filePath string) error {
	var checksumURL string
	for _, asset := range release.Assets {
		if asset.Name == "checksums.txt" {
			checksumURL = asset.BrowserDownloadURL
			break
		}
	}

	if checksumURL == "" {
		return fmt.Errorf("checksums.txt not found in release")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", checksumURL, nil)
	if err != nil {
		return err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	lines := strings.Split(string(body), "\n")
	var expectedHash string
	for _, line := range lines {
		if strings.Contains(line, assetName) {
			parts := strings.Fields(line)
			if len(parts) >= 1 {
				expectedHash = parts[0]
				break
			}
		}
	}

	if expectedHash == "" {
		return fmt.Errorf("checksum for %s not found", assetName)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return err
	}

	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if actualHash != expectedHash {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHash, actualHash)
	}

	return nil
}

func runMigrations(installDir string) error {
	apiBin := filepath.Join(installDir, "bin", "whcms-api")
	envFile := filepath.Join(installDir, "config", "whcms.env")

	cmd := exec.Command(apiBin, "migrate")
	cmd.Env = append(os.Environ(), fmt.Sprintf("ENV_FILE=%s", envFile))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

var getReleaseByVersion = getReleaseByVersionImpl

func getReleaseByVersionImpl(version string) (*githubRelease, error) {
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url := fmt.Sprintf("https://api.github.com/repos/tsdlamongan/whcms/releases/tags/%s", version)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("version %s not found", version)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}

	return &release, nil
}

func backupBinaries(installDir, backupPath string) error {
	out, err := os.Create(backupPath)
	if err != nil {
		return err
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

	binDir := filepath.Join(installDir, "bin")
	binaries := []string{"whcms-api", "whcms-worker", "whcms-frontend"}

	for _, bin := range binaries {
		binPath := filepath.Join(binDir, bin)
		if _, err := os.Stat(binPath); os.IsNotExist(err) {
			continue
		}

		info, err := os.Stat(binPath)
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = bin

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		f, err := os.Open(binPath)
		if err != nil {
			return err
		}

		if _, err := io.Copy(tw, f); err != nil {
			f.Close()
			return err
		}
		f.Close()
	}

	return nil
}

func downloadFileWithProgress(url, filepath string, totalSize int64) error {
	client := http.Client{Timeout: 10 * time.Minute}
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
	var downloaded int64
	lastPrint := time.Now()

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return err
			}
			downloaded += int64(n)

			if time.Since(lastPrint) > 500*time.Millisecond {
				pct := float64(downloaded) / float64(totalSize) * 100
				fmt.Printf("\r  Progress: %.1f%%", pct)
				lastPrint = time.Now()
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return readErr
		}
	}

	fmt.Println()
	return nil
}

func extractUpdate(tarball, installDir string) error {
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
	binDir := filepath.Join(installDir, "bin")

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if header.Typeflag == tar.TypeReg {
			targetPath := filepath.Join(binDir, header.Name)
			out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
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

func restoreBinaries(backupPath, installDir string) error {
	return extractUpdate(backupPath, installDir)
}

func stopServices() {
	services := []string{"whcms-api", "whcms-worker"}
	for _, svc := range services {
		if err := exec.Command("systemctl", "stop", svc).Run(); err != nil {
			fmt.Printf("Warning: failed to stop %s: %v\n", svc, err)
		}
	}
}

func startServicesAfterUpdate() {
	services := []string{"whcms-api", "whcms-worker"}
	for _, svc := range services {
		if err := exec.Command("systemctl", "start", svc).Run(); err != nil {
			fmt.Printf("Warning: failed to start %s: %v\n", svc, err)
		}
	}
}
