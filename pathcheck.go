package netsec

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrEmptyPath           = errors.New("netsec: empty path")
	ErrInvalidPath         = errors.New("netsec: invalid path")
	ErrUNCPath             = errors.New("netsec: UNC or network path rejected")
	ErrDevicePath          = errors.New("netsec: device path rejected")
	ErrFileURLWithHost     = errors.New("netsec: file URL with host rejected")
	ErrWebDAVPath          = errors.New("netsec: WebDAV path rejected")
	ErrDriveRelative       = errors.New("netsec: drive-relative path rejected")
	ErrDriveNotAllowed     = errors.New("netsec: drive not allowed")
	ErrAlternateDataStream = errors.New("netsec: alternate data stream rejected")
	ErrColon               = errors.New("netsec: colon outside drive prefix rejected")
	ErrReservedName        = errors.New("netsec: reserved device name rejected")
	ErrEncodedSeparator    = errors.New("netsec: percent-encoded separator rejected")
	ErrResolve             = errors.New("netsec: path resolution failed")
	ErrLinkTarget          = errors.New("netsec: resolved link target rejected")
)

// Alternative names kept explicit for callers that prefer the terminology used
// by Windows APIs.
var (
	ErrLeadingSeparator        = ErrUNCPath
	ErrNetworkPath             = ErrUNCPath
	ErrDeviceNamespace         = ErrDevicePath
	ErrDriveRelativePath       = ErrDriveRelative
	ErrReservedDeviceName      = ErrReservedName
	ErrPercentEncodedSeparator = ErrEncodedSeparator
	ErrADS                     = ErrAlternateDataStream
)

// ResolveFunc returns the final target of path. Callbacks should follow
// symlinks and reparse points to their final target and return path unchanged
// when it is not a link. An empty target is invalid.
//
// The callback is invoked only after all lexical checks succeed, so it must not
// be used as the first line of defense against UNC or device paths.
type ResolveFunc func(path string) (resolved string, err error)

// Config controls path admission.
type Config struct {
	// AllowedDrives contains drive letters that may be used by absolute drive
	// paths, for example []string{"C", "D:"}. An empty list denies every
	// drive-letter path.
	AllowedDrives []string

	// Resolve is an optional caller-provided resolver for symbolic links and
	// Windows reparse points. If it returns a different path, that target is
	// checked with the same policy. A nil resolver disables target checking.
	Resolve ResolveFunc
}

// Options is an alias for Config.
type Options = Config

// Checker is a reusable path admission policy.
type Checker struct {
	allowedDrives map[byte]struct{}
	resolve       ResolveFunc
}

// NewChecker creates a reusable checker and copies the drive allowlist.
func NewChecker(cfg Config) *Checker {
	allowed := make(map[byte]struct{}, len(cfg.AllowedDrives))
	for _, value := range cfg.AllowedDrives {
		if drive, ok := normalizeDrive(value); ok {
			allowed[drive] = struct{}{}
		}
	}
	return &Checker{allowedDrives: allowed, resolve: cfg.Resolve}
}

// CheckPath applies cfg to path without making any filesystem calls.
func CheckPath(path string, cfg Config) error {
	return NewChecker(cfg).Check(path)
}

// Check is an alias for CheckPath.
func Check(path string, cfg Config) error {
	return CheckPath(path, cfg)
}

// Check applies cfg and then checks the caller-provided resolved target, if
// any.
func (cfg Config) Check(path string) error {
	return CheckPath(path, cfg)
}

// Check applies the checker to path.
func (c *Checker) Check(path string) error {
	if c == nil {
		return fmt.Errorf("%w: nil checker", ErrInvalidPath)
	}
	if err := checkLexical(path, c.allowedDrives); err != nil {
		return err
	}
	if c.resolve == nil {
		return nil
	}

	target, err := c.resolve(path)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrResolve, err)
	}
	if target == "" {
		return fmt.Errorf("%w: resolver returned an empty path", ErrLinkTarget)
	}
	if target == path {
		return nil
	}
	if err := checkLexical(target, c.allowedDrives); err != nil {
		return fmt.Errorf("%w: %w", ErrLinkTarget, err)
	}
	return nil
}

