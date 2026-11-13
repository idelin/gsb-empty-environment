package netsec

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// ErrUnsafePath is the sentinel wrapped by every rejection returned by this
// package. Callers can use errors.Is(err, ErrUnsafePath) to distinguish an
// admission failure from an unrelated error.
var ErrUnsafePath = errors.New("unsafe path")

// Reason identifies the rule that rejected a path.
type Reason string

const (
	// ReasonEmptyPath indicates an empty or malformed file URL.
	ReasonEmptyPath Reason = "empty_path"
	// ReasonNULByte indicates that the path contains a NUL byte.
	ReasonNULByte Reason = "nul_byte"
	// ReasonLeadingSeparators indicates a path starting with two path
	// separators. This is checked before repeated separators are collapsed.
	ReasonLeadingSeparators Reason = "leading_separators"
	// ReasonFileURLHost indicates a file URL with an authority/host part.
	ReasonFileURLHost Reason = "file_url_host"
	// ReasonDevicePrefix indicates an UNC device or device namespace prefix.
	ReasonDevicePrefix Reason = "device_prefix"
	// ReasonGLOBALROOT indicates a path containing GLOBALROOT.
	ReasonGLOBALROOT Reason = "globalroot"
	// ReasonWebDAV indicates an @SSL or @443 WebDAV path.
	ReasonWebDAV Reason = "webdav"
	// ReasonDavWWWRoot indicates a path containing DavWWWRoot.
	ReasonDavWWWRoot Reason = "davwwwroot"
	// ReasonPercentEncodedSeparator indicates a literal %5c in the name.
	ReasonPercentEncodedSeparator Reason = "percent_encoded_separator"
	// ReasonColon indicates a colon other than the drive-designating colon.
	ReasonColon Reason = "colon"
	// ReasonAlternateDataStream indicates an alternate data stream marker.
	ReasonAlternateDataStream Reason = "alternate_data_stream"
	// ReasonDriveNotAllowed indicates a drive-qualified path whose drive is
	// not in the caller's allowlist.
	ReasonDriveNotAllowed Reason = "drive_not_allowed"
	// ReasonDriveRelative indicates a drive-relative path such as C:foo.
	ReasonDriveRelative Reason = "drive_relative"
	// ReasonReservedDeviceName indicates a reserved Windows device name.
	ReasonReservedDeviceName Reason = "reserved_device_name"
	// ReasonResolveFailed indicates that the caller's resolver failed or
	// returned an unusable result.
	ReasonResolveFailed Reason = "resolve_failed"
	// ReasonResolveLoop indicates a resolution cycle or excessive resolution
	// depth.
	ReasonResolveLoop Reason = "resolve_loop"
)

// Error describes a path rejected by the admission policy.
type Error struct {
	Reason Reason
	Path   string
	Detail string
	Err    error
}

// Error implements error.
func (e *Error) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("netsec: path %q rejected (%s)", e.Path, e.Reason)
	}
	return fmt.Sprintf("netsec: path %q rejected (%s): %s", e.Path, e.Reason, e.Detail)
}

// Unwrap returns the underlying resolver error, when present.
func (e *Error) Unwrap() error {
	return e.Err
}

// Is makes all admission failures match ErrUnsafePath.
func (e *Error) Is(target error) bool {
	return target == ErrUnsafePath
}

// ResolveFunc resolves a path through any symlinks or reparse points. The
// callback is invoked only after the original path has passed all lexical
// checks. It should return the final local target, or the original path when
// there is nothing to resolve. An empty result is treated as "unchanged".
//
// The callback may perform filesystem operations. It must not call back into
// this package with an unvalidated result; the package validates every
// non-empty result it receives before returning success.
type ResolveFunc func(path string) (resolved string, err error)

// Policy is the caller-supplied admission policy.
type Policy struct {
	// AllowedDrives is a case-insensitive allowlist of drive letters for
	// drive-qualified paths. Entries may be "C", "C:", "C:\", or "C:/". An
	// empty or nil list rejects every drive-qualified path; it does not mean
	// "allow all". Paths without a drive designator are not governed by this
	// list.
	AllowedDrives []string

	// ResolvePath, when non-nil, is used to resolve symlinks and reparse
	// points. The result is checked against the same admission rules, so a
	// link pointing at a UNC or otherwise unsafe path is rejected. Nil means
	// that the caller accepts responsibility for resolving links separately.
	ResolvePath ResolveFunc
}

const maxResolveDepth = 8

// Check validates path against p before any filesystem operation is performed.
func (p Policy) Check(path string) error {
	return Check(path, p)
}

// Validate is an alias for Check.
func (p Policy) Validate(path string) error {
	return Check(path, p)
}

// CheckPath validates path against p. It is an alias for Check.
func CheckPath(path string, p Policy) error {
	return Check(path, p)
}

// ValidatePath validates path against p. It is an alias for Check.
func ValidatePath(path string, p Policy) error {
	return Check(path, p)
}

