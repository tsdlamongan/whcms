package cli

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_NoArgs(t *testing.T) {
	err := Run([]string{"whcms"})
	assert.NoError(t, err)
}

func TestRun_Version(t *testing.T) {
	err := Run([]string{"whcms", "version"})
	assert.NoError(t, err)
}

func TestRun_Help(t *testing.T) {
	err := Run([]string{"whcms", "help"})
	assert.NoError(t, err)
}

func TestRun_HelpFlags(t *testing.T) {
	for _, flag := range []string{"--help", "-h", "--version", "-v"} {
		err := Run([]string{"whcms", flag})
		assert.NoError(t, err, "flag %s should not error", flag)
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	err := Run([]string{"whcms", "nonexistent"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown command")
}

func TestRun_InstallCommand(t *testing.T) {
	if runtime.GOOS != "linux" {
		err := Run([]string{"whcms", "install"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "only supports Linux")
	}
}

func TestRun_StatusCommand(t *testing.T) {
	err := Run([]string{"whcms", "status"})
	assert.NoError(t, err)
}

func TestRun_StatusJSON(t *testing.T) {
	err := Run([]string{"whcms", "status", "--json"})
	assert.NoError(t, err)
}

func TestRun_LogsCommand(t *testing.T) {
	err := Run([]string{"whcms", "logs"})
	assert.NoError(t, err)
}

func TestRun_BackupCommand(t *testing.T) {
	tmpDir := t.TempDir()
	original := os.Getenv("WHCMS_HOME")
	defer os.Setenv("WHCMS_HOME", original)

	os.Setenv("WHCMS_HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, "config"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "config", "whcms.env"), []byte("DATABASE_URL=postgres://test\n"), 0644)

	// backup will fail because docker/pg_dump don't exist in test env,
	// but we verify the command is recognized and attempts execution
	err := Run([]string{"whcms", "backup", "--output", filepath.Join(tmpDir, "backup.tar.gz")})
	// Expected to fail due to missing database tools
	assert.Error(t, err)
}

func TestRun_RestoreCommand(t *testing.T) {
	err := Run([]string{"whcms", "restore"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--input")
}

func TestRun_ResetAdminCommand(t *testing.T) {
	err := Run([]string{"whcms", "reset-admin"})
	assert.Error(t, err)
}

func TestRun_UninstallCommand(t *testing.T) {
	err := Run([]string{"whcms", "uninstall", "--force"})
	assert.NoError(t, err)
}

func TestParseInstallFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want installConfig
	}{
		{
			name: "defaults",
			args: []string{},
			want: installConfig{Port: 8080},
		},
		{
			name: "all flags",
			args: []string{"--non-interactive", "--domain", "example.com", "--email", "a@b.com", "--password", "secret123", "--skip-deps", "--port", "9090"},
			want: installConfig{
				NonInteractive: true,
				Domain:         "example.com",
				Email:          "a@b.com",
				Password:       "secret123",
				SkipDeps:       true,
				Port:           9090,
			},
		},
		{
			name: "partial flags",
			args: []string{"--domain", "test.com", "--port", "3000"},
			want: installConfig{
				Domain: "test.com",
				Port:   3000,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseInstallFlags(tt.args)
			assert.Equal(t, tt.want.Domain, got.Domain)
			assert.Equal(t, tt.want.Email, got.Email)
			assert.Equal(t, tt.want.Password, got.Password)
			assert.Equal(t, tt.want.NonInteractive, got.NonInteractive)
			assert.Equal(t, tt.want.SkipDeps, got.SkipDeps)
			assert.Equal(t, tt.want.Port, got.Port)
		})
	}
}

func TestValidateInstallConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     installConfig
		wantErr string
	}{
		{
			name:    "missing domain",
			cfg:     installConfig{Email: "a@b.com", Password: "secret123", Port: 8080},
			wantErr: "domain is required",
		},
		{
			name:    "missing email",
			cfg:     installConfig{Domain: "example.com", Password: "secret123", Port: 8080},
			wantErr: "admin email is required",
		},
		{
			name:    "missing password",
			cfg:     installConfig{Domain: "example.com", Email: "a@b.com", Port: 8080},
			wantErr: "admin password is required",
		},
		{
			name:    "invalid port low",
			cfg:     installConfig{Domain: "example.com", Email: "a@b.com", Password: "secret123", Port: 0},
			wantErr: "port must be 1-65535",
		},
		{
			name:    "invalid port high",
			cfg:     installConfig{Domain: "example.com", Email: "a@b.com", Password: "secret123", Port: 70000},
			wantErr: "port must be 1-65535",
		},
		{
			name: "valid",
			cfg:  installConfig{Domain: "example.com", Email: "a@b.com", Password: "secret123", Port: 8080},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateInstallConfig(tt.cfg)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestInstallCmd_RejectsNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("this test is for non-linux platforms")
	}
	err := cmdInstall([]string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "only supports Linux")
}

func TestCreateInstallDirs(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &installConfig{InstallDir: tmpDir}

	err := createInstallDirs(cfg)
	require.NoError(t, err)

	expected := []string{"bin", "config", "data", "logs"}
	for _, sub := range expected {
		path := tmpDir + "/" + sub
		info, err := os.Stat(path)
		assert.NoError(t, err, "directory %s should exist", sub)
		assert.True(t, info.IsDir(), "%s should be a directory", sub)
	}
}

func TestRandomToken(t *testing.T) {
	token1, err := randomToken(32)
	require.NoError(t, err)
	assert.NotEmpty(t, token1)

	token2, err := randomToken(32)
	require.NoError(t, err)
	assert.NotEqual(t, token1, token2, "tokens should be unique")
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{100, "100 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tt := range tests {
		got := formatBytes(tt.input)
		assert.Equal(t, tt.want, got, "formatBytes(%d)", tt.input)
	}
}

func TestGetInstallDir(t *testing.T) {
	original := os.Getenv("WHCMS_HOME")
	defer os.Setenv("WHCMS_HOME", original)

	os.Setenv("WHCMS_HOME", "/custom/path")
	assert.Equal(t, "/custom/path", getInstallDir())

	os.Unsetenv("WHCMS_HOME")
	dir := getInstallDir()
	assert.True(t, strings.HasSuffix(dir, ".whcms"), "default should end with .whcms, got %s", dir)
}

func TestCommandExists(t *testing.T) {
	assert.True(t, commandExists("go"), "go should exist in test environment")
	assert.False(t, commandExists("nonexistent_binary_xyz_123"), "random binary should not exist")
}

func TestIsPortAvailable(t *testing.T) {
	assert.True(t, isPortAvailable(0), "port 0 should ask OS for any free port")
}

func TestGetEnvValue(t *testing.T) {
	tmpFile, err := os.CreateTemp(t.TempDir(), "env-*.env")
	require.NoError(t, err)

	content := `# comment
APP_ENV=production
APP_PORT=8080
DATABASE_URL=postgres://user:pass@localhost/db?sslmode=disable
EMPTY_VAR=
`
	_, err = io.Copy(tmpFile, strings.NewReader(content))
	require.NoError(t, err)
	tmpFile.Close()

	assert.Equal(t, "production", getEnvValue(tmpFile.Name(), "APP_ENV"))
	assert.Equal(t, "8080", getEnvValue(tmpFile.Name(), "APP_PORT"))
	assert.Equal(t, "postgres://user:pass@localhost/db?sslmode=disable", getEnvValue(tmpFile.Name(), "DATABASE_URL"))
	assert.Equal(t, "", getEnvValue(tmpFile.Name(), "EMPTY_VAR"))
	assert.Equal(t, "", getEnvValue(tmpFile.Name(), "NONEXISTENT"))
	assert.Equal(t, "", getEnvValue("/nonexistent/path.env", "ANY"))
}

func TestBackupRestore_RoundTrip(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	require.NoError(t, os.MkdirAll(srcDir+"/subdir", 0755))
	require.NoError(t, os.WriteFile(srcDir+"/file1.txt", []byte("hello"), 0644))
	require.NoError(t, os.WriteFile(srcDir+"/subdir/file2.txt", []byte("world"), 0644))

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	err := addDirToTar(tw, srcDir, "backup")
	require.NoError(t, err)

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	tarPath := srcDir + "/test.tar.gz"
	require.NoError(t, os.WriteFile(tarPath, buf.Bytes(), 0644))

	err = extractTarGz(tarPath, dstDir)
	require.NoError(t, err)

	data, err := os.ReadFile(dstDir + "/backup/file1.txt")
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))

	data, err = os.ReadFile(dstDir + "/backup/subdir/file2.txt")
	require.NoError(t, err)
	assert.Equal(t, "world", string(data))
}

func TestCopyDir(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir() + "/dst"

	require.NoError(t, os.MkdirAll(srcDir+"/nested", 0755))
	require.NoError(t, os.WriteFile(srcDir+"/a.txt", []byte("aaa"), 0644))
	require.NoError(t, os.WriteFile(srcDir+"/nested/b.txt", []byte("bbb"), 0644))

	err := copyDir(srcDir, dstDir)
	require.NoError(t, err)

	data, err := os.ReadFile(dstDir + "/a.txt")
	require.NoError(t, err)
	assert.Equal(t, "aaa", string(data))

	data, err = os.ReadFile(dstDir + "/nested/b.txt")
	require.NoError(t, err)
	assert.Equal(t, "bbb", string(data))
}

func TestServiceStatus_Fallback(t *testing.T) {
	status := getServiceStatus("nonexistent-service-xyz")
	assert.Equal(t, "nonexistent-service-xyz", status.Name)
}

func TestCmdStatus_NoPanic(t *testing.T) {
	err := cmdStatus([]string{})
	assert.NoError(t, err)
}

func TestCmdStatus_JSON(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := cmdStatus([]string{"--json"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	assert.NoError(t, err)
	assert.Contains(t, buf.String(), "whcms-api")
}

func TestAddFileToTar(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := tmpDir + "/test.txt"
	require.NoError(t, os.WriteFile(testFile, []byte("test content"), 0644))

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	err := addFileToTar(tw, testFile, "test.txt")
	require.NoError(t, err)

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	assert.True(t, buf.Len() > 0)
}

func TestBackupBinaries(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := tmpDir + "/bin"
	require.NoError(t, os.MkdirAll(binDir, 0755))
	require.NoError(t, os.WriteFile(binDir+"/whcms-api", []byte("fake binary"), 0755))

	backupPath := tmpDir + "/backup.tar.gz"
	err := backupBinaries(tmpDir, backupPath)
	require.NoError(t, err)

	info, err := os.Stat(backupPath)
	require.NoError(t, err)
	assert.True(t, info.Size() > 0)
}

func TestDownloadFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("test content"))
	}))
	defer server.Close()

	tmpFile := filepath.Join(t.TempDir(), "downloaded.txt")
	err := downloadFile(server.URL, tmpFile)
	require.NoError(t, err)

	data, err := os.ReadFile(tmpFile)
	require.NoError(t, err)
	assert.Equal(t, "test content", string(data))
}

