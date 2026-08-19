package cli

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- helpers ---------------------------------------------------------------

// withStdin temporarily replaces os.Stdin with a pipe fed the given input,
// restoring the original on cleanup.
func withStdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })

	go func() {
		_, _ = w.WriteString(input)
		w.Close()
	}()
}

// withEnv sets an env var for the duration of the test and restores it.
func withEnv(t *testing.T, key, value string) {
	t.Helper()
	orig, existed := os.LookupEnv(key)
	require.NoError(t, os.Setenv(key, value))
	t.Cleanup(func() {
		if existed {
			os.Setenv(key, orig)
		} else {
			os.Unsetenv(key)
		}
	})
}

func buildTarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0755, Size: int64(len(content))}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write(content)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

// --- backup.go: dumpDatabase/restoreDatabase branches -----------------------

func TestDumpDatabaseImpl_NoDocker(t *testing.T) {
	orig := commandExists
	commandExists = func(cmd string) bool { return false }
	defer func() { commandExists = orig }()

	tmpDir := t.TempDir()
	err := dumpDatabaseImpl(tmpDir, filepath.Join(tmpDir, "out.sql"))
	assert.Error(t, err)
}

func TestDumpDatabaseImpl_DockerForced(t *testing.T) {
	orig := commandExists
	commandExists = func(cmd string) bool { return cmd == "docker" }
	defer func() { commandExists = orig }()

	tmpDir := t.TempDir()
	err := dumpDatabaseImpl(tmpDir, filepath.Join(tmpDir, "out.sql"))
	assert.Error(t, err)
}

func TestRestoreDatabaseImpl_NoDocker(t *testing.T) {
	orig := commandExists
	commandExists = func(cmd string) bool { return false }
	defer func() { commandExists = orig }()

	tmpDir := t.TempDir()
	dumpPath := filepath.Join(tmpDir, "dump.sql")
	require.NoError(t, os.WriteFile(dumpPath, []byte("-- sql"), 0644))

	err := restoreDatabaseImpl(tmpDir, dumpPath)
	assert.Error(t, err)
}

func TestRestoreDatabaseImpl_DockerForced(t *testing.T) {
	orig := commandExists
	commandExists = func(cmd string) bool { return cmd == "docker" }
	defer func() { commandExists = orig }()

	tmpDir := t.TempDir()
	dumpPath := filepath.Join(tmpDir, "dump.sql")
	require.NoError(t, os.WriteFile(dumpPath, []byte("-- sql"), 0644))

	err := restoreDatabaseImpl(tmpDir, dumpPath)
	assert.Error(t, err)
}

// --- cmdBackup full success paths (dumpDatabase mocked) --------------------

func TestCmdBackup_FullSuccess_NoStorage(t *testing.T) {
	tmpDir := t.TempDir()
	withEnv(t, "WHCMS_HOME", tmpDir)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "config"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config", "app.txt"), []byte("cfg"), 0644))

	origDump := dumpDatabase
	dumpDatabase = func(installDir, outputPath string) error {
		return os.WriteFile(outputPath, []byte("-- fake dump\n"), 0644)
	}
	defer func() { dumpDatabase = origDump }()

	out := filepath.Join(tmpDir, "backup.tar.gz")
	err := cmdBackup([]string{"--output", out, "--no-storage"})
	require.NoError(t, err)

	info, err := os.Stat(out)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))
}

func TestCmdBackup_FullSuccess_WithStorage(t *testing.T) {
	tmpDir := t.TempDir()
	withEnv(t, "WHCMS_HOME", tmpDir)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "config"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "data", "storage"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "data", "storage", "f.txt"), []byte("data"), 0644))

	origDump := dumpDatabase
	dumpDatabase = func(installDir, outputPath string) error {
		return os.WriteFile(outputPath, []byte("-- fake dump\n"), 0644)
	}
	defer func() { dumpDatabase = origDump }()

	out := filepath.Join(t.TempDir(), "backup.tar.gz")
	err := cmdBackup([]string{"--output", out})
	require.NoError(t, err)

	info, err := os.Stat(out)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))
}

