package mailauth

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

const (
	canonSimple  = "simple"
	canonRelaxed = "relaxed"
)

var crlf = []byte("\r\n")

// canonicalHeader applies the named header canonicalization algorithm
// ("simple" or "relaxed") to a concrete header field. The field is expected in
// normalized CRLF wire form including its terminating CRLF.
func canonicalHeader(algorithm string, h Header) []byte {
	if algorithm == canonRelaxed {
		return relaxedHeader(h)
	}
	// Simple: the field must not be modified in any way.
	return []byte(h.Raw)
}

// missingHeader renders the placeholder consumed when an h= entry has no
// matching field left (oversigning, RFC 6376 section 5.4.2). Relaxed emits the
// lowercased name followed by a colon and CRLF; simple emits a bare CRLF.
func missingHeader(algorithm, name string) []byte {
	if algorithm == canonRelaxed {
		return []byte(strings.ToLower(name) + ":" + "\r\n")
	}
	return []byte("\r\n")
}

// relaxedHeader implements RFC 6376 section 3.4.2.
func relaxedHeader(h Header) []byte {
	name := strings.ToLower(h.Name)

	// Drop the field terminating CRLF, then remove folding CRLFs that are
	// followed by WSP (unfolding keeps the continuation WSP itself).
	value := h.Value
	var unfolded bytes.Buffer
	unfolded.Grow(len(value))
	for i := 0; i < len(value); i++ {
		if value[i] == '\r' && i+2 < len(value) && value[i+1] == '\n' &&
			(value[i+2] == ' ' || value[i+2] == '\t') {
			i++ // skip the CRLF, retain the continuation WSP
			continue
		}
		unfolded.WriteByte(value[i])
	}

	collapsed := collapseWSP(unfolded.Bytes())
	// Remove WSP immediately after the colon and any trailing WSP.
	trimmed := bytes.TrimLeft(collapsed, " \t")
	trimmed = bytes.TrimRight(trimmed, " \t")

	var out bytes.Buffer
	out.Grow(len(name) + len(trimmed) + 3)
	out.WriteString(name)
	out.WriteByte(':')
	out.Write(trimmed)
	out.Write(crlf)
	return out.Bytes()
}

// collapseWSP replaces every maximal run of SP/HTAB bytes with one SP.
func collapseWSP(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inWSP := false
	for _, ch := range b {
		if ch == ' ' || ch == '\t' {
			if !inWSP {
				out = append(out, ' ')
			}
			inWSP = true
			continue
		}
		inWSP = false
		out = append(out, ch)
	}
	return out
}

// canonicalBody applies the named body canonicalization algorithm. Inputs are
// assumed to use normalized CRLF line endings.
func canonicalBody(algorithm string, body []byte) []byte {
	if algorithm == canonRelaxed {
		return relaxedBody(body)
	}
	return simpleBody(body)
}

// simpleBody implements RFC 6376 section 3.4.3: trailing empty lines are
// removed and the body is terminated with exactly one CRLF; an empty body
// canonicalizes to a single CRLF.
func simpleBody(body []byte) []byte {
	trimmed := bytes.TrimRight(body, "\r\n")
	if len(trimmed) == 0 {
		return []byte("\r\n")
	}
	out := make([]byte, 0, len(trimmed)+2)
	out = append(out, trimmed...)
	out = append(out, '\r', '\n')
	return out
}

// relaxedBody implements RFC 6376 section 3.4.4.
func relaxedBody(body []byte) []byte {
	lines := splitLines(body)

	canonicalLines := make([][]byte, 0, len(lines))
	for _, line := range lines {
		collapsed := collapseWSP(line)
		// WSP at the end of the unfolded line is deleted. Leading WSP is
		// reduced to a single space, not deleted.
		collapsed = bytes.TrimRight(collapsed, " \t")
		canonicalLines = append(canonicalLines, collapsed)
	}

	// Ignore empty lines at the end of the body.
	for len(canonicalLines) > 0 && len(canonicalLines[len(canonicalLines)-1]) == 0 {
		canonicalLines = canonicalLines[:len(canonicalLines)-1]
	}
	if len(canonicalLines) == 0 {
		return []byte("\r\n")
	}

	var out bytes.Buffer
	out.Grow(len(body))
	for i, line := range canonicalLines {
		if i > 0 {
			out.Write(crlf)
		}
		out.Write(line)
	}
	out.Write(crlf)
	return out.Bytes()
}

// splitLines splits a CRLF terminated body into lines without the CRLF. A body
// with no trailing CRLF still yields a final line holding the remaining bytes.
func splitLines(body []byte) [][]byte {
	var lines [][]byte
	for len(body) > 0 {
		idx := bytes.Index(body, crlf)
		if idx < 0 {
			lines = append(lines, body)
			break
		}
		lines = append(lines, body[:idx])
		body = body[idx+2:]
	}
	return lines
}

// bodyHash computes the base64 encoded SHA-256 of the canonicalized body, or
// of its first lengthLimit bytes when an l= tag is in effect. A negative limit
// means the whole canonical body is hashed.
func bodyHash(bodyCanonAlgo string, body []byte, lengthLimit int64) string {
	canon := canonicalBody(bodyCanonAlgo, body)
	hashed := canon
	if lengthLimit >= 0 {
		if lengthLimit > int64(len(canon)) {
			lengthLimit = int64(len(canon))
		}
		hashed = canon[:lengthLimit]
	}
	sum := sha256.Sum256(hashed)
	return base64.StdEncoding.EncodeToString(sum[:])
}

// canonicalBodyLength returns the size of the canonicalized body, used to
// decide whether an l= tag hides appended (unsigned) content.
func canonicalBodyLength(bodyCanonAlgo string, body []byte) int {
	return len(canonicalBody(bodyCanonAlgo, body))
}
