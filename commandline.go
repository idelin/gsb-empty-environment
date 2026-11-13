package netsec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const defaultPathExt = ".COM;.EXE;.BAT;.CMD;.VBS;.VBE;.JS;.JSE;.WSF;.WSH;.MSC"

// ErrUnsafeArgument is returned when an argument cannot safely be passed to
// cmd.exe through a .bat or .cmd target.
var ErrUnsafeArgument = errors.New("unsafe command-line argument")

// Env contains the Windows path variables used to resolve extensionless
// executable targets.
type Env struct {
	Path    string
	PathExt string
}

// CurrentEnv reads PATH and PATHEXT from the process environment.
func CurrentEnv() Env {
	return Env{
		Path:    os.Getenv("PATH"),
		PathExt: os.Getenv("PATHEXT"),
	}
}

// IsBatchTarget reports whether target is a .bat or .cmd file. Windows ignores
// trailing spaces and dots in path components, and extensionless targets are
// completed using PATHEXT before checking the extension.
func (env Env) IsBatchTarget(target string) bool {
	normalized := strings.TrimRight(target, " .")
	if normalized == "" {
		return false
	}

	ext := strings.ToLower(extension(normalized))
	switch ext {
	case ".bat", ".cmd":
		return true
	case "":
		return env.resolvesToBatch(normalized, regularFileExists)
	default:
		return false
	}
}

// ValidateArguments enforces the strict argument policy required by .bat and
// .cmd targets. Other targets only reject bytes that cannot appear in a
// Windows command line.
func (env Env) ValidateArguments(target string, args []string) error {
	for index, arg := range args {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("%w: argument %d contains a NUL byte", ErrUnsafeArgument, index)
		}
	}

	if !env.IsBatchTarget(target) {
		return nil
	}

	for index, arg := range args {
		if strings.ContainsAny(arg, "%\"&|<>^!\n\r") {
			return fmt.Errorf("%w: argument %d contains a cmd.exe metacharacter", ErrUnsafeArgument, index)
		}
	}

	return nil
}

// ValidateArguments uses PATH and PATHEXT from the current environment.
func ValidateArguments(target string, args []string) error {
	return CurrentEnv().ValidateArguments(target, args)
}

// SafeCommand validates args before creating an exec.Cmd. The returned command
// still receives raw argv values; Go applies Windows C runtime quoting when it
// starts the process.
func SafeCommand(name string, args ...string) (*exec.Cmd, error) {
	if err := ValidateArguments(name, args); err != nil {
		return nil, err
	}
	return exec.Command(name, args...), nil
}

// EscapeArgument applies the Windows C runtime command-line quoting rules.
func EscapeArgument(arg string) string {
	needsQuotes := arg == "" || strings.ContainsAny(arg, " \t\"")
	var builder strings.Builder

	if needsQuotes {
		builder.WriteByte('"')
	}

	for index := 0; index < len(arg); index++ {
		if arg[index] != '\\' {
			if arg[index] == '"' {
				builder.WriteByte('\\')
			}
			builder.WriteByte(arg[index])
			continue
		}

		count := 0
		for index+count < len(arg) && arg[index+count] == '\\' {
			count++
		}

		nextIsQuote := index+count < len(arg) && arg[index+count] == '"'
		atEnd := index+count == len(arg)
		if nextIsQuote || (atEnd && needsQuotes) {
			builder.WriteString(strings.Repeat(`\\`, count))
		} else {
			builder.WriteString(strings.Repeat(`\\`, count/2))
			if count%2 == 1 {
				builder.WriteByte('\\')
			}
		}

		if nextIsQuote {
			builder.WriteString(`\"`)
			index += count
		} else {
			index += count - 1
		}
	}

	if needsQuotes {
		builder.WriteByte('"')
	}

	return builder.String()
}

// EscapeCommandLine validates the arguments and renders a Windows command line
// using C runtime quoting rules.
func EscapeCommandLine(target string, args []string) (string, error) {
	if strings.TrimSpace(target) == "" {
		return "", errors.New("command target must not be empty")
	}
	if err := ValidateArguments(target, args); err != nil {
		return "", err
	}

	parts := make([]string, 0, len(args)+1)
	parts = append(parts, EscapeArgument(target))
	for _, arg := range args {
		parts = append(parts, EscapeArgument(arg))
	}
	return strings.Join(parts, " "), nil
}

func (env Env) resolvesToBatch(target string, exists func(string) bool) bool {
	pathExt := env.PathExt
	if pathExt == "" {
		pathExt = defaultPathExt
	}

	for _, candidateRoot := range env.candidateRoots(target) {
		candidateBase := candidateRoot
		for _, executableExt := range splitPathExt(pathExt) {
			candidate := candidateBase + executableExt
			if !exists(candidate) {
				continue
			}
			return strings.EqualFold(executableExt, ".bat") || strings.EqualFold(executableExt, ".cmd")
		}
	}

	return false
}

func (env Env) candidateRoots(target string) []string {
	if containsPathSeparator(target) {
		return []string{target}
	}

	roots := []string{"."}
	for _, directory := range strings.Split(env.Path, ";") {
		if directory != "" {
			roots = append(roots, directory)
		}
	}

	result := make([]string, 0, len(roots))
	for _, root := range roots {
		result = append(result, filepath.Join(root, target))
	}
	return result
}

func splitPathExt(pathExt string) []string {
	entries := strings.Split(pathExt, ";")
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if !strings.HasPrefix(entry, ".") {
			entry = "." + entry
		}
		result = append(result, entry)
	}
	return result
}

func extension(path string) string {
	base := path
	if separator := strings.LastIndexAny(path, `/\`); separator >= 0 {
		base = path[separator+1:]
	}
	if dot := strings.LastIndexByte(base, '.'); dot >= 0 {
		return base[dot:]
	}
	return ""
}

func containsPathSeparator(path string) bool {
	return strings.ContainsAny(path, `/\`)
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
