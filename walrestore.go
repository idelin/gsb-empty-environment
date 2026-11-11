// Package walrestore recovers the latest committed snapshot of a mobile
// synchronization log from a write-ahead log (WAL).
//
// The on-disk log is a sequence of fixed-size blocks. Every block contains one
// or more fragments; a logical record may span several fragments and therefore
// several blocks. Each fragment carries a little-endian header with its payload
// length, fragment type and a CRC-32 checksum. The unused remainder of a block
// is zero padding.
//
// Records are committed batches of key/value mutations. Each batch has a
// strictly increasing sequence number and contains puts together with delete
// markers. A batch is only published to consumers once its complete,
// checksum-verified record has been decoded; a partial record never reaches the
// recovered store.
//
// A CRC or length mismatch inside the final physical block is treated as a
// torn tail: recovery truncates at the start of the torn block and keeps every
// fully committed batch preceding it. The same mismatch in any earlier block is
// real mid-log corruption and reports ErrCorruption instead of silently losing
// committed data.
//
// Consumers read recovered state through immutable Views pinned to a sequence
// number; the backing store keeps every value snapshot so historical views
// remain valid after newer batches are observed.
package walrestore

import (
	"context"
	"errors"
	"io"
	"os"
)

// DefaultBlockSize is the block size used when none is configured.
const DefaultBlockSize = 32 * 1024

// Sentinel errors returned by the package.
var (
	// ErrCorruption indicates a structural or checksum failure at or before the
	// final physical block, where truncation cannot hide committed data.
	ErrCorruption = errors.New("walrestore: corrupt log")
	// ErrTruncated indicates the final block contained an incomplete record.
	// The committed prefix remains usable; Store.TruncationError reports detail.
	ErrTruncated = errors.New("walrestore: torn tail truncated")
	// ErrSequenceNotFound is returned when no committed batch has the requested
	// sequence number (or a prefix sequence when no batch exists yet).
	ErrSequenceNotFound = errors.New("walrestore: sequence not found")
	// ErrNotFound is returned for a key that is absent or tombstoned in a view.
	ErrNotFound = errors.New("walrestore: key not found")
)

// Op identifies the kind of mutation a single entry performs.
type Op uint8

const (
	// OpPut stores or replaces a value.
	OpPut Op = 1
	// OpDelete records a tombstone. The associated value is empty.
	OpDelete Op = 2
)

// Entry is one mutation inside a batch.
type Entry struct {
	Key   []byte
	Value []byte
	Op    Op
}

// Batch is a fully recovered, committed unit of work.
type Batch struct {
	// Seq is the strictly increasing batch sequence number (the first batch is 1).
	Seq uint64
	// Entries are the mutations applied atomically by this batch.
	Entries []Entry
}

// version is one historical value of a key.
type version struct {
	seq     uint64
	value   []byte
	deleted bool
}

// Store is the recovered state. It is safe for concurrent reads: every method
// is read-only once construction finishes and every returned slice is an
// independent copy.
type Store struct {
	// keys is the sorted list of every key ever observed.
	keys [][]byte
	// history maps a key index to its versions, ordered by ascending sequence.
	history [][]version
	// seqs is the ascending list of committed batch sequence numbers.
	seqs []uint64
	// maxSeq is the sequence number of the newest committed batch (0 if empty).
	maxSeq uint64

	truncated   bool
	truncatedAt int64
	truncErr    error
}

// Open recovers a WAL file and returns the committed store.
//
// Recovery is interruptible through ctx: if ctx is cancelled before every
// complete batch has been published, Open returns the context error and no
// partial store is produced. A torn tail truncates cleanly and is observable on
// the returned store; mid-log corruption surfaces as ErrCorruption.
func Open(ctx context.Context, path string, opts ...Option) (*Store, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return Recover(ctx, f, fi.Size(), opts...)
}

// Recover reads a WAL of the given byte size through r and returns the
// committed store. r must support io.ReaderAt. It is the streaming, testable
// core used by Open; ownership of r stays with the caller.
func Recover(ctx context.Context, r io.ReaderAt, size int64, opts ...Option) (*Store, error) {
	if size < 0 {
		return nil, errors.New("walrestore: negative log size")
	}
	cfg, err := buildConfig(opts)
	if err != nil {
		return nil, err
	}

	iter := newIterator(r, size, cfg)
	store := &Store{}

	index := make(map[string]int)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := iter.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if errors.Is(err, ErrTruncated) {
				store.truncated = true
				store.truncatedAt = iter.truncatedAt
				store.truncErr = iter.truncErr
				break
			}
			return nil, err
		}
		store.publish(batch, index)
	}

	store.compact()
	return store, nil
}

