package budget

import (
	"context"
	"io"
	"sync"
)

// sink is the internal staging destination behind the controlled writer. The
// production sink is a plain byte buffer; tests inject faulty sinks to prove
// the engine tolerates short writes.
type sink interface {
	io.Writer
	// bytes returns the fully staged payload.
	bytes() []byte
	// release returns any retained resources to the runtime. It must be
	// idempotent and safe to call after a failed or abandoned decode.
	release()
}

// byteSink is the default staging sink backed by a growable byte slice. Its
// Write always accepts the full slice or returns an error, so under normal
// operation ErrShortWrite cannot originate here.
type byteSink struct {
	buf []byte
}

func (s *byteSink) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)
	return len(p), nil
}

func (s *byteSink) bytes() []byte { return s.buf }

func (s *byteSink) release() { s.buf = nil }

// budgetWriter is the only io.Writer handed to a Decode callback. Each Write is
// validated against the live context and the byte budget before the underlying
// sink is touched. Once an error occurs it is sticky: every later Write fails
// too, which stops hostile callbacks from continuing after a violation.
type budgetWriter struct {
	ctx context.Context
	acc *accountant
	dst sink

	mu  sync.Mutex
	err error
}

// Write reserves the full payload up front. Only after the reservation
// succeeds are bytes pushed to the sink, and any short write is compensated by
// refunding the unaccepted bytes. It never returns a short, nil-error write.
func (w *budgetWriter) Write(p []byte) (int, error) {
	if err := w.failure(); err != nil {
		return 0, err
	}
	if err := w.ctx.Err(); err != nil {
		return 0, w.fail(err)
	}

	n := len(p)
	if err := w.acc.reserve(n); err != nil {
		// Rejected before any byte is written; nothing to undo.
		return 0, w.fail(err)
	}

	written, werr := writeFull(w.dst, p)
	if written < n {
		// Return the reservation for bytes the sink never accepted so the
		// global total reflects real decoded bytes only.
		w.acc.refund(n - written)
	}
	if werr != nil {
		return written, w.fail(werr)
	}
	return written, nil
}

func (w *budgetWriter) fail(err error) error {
	w.mu.Lock()
	if w.err == nil {
		w.err = err
	}
	w.mu.Unlock()
	return err
}

func (w *budgetWriter) failure() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// writeFull pushes all of p into dst, retrying partial writes. A sink that
// makes no forward progress twice in a row is reported as ErrShortWrite.
func writeFull(dst io.Writer, p []byte) (int, error) {
	total := 0
	stuck := 0
	for total < len(p) {
		k, err := dst.Write(p[total:])
		total += k
		if err != nil {
			return total, err
		}
		if k == 0 {
			stuck++
			if stuck >= 2 {
				return total, ErrShortWrite
			}
			continue
		}
		stuck = 0
	}
	return total, nil
}
