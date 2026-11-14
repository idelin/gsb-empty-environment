package netsec

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	jobURIScheme   = "job"
	maxURIValueLen = 2048
	maxComponent   = 128
	maxDocumentLen = 200
)

// Scope identifies the isolated storage namespace of exactly one print job.
// It is derived from authenticated submission context and server-side job
// identifiers. None of these fields may be taken from an untrusted print
// request.
type Scope struct {
	Queue     string
	Submitter string
	JobID     string
}

// Validate checks that every scope component is a safe, single path segment.
func (s Scope) Validate() error {
	if err := validateToken("queue", s.Queue); err != nil {
		return err
	}
	if err := validateToken("submitter", s.Submitter); err != nil {
		return err
	}
	if err := validateToken("job-id", s.JobID); err != nil {
		return err
	}
	return nil
}

// CanonicalJobURI returns the only URI accepted for a document in the given
// job scope. The queue, submitter and job ID become path segments, and the
// document name becomes the final segment. Every segment is escaped and then
// validated, so a URI cannot cross into another user's or queue's storage.
func CanonicalJobURI(scope Scope, documentName string) (string, error) {
	if err := scope.Validate(); err != nil {
		return "", err
	}
	if err := validateDocumentName(documentName); err != nil {
		return "", err
	}

	parts := []string{
		url.PathEscape(scope.Queue),
		url.PathEscape(scope.Submitter),
		url.PathEscape(scope.JobID),
		url.PathEscape(documentName),
	}
	uri := jobURIScheme + ":///" + strings.Join(parts, "/")
	if len(uri) > maxURIValueLen {
		return "", fmt.Errorf("%w: URI is too long", ErrInvalidURI)
	}
	return uri, nil
}

// ParseJobURI parses a canonical job:/// URI into its storage scope and
// document name. Alternate spellings, escaped path separators, userinfo,
// queries, fragments, trailing slashes and non-canonical percent encodings are
// rejected. Requiring one canonical representation avoids ambiguity when a
// URI is used as an authorization or storage key.
func ParseJobURI(raw string) (Scope, string, error) {
	var empty Scope
	if len(raw) == 0 || len(raw) > maxURIValueLen {
		return empty, "", fmt.Errorf("%w: bad length", ErrInvalidURI)
	}
	if !utf8.ValidString(raw) || containsControl(raw) {
		return empty, "", fmt.Errorf("%w: control or invalid UTF-8", ErrInvalidURI)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return empty, "", fmt.Errorf("%w: %v", ErrInvalidURI, err)
	}
	if u.Scheme != jobURIScheme || u.Opaque != "" || u.Host != "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		u.ForceQuery || u.RawFragment != "" {
		return empty, "", fmt.Errorf("%w: wrong scheme or authority", ErrInvalidURI)
	}

	escaped := strings.TrimPrefix(u.EscapedPath(), "/")
	if escaped == "" {
		return empty, "", fmt.Errorf("%w: empty path", ErrInvalidURI)
	}
	parts := strings.Split(escaped, "/")
	if len(parts) != 4 {
		return empty, "", fmt.Errorf("%w: want four path segments", ErrInvalidURI)
	}

	decoded := make([]string, len(parts))
	for i, part := range parts {
		if part == "" {
			return empty, "", fmt.Errorf("%w: empty path segment", ErrInvalidURI)
		}
		value, err := url.PathUnescape(part)
		if err != nil {
			return empty, "", fmt.Errorf("%w: bad escape", ErrInvalidURI)
		}
		// A decoded separator or backslash would let a segment escape its
		// intended path component.
		if strings.ContainsAny(value, `/\`) {
			return empty, "", fmt.Errorf("%w: escaped path separator", ErrInvalidURI)
		}
		decoded[i] = value
	}

	scope := Scope{Queue: decoded[0], Submitter: decoded[1], JobID: decoded[2]}
	documentName := decoded[3]
	if err := scope.Validate(); err != nil {
		return empty, "", err
	}
	if err := validateDocumentName(documentName); err != nil {
		return empty, "", err
	}

	canonical, err := CanonicalJobURI(scope, documentName)
	if err != nil {
		return empty, "", err
	}
	if canonical != raw {
		return empty, "", fmt.Errorf("%w: non-canonical URI", ErrInvalidURI)
	}
	return scope, documentName, nil
}

// ValidateJobURI confirms that raw is exactly the canonical URI for the given
// job's storage scope and document. It is the explicit cross-user/cross-queue
// check used when a URI is supplied by a caller.
func ValidateJobURI(raw string, scope Scope, documentName string) error {
	gotScope, gotDocument, err := ParseJobURI(raw)
	if err != nil {
		return err
	}
	if gotScope != scope || gotDocument != documentName {
		return fmt.Errorf("%w: URI scope does not match the job", ErrURIOutsideJobStorage)
	}
	want, err := CanonicalJobURI(scope, documentName)
	if err != nil {
		return err
	}
	if raw != want {
		return fmt.Errorf("%w: expected %q", ErrURIOutsideJobStorage, want)
	}
	return nil
}

func validateToken(field, value string) error {
	if err := validateSafeValue(field, value, maxComponent, false); err != nil {
		return err
	}
	first, _ := utf8.DecodeRuneInString(value)
	if !unicode.IsLetter(first) && !unicode.IsDigit(first) {
		return fmt.Errorf("%w: %s must start with a letter or digit", ErrInvalidAttribute, field)
	}
	if strings.HasSuffix(value, ".") {
		return fmt.Errorf("%w: %s must not end with a dot", ErrInvalidAttribute, field)
	}
	return nil
}

func validateDocumentName(value string) error {
	if err := validateSafeValue("document-name", value, maxDocumentLen, true); err != nil {
		return err
	}
	first, _ := utf8.DecodeRuneInString(value)
	if !unicode.IsLetter(first) && !unicode.IsDigit(first) {
		return fmt.Errorf("%w: document name must start with a letter or digit", ErrInvalidAttribute)
	}
	if strings.HasSuffix(value, ".") || strings.HasSuffix(value, " ") {
		return fmt.Errorf("%w: document name has an unsafe suffix", ErrInvalidAttribute)
	}
	if value == "." || value == ".." {
		return fmt.Errorf("%w: document name is a path component", ErrInvalidAttribute)
	}
	return nil
}

func validateSafeValue(field, value string, max int, allowSpace bool) error {
	if value == "" {
		return fmt.Errorf("%w: %s is empty", ErrInvalidAttribute, field)
	}
	if len(value) > max {
		return fmt.Errorf("%w: %s is too long", ErrInvalidAttribute, field)
	}
	if !utf8.ValidString(value) || containsControl(value) {
		return fmt.Errorf("%w: %s contains control or invalid UTF-8", ErrInvalidAttribute, field)
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		switch r {
		case '.', '_', '-', '+', '@':
			continue
		case ' ':
			if allowSpace {
				continue
			}
		}
		return fmt.Errorf("%w: %s contains an unsafe character", ErrInvalidAttribute, field)
	}
	return nil
}

func containsControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) ||
			r == utf8.RuneError || r == '\u2028' || r == '\u2029' {
			return true
		}
	}
	return false
}
