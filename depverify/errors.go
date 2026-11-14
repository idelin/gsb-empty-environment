package depverify

import "strings"

// Sentinel errors returned by the verifier. Concrete failures wrap these so
// callers can use errors.Is while still reading the detailed message.
var (
	// ErrChecksumMissing is returned when go.sum has no hash for a module
	// (or its go.mod) that must be verified.
	ErrChecksumMissing = verificationError("missing go.sum entry")

	// ErrChecksumMismatch is returned when the fetched content digest does
	// not equal the hash recorded in go.sum.
	ErrChecksumMismatch = verificationError("go.sum checksum mismatch")

	// ErrWildcardReplace is returned for a path-only replace, which is
	// rejected by default because it can silently retarget every version.
	ErrWildcardReplace = verificationError("wildcard replace is not allowed")

	// ErrCompetingReplace is returned when more than one effective replace
	// directive targets the same module (path or path@version).
	ErrCompetingReplace = verificationError("competing replace directives")

	// ErrExcluded is returned when a required or replacement module version
	// is listed in an exclude directive.
	ErrExcluded = verificationError("module version is excluded")

	// ErrProtocol is returned when a source URL, or any redirect hop, uses a
	// protocol that is not on the allow list.
	ErrProtocol = verificationError("disallowed source protocol")

	// ErrLocalNotRegistered is returned for a local directory replacement
	// that has no matching path+version entry in Options.LocalReplacements.
	ErrLocalNotRegistered = verificationError("unregistered local replacement")

	// ErrDuplicateRequire is returned when the same module path is required
	// at two different versions.
	ErrDuplicateRequire = verificationError("duplicate require")

	// ErrInvalidSource is returned when a source descriptor is malformed or
	// internally inconsistent.
	ErrInvalidSource = verificationError("invalid module source")
)

// verificationError is a constant-like error type supporting errors.Is.
type verificationError string

func (e verificationError) Error() string { return string(e) }

// ModuleError binds a verification failure to the effective module it concerns.
type ModuleError struct {
	Path    string
	Version string
	Err     error
}

func (e *ModuleError) Error() string {
	if e.Version == "" {
		return e.Path + ": " + e.Err.Error()
	}
	return e.Path + "@" + e.Version + ": " + e.Err.Error()
}

func (e *ModuleError) Unwrap() error { return e.Err }

// MultiError collects every verification failure in one run so the build
// service sees the full set instead of stopping at the first problem.
type MultiError struct {
	Errors []error
}

func (m *MultiError) Error() string {
	if len(m.Errors) == 1 {
		return m.Errors[0].Error()
	}
	var b strings.Builder
	b.WriteString("depverify: ")
	b.WriteString(itoa(len(m.Errors)))
	b.WriteString(" verification errors:\n")
	for _, err := range m.Errors {
		b.WriteString("\t- ")
		b.WriteString(err.Error())
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *MultiError) Unwrap() []error { return m.Errors }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
