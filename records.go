package evidence

import (
	"fmt"
	"strings"
	"time"
)

// RefTarget is a cross-file reference found inside a record payload,
// written as "ref=<file>:<recordID>".
type RefTarget struct {
	File   string
	Record string
}

// Record is one line of an evidence file: "<id>|<rfc3339nano-ts>|<payload>".
type Record struct {
	ID      string
	TS      time.Time
	Payload string
	Refs    []RefTarget
}

// parseRecords decodes newline-delimited evidence records.
func parseRecords(data []byte) ([]Record, error) {
	var out []Record
	for lineno, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("line %d: malformed record", lineno+1)
		}
		ts, err := time.Parse(time.RFC3339Nano, parts[1])
		if err != nil {
			return nil, fmt.Errorf("line %d: bad timestamp: %v", lineno+1, err)
		}
		rec := Record{ID: parts[0], TS: ts, Payload: parts[2]}
		for _, tok := range strings.Fields(parts[2]) {
			if !strings.HasPrefix(tok, "ref=") {
				continue
			}
			dst := strings.TrimPrefix(tok, "ref=")
			i := strings.Index(dst, ":")
			if i <= 0 || i == len(dst)-1 {
				return nil, fmt.Errorf("line %d: malformed ref %q", lineno+1, tok)
			}
			rec.Refs = append(rec.Refs, RefTarget{File: dst[:i], Record: dst[i+1:]})
		}
		out = append(out, rec)
	}
	return out, nil
}
