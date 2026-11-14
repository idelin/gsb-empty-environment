package netsec

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// SchemaVersion is written into every record. Verifiers reject records
	// produced by an unknown schema version.
	SchemaVersion = 1

	// HashSize is the number of bytes in a SHA-256 digest.
	HashSize = sha256.Size

	// Genesis is the chain head before the first record is written.
	Genesis = "0000000000000000000000000000000000000000000000000000000000000000"

	defaultMaxRecordBytes = 1 << 20
)

var (
	// ErrClosed is returned when an encoder is used after Seal or Close.
	ErrClosed = errors.New("auditlog: encoder is sealed")

	// ErrUnreadable is returned by Encoder.Verify when the destination cannot
	// be read back. Call Verify with a reader instead.
	ErrUnreadable = errors.New("auditlog: destination is not readable")

	// ErrInvalidRecord is the umbrella error for malformed records.
	ErrInvalidRecord = errors.New("auditlog: invalid record")

	// ErrInvalidUTF8 is returned when a field cannot be represented as JSON.
	ErrInvalidUTF8 = errors.New("auditlog: field is not valid UTF-8")

	// ErrEventID is returned when the server-side event ID generator fails or
	// produces a duplicate.
	ErrEventID = errors.New("auditlog: event ID generation failed")

	// ErrRecordTooLarge is returned when a canonical record exceeds the limit.
	ErrRecordTooLarge = errors.New("auditlog: record exceeds size limit")

	// ErrChainMismatch is returned when a digest link or the head is not valid.
	ErrChainMismatch = errors.New("auditlog: digest chain mismatch")

	// ErrCountMismatch is returned when a record count differs from the
	// expected or sealed count.
	ErrCountMismatch = errors.New("auditlog: record count mismatch")

	// ErrTimestamp is returned for a non-canonical audit timestamp.
	ErrTimestamp = errors.New("auditlog: invalid server timestamp")
)

// Record is the decoded form of one audit line. Timestamp, Subject, EventID,
// Seq, Prev and Hash are server-owned envelope fields. User-controlled data is
// contained in Fields and can never overwrite the envelope.
type Record struct {
	Version   int               `json:"v"`
	Seq       uint64            `json:"seq"`
	Timestamp string            `json:"timestamp"`
	Subject   string            `json:"subject"`
	EventID   string            `json:"event_id"`
	Fields    map[string]string `json:"fields"`
	Prev      string            `json:"prev"`
	Hash      string            `json:"hash"`
}

// MarshalJSON returns the canonical record JSON (without a trailing newline).
// It does not recompute Hash; the record returned by Emit already carries the
// server-computed link.
func (r Record) MarshalJSON() ([]byte, error) {
	for _, value := range []string{r.Timestamp, r.Subject, r.EventID, r.Prev, r.Hash} {
		if !utf8.ValidString(value) {
			return nil, ErrInvalidUTF8
		}
	}
	if err := validateFields(r.Fields); err != nil {
		return nil, err
	}
	return appendHash(encodePayload(r), r.Hash), nil
}

// UnmarshalJSON strictly decodes a canonical audit record and rejects
// duplicate, unknown or missing envelope fields.
func (r *Record) UnmarshalJSON(data []byte) error {
	rec, err := decodeRecord(data)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRecord, err)
	}
	*r = rec
	return nil
}

// Digest identifies a prefix of an audit log by its record count and head.
// Persist it separately from the log and compare it with VerifySealed to detect
// truncation as well as modification.
type Digest struct {
	Records uint64 `json:"records"`
	Head    string `json:"head"`
}

// Equal reports whether two summaries describe the same chain prefix.
func (d Digest) Equal(other Digest) bool {
	return d.Records == other.Records &&
		subtle.ConstantTimeCompare([]byte(d.Head), []byte(other.Head)) == 1
}

// Verification is the result of a successful chain verification.
type Verification struct {
	Records        uint64 `json:"records"`
	Count          uint64 `json:"count"`
	Head           string `json:"head"`
	FirstSeq       uint64 `json:"first_seq"`
	LastSeq        uint64 `json:"last_seq"`
	FirstTimestamp string `json:"first_timestamp,omitempty"`
	LastTimestamp  string `json:"last_timestamp,omitempty"`
}

