package netsec

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Canonical whitelisted job attribute names. Parsing is intentionally
// case-sensitive: names such as "Job-URI" or "job_uri" are unknown and are
// rejected rather than normalized into a privileged attribute.
const (
	AttrJobID          = "job-id"
	AttrQueue          = "queue"
	AttrSubmitter      = "submitter"
	AttrJobURI         = "job-uri"
	AttrDocumentURI    = "document-uri"
	AttrSHA256         = "sha256"
	AttrContentDigest  = "content-digest"
	AttrDocumentName   = "document-name"
	AttrCopies         = "copies"
	AttrMedia          = "media"
	AttrSides          = "sides"
	AttrTitle          = "title"
	maxAttributeKeyLen = 64
	attrValueMaxLength = 2048
)

var attributeWhitelist = map[string]struct{}{
	AttrJobID:         {},
	AttrQueue:         {},
	AttrSubmitter:     {},
	AttrJobURI:        {},
	AttrDocumentURI:   {},
	AttrSHA256:        {},
	AttrContentDigest: {},
	AttrDocumentName:  {},
	AttrCopies:        {},
	AttrMedia:         {},
	AttrSides:         {},
	AttrTitle:         {},
}

// Attributes is a validated, whitelisted set of print-job attributes. Its
// zero value is a valid empty set. The internal map is never exposed directly;
// callers receive copies, so a returned Job cannot be used to mutate the
// service's authorization binding.
type Attributes struct {
	values map[string]string
}

// ParseAttributes copies raw into a new Attributes value after applying an
// exact-name whitelist and type checks. Any unknown name, empty value, control
// character, unsafe identity token, malformed digest or conflicting alias
// causes the entire map to be rejected. Nothing is silently ignored.
func ParseAttributes(raw map[string]string) (Attributes, error) {
	values := make(map[string]string, len(raw))
	for key, value := range raw {
		if len(key) > maxAttributeKeyLen {
			return Attributes{}, fmt.Errorf("%w: attribute name is too long", ErrUnknownAttribute)
		}
		if _, ok := attributeWhitelist[key]; !ok {
			return Attributes{}, fmt.Errorf("%w: %q", ErrUnknownAttribute, key)
		}
		if err := validateAttributeValue(key, value); err != nil {
			return Attributes{}, err
		}
		values[key] = value
	}

	if uri, ok := values[AttrJobURI]; ok {
		if documentURI, ok := values[AttrDocumentURI]; ok && uri != documentURI {
			return Attributes{}, fmt.Errorf("%w: %s and %s differ", ErrDuplicateAttribute, AttrJobURI, AttrDocumentURI)
		}
	}
	if digest, ok := values[AttrSHA256]; ok {
		if contentDigest, ok := values[AttrContentDigest]; ok && digest != contentDigest {
			return Attributes{}, fmt.Errorf("%w: %s and %s differ", ErrDuplicateAttribute, AttrSHA256, AttrContentDigest)
		}
	}

	return Attributes{values: values}, nil
}

func validateAttributeValue(key, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s is empty", ErrInvalidAttribute, key)
	}
	if len(value) > attrValueMaxLength {
		return fmt.Errorf("%w: %s is too long", ErrInvalidAttribute, key)
	}

	switch key {
	case AttrJobID:
		return validateToken(key, value)
	case AttrQueue:
		return validateToken(key, value)
	case AttrSubmitter:
		return validateToken(key, value)
	case AttrJobURI, AttrDocumentURI:
		if _, _, err := ParseJobURI(value); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrInvalidAttribute, key, err)
		}
	case AttrSHA256, AttrContentDigest:
		if err := validateDigest(value); err != nil {
			return err
		}
	case AttrDocumentName:
		return validateDocumentName(value)
	case AttrCopies:
		return validateCopies(value)
	case AttrMedia:
		return validateToken(key, value)
	case AttrSides:
		switch value {
		case "one-sided", "two-sided-long-edge", "two-sided-short-edge":
			return nil
		default:
			return fmt.Errorf("%w: %s has an unsupported value", ErrInvalidAttribute, key)
		}
	case AttrTitle:
		return validateTitle(value)
	default:
		return fmt.Errorf("%w: %q", ErrUnknownAttribute, key)
	}
	return nil
}

func validateDigest(value string) error {
	if len(value) != 64 {
		return fmt.Errorf("%w: digest must be 64 lowercase hex characters", ErrInvalidAttribute)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("%w: digest is not hexadecimal", ErrInvalidAttribute)
	}
	if strings.ToLower(value) != value {
		return fmt.Errorf("%w: digest must be lowercase", ErrInvalidAttribute)
	}
	return nil
}

func validateCopies(value string) error {
	if !allASCIIDigits(value) {
		return fmt.Errorf("%w: copies must be a decimal integer", ErrInvalidAttribute)
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > 999 {
		return fmt.Errorf("%w: copies must be between 1 and 999", ErrInvalidAttribute)
	}
	if strconv.Itoa(n) != value {
		return fmt.Errorf("%w: copies is not canonical", ErrInvalidAttribute)
	}
	return nil
}

func validateTitle(value string) error {
	if !utf8ValidNoControl(value) {
		return fmt.Errorf("%w: title contains control or invalid UTF-8", ErrInvalidAttribute)
	}
	if len([]rune(value)) > 256 {
		return fmt.Errorf("%w: title is too long", ErrInvalidAttribute)
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: title is empty", ErrInvalidAttribute)
	}
	return nil
}

func allASCIIDigits(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func utf8ValidNoControl(value string) bool {
	return !containsControl(value)
}

// Get returns a whitelisted attribute value. The boolean is false when the
// attribute was not present.
func (a Attributes) Get(key string) (string, bool) {
	value, ok := a.values[key]
	return value, ok
}

// Has reports whether a whitelisted attribute is present.
func (a Attributes) Has(key string) bool {
	_, ok := a.values[key]
	return ok
}

// Len returns the number of attributes.
func (a Attributes) Len() int {
	return len(a.values)
}

// Values returns an independent copy of the attribute map.
func (a Attributes) Values() map[string]string {
	out := make(map[string]string, len(a.values))
	for key, value := range a.values {
		out[key] = value
	}
	return out
}

// Clone returns a deep copy.
func (a Attributes) Clone() Attributes {
	return Attributes{values: a.Values()}
}

// Merge returns a new set containing a followed by overrides. Both inputs are
// already validated; Merge does not re-parse them.
func (a Attributes) Merge(overrides Attributes) Attributes {
	merged := a.Values()
	for key, value := range overrides.values {
		merged[key] = value
	}
	return Attributes{values: merged}
}

func (a Attributes) equal(other Attributes) bool {
	if len(a.values) != len(other.values) {
		return false
	}
	for key, value := range a.values {
		if other.values[key] != value {
			return false
		}
	}
	return true
}
