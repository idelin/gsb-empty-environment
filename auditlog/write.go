package auditlog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// File is the subset of *os.File used by the writer.
type File interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
	Name() string
	Stat() (os.FileInfo, error)
}

// Opener opens segment files. The default implementation uses os.OpenFile;
// tests inject a fault-simulating opener.
type Opener interface {
	OpenFile(name string, flag int, perm os.FileMode) (File, error)
}

type osOpener struct{}

func (osOpener) OpenFile(name string, flag int, perm os.FileMode) (File, error) {
	return os.OpenFile(name, flag, perm)
}

// Option configures a Writer.
type Option func(*writerConfig)

type writerConfig struct {
	maxRecords int
	opener     Opener
}

// WithMaxSegmentRecords sets the number of records after which Append seals
// the current segment and starts the next one.
func WithMaxSegmentRecords(n int) Option {
	return func(cfg *writerConfig) {
		cfg.maxRecords = n
	}
}

// WithOpener injects a custom segment opener, used for fault injection tests.
func WithOpener(opener Opener) Option {
	return func(cfg *writerConfig) {
		cfg.opener = opener
	}
}

// Writer is a concurrency-safe, append-only audit log writer.
type Writer struct {
	mu      sync.Mutex
	dir     string
	keys    Keys
	cfg     writerConfig
	active  *activeSegment
	nextSeq uint64
	torn    []TornTail
	closed  bool
	failed  bool
}

type activeSegment struct {
	index           uint64
	file            File
	records         int
	firstSeq        uint64
	lastSeq         uint64
	lastDigest      []byte
	firstPrevDigest []byte
	prevSealDigest  []byte
	sealDigest      []byte
	sealed          bool
}

// Open verifies the existing log state and prepares it for appending.
// Tampered logs are rejected without modification. A torn tail at the end of
// an unsealed segment is preserved untouched and appends continue in a fresh
// segment.
func Open(ctx context.Context, dir string, keys Keys, opts ...Option) (*Writer, error) {
	cfg := writerConfig{
		maxRecords: DefaultMaxSegmentRecords,
		opener:     osOpener{},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.maxRecords <= 0 {
		return nil, errors.New("auditlog: max segment records must be positive")
	}
	if err := keys.validate(true); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	report, err := ReadDir(ctx, dir, Keys{HMAC: keys.HMAC, Verify: keys.Verify})
	if err != nil {
		return nil, err
	}

	writer := &Writer{
		dir:     dir,
		keys:    keys,
		cfg:     cfg,
		nextSeq: report.LastSequence + 1,
		torn:    append([]TornTail(nil), report.TornTails...),
	}

	if len(report.Segments) == 0 {
		if err := writer.startSegment(0, genesisDigest[:], genesisDigest[:]); err != nil {
			return nil, err
		}
		return writer, nil
	}

	last := report.Segments[len(report.Segments)-1]
	switch {
	case len(report.TornTails) > 0:
		if err := writer.startSegment(last.Index+1, prevSealForNew(report),
			prevRecordFromReport(report)); err != nil {
			return nil, err
		}
	case last.Sealed:
		if err := writer.startSegment(last.Index+1, last.SealDigest,
			lastRecordForSealed(last)); err != nil {
			return nil, err
		}
	default:
		if err := writer.resumeSegment(last, report); err != nil {
			return nil, err
		}
	}

	return writer, nil
}

func prevSealForNew(report *Report) []byte {
	for i := len(report.Segments) - 1; i >= 0; i-- {
		if report.Segments[i].Sealed {
			return report.Segments[i].SealDigest
		}
	}
	return genesisDigest[:]
}

func prevRecordFromReport(report *Report) []byte {
	if report.LastRecordDigest != nil {
		return report.LastRecordDigest
	}
	return genesisDigest[:]
}

func lastRecordForSealed(status SegmentStatus) []byte {
	if status.LastRecordDigest != nil {
		return status.LastRecordDigest
	}
	return genesisDigest[:]
}

func segmentName(index uint64, part bool) string {
	name := fmt.Sprintf("segment-%016d", index)
	if part {
		return name + openExt
	}
	return name + sealedExt
}

func (w *Writer) startSegment(index uint64, prevSealDigest, prevRecordDigest []byte) error {
	path := filepath.Join(w.dir, segmentName(index, true))
	file, err := w.cfg.opener.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if prevRecordDigest == nil {
		prevRecordDigest = genesisDigest[:]
	}
	w.active = &activeSegment{
		index:           index,
		file:            file,
		lastDigest:      append([]byte(nil), genesisDigest[:]...),
		firstPrevDigest: append([]byte(nil), prevRecordDigest...),
		prevSealDigest:  append([]byte(nil), prevSealDigest...),
	}
	return nil
}

func (w *Writer) resumeSegment(status SegmentStatus, report *Report) error {
	path := filepath.Join(w.dir, segmentName(status.Index, true))
	file, err := w.cfg.opener.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}

	prevSeal := genesisDigest[:]
	if status.Index > 0 {
		prevSeal = report.Segments[len(report.Segments)-2].SealDigest
	}

	w.active = &activeSegment{
		index:           status.Index,
		file:            file,
		records:         status.Records,
		firstSeq:        status.FirstSequence,
		lastSeq:         status.LastSequence,
		lastDigest:      append([]byte(nil), status.LastRecordDigest...),
		firstPrevDigest: append([]byte(nil), status.LastRecordDigest...),
		prevSealDigest:  append([]byte(nil), prevSeal...),
	}
	return nil
}

// TornTails reports torn evidence observed during Open.
func (w *Writer) TornTails() []TornTail {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]TornTail, len(w.torn))
	copy(out, w.torn)
	return out
}

// LastSequence returns the highest durable sequence number.
func (w *Writer) LastSequence() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.nextSeq == 0 {
		return 0
	}
	return w.nextSeq - 1
}
