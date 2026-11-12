package netsec

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

var (
	// ErrBodyTooLarge is returned by a body reader after the configured byte
	// budget has been exhausted.
	ErrBodyTooLarge = errors.New("netsec: request body exceeds configured limit")

	// ErrAuthenticationPending is returned if code reads the request body
	// before the authentication decision is available.
	ErrAuthenticationPending = errors.New("netsec: request body unavailable until authentication completes")
)

type authenticationPendingBody struct{}

func (*authenticationPendingBody) Read([]byte) (int, error) {
	return 0, ErrAuthenticationPending
}

func (*authenticationPendingBody) Close() error {
	// Keep the underlying body intact so the middleware can restore it after
	// authentication; closing it here could drain unread bytes.
	return nil
}

type readGuard struct {
	request  *http.Request
	writer   http.ResponseWriter
	overflow atomic.Bool
}

func (g *readGuard) markOverflow() {
	g.overflow.Store(true)
	if g.request != nil {
		g.request.Close = true
	}
	if g.writer != nil {
		// Stop any later cleanup read immediately. This also makes an overflow
		// that happens after a response header has been committed terminate
		// the connection rather than drain the remaining body.
		_ = http.NewResponseController(g.writer).SetReadDeadline(time.Now())
	}
}

func (g *readGuard) overflowed() bool {
	return g != nil && g.overflow.Load()
}

type budgetBody struct {
	body      io.ReadCloser
	limit     int64
	remaining int64
	guard     *readGuard
	done      bool
}

func (b *budgetBody) Read(p []byte) (int, error) {
	if b.done {
		return 0, b.tooLarge()
	}
	if len(p) == 0 {
		return 0, nil
	}

	if b.remaining == 0 {
		n, err := b.body.Read(p[:1])
		if n > 0 {
			b.done = true
			b.guard.markOverflow()
			return 0, b.tooLarge()
		}
		if err == io.EOF {
			b.done = true
		}
		return 0, err
	}

	if int64(len(p)) > b.remaining {
		p = p[:b.remaining+1]
	}
	n, err := b.body.Read(p)
	if int64(n) <= b.remaining {
		b.remaining -= int64(n)
		if err == io.EOF {
			b.done = true
		}
		return n, err
	}

	allowed := int(b.remaining)
	b.remaining = 0
	b.done = true
	b.guard.markOverflow()
	return allowed, b.tooLarge()
}

func (b *budgetBody) Close() error {
	if b.done && b.guard != nil && b.guard.overflowed() {
		// Do not let net/http's doEarlyClose path drain more bytes after a
		// budget violation. The original body is restored before the handler
		// returns, so net/http will close the connection instead of reusing it.
		return nil
	}
	return b.body.Close()
}

func (b *budgetBody) tooLarge() error {
	return fmt.Errorf("%w (%d bytes)", ErrBodyTooLarge, b.limit)
}
