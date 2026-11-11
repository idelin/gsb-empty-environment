package knownhosts

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

const (
	markerNone          = ""
	markerCertAuthority = "cert-authority"
	markerRevoked       = "revoked"
)

// pattern is a single entry of the comma-separated host list.
type pattern struct {
	negated bool
	hashed  bool
	host    string // plain host glob, or raw hash token for hashed entries
	port    int    // bracketed [host]:port patterns; -1 means any port
	hasPort bool
	salt    []byte // hashed entries only
	digest  []byte // hashed entries only
}

// hostLine is one parsed known_hosts record.
type hostLine struct {
	patterns []pattern
	marker   string
	key      rawKey
	comment  string
	line     int
}

// known adapts the line's key to the exported KnownKey view.
func (l hostLine) knownKey() KnownKey {
	return l.key.known(l.line)
}

func (k rawKey) known(line int) KnownKey {
	return KnownKey{KeyType: k.keyType, Key: rawKey{k.keyType, append([]byte(nil), k.blob...)}, Line: line}
}

// NewFromReader parses known_hosts content. Blank lines and comments are
// skipped; the first malformed line aborts parsing with a *ParseError.
func NewFromReader(r io.Reader) (*KnownHosts, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: nil reader", errMalformed)
	}
	kh := &KnownHosts{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for lineNo := 0; scanner.Scan(); {
		lineNo++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		entry, err := parseLine(raw)
		if err != nil {
			return nil, &ParseError{Line: lineNo, Err: err}
		}
		entry.line = lineNo
		kh.lines = append(kh.lines, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return kh, nil
}

// Lines returns every parsed record as a KnownKey.
func (k *KnownHosts) Lines() []KnownKey {
	out := make([]KnownKey, 0, len(k.lines))
	for _, line := range k.lines {
		out = append(out, line.knownKey())
	}
	return out
}

// Line parses a single known_hosts line. It is exported so callers can parse
// individual records independently of a file.
func Line(text string) (KnownKey, error) {
	entry, err := parseLine(strings.TrimSpace(text))
	if err != nil {
		return KnownKey{}, err
	}
	return entry.knownKey(), nil
}

func parseLine(line string) (hostLine, error) {
	var entry hostLine

	fields := strings.Fields(line)
	if len(fields) < 2 {
		return entry, fmt.Errorf("%w: expected at least host list and key", errMalformed)
	}

	idx := 0
	if len(fields[idx]) > 0 && fields[idx][0] == '@' {
		marker := fields[idx][1:]
		switch marker {
		case markerCertAuthority, markerRevoked:
			entry.marker = marker
		default:
			// Unknown markers are tolerated, matching OpenSSH behavior.
		}
		idx++
		if len(fields)-idx < 2 {
			return entry, fmt.Errorf("%w: marker without key fields", errMalformed)
		}
	}

	patterns, err := parsePatternList(fields[idx])
	if err != nil {
		return entry, err
	}
	entry.patterns = patterns
	idx++

	keyField := fields[idx]
	if keyField == "*" {
		// OpenSSH 9.5+ allows a wildcard key type on revoked lines.
		if entry.marker != markerRevoked {
			return entry, fmt.Errorf("%w: wildcard key is only valid for @revoked", errMalformed)
		}
		entry.key = rawKey{keyType: "*"}
	} else {
		blob, err := base64.StdEncoding.DecodeString(keyField)
		if err != nil {
			return entry, fmt.Errorf("%w: invalid base64 key: %v", errMalformed, err)
		}
		keyType, err := keyTypeOf(blob)
		if err != nil {
			return entry, fmt.Errorf("%w: %v", errMalformed, err)
		}
		entry.key = rawKey{keyType: keyType, blob: blob}
	}
	idx++

	if idx < len(fields) {
		entry.comment = strings.Join(fields[idx:], " ")
	}
	return entry, nil
}

// keyTypeOf reads the leading length-prefixed string of an SSH wire blob.
func keyTypeOf(blob []byte) (string, error) {
	c := cursor{data: blob}
	name, err := c.readString()
	if err != nil {
		return "", err
	}
	if len(name) == 0 {
		return "", fmt.Errorf("empty key type")
	}
	return string(name), nil
}

func parsePatternList(list string) ([]pattern, error) {
	tokens := strings.Split(list, ",")
	patterns := make([]pattern, 0, len(tokens))
	for _, token := range tokens {
		if token == "" {
			return nil, fmt.Errorf("%w: empty host pattern", errMalformed)
		}
		p, err := parsePattern(token)
		if err != nil {
			return nil, err
		}
		patterns = append(patterns, p)
	}
	return patterns, nil
}

func parsePattern(token string) (pattern, error) {
	var p pattern

	if token[0] == '!' {
		p.negated = true
		token = token[1:]
		if token == "" {
			return p, fmt.Errorf("%w: dangling negation", errMalformed)
		}
	}

	if strings.HasPrefix(token, "|1|") {
		parts := strings.Split(token, "|")
		if len(parts) != 4 || parts[0] != "" || parts[1] != "1" {
			return p, fmt.Errorf("%w: malformed hashed host", errMalformed)
		}
		salt, err := b64decode(parts[2])
		if err != nil {
			return p, fmt.Errorf("%w: bad hash salt: %v", errMalformed, err)
		}
		digest, err := b64decode(parts[3])
		if err != nil {
			return p, fmt.Errorf("%w: bad hash digest: %v", errMalformed, err)
		}
		p.hashed = true
		p.salt = salt
		p.digest = digest
		p.port = -1
		return p, nil
	}

	p.port = -1
	if token[0] == '[' {
		closeBracket := strings.IndexByte(token, ']')
		if closeBracket < 0 {
			return p, fmt.Errorf("%w: unterminated bracket in %q", errMalformed, token)
		}
		p.host = token[1:closeBracket]
		rest := token[closeBracket+1:]
		if rest != "" {
			if rest[0] != ':' {
				return p, fmt.Errorf("%w: expected :port after bracket in %q", errMalformed, token)
			}
			spec := rest[1:]
			if spec == "*" {
				p.hasPort = true
			} else if allDigits(spec) {
				p.hasPort = true
				p.port = atoiOrZero(spec)
			} else {
				return p, fmt.Errorf("%w: invalid port in %q", errMalformed, token)
			}
		}
	} else if idx := strings.LastIndexByte(token, ':'); idx >= 0 && idx < len(token)-1 {
		spec := token[idx+1:]
		if allDigits(spec) || spec == "*" {
			p.host = token[:idx]
			p.hasPort = true
			if spec != "*" {
				p.port = atoiOrZero(spec)
			}
		} else {
			p.host = token
		}
	} else {
		p.host = token
	}

	if p.host == "" {
		return p, fmt.Errorf("%w: empty host pattern", errMalformed)
	}
	return p, nil
}

// matches evaluates every pattern in the line against host:port. A line
// matches when at least one positive pattern matches and no negated pattern
// matches, mirroring OpenSSH's host list semantics.
func (l hostLine) matches(host string, port int) bool {
	positive := false
	for _, p := range l.patterns {
		ok := p.match(host, port)
		if ok && p.negated {
			return false
		}
		if ok {
			positive = true
		}
	}
	return positive
}

func (p pattern) match(host string, port int) bool {
	if p.hashed {
		digest := hashHost(p.salt, canonicalAddr(host, port))
		return hmac.Equal(digest, p.digest)
	}
	if !globMatch(p.host, host) {
		return false
	}
	if p.hasPort && p.port >= 0 && p.port != port {
		return false
	}
	return true
}

// revokes reports whether the line (which must carry @revoked) revokes the
// presented key. A "*" key type revokes every key of the host. For
// certificates the inner CA key is compared.
func (l hostLine) revokes(pubKey PublicKey, isCert bool, caKey []byte) bool {
	if l.key.keyType == "*" {
		return true
	}
	if isCert {
		caType := typeOfBlob(caKey)
		if caType == "" {
			return false
		}
		return l.key.keyType == caType && bytes.Equal(l.key.blob, caKey)
	}
	return l.key.keyType == pubKey.Type() && bytes.Equal(l.key.blob, pubKey.Marshal())
}

func typeOfBlob(blob []byte) string {
	name, err := keyTypeOf(blob)
	if err != nil {
		return ""
	}
	return name
}