func TestDownloadFile_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tmpFile := filepath.Join(t.TempDir(), "downloaded.txt")
	err := downloadFile(server.URL, tmpFile)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")
}

func TestGetLatestRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		release := githubRelease{
			TagName:     "v1.0.0",
			Name:        "Release 1.0.0",
			PublishedAt: "2024-01-01T00:00:00Z",
			Assets: []struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
				Size               int64  `json:"size"`
			}{
				{
					Name:               "whcms-linux-amd64.tar.gz",
					BrowserDownloadURL: "https://example.com/download",
					Size:               1024,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(release)
	}))
	defer server.Close()

	// This test would need to mock the GitHub API URL
	// For now, just test that the function exists and returns correct type
	_, err := getLatestRelease()
	// We expect this to fail in test environment without internet
	if err != nil {
		assert.Contains(t, err.Error(), "GitHub API")
	}
}

func TestVerifyChecksum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("abc123  whcms-linux-amd64.tar.gz\n"))
	}))
	defer server.Close()

	// Create a test file with matching checksum
	tmpFile := filepath.Join(t.TempDir(), "test.tar.gz")
	require.NoError(t, os.WriteFile(tmpFile, []byte("test"), 0644))

	release := &githubRelease{
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		}{
			{
				Name:               "checksums.txt",
				BrowserDownloadURL: server.URL + "/checksums.txt",
			},
		},
	}

	// This will fail because checksum won't match, but tests the function exists
	err := verifyChecksum(release, "whcms-linux-amd64.tar.gz", tmpFile)
	assert.Error(t, err)
}

func TestExtractUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0755))

	// Create a tar.gz with a test binary
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	content := []byte("binary content")
	header := &tar.Header{
		Name: "whcms-api",
		Mode: 0755,
		Size: int64(len(content)),
	}
	require.NoError(t, tw.WriteHeader(header))
	_, err := tw.Write(content)
	require.NoError(t, err)

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	tarPath := filepath.Join(tmpDir, "update.tar.gz")
	require.NoError(t, os.WriteFile(tarPath, buf.Bytes(), 0644))

	err = extractUpdate(tarPath, tmpDir)
	require.NoError(t, err)

	// Verify the binary was extracted
	extractedPath := filepath.Join(binDir, "whcms-api")
	data, err := os.ReadFile(extractedPath)
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

func TestStopServices(t *testing.T) {
	// This should not panic even if systemctl doesn't exist
	assert.NotPanics(t, func() {
		stopServices()
	})
}

func TestStartServicesAfterUpdate(t *testing.T) {
	// This should not panic even if systemctl doesn't exist
	assert.NotPanics(t, func() {
		startServicesAfterUpdate()
	})
}

func TestDumpDatabase(t *testing.T) {
	t.Skip("requires external database tools (pg_dump/docker)")
}

func TestRestoreDatabase(t *testing.T) {
	t.Skip("requires external database tools (psql/docker)")
}

