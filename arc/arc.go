// Package arc validates an Authenticated Received Chain (ARC) as observed by
// a receiving MTA. ARC is defined in RFC 8617.
//
// A chained message carries, for every instance i = 1..N, exactly one of
// each ARC header field:
//
//   - ARC-Authentication-Results (AAR)
//   - ARC-Message-Signature (AMS)
//   - ARC-Seal (AS)
//
// The package performs structural and policy validation of the chain
// (instance continuity, per-instance uniqueness, on-the-wire ordering and the
// chain-value (cv) rules). All cryptographic work is delegated to verifier
// functions injected by the caller via [Verifiers], so the package itself has
// no key handling or DKIM dependencies.
package arc

import (
	"fmt"
	"strconv"
	"strings"
)

// Kind identifies one of the three ARC header field types.
type Kind int

const (
	// Unknown marks a header that could not be classified as an ARC field.
	Unknown Kind = iota
	// AAR is an ARC-Authentication-Results header.
	AAR
	// AMS is an ARC-Message-Signature header.
	AMS
	// AS is an ARC-Seal header.
	AS
)

// kindOrder is the mandatory relative on-the-wire order of the three headers
// within a single ARC instance set (RFC 8617, section 5.2).
var kindOrder = [...]Kind{AAR, AMS, AS}

var headerKinds = map[string]Kind{
	"arc-authentication-results": AAR,
	"arc-message-signature":      AMS,
	"arc-seal":                   AS,
}

// Header is one parsed ARC header field, in the order it appeared in the
// message.
type Header struct {
	// Kind is the classified ARC header type.
	Kind Kind
	// Instance is the value of the mandatory i= tag; always >= 1.
	Instance int
	// CV is the lower-cased value of the cv= tag, or "" when absent. It is
	// only policy-significant on ARC-Seal headers.
	CV string
	// Name is the original header field name, with surrounding whitespace
	// trimmed.
	Name string
	// Value is the raw field value (everything after the first colon), with
	// surrounding whitespace trimmed. Folded whitespace must be unfolded by
	// the caller before parsing.
	Value string
	// Raw is the original, unmodified header field line.
	Raw string
	// Index is the physical position of the header in the slice handed to
	// [Validate], starting at 0.
	Index int
}

// ParseHeader parses a single "Name: value" ARC header field line. It returns
// an error for non-ARC fields, lines without a colon, or fields missing a
// valid positive i= tag.
func ParseHeader(line string) (*Header, error) {
	name, value, found := strings.Cut(line, ":")
	if !found {
		return nil, fmt.Errorf("arc: malformed header %q: missing colon", line)
	}
	name = strings.TrimSpace(name)
	kind, ok := headerKinds[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("arc: %q is not an ARC header", name)
	}

	value = strings.TrimSpace(value)
	instance, err := parseInstance(value)
	if err != nil {
		return nil, fmt.Errorf("arc: %s header: %w", name, err)
	}

	return &Header{
		Kind:     kind,
		Instance: instance,
		CV:       strings.ToLower(tagValue(value, "cv")),
		Name:     name,
		Value:    value,
		Raw:      line,
	}, nil
}

// parseInstance extracts and validates the mandatory i= tag.
func parseInstance(value string) (int, error) {
	raw := tagValue(value, "i")
	if raw == "" {
		return 0, fmt.Errorf("missing required i= tag")
	}
	instance, err := strconv.Atoi(raw)
	if err != nil || instance < 1 {
		return 0, fmt.Errorf("invalid instance number %q", raw)
	}
	return instance, nil
}

// tagValue returns the trimmed value of the first occurrence of tag in a
// semicolon-separated DKIM/ARC tag list, or "" if the tag is absent.
func tagValue(value, tag string) string {
	for _, spec := range strings.Split(value, ";") {
		name, val, ok := strings.Cut(spec, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), tag) {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

// TagValue exposes the value of a tag in the header's tag-list, for example
// TagValue(h, "b") to read a signature. It returns "" when the tag is absent.
func (h *Header) TagValue(tag string) string {
	return tagValue(h.Value, tag)
}
