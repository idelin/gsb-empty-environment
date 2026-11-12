package netsec

import (
	"bytes"
	"io"
	"sync"
)

// countingTransport is an in-memory Transport that records every operation.
// It can be configured to return short writes (n < len(p), nil) and to fail
// after a configurable number of bytes, which lets tests exercise the
// partial-write and write-error paths deterministically.
type countingTransport struct {
	mu sync.Mutex

	writeCalls int
	readCalls  int
	closeCalls int

	bytesWritten int
	bytesRead    int

	written  bytes.Buffer
	readData []byte
	readPos  int

	// partialAt caps the number of bytes accepted per Write. Zero means
	// full writes.
	partialAt int
	// failAfter makes Write return writeErr once bytesWritten reaches the
	// threshold. Zero disables failure.
	failAfter int
	writeErr  error
	closeErr  error
}

func (t *countingTransport) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.writeCalls++
	n := len(p)
	if t.writeErr != nil && t.failAfter > 0 {
		remaining := t.failAfter - t.bytesWritten
		if remaining <= 0 {
			return 0, t.writeErr
		}
		if n > remaining {
			n = remaining
		}
	}
	if t.partialAt > 0 && n > t.partialAt {
		n = t.partialAt
	}
	t.written.Write(p[:n])
	t.bytesWritten += n

	if t.writeErr != nil && t.failAfter > 0 && t.bytesWritten >= t.failAfter {
		return n, t.writeErr
	}
	return n, nil
}

func (t *countingTransport) Read(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.readCalls++
	if t.readPos >= len(t.readData) {
		return 0, io.EOF
	}
	n := copy(p, t.readData[t.readPos:])
	t.readPos += n
	t.bytesRead += n
	return n, nil
}

func (t *countingTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closeCalls++
	return t.closeErr
}

type transportSnapshot struct {
	writeCalls   int
	readCalls    int
	closeCalls   int
	bytesWritten int
	bytesRead    int
	payload      []byte
}

func (t *countingTransport) snapshot() transportSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return transportSnapshot{
		writeCalls:   t.writeCalls,
		readCalls:    t.readCalls,
		closeCalls:   t.closeCalls,
		bytesWritten: t.bytesWritten,
		bytesRead:    t.bytesRead,
		payload:      append([]byte(nil), t.written.Bytes()...),
	}
}

// recordingLogger collects LogEntry values under a mutex.
type recordingLogger struct {
	mu      sync.Mutex
	entries []LogEntry
}

func (l *recordingLogger) Log(entry LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, entry)
}

func (l *recordingLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

func (l *recordingLogger) single() LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) != 1 {
		panic("expected exactly one log entry")
	}
	return l.entries[0]
}

// countingCallback records how often the failure callback fires.
type countingCallback struct {
	mu    sync.Mutex
	calls int
	last  PublicError
}

func (c *countingCallback) callback(pub PublicError) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.last = pub
}

func (c *countingCallback) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *countingCallback) single() PublicError {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls != 1 {
		panic("expected exactly one callback invocation")
	}
	return c.last
}