// Digest returns the verified chain summary.
func (v *Verification) Digest() Digest {
	return Digest{Records: v.Records, Head: v.Head}
}

type config struct {
	now            func() time.Time
	subject        string
	nextEventID    func() (string, error)
	key            []byte
	maxRecordBytes int
}

func defaultConfig() *config {
	return &config{
		now:            time.Now,
		subject:        "system",
		nextEventID:    randomEventID,
		maxRecordBytes: defaultMaxRecordBytes,
	}
}

// Option configures an Encoder or a verifier. Options that only affect
// encoding (clock, subject and ID generator) are ignored by Verify.
type Option func(*config) error

// WithClock supplies the server clock. Callers should not expose this value to
// end users; it exists so the audit service can use its trusted time source and
// so tests can make output deterministic.
func WithClock(now func() time.Time) Option {
	return func(c *config) error {
		if now == nil {
			return errors.New("auditlog: nil clock")
		}
		c.now = now
		return nil
	}
}

// WithNow is an alias for WithClock.
func WithNow(now func() time.Time) Option { return WithClock(now) }

// WithSubject sets the server-authenticated subject for records written with
// Write. The subject must come from the service's authenticated context, not
// from a request body.
func WithSubject(subject string) Option {
	return func(c *config) error {
		c.subject = subject
		return nil
	}
}

// WithEventIDGenerator replaces the default crypto/rand event ID generator.
// Generated IDs are checked for emptiness and uniqueness. Like WithClock, this
// is a server-side trust boundary.
func WithEventIDGenerator(next func() (string, error)) Option {
	return func(c *config) error {
		if next == nil {
			return errors.New("auditlog: nil event ID generator")
		}
		c.nextEventID = next
		return nil
	}
}

// WithIDGenerator is an alias for WithEventIDGenerator.
func WithIDGenerator(next func() (string, error)) Option {
	return WithEventIDGenerator(next)
}

// WithKey enables HMAC-SHA256 chain links instead of plain SHA-256. The key is
// copied. Use the same key when verifying. A keyed chain prevents an attacker
// who can rewrite the log file from recomputing a valid chain.
func WithKey(key []byte) Option {
	return func(c *config) error {
		c.key = append([]byte(nil), key...)
		return nil
	}
}

// WithHashKey is an alias for WithKey.
func WithHashKey(key []byte) Option { return WithKey(key) }

// WithMaxRecordBytes sets the maximum canonical line length (excluding the
// trailing newline). The default is 1 MiB.
func WithMaxRecordBytes(n int) Option {
	return func(c *config) error {
		if n <= 0 {
			return errors.New("auditlog: invalid maximum record size")
		}
		c.maxRecordBytes = n
		return nil
	}
}

// Encoder writes canonical, one-line audit records to a destination. It is
// safe for concurrent use. The writer is not closed by Seal or Close.
type Encoder struct {
	mu     sync.Mutex
	w      io.Writer
	cfg    *config
	seq    uint64
	count  uint64
	prev   string
	ids    map[string]struct{}
	sealed bool
	err    error
}

// Logger and AuditLogger are compatibility aliases for Encoder.
type Logger = Encoder

// AuditLogger is an alias for Encoder.
type AuditLogger = Encoder

// NewEncoder returns an encoder writing to w. w must be non-nil.
func NewEncoder(w io.Writer, opts ...Option) (*Encoder, error) {
	if w == nil {
		return nil, errors.New("auditlog: nil writer")
	}
	cfg := defaultConfig()
	for _, opt := range opts {
		if opt == nil {
			return nil, errors.New("auditlog: nil option")
		}
		if err := opt(cfg); err != nil {
			return nil, err
		}
	}
	return &Encoder{
		w:    w,
		cfg:  cfg,
		prev: Genesis,
		ids:  make(map[string]struct{}),
	}, nil
}

// New is an alias for NewEncoder.
func New(w io.Writer, opts ...Option) (*Encoder, error) {
	return NewEncoder(w, opts...)
}

