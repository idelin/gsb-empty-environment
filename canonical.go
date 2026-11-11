package sigv4chunk

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const (
	algorithmName      = "AWS4-HMAC-SHA256"
	terminatorString   = "aws4_request"
	payloadAlgorithm   = "AWS4-HMAC-SHA256-PAYLOAD"
	streamingPayload   = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD"
	amzContentSHA256   = "x-amz-content-sha256"
	amzDateHeader      = "x-amz-date"
	amzDecodedLength   = "x-amz-decoded-content-length"
	maxChunkHeaderSize = 1 << 20
)

var hexUpper = "0123456789ABCDEF"

func isUnreserved(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9':
		return true
	case b == '-', b == '_', b == '.', b == '~':
		return true
	default:
		return false
	}
}

// percentEncode encodes every byte that is not an RFC 3986 unreserved
// character. When keepSlash is set, '/' is preserved so it can be used on
// whole URI paths; query components must always encode '/'.
func percentEncode(s string, keepSlash bool) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) || (keepSlash && c == '/') {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexUpper[c>>4])
		b.WriteByte(hexUpper[c&0x0f])
	}
	return b.String()
}

// canonicalURIPath applies the object-storage service convention: the path is
// split on '/', each segment is percent-encoded byte by byte (only RFC 3986
// unreserved characters stay literal), then the segments are rejoined. Dot
// segments are never normalized because the signature is computed over the
// request path as sent.
func canonicalURIPath(path string) string {
	if path == "" {
		return "/"
	}
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		segments[i] = percentEncode(seg, false)
	}
	return strings.Join(segments, "/")
}

type queryPair struct {
	key   string
	value string
}

// canonicalQueryString decodes the raw query, sorts pairs by (key, value)
// using code-point order and re-encodes every component strictly. A literal
// '+' is not treated as a space: SigV4 signers encode spaces as %20, so an
// unescaped '+' represents a real plus sign and is re-encoded as %2B.
func canonicalQueryString(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	pairs := make([]queryPair, 0)
	for _, piece := range strings.Split(rawQuery, "&") {
		if piece == "" {
			continue
		}
		key := piece
		value := ""
		if eq := strings.IndexByte(piece, '='); eq >= 0 {
			key = piece[:eq]
			value = piece[eq+1:]
		}
		key = strictUnescape(key)
		value = strictUnescape(value)
		pairs = append(pairs, queryPair{key: key, value: value})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].key != pairs[j].key {
			return pairs[i].key < pairs[j].key
		}
		return pairs[i].value < pairs[j].value
	})
	encoded := make([]string, len(pairs))
	for i, p := range pairs {
		encoded[i] = percentEncode(p.key, false) + "=" + percentEncode(p.value, false)
	}
	return strings.Join(encoded, "&")
}

// strictUnescape only resolves percent-encoded octets; unlike
// url.QueryUnescape it never converts '+' into a space.
func strictUnescape(s string) string {
	if !strings.ContainsRune(s, '%') {
		return s
	}
	decoded, err := url.PathUnescape(s)
	if err != nil {
		return s
	}
	return decoded
}

// canonicalHeaderBlock builds the canonical headers block and the signed
// header list from the header names carried in the Authorization header.
// Header names are lower-cased, values are trimmed and runs of whitespace are
// collapsed to a single space, matching the SigV4 service convention.
func canonicalHeaderBlock(r *http.Request, signedHeaders []string) (canonicalHeaders string, signedHeaderList string) {
	type headerLine struct {
		name  string
		value string
	}
	lines := make([]headerLine, 0, len(signedHeaders))
	for _, name := range signedHeaders {
		var value string
		switch name {
		case "host":
			value = r.Host
			if value == "" {
				value = r.Header.Get("Host")
			}
		default:
			values := r.Header.Values(http.CanonicalHeaderKey(name))
			parts := make([]string, 0, len(values))
			for _, v := range values {
				parts = append(parts, strings.Join(strings.Fields(v), " "))
			}
			value = strings.Join(parts, ",")
		}
		lines = append(lines, headerLine{name: name, value: strings.Join(strings.Fields(value), " ")})
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].name < lines[j].name })

	names := make([]string, len(lines))
	var block strings.Builder
	for i, line := range lines {
		names[i] = line.name
		block.WriteString(line.name)
		block.WriteByte(':')
		block.WriteString(line.value)
		block.WriteByte('\n')
	}
	return block.String(), strings.Join(names, ";")
}

// buildCanonicalRequest renders the SigV4 canonical request for the supplied
// signed header set and payload hash.
func buildCanonicalRequest(r *http.Request, signedHeaders []string, payloadHash string) string {
	headerBlock, signedList := canonicalHeaderBlock(r, signedHeaders)
	var b strings.Builder
	b.WriteString(r.Method)
	b.WriteByte('\n')
	b.WriteString(canonicalURIPath(r.URL.Path))
	b.WriteByte('\n')
	b.WriteString(canonicalQueryString(r.URL.RawQuery))
	b.WriteByte('\n')
	b.WriteString(headerBlock)
	b.WriteByte('\n')
	b.WriteString(signedList)
	b.WriteByte('\n')
	b.WriteString(payloadHash)
	return b.String()
}

func hexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
