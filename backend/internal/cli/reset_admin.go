package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"golang.org/x/crypto/bcrypt"
)

func cmdResetAdmin(args []string) error {
	installDir := getInstallDir()
	email := ""
	password := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--email":
			if i+1 < len(args) {
				email = args[i+1]
				i++
			}
		case "--password":
			if i+1 < len(args) {
				password = args[i+1]
				i++
			}
		}
	}

	var reader *bufio.Reader
	if email == "" || password == "" {
		reader = bufio.NewReader(os.Stdin)
	}

	if email == "" {
		fmt.Print("Admin email: ")
		email, _ = reader.ReadString('\n')
		email = strings.TrimSpace(email)
	}

	if email == "" {
		return fmt.Errorf("email is required")
	}

	if password == "" {
		fmt.Print("New password (min 8 chars): ")
		password, _ = reader.ReadString('\n')
		password = strings.TrimSpace(password)
	}

	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}

	envFile := getEnvFilePath(installDir)
	dbURL := getEnvValue(envFile, "DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("DATABASE_URL not found in config. Run 'whcms install' first")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	database, err := db.Connect(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("cannot connect to database: %w", err)
	}
	defer database.Close()

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	query := `UPDATE users SET password_hash = $1, updated_at = NOW() WHERE email = $2 AND role IN ('admin', 'staff')`
	tag, err := database.Pool().Exec(ctx, query, string(hashedPassword), email)
	if err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("no admin user found with email %s", email)
	}

	fmt.Printf("✓ Password reset for %s\n", email)
	return nil
}

func getEnvFilePath(installDir string) string {
	return installDir + "/config/whcms.env"
}
