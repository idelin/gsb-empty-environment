// Package knownhosts implements an OpenSSH style known_hosts matcher that can
// be plugged into an SSH client as a host-key verification callback.
//
// It supports:
//   - plain host names and [host]:port patterns, including IPv6 literals;
//   - glob patterns ("*", "?") and negated patterns prefixed with "!";
//   - hashed host names (the "|1|<salt>|<hash>" form used by OpenSSH);
//   - the @cert-authority and @revoked markers;
//   - host certificates, whose signing CA must match a cert-authority line.
//
// A @revoked record always wins: when a presented key (or the CA that signed
// a presented certificate) matches a revoked line, verification fails even if
// another line would accept it. When a host has entries but none of them
// matches the presented key, a KeyError is reported instead of silently
// accepting the key.
package knownhosts

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// PublicKey is the minimal view of an SSH public key required for
// verification. The ssh.PublicKey type from golang.org/x/crypto/ssh satisfies
// this interface.
type PublicKey interface {
	// Type returns the SSH key type, for example "ssh-ed25519".
	Type() string
	// Marshal returns the SSH wire encoding of the public key, exactly the
	// blob stored in a known_hosts file.
	Marshal() []byte
}

// rawKey is the straightforward PublicKey implementation used for keys parsed
// out of known_hosts files.
type rawKey struct {
	keyType string
	blob    []byte
}

func (k rawKey) Type() string    { return k.keyType }
func (k rawKey) Marshal() []byte { return k.blob }

// KnownKey is a public key together with the line number it came from.
type KnownKey struct {
	KeyType string
	Key     PublicKey
	Line    int
}

// HostKeyCallback verifies the key presented for hostname:port. It is safe for
// concurrent use once the KnownHosts value has been parsed.
type HostKeyCallback func(hostname string, port int, key PublicKey) error

// KnownHosts holds the parsed contents of one or more known_hosts files.
type KnownHosts struct {
	lines []hostLine
}

// New parses the known_hosts file at path.
func New(path string) (*KnownHosts, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return NewFromReader(f)
}

// Callback returns a function value suitable for wiring into an SSH client.
// With golang.org/x/crypto/ssh it is adapted like:
//
//	cfg.HostKeyCallback = func(host string, addr net.Addr, key ssh.PublicKey) error {
//		_, port, _ := net.SplitHostPort(addr.String())
//		n, _ := strconv.Atoi(port)
//		return kh.HostKeyCallback(host, n, key)
//	}
func (k *KnownHosts) Callback() HostKeyCallback {
	return k.HostKeyCallback
}

// RevokedError is returned when a matching @revoked entry is found. Revocation
// takes priority over every other kind of match.
type RevokedError struct {
	// Line is the 1-based known_hosts line number, zero if unknown.
	Line int
	// KeyType is the revoked key type, or "*" for a wildcard revocation.
	KeyType string
}

func (e *RevokedError) Error() string {
	return fmt.Sprintf("knownhosts: revoked %s host key (line %d)", e.KeyType, e.Line)
}

// KeyError is returned when the presented key does not match the entries
// recorded for the host.
type KeyError struct {
	// Reason gives a human readable explanation of the failed decision.
	Reason string
	// Want lists the keys recorded for the matched host, when any exist.
	Want []KnownKey
	// Got is the key presented by the server.
	Got KnownKey
}

func (e *KeyError) Error() string {
	if e.Reason != "" {
		return "knownhosts: " + e.Reason
	}
	return "knownhosts: host key verification failed"
}