func TestCmdBackup_DefaultOutputPath(t *testing.T) {
	tmpDir := t.TempDir()
	withEnv(t, "WHCMS_HOME", tmpDir)

	origDump := dumpDatabase
	dumpDatabase = func(installDir, outputPath string) error {
		return fmt.Errorf("dump intentionally fails")
	}
	defer func() { dumpDatabase = origDump }()

	// Run from a temp working directory so we don't litter the repo with the
	// default `whcms-backup-<timestamp>.tar.gz` file.
	origWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(t.TempDir()))
	defer os.Chdir(origWD)

	err = cmdBackup([]string{})
	assert.Error(t, err)
}

// --- cmdRestore full flows ---------------------------------------------------

func TestCmdRestore_ConfirmNo(t *testing.T) {
	tmpDir := t.TempDir()
	backupPath := filepath.Join(tmpDir, "backup.tar.gz")
	require.NoError(t, os.WriteFile(backupPath, buildTarGz(t, map[string][]byte{"database.sql": []byte("x")}), 0644))

	withStdin(t, "n\n")

	err := cmdRestore([]string{"--input", backupPath})
	assert.NoError(t, err)
}

func TestCmdRestore_FullSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	withEnv(t, "WHCMS_HOME", tmpDir)

	backupPath := filepath.Join(tmpDir, "backup.tar.gz")
	files := map[string][]byte{
		"database.sql":     []byte("-- dump"),
		"config/whcms.env": []byte("APP_ENV=production\n"),
		"storage/f.txt":    []byte("stored"),
	}
	require.NoError(t, os.WriteFile(backupPath, buildTarGz(t, files), 0644))

	origRestore := restoreDatabase
	restoreDatabase = func(installDir, dumpPath string) error { return nil }
	defer func() { restoreDatabase = origRestore }()

	withStdin(t, "y\n")

	err := cmdRestore([]string{"--input", backupPath})
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(tmpDir, "config", "whcms.env"))
	require.NoError(t, err)
	assert.Equal(t, "APP_ENV=production\n", string(data))

	data, err = os.ReadFile(filepath.Join(tmpDir, "data", "storage", "f.txt"))
	require.NoError(t, err)
	assert.Equal(t, "stored", string(data))
}

func TestCmdRestore_DatabaseRestoreFails(t *testing.T) {
	tmpDir := t.TempDir()
	withEnv(t, "WHCMS_HOME", tmpDir)

	backupPath := filepath.Join(tmpDir, "backup.tar.gz")
	require.NoError(t, os.WriteFile(backupPath, buildTarGz(t, map[string][]byte{"database.sql": []byte("x")}), 0644))

	origRestore := restoreDatabase
	restoreDatabase = func(installDir, dumpPath string) error { return fmt.Errorf("boom") }
	defer func() { restoreDatabase = origRestore }()

	withStdin(t, "yes\n")

	err := cmdRestore([]string{"--input", backupPath})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database restore failed")
}

func TestCmdRestore_ScanlnFails(t *testing.T) {
	// Empty stdin makes fmt.Scanln return an error (EOF), which cmdRestore
	// treats as "cancel".
	tmpDir := t.TempDir()
	backupPath := filepath.Join(tmpDir, "backup.tar.gz")
	require.NoError(t, os.WriteFile(backupPath, buildTarGz(t, map[string][]byte{"database.sql": []byte("x")}), 0644))

	withStdin(t, "")

	err := cmdRestore([]string{"--input", backupPath})
	assert.NoError(t, err)
}

// --- install.go: commandExists/downloadFile-backed functions ----------------

func TestDownloadWHCMS_Success(t *testing.T) {
	tarGz := buildTarGz(t, map[string][]byte{
		"whcms-api":      []byte("api-binary"),
		"whcms-worker":   []byte("worker-binary"),
		"whcms-frontend": []byte("frontend-binary"),
	})

	origDownload := downloadFile
	downloadFile = func(url, path string) error {
		return os.WriteFile(path, tarGz, 0644)
	}
	defer func() { downloadFile = origDownload }()

	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "bin"), 0755))

	cfg := &installConfig{InstallDir: tmpDir}
	err := downloadWHCMS(cfg)
	require.NoError(t, err)

	for _, bin := range []string{"whcms-api", "whcms-worker", "whcms-frontend"} {
		info, err := os.Stat(filepath.Join(tmpDir, "bin", bin))
		require.NoError(t, err, "binary %s should exist", bin)
		assert.Equal(t, os.FileMode(0755), info.Mode().Perm())
	}

	// The downloaded tarball itself must have been removed.
	_, err = os.Stat(filepath.Join(tmpDir, "whcms.tar.gz"))
	assert.True(t, os.IsNotExist(err))
}

