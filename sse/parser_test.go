package sse_test

import (
	"strings"
	"unicode/utf8"
)

// parsedEvent is one dispatched WHATWG "message" event.
type parsedEvent struct {
	event string
	id    string
	data  string
}

// parsedStream is the result of running the WHATWG event stream parsing
// algorithm: dispatched message events plus the last successful "retry:"
// update, if any.
type parsedStream struct {
	events    []parsedEvent
	lastRetry int64
	hasRetry  bool
}

// parseWHATWG is a close transcription of the "Interpreting an event
// stream" algorithm from the WHATWG HTML standard (section 9.2.6). It
// intentionally lives in the test package: the production writer must stay
// safe even when read back by an independent, spec-faithful parser.
func parseWHATWG(input []byte) parsedStream {
	var out parsedStream

	// Decode the byte stream as UTF-8 with the error mode "replace".
	decoded := make([]rune, 0, len(input))
	rest := input
	for len(rest) > 0 {
		r, size := utf8.DecodeRune(rest)
		decoded = append(decoded, r)
		rest = rest[size:]
	}

	// If the stream starts with a U+FEFF BOM, remove it.
	if len(decoded) > 0 && decoded[0] == '\uFEFF' {
		decoded = decoded[1:]
	}

	var (
		lastEventID string
		eventType   string
		data        []rune
	)

	dispatch := func() {
		if len(data) == 0 {
			eventType = ""
			return
		}
		// Remove the trailing U+000A LINE FEED, if any.
		if data[len(data)-1] == '\n' {
			data = data[:len(data)-1]
		}
		event := eventType
		if event == "" {
			event = "message"
		}
		out.events = append(out.events, parsedEvent{
			event: event,
			id:    lastEventID,
			data:  string(data),
		})
		data = nil
		eventType = ""
	}

	lines := splitLinesWHATWG(string(decoded))
	for _, line := range lines {
		if line == "" {
			dispatch()
			continue
		}
		if line[0] == ':' { // comment line
			continue
		}

		field := line
		value := ""
		if colon := strings.IndexByte(line, ':'); colon >= 0 {
			field = line[:colon]
			value = line[colon+1:]
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
		}

		switch field {
		case "event":
			eventType = value
		case "data":
			data = append(data, []rune(value)...)
			data = append(data, '\n')
		case "id":
			if !containsRune(value, 0) {
				lastEventID = value
			}
		case "retry":
			if n, ok := parseASCIIUint(value); ok {
				out.lastRetry = n
				out.hasRetry = true
			}
		default:
			// Unknown fields, including forged ones, are ignored.
		}
	}
	// Per the WHATWG algorithm, events are dispatched only when a blank
	// line is encountered; reaching the end of the stream dispatches
	// nothing on its own.
	return out
}

// splitLinesWHATWG implements the standard's line splitting: U+000D,
// U+000A and U+000D U+000A are all line terminators.
func splitLinesWHATWG(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\n':
			lines = append(lines, s[start:i])
			start = i + 1
		case '\r':
			lines = append(lines, s[start:i])
			i++
			if i < len(s) && s[i] == '\n' {
				i++
			}
			start = i
			i--
		}
	}
	lines = append(lines, s[start:])
	return lines
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}

// parseASCIIUint mirrors the "retry" rule: the value must consist solely of
// ASCII digits, interpreted as a base-ten integer.
func parseASCIIUint(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	var n int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}
