package netsec

import (
	"io"
	"sync"
)

const (
	maxReadChunk  = 32 << 10
	streamBuffers = 2 * maxReadChunk
)

type prefetchMode uint8

const (
	prefetchBuffering prefetchMode = iota
	prefetchFlushing
	prefetchForwarding
)

// prefetchQueue owns all reads from the client until forwarding is complete.
// While the admission barriers are closed it stores at most limit bytes. Once
// the successful response has been written, flush drains that bounded prefix
// and the queue switches to ordinary streaming.
type prefetchQueue struct {
	mu       sync.Mutex
	cond     *sync.Cond
	chunks   [][]byte
	buffered int
	limit    int
	mode     prefetchMode
	closed   bool
	eof      bool
	err      error
}

func newPrefetchQueue(limit int) *prefetchQueue {
	if limit <= 0 {
		limit = DefaultMaxEarlyData
	}
	q := &prefetchQueue{limit: limit}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// readFrom is the only client reader. It stops reading at the pre-authorization
// limit, which applies backpressure to a client that sends early tunnel data.
func (q *prefetchQueue) readFrom(r io.Reader) {
	buf := make([]byte, maxReadChunk)
	for {
		budget, ok := q.readBudget()
		if !ok {
			return
		}
		if budget > len(buf) {
			budget = len(buf)
		}
		n, err := r.Read(buf[:budget])
		if n > 0 {
			if !q.append(buf[:n]) {
				return
			}
		}
		if err != nil {
			q.finish(err)
			return
		}
	}
}

func (q *prefetchQueue) readBudget() (int, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for {
		if q.closed {
			return 0, false
		}
		if q.mode == prefetchFlushing {
			q.cond.Wait()
			continue
		}
		if q.buffered < q.limit {
			budget := q.limit - q.buffered
			if budget > maxReadChunk {
				budget = maxReadChunk
			}
			return budget, true
		}
		q.cond.Wait()
	}
}

func (q *prefetchQueue) append(p []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for q.mode == prefetchFlushing && !q.closed {
		q.cond.Wait()
	}
	if q.closed {
		return false
	}
	chunk := make([]byte, len(p))
	copy(chunk, p)
	q.chunks = append(q.chunks, chunk)
	q.buffered += len(chunk)
	q.cond.Broadcast()
	return true
}

// flush writes the bounded early-data prefix and only then opens streaming.
// The reader is held in prefetchFlushing, so the prefix cannot be reordered
// with data that arrives later.
func (q *prefetchQueue) flush(w io.Writer) error {
	q.mu.Lock()
	for q.mode == prefetchFlushing && !q.closed {
		q.cond.Wait()
	}
	if q.closed {
		q.mu.Unlock()
		return ErrClosed
	}
	chunks := q.chunks
	q.chunks = nil
	q.buffered = 0
	q.mode = prefetchFlushing
	q.mu.Unlock()

	var writeErr error
	for _, chunk := range chunks {
		if err := writeAll(w, chunk); err != nil {
			writeErr = err
			break
		}
	}

	q.mu.Lock()
	if writeErr != nil {
		q.closed = true
		q.err = writeErr
	} else {
		q.mode = prefetchForwarding
		if q.limit < streamBuffers {
			q.limit = streamBuffers
		}
	}
	q.cond.Broadcast()
	q.mu.Unlock()
	return writeErr
}

func (q *prefetchQueue) next() ([]byte, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.chunks) == 0 && !q.eof && !q.closed {
		q.cond.Wait()
	}
	if q.closed {
		if q.err != nil {
			return nil, q.err
		}
		return nil, ErrClosed
	}
	if len(q.chunks) == 0 {
		if q.err != nil {
			return nil, q.err
		}
		return nil, io.EOF
	}
	chunk := q.chunks[0]
	q.chunks[0] = nil
	q.chunks = q.chunks[1:]
	q.buffered -= len(chunk)
	q.cond.Broadcast()
	return chunk, nil
}

// forward consumes the queue in order. It returns nil only for a clean EOF.
func (q *prefetchQueue) forward(w io.Writer) error {
	for {
		chunk, err := q.next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := writeAll(w, chunk); err != nil {
			return err
		}
	}
}

func (q *prefetchQueue) finish(err error) {
	q.mu.Lock()
	if err == io.EOF {
		err = nil
	}
	if q.err == nil {
		q.err = err
	}
	q.eof = true
	q.cond.Broadcast()
	q.mu.Unlock()
}

func (q *prefetchQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.chunks = nil
	q.buffered = 0
	q.cond.Broadcast()
	q.mu.Unlock()
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			if len(p) == 0 {
				return nil
			}
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
