package netsec

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// ParseConfig reads an rsyncd.conf-style configuration. It understands the
// parameters needed for admission decisions and ignores unrelated parameters.
// Module names are retained exactly as written, including their case.
func ParseConfig(reader io.Reader) (*Config, error) {
	cfg := &Config{
		Modules: make(map[string]Module),
		Global: Module{
			ReadOnly:  true,
			UseChroot: true,
		},
	}

	var current *Module
	scanner := bufio.NewScanner(reader)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSuffix(scanner.Text(), "\r")
		line = strings.TrimPrefix(line, "\ufeff")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}

		if strings.HasPrefix(trimmed, "[") {
			closeIndex := strings.IndexByte(trimmed, ']')
			if closeIndex < 0 {
				return nil, fmt.Errorf("line %d: unterminated module header", lineNumber)
			}
			remainder := strings.TrimSpace(trimmed[closeIndex+1:])
			if remainder != "" && !strings.HasPrefix(remainder, "#") && !strings.HasPrefix(remainder, ";") {
				return nil, fmt.Errorf("line %d: unexpected text after module header", lineNumber)
			}

			name := strings.TrimSpace(trimmed[1:closeIndex])
			if name == "" {
				return nil, fmt.Errorf("line %d: empty module name", lineNumber)
			}
			if name == "global" {
				current = &cfg.Global
				continue
			}
			if _, exists := cfg.Modules[name]; exists {
				return nil, fmt.Errorf("line %d: duplicate module %q", lineNumber, name)
			}
			module := cloneModule(cfg.Global)
			module.Name = name
			current = &module
			cfg.Modules[name] = cloneModule(module)
			continue
		}

		key, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key = value", lineNumber)
		}
		key = strings.Join(strings.Fields(strings.ToLower(key)), " ")
		value = unquote(stripConfigComment(strings.TrimSpace(value)))

		if current == nil {
			current = &cfg.Global
		}
		if err := setModuleParameter(current, key, value); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNumber, err)
		}
		if current != &cfg.Global && current.Name != "" {
			cfg.Modules[current.Name] = cloneModule(*current)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	return cfg, nil
}

// ParseConfigString parses configuration text.
func ParseConfigString(text string) (*Config, error) {
	return ParseConfig(strings.NewReader(text))
}

// ParseConfigFile reads and parses an rsyncd.conf file.
func ParseConfigFile(filename string) (*Config, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return ParseConfig(file)
}

func setModuleParameter(module *Module, key, value string) error {
	switch key {
	case "path":
		module.Root = value
	case "hosts allow":
		module.HostsAllow = splitConfigList([]string{value})
	case "hosts deny":
		module.HostsDeny = splitConfigList([]string{value})
	case "read only":
		parsed, err := parseConfigBool(value)
		if err != nil {
			return fmt.Errorf("read only: %w", err)
		}
		module.ReadOnly = parsed
	case "use chroot":
		parsed, err := parseConfigBool(value)
		if err != nil {
			return fmt.Errorf("use chroot: %w", err)
		}
		module.UseChroot = parsed
	case "refuse options":
		module.RefuseOptions = splitConfigList([]string{value})
	case "exclude":
		module.Exclude = splitConfigList([]string{value})
	}
	return nil
}

func parseConfigBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "yes", "true", "1", "on":
		return true, nil
	case "no", "false", "0", "off":
		return false, nil
	default:
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed, nil
		}
		return false, fmt.Errorf("invalid boolean %q", value)
	}
}

func unquote(value string) string {
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func stripConfigComment(value string) string {
	var quote byte
	for index := 0; index < len(value); index++ {
		char := value[index]
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			continue
		}
		if char == '"' || char == '\'' {
			quote = char
			continue
		}
		if (char == '#' || char == ';') && (index == 0 || value[index-1] == ' ' || value[index-1] == '\t') {
			return strings.TrimSpace(value[:index])
		}
	}
	return strings.TrimSpace(value)
}

func cloneModule(module Module) Module {
	clone := module
	clone.HostsAllow = append([]string(nil), module.HostsAllow...)
	clone.HostsDeny = append([]string(nil), module.HostsDeny...)
	clone.RefuseOptions = append([]string(nil), module.RefuseOptions...)
	clone.Exclude = append([]string(nil), module.Exclude...)
	return clone
}