// NewLogger is an alias for NewEncoder.
func NewLogger(w io.Writer, opts ...Option) (*Encoder, error) {
	return NewEncoder(w, opts...)
}

// NewAuditLogger is an alias for NewEncoder.
func NewAuditLogger(w io.Writer, opts ...Option) (*Encoder, error) {
	return NewEncoder(w, opts...)
}

// Write appends one record using the configured server subject. Fields are
// user-controlled data: they are nested under "fields", never promoted to the
// envelope, and are escaped as canonical JSON strings.
func (e *Encoder) Write(fields map[string]string) error {
	_, err := e.Emit(fields)
	return err
}

// Append is an alias for Write.
func (e *Encoder) Append(fields map[string]string) error { return e.Write(fields) }

// WriteFields is an alias for Write.
func (e *Encoder) WriteFields(fields map[string]string) error { return e.Write(fields) }

// WriteAs appends one record for subject. The caller must obtain subject from
// the service's authenticated context, never directly from user input.
func (e *Encoder) WriteAs(subject string, fields map[string]string) error {
	_, err := e.EmitAs(subject, fields)
	return err
}

// Emit appends a record and returns its server-owned envelope and chain link.
func (e *Encoder) Emit(fields map[string]string) (Record, error) {
	e.mu.Lock()
	subject := e.cfg.subject
	e.mu.Unlock()
	return e.EmitAs(subject, fields)
}

// EmitAs appends a record for the supplied trusted subject.
func (e *Encoder) EmitAs(subject string, fields map[string]string) (Record, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.err != nil {
		return Record{}, e.err
	}
	if e.sealed {
		return Record{}, ErrClosed
	}

	if !utf8.ValidString(subject) {
		return Record{}, fmt.Errorf("%w: subject", ErrInvalidUTF8)
	}
	if err := validateFields(fields); err != nil {
		return Record{}, err
	}

	eventID, err := e.generateEventID()
	if err != nil {
		return Record{}, err
	}

	timestamp := e.cfg.now().UTC().Format(time.RFC3339Nano)
	if !validTimestamp(timestamp) {
		return Record{}, fmt.Errorf("%w: %q", ErrTimestamp, timestamp)
	}

	rec := Record{
		Version:   SchemaVersion,
		Seq:       e.seq + 1,
		Timestamp: timestamp,
		Subject:   subject,
		EventID:   eventID,
		Fields:    cloneFields(fields),
		Prev:      e.prev,
	}

	payload := encodePayload(rec)
	rec.Hash = chainDigest(e.cfg.key, payload)
	line := appendHash(payload, rec.Hash)
	if len(line) > e.cfg.maxRecordBytes {
		return Record{}, fmt.Errorf("%w: %d bytes", ErrRecordTooLarge, len(line))
	}
	line = append(line, '\n')

	if err := writeAll(e.w, line); err != nil {
		e.err = fmt.Errorf("auditlog: write record: %w", err)
		return Record{}, e.err
	}

	e.seq = rec.Seq
	e.count++
	e.prev = rec.Hash
	e.ids[eventID] = struct{}{}
	return rec, nil
}

func (e *Encoder) generateEventID() (string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		id, err := e.cfg.nextEventID()
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrEventID, err)
		}
		if id == "" {
			continue
		}
		if !utf8.ValidString(id) {
			return "", fmt.Errorf("%w: ID is not valid UTF-8", ErrEventID)
		}
		if _, exists := e.ids[id]; !exists {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: duplicate or empty event ID", ErrEventID)
}

// Count returns the number of successfully written records.
func (e *Encoder) Count() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.count
}

// Head returns the current chain head.
func (e *Encoder) Head() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.prev
}

// Summary returns the current record count and chain head.
func (e *Encoder) Summary() Digest {
	e.mu.Lock()
	defer e.mu.Unlock()
	return Digest{Records: e.count, Head: e.prev}
}

// Seal flushes the destination, if it supports Flush or Sync, and returns the
// sealed count and head. No more records may be written afterwards.
func (e *Encoder) Seal() (Digest, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return Digest{}, e.err
	}
	if e.sealed {
		return Digest{Records: e.count, Head: e.prev}, nil
	}
	if err := flushWriter(e.w); err != nil {
		e.err = fmt.Errorf("auditlog: flush destination: %w", err)
		return Digest{}, e.err
	}
	e.sealed = true
	return Digest{Records: e.count, Head: e.prev}, nil
}

