package filewrap

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"
)

// fakeBackend is an in-memory virtual backend. It records call counts
// and supports an entry hook so tests can install barriers inside a
// read that has entered the backend.
type fakeBackend struct {
	data []byte

	// hook, if non-nil, runs at the top of every ReadAt call.
	hook func(off int64, n int)
	// readAt, if non-nil, replaces the default slice-based read.
	readAt func(p []byte, off int64) (int, error)

	mu          sync.Mutex
	readAtCalls int
	closeCalls  int
}

func (b *fakeBackend) ReadAt(p []byte, off int64) (int, error) {
	b.mu.Lock()
	b.readAtCalls++
	hook := b.hook
	readAt := b.readAt
	b.mu.Unlock()

	if hook != nil {
		hook(off, len(p))
	}
	if readAt != nil {
		return readAt(p, off)
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

func (b *fakeBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeCalls++
	return nil
}

func (b *fakeBackend) reads() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.readAtCalls
}

func (b *fakeBackend) closes() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closeCalls
}

func TestSharedCursorReadsAdvanceSequentially(t *testing.T) {
	f := New(&fakeBackend{data: []byte("abcdefgh")})

	buf := make([]byte, 3)
	if n, err := f.Read(buf); n != 3 || err != nil || string(buf) != "abc" {
		t.Fatalf("first read = %d %q %v, want 3 \"abc\" nil", n, buf, err)
	}
	if n, err := f.Read(buf); n != 3 || err != nil || string(buf) != "def" {
		t.Fatalf("second read = %d %q %v, want 3 \"def\" nil", n, buf, err)
	}
	if got := f.Offset(); got != 6 {
		t.Fatalf("offset = %d, want 6", got)
	}
}

func TestReadAtDoesNotAffectCursor(t *testing.T) {
	f := New(&fakeBackend{data: []byte("0123456789")})

	buf := make([]byte, 4)
	if n, err := f.Read(buf); n != 4 || err != nil || string(buf) != "0123" {
		t.Fatalf("cursor read = %d %q %v, want 4 \"0123\" nil", n, buf, err)
	}
	// Positioned reads elsewhere must leave the cursor alone.
	for _, off := range []int64{8, 0, 5} {
		if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
			t.Fatalf("ReadAt(%d): %v", off, err)
		}
	}
	if got := f.Offset(); got != 4 {
		t.Fatalf("offset after ReadAt = %d, want 4", got)
	}
	if n, err := f.Read(buf); n != 4 || err != nil || string(buf) != "4567" {
		t.Fatalf("cursor read after ReadAt = %d %q %v, want 4 \"4567\" nil", n, buf, err)
	}
}

func TestShortReadAdvancesOnlyActualBytes(t *testing.T) {
	var b *fakeBackend
	b = &fakeBackend{
		data: []byte("abcdef"),
		readAt: func(p []byte, off int64) (int, error) {
			// Always deliver at most 2 bytes, with a nil error, to
			// force a genuine short read.
			n := 2
			if rem := len(b.data) - int(off); rem < n {
				n = rem
			}
			return copy(p[:n], b.data[off:]), nil
		},
	}
	f := New(b)

	buf := make([]byte, 8)
	if n, err := f.Read(buf); n != 2 || err != nil {
		t.Fatalf("short read = %d, %v; want 2, nil", n, err)
	}
	if got := f.Offset(); got != 2 {
		t.Fatalf("offset after short read = %d, want 2", got)
	}
	if n, err := f.Read(buf); n != 2 || err != nil || string(buf[:n]) != "cd" {
		t.Fatalf("next read = %d %q %v, want 2 \"cd\" nil", n, buf[:n], err)
	}
}

func TestEOF(t *testing.T) {
	f := New(&fakeBackend{data: []byte("hello")})

	buf := make([]byte, 8)
	n, err := f.Read(buf)
	if n != 5 || err != io.EOF || string(buf[:n]) != "hello" {
		t.Fatalf("read past end = %d %q %v, want 5 \"hello\" EOF", n, buf[:n], err)
	}
	if got := f.Offset(); got != 5 {
		t.Fatalf("offset at EOF = %d, want 5", got)
	}
	// Cursor sits at end: further reads deliver nothing.
	if n, err := f.Read(buf); n != 0 || err != io.EOF {
		t.Fatalf("read at EOF = %d, %v; want 0, EOF", n, err)
	}
	if got := f.Offset(); got != 5 {
		t.Fatalf("offset moved at EOF: %d, want 5", got)
	}
	// Positioned reads observe the same EOF boundary.
	if n, err := f.ReadAt(buf, 5); n != 0 || err != io.EOF {
		t.Fatalf("ReadAt at end = %d, %v; want 0, EOF", n, err)
	}
	if n, err := f.ReadAt(buf, 3); n != 2 || err != io.EOF || string(buf[:n]) != "lo" {
		t.Fatalf("ReadAt short tail = %d %q %v, want 2 \"lo\" EOF", n, buf[:n], err)
	}
}

