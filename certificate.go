package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// CertificateVersion is the only push certificate version supported by this
// package. Git emits this version for signed pushes.
const CertificateVersion = "0.1"

// Update is one ref update from a push certificate or from the pre-receive
// hook's standard input. Old is all zeroes when the ref is created, and New is
// all zeroes when the ref is deleted.
type Update struct {
	Old string
	New string
	Ref string
}

// Equal reports whether both updates contain the same old object, new object,
// and ref.
func (u Update) Equal(other Update) bool {
	return u.Old == other.Old && u.New == other.New && u.Ref == other.Ref
}

// String returns the update in the format used by a push certificate.
func (u Update) String() string {
	return u.Old + " " + u.New + " " + u.Ref
}

// Certificate is a parsed Git push certificate (version 0.1).
//
// Payload is the exact byte sequence covered by the signature: the version
// line, metadata lines, and update lines. Signature starts at the signature
// block marker (for example, "-----BEGIN PGP SIGNATURE-----").
type Certificate struct {
	Version   string
	Pusher    string
	Pushee    string
	Nonce     string
	Updates   []Update
	Payload   []byte
	Signature []byte
}

var signatureMarkers = []string{
	"-----BEGIN PGP SIGNATURE-----",
	"-----BEGIN SSH SIGNATURE-----",
}

// ParseCertificate parses a signed push certificate. It requires the version
// 0.1 header, the pusher, pushee, and nonce metadata, at least one update line
// with a fully qualified ref, and a detached signature block.
func ParseCertificate(certificate []byte) (*Certificate, error) {
	if len(bytes.TrimSpace(certificate)) == 0 {
		return nil, fmt.Errorf("%w: empty certificate", ErrEmptyCertificate)
	}

	raw := append([]byte(nil), certificate...)
	payload, signature, err := splitSignature(raw)
	if err != nil {
		return nil, err
	}

	lines := splitLines(payload)
	if len(lines) == 0 {
		return nil, malformed("empty signed payload")
	}
	if lines[0] != "certificate version "+CertificateVersion {
		if strings.HasPrefix(lines[0], "certificate version ") {
			return nil, fmt.Errorf("%w: %q", ErrUnsupportedVersion,
				strings.TrimPrefix(lines[0], "certificate version "))
		}
		return nil, malformed("missing certificate version 0.1 header")
	}

	cert := &Certificate{
		Version:   CertificateVersion,
		Payload:   payload,
		Signature: signature,
	}
	seen := make(map[string]bool, 3)
	inUpdates := false

	for i, line := range lines[1:] {
		lineNumber := i + 2
		if line == "" {
			continue
		}

		if !inUpdates {
			key, value, isHeader, err := parseHeaderLine(line)
			if err != nil {
				return nil, err
			}
			if isHeader {
				if seen[key] {
					return nil, malformed("duplicate %s header on line %d", key, lineNumber)
				}
				seen[key] = true
				switch key {
				case "pusher":
					cert.Pusher = value
				case "pushee":
					cert.Pushee = value
				case "nonce":
					cert.Nonce = value
				}
				continue
			}
			inUpdates = true
		}

		update, ok := parseUpdateLine(line)
		if !ok {
			return nil, malformed("invalid update line %d: %q", lineNumber, line)
		}
		cert.Updates = append(cert.Updates, update)
	}

	if !seen["pusher"] {
		return nil, fmt.Errorf("%w: certificate has no pusher", ErrMissingPusher)
	}
	if !seen["pushee"] {
		return nil, fmt.Errorf("%w: certificate has no pushee", ErrMissingPushee)
	}
	if !seen["nonce"] {
		return nil, fmt.Errorf("%w: certificate has no nonce", ErrMissingNonce)
	}
	if len(cert.Updates) == 0 {
		return nil, malformed("certificate has no update lines")
	}

	return cert, nil
}

func splitSignature(certificate []byte) ([]byte, []byte, error) {
	offset := -1
	for _, marker := range signatureMarkers {
		for searchFrom := 0; searchFrom < len(certificate); {
			found := bytes.Index(certificate[searchFrom:], []byte(marker))
			if found < 0 {
				break
			}
			index := searchFrom + found
			if index == 0 || certificate[index-1] == '\n' {
				if offset == -1 || index < offset {
					offset = index
				}
				break
			}
			searchFrom = index + 1
		}
	}
	if offset < 0 {
		return nil, nil, fmt.Errorf("%w: no signature block", ErrMissingSignature)
	}
	if offset == 0 {
		return nil, nil, malformed("signature block precedes signed payload")
	}
	return certificate[:offset], certificate[offset:], nil
}

func splitLines(payload []byte) []string {
	text := string(payload)
	if strings.HasSuffix(text, "\n") {
		text = text[:len(text)-1]
	}
	if text == "" {
		return nil
	}

	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

func parseHeaderLine(line string) (key, value string, isHeader bool, err error) {
	for _, candidate := range []string{"pusher", "pushee", "nonce"} {
		if line != candidate && !strings.HasPrefix(line, candidate+" ") {
			continue
		}
		if line == candidate {
			return "", "", true, malformed("empty %s header", candidate)
		}
		value = strings.TrimSpace(line[len(candidate)+1:])
		if value == "" {
			return "", "", true, malformed("empty %s header", candidate)
		}
		return candidate, value, true, nil
	}
	return "", "", false, nil
}

func parseUpdateLine(line string) (Update, bool) {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return Update{}, false
	}
	if !strings.HasPrefix(fields[2], "refs/") {
		return Update{}, false
	}
	return Update{Old: fields[0], New: fields[1], Ref: fields[2]}, true
}

func malformed(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrMalformedCertificate, fmt.Sprintf(format, args...))
}

// ErrEmptyCertificate indicates that no certificate was supplied.
var ErrEmptyCertificate = errors.New("netsec: empty push certificate")

// ErrMalformedCertificate indicates a structurally invalid certificate.
var ErrMalformedCertificate = errors.New("netsec: malformed push certificate")

// ErrUnsupportedVersion indicates a certificate version other than 0.1.
var ErrUnsupportedVersion = errors.New("netsec: unsupported push certificate version")

// ErrMissingSignature indicates that the certificate has no signature block.
var ErrMissingSignature = errors.New("netsec: push certificate has no signature block")

// ErrMissingPusher indicates that the pusher field is absent.
var ErrMissingPusher = errors.New("netsec: push certificate has no pusher")

// ErrMissingPushee indicates that the pushee field is absent.
var ErrMissingPushee = errors.New("netsec: push certificate has no pushee")

// ErrMissingNonce indicates that the nonce field is absent.
var ErrMissingNonce = errors.New("netsec: push certificate has no nonce")