// Close seals the encoder. It does not close the underlying writer.
func (e *Encoder) Close() error {
	_, err := e.Seal()
	return err
}

// SealAndVerify seals the encoder and verifies the readable destination
// against the sealed record count and chain head. Use VerifySealed when the
// destination is not readable by Encoder.Verify.
func (e *Encoder) SealAndVerify() (*Verification, error) {
	sealed, err := e.Seal()
	if err != nil {
		return nil, err
	}
	v, err := e.Verify()
	if err != nil {
		return nil, err
	}
	if !v.Digest().Equal(sealed) {
		return nil, fmt.Errorf("%w: sealed digest changed during verification", ErrChainMismatch)
	}
	return v, nil
}

// Verify re-reads a readable destination and checks the full chain against the
// encoder's in-memory count and head. It supports bytes.Buffer, strings.Builder
// and seekable readers such as read-write files.
func (e *Encoder) Verify() (*Verification, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return nil, e.err
	}

	v, err := verifyDestination(e.w, e.cfg)
	if err != nil {
		return nil, err
	}
	if v.Records != e.count {
		return nil, fmt.Errorf("%w: wrote %d records, found %d", ErrCountMismatch, e.count, v.Records)
	}
	if !equalDigest(v.Head, e.prev) {
		return nil, fmt.Errorf("%w: expected head %s, got %s", ErrChainMismatch, e.prev, v.Head)
	}
	return v, nil
}

func verifyDestination(w io.Writer, cfg *config) (*Verification, error) {
	switch src := w.(type) {
	case interface{ Bytes() []byte }:
		return VerifyBytes(src.Bytes(), configOption(cfg))
	case interface{ String() string }:
		return VerifyBytes([]byte(src.String()), configOption(cfg))
	}
	rs, ok := w.(io.ReadSeeker)
	if !ok {
		return nil, ErrUnreadable
	}
	pos, err := rs.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, ErrUnreadable
	}
	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		return nil, ErrUnreadable
	}
	v, verifyErr := verifyReader(rs, cfg)
	if _, err := rs.Seek(pos, io.SeekStart); err != nil && verifyErr == nil {
		return nil, fmt.Errorf("auditlog: restore destination position: %w", err)
	}
	return v, verifyErr
}

func configOption(cfg *config) Option {
	return func(c *config) error {
		*c = *cfg
		return nil
	}
}

// Verify checks a line-oriented audit log: every line must be canonical, the
// sequence must be contiguous, every prev link must match the previous hash,
// and every digest must recompute. It returns the verified count and head.
func Verify(r io.Reader, opts ...Option) (*Verification, error) {
	if r == nil {
		return nil, errors.New("auditlog: nil reader")
	}
	cfg := defaultConfig()
	for _, opt := range opts {
		if opt == nil {
			return nil, errors.New("auditlog: nil option")
		}
		if err := opt(cfg); err != nil {
			return nil, err
		}
	}
	return verifyReader(r, cfg)
}

// VerifyChain is an alias for Verify.
func VerifyChain(r io.Reader, opts ...Option) (*Verification, error) {
	return Verify(r, opts...)
}

// VerifyRecords is an alias for Verify.
func VerifyRecords(r io.Reader, opts ...Option) (*Verification, error) {
	return Verify(r, opts...)
}

// VerifyBytes verifies a log held in memory.
func VerifyBytes(data []byte, opts ...Option) (*Verification, error) {
	return Verify(bytes.NewReader(data), opts...)
}

// VerifyReader is an alias for Verify.
func VerifyReader(r io.Reader, opts ...Option) (*Verification, error) {
	return Verify(r, opts...)
}

// VerifyFile opens and verifies a log file.
func VerifyFile(path string, opts ...Option) (*Verification, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Verify(f, opts...)
}