func TestDownloadWHCMS_DownloadFails(t *testing.T) {
	origDownload := downloadFile
	downloadFile = func(url, path string) error { return fmt.Errorf("network down") }
	defer func() { downloadFile = origDownload }()

	tmpDir := t.TempDir()
	cfg := &installConfig{InstallDir: tmpDir}
	err := downloadWHCMS(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "download failed")
}

func TestSetupDatabase_Fails(t *testing.T) {
	// No docker-compose.yml exists in this temp dir, so either "docker"
	// itself is missing or `docker compose -f <missing file> up -d` fails
	// fast without creating any real containers.
	cfg := &installConfig{InstallDir: t.TempDir()}
	err := setupDatabase(cfg)
	assert.Error(t, err)
}

func TestCreateAdminUser_NoBinary(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &installConfig{InstallDir: tmpDir}
	err := createAdminUser(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "migration failed")
}

func TestInstallDependencies_ForcedNoDocker(t *testing.T) {
	origExists := commandExists
	origRun := runCommand
	var ran []string
	runCommand = func(name string, args ...string) error {
		ran = append(ran, name)
		return fmt.Errorf("simulated failure: %s", name)
	}
	commandExists = func(cmd string) bool { return false }
	defer func() {
		commandExists = origExists
		runCommand = origRun
	}()

	cfg := &installConfig{}
	err := installDependencies(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "docker installation failed")
	require.NotEmpty(t, ran)
}

// --- promptInstallConfig branches -------------------------------------------

func TestPromptInstallConfig_EmptyFieldsFromStdin(t *testing.T) {
	withStdin(t, "mydomain.example\nme@example.com\nsupersecret1\n")

	result, err := promptInstallConfig(bufio.NewReader(os.Stdin), installConfig{})
	require.NoError(t, err)
	assert.Equal(t, "mydomain.example", result.Domain)
	assert.Equal(t, "me@example.com", result.Email)
	assert.Equal(t, "supersecret1", result.Password)
}

func TestPromptInstallConfig_DefaultsOnEmptyInput(t *testing.T) {
	withStdin(t, "\n\nlongenough1\n")

	result, err := promptInstallConfig(bufio.NewReader(os.Stdin), installConfig{})
	require.NoError(t, err)
	hostname, _ := os.Hostname()
	assert.Equal(t, hostname, result.Domain)
	assert.Equal(t, "admin@example.com", result.Email)
}

func TestPromptInstallConfig_PasswordTooShort(t *testing.T) {
	withStdin(t, "d.example\ne@example.com\nshort\n")

	_, err := promptInstallConfig(bufio.NewReader(os.Stdin), installConfig{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least 8 characters")
}

// --- status.go: parseSystemctlShow / getServiceStatus branches -------------

func TestParseSystemctlShow(t *testing.T) {
	output := "MainPID=1234\n" +
		"ActiveEnterTimestamp=" + "2020-01-01 00:00:00 UTC" + "\n" +
		"MemoryCurrent=2097152\n" +
		"MalformedLineWithoutEquals\n" +
		"UnknownKey=somevalue\n"

	var status serviceStatus
	parseSystemctlShow(&status, output)

	assert.Equal(t, 1234, status.PID)
	assert.NotEmpty(t, status.Uptime)
	assert.Equal(t, "2.0 MB", status.Memory)
}

func TestParseSystemctlShow_ZeroMemoryIgnored(t *testing.T) {
	var status serviceStatus
	parseSystemctlShow(&status, "MemoryCurrent=0\n")
	assert.Empty(t, status.Memory)
}

func TestGetServiceStatus_ForcedNoSystemctl_APIUnit(t *testing.T) {
	orig := commandExists
	commandExists = func(cmd string) bool { return false }
	defer func() { commandExists = orig }()

	status := getServiceStatus("whcms-api")
	assert.Equal(t, "whcms-api", status.Name)
	assert.Contains(t, []string{"active", "inactive"}, status.Status)
}

func TestGetServiceStatus_ForcedNoSystemctl_DockerUnit(t *testing.T) {
	orig := commandExists
	commandExists = func(cmd string) bool { return false }
	defer func() { commandExists = orig }()

	status := getServiceStatus("postgres")
	assert.Equal(t, "postgres", status.Name)
	assert.Contains(t, []string{"active", "inactive"}, status.Status)
}

func TestCmdLogs_ForcedNoJournalctl(t *testing.T) {
	orig := commandExists
	commandExists = func(cmd string) bool { return false }
	defer func() { commandExists = orig }()

	tmpDir := t.TempDir()
	withEnv(t, "WHCMS_HOME", tmpDir)

	for _, args := range [][]string{{"--api"}, {"--worker"}, {}} {
		err := cmdLogs(args)
		assert.NoError(t, err)
	}
}

// --- uninstall.go: confirmation prompt --------------------------------------

func TestCmdUninstall_ConfirmNo(t *testing.T) {
	tmpDir := t.TempDir()
	withEnv(t, "WHCMS_HOME", tmpDir)

	withStdin(t, "n\n")

	err := cmdUninstall([]string{})
	assert.NoError(t, err)

	_, statErr := os.Stat(tmpDir)
	assert.NoError(t, statErr, "install dir should still exist when cancelled")
}

func TestCmdUninstall_ConfirmYes(t *testing.T) {
	tmpDir := t.TempDir()
	withEnv(t, "WHCMS_HOME", tmpDir)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "bin"), 0755))

	withStdin(t, "y\n")

	err := cmdUninstall([]string{})
	assert.NoError(t, err)

	_, statErr := os.Stat(tmpDir)
	assert.True(t, os.IsNotExist(statErr))
}