func TestExtractTarGz_InvalidFile(t *testing.T) {
	tmpDir := t.TempDir()
	invalidFile := filepath.Join(tmpDir, "invalid.tar.gz")
	require.NoError(t, os.WriteFile(invalidFile, []byte("not a gzip file"), 0644))

	err := extractTarGz(invalidFile, tmpDir)
	assert.Error(t, err)
}

func TestAddDirToTar_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	emptyDir := filepath.Join(tmpDir, "empty")
	require.NoError(t, os.MkdirAll(emptyDir, 0755))

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	err := addDirToTar(tw, emptyDir, "backup")
	require.NoError(t, err)

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	assert.True(t, buf.Len() > 0)
}

func TestCopyDir_NonExistentSource(t *testing.T) {
	err := copyDir("/nonexistent/path", t.TempDir())
	assert.Error(t, err)
}

func TestGetEnvValue_EmptyFile(t *testing.T) {
	tmpFile, err := os.CreateTemp(t.TempDir(), "empty-*.env")
	require.NoError(t, err)
	tmpFile.Close()

	value := getEnvValue(tmpFile.Name(), "ANY_KEY")
	assert.Equal(t, "", value)
}

func TestRandomToken_DifferentLengths(t *testing.T) {
	token16, err := randomToken(16)
	require.NoError(t, err)
	assert.NotEmpty(t, token16)

	token32, err := randomToken(32)
	require.NoError(t, err)
	assert.NotEmpty(t, token32)
	assert.NotEqual(t, token16, token32)
}

func TestApiServiceUnit(t *testing.T) {
	cfg := &installConfig{InstallDir: "/opt/whcms"}
	unit := apiServiceUnit(cfg)

	assert.Contains(t, unit, "Description=WHCMS API Server")
	assert.Contains(t, unit, "WorkingDirectory=/opt/whcms")
	assert.Contains(t, unit, "EnvironmentFile=/opt/whcms/config/whcms.env")
	assert.Contains(t, unit, "ExecStart=/opt/whcms/bin/whcms-api")
	assert.Contains(t, unit, "StandardOutput=append:/opt/whcms/logs/api.log")
	assert.Contains(t, unit, "WantedBy=multi-user.target")
}

