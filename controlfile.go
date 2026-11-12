package netsec

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrInvalidControlFile indicates malformed extension control-file metadata.
var ErrInvalidControlFile = errors.New("invalid control file")

// ParseControlFile parses PostgreSQL extension control-file metadata.
//
// Only security-relevant keys are retained: name, trusted, superuser,
// superuser_only, requires, and module_pathname. Other control-file keys are
// ignored. Values may be single-quoted, double-quoted, or bare. In accordance
// with PostgreSQL semantics, superuser defaults to true when the key is absent.
func ParseControlFile(data []byte) (Extension, error) {
	return ParseControlFileReader(strings.NewReader(string(data)))
}

// ParseControl parses PostgreSQL extension control-file metadata from bytes.
func ParseControl(data []byte) (Extension, error) {
	return ParseControlFile(data)
}

// RawControlFile is raw key/value control-file metadata.
type RawControlFile map[string]string

// ParseControlFileReader parses control-file metadata from r.
func ParseControlFileReader(r io.Reader) (Extension, error) {
	if r == nil {
		return Extension{}, fmt.Errorf("%w: nil reader", ErrInvalidControlFile)
	}

	ext := Extension{Superuser: true}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, rawValue, ok := splitControlLine(line)
		if !ok {
			return Extension{}, fmt.Errorf("%w: line %d: expected key = value", ErrInvalidControlFile, lineNumber)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			return Extension{}, fmt.Errorf("%w: line %d: empty key", ErrInvalidControlFile, lineNumber)
		}

		value, err := parseControlValue(rawValue)
		if err != nil {
			return Extension{}, fmt.Errorf("%w: line %d: %v", ErrInvalidControlFile, lineNumber, err)
		}

		switch key {
		case "name":
			ext.Name = value
		case "trusted":
			v, err := parseControlBool(value, key)
			if err != nil {
				return Extension{}, fmt.Errorf("%w: line %d: %v", ErrInvalidControlFile, lineNumber, err)
			}
			ext.Trusted = v
		case "superuser":
			v, err := parseControlBool(value, key)
			if err != nil {
				return Extension{}, fmt.Errorf("%w: line %d: %v", ErrInvalidControlFile, lineNumber, err)
			}
			ext.Superuser = v
		case "superuser_only":
			v, err := parseControlBool(value, key)
			if err != nil {
				return Extension{}, fmt.Errorf("%w: line %d: %v", ErrInvalidControlFile, lineNumber, err)
			}
			ext.SuperuserOnly = v
		case "requires":
			ext.Requires = parseRequires(value)
		case "module_pathname":
			ext.ModulePathname = value
		}
	}
	if err := scanner.Err(); err != nil {
		return Extension{}, fmt.Errorf("%w: %v", ErrInvalidControlFile, err)
	}
	return ext, nil
}

func splitControlLine(line string) (string, string, bool) {
	index := strings.IndexByte(line, '=')
	if index < 0 {
		return "", "", false
	}
	return line[:index], line[index+1:], true
}

func parseControlValue(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}

	if raw[0] == '\'' || raw[0] == '"' {
		quote := raw[0]
		var builder strings.Builder
		for i := 1; i < len(raw); i++ {
			if raw[i] != quote {
				builder.WriteByte(raw[i])
				continue
			}
			if quote == '\'' && i+1 < len(raw) && raw[i+1] == '\'' {
				builder.WriteByte('\'')
				i++
				continue
			}
			rest := strings.TrimSpace(raw[i+1:])
			if rest != "" && !strings.HasPrefix(rest, "#") {
				return "", fmt.Errorf("unexpected text after closing quote")
			}
			return builder.String(), nil
		}
		return "", fmt.Errorf("unterminated quoted value")
	}

	if comment := strings.IndexByte(raw, '#'); comment >= 0 {
		raw = raw[:comment]
	}
	return strings.TrimSpace(raw), nil
}

func parseControlBool(value, key string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "on", "1":
		return true, nil
	case "false", "no", "off", "0":
		return false, nil
	default:
		return false, fmt.Errorf("%s value %q is not a boolean", key, value)
	}
}

func parseRequires(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	requires := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		name = strings.Trim(name, `"'`)
		if name != "" {
			requires = append(requires, name)
		}
	}
	return requires
}

// ParseControlBool is exported as a small helper for adapters which receive
// control-file values as strings instead of parsed metadata.
func ParseControlBool(value string) (bool, error) {
	return parseControlBool(value, "value")
}

// ParseRequiresList splits a control-file requires value into names.
func ParseRequiresList(value string) []string {
	return parseRequires(value)
}

// ControlFileFromMap converts a map of raw control-file values into Extension
// metadata. It uses the same defaults and value syntax as ParseControlFile.
func ControlFileFromMap(values map[string]string) (Extension, error) {
	ext := Extension{Superuser: true}
	for key, rawValue := range values {
		value, err := parseControlValue(rawValue)
		if err != nil {
			return Extension{}, fmt.Errorf("%w: %s: %v", ErrInvalidControlFile, key, err)
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			ext.Name = value
		case "trusted":
			v, err := parseControlBool(value, "trusted")
			if err != nil {
				return Extension{}, fmt.Errorf("%w: %v", ErrInvalidControlFile, err)
			}
			ext.Trusted = v
		case "superuser":
			v, err := parseControlBool(value, "superuser")
			if err != nil {
				return Extension{}, fmt.Errorf("%w: %v", ErrInvalidControlFile, err)
			}
			ext.Superuser = v
		case "superuser_only":
			v, err := parseControlBool(value, "superuser_only")
			if err != nil {
				return Extension{}, fmt.Errorf("%w: %v", ErrInvalidControlFile, err)
			}
			ext.SuperuserOnly = v
		case "requires":
			ext.Requires = parseRequires(value)
		case "module_pathname":
			ext.ModulePathname = value
		}
	}
	return ext, nil
}

// ExtensionFromMap is an alias for ControlFileFromMap.
func ExtensionFromMap(values map[string]string) (Extension, error) {
	return ControlFileFromMap(values)
}

// CheckControlFile parses control-file bytes and applies the installation
// policy.
func CheckControlFile(role any, data []byte, installed ...any) error {
	ext, err := ParseControlFile(data)
	if err != nil {
		return err
	}
	args := append([]any{role, ext}, installed...)
	return CheckInstall(args...)
}

// CheckControl is an alias for CheckControlFile.
func CheckControl(role any, data []byte, installed ...any) error {
	return CheckControlFile(role, data, installed...)
}

// CheckControlFileReader parses control-file metadata from r and applies the
// installation policy.
func CheckControlFileReader(role any, r io.Reader, installed ...any) error {
	ext, err := ParseControlFileReader(r)
	if err != nil {
		return err
	}
	args := append([]any{role, ext}, installed...)
	return CheckInstall(args...)
}
