package mailauth

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// sigTags holds the DKIM-Signature / ARC-Message-Signature / ARC-Seal tag set
// used during verification.
type sigTags struct {
	version       string // v=
	algorithm     string // a=
	domain        string // d=
	selector      string // s=
	headerAlgo    string // c= header part
	bodyAlgo      string // c= body part
	headerList    []string
	bodyHashB64   string // bh=
	signatureB64  string // b=
	created       int64  // t=
	hasCreated    bool
	expires       int64 // x=
	hasExpires    bool
	bodyLength    int64 // l=
	hasBodyLength bool
	cv            string // ARC-Seal only
	instance      int    // ARC i=
	hasInstance   bool
	query         string // q=
	raw           map[string]string
}

var errMalformedSignature = errors.New("malformed signature tag list")

// parseTagList implements RFC 6376 section 3.2 tag=value parsing. Folding
// whitespace inside values is tolerated and preserved for later
// canonicalization; malformed entries without an equals sign are rejected.
func parseTagList(value string) (map[string]string, error) {
	tags := make(map[string]string)
	for {
		value = trimFWSLeft(value)
		if value == "" {
			break
		}
		semi := strings.IndexByte(value, ';')
		var spec string
		if semi < 0 {
			spec, value = value, ""
		} else {
			spec, value = value[:semi], value[semi+1:]
		}
		eq := strings.IndexByte(spec, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("%w: entry %q", errMalformedSignature, spec)
		}
		key := strings.TrimSpace(spec[:eq])
		val := spec[eq+1:]
		// Duplicate tags are a permerror (RFC 6376 section 3.2).
		if _, seen := tags[key]; seen {
			return nil, fmt.Errorf("%w: duplicate tag %q", errMalformedSignature, key)
		}
		tags[key] = val
	}
	return tags, nil
}

func trimFWSLeft(s string) string {
	for len(s) > 0 {
		switch s[0] {
		case ' ', '\t':
			s = s[1:]
		case '\r':
			if len(s) >= 2 && s[1] == '\n' {
				s = s[2:]
			} else {
				s = s[1:]
			}
		case '\n':
			s = s[1:]
		default:
			return s
		}
	}
	return s
}

// parseSignature parses and validates the structural contents of a signature
// header field value (the b tag remains in its folded/base64 wire form here).
func parseSignature(value string) (*sigTags, error) {
	raw, err := parseTagList(value)
	if err != nil {
		return nil, err
	}
	t := &sigTags{
		raw:        raw,
		bodyLength: -1,
		headerAlgo: canonSimple,
		bodyAlgo:   canonSimple,
		instance:   0,
	}

	if v, ok := raw["v"]; ok {
		t.version = strings.TrimSpace(v)
	}
	if c, ok := raw["c"]; ok {
		headerAlgo, bodyAlgo, err := parseCanonTag(c)
		if err != nil {
			return nil, err
		}
		t.headerAlgo, t.bodyAlgo = headerAlgo, bodyAlgo
	}
	if h, ok := raw["h"]; ok {
		t.headerList = parseHeaderList(h)
	}
	if bh, ok := raw["bh"]; ok {
		t.bodyHashB64 = stripFWS(bh)
	}
	if b, ok := raw["b"]; ok {
		t.signatureB64 = stripFWS(b)
	}
	if d, ok := raw["d"]; ok {
		t.domain = trimFWS(d)
	}
	if s, ok := raw["s"]; ok {
		t.selector = trimFWS(s)
	}
	if a, ok := raw["a"]; ok {
		t.algorithm = trimFWS(a)
	}
	if q, ok := raw["q"]; ok {
		t.query = trimFWS(q)
	}
	if tv, ok := raw["t"]; ok {
		n, err := parseUnsignedTag(tv)
		if err != nil {
			return nil, fmt.Errorf("invalid t= tag: %w", err)
		}
		t.created, t.hasCreated = n, true
	}
	if xv, ok := raw["x"]; ok {
		n, err := parseUnsignedTag(xv)
		if err != nil {
			return nil, fmt.Errorf("invalid x= tag: %w", err)
		}
		t.expires, t.hasExpires = n, true
	}
	if lv, ok := raw["l"]; ok {
		n, err := parseUnsignedTag(lv)
		if err != nil {
			return nil, fmt.Errorf("invalid l= tag: %w", err)
		}
		t.bodyLength, t.hasBodyLength = n, true
	}
	if cv, ok := raw["cv"]; ok {
		t.cv = trimFWS(cv)
	}
	if iv, ok := raw["i"]; ok {
		n, err := parseUnsignedTag(iv)
		if err != nil {
			return nil, fmt.Errorf("invalid i= tag: %w", err)
		}
		t.instance, t.hasInstance = int(n), true
	}
	return t, nil
}

func parseCanonTag(c string) (header, body string, err error) {
	c = trimFWS(c)
	parts := strings.SplitN(c, "/", 2)
	header = strings.TrimSpace(parts[0])
	if header != canonSimple && header != canonRelaxed {
		return "", "", fmt.Errorf("unsupported header canonicalization %q", header)
	}
	body = canonSimple
	if len(parts) == 2 {
		body = strings.TrimSpace(parts[1])
	}
	if body != canonSimple && body != canonRelaxed {
		return "", "", fmt.Errorf("unsupported body canonicalization %q", body)
	}
	return header, body, nil
}

func parseHeaderList(h string) []string {
	var names []string
	for _, name := range strings.Split(h, ":") {
		name = trimFWS(name)
		if name == "" {
			continue
		}
		names = append(names, strings.ToLower(name))
	}
	return names
}

func parseUnsignedTag(v string) (int64, error) {
	v = trimFWS(v)
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("not an unsigned integer: %q", v)
	}
	return n, nil
}

// stripFWS removes all folding whitespace so base64 content can be decoded.
func stripFWS(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// trimFWS trims spaces, tabs and folding CRLFs from both ends.
func trimFWS(s string) string {
	return trimFWSRight(trimFWSLeft(s))
}

func trimFWSRight(s string) string {
	for len(s) > 0 {
		switch s[len(s)-1] {
		case ' ', '\t', '\n':
			s = s[:len(s)-1]
		case '\r':
			s = s[:len(s)-1]
		default:
			return s
		}
	}
	return s
}

// decodeBase64 decodes standard base64 after removing embedded FWS.
func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(stripFWS(s))
}