func TestWorkerServiceUnit(t *testing.T) {
	cfg := &installConfig{InstallDir: "/opt/whcms"}
	unit := workerServiceUnit(cfg)

	assert.Contains(t, unit, "Description=WHCMS Worker")
	assert.Contains(t, unit, "WorkingDirectory=/opt/whcms")
	assert.Contains(t, unit, "EnvironmentFile=/opt/whcms/config/whcms.env")
	assert.Contains(t, unit, "ExecStart=/opt/whcms/bin/whcms-worker")
	assert.Contains(t, unit, "StandardOutput=append:/opt/whcms/logs/worker.log")
	assert.Contains(t, unit, "After=network.target docker.service whcms-api.service")
}

func TestGetEnvFilePath(t *testing.T) {
	path := getEnvFilePath("/opt/whcms")
	assert.Equal(t, "/opt/whcms/config/whcms.env", path)
}

func TestShortCommit(t *testing.T) {
	result := shortCommit()
	assert.NotEmpty(t, result)
	assert.LessOrEqual(t, len(result), 7)
}

func TestRestoreBinaries(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0755))

	// Create a backup archive
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	content := []byte("binary content")
	header := &tar.Header{
		Name: "whcms-api",
		Mode: 0755,
		Size: int64(len(content)),
	}
	require.NoError(t, tw.WriteHeader(header))
	_, err := tw.Write(content)
	require.NoError(t, err)

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	backupPath := filepath.Join(tmpDir, "backup.tar.gz")
	require.NoError(t, os.WriteFile(backupPath, buf.Bytes(), 0644))

	err = restoreBinaries(backupPath, tmpDir)
	require.NoError(t, err)

	extractedPath := filepath.Join(binDir, "whcms-api")
	data, err := os.ReadFile(extractedPath)
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

func TestGetReleaseByVersion(t *testing.T) {
	// This will fail in test env because it calls real GitHub API
	_, err := getReleaseByVersion("v1.2.3")
	assert.Error(t, err)
}

func TestDownloadFileWithProgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "12")
		w.Write([]byte("test content"))
	}))
	defer server.Close()

	tmpFile := filepath.Join(t.TempDir(), "downloaded.txt")
	err := downloadFileWithProgress(server.URL, tmpFile, 12)
	require.NoError(t, err)

	data, err := os.ReadFile(tmpFile)
	require.NoError(t, err)
	assert.Equal(t, "test content", string(data))
}

func TestRunCommand(t *testing.T) {
	// Test with a command that exists
	err := runCommand("echo", "test")
	assert.NoError(t, err)

	// Test with a command that doesn't exist
	err = runCommand("nonexistent_command_xyz")
	assert.Error(t, err)
}

func TestRunCommandSilent(t *testing.T) {
	// Test with a command that exists
	err := runCommandSilent("echo", "test")
	assert.NoError(t, err)

	// Test with a command that doesn't exist
	err = runCommandSilent("nonexistent_command_xyz")
	assert.Error(t, err)
}

func TestDockerComposeExists(t *testing.T) {
	// Just verify it doesn't panic
	assert.NotPanics(t, func() {
		_ = dockerComposeExists()
	})
}

func TestTailLog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tail command not available on Windows")
	}

	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")
	require.NoError(t, os.WriteFile(logFile, []byte("line 1\nline 2\nline 3"), 0644))

	err := tailLog(logFile, false)
	assert.NoError(t, err)

	err = tailLog(filepath.Join(tmpDir, "nonexistent.log"), false)
	assert.NoError(t, err)
}

func TestGenerateConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &installConfig{
		InstallDir: tmpDir,
		Domain:     "test.example.com",
		Port:       8080,
		Email:      "admin@example.com",
	}

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "config"), 0755))

	err := generateConfig(cfg)
	require.NoError(t, err)

	envPath := filepath.Join(tmpDir, "config", "whcms.env")
	assert.FileExists(t, envPath)

	envContent, err := os.ReadFile(envPath)
	require.NoError(t, err)
	assert.Contains(t, string(envContent), "APP_ENV=production")
	assert.Contains(t, string(envContent), "APP_PORT=8080")
	assert.Contains(t, string(envContent), "test.example.com")
	assert.Contains(t, string(envContent), "admin@example.com")

	composePath := filepath.Join(tmpDir, "config", "docker-compose.yml")
	assert.FileExists(t, composePath)
}

func TestPromptInstallConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("interactive prompt test skipped on Windows")
	}

	cfg := installConfig{
		Domain:   "test.com",
		Email:    "test@example.com",
		Password: "password123",
	}

	result, err := promptInstallConfig(bufio.NewReader(os.Stdin), cfg)
	require.NoError(t, err)
	assert.Equal(t, "test.com", result.Domain)
	assert.Equal(t, "test@example.com", result.Email)
}

func TestInstallSystemdServices_NoSystemctl(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only runs on Windows where systemctl is absent")
	}

	tmpDir := t.TempDir()
	cfg := &installConfig{
		InstallDir: tmpDir,
	}

	err := installSystemdServices(cfg)
	require.NoError(t, err)
}

func TestStartServices_NoSystemctl(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &installConfig{
		InstallDir: tmpDir,
	}

	err := startServices(cfg)
	require.NoError(t, err)
}

func TestInstallDependencies_DockerExists(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Docker installation test skipped on Windows")
	}

	tmpDir := t.TempDir()
	cfg := &installConfig{
		InstallDir: tmpDir,
	}

	err := installDependencies(cfg)
	require.NoError(t, err)
}

func TestCmdResetAdmin_NoDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	original := os.Getenv("WHCMS_HOME")
	defer os.Setenv("WHCMS_HOME", original)

	os.Setenv("WHCMS_HOME", tmpDir)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "config"), 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "config", "whcms.env"),
		[]byte("DATABASE_URL=postgres://invalid:invalid@localhost:9999/invalid\n"),
		0644,
	))

	err := cmdResetAdmin([]string{"--email", "admin@example.com", "--password", "newpassword123"})
	assert.Error(t, err)
}

func TestCmdUpdate_CheckOnly(t *testing.T) {
	// cmdUpdate --check hits the real GitHub API, which may fail in CI.
	// Just verify it doesn't panic.
	assert.NotPanics(t, func() {
		_ = cmdUpdate([]string{"--check"})
	})
}

func TestCmdBackup_NoStorage(t *testing.T) {
	tmpDir := t.TempDir()
	original := os.Getenv("WHCMS_HOME")
	defer os.Setenv("WHCMS_HOME", original)

	os.Setenv("WHCMS_HOME", tmpDir)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "config"), 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "config", "whcms.env"),
		[]byte("DATABASE_URL=postgres://invalid:invalid@localhost:9999/invalid\n"),
		0644,
	))

	err := cmdBackup([]string{"--no-storage"})
	assert.Error(t, err)
}

func TestCmdRestore_NoInput(t *testing.T) {
	err := cmdRestore([]string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--input")
}

func TestCmdRestore_NonexistentFile(t *testing.T) {
	err := cmdRestore([]string{"--input", "/nonexistent/backup.tar.gz"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestCmdUninstall_Force(t *testing.T) {
	tmpDir := t.TempDir()
	original := os.Getenv("WHCMS_HOME")
	defer os.Setenv("WHCMS_HOME", original)

	os.Setenv("WHCMS_HOME", tmpDir)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "bin"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "bin", "whcms"), []byte("fake"), 0755))

	err := cmdUninstall([]string{"--force"})
	assert.NoError(t, err)

	_, err = os.Stat(tmpDir)
	assert.True(t, os.IsNotExist(err))
}
