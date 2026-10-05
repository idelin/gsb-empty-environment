package logreasm

import (
	"regexp"
	"time"
)

// tsRe matches a leading ISO-8601-like timestamp.
var tsRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?)`)

var tsLayouts = []string{
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05.999999999Z0700",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999Z0700",
	"2006-01-02 15:04:05.999999999",
}

// isRecordStart reports whether a line begins a new record: any
// non-indented line. Timestamp lines always match this.
func isRecordStart(line string) bool {
	if line == "" {
		return true
	}
	return line[0] != ' ' && line[0] != '\t'
}

// parseTick extracts and parses the leading timestamp of a record line.
func parseTick(line string) (time.Time, bool) {
	m := tsRe.FindStringSubmatch(line)
	if m == nil {
		return time.Time{}, false
	}
	s := m[1]
	for _, layout := range tsLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