// Check performs a lexical admission check and, if configured, follows the
// caller-provided symlink/reparse-point resolver. No filesystem API is used
// by this package.
func Check(path string, p Policy) error {
	if err := checkLexical(path, p); err != nil {
		return err
	}
	if p.ResolvePath == nil {
		return nil
	}

	seen := map[string]struct{}{path: {}}
	current := path
	for depth := 0; depth < maxResolveDepth; depth++ {
		resolved, err := p.ResolvePath(current)
		if err != nil {
			return reject(path, ReasonResolveFailed, "resolver failed", err)
		}
		if resolved == "" || resolved == current {
			return nil
		}
		if _, exists := seen[resolved]; exists {
			return reject(path, ReasonResolveLoop, "resolution cycle", nil)
		}
		if err := checkLexical(resolved, p); err != nil {
			return err
		}
		seen[resolved] = struct{}{}
		current = resolved
	}
	return reject(path, ReasonResolveLoop, "resolution depth exceeded", nil)
}

func checkLexical(path string, p Policy) error {
	if path == "" {
		return reject(path, ReasonEmptyPath, "empty path", nil)
	}
	if strings.IndexByte(path, 0) >= 0 {
		return reject(path, ReasonNULByte, "NUL byte", nil)
	}

	// This must happen before any separator-folding. In particular, mixed
	// separator spellings such as \/evil and /\evil are just as dangerous as
	// \\evil and //evil.
	if err := checkLeadingSeparators(path); err != nil {
		return err
	}

	// A file URL with an authority/host is rejected. For a hostless file URL,
	// localPath is the local path that the URL denotes and is subjected to the
	// same rules as a directly supplied path.
	localPath := path
	if file, isFileURL, err := parseFileURL(path); err != nil {
		return err
	} else if isFileURL {
		localPath = file
		if localPath == "" {
			return reject(path, ReasonEmptyPath, "file URL has no path", nil)
		}
		if err := checkLeadingSeparators(localPath); err != nil {
			return err
		}
	}

	if err := checkMarkers(path); err != nil {
		return err
	}
	if localPath != path {
		if err := checkMarkers(localPath); err != nil {
			return err
		}
	}
	if err := checkColon(localPath); err != nil {
		return err
	}
	if err := checkDrive(localPath, p.AllowedDrives); err != nil {
		return err
	}
	if err := checkReservedNames(localPath); err != nil {
		return err
	}
	return nil
}

func checkLeadingSeparators(path string) error {
	// Leading whitespace is ignored for this check so a Windows service
	// cannot be tricked by "  //evil/share" when a later layer trims the
	// value. The rest of the checks remain deliberately literal.
	trimmed := strings.TrimLeftFunc(path, unicode.IsSpace)
	if len(trimmed) >= 2 && isSeparator(trimmed[0]) && isSeparator(trimmed[1]) {
		return reject(path, ReasonLeadingSeparators, "path starts with two separators", nil)
	}
	return nil
}

func checkMarkers(path string) error {
	lower := strings.ToLower(path)
	if strings.Contains(lower, ":$data") {
		return reject(path, ReasonAlternateDataStream, "contains :$DATA", nil)
	}
	if strings.Contains(lower, "%5c") {
		return reject(path, ReasonPercentEncodedSeparator, "contains literal %5c", nil)
	}
	if strings.Contains(lower, "@ssl") || strings.Contains(lower, "@443") {
		return reject(path, ReasonWebDAV, "contains a WebDAV @SSL or @443 marker", nil)
	}
	if strings.Contains(lower, "davwwwroot") {
		return reject(path, ReasonDavWWWRoot, "contains DavWWWRoot", nil)
	}
	if strings.Contains(lower, "globalroot") {
		return reject(path, ReasonGLOBALROOT, "contains GLOBALROOT", nil)
	}
	if hasDevicePrefix(path) {
		return reject(path, ReasonDevicePrefix, "uses an UNC device or device namespace prefix", nil)
	}
	return nil
}

