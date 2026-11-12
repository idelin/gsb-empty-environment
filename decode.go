package netsec

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// ErrClosed is returned when reading from a closed Reader.
var ErrClosed = errors.New("netsec: reader closed")

// errSinkFull means the caller-provided buffer is full for this Read call. It
// is a soft stop local to one Read, not a resource-limit failure.
var errSinkFull = errors.New("netsec: output buffer full")

// Read implements io.Reader using the context supplied to Open or the most
// recent ReadContext call.
func (r *Reader) Read(p []byte) (int, error) {
	return r.ReadContext(r.ctx, p)
}

// ReadContext fills p with decoded logical bytes while honoring ctx.
//
// On cancellation it returns the bytes already written together with ctx.Err();
// the reader keeps its position and both meters, so a later call resumes after
// the returned prefix without double-charging. An exhausted output or
// expansion budget is terminal and is returned on every subsequent call.
func (r *Reader) ReadContext(ctx context.Context, p []byte) (int, error) {
	if ctx == nil {
		return 0, errors.New("netsec: nil context")
	}
	r.ctx = ctx
	if r.closed {
		return 0, ErrClosed
	}
	if r.err != nil {
		return 0, r.err
	}
	if len(p) == 0 {
		return 0, nil
	}

	n, err := r.fill(ctx, &pSink{p: p})
	switch {
	case errors.Is(err, errSinkFull):
		err = nil
	case errors.Is(err, io.EOF):
		// Keep the io.EOF contract for a fully consumed stream.
	case err != nil && !isContextErr(err):
		r.err = err
	}
	return n, err
}

// Account returns a snapshot of the resources currently charged.
func (r *Reader) Account() Account {
	return Account{
		OutputBytes:  r.outputBytes,
		Expansions:   r.expansions,
		CachedBytes:  r.cachedSize,
		CacheEntries: len(r.cache),
		CacheHits:    r.cacheHits,
	}
}

// Close releases cached blocks and marks the reader unusable.
func (r *Reader) Close() error {
	if r.closed {
		return ErrClosed
	}
	r.closed = true
	r.cache = nil
	r.inflight = nil
	r.stack = nil
	return nil
}

// sink receives decoded bytes. Every accepted byte has already been charged to
// the output meter by chargeOutput.
type sink interface {
	write(r *Reader, data []byte) (int, error)
}

// pSink writes into a caller-provided buffer and is bounded by its length.
type pSink struct{ p []byte }

func (s *pSink) write(r *Reader, data []byte) (int, error) {
	if len(s.p) == 0 {
		return 0, errSinkFull
	}
	want := len(data)
	if avail := len(s.p); want > avail {
		want = avail
	}
	n, err := r.chargeOutput(int64(want))
	copy(s.p[:n], data[:n])
	s.p = s.p[n:]
	return n, err
}

// bufSink accumulates decoded bytes while materializing a dictionary block.
type bufSink struct{ buf []byte }

func (s *bufSink) write(r *Reader, data []byte) (int, error) {
	s.buf = append(s.buf, data...)
	return len(data), nil
}

// chargeOutput charges want bytes against the logical output budget and
// returns the number actually granted. A short grant is accompanied by
// ErrOutputLimit, so the logical cap can never be exceeded, including when the
// bytes are produced for a cached dictionary block.
func (r *Reader) chargeOutput(want int64) (int, error) {
	remaining := r.lim.MaxOutputBytes - r.outputBytes
	if remaining <= 0 {
		return 0, ErrOutputLimit
	}
	n := want
	if n > remaining {
		n = remaining
	}
	r.outputBytes += n
	if n < want {
		return int(n), ErrOutputLimit
	}
	return int(n), nil
}

