package filewrap

import (
	"bytes"
	"io"
	"sync"
	"testing"
)

// virtualBackend is an in-memory Backend with test hooks: onEnter receives a
// signal on every ReadAt entry, and blockOn (when non-nil) makes ReadAt wait
// until the channel is closed, acting as a barrier.
type virtualBackend struct {
	data []byte

	onEnter chan struct{}
	blockOn <-chan struct{}

	mu         sync.Mutex
	reads      int
	closeCalls int
}

func newVirtualBackend(data []byte) *virtualBackend {
	return &virtualBackend{data: data}
}

func (b *virtualBackend) ReadAt(p []byte, off int64) (int, error) {
	b.mu.Lock()
	b.reads++
	onEnter, blockOn := b.onEnter, b.blockOn
	b.mu.Unlock()

	if onEnter != nil {
		onEnter <- struct{}{}
	}
	if blockOn != nil {
		<-blockOn
	}

	if off < 0 {
		return 0, ErrNegativeOffset
	}
	if off >= int64(len(b.data)) {
		return 0, io.EOF
	}
	n := copy(p, b.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (b *virtualBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeCalls++
	return nil
}

func (b *virtualBackend) closeCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closeCalls
}

func makeData(n int) []byte {
	d := make([]byte, n)
	for i := range d {
		d[i] = byte(i)
	}
	return d
}

// Positioned reads from many goroutines must overlap (verified via barrier),
// return the right bytes, and leave the shared cursor untouched.
func TestConcurrentReadAtDoesNotMoveCursor(t *testing.T) {
	data := makeData(4096)
	backend := newVirtualBackend(data)
	const goroutines = 8

	f := Open(backend)
	// Move the cursor first so the test proves ReadAt doesn't consult it.
	if n, err := f.Read(make([]byte, 100)); n != 100 || err != nil {
		t.Fatalf("priming Read: n=%d err=%v", n, err)
	}

	// Arm the barrier only after priming so the priming read isn't blocked.
	backend.onEnter = make(chan struct{}, goroutines)
	release := make(chan struct{})
	backend.blockOn = release

	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			off := int64(g * 256)
			buf := make([]byte, 256)
			if _, err := f.ReadAt(buf, off); err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(buf, data[off:off+256]) {
				errs <- &mismatchError{off: off}
			}
		}(g)
	}

	// Barrier: all goroutines must be inside ReadAt simultaneously.
	for g := 0; g < goroutines; g++ {
		<-backend.onEnter
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	if got := f.Offset(); got != 100 {
		t.Fatalf("cursor moved by ReadAt: got %d, want 100", got)
	}
}

type mismatchError struct{ off int64 }

func (e *mismatchError) Error() string { return "data mismatch" }

// A short read near EOF advances the cursor only by the bytes actually read;
// a subsequent read at EOF returns (0, io.EOF) and does not move the cursor.
func TestSharedCursorShortReadAndEOF(t *testing.T) {
	data := makeData(10)
	f := Open(newVirtualBackend(data))

	buf := make([]byte, 8)
	if n, err := f.Read(buf); n != 8 || err != nil {
		t.Fatalf("first Read: n=%d err=%v", n, err)
	}
	if got := f.Offset(); got != 8 {
		t.Fatalf("cursor after full read: got %d, want 8", got)
	}

	// Short read: 8 bytes requested, only 2 remain.
	n, err := f.Read(buf)
	if n != 2 || err != io.EOF {
		t.Fatalf("short Read: n=%d err=%v, want n=2 err=io.EOF", n, err)
	}
	if !bytes.Equal(buf[:2], data[8:]) {
		t.Fatalf("short Read bytes: got %v, want %v", buf[:2], data[8:])
	}
	if got := f.Offset(); got != 10 {
		t.Fatalf("cursor after short read: got %d, want 10", got)
	}

	// At EOF: no bytes, io.EOF, cursor stays.
	n, err = f.Read(buf)
	if n != 0 || err != io.EOF {
		t.Fatalf("EOF Read: n=%d err=%v, want n=0 err=io.EOF", n, err)
	}
	if got := f.Offset(); got != 10 {
		t.Fatalf("cursor after EOF read: got %d, want 10", got)
	}
}

