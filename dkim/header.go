package dkim

import (
	"strings"
)

// Header is one header field in its original order. Raw contains the complete
// field, including folded continuation lines but without the terminating CRLF.
type Header struct {
	Name     string
	Raw      string
	ValueRaw string
}

// ParseHeaders splits a message into headers and body. Header order and folding
// are preserved.
func ParseHeaders(message []byte) ([]Header, []byte) {
	normalized := normalizeCRLFBytes(message)
	headerEnd, bodyStart := splitMessage(normalized)
	headers := parseHeaderBlock(normalized[:headerEnd])
	return headers, normalized[bodyStart:]
}

func splitMessage(message []byte) (headerEnd, bodyStart int) {
	if len(message) == 0 {
		return 0, 0
	}
	if len(message) >= 2 && message[0] == '\r' && message[1] == '\n' {
		return 0, 2
	}
	for i := 0; i < len(message); i++ {
		if i+3 < len(message) && message[i] == '\r' && message[i+1] == '\n' &&
			message[i+2] == '\r' && message[i+3] == '\n' {
			return i, i + 4
		}
		if i+1 < len(message) && message[i] == '\n' && message[i+1] == '\n' {
			return i, i + 2
		}
	}
	return len(message), len(message)
}

func parseHeaderBlock(block []byte) []Header {
	if len(block) == 0 {
		return nil
	}
	lines := splitLines(block)
	headers := make([]Header, 0, len(lines))
	var current strings.Builder
	finish := func() {
		if current.Len() == 0 {
			return
		}
		raw := current.String()
		current.Reset()
		name, value, ok := strings.Cut(raw, ":")
		if !ok {
			headers = append(headers, Header{Name: raw, Raw: raw})
			return
		}
		headers = append(headers, Header{
			Name:     strings.TrimSpace(name),
			Raw:      raw,
			ValueRaw: value,
		})
	}
	for _, line := range lines {
		if line == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if current.Len() > 0 {
				current.WriteString("\r\n")
				current.WriteString(line)
			}
			continue
		}
		finish()
		current.WriteString(line)
	}
	finish()
	return headers
}

func splitLines(block []byte) []string {
	text := string(block)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Split(text, "\n")
}

func normalizeCRLFBytes(in []byte) []byte {
	text := string(in)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\n", "\r\n")
	return []byte(text)
}

func normalizeCRLF(in string) string {
	return string(normalizeCRLFBytes([]byte(in)))
}