// --- reset_admin.go extra branches ------------------------------------------

func TestCmdResetAdmin_EmptyEmailFromStdin(t *testing.T) {
	withStdin(t, "\n")

	err := cmdResetAdmin([]string{"--password", "longenough1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "email is required")
}

func TestCmdResetAdmin_PasswordPromptTooShort(t *testing.T) {
	withStdin(t, "short\n")

	err := cmdResetAdmin([]string{"--email", "a@b.com"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least 8 characters")
}

func TestCmdResetAdmin_NoDatabaseURLConfigured(t *testing.T) {
	tmpDir := t.TempDir()
	withEnv(t, "WHCMS_HOME", tmpDir)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "config"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config", "whcms.env"), []byte(""), 0644))

	err := cmdResetAdmin([]string{"--email", "a@b.com", "--password", "longenough1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Run 'whcms install' first")
}

// --- update.go: cmdUpdate flows via mocked GitHub release -------------------

func releaseAssetType(name, url string, size int64) struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
} {
	return struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	}{Name: name, BrowserDownloadURL: url, Size: size}
}

func TestCmdUpdate_AlreadyUpToDate(t *testing.T) {
	origLatest := getLatestRelease
	getLatestRelease = func() (*githubRelease, error) {
		return &githubRelease{TagName: "v" + Version}, nil
	}
	defer func() { getLatestRelease = origLatest }()

	err := cmdUpdate([]string{})
	assert.NoError(t, err)
}

func TestCmdUpdate_CheckOnly_UpdateAvailable(t *testing.T) {
	origLatest := getLatestRelease
	getLatestRelease = func() (*githubRelease, error) {
		return &githubRelease{TagName: "v99.0.0"}, nil
	}
	defer func() { getLatestRelease = origLatest }()

	err := cmdUpdate([]string{"--check"})
	assert.NoError(t, err)
}

func TestCmdUpdate_NoMatchingAsset(t *testing.T) {
	origLatest := getLatestRelease
	getLatestRelease = func() (*githubRelease, error) {
		return &githubRelease{TagName: "v99.0.0"}, nil
	}
	defer func() { getLatestRelease = origLatest }()

	err := cmdUpdate([]string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no release asset found")
}

func TestCmdUpdate_GetLatestReleaseError(t *testing.T) {
	origLatest := getLatestRelease
	getLatestRelease = func() (*githubRelease, error) { return nil, fmt.Errorf("network down") }
	defer func() { getLatestRelease = origLatest }()

	err := cmdUpdate([]string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check for updates")
}

func TestCmdUpdate_FullSuccessWithChecksum(t *testing.T) {
	tarGz := buildTarGz(t, map[string][]byte{"whcms-api": []byte("new binary content")})
	sum := sha256.Sum256(tarGz)
	assetName := fmt.Sprintf("whcms-linux-%s.tar.gz", runtime.GOARCH)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), assetName)

	mux := http.NewServeMux()
	mux.HandleFunc("/asset.tar.gz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(tarGz) })
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(checksums)) })
	server := httptest.NewServer(mux)
	defer server.Close()

	release := &githubRelease{
		TagName: "v9.9.9",
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		}{
			releaseAssetType(assetName, server.URL+"/asset.tar.gz", int64(len(tarGz))),
			releaseAssetType("checksums.txt", server.URL+"/checksums.txt", 0),
		},
	}

	origLatest := getLatestRelease
	getLatestRelease = func() (*githubRelease, error) { return release, nil }
	defer func() { getLatestRelease = origLatest }()

	tmpDir := t.TempDir()
	withEnv(t, "WHCMS_HOME", tmpDir)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "bin"), 0755))

	err := cmdUpdate([]string{})
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(tmpDir, "bin", "whcms-api"))
	require.NoError(t, err)
	assert.Equal(t, "new binary content", string(data))
}

