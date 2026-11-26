// Package mailauth implements byte-level, dependency-free verification and
// anti-spoofing analysis of RFC 6376 DKIM signatures and RFC 8617 ARC
// authentication chains over raw RFC 5322 messages.
//
// The package never performs network access and never relies on third party
// DKIM libraries. Cryptographic primitives are limited to the Go standard
// library crypto/rsa, crypto/ed25519 and crypto/sha256 packages. Public keys
// are supplied by the embedding host through the KeyResolver interface.
package mailauth

import (
	"bytes"
	"strings"
)

// Header is one raw RFC 5322 header field. Raw includes the terminating CRLF
// and preserves any folding whitespace exactly as it appears on the wire after
// line-ending normalization, which is what DKIM/ARC canonicalization requires.
type Header struct {
	// Index is the zero based position of the field within the header block.
	Index int
	// Name is the raw field name (the bytes before the first colon).
	Name string
	// Value is the raw field value including folding whitespace, without the
	// terminating CRLF.
	Value string
	// Raw is the complete field, including its terminating CRLF.
	Raw string
}

// Message is a parsed RFC 5322 message split at the header/body separator
// without altering the semantic content of either section.
type Message struct {
	// Headers are the header fields in wire order.
	Headers []Header
	// Body is the exact body section bytes following the blank separator line,
	// normalized to CRLF line endings.
	Body []byte
	// FromCount is the number of From header field instances.
	FromCount int
	// DKIMSignatureCount is the number of DKIM-Signature field instances.
	DKIMSignatureCount int
}

// normalizeCRLF turns bare LF (and bare CR) line endings into canonical CRLF
// sequences so that every downstream algorithm can assume RFC 5322 wire form.
func normalizeCRLF(b []byte) []byte {
	if !bytes.ContainsAny(b, "\r\n") {
		return b
	}
	var out bytes.Buffer
	out.Grow(len(b) + len(b)/16)
	for i := 0; i < len(b); i++ {
		switch b[i] {
		case '\r':
			if i+1 < len(b) && b[i+1] == '\n' {
				out.WriteByte('\r')
				out.WriteByte('\n')
				i++
			} else {
				out.WriteByte('\r')
				out.WriteByte('\n')
			}
		case '\n':
			out.WriteByte('\r')
			out.WriteByte('\n')
		default:
			out.WriteByte(b[i])
		}
	}
	return out.Bytes()
}

// ParseMessage parses a raw RFC 5322 message. Inputs using bare LF or bare CR
// line endings are accepted and internally normalized to CRLF; this never
// changes the bytes that a correctly formed CRLF message would canonicalize to.
func ParseMessage(raw []byte) (*Message, error) {
	raw = normalizeCRLF(raw)

	sep := bytes.Index(raw, []byte("\r\n\r\n"))
	var headerBlock, body []byte
	if sep < 0 {
		headerBlock = raw
		// RFC 6376 treats a message without a body as an empty body.
		body = nil
	} else {
		headerBlock = raw[:sep+2]
		body = raw[sep+4:]
	}

	headers, err := parseHeaderBlock(headerBlock)
	if err != nil {
		return nil, err
	}

	m := &Message{Headers: headers, Body: body}
	for _, h := range headers {
		switch strings.ToLower(h.Name) {
		case "from":
			m.FromCount++
		case "dkim-signature":
			m.DKIMSignatureCount++
		}
	}
	return m, nil
}

// parseHeaderBlock splits the CRLF terminated header block into fields,
// honoring RFC 5322 folding (a CRLF followed by WSP continues a field).
func parseHeaderBlock(block []byte) ([]Header, error) {
	var headers []Header
	rest := block
	for len(rest) > 0 {
		// Find the end of this (possibly folded) field.
		end := len(rest)
		for pos := 0; pos < len(rest); {
			cr := bytes.IndexByte(rest[pos:], '\r')
			if cr < 0 {
				break
			}
			cr += pos
			next := cr + 2
			if next < len(rest) && (rest[next] == ' ' || rest[next] == '\t') {
				pos = next
				continue
			}
			end = next
			break
		}

		field := rest[:end]
		rest = rest[end:]

		// A field without a colon is malformed; skip it defensively but keep
		// parsing so that verification reports structured findings instead of
		// aborting the whole message.
		colon := bytes.IndexByte(field, ':')
		if colon < 0 {
			continue
		}
		name := string(field[:colon])
		var value string
		// The final field of a block without a terminating CRLF is still a
		// valid header for analysis; normalize it to wire form so downstream
		// canonicalization always sees a CRLF terminated field.
		if len(field) >= 2 && field[len(field)-2] == '\r' && field[len(field)-1] == '\n' {
			value = string(field[colon+1 : len(field)-2])
		} else {
			value = string(field[colon+1:])
			field = append(field, '\r', '\n')
		}
		headers = append(headers, Header{
			Index: len(headers),
			Name:  name,
			Value: value,
			Raw:   string(field),
		})
	}
	return headers, nil
}

// HeaderNamed reports whether the field name matches case insensitively.
func headerNameIs(name, want string) bool {
	return strings.EqualFold(name, want)
}

// scopedHeaders returns the ordered header fields visible to an ARC
// verification at instance i. Per RFC 8617 section 5, an ARC-Message-Signature
// sees regular headers, earlier ARC sets (instance < i) and its own instance's
// AAR/AMS, but never the same instance's ARC-Seal, which is produced later.
// An ARC-Seal additionally sees the same-instance AAR and AMS. A maxInstance
// of 0 disables scoping and returns all headers, the DKIM verification view.
func (m *Message) scopedHeaders(maxInstance int, includeSameInstanceSeal bool) []Header {
	if maxInstance <= 0 {
		return m.Headers
	}
	out := make([]Header, 0, len(m.Headers))
	for _, h := range m.Headers {
		base := strings.ToLower(h.Name)
		inst, ok := arcInstance(base)
		if !ok {
			out = append(out, h)
			continue
		}
		isSeal := strings.HasPrefix(base, "arc-seal")
		if inst < maxInstance || (inst == maxInstance && (!isSeal || includeSameInstanceSeal)) {
			out = append(out, h)
		}
	}
	return out
}

// arcInstance parses the instance number out of an ARC-Authentication-Results,
// ARC-Message-Signature or ARC-Seal field name.
func arcInstance(lowerName string) (int, bool) {
	var prefix string
	switch {
	case strings.HasPrefix(lowerName, "arc-authentication-results"):
		prefix = "arc-authentication-results"
	case strings.HasPrefix(lowerName, "arc-message-signature"):
		prefix = "arc-message-signature"
	case strings.HasPrefix(lowerName, "arc-seal"):
		prefix = "arc-seal"
	default:
		return 0, false
	}
	rest := lowerName[len(prefix):]
	if rest == "" {
		return 0, false
	}
	inst := 0
	for _, ch := range rest {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		inst = inst*10 + int(ch-'0')
	}
	return inst, true
}
