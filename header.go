package netsec

import (
	"bytes"
	"fmt"
	"strings"
)

const tlsRequiredField = "tls-required"

// ValidateTLSRequirement checks the message's TLS-Required header against the
// envelope's REQUIRETLS parameter. Absence of the header is valid in both
// cases: it means "No" by default, while a sending MTA adds Require for an
// envelope that used REQUIRETLS.
//
// TLS-Required: No may only accompany an envelope without REQUIRETLS.
// TLS-Required: Require may only accompany an envelope with REQUIRETLS.
func ValidateTLSRequirement(data []byte, requireTLS bool) error {
	_, _, values, err := inspectTLSRequired(data)
	if err != nil {
		return err
	}
	if len(values) > 1 {
		return fmt.Errorf("%w: duplicate field", ErrMalformedTLSRequiredHeader)
	}
	if len(values) == 0 {
		return nil
	}

	switch strings.ToLower(strings.TrimSpace(values[0])) {
	case "require":
		if !requireTLS {
			return ErrTLSRequiredWithoutParameter
		}
	case "no":
		if requireTLS {
			return ErrTLSRequiredHeaderConflict
		}
	default:
		return fmt.Errorf("%w: unknown value %q", ErrMalformedTLSRequiredHeader, values[0])
	}
	return nil
}

// PrepareMessage validates the TLS-Required header and, when the envelope
// uses REQUIRETLS, adds TLS-Required: Require as required by RFC 8689. The
// returned slice is a copy when a header is inserted; callers must send the
// returned data rather than the original.
func PrepareMessage(data []byte, requireTLS bool) ([]byte, error) {
	if err := ValidateTLSRequirement(data, requireTLS); err != nil {
		return nil, err
	}
	if !requireTLS {
		return data, nil
	}

	headerEnd, sep, values, err := inspectTLSRequired(data)
	if err != nil {
		return nil, err
	}
	if len(values) == 1 {
		return data, nil
	}

	lineSep := "\r\n"
	if sep == "\n\n" {
		lineSep = "\n"
	}
	// headerEnd points at the first CRLF/LF of the header/body separator,
	// so the separator itself supplies the line ending after the new field.
	line := []byte(lineSep + "TLS-Required: Require")

	out := make([]byte, 0, len(data)+len(line))
	out = append(out, data[:headerEnd]...)
	out = append(out, line...)
	out = append(out, data[headerEnd:]...)
	return out, nil
}

// inspectTLSRequired returns the header/body boundary, the separator at that
// boundary, and every unfolded TLS-Required value. It deliberately does not
// use net/mail so the exact original message bytes can be preserved.
func inspectTLSRequired(data []byte) (int, string, []string, error) {
	headerEnd := -1
	sep := ""
	if i := bytes.Index(data, []byte("\r\n\r\n")); i >= 0 {
		headerEnd, sep = i, "\r\n\r\n"
	} else if i := bytes.Index(data, []byte("\n\n")); i >= 0 {
		headerEnd, sep = i, "\n\n"
	} else {
		return 0, "", nil, fmt.Errorf("%w: missing header/body separator", ErrMalformedTLSRequiredHeader)
	}

	header := strings.ReplaceAll(string(data[:headerEnd]), "\r\n", "\n")
	lines := strings.Split(header, "\n")

	var values []string
	var current string
	collecting := false
	inField := false
	flush := func() {
		if collecting {
			values = append(values, strings.TrimSpace(current))
			collecting = false
		}
	}

	for _, line := range lines {
		if line == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if !inField {
				return 0, "", nil, fmt.Errorf("%w: orphan continuation line", ErrMalformedTLSRequiredHeader)
			}
			if collecting {
				current += " " + strings.TrimSpace(line)
			}
			continue
		}
		flush()
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			return 0, "", nil, fmt.Errorf("%w: malformed header line", ErrMalformedTLSRequiredHeader)
		}
		inField = true
		name := strings.ToLower(strings.TrimSpace(line[:colon]))
		if name == tlsRequiredField {
			current = line[colon+1:]
			collecting = true
		}
	}
	flush()
	return headerEnd, sep, values, nil
}