func TestCmdUpdate_SpecificVersion(t *testing.T) {
	tarGz := buildTarGz(t, map[string][]byte{"whcms-api": []byte("versioned binary")})
	assetName := fmt.Sprintf("whcms-linux-%s.tar.gz", runtime.GOARCH)

	mux := http.NewServeMux()
	mux.HandleFunc("/asset.tar.gz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(tarGz) })
	server := httptest.NewServer(mux)
	defer server.Close()

	release := &githubRelease{
		TagName: "v1.2.3",
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		}{
			releaseAssetType(assetName, server.URL+"/asset.tar.gz", int64(len(tarGz))),
		},
	}

	origLatest := getLatestRelease
	getLatestRelease = func() (*githubRelease, error) {
		return &githubRelease{TagName: "v0.0.1"}, nil
	}
	defer func() { getLatestRelease = origLatest }()

	origByVersion := getReleaseByVersion
	getReleaseByVersion = func(version string) (*githubRelease, error) {
		assert.Equal(t, "1.2.3", version)
		return release, nil
	}
	defer func() { getReleaseByVersion = origByVersion }()

	tmpDir := t.TempDir()
	withEnv(t, "WHCMS_HOME", tmpDir)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "bin"), 0755))

	err := cmdUpdate([]string{"--version", "1.2.3"})
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(tmpDir, "bin", "whcms-api"))
	require.NoError(t, err)
	assert.Equal(t, "versioned binary", string(data))
}

func TestCmdUpdate_VersionNotFound(t *testing.T) {
	origLatest := getLatestRelease
	getLatestRelease = func() (*githubRelease, error) {
		return &githubRelease{TagName: "v0.0.1"}, nil
	}
	defer func() { getLatestRelease = origLatest }()

	origByVersion := getReleaseByVersion
	getReleaseByVersion = func(version string) (*githubRelease, error) {
		return nil, fmt.Errorf("not found")
	}
	defer func() { getReleaseByVersion = origByVersion }()

	err := cmdUpdate([]string{"--version", "9.9.9"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to find version")
}

func TestCmdUpdate_ExtractFailsAndRollsBack(t *testing.T) {
	assetName := fmt.Sprintf("whcms-linux-%s.tar.gz", runtime.GOARCH)

	mux := http.NewServeMux()
	mux.HandleFunc("/asset.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not a valid gzip file"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	release := &githubRelease{
		TagName: "v9.9.9",
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		}{
			releaseAssetType(assetName, server.URL+"/asset.tar.gz", 21),
		},
	}

	origLatest := getLatestRelease
	getLatestRelease = func() (*githubRelease, error) { return release, nil }
	defer func() { getLatestRelease = origLatest }()

	tmpDir := t.TempDir()
	withEnv(t, "WHCMS_HOME", tmpDir)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "bin"), 0755))

	err := cmdUpdate([]string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rolled back")
}

func TestRunMigrations_NoBinary(t *testing.T) {
	err := runMigrations(t.TempDir())
	assert.Error(t, err)
}

// --- randomToken error branch (both cli.go's and via override) -------------

func TestRandomToken_ReaderError(t *testing.T) {
	origReader := randReader
	randReader = &failingReader{}
	defer func() { randReader = origReader }()

	_, err := randomToken(16)
	assert.Error(t, err)
}

type failingReader struct{}

func (f *failingReader) Read(p []byte) (int, error) {
	return 0, fmt.Errorf("simulated rand failure")
}

// sanity: ensure strings import is used (guards against accidental future
// removal breaking the build silently via unused import elsewhere).
var _ = strings.TrimSpace

// --- installSystemdServices: pure logic via injectable systemdUnitDir ------