// ParseError reports a malformed known_hosts line.
type ParseError struct {
	// Line is the 1-based line number within the source.
	Line int
	// Err is the underlying parse error.
	Err error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("knownhosts: malformed line %d: %v", e.Line, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

var (
	errMalformed    = errors.New("malformed known_hosts entry")
	errUnsupported  = errors.New("unsupported certificate key type")
	errShortBuffer  = errors.New("truncated SSH key blob")
	errBadSignature = errors.New("certificate signature does not verify")
	errBadPrincipal = errors.New("certificate principal does not match host")
)

// HostKeyCallback implements the verification decision.
func (k *KnownHosts) HostKeyCallback(hostname string, port int, pubKey PublicKey) error {
	host := normalizeHost(hostname)

	isCert := strings.Contains(pubKey.Type(), "-cert-")
	var certSigningKey []byte
	if isCert {
		if cert, err := parseCertificate(pubKey.Marshal()); err == nil {
			certSigningKey = cert.signatureKey
		}
	}

	var matched []hostLine
	for _, line := range k.lines {
		if line.matches(host, port) {
			matched = append(matched, line)
		}
	}
	if len(matched) == 0 {
		return &KeyError{
			Reason: fmt.Sprintf("no known host key for %s", canonicalAddr(host, port)),
			Got:    knownOf(pubKey, 0),
		}
	}

	// Revocation always wins, including revocation of the CA key that signed a
	// presented certificate.
	for _, line := range matched {
		if line.marker != markerRevoked {
			continue
		}
		if line.revokes(pubKey, isCert, certSigningKey) {
			return &RevokedError{Line: line.line, KeyType: line.key.keyType}
		}
	}

	if isCert {
		return k.verifyCertificate(host, matched, pubKey)
	}

	var regular []hostLine
	for _, line := range matched {
		if line.marker == markerNone {
			regular = append(regular, line)
		}
	}
	if len(regular) == 0 {
		return &KeyError{
			Reason: fmt.Sprintf("host %s only has certificate-authority or revoked entries", host),
			Got:    knownOf(pubKey, 0),
		}
	}

	for _, line := range regular {
		if line.key.keyType == pubKey.Type() && bytes.Equal(line.key.blob, pubKey.Marshal()) {
			return nil
		}
	}

	// The host is known, but it presented a different key: report the conflict
	// rather than accepting the new key silently.
	want := make([]KnownKey, 0, len(regular))
	for _, line := range regular {
		want = append(want, line.key.known(line.line))
	}
	return &KeyError{
		Reason: fmt.Sprintf("host key mismatch for %s", canonicalAddr(host, port)),
		Want:   want,
		Got:    knownOf(pubKey, 0),
	}
}

func (k *KnownHosts) verifyCertificate(host string, matched []hostLine, pubKey PublicKey) error {
	var caLines []hostLine
	for _, line := range matched {
		if line.marker == markerCertAuthority {
			caLines = append(caLines, line)
		}
	}
	if len(caLines) == 0 {
		return &KeyError{
			Reason: fmt.Sprintf("no cert-authority entry for host %s", host),
			Got:    knownOf(pubKey, 0),
		}
	}

	var lastErr error
	for _, line := range caLines {
		err := verifyCertificate(pubKey.Marshal(), line.key.blob, host)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return &KeyError{
		Reason: fmt.Sprintf("certificate rejected for host %s: %v", host, lastErr),
		Want:   []KnownKey{caLines[0].key.known(caLines[0].line)},
		Got:    knownOf(pubKey, 0),
	}
}

func knownOf(key PublicKey, line int) KnownKey {
	return KnownKey{KeyType: key.Type(), Key: rawKey{key.Type(), append([]byte(nil), key.Marshal()...)}, Line: line}
}

// normalizeHost lower-cases the host name and canonicalizes IP literals.
func normalizeHost(hostname string) string {
	host := strings.ToLower(strings.TrimSpace(hostname))
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return host
}

// canonicalAddr renders the address string hashed entries are keyed on. It
// mirrors OpenSSH, which hashes "[host]:port" for IPv6 literals.
func canonicalAddr(host string, port int) string {
	if strings.Contains(host, ":") {
		return fmt.Sprintf("[%s]:%d", host, port)
	}
	return fmt.Sprintf("%s:%d", host, port)
}

// hashHost computes the OpenSSH hashed-host value: HMAC-SHA1 keyed with salt
// over the canonical "[host]:port" string.
func hashHost(salt []byte, canonical string) []byte {
	mac := hmac.New(sha1.New, salt)
	mac.Write([]byte(canonical))
	return mac.Sum(nil)
}

// HashHostname returns a hashed known_hosts host field for hostname and port,
// using a freshly generated random salt.
func HashHostname(hostname string, port int) (string, error) {
	salt := make([]byte, sha1.Size)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	host := normalizeHost(hostname)
	digest := hashHost(salt, canonicalAddr(host, port))
	return "|1|" + base64.RawStdEncoding.EncodeToString(salt) + "|" +
		base64.RawStdEncoding.EncodeToString(digest), nil
}

// globMatch reports whether s matches pattern using OpenSSH glob semantics:
// "*" matches any run of characters (including dots), "?" matches one
// character and matching is case-insensitive.
func globMatch(pattern, s string) bool {
	pattern = strings.ToLower(pattern)
	s = strings.ToLower(s)
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			if len(pattern) == 1 {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if globMatch(pattern[1:], s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
			s = s[1:]
		default:
			if len(s) == 0 || s[0] != pattern[0] {
				return false
			}
			s = s[1:]
		}
		pattern = pattern[1:]
	}
	return len(s) == 0
}

func b64decode(value string) ([]byte, error) {
	if strings.HasSuffix(value, "=") {
		return base64.StdEncoding.DecodeString(value)
	}
	return base64.RawStdEncoding.DecodeString(value)
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func atoiOrZero(value string) int {
	n, _ := strconv.Atoi(value)
	return n
}