// hasDevicePrefix checks a separator-folded view only after the leading
// double-separator check has already run. Folding here lets us recognize
// single-slash-spelled NT prefixes such as \??\ while ensuring that
// \\?\... has already been rejected by the leading-separator rule.
func hasDevicePrefix(path string) bool {
	p := strings.TrimLeftFunc(path, unicode.IsSpace)
	p = strings.ToLower(strings.ReplaceAll(p, "/", `\`))
	p = foldSeparators(p)

	// The UNC device (\.\) and Win32 device namespace (\?\) prefixes fold
	// to a single leading separator.
	if !strings.HasPrefix(p, `\`) {
		return false
	}
	rest := p[1:]
	for _, prefix := range []string{`?\`, `??\`, `.\`, `device\`, `dosdevices\`, `globalroot\`, `systemroot\`, `registry\`} {
		if strings.HasPrefix(rest, prefix) {
			return true
		}
	}
	switch rest {
	case "?", "??", ".", "device", "dosdevices", "globalroot", "systemroot", "registry":
		return true
	}
	return false
}

func foldSeparators(path string) string {
	var b strings.Builder
	b.Grow(len(path))
	lastSeparator := false
	for i := 0; i < len(path); i++ {
		if path[i] == '\\' {
			if lastSeparator {
				continue
			}
			lastSeparator = true
		} else {
			lastSeparator = false
		}
		b.WriteByte(path[i])
	}
	return b.String()
}

func parseFileURL(path string) (localPath string, isFileURL bool, err error) {
	if len(path) < len("file:") || !strings.EqualFold(path[:len("file:")], "file:") {
		return "", false, nil
	}

	rest := path[len("file:"):]
	localPath = rest
	if strings.HasPrefix(rest, "//") {
		authorityAndPath := rest[2:]
		// Only '/' terminates a URL authority. Treating a backslash as an
		// authority delimiter would turn file://\evil into an "empty host"
		// URL even though Windows may normalize it into a UNC path.
		separator := strings.IndexByte(authorityAndPath, '/')
		var authority string
		if separator < 0 {
			authority = authorityAndPath
			localPath = ""
		} else {
			authority = authorityAndPath[:separator]
			localPath = authorityAndPath[separator:]
		}
		if authority != "" {
			return "", true, reject(path, ReasonFileURLHost, "file URL has a host part", nil)
		}
	}

	// file:///C:/x and file:/C:/x both denote the local drive path C:/x on
	// Windows. Strip only the single leading separator that precedes the
	// drive designator; additional separators remain visible to the leading
	// separator check.
	if len(localPath) >= 3 && isSeparator(localPath[0]) &&
		isASCIILetter(localPath[1]) && localPath[2] == ':' {
		localPath = localPath[1:]
	}
	return localPath, true, nil
}

func checkColon(path string) error {
	for i := 0; i < len(path); i++ {
		if path[i] != ':' {
			continue
		}
		if i == 1 && isASCIILetter(path[0]) {
			// The drive-designating colon is handled by checkDrive. Any
			// subsequent colon is an ADS or an ambiguous path component.
			continue
		}
		return reject(path, ReasonColon, "colon outside the drive designator", nil)
	}
	return nil
}

func checkDrive(path string, allowedDrives []string) error {
	if len(path) < 2 || path[1] != ':' {
		return nil
	}
	if !isASCIILetter(path[0]) {
		return reject(path, ReasonColon, "malformed drive designator", nil)
	}
	if len(path) == 2 || !isSeparator(path[2]) {
		return reject(path, ReasonDriveRelative, "drive-relative path", nil)
	}

	drive := upperASCII(path[0])
	allowed := makeDriveSet(allowedDrives)
	if _, ok := allowed[drive]; !ok {
		return reject(path, ReasonDriveNotAllowed, fmt.Sprintf("drive %c: is not allowed", drive), nil)
	}
	return nil
}

func makeDriveSet(drives []string) map[byte]struct{} {
	set := make(map[byte]struct{}, len(drives))
	for _, drive := range drives {
		drive = strings.TrimSpace(drive)
		if len(drive) == 0 || !isASCIILetter(drive[0]) {
			continue
		}
		rest := drive[1:]
		if rest != "" {
			if rest[0] != ':' {
				continue
			}
			rest = rest[1:]
			if rest != "" && strings.Trim(rest, `/\`) != "" {
				continue
			}
		}
		set[upperASCII(drive[0])] = struct{}{}
	}
	return set
}

func checkReservedNames(path string) error {
	for _, segment := range strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if isReservedDeviceName(segment) {
			return reject(path, ReasonReservedDeviceName, fmt.Sprintf("reserved device name %q", segment), nil)
		}
	}
	return nil
}

func isReservedDeviceName(segment string) bool {
	// Windows resolves reserved devices even when the name has an extension
	// and even when trailing dots or spaces precede the extension.
	trimmed := strings.TrimRightFunc(segment, func(r rune) bool {
		return r == '.' || unicode.IsSpace(r)
	})
	if trimmed == "" {
		return false
	}
	if dot := strings.IndexByte(trimmed, '.'); dot >= 0 {
		trimmed = trimmed[:dot]
	}
	trimmed = strings.TrimRightFunc(trimmed, unicode.IsSpace)
	if trimmed == "" {
		return false
	}

	name := strings.ToUpper(trimmed)
	switch name {
	case "CON", "PRN", "AUX", "NUL", "CLOCK$", "CONIN$", "CONOUT$":
		return true
	}
	if len(name) == 4 && (strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT")) {
		return name[3] >= '1' && name[3] <= '9'
	}
	return false
}

func isSeparator(b byte) bool {
	return b == '/' || b == '\\'
}

func isASCIILetter(b byte) bool {
	return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}

func upperASCII(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - ('a' - 'A')
	}
	return b
}

func reject(path string, reason Reason, detail string, err error) error {
	return &Error{Reason: reason, Path: path, Detail: detail, Err: err}
}