func TestInstallSystemdServices_WritesUnits(t *testing.T) {
	origDir := systemdUnitDir
	tmpUnitDir := t.TempDir()
	systemdUnitDir = tmpUnitDir
	defer func() { systemdUnitDir = origDir }()

	origExists := commandExists
	commandExists = func(cmd string) bool { return cmd == "systemctl" }
	defer func() { commandExists = origExists }()

	origRun := runCommand
	runCommand = func(name string, args ...string) error { return nil }
	defer func() { runCommand = origRun }()

	cfg := &installConfig{InstallDir: t.TempDir()}
	err := installSystemdServices(cfg)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(tmpUnitDir, "whcms-api.service"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "Description=WHCMS API Server")

	data, err = os.ReadFile(filepath.Join(tmpUnitDir, "whcms-worker.service"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "Description=WHCMS Worker")
}

func TestInstallSystemdServices_PermissionDenied(t *testing.T) {
	origDir := systemdUnitDir
	roUnitDir := filepath.Join(t.TempDir(), "readonly")
	require.NoError(t, os.MkdirAll(roUnitDir, 0555))
	systemdUnitDir = roUnitDir
	defer func() {
		systemdUnitDir = origDir
		_ = os.Chmod(roUnitDir, 0755)
	}()

	origExists := commandExists
	commandExists = func(cmd string) bool { return cmd == "systemctl" }
	defer func() { commandExists = origExists }()

	origRun := runCommand
	runCommand = func(name string, args ...string) error { return nil }
	defer func() { runCommand = origRun }()

	if os.Geteuid() == 0 {
		t.Skip("running as root: permission checks on the unit dir don't apply")
	}

	cfg := &installConfig{InstallDir: t.TempDir()}
	err := installSystemdServices(cfg)
	// Cannot write into a read-only dir: the warning-and-continue branch
	// should be taken, so no hard error should propagate.
	assert.NoError(t, err)
}

func TestInstallSystemdServices_DaemonReloadFails(t *testing.T) {
	origDir := systemdUnitDir
	systemdUnitDir = t.TempDir()
	defer func() { systemdUnitDir = origDir }()

	origExists := commandExists
	commandExists = func(cmd string) bool { return cmd == "systemctl" }
	defer func() { commandExists = origExists }()

	origRun := runCommand
	runCommand = func(name string, args ...string) error { return fmt.Errorf("daemon-reload failed") }
	defer func() { runCommand = origRun }()

	cfg := &installConfig{InstallDir: t.TempDir()}
	err := installSystemdServices(cfg)
	assert.Error(t, err)
}

// --- cmdInstall: cover the pure orchestration lines without touching real
// infra or the multi-second setupDatabase retry/sleep loop -----------------

func TestCmdInstall_NonInteractive_StepFails(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("installer only supports Linux")
	}
	// Point WHCMS_HOME at a path where a path component is a regular file,
	// so the very first step (createInstallDirs -> os.MkdirAll) fails
	// deterministically without touching real infra.
	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0644))
	withEnv(t, "WHCMS_HOME", filepath.Join(blocker, "sub"))

	err := cmdInstall([]string{"--non-interactive", "--domain", "d.example", "--email", "a@b.com", "--password", "secret123"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Create directories failed")
}

func TestCmdInstall_InteractiveCancelled(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("installer only supports Linux")
	}
	withStdin(t, "d.example\na@b.com\nsecret1234\nn\n")
	withEnv(t, "WHCMS_HOME", t.TempDir())

	err := cmdInstall([]string{})
	assert.NoError(t, err)
}

// --- getServiceStatus / cmdLogs with systemctl forced present --------------

func TestGetServiceStatus_ForcedSystemctlPresent(t *testing.T) {
	orig := commandExists
	commandExists = func(cmd string) bool { return true }
	defer func() { commandExists = orig }()

	status := getServiceStatus("whcms-api")
	assert.Equal(t, "whcms-api", status.Name)
}

func TestCmdLogs_ForcedJournalctlPresent(t *testing.T) {
	orig := commandExists
	commandExists = func(cmd string) bool { return cmd == "journalctl" }
	defer func() { commandExists = orig }()

	for _, args := range [][]string{{"--api"}, {"--worker"}, {}} {
		done := make(chan struct{})
		go func(a []string) {
			_ = cmdLogs(a)
			close(done)
		}(args)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("cmdLogs(%v) did not return in time (possible journalctl -f hang)", args)
		}
	}
}
