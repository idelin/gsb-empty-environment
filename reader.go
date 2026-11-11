package warcwriter

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DigestMismatchError reports a record whose recomputed digest does not match
// the digest declared in its WARC header.
type DigestMismatchError struct {
	RecordID string
	Header   string
	Expected string
	Got      string
}

func (e *DigestMismatchError) Error() string {
	return fmt.Sprintf("warcwriter: digest mismatch for %s (%s): header=%s computed=%s",
		e.RecordID, e.Header, e.Expected, e.Got)
}

// Reader provides random access to WARC records inside a directory of
// size-rolled .warc.gz files via a CDXJ index.
type Reader struct {
	dir  string
	file *os.File
	name string
}

// NewReader opens a WARC directory for reading.
func NewReader(dir string) *Reader {
	return &Reader{dir: dir}
}

func readFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("warcwriter: read %s: %w", path, err)
	}
	return data, nil
}

// Open reads and verifies the single gzip WARC member referenced by entry,
// seeking directly to entry.Offset within the indexed file.
func (r *Reader) Open(entry *IndexEntry) (*Record, error) {
	if entry == nil {
		return nil, errors.New("warcwriter: nil index entry")
	}
	if filepath.Base(entry.Filename) != entry.Filename {
		return nil, fmt.Errorf("warcwriter: unsafe index filename %q", entry.Filename)
	}
	path := filepath.Join(r.dir, entry.Filename)
	if r.name != path || r.file == nil {
		if r.file != nil {
			_ = r.file.Close()
			r.file = nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("warcwriter: open warc: %w", err)
		}
		r.file = f
		r.name = path
	}

	if _, err := r.file.Seek(entry.Offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("warcwriter: seek offset: %w", err)
	}
	// Read one gzip member through a bounded buffer: the file handle's own
	// buffering would overshoot the member, so the member's exact end is
	// measured on the buffer rather than on the file position.
	limit := entry.Length
	if limit <= 0 {
		info, err := r.file.Stat()
		if err != nil {
			return nil, fmt.Errorf("warcwriter: stat: %w", err)
		}
		limit = info.Size() - entry.Offset
	}
	memberBytes, err := io.ReadAll(io.LimitReader(r.file, limit))
	if err != nil {
		return nil, fmt.Errorf("warcwriter: read member region: %w", err)
	}
	memberReader := bytes.NewReader(memberBytes)
	gz, err := newSingleStreamReader(memberReader)
	if err != nil {
		return nil, fmt.Errorf("warcwriter: gzip member at offset %d: %w", entry.Offset, err)
	}
	block, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("warcwriter: inflate record: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("warcwriter: close gzip member: %w", err)
	}

	record, err := parseRecord(block)
	if err != nil {
		return nil, err
	}
	if err := verifyRecord(record); err != nil {
		return nil, err
	}
	if entry.Length > 0 {
		end, err := memberReader.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, fmt.Errorf("warcwriter: member end: %w", err)
		}
		if end != int64(len(memberBytes)) {
			return nil, fmt.Errorf("warcwriter: member length mismatch: index=%d actual=%d",
				entry.Length, end)
		}
	}
	return record, nil
}

// Close releases the underlying WARC file handle.
func (r *Reader) Close() error {
	if r.file != nil {
		err := r.file.Close()
		r.file = nil
		return err
	}
	return nil
}

// parseRecord parses an inflated WARC/1.1 record block.
func parseRecord(block []byte) (*Record, error) {
	headerBytes, body, ok := bytes.Cut(block, []byte("\r\n\r\n"))
	if !ok {
		return nil, errors.New("warcwriter: record missing header terminator")
	}
	lines := strings.Split(string(headerBytes), "\r\n")
	if len(lines) == 0 || lines[0] != warcVersion {
		return nil, fmt.Errorf("warcwriter: unsupported version line %q", lines[0])
	}
	record := &Record{Extra: map[string]string{}}
	declaredLength := -1
	for _, line := range lines[1:] {
		name, value, found := strings.Cut(line, ":")
		if !found {
			return nil, fmt.Errorf("warcwriter: malformed header line %q", line)
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(name) {
		case "warc-type":
			record.Type = value
		case "warc-target-uri":
			record.TargetURI = value
		case "warc-record-id":
			record.RecordID = value
		case "warc-concurrent-to":
			record.ConcurrentTo = value
		case "warc-refers-to":
			record.RefersTo = value
		case "warc-profile":
			record.Profile = value
		case "content-type":
			record.ContentType = value
		case "warc-block-digest":
			record.BlockDigest = value
		case "warc-payload-digest":
			record.PayloadDigest = value
		case "content-length":
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("warcwriter: bad content-length: %w", err)
			}
			declaredLength = n
		default:
			record.Extra[name] = value
		}
	}
	if !bytes.HasSuffix(body, []byte("\r\n\r\n")) {
		return nil, errors.New("warcwriter: record missing trailing CRLF CRLF")
	}
	record.Body = body[:len(body)-4]
	if declaredLength >= 0 && declaredLength != len(record.Body) {
		return nil, fmt.Errorf("warcwriter: content-length %d != body %d", declaredLength, len(record.Body))
	}
	return record, nil
}

// verifyRecord recomputes block and payload digests and compares them against
// the WARC headers. Revisit records carry only HTTP headers as the block, so
// payload digest verification is skipped for them.
func verifyRecord(r *Record) error {
	if r.BlockDigest != "" {
		if got := LabeledSHA1(r.Body); got != r.BlockDigest {
			return &DigestMismatchError{RecordID: r.RecordID, Header: "WARC-Block-Digest", Expected: r.BlockDigest, Got: got}
		}
	}
	if r.PayloadDigest == "" || r.Type == TypeRevisit || r.Type == TypeWarcinfo || r.Type == TypeMetadata {
		return nil
	}
	payload, err := recordPayload(r)
	if err != nil {
		return err
	}
	if got := LabeledSHA1(payload); got != r.PayloadDigest {
		return &DigestMismatchError{RecordID: r.RecordID, Header: "WARC-Payload-Digest", Expected: r.PayloadDigest, Got: got}
	}
	return nil
}

// recordPayload extracts the HTTP message body for payload-digest checking.
func recordPayload(r *Record) ([]byte, error) {
	switch r.Type {
	case TypeResponse:
		resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(r.Body)), nil)
		if err != nil {
			return nil, fmt.Errorf("warcwriter: parse response for digest: %w", err)
		}
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	case TypeRequest:
		req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(r.Body)))
		if err != nil {
			return nil, fmt.Errorf("warcwriter: parse request for digest: %w", err)
		}
		if req.Body == nil {
			return nil, nil
		}
		defer req.Body.Close()
		return io.ReadAll(req.Body)
	default:
		return r.Body, nil
	}
}