func checkLexical(path string, allowedDrives map[byte]struct{}) error {
	if path == "" {
		return ErrEmptyPath
	}
	if strings.IndexByte(path, 0) >= 0 {
		return fmt.Errorf("%w: contains NUL", ErrInvalidPath)
	}

	// This check intentionally uses the original string. No cleaning or
	// separator collapsing may happen before it, because that could turn a
	// network path into a seemingly local one.
	if hasLeadingDoubleSeparator(path) {
		return fmt.Errorf("%w: %q", ErrUNCPath, path)
	}
	if hasDevicePath(path) {
		return fmt.Errorf("%w: %q", ErrDevicePath, path)
	}
	if isFileURLWithHost(path) {
		return fmt.Errorf("%w: %q", ErrFileURLWithHost, path)
	}

	lower := strings.ToLower(path)
	if strings.Contains(lower, "@ssl") || strings.Contains(lower, "@443") ||
		strings.Contains(lower, "davwwwroot") {
		return fmt.Errorf("%w: %q", ErrWebDAVPath, path)
	}
	if strings.Contains(lower, "%5c") {
		return fmt.Errorf("%w: %q", ErrEncodedSeparator, path)
	}

	hasDrive := hasDrivePrefix(path)
	if hasDrive {
		if len(path) < 3 || !isPathSeparator(path[2]) {
			return fmt.Errorf("%w: %q", ErrDriveRelative, path)
		}
		drive := asciiUpper(path[0])
		if _, ok := allowedDrives[drive]; !ok {
			return fmt.Errorf("%w: %c", ErrDriveNotAllowed, path[0])
		}
	}

	if strings.Contains(lower, ":$data") {
		return fmt.Errorf("%w: %q", ErrAlternateDataStream, path)
	}

	colonStart := 0
	if hasDrive {
		colonStart = 2
	}
	if strings.IndexByte(path[colonStart:], ':') >= 0 {
		return fmt.Errorf("%w: %q", ErrColon, path)
	}

	if hasReservedComponent(path) {
		return fmt.Errorf("%w: %q", ErrReservedName, path)
	}
	return nil
}

func hasLeadingDoubleSeparator(path string) bool {
	return len(path) >= 2 && isPathSeparator(path[0]) && isPathSeparator(path[1])
}

func hasDevicePath(path string) bool {
	normalized := strings.ReplaceAll(strings.ToLower(path), `\`, "/")

	if strings.Contains(normalized, "globalroot") {
		return true
	}
	if strings.Contains(normalized, "//?/") ||
		strings.Contains(normalized, "//./") ||
		strings.Contains(normalized, "/??/") ||
		strings.Contains(normalized, "/?/") {
		return true
	}
	return strings.HasPrefix(normalized, "/device/") ||
		strings.HasPrefix(normalized, "/dosdevices/")
}

func isFileURLWithHost(path string) bool {
	if len(path) < 5 || !strings.EqualFold(path[:5], "file:") {
		return false
	}
	rest := path[5:]
	if len(rest) < 3 || !isPathSeparator(rest[0]) || !isPathSeparator(rest[1]) {
		return false
	}
	// file://host/path has a non-empty authority. file:///path does not.
	return !isPathSeparator(rest[2])
}

func hasDrivePrefix(path string) bool {
	return len(path) >= 2 && isASCIIAlpha(path[0]) && path[1] == ':'
}

func normalizeDrive(value string) (byte, bool) {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[1] == ':' {
		value = value[:1]
	}
	if len(value) != 1 || !isASCIIAlpha(value[0]) {
		return 0, false
	}
	return asciiUpper(value[0]), true
}

func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func asciiUpper(value byte) byte {
	if value >= 'a' && value <= 'z' {
		return value - ('a' - 'A')
	}
	return value
}

func isPathSeparator(value byte) bool {
	return value == '/' || value == '\\'
}

var reservedDeviceNames = map[string]struct{}{
	"CON":     {},
	"PRN":     {},
	"AUX":     {},
	"NUL":     {},
	"CLOCK$":  {},
	"CONIN$":  {},
	"CONOUT$": {},
	"COM1":    {},
	"COM2":    {},
	"COM3":    {},
	"COM4":    {},
	"COM5":    {},
	"COM6":    {},
	"COM7":    {},
	"COM8":    {},
	"COM9":    {},
	"LPT1":    {},
	"LPT2":    {},
	"LPT3":    {},
	"LPT4":    {},
	"LPT5":    {},
	"LPT6":    {},
	"LPT7":    {},
	"LPT8":    {},
	"LPT9":    {},
	"COM¹":    {},
	"COM²":    {},
	"COM³":    {},
	"LPT¹":    {},
	"LPT²":    {},
	"LPT³":    {},
}

func hasReservedComponent(path string) bool {
	start := 0
	for i := 0; i <= len(path); i++ {
		if i == len(path) || isPathSeparator(path[i]) {
			if isReservedComponent(path[start:i]) {
				return true
			}
			start = i + 1
		}
	}
	return false
}

func isReservedComponent(component string) bool {
	component = strings.TrimRight(component, " .")
	if component == "" {
		return false
	}
	if dot := strings.IndexByte(component, '.'); dot >= 0 {
		component = component[:dot]
	}
	component = strings.TrimRight(component, " .")
	if component == "" {
		return false
	}
	_, reserved := reservedDeviceNames[strings.ToUpper(component)]
	return reserved
}
