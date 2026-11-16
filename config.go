package netsec

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type safeDirectoryEntry struct {
	value string
	home  string
}

func (e safeDirectoryEntry) matches(path string) bool {
	if e.value == "*" {
		return true
	}

	pattern, prefix, ok := safeDirectoryPattern(e.value, e.home)
	if !ok {
		return false
	}

	candidate, err := canonicalExisting(path)
	if err != nil {
		candidate = filepath.Clean(path)
	}

	if !prefix {
		return candidate == pattern
	}
	if pattern == string(filepath.Separator) {
		return candidate != pattern && filepath.IsAbs(candidate)
	}
	return candidate != pattern &&
		strings.HasPrefix(candidate, pattern+string(filepath.Separator))
}

func safeDirectoryPattern(value, home string) (pattern string, prefix bool, ok bool) {
	value = strings.TrimSpace(value)
	if value == "" || value == "*" {
		return "", false, false
	}

	value = expandHome(value, home)
	if strings.HasSuffix(value, "/*") {
		value = strings.TrimSuffix(value, "/*")
		prefix = true
		if value == "" {
			value = string(filepath.Separator)
		}
	}

	value = filepath.Clean(value)
	if !filepath.IsAbs(value) {
		return "", false, false
	}
	if resolved, err := filepath.EvalSymlinks(value); err == nil {
		value = filepath.Clean(resolved)
	}
	return value, prefix, true
}

func expandHome(value, home string) string {
	if value == "~" {
		return home
	}
	if strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		return filepath.Join(home, value[2:])
	}
	return value
}

func (c *Checker) loadSafeDirectories(localConfig string) ([]safeDirectoryEntry, error) {
	var exclude string
	if localConfig != "" {
		if resolved, err := canonicalExisting(localConfig); err == nil {
			exclude = resolved
		} else {
			exclude = filepath.Clean(localConfig)
		}
	}

	var entries []safeDirectoryEntry
	seen := make(map[string]bool)
	for _, path := range c.protectedConfigFiles() {
		canonical := filepath.Clean(path)
		if absolute, err := filepath.Abs(canonical); err == nil {
			canonical = absolute
		}
		if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
			canonical = filepath.Clean(resolved)
		}
		if exclude != "" && canonical == exclude {
			continue
		}
		if seen[canonical] {
			continue
		}
		seen[canonical] = true

		values, err := readSafeDirectoryValues(canonical)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			entries = append(entries, safeDirectoryEntry{
				value: value,
				home:  c.home(),
			})
		}
	}
	return entries, nil
}

func (c *Checker) protectedConfigFiles() []string {
	var paths []string

	if c.systemConfigSet {
		if c.systemConfig != "" {
			paths = append(paths, c.systemConfig)
		}
	} else if !c.envBool("GIT_CONFIG_NOSYSTEM") {
		if path, ok := c.getenv("GIT_CONFIG_SYSTEM"); ok {
			if path != "" {
				paths = append(paths, path)
			}
		} else {
			paths = append(paths, "/etc/gitconfig")
		}
	}

	if c.globalConfigsSet {
		for _, path := range c.globalConfigs {
			if path != "" {
				paths = append(paths, path)
			}
		}
		return paths
	}

	if path, ok := c.getenv("GIT_CONFIG_GLOBAL"); ok {
		if path != "" {
			paths = append(paths, path)
		}
		return paths
	}

	home := c.home()
	xdg := ""
	if value, ok := c.getenv("XDG_CONFIG_HOME"); ok {
		xdg = value
	}
	if xdg == "" && home != "" {
		xdg = filepath.Join(home, ".config")
	}
	if xdg != "" {
		paths = append(paths, filepath.Join(xdg, "git", "config"))
	}
	if home != "" {
		paths = append(paths, filepath.Join(home, ".gitconfig"))
	}
	return paths
}

func (c *Checker) envBool(name string) bool {
	value, ok := c.getenv(name)
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (c *Checker) home() string {
	if c.homeDir != "" {
		return c.homeDir
	}
	if home, ok := c.getenv("HOME"); ok {
		return home
	}
	home, _ := os.UserHomeDir()
	return home
}

func readSafeDirectoryValues(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	values, err := parseSafeDirectories(file)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return values, nil
}

func parseSafeDirectories(r io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1024*1024)

	var values []string
	sectionSafe := false
	var logical strings.Builder

	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		logical.WriteString(line)

		text := logical.String()
		if hasLineContinuation(text) {
			logical.Reset()
			logical.WriteString(text[:len(text)-1])
			continue
		}
		logical.Reset()

		line = strings.TrimSpace(text)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			safe, parseErr := isSafeSection(line)
			if parseErr != nil {
				return nil, fmt.Errorf("line %d: %w", lineNumber, parseErr)
			}
			sectionSafe = safe
			continue
		}
		if !sectionSafe {
			continue
		}

		name, rawValue, hasValue := splitConfigVariable(line)
		if !hasValue || !strings.EqualFold(name, "directory") {
			continue
		}
		value, err := parseConfigValue(rawValue)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNumber, err)
		}
		if value != "" {
			values = append(values, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if logical.Len() != 0 {
		return nil, fmt.Errorf("unterminated line continuation")
	}
	return values, nil
}

func hasLineContinuation(line string) bool {
	n := 0
	for i := len(line) - 1; i >= 0 && line[i] == '\\'; i-- {
		n++
	}
	return n%2 == 1
}

func isSafeSection(line string) (bool, error) {
	end := strings.IndexByte(line, ']')
	if end < 0 {
		return false, fmt.Errorf("unterminated section header")
	}
	header := strings.TrimSpace(line[1:end])
	if strings.ContainsRune(header, '"') {
		return false, nil
	}
	return strings.EqualFold(header, "safe"), nil
}

func splitConfigVariable(line string) (name, value string, hasValue bool) {
	if before, after, ok := strings.Cut(line, "="); ok {
		return strings.TrimSpace(before), after, true
	}
	return strings.TrimSpace(line), "", false
}

func parseConfigValue(raw string) (string, error) {
	var result strings.Builder
	quoted := false
	escaped := false

	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if escaped {
			switch ch {
			case 'n':
				result.WriteByte('\n')
			case 't':
				result.WriteByte('\t')
			case 'b':
				result.WriteByte('\b')
			case '\\', '"':
				result.WriteByte(ch)
			default:
				result.WriteByte(ch)
			}
			escaped = false
			continue
		}

		switch ch {
		case '\\':
			escaped = true
		case '"':
			quoted = !quoted
		case '#', ';':
			if !quoted {
				return strings.TrimSpace(result.String()), nil
			}
			result.WriteByte(ch)
		default:
			result.WriteByte(ch)
		}
	}
	if escaped {
		return "", fmt.Errorf("trailing escape")
	}
	if quoted {
		return "", fmt.Errorf("unterminated quoted value")
	}
	return strings.TrimSpace(result.String()), nil
}