// VerifyCount verifies the chain and requires exactly expected records. The
// count check catches truncation, which a prefix of a valid chain cannot
// otherwise reveal.
func VerifyCount(r io.Reader, expected uint64, opts ...Option) (*Verification, error) {
	v, err := Verify(r, opts...)
	if err != nil {
		return nil, err
	}
	if v.Records != expected {
		return nil, fmt.Errorf("%w: expected %d, found %d", ErrCountMismatch, expected, v.Records)
	}
	return v, nil
}

// VerifySealed verifies the chain and requires the exact sealed count and head.
func VerifySealed(r io.Reader, want Digest, opts ...Option) (*Verification, error) {
	v, err := Verify(r, opts...)
	if err != nil {
		return nil, err
	}
	if v.Records != want.Records {
		return nil, fmt.Errorf("%w: sealed %d, found %d", ErrCountMismatch, want.Records, v.Records)
	}
	if !equalDigest(v.Head, want.Head) {
		return nil, fmt.Errorf("%w: sealed head %s, found %s", ErrChainMismatch, want.Head, v.Head)
	}
	return v, nil
}

func verifyReader(r io.Reader, cfg *config) (*Verification, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	var (
		count  uint64
		prev   = Genesis
		result = &Verification{Head: Genesis}
		ids    = make(map[string]struct{})
	)

	for {
		line, readErr := readLogLine(br, cfg.maxRecordBytes)
		if errors.Is(readErr, ErrRecordTooLarge) {
			return nil, fmt.Errorf("%w: line %d exceeds %d bytes", ErrRecordTooLarge, count+1, cfg.maxRecordBytes)
		}
		if readErr != nil && readErr != io.EOF {
			return nil, fmt.Errorf("auditlog: read log: %w", readErr)
		}
		if readErr == io.EOF && len(line) == 0 {
			break
		}

		line = bytes.TrimSuffix(line, []byte("\n"))
		if len(line) > cfg.maxRecordBytes {
			return nil, fmt.Errorf("%w: line %d exceeds %d bytes", ErrRecordTooLarge, count+1, cfg.maxRecordBytes)
		}
		if len(line) == 0 {
			return nil, fmt.Errorf("%w: line %d is empty", ErrInvalidRecord, count+1)
		}
		if bytes.HasSuffix(line, []byte("\r")) {
			return nil, fmt.Errorf("%w: line %d uses CRLF", ErrInvalidRecord, count+1)
		}

		rec, err := decodeRecord(line)
		if err != nil {
			return nil, fmt.Errorf("%w: line %d: %v", ErrInvalidRecord, count+1, err)
		}
		if rec.Version != SchemaVersion {
			return nil, fmt.Errorf("%w: line %d: unsupported version %d", ErrInvalidRecord, count+1, rec.Version)
		}
		if rec.Seq != count+1 {
			return nil, fmt.Errorf("%w: line %d: sequence is %d, want %d", ErrChainMismatch, count+1, rec.Seq, count+1)
		}
		if !equalDigest(rec.Prev, prev) {
			return nil, fmt.Errorf("%w: line %d: prev does not match previous head", ErrChainMismatch, count+1)
		}
		if !utf8.ValidString(rec.Timestamp) {
			return nil, fmt.Errorf("%w: line %d", ErrTimestamp, count+1)
		}
		if !validTimestamp(rec.Timestamp) {
			return nil, fmt.Errorf("%w: line %d: %q", ErrTimestamp, count+1, rec.Timestamp)
		}
		if _, exists := ids[rec.EventID]; exists {
			return nil, fmt.Errorf("%w: line %d: duplicate event ID", ErrInvalidRecord, count+1)
		}
		if rec.EventID == "" {
			return nil, fmt.Errorf("%w: line %d: empty event ID", ErrInvalidRecord, count+1)
		}
		ids[rec.EventID] = struct{}{}

		payload := encodePayload(rec)
		wantHash := chainDigest(cfg.key, payload)
		if !equalDigest(rec.Hash, wantHash) {
			return nil, fmt.Errorf("%w: line %d: digest mismatch", ErrChainMismatch, count+1)
		}
		if !bytes.Equal(appendHash(payload, rec.Hash), line) {
			return nil, fmt.Errorf("%w: line %d: record is not canonical", ErrInvalidRecord, count+1)
		}

		if count == 0 {
			result.FirstSeq = rec.Seq
			result.FirstTimestamp = rec.Timestamp
		}
		result.LastSeq = rec.Seq
		result.LastTimestamp = rec.Timestamp
		prev = rec.Hash
		count++

		if readErr == io.EOF {
			break
		}
	}

	result.Records = count
	result.Count = count
	result.Head = prev
	return result, nil
}

