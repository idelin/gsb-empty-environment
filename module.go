package netsec

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var goVersionPattern = regexp.MustCompile(
	`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z][0-9A-Za-z.-]*)?(\+[0-9A-Za-z][0-9A-Za-z.-]*)?$`,
)

// Request identifies the exact module version to resolve.
//
// ExpectedChecksum and ExpectedSignerKeyID are optional pins supplied by a
// trusted lockfile or dependency database. When non-empty they are enforced
// before the artifact is accepted.
type Request struct {
	ModulePath          string `json:"module_path"`
	Version             string `json:"version"`
	ExpectedChecksum    string `json:"expected_checksum,omitempty"`
	ExpectedSignerKeyID string `json:"expected_signer_key_id,omitempty"`
}

func (r Request) validate() error {
	if err := validateModulePath(r.ModulePath); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if !goVersionPattern.MatchString(r.Version) {
		return fmt.Errorf("%w: version %q is not a valid Go module version", ErrInvalidRequest, r.Version)
	}
	if r.ExpectedChecksum != "" {
		if _, err := checksumAlgorithm(r.ExpectedChecksum); err != nil {
			return fmt.Errorf("%w: expected checksum: %v", ErrInvalidRequest, err)
		}
	}
	if r.ExpectedSignerKeyID != "" {
		if err := validateIdentifier("signer key ID", r.ExpectedSignerKeyID); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
	}
	return nil
}

func validateModulePath(path string) error {
	if path == "" {
		return fmt.Errorf("module path is empty")
	}
	if strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return fmt.Errorf("module path %q must not start or end with '/'", path)
	}
	if strings.Contains(path, "@") {
		return fmt.Errorf("module path %q contains '@'", path)
	}
	for _, r := range path {
		if unicode.IsControl(r) || unicode.IsSpace(r) || r == '\\' {
			return fmt.Errorf("module path %q contains an invalid character", path)
		}
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("module path %q has an invalid path segment", path)
		}
	}
	return nil
}

func validateIdentifier(name, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s is empty", name)
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return fmt.Errorf("%s %q contains whitespace or a control character", name, value)
		}
	}
	return nil
}