// publish applies one fully committed batch to the in-progress store.
func (s *Store) publish(batch *Batch, index map[string]int) {
	s.seqs = append(s.seqs, batch.Seq)
	s.maxSeq = batch.Seq

	for _, entry := range batch.Entries {
		key := string(entry.Key)
		idx, ok := index[key]
		if !ok {
			idx = len(s.keys)
			index[key] = idx
			s.keys = append(s.keys, append([]byte(nil), entry.Key...))
			s.history = append(s.history, nil)
		}

		ver := version{seq: batch.Seq, deleted: entry.Op == OpDelete}
		if !ver.deleted {
			ver.value = append([]byte(nil), entry.Value...)
		}
		s.history[idx] = append(s.history[idx], ver)
	}
}

// compact sorts the key index and reorders histories to match it.
func (s *Store) compact() {
	order := make([]int, len(s.keys))
	for i := range order {
		order[i] = i
	}
	sortIndices(order, s.keys)

	sortedKeys := make([][]byte, len(s.keys))
	sortedHistory := make([][]version, len(s.history))
	for newPos, oldPos := range order {
		sortedKeys[newPos] = s.keys[oldPos]
		sortedHistory[newPos] = s.history[oldPos]
	}
	s.keys = sortedKeys
	s.history = sortedHistory
}

// MaxSeq returns the sequence number of the newest committed batch, or zero for
// an empty log.
func (s *Store) MaxSeq() uint64 { return s.maxSeq }

// Sequences returns a copy of the committed batch sequence numbers in order.
func (s *Store) Sequences() []uint64 {
	out := make([]uint64, len(s.seqs))
	copy(out, s.seqs)
	return out
}

// Truncated reports whether recovery discarded a torn tail.
func (s *Store) Truncated() bool { return s.truncated }

// TruncatedOffset returns the byte offset where a torn tail was cut, or -1 when
// the log was not truncated.
func (s *Store) TruncatedOffset() int64 {
	if !s.truncated {
		return -1
	}
	return s.truncatedAt
}

// TruncationError returns the wrapped torn-tail error, or nil when the log was
// not truncated.
func (s *Store) TruncationError() error { return s.truncErr }

// View returns an immutable view of the batch with the exact sequence number
// seq. It returns ErrSequenceNotFound when no committed batch has that number.
func (s *Store) View(seq uint64) (*View, error) {
	if !containsSeq(s.seqs, seq) {
		return nil, ErrSequenceNotFound
	}
	return &View{store: s, seq: seq}, nil
}

// ViewAt returns an immutable view containing every committed batch whose
// sequence number is at most seq. It returns ErrSequenceNotFound when no batch
// qualifies. Deleted keys are hidden unless a later put resurrects them.
func (s *Store) ViewAt(seq uint64) (*View, error) {
	if len(s.seqs) == 0 || s.seqs[0] > seq {
		return nil, ErrSequenceNotFound
	}
	return &View{store: s, seq: seq}, nil
}

// Latest returns an immutable view over the newest committed batch.
func (s *Store) Latest() *View {
	return &View{store: s, seq: s.maxSeq}
}

// View is an immutable, point-in-time read-only snapshot.
type View struct {
	store *Store
	seq   uint64
}

// Seq returns the sequence number the view is pinned to.
func (v *View) Seq() uint64 { return v.seq }

// Get returns a copy of the value for key. A key that never existed, or that a
// delete marker removed without a later resurrection, returns ErrNotFound.
func (v *View) Get(key []byte) ([]byte, error) {
	idx, found := lookupKey(v.store.keys, key)
	if !found {
		return nil, ErrNotFound
	}
	ver, ok := v.store.versionAt(idx, v.seq)
	if !ok || ver.deleted {
		return nil, ErrNotFound
	}
	return append([]byte(nil), ver.value...), nil
}

// Has reports whether key holds a live (non-deleted) value in the view.
func (v *View) Has(key []byte) bool {
	idx, found := lookupKey(v.store.keys, key)
	if !found {
		return false
	}
	ver, ok := v.store.versionAt(idx, v.seq)
	return ok && !ver.deleted
}

// versionAt returns the latest version of key index idx not newer than seq.
func (s *Store) versionAt(idx int, seq uint64) (version, bool) {
	hist := s.history[idx]
	found := false
	var best version
	for _, ver := range hist {
		if ver.seq > seq {
			break
		}
		best = ver
		found = true
	}
	return best, found
}

func containsSeq(seqs []uint64, seq uint64) bool {
	for _, candidate := range seqs {
		if candidate == seq {
			return true
		}
		if candidate > seq {
			break
		}
	}
	return false
}

// Option configures recovery.
type Option func(*config)

type config struct {
	blockSize int
}

// WithBlockSize overrides the physical block size. It must be larger than one
// fragment header.
func WithBlockSize(size int) Option {
	return func(c *config) { c.blockSize = size }
}

func buildConfig(opts []Option) (config, error) {
	cfg := config{blockSize: DefaultBlockSize}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.blockSize <= fragmentHeaderSize {
		return config{}, errors.New("walrestore: block size too small")
	}
	return cfg, nil
}
