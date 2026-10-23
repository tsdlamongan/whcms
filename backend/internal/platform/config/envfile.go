package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ApplyEnvFile reads simple KEY=VALUE lines from path and calls os.Setenv for
// every key not already present in the real process environment - a real env
// var always wins over the file. A missing file is not an error: this lets
// cmd/api optimistically look for an installer-written env file (backing the
// installation wizard, docs/CONTRACTS.md §15) without requiring one to exist.
func ApplyEnvFile(path string) error {
	values, err := ReadEnvFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for key, value := range values {
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("env file %s: setenv %s: %w", path, key, err)
		}
	}
	return nil
}

// WriteEnvFile merges values into any existing KEY=VALUE file at path
// (preserving keys already there that values doesn't touch) and writes the
// result back atomically with 0600 permissions.
func WriteEnvFile(path string, values map[string]string) error {
	merged, err := ReadEnvFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		merged = map[string]string{}
	}
	for k, v := range values {
		merged[k] = v
	}

	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, merged[k])
	}

	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("env file %s: mkdir: %w", path, err)
		}
	}

	tmp, err := os.CreateTemp(dir, ".env-*.tmp")
	if err != nil {
		return fmt.Errorf("env file %s: create temp: %w", path, err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("env file %s: write: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("env file %s: close: %w", path, err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("env file %s: chmod: %w", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("env file %s: rename: %w", path, err)
	}
	return nil
}

// ReadEnvFile parses path's KEY=VALUE lines (blank lines, "#" comments, and an
// optional leading "export " are ignored). Values may themselves contain "="
// (e.g. a DSN's query string) since only the first "=" on a line is treated
// as the delimiter. Read-only - never touches the process environment.
func ReadEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, ok := parseEnvLine(line); ok {
			out[key] = value
		}
	}
	return out, scanner.Err()
}

func parseEnvLine(line string) (key, value string, ok bool) {
	line = strings.TrimPrefix(line, "export ")
	idx := strings.IndexByte(line, '=')
	if idx <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	raw := line[idx+1:]
	// A quoted value keeps any '#' inside it literally (e.g. a password);
	// only an unquoted value gets its trailing "  # comment" stripped, same
	// as .env.example's own style of documenting a blank default
	// inline (e.g. "DUITKU_BASE_URL=            # empty -> derived from...").
	// Checked on the pre-trim value so a stripped-to-nothing comment (the
	// common case: value is blank, comment follows after whitespace)
	// collapses to "" instead of leaking the comment text as the value.
	if !isQuoted(strings.TrimSpace(raw)) {
		raw = stripInlineComment(raw)
	}
	value = unquote(strings.TrimSpace(raw))
	return key, value, true
}

func isQuoted(s string) bool {
	if len(s) < 2 {
		return false
	}
	first, last := s[0], s[len(s)-1]
	return (first == '"' && last == '"') || (first == '\'' && last == '\'')
}

// stripInlineComment truncates raw at a '#' that starts a comment - i.e. one
// immediately preceded by a space or tab, matching shell/Make comment
// semantics (a '#' glued directly to other text, with no preceding
// whitespace, is left alone as ordinary value data).
func stripInlineComment(raw string) string {
	for i := 1; i < len(raw); i++ {
		if raw[i] == '#' && (raw[i-1] == ' ' || raw[i-1] == '\t') {
			return raw[:i]
		}
	}
	return raw
}

func unquote(s string) string {
	if isQuoted(s) {
		return s[1 : len(s)-1]
	}
	return s
}
