// Package streamdiff compares two ordered streams of records whose total
// size may exceed available memory, and emits a stream of Added, Deleted
// and Changed events.
//
// Both sources must yield records in strictly ascending key order (keys are
// unique within each source). Values are consumed as chunked byte streams,
// so records are never required to fit in memory.
//
// Value equality is always decided by an exact byte-wise comparison. A weak
// digest (WeakHasher) may only be used as a negative filter: differing weak
// digests prove inequality, equal weak digests prove nothing. A strong
// cryptographic digest (StrongHasher) may short-circuit the byte comparison
// only when the caller explicitly opts into the trust model via
// Options.TrustStrongHash.
//
// The output channel applies backpressure: the differ blocks when the
// consumer falls behind, and the whole operation honours context
// cancellation. Every Event carries a Cursor bound to both source versions,
// which allows a failed diff to be resumed (see Resume) without emitting
// duplicate events.
package streamdiff

import (
	"context"
	"io"
)

// EventType classifies a diff event.
type EventType int

const (
	// Added indicates a key present only in the new source.
	Added EventType = iota
	// Deleted indicates a key present only in the old source.
	Deleted
	// Changed indicates a key present in both sources with differing values.
	Changed
)

func (t EventType) String() string {
	switch t {
	case Added:
		return "added"
	case Deleted:
		return "deleted"
	case Changed:
		return "changed"
	}
	return "unknown"
}

// Record is a single keyed entry. Keys must be non-empty; the empty key is
// reserved as the "beginning of stream" cursor sentinel.
type Record struct {
	Key   string
	Value Value
}

// Value is a chunked, streaming view of a record's value. Implementations
// may additionally expose StrongHasher and/or WeakHasher.
type Value interface {
	io.Reader
}

// StrongHasher optionally exposes a cryptographic digest (e.g. SHA-256) of
// the full value. It is consulted only under the explicit trust model
// (Options.TrustStrongHash).
type StrongHasher interface {
	StrongHash() (hash []byte, ok bool)
}

// WeakHasher optionally exposes a cheap non-cryptographic digest (e.g.
// CRC64, FNV). It is used exclusively as a negative filter: differing weak
// digests conclusively prove the values differ, but equal weak digests are
// never accepted as proof of equality.
type WeakHasher interface {
	WeakHash() (sum uint64, ok bool)
}

// Source is an ordered stream of records over a snapshot identified by
// Version. Next returns io.EOF when the stream is exhausted. Any other
// error is treated as transient: the diff aborts with a *Failure whose
// Cursor allows resumption from a consistent point.
type Source interface {
	Next(ctx context.Context) (Record, error)
	Version() uint64
}

// SeekSource is an optional Source capability: reposition the stream
// strictly after the given key, allowing Resume to avoid re-scanning.
type SeekSource interface {
	Source
	Seek(afterKey string) error
}

// Cursor marks a resumable position in the diff. It is bound to both
// source versions: a cursor is only valid against sources whose Version
// matches OldVersion / NewVersion respectively.
type Cursor struct {
	// AfterKey is the last key for which an event was emitted; resumption
	// continues strictly after it. Empty means the beginning of the stream.
	AfterKey   string
	OldVersion uint64
	NewVersion uint64
}

// Event is a single diff result. Cursor is the resume point immediately
// after this event.
type Event struct {
	Type   EventType
	Key    string
	Cursor Cursor
}

// Failure is delivered on the error channel when a source fails or value
// comparison errors. Cursor is the last consistent resume point: every
// event up to and including Cursor.AfterKey has already been emitted.
type Failure struct {
	Err    error
	Cursor Cursor
}

func (f *Failure) Error() string { return "streamdiff: " + f.Err.Error() }

// Options configures a Diff run.
type Options struct {
	// EmitBuffer is the capacity of the event channel. A small buffer
	// (including zero) applies tight backpressure to the differ.
	EmitBuffer int

	// TrustStrongHash enables the strong-hash trust model: when both
	// values expose a cryptographic digest via StrongHasher, equal digests
	// are accepted as proof of equality and differing digests as proof of
	// inequality, without reading the value bytes.
	//
	// Only enable this when the digest algorithm is collision resistant
	// (e.g. SHA-256) and both sources are trusted to compute it over the
	// exact value bytes. When disabled, or when either side lacks a
	// digest, equality always falls back to byte-wise comparison.
	TrustStrongHash bool
}

// Diff streams the difference between oldSrc and newSrc. It returns an
// event channel and an error channel. The error channel receives exactly
// one value after the event channel closes: nil on success, a *Failure on
// source/value errors, or the context error on cancellation.
func Diff(ctx context.Context, oldSrc, newSrc Source, opts Options) (<-chan Event, <-chan error) {
	if opts.EmitBuffer < 0 {
		opts.EmitBuffer = 0
	}
	out := make(chan Event, opts.EmitBuffer)
	errc := make(chan error, 1)
	go func() {
		defer close(out)
		errc <- run(ctx, oldSrc, newSrc, opts, out)
		close(errc)
	}()
	return out, errc
}

func run(ctx context.Context, oldSrc, newSrc Source, opts Options, out chan<- Event) error {
	cur := Cursor{OldVersion: oldSrc.Version(), NewVersion: newSrc.Version()}

	emit := func(t EventType, key string) error {
		next := cur
		next.AfterKey = key
		select {
		case out <- Event{Type: t, Key: key, Cursor: next}:
			cur = next
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// fail wraps source/value errors in a *Failure carrying the resume
	// cursor. Cancellation is reported plainly instead.
	fail := func(err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Failure{Err: err, Cursor: cur}
	}

	oldRec, oldOK, err := next(ctx, oldSrc)
	if err != nil {
		return fail(err)
	}
	newRec, newOK, err := next(ctx, newSrc)
	if err != nil {
		return fail(err)
	}

	for oldOK || newOK {
		switch {
		case !newOK || (oldOK && oldRec.Key < newRec.Key):
			if err := emit(Deleted, oldRec.Key); err != nil {
				return err
			}
			oldRec, oldOK, err = next(ctx, oldSrc)
		case !oldOK || newRec.Key < oldRec.Key:
			if err := emit(Added, newRec.Key); err != nil {
				return err
			}
			newRec, newOK, err = next(ctx, newSrc)
		default:
			eq, verr := valuesEqual(oldRec.Value, newRec.Value, opts.TrustStrongHash)
			if verr != nil {
				return fail(verr)
			}
			if !eq {
				if err := emit(Changed, oldRec.Key); err != nil {
					return err
				}
			}
			oldRec, oldOK, err = next(ctx, oldSrc)
			if err == nil {
				newRec, newOK, err = next(ctx, newSrc)
			}
		}
		if err != nil {
			return fail(err)
		}
	}
	return nil
}

func next(ctx context.Context, s Source) (Record, bool, error) {
	r, err := s.Next(ctx)
	if err == io.EOF {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	return r, true, nil
}
