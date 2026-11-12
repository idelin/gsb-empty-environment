package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	// MaxFileSize is the largest .env file that will be accepted.
	MaxFileSize = 64 * 1024
	// MaxLines is the largest number of physical lines that will be accepted.
	MaxLines = 512
)

var (
	// ErrFileTooLarge is returned when the input exceeds MaxFileSize.
	ErrFileTooLarge = errors.New("dotenv file exceeds 64 KiB")
	// ErrTooManyLines is returned when the input exceeds MaxLines.
	ErrTooManyLines = errors.New("dotenv file exceeds 512 lines")
	// ErrNUL is returned when a value contains a NUL byte, either literally
	// or through a \0 escape.
	ErrNUL = errors.New("dotenv value contains NUL")
	// ErrProtected is returned for variables that can alter process behavior.
	ErrProtected = errors.New("protected dotenv variable")
	// ErrInvalidSyntax is returned for malformed dotenv input.
	ErrInvalidSyntax = errors.New("invalid dotenv syntax")
	// ErrBOM is returned when a UTF-8 BOM appears anywhere except the start.
	ErrBOM = errors.New("UTF-8 BOM is only allowed at the start of the file")
)

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

// ParseError describes an error at a particular line in a dotenv file.
type ParseError struct {
	Line int
	Key  string
	Err  error
}

func (e *ParseError) Error() string {
	var b strings.Builder
	b.WriteString("dotenv")
	if e.Line > 0 {
		fmt.Fprintf(&b, " line %d", e.Line)
	}
	if e.Key != "" {
		fmt.Fprintf(&b, " variable %q", e.Key)
	}
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

// Unwrap allows errors.Is and errors.As to inspect the underlying cause.
func (e *ParseError) Unwrap() error {
	return e.Err
}

// Parse validates data and returns the environment variables it defines.
// The returned map is not applied to the process environment.
//
// Any error rejects the whole input; Parse never returns a partially parsed
// map. Additional protected variable names may be supplied as variadic
// arguments.
func Parse(data []byte, protected ...string) (map[string]string, error) {
	if len(data) > MaxFileSize {
		return nil, &ParseError{Err: ErrFileTooLarge}
	}

	if bytes.HasPrefix(data, utf8BOM) {
		data = data[len(utf8BOM):]
	}
	if i := bytes.Index(data, utf8BOM); i >= 0 {
		return nil, &ParseError{Line: lineAt(data, i), Err: ErrBOM}
	}
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return nil, &ParseError{Line: lineAt(data, i), Err: ErrNUL}
	}
	if physicalLineCount(data) > MaxLines {
		return nil, &ParseError{Err: ErrTooManyLines}
	}

	return parseLines(data, makeProtectedSet(protected))
}

// ParseReader reads and validates a dotenv stream. It never reads more than
// MaxFileSize+1 bytes.
func ParseReader(r io.Reader, protected ...string) (map[string]string, error) {
	data, err := readLimited(r)
	if err != nil {
		return nil, err
	}
	return Parse(data, protected...)
}

// ParseFile reads and validates a dotenv file without changing the process
// environment.
func ParseFile(path string, protected ...string) (map[string]string, error) {
	data, err := readFileLimited(path)
	if err != nil {
		return nil, err
	}
	return Parse(data, protected...)
}

// LoadReader validates and then applies a dotenv stream to the process
// environment. Validation is completed before any environment variable is
// changed, so malformed input leaves the environment untouched.
func LoadReader(r io.Reader, protected ...string) error {
	values, err := ParseReader(r, protected...)
	if err != nil {
		return err
	}
	return apply(values)
}

// LoadFile validates and then applies a dotenv file to the process
// environment. Validation is completed before any environment variable is
// changed, so malformed input leaves the environment untouched.
func LoadFile(path string, protected ...string) error {
	values, err := ParseFile(path, protected...)
	if err != nil {
		return err
	}
	return apply(values)
}

// Load is an alias for LoadFile.
func Load(path string, protected ...string) error {
	return LoadFile(path, protected...)
}

func readFileLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readLimited(f)
}

func readLimited(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileSize {
		return nil, &ParseError{Err: ErrFileTooLarge}
	}
	return data, nil
}

func parseLines(data []byte, protected map[string]struct{}) (map[string]string, error) {
	lines := splitLines(data)
	values := make(map[string]string)

	for i := 0; i < len(lines); i++ {
		lineNo := i + 1
		line := strings.TrimLeft(lines[i], " \t")
		if line == "" || line[0] == '#' {
			continue
		}

		if strings.HasPrefix(line, "export") && len(line) > len("export") && isHorizontalSpace(line[len("export")]) {
			line = strings.TrimLeft(line[len("export"):], " \t")
		}

		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			return nil, &ParseError{Line: lineNo, Err: fmt.Errorf("%w: expected NAME=VALUE", ErrInvalidSyntax)}
		}

		name := line[:eq]
		if !validName(name) {
			return nil, &ParseError{
				Line: lineNo,
				Key:  name,
				Err:  fmt.Errorf("%w: NAME must match [A-Za-z_][A-Za-z0-9_]*", ErrInvalidSyntax),
			}
		}
		if isProtected(name, protected) {
			return nil, &ParseError{Line: lineNo, Key: name, Err: ErrProtected}
		}

		raw := line[eq+1:]
		leading := 0
		for leading < len(raw) && isHorizontalSpace(raw[leading]) {
			leading++
		}

		var (
			value string
			err   error
		)
		if leading < len(raw) && raw[leading] == '"' {
			value, i, err = parseDoubleQuoted(lines, i, raw, leading+1)
		} else if leading < len(raw) && raw[leading] == '\'' {
			value, err = parseSingleQuoted(raw, leading+1)
		} else {
			value, err = parseUnquoted(raw)
		}
		if err != nil {
			return nil, &ParseError{Line: lineNo, Key: name, Err: err}
		}

		values[name] = value
	}

	return values, nil
}

