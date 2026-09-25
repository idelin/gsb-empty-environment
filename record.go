package kvimport

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// dataEntry is one confirmed record in the staging log. Off is the input
// offset just past the record's terminating newline.
type dataEntry struct {
	Off int64  `json:"off"`
	K   string `json:"k"`
	V   string `json:"v"`
}

// rejectEntry is one quarantined malformed record.
type rejectEntry struct {
	Off    int64  `json:"off"`
	Reason string `json:"reason"`
	Raw    string `json:"raw"`
}

func writeData(buf *bytes.Buffer, off int64, k, v string) {
	b, _ := json.Marshal(dataEntry{Off: off, K: k, V: v})
	buf.Write(b)
	buf.WriteByte('\n')
}

func writeReject(buf *bytes.Buffer, off int64, raw []byte, reason string) {
	b, _ := json.Marshal(rejectEntry{
		Off:    off,
		Reason: reason,
		Raw:    string(bytes.TrimRight(raw, "\r\n")),
	})
	buf.Write(b)
	buf.WriteByte('\n')
}

// parseRecord validates one input line. skip is true for blank lines, which
// are consumed silently. A non-empty reason means the record is malformed
// and must be quarantined without aborting the import.
func parseRecord(line []byte) (k, v, reason string, skip bool) {
	trimmed := bytes.TrimRight(line, "\r\n")
	if len(bytes.TrimSpace(trimmed)) == 0 {
		return "", "", "", true
	}
	var raw struct {
		K *string `json:"k"`
		V *string `json:"v"`
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return "", "", "invalid JSON: " + err.Error(), false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", "", "trailing data after JSON object", false
	}
	if raw.K == nil || raw.V == nil {
		return "", "", "missing k or v field", false
	}
	if *raw.K == "" {
		return "", "", "empty key", false
	}
	return *raw.K, *raw.V, "", false
}