func readLogLine(br *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	for {
		fragment, err := br.ReadSlice('\n')
		if len(line)+len(fragment) > max+1 {
			return nil, ErrRecordTooLarge
		}
		line = append(line, fragment...)
		switch err {
		case nil:
			return line, nil
		case bufio.ErrBufferFull:
			continue
		case io.EOF:
			return line, io.EOF
		default:
			return nil, err
		}
	}
}

func decodeRecord(line []byte) (Record, error) {
	dec := json.NewDecoder(bytes.NewReader(line))
	first, err := dec.Token()
	if err != nil {
		return Record{}, err
	}
	if first != json.Delim('{') {
		return Record{}, errors.New("top-level value is not an object")
	}

	rec := Record{Fields: make(map[string]string)}
	seen := make(map[string]struct{}, 8)
	const (
		seenVersion = 1 << iota
		seenSeq
		seenTimestamp
		seenSubject
		seenEventID
		seenFields
		seenPrev
		seenHash
		allFields = (1 << 8) - 1
	)
	var mask uint16

	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return Record{}, err
		}
		key, ok := token.(string)
		if !ok {
			return Record{}, errors.New("object key is not a string")
		}
		if _, duplicate := seen[key]; duplicate {
			return Record{}, fmt.Errorf("duplicate field %q", key)
		}
		seen[key] = struct{}{}

		switch key {
		case "v":
			if err := dec.Decode(&rec.Version); err != nil {
				return Record{}, fmt.Errorf("v: %w", err)
			}
			mask |= seenVersion
		case "seq":
			if err := dec.Decode(&rec.Seq); err != nil {
				return Record{}, fmt.Errorf("seq: %w", err)
			}
			mask |= seenSeq
		case "timestamp":
			if err := dec.Decode(&rec.Timestamp); err != nil {
				return Record{}, fmt.Errorf("timestamp: %w", err)
			}
			mask |= seenTimestamp
		case "subject":
			if err := dec.Decode(&rec.Subject); err != nil {
				return Record{}, fmt.Errorf("subject: %w", err)
			}
			mask |= seenSubject
		case "event_id":
			if err := dec.Decode(&rec.EventID); err != nil {
				return Record{}, fmt.Errorf("event_id: %w", err)
			}
			mask |= seenEventID
		case "fields":
			fields, err := decodeFields(dec)
			if err != nil {
				return Record{}, fmt.Errorf("fields: %w", err)
			}
			rec.Fields = fields
			mask |= seenFields
		case "prev":
			if err := dec.Decode(&rec.Prev); err != nil {
				return Record{}, fmt.Errorf("prev: %w", err)
			}
			mask |= seenPrev
		case "hash":
			if err := dec.Decode(&rec.Hash); err != nil {
				return Record{}, fmt.Errorf("hash: %w", err)
			}
			mask |= seenHash
		default:
			return Record{}, fmt.Errorf("unknown field %q", key)
		}
	}

	last, err := dec.Token()
	if err != nil {
		return Record{}, err
	}
	if last != json.Delim('}') {
		return Record{}, errors.New("record object is not closed")
	}
	if mask != allFields {
		return Record{}, errors.New("record is missing envelope fields")
	}
	if dec.More() {
		return Record{}, errors.New("trailing data after record")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return Record{}, errors.New("trailing data after record")
		}
		return Record{}, err
	}
	return rec, nil
}

func decodeFields(dec *json.Decoder) (map[string]string, error) {
	first, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if first != json.Delim('{') {
		return nil, errors.New("fields is not an object")
	}
	fields := make(map[string]string)
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("field key is not a string")
		}
		if key == "" {
			return nil, errors.New("empty field key")
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, fmt.Errorf("duplicate field key %q", key)
		}
		var value string
		if err := dec.Decode(&value); err != nil {
			return nil, fmt.Errorf("field %q: %w", key, err)
		}
		fields[key] = value
	}
	last, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if last != json.Delim('}') {
		return nil, errors.New("fields object is not closed")
	}
	return fields, nil
}

