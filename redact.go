package redact

import "errors"

// ErrBackpressure is returned by Write when the uncommitted (pending)
// fragment exceeds the limit set by WithMaxPending. The write is rejected
// and nothing is consumed; the caller may retry after the pending fragment
// becomes decidable (more input resolves it) or after forcing a commit
// with Flush.
var ErrBackpressure = errors.New("redact: pending fragment exceeds limit")

// Segment maps one contiguous piece of output back to the input bytes it
// came from. Segments are emitted in order and cover the whole stream.
type Segment struct {
	InputStart  int64 // byte offset in the input stream, inclusive
	InputEnd    int64 // byte offset in the input stream, exclusive
	OutputStart int64 // byte offset in the output stream, inclusive
	OutputEnd   int64 // byte offset in the output stream, exclusive
	EntryID     int   // dictionary entry that produced this segment
	Matched     bool  // false for literal (unredacted) passthrough
}

// bound marks that pending bytes before end belong to dictionary d.
type bound struct {
	end int
	d   *Dictionary
}

// emitOp is one committed unit produced by scan.
type emitOp struct {
	start, end int       // offsets within pending
	term       *terminal // nil means literal passthrough
}

// Redactor is a streaming redactor. Bytes are written with Write, committed
// output is collected via Output, and the input/output correspondence via
// Mapping. A Redactor is not safe for concurrent use.
type Redactor struct {
	current    *Dictionary
	pending    []byte
	bounds     []bound
	out        []byte
	mapping    []Segment
	inOff      int64
	maxPending int
}

// Option configures a Redactor.
type Option func(*Redactor)

// WithMaxPending sets the maximum number of uncommitted bytes the Redactor
// will buffer before Write starts failing with ErrBackpressure. Zero (the
// default) disables the limit.
func WithMaxPending(n int) Option {
	return func(r *Redactor) { r.maxPending = n }
}

// NewRedactor creates a Redactor that starts with the given dictionary.
func NewRedactor(d *Dictionary, opts ...Option) *Redactor {
	r := &Redactor{current: d}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// SetVersion switches the dictionary for subsequently written bytes. The
// change takes effect at an explicit byte boundary: bytes already written
// (including ones still pending) keep the dictionary they arrived under.
func (r *Redactor) SetVersion(d *Dictionary) {
	if d != nil {
		r.current = d
	}
}

// Write appends p to the stream and commits whatever prefix is already
// decidable. It returns ErrBackpressure (consuming nothing) when the
// pending fragment would exceed the configured limit.
func (r *Redactor) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	prevLen := len(r.pending)
	prevBounds := len(r.bounds)
	r.appendPending(p)
	ops, consumed := r.scan(false)
	if r.maxPending > 0 && len(r.pending)-consumed > r.maxPending {
		r.pending = r.pending[:prevLen]
		r.bounds = r.bounds[:prevBounds]
		return 0, ErrBackpressure
	}
	r.apply(ops, consumed)
	return len(p), nil
}

// Flush forces the whole pending fragment to be committed as if the stream
// ended here. The Redactor remains usable for further writes.
func (r *Redactor) Flush() error {
	ops, consumed := r.scan(true)
	r.apply(ops, consumed)
	return nil
}

// Output returns the committed output bytes so far.
func (r *Redactor) Output() []byte { return r.out }

// Mapping returns the output-to-input correspondence for committed output.
func (r *Redactor) Mapping() []Segment { return r.mapping }

// Pending reports how many input bytes are buffered awaiting a decision.
func (r *Redactor) Pending() int { return len(r.pending) }

func (r *Redactor) appendPending(p []byte) {
	r.pending = append(r.pending, p...)
	if n := len(r.bounds); n > 0 && r.bounds[n-1].d == r.current {
		r.bounds[n-1].end += len(p)
	} else {
		r.bounds = append(r.bounds, bound{end: len(r.pending), d: r.current})
	}
}

func (r *Redactor) dictAt(pos int) *Dictionary {
	for _, b := range r.bounds {
		if pos < b.end {
			return b.d
		}
	}
	return r.current
}

// scan walks pending from the left and decides as much as possible. With
// final=false it stops at the first position whose outcome could still be
// changed by future input (the remaining suffix is a proper prefix of some
// dictionary entry). It returns the commit plan and the number of pending
// bytes it covers.
func (r *Redactor) scan(final bool) (ops []emitOp, consumed int) {
	i := 0
	for i < len(r.pending) {
		d := r.dictAt(i)
		cur := d.root
		j := i
		var term *terminal
		termEnd := -1
		for j < len(r.pending) && j-i < d.maxLen {
			next := cur.children[r.pending[j]]
			if next == nil {
				break
			}
			cur = next
			j++
			if cur.term != nil {
				term = cur.term
				termEnd = j
			}
		}
		if !final && j == len(r.pending) && len(cur.children) > 0 {
			// pending[i:] is a proper prefix of some entry: future
			// bytes may still create or extend a match here.
			break
		}
		if term != nil {
			ops = append(ops, emitOp{start: i, end: termEnd, term: term})
			i = termEnd
		} else {
			if n := len(ops); n > 0 && ops[n-1].term == nil && ops[n-1].end == i {
				ops[n-1].end = i + 1
			} else {
				ops = append(ops, emitOp{start: i, end: i + 1})
			}
			i++
		}
	}
	return ops, i
}

// apply emits the commit plan: appends output, records mapping segments,
// and drops the consumed prefix of pending.
func (r *Redactor) apply(ops []emitOp, consumed int) {
	for _, op := range ops {
		inStart := r.inOff + int64(op.start)
		inEnd := r.inOff + int64(op.end)
		outStart := int64(len(r.out))
		if op.term != nil {
			r.out = append(r.out, op.term.replacement...)
			r.mapping = append(r.mapping, Segment{
				InputStart:  inStart,
				InputEnd:    inEnd,
				OutputStart: outStart,
				OutputEnd:   int64(len(r.out)),
				EntryID:     op.term.id,
				Matched:     true,
			})
		} else {
			r.out = append(r.out, r.pending[op.start:op.end]...)
			if n := len(r.mapping); n > 0 && !r.mapping[n-1].Matched &&
				r.mapping[n-1].InputEnd == inStart && r.mapping[n-1].OutputEnd == outStart {
				r.mapping[n-1].InputEnd = inEnd
				r.mapping[n-1].OutputEnd = int64(len(r.out))
			} else {
				r.mapping = append(r.mapping, Segment{
					InputStart:  inStart,
					InputEnd:    inEnd,
					OutputStart: outStart,
					OutputEnd:   int64(len(r.out)),
				})
			}
		}
	}
	r.pending = r.pending[consumed:]
	kept := r.bounds[:0]
	for _, b := range r.bounds {
		if b.end > consumed {
			kept = append(kept, bound{end: b.end - consumed, d: b.d})
		}
	}
	r.bounds = kept
	r.inOff += int64(consumed)
}
