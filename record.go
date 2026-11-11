package warcwriter

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// WARC record type names defined by the WARC/1.1 specification.
const (
	TypeWarcinfo = "warcinfo"
	TypeRequest  = "request"
	TypeResponse = "response"
	TypeMetadata = "metadata"
	TypeRevisit  = "revisit"
)

// RevisitProfileIdenticalPayload is the WARC/1.1 profile used when a payload
// digest has already been archived.
const RevisitProfileIdenticalPayload = "http://netpreserve.org/warc/1.1/revisit/identical-payload-digest"

const warcVersion = "WARC/1.1"

// Record is an in-memory representation of a single WARC/1.1 record.
type Record struct {
	Type          string
	TargetURI     string
	Date          time.Time
	RecordID      string
	ConcurrentTo  string
	RefersTo      string
	Profile       string
	ContentType   string
	BlockDigest   string
	PayloadDigest string
	Extra         map[string]string
	Body          []byte
}

// Field returns a WARC header value (case-insensitive), or "" if absent.
func (r *Record) Field(name string) string {
	if r.Extra == nil {
		return ""
	}
	for k, v := range r.Extra {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

// marshal serializes the record as a WARC/1.1 byte block (without gzip).
// The block digest is computed over Body and stored on the record when empty.
func (r *Record) marshal() []byte {
	if r.RecordID == "" {
		r.RecordID = newRecordID()
	}
	if r.Date.IsZero() {
		r.Date = time.Now().UTC()
	}
	if r.BlockDigest == "" {
		r.BlockDigest = LabeledSHA1(r.Body)
	}

	var b bytes.Buffer
	b.WriteString(warcVersion)
	b.WriteString("\r\n")

	writeHeader := func(name, value string) {
		if value == "" {
			return
		}
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(value)
		b.WriteString("\r\n")
	}

	writeHeader("WARC-Type", r.Type)
	writeHeader("WARC-Target-URI", r.TargetURI)
	writeHeader("WARC-Date", r.Date.UTC().Format(time.RFC3339Nano))
	writeHeader("WARC-Record-ID", r.RecordID)
	writeHeader("WARC-Concurrent-To", r.ConcurrentTo)
	writeHeader("WARC-Refers-To", r.RefersTo)
	writeHeader("WARC-Profile", r.Profile)

	extraKeys := make([]string, 0, len(r.Extra))
	for k := range r.Extra {
		extraKeys = append(extraKeys, k)
	}
	sort.Strings(extraKeys)
	for _, k := range extraKeys {
		writeHeader(k, r.Extra[k])
	}

	writeHeader("WARC-Payload-Digest", r.PayloadDigest)
	writeHeader("WARC-Block-Digest", r.BlockDigest)
	writeHeader("Content-Type", r.ContentType)
	writeHeader("Content-Length", strconv.Itoa(len(r.Body)))
	b.WriteString("\r\n")
	b.Write(r.Body)
	b.WriteString("\r\n\r\n")
	return b.Bytes()
}

// LabeledSHA1 returns the WARC labeled digest form of the SHA-1 of data:
// "sha1:" followed by the unpadded RFC 4648 base32 encoding.
func LabeledSHA1(data []byte) string {
	sum := sha1.Sum(data)
	encoded := base32.StdEncoding.EncodeToString(sum[:])
	return "sha1:" + strings.TrimRight(encoded, "=")
}

func newRecordID() string {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		panic(fmt.Errorf("warcwriter: generate record id: %w", err))
	}
	// RFC 4122 version 4 (random) UUID.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// splitMessage splits a raw HTTP message into its header section (including
// the terminating blank line) and the message body.
func splitMessage(raw []byte) (head, body []byte) {
	sep := []byte("\r\n\r\n")
	idx := bytes.Index(raw, sep)
	if idx < 0 {
		sep = []byte("\n\n")
		idx = bytes.Index(raw, sep)
		if idx < 0 {
			return raw, nil
		}
	}
	return raw[:idx+len(sep)], raw[idx+len(sep):]
}

// warcFieldsBody serializes key/value metadata as an application/warc-fields body.
func warcFieldsBody(fields map[string]string) []byte {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(fields[k])
		b.WriteString("\r\n")
	}
	return b.Bytes()
}
