package netsec

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// configFilePaths locates the system and global Git config files. The
// GIT_CONFIG_SYSTEM and GIT_CONFIG_GLOBAL environment variables override the
// defaults, matching Git's own behavior. Repository-local configuration is
// deliberately never included.
func configFilePaths() (system, global string) {
	system = os.Getenv("GIT_CONFIG_SYSTEM")
	if system == "" {
		system = "/etc/gitconfig"
	}

	global = os.Getenv("GIT_CONFIG_GLOBAL")
	if global == "" {
		if home, err := os.UserHomeDir(); err == nil {
			global = filepath.Join(home, ".gitconfig")
		}
	}
	return system, global
}

// loadSafeDirectories reads safe.directory entries from system and global
// configuration only. A missing file is not an error; any other read failure
// is returned so callers fail closed instead of silently trusting.
func (e *Evaluator) loadSafeDirectories() ([]string, error) {
	var entries []string
	for _, file := range []string{e.systemConfig, e.globalConfig} {
		if file == "" {
			continue
		}
		parsed, err := parseSafeDirectories(file)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		entries = append(entries, parsed...)
	}
	return entries, nil
}

// parseSafeDirectories extracts every safe.directory value from a gitconfig
// file. Only the [safe] section's "directory" keys are relevant; the parser
// understands section headers, subsections and quoted values.
func parseSafeDirectories(file string) ([]string, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []string
	section := ""
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "[") {
			section = parseSectionHeader(line)
			continue
		}

		key, value, ok := parseKeyValue(line)
		if !ok {
			continue
		}
		// Only the bare [safe] section carries safe.directory; a subsection
		// such as [safe "foo"] is a different section and must be ignored.
		if section == "safe" && key == "directory" {
			entries = append(entries, unquoteConfigValue(value))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// parseSectionHeader returns a lowercased header in which the primary section
// name and any subsection are kept distinct: "[safe]" yields "safe" while
// '[safe "x"]' yields `safe "x"`.
func parseSectionHeader(line string) string {
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
	return strings.ToLower(body)
}

// parseKeyValue splits a "name = value" line. The key is lowercased and the
// value is returned verbatim (quotes still attached) for later unquoting.
func parseKeyValue(line string) (key, value string, ok bool) {
	idx := strings.IndexByte(line, '=')
	if idx < 0 {
		return "", "", false
	}
	key = strings.ToLower(strings.TrimSpace(line[:idx]))
	value = strings.TrimSpace(line[idx+1:])
	if key == "" {
		return "", "", false
	}
	return key, value, true
}

// unquoteConfigValue removes surrounding double quotes and resolves the
// escape sequences Git permits inside quoted config values.
func unquoteConfigValue(raw string) string {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return raw
	}
	body := raw[1 : len(raw)-1]
	var builder strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' && i+1 < len(body) {
			i++
			switch body[i] {
			case 'n':
				builder.WriteByte('\n')
			case 't':
				builder.WriteByte('\t')
			case '"':
				builder.WriteByte('"')
			case '\\':
				builder.WriteByte('\\')
			default:
				builder.WriteByte(body[i])
			}
			continue
		}
		builder.WriteByte(body[i])
	}
	return builder.String()
}
