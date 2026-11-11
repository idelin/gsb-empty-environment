package warcwriter

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// IndexEntry is one CDXJ index line pointing at a response/revisit gzip
// member inside a WARC file.
type IndexEntry struct {
	SURT          string    `json:"-"`
	Timestamp     string    `json:"-"`
	Date          time.Time `json:"-"`
	URI           string    `json:"uri"`
	Status        string    `json:"status_code"`
	MIME          string    `json:"mime_type"`
	Filename      string    `json:"filename"`
	Offset        int64     `json:"offset"`
	Length        int64     `json:"length"`
	Digest        string    `json:"digest"`
	PayloadDigest string    `json:"payload_digest"`
	ID            string    `json:"record_id"`
	Revisit       bool      `json:"is_revisit"`
	RefersTo      string    `json:"refers_to,omitempty"`
}

// cdxjEntry mirrors IndexEntry plus the in-memory parsed capture date.
type cdxjEntry = IndexEntry

// line serializes the entry as "<surt> <14-digit timestamp> <json>".
func (e *IndexEntry) line() (string, error) {
	payload, err := json.Marshal(e)
	if err != nil {
		return "", fmt.Errorf("warcwriter: marshal cdxj entry: %w", err)
	}
	return e.SURT + " " + e.Timestamp + " " + string(payload), nil
}

// parseCDXJLine parses one "<surt> <timestamp> <json>" index line.
func parseCDXJLine(line string) (*IndexEntry, error) {
	parts := strings.SplitN(line, " ", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("warcwriter: malformed cdxj line: %q", line)
	}
	entry := &IndexEntry{}
	if err := json.Unmarshal([]byte(parts[2]), entry); err != nil {
		return nil, fmt.Errorf("warcwriter: unmarshal cdxj json: %w", err)
	}
	entry.SURT = parts[0]
	entry.Timestamp = parts[1]
	if t, err := time.Parse("20060102150405", parts[1]); err == nil {
		entry.Date = t.UTC()
	}
	return entry, nil
}

// ReadCDXJ loads every entry from a CDXJ index file.
func ReadCDXJ(path string) ([]*IndexEntry, error) {
	data, err := readFile(path)
	if err != nil {
		return nil, err
	}
	var entries []*IndexEntry
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "{") {
			continue
		}
		entry, err := parseCDXJLine(line)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// String renders the entry back into a CDXJ line.
func (e *IndexEntry) String() string {
	line, err := e.line()
	if err != nil {
		return ""
	}
	return line
}
