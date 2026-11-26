package mailauth

import (
	"bytes"
	"fmt"
	"strings"
)

// signedData builds the exact byte stream that was signed, following RFC 6376
// section 5.4. It returns the canonicalized bytes plus the number of From
// header instances the h= list actually bound to (used to detect signatures
// that fail to cover From).
func signedData(headers []Header, sigHeader Header, sigMatchName string,
	t *sigTags) ([]byte, int, error) {
	sigMatchName = strings.ToLower(sigMatchName)

	blanked, err := blankSignatureB(sigHeader.Raw)
	if err != nil {
		return nil, 0, err
	}
	blankedHeader := sigHeader
	blankedHeader.Raw = blanked
	colon := strings.IndexByte(blanked, ':')
	if colon >= 0 && len(blanked) >= 2 {
		blankedHeader.Value = blanked[colon+1 : len(blanked)-2]
	}

	// Every header starts available; matched entries are removed so repeated
	// h= names consume fields from the bottom of the block upwards.
	available := make([]int, len(headers))
	for i := range headers {
		available[i] = i
	}

	var data bytes.Buffer
	fromBound := 0

	for n := len(t.headerList) - 1; n >= 0; n-- {
		name := t.headerList[n]
		match := -1

		// The signature field being verified is always matched to itself, even
		// when several instances of the same field name exist.
		for k := len(available) - 1; k >= 0; k-- {
			idx := available[k]
			h := headers[idx]
			if !headerNameIs(h.Name, name) {
				continue
			}
			if headerNameIs(sigMatchName, name) && idx != sigHeader.Index {
				continue
			}
			match = k
			break
		}

		if match < 0 {
			// Oversigning placeholder: the name has no remaining instance.
			// It is still fed into the signed stream (RFC 6376 5.4.2), which
			// pins the absence of any later-injected field of that name.
			data.Write(missingHeader(t.headerAlgo, name))
			continue
		}

		idx := available[match]
		available = append(available[:match], available[match+1:]...)
		chosen := headers[idx]
		// RFC 6376 3.7: when the signature field's own name appears in h=, its
		// b= tag is treated as empty in every canonicalized occurrence.
		if headerNameIs(chosen.Name, sigMatchName) && chosen.Index == sigHeader.Index {
			chosen = blankedHeader
		}
		data.Write(canonicalHeader(t.headerAlgo, chosen))
		if name == "from" {
			fromBound++
		}
	}

	data.Write(canonicalHeader(t.headerAlgo, blankedHeader))

	return data.Bytes(), fromBound, nil
}

// blankSignatureB rewrites the b= tag value of a signature field to empty while
// preserving every other byte, including folding whitespace and the trailing
// CRLF. Semicolons cannot occur inside base64 tag values, so tag boundaries are
// unambiguous even when the value spans folded lines.
func blankSignatureB(raw string) (string, error) {
	colon := strings.IndexByte(raw, ':')
	if colon < 0 {
		return "", fmt.Errorf("%w: signature field lacks colon", errMalformedSignature)
	}
	prefix, value := raw[:colon+1], raw[colon+1:]

	var out bytes.Buffer
	out.Grow(len(value))
	pos := 0
	blanked := false
	for pos < len(value) {
		// Split at the next tag boundary first. A tag value (notably base64 in
		// b=) may itself contain '=' padding, so the equals sign cannot be
		// located across semicolon boundaries.
		semi := strings.IndexByte(value[pos:], ';')
		var end int
		if semi < 0 {
			end = len(value)
		} else {
			end = pos + semi
		}
		spec := value[pos:end]
		eq := strings.IndexByte(spec, '=')
		if eq < 0 {
			out.WriteString(spec)
			if end >= len(value) {
				break
			}
			out.WriteByte(';')
			pos = end + 1
			continue
		}
		key := trimFWS(spec[:eq])

		out.WriteString(value[pos : pos+eq+1])
		if key == "b" {
			blanked = true
			// Emit nothing for the value; the boundary semicolon is rewritten
			// below unchanged.
		} else {
			out.WriteString(value[pos+eq+1 : end])
		}
		if end >= len(value) {
			break
		}
		out.WriteByte(';')
		pos = end + 1
	}
	if !blanked {
		return "", fmt.Errorf("%w: signature has no b= tag", errMalformedSignature)
	}
	return prefix + out.String(), nil
}