// fill drives the frame stack until out is satisfied, the stream ends or an
// error occurs. It is shared by caller-facing reads and block materialization.
// The returned count only covers bytes delivered to out; work done to decode
// nested blocks is billed through the meters but not counted here.
func (r *Reader) fill(ctx context.Context, out sink) (int, error) {
	total := 0
	for len(r.stack) > 0 {
		f := r.stack[len(r.stack)-1]

		if f.needAdvance {
			err := r.nextSeg(ctx, f)
			if errors.Is(err, io.EOF) {
				return total, io.EOF
			}
			if err != nil {
				return total, err
			}
		}

		if err := ctx.Err(); err != nil {
			if f.seg >= 0 && f.block.Segments[f.seg].kind == segReference {
				r.rollbackExpansion(f)
			}
			return total, err
		}

		seg := f.block.Segments[f.seg]
		switch seg.kind {
		case segLiteral:
			n, err := out.write(r, seg.data[f.off:f.off+int(f.left)])
			f.off += n
			f.left -= int64(n)
			total += n
			if err != nil {
				return total, err
			}
		case segReference:
			cached, hit := r.cache[seg.ref]
			if !f.charged {
				// Charge the expansion before any work so cancellation never
				// leaves a half-counted reference. Cached reuse is charged too.
				if r.expansions >= r.lim.MaxExpansions {
					return total, ErrExpansionLimit
				}
				r.expansions++
				f.charged = true
				if r.testHook != nil {
					r.testHook(seg.ref)
				}
				if hit {
					r.cacheHits++
				}
			}
			if !hit {
				buf, err := r.materialize(ctx, seg.ref)
				if err != nil {
					if isContextErr(err) {
						r.rollbackExpansion(f)
					}
					return total, err
				}
				cached = buf
			}
			n, err := out.write(r, cached[f.off:f.off+int(f.left)])
			f.off += n
			f.left -= int64(n)
			total += n
			if err != nil {
				return total, err
			}
		default:
			return total, fmt.Errorf("%w: block %d unknown segment kind", ErrBadDecl, f.block.ID)
		}

		if f.left == 0 {
			f.needAdvance = true
		}
	}
	return total, io.EOF
}

// nextSeg advances f to its next non-empty literal or any reference segment.
// Reference segments are charged lazily by fill when they are entered, so a
// zero-length reference is still counted as one expansion.
func (r *Reader) nextSeg(_ context.Context, f *frame) error {
	for f.seg+1 < len(f.block.Segments) {
		f.seg++
		f.off = 0
		f.charged = false
		f.needAdvance = false

		seg := f.block.Segments[f.seg]
		switch seg.kind {
		case segLiteral:
			f.left = int64(len(seg.data))
			if f.left == 0 {
				continue
			}
			return nil
		case segReference:
			f.left = r.sizes[seg.ref]
			return nil
		default:
			return fmt.Errorf("%w: block %d unknown segment kind", ErrBadDecl, f.block.ID)
		}
	}
	return io.EOF
}

// rollbackExpansion reverses an expansion charge whose target could not be
// resolved because of cancellation. The frame stays on the same segment; the
// next fill re-enters it and re-charges the expansion exactly once.
func (r *Reader) rollbackExpansion(f *frame) {
	if f.charged {
		r.expansions--
		f.charged = false
	}
}

// materialize decodes the block identified by id and caches the result.
// Materialization itself does not touch the output meter; every emission of
// the cached bytes goes through a sink and is charged then, so a shared block
// can never bypass the logical output cap.
func (r *Reader) materialize(ctx context.Context, id int64) ([]byte, error) {
	if cached, ok := r.cache[id]; ok {
		return cached, nil
	}
	if r.inflight[id] {
		// Open proved the graph acyclic, so reaching here means an internal
		// invariant violation rather than attacker-controlled input.
		return nil, fmt.Errorf("%w: block %d re-entered while decoding", ErrCycle, id)
	}

	blk, err := r.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if blk == nil || blk.ID != id {
		return nil, fmt.Errorf("%w: block %d", ErrUnknownBlock, id)
	}

	r.inflight[id] = true
	child := &frame{block: blk, seg: -1, needAdvance: true}
	r.stack = append(r.stack, child)
	sink := &bufSink{}
	_, fillErr := r.fill(ctx, sink)
	r.stack = r.stack[:len(r.stack)-1]

	if fillErr != nil && !errors.Is(fillErr, io.EOF) {
		delete(r.inflight, id)
		return nil, fillErr
	}

	buf := sink.buf
	if int64(len(buf)) != blk.Declared {
		delete(r.inflight, id)
		return nil, fmt.Errorf("%w: block %d declares %d, produced %d",
			ErrOverlongDecl, id, blk.Declared, len(buf))
	}
	if r.cachedSize+int64(len(buf)) > r.lim.MaxCacheBytes {
		delete(r.inflight, id)
		return nil, fmt.Errorf("%w: block %d would use %d cached bytes, budget %d",
			ErrCacheLimit, id, r.cachedSize+int64(len(buf)), r.lim.MaxCacheBytes)
	}

	r.cache[id] = buf
	r.cachedSize += int64(len(buf))
	delete(r.inflight, id)
	return buf, nil
}

func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