func parseDoubleQuoted(lines []string, lineIndex int, first string, start int) (string, int, error) {
	var b strings.Builder
	line := first

	for {
		for i := start; i < len(line); i++ {
			switch line[i] {
			case '"':
				if err := validateTrailing(line[i+1:]); err != nil {
					return "", lineIndex, err
				}
				return b.String(), lineIndex, nil
			case '\\':
				if i+1 >= len(line) {
					return "", lineIndex, fmt.Errorf("%w: incomplete escape sequence", ErrInvalidSyntax)
				}
				i++
				switch line[i] {
				case 'n':
					b.WriteByte('\n')
				case '\\':
					b.WriteByte('\\')
				case '"':
					b.WriteByte('"')
				case '0':
					return "", lineIndex, ErrNUL
				default:
					return "", lineIndex, fmt.Errorf("%w: unsupported escape \\%c", ErrInvalidSyntax, line[i])
				}
			case 0:
				return "", lineIndex, ErrNUL
			default:
				b.WriteByte(line[i])
			}
		}

		b.WriteByte('\n')
		lineIndex++
		if lineIndex >= len(lines) {
			return "", lineIndex, fmt.Errorf("%w: unterminated double-quoted value", ErrInvalidSyntax)
		}
		line = lines[lineIndex]
		start = 0
	}
}

func parseSingleQuoted(line string, start int) (string, error) {
	var b strings.Builder
	for i := start; i < len(line); i++ {
		if line[i] == '\'' {
			if err := validateTrailing(line[i+1:]); err != nil {
				return "", err
			}
			return b.String(), nil
		}
		if line[i] == 0 {
			return "", ErrNUL
		}
		b.WriteByte(line[i])
	}
	return "", fmt.Errorf("%w: unterminated single-quoted value", ErrInvalidSyntax)
}

func parseUnquoted(raw string) (string, error) {
	if i := inlineCommentIndex(raw); i >= 0 {
		raw = raw[:i]
	}
	value := strings.Trim(raw, " \t")
	if strings.IndexByte(value, 0) >= 0 {
		return "", ErrNUL
	}
	for i := 0; i+1 < len(value); i++ {
		if value[i] == '\\' && value[i+1] == '0' {
			return "", ErrNUL
		}
	}
	return value, nil
}

func validateTrailing(rest string) error {
	rest = strings.TrimLeft(rest, " \t")
	if rest == "" || rest[0] == '#' {
		return nil
	}
	return fmt.Errorf("%w: unexpected text after quoted value", ErrInvalidSyntax)
}

func inlineCommentIndex(s string) int {
	for i := 1; i < len(s); i++ {
		if s[i] == '#' && isHorizontalSpace(s[i-1]) {
			return i
		}
	}
	return -1
}

func validName(name string) bool {
	if len(name) == 0 || !isNameStart(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !isNameChar(name[i]) {
			return false
		}
	}
	return true
}

func isNameStart(c byte) bool {
	return c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

func isNameChar(c byte) bool {
	return isNameStart(c) || c >= '0' && c <= '9'
}

func isHorizontalSpace(c byte) bool {
	return c == ' ' || c == '\t'
}

func makeProtectedSet(extra []string) map[string]struct{} {
	set := make(map[string]struct{}, len(extra))
	for _, name := range extra {
		if name != "" {
			set[name] = struct{}{}
		}
	}
	return set
}

func isProtected(name string, extra map[string]struct{}) bool {
	switch name {
	case "BASH_ENV", "ENV", "IFS", "PATH":
		return true
	}
	if strings.HasPrefix(name, "LD_") || strings.HasPrefix(name, "DYLD_") {
		return true
	}
	_, ok := extra[name]
	return ok
}

func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

func physicalLineCount(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}

func lineAt(data []byte, offset int) int {
	return bytes.Count(data[:offset], []byte{'\n'}) + 1
}

type priorEnv struct {
	key     string
	value   string
	existed bool
}

func apply(values map[string]string) error {
	applied := make([]priorEnv, 0, len(values))
	for key, value := range values {
		oldValue, existed := os.LookupEnv(key)
		if err := os.Setenv(key, value); err != nil {
			for i := len(applied) - 1; i >= 0; i-- {
				if applied[i].existed {
					_ = os.Setenv(applied[i].key, applied[i].value)
				} else {
					_ = os.Unsetenv(applied[i].key)
				}
			}
			return fmt.Errorf("dotenv: set %q: %w", key, err)
		}
		applied = append(applied, priorEnv{key: key, value: oldValue, existed: existed})
	}
	return nil
}