// TestConcurrentPositionedReads hammers the file with positioned reads
// and shared-cursor reads at the same time. Positioned reads must see
// correct bytes, and every cursor read must be full (a lost cursor
// update would surface as an early short read or EOF).
func TestConcurrentPositionedReads(t *testing.T) {
	const size = 4096
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i*31 + 7)
	}
	f := New(&fakeBackend{data: data})

	start := make(chan struct{})
	errs := make(chan error, 64)
	var wg sync.WaitGroup

	// Positioned readers: disjoint windows, verified by content.
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			buf := make([]byte, 128)
			off := int64(w * 128)
			if _, err := f.ReadAt(buf, off); err != nil {
				errs <- fmt.Errorf("ReadAt(%d): %v", off, err)
				return
			}
			if !bytes.Equal(buf, data[off:off+128]) {
				errs <- fmt.Errorf("ReadAt(%d): wrong bytes", off)
			}
		}(w)
	}

	// Cursor readers: exactly tile the file in 32-byte chunks. Any
	// cursor race (overlap or lost advance) makes some read short.
	const chunk = 32
	const reads = size / chunk
	var cwg sync.WaitGroup
	results := make(chan int, reads)
	for g := 0; g < 8; g++ {
		cwg.Add(1)
		go func() {
			defer cwg.Done()
			<-start
			buf := make([]byte, chunk)
			for i := 0; i < reads/8; i++ {
				n, err := f.Read(buf)
				if err != nil {
					errs <- fmt.Errorf("cursor read: n=%d err=%v", n, err)
					return
				}
				results <- n
			}
		}()
	}

	close(start)
	wg.Wait()
	cwg.Wait()
	close(results)
	close(errs)

	total := 0
	for n := range results {
		if n != chunk {
			t.Fatalf("short cursor read under concurrency: %d", n)
		}
		total += n
	}
	if total != size {
		t.Fatalf("cursor delivered %d bytes, want %d", total, size)
	}
	for err := range errs {
		t.Fatal(err)
	}
	if got := f.Offset(); got != size {
		t.Fatalf("final offset = %d, want %d", got, size)
	}
}

// TestCloseWaitsForEnteredRead installs a barrier inside the backend:
// a read that has entered must complete even though Close is pending,
// and Close must not return before it does.
func TestCloseWaitsForEnteredRead(t *testing.T) {
	data := []byte("0123456789abcdef")
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once

	b := &fakeBackend{data: data}
	b.hook = func(off int64, n int) {
		once.Do(func() {
			close(entered)
			<-release
		})
	}
	f := New(b)

	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 8)
		n, err := f.Read(buf)
		if err != nil {
			readDone <- fmt.Errorf("entered read failed: %v", err)
			return
		}
		if string(buf[:n]) != "01234567" {
			readDone <- fmt.Errorf("entered read got %q", buf[:n])
			return
		}
		readDone <- nil
	}()

	<-entered // the read is inside the backend

	closeDone := make(chan error, 1)
	go func() { closeDone <- f.Close() }()

	select {
	case <-closeDone:
		t.Fatal("Close returned while an entered read was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(release) // let the entered read finish
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := f.Offset(); got != 8 {
		t.Fatalf("offset = %d, want 8", got)
	}
}

func TestOperationsAfterClose(t *testing.T) {
	b := &fakeBackend{data: []byte("abc")}
	f := New(b)
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	readsBefore := b.reads()

	if _, err := f.Read(make([]byte, 1)); err != ErrClosed {
		t.Fatalf("Read after Close = %v, want ErrClosed", err)
	}
	if _, err := f.ReadAt(make([]byte, 1), 0); err != ErrClosed {
		t.Fatalf("ReadAt after Close = %v, want ErrClosed", err)
	}
	if got := b.reads(); got != readsBefore {
		t.Fatalf("backend received %d reads after Close", got-readsBefore)
	}
}

func TestRepeatedClose(t *testing.T) {
	b := &fakeBackend{data: []byte("abc")}
	f := New(b)

	if err := f.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := f.Close(); err != ErrClosed {
		t.Fatalf("second Close = %v, want ErrClosed", err)
	}
	if got := b.closes(); got != 1 {
		t.Fatalf("backend closed %d times, want 1", got)
	}
}

// TestConcurrentCloseExactlyOnce races many closers; exactly one may
// win and the backend must be closed exactly once.
func TestConcurrentCloseExactlyOnce(t *testing.T) {
	b := &fakeBackend{data: []byte("abc")}
	f := New(b)

	const closers = 8
	start := make(chan struct{})
	results := make(chan error, closers)
	var wg sync.WaitGroup
	for i := 0; i < closers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- f.Close()
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	ok := 0
	for err := range results {
		switch err {
		case nil:
			ok++
		case ErrClosed:
		default:
			t.Fatalf("unexpected Close error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d closers succeeded, want exactly 1", ok)
	}
	if got := b.closes(); got != 1 {
		t.Fatalf("backend closed %d times, want 1", got)
	}
}