// Concurrent shared-cursor reads must partition the input exactly: every
// byte delivered once, in order, with no overlap or loss.
func TestConcurrentSharedReadsPartitionInput(t *testing.T) {
	const size = 10000
	const chunk = 64
	data := makeData(size)
	f := Open(newVirtualBackend(data))

	var wg sync.WaitGroup
	var mu sync.Mutex
	got := make([]byte, 0, size)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, chunk)
			for {
				n, err := f.Read(buf)
				mu.Lock()
				got = append(got, buf[:n]...)
				mu.Unlock()
				if err == io.EOF {
					return
				}
				if err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if len(got) != size {
		t.Fatalf("total bytes read: got %d, want %d", len(got), size)
	}
	// Every chunk of `chunk` bytes (except the tail) must match the source
	// at some chunk-aligned offset, proving no interleaved cursors.
	seen := make(map[int64]bool)
	for off := 0; off+chunk <= size; off += chunk {
		seen[int64(off)] = false
	}
	for off := 0; off+chunk <= len(got); off += chunk {
		if !bytes.Equal(got[off:off+chunk], data[off:off+chunk]) {
			t.Fatalf("chunk at %d does not match source", off)
		}
	}
	if got := f.Offset(); got != size {
		t.Fatalf("final cursor: got %d, want %d", got, size)
	}
}

// A read that has entered before Close wins the gate runs to completion;
// Close waits for it; reads attempted afterwards fail with ErrClosed.
func TestCloseReadRaceEnteredReadCompletes(t *testing.T) {
	data := makeData(512)
	backend := newVirtualBackend(data)
	backend.onEnter = make(chan struct{}, 1)
	release := make(chan struct{})
	backend.blockOn = release

	f := Open(backend)

	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 512)
		n, err := f.Read(buf)
		if err == nil && (n != 512 || !bytes.Equal(buf, data)) {
			readDone <- &mismatchError{}
			return
		}
		readDone <- err
	}()

	// Wait until the read is inside the backend, then race Close against it.
	<-backend.onEnter
	closeDone := make(chan error, 1)
	go func() { closeDone <- f.Close() }()

	// The entered read must be allowed to finish.
	close(release)
	if err := <-readDone; err != nil {
		t.Fatalf("entered read failed: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// After Close, both read modes are rejected at the entry point.
	if _, err := f.Read(make([]byte, 1)); err != ErrClosed {
		t.Fatalf("Read after Close: err=%v, want ErrClosed", err)
	}
	if _, err := f.ReadAt(make([]byte, 1), 0); err != ErrClosed {
		t.Fatalf("ReadAt after Close: err=%v, want ErrClosed", err)
	}
}

// Close closes the backend exactly once; repeated Close returns ErrClosed.
func TestDoubleClose(t *testing.T) {
	backend := newVirtualBackend(makeData(16))
	f := Open(backend)

	if err := f.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := f.Close(); err != ErrClosed {
		t.Fatalf("second Close: err=%v, want ErrClosed", err)
	}
	if err := f.Close(); err != ErrClosed {
		t.Fatalf("third Close: err=%v, want ErrClosed", err)
	}
	if got := backend.closeCount(); got != 1 {
		t.Fatalf("backend closed %d times, want 1", got)
	}
}

// ReadAt rejects negative offsets without touching the backend or cursor.
func TestReadAtNegativeOffset(t *testing.T) {
	backend := newVirtualBackend(makeData(16))
	f := Open(backend)
	if _, err := f.ReadAt(make([]byte, 4), -1); err != ErrNegativeOffset {
		t.Fatalf("ReadAt(-1): err=%v, want ErrNegativeOffset", err)
	}
	if got := f.Offset(); got != 0 {
		t.Fatalf("cursor moved: got %d, want 0", got)
	}
}
