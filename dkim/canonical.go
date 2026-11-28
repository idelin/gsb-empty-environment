package dkim

import (
	"strings"
)

// CanonicalizeHeader applies the simple or relaxed DKIM header
// canonicalization algorithm.
func CanonicalizeHeader(header Header, mode string) string {
	raw := normalizeCRLF(header.Raw)
	if mode == "relaxed" {
		value := header.ValueRaw
		if value == "" {
			if _, after, ok := strings.Cut(raw, ":"); ok {
				value = after
			}
		}
		value = relaxedValue(value)
		return strings.ToLower(strings.TrimSpace(header.Name)) + ":" + value + "\r\n"
	}
	if !strings.Contains(raw, ":") {
		raw += ":"
	}
	return raw + "\r\n"
}

func canonicalizeEmptyHeader(name, mode string) string {
	return CanonicalizeHeader(Header{Name: name, Raw: name + ":", ValueRaw: ""}, mode)
}

func relaxedValue(value string) string {
	var out strings.Builder
	space := false
	for _, r := range value {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			space = true
			continue
		}
		if space && out.Len() > 0 {
			out.WriteByte(' ')
		}
		space = false
		out.WriteRune(r)
	}
	return out.String()
}

// CanonicalizeBody applies the simple or relaxed DKIM body canonicalization
// algorithm.
func CanonicalizeBody(body []byte, mode string) []byte {
	normalized := string(normalizeCRLFBytes(body))
	lines := strings.Split(normalized, "\r\n")

	if mode == "relaxed" {
		for i, line := range lines {
			lines[i] = relaxBodyLine(line)
		}
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil
	}
	return []byte(strings.Join(lines, "\r\n") + "\r\n")
}

func relaxBodyLine(line string) string {
	var out strings.Builder
	space := false
	for _, r := range line {
		if r == ' ' || r == '\t' {
			space = true
			continue
		}
		if space {
			out.WriteByte(' ')
			space = false
		}
		out.WriteRune(r)
	}
	return strings.TrimRight(out.String(), " \t")
}

func signatureCandidates(header Header) []Header {
	raw := normalizeCRLF(header.Raw)
	name, value, ok := strings.Cut(raw, ":")
	if !ok {
		return []Header{header}
	}
	empty := header
	empty.Raw = name + ":" + stripBTag(value, false)
	empty.ValueRaw = stripBTag(value, false)
	keepSpace := header
	keepSpace.Raw = name + ":" + stripBTag(value, true)
	keepSpace.ValueRaw = stripBTag(value, true)
	if empty.Raw == keepSpace.Raw {
		return []Header{empty}
	}
	return []Header{empty, keepSpace}
}

func stripBTag(value string, preserveWhitespace bool) string {
	segments := strings.Split(value, ";")
	for i, segment := range segments {
		eq := strings.IndexByte(segment, '=')
		if eq < 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(segment[:eq]), "b") {
			if preserveWhitespace {
				rest := segment[eq+1:]
				var whitespace strings.Builder
				for _, r := range rest {
					if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
						whitespace.WriteRune(r)
					}
				}
				segments[i] = segment[:eq+1] + whitespace.String()
			} else {
				segments[i] = segment[:eq+1]
			}
		}
	}
	return strings.Join(segments, ";")
}