func validateFields(fields map[string]string) error {
	for key, value := range fields {
		if key == "" {
			return fmt.Errorf("%w: empty field name", ErrInvalidRecord)
		}
		if !utf8.ValidString(key) {
			return fmt.Errorf("%w: field name", ErrInvalidUTF8)
		}
		if !utf8.ValidString(value) {
			return fmt.Errorf("%w: value for field %q", ErrInvalidUTF8, key)
		}
	}
	return nil
}

func cloneFields(fields map[string]string) map[string]string {
	out := make(map[string]string, len(fields))
	for key, value := range fields {
		out[key] = value
	}
	return out
}

func randomEventID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func encodePayload(rec Record) []byte {
	b := make([]byte, 0, 256)
	b = append(b, `{"v":`...)
	b = strconv.AppendInt(b, int64(rec.Version), 10)
	b = append(b, `,"seq":`...)
	b = strconv.AppendUint(b, rec.Seq, 10)
	b = append(b, `,"timestamp":`...)
	b = appendJSONString(b, rec.Timestamp)
	b = append(b, `,"subject":`...)
	b = appendJSONString(b, rec.Subject)
	b = append(b, `,"event_id":`...)
	b = appendJSONString(b, rec.EventID)
	b = append(b, `,"fields":{`...)
	keys := make([]string, 0, len(rec.Fields))
	for key := range rec.Fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i, key := range keys {
		if i > 0 {
			b = append(b, ',')
		}
		b = appendJSONString(b, key)
		b = append(b, ':')
		b = appendJSONString(b, rec.Fields[key])
	}
	b = append(b, `},"prev":`...)
	b = appendJSONString(b, rec.Prev)
	b = append(b, '}')
	return b
}

func appendHash(payload []byte, hash string) []byte {
	b := make([]byte, 0, len(payload)+len(hash)+12)
	b = append(b, payload[:len(payload)-1]...)
	b = append(b, `,"hash":`...)
	b = appendJSONString(b, hash)
	b = append(b, '}')
	return b
}

func appendJSONString(dst []byte, s string) []byte {
	const hexDigits = "0123456789abcdef"
	dst = append(dst, '"')
	for _, r := range s {
		switch r {
		case '"':
			dst = append(dst, '\\', '"')
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			if r < 0x20 || r == 0x7f || r == '\u2028' || r == '\u2029' ||
				unicode.In(r, unicode.Cc, unicode.Cf) {
				if r <= 0xffff {
					dst = append(dst, '\\', 'u',
						hexDigits[(r>>12)&0xf],
						hexDigits[(r>>8)&0xf],
						hexDigits[(r>>4)&0xf],
						hexDigits[r&0xf])
					continue
				}
				r -= 0x10000
				hi := 0xd800 + (r >> 10)
				lo := 0xdc00 + (r & 0x3ff)
				dst = append(dst, '\\', 'u',
					hexDigits[(hi>>12)&0xf],
					hexDigits[(hi>>8)&0xf],
					hexDigits[(hi>>4)&0xf],
					hexDigits[hi&0xf],
					'\\', 'u',
					hexDigits[(lo>>12)&0xf],
					hexDigits[(lo>>8)&0xf],
					hexDigits[(lo>>4)&0xf],
					hexDigits[lo&0xf])
				continue
			}
			dst = append(dst, string(r)...)
		}
	}
	return append(dst, '"')
}

func chainDigest(key, payload []byte) string {
	if len(key) > 0 {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write(payload)
		return hex.EncodeToString(mac.Sum(nil))
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func validTimestamp(value string) bool {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return false
	}
	return t.UTC().Format(time.RFC3339Nano) == value
}

func equalDigest(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func flushWriter(w io.Writer) error {
	if f, ok := w.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	if s, ok := w.(interface{ Sync() error }); ok {
		return s.Sync()
	}
	return nil
}
