package chunkaead

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
)

// memFile is an in-memory random-access storage backend implementing
// io.ReaderAt, Write and Size.
type memFile struct {
	data []byte
}

func (m *memFile) Write(p []byte) (int, error) {
	m.data = append(m.data, p...)
	return len(p), nil
}

func (m *memFile) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off > int64(len(m.data)) {
		return 0, io.EOF
	}
	n := copy(p, m.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (m *memFile) Size() int64 { return int64(len(m.data)) }

func (m *memFile) clone() *memFile {
	cp := &memFile{data: make([]byte, len(m.data))}
	copy(cp.data, m.data)
	return cp
}

func encryptObject(t *testing.T, key []byte, objectID string, chunkSize int, plain []byte) *memFile {
	t.Helper()
	store := &memFile{}
	w, err := NewWriter(store, key, objectID, chunkSize)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if _, err := w.Write(plain); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return store
}

func makeKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return key
}

func makePlain(n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte(i*31 + 7)
	}
	return p
}

func readFullAt(t *testing.T, r *Reader) []byte {
	t.Helper()
	out := make([]byte, r.Size())
	if len(out) == 0 {
		return out
	}
	n, err := r.ReadAt(out, 0)
	if err != nil {
		t.Fatalf("ReadAt full: %v", err)
	}
	if n != len(out) {
		t.Fatalf("ReadAt full: n=%d want %d", n, len(out))
	}
	return out
}

func TestRoundTripVariousSizes(t *testing.T) {
	key := makeKey(t)
	cases := []int{0, 1, 15, 16, 17, 31, 32, 33, 64, 100, 127, 128, 129, 1000}
	for _, chunkSize := range []int{16, 32, 64} {
		for _, size := range cases {
			name := fmt.Sprintf("chunk%d/size%d", chunkSize, size)
			t.Run(name, func(t *testing.T) {
				plain := makePlain(size)
				store := encryptObject(t, key, "obj/"+name, chunkSize, plain)
				r, err := NewReader(store, key, "obj/"+name)
				if err != nil {
					t.Fatalf("NewReader: %v", err)
				}
				if r.Size() != int64(size) {
					t.Fatalf("Size=%d want %d", r.Size(), size)
				}
				if got := readFullAt(t, r); !bytes.Equal(got, plain) {
					t.Fatalf("plaintext mismatch")
				}
			})
		}
	}
}

func TestEmptyObject(t *testing.T) {
	key := makeKey(t)
	store := encryptObject(t, key, "empty", 32, nil)

	r, err := NewReader(store, key, "empty")
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	if r.Size() != 0 {
		t.Fatalf("Size=%d want 0", r.Size())
	}
	buf := make([]byte, 4)
	if n, err := r.ReadAt(buf, 0); err != io.EOF || n != 0 {
		t.Fatalf("empty ReadAt: n=%d err=%v", n, err)
	}
	// Zero-length read at EOF is allowed to succeed.
	if n, err := r.ReadAt(nil, 0); n != 0 || err != nil {
		t.Fatalf("empty zero ReadAt: n=%d err=%v", n, err)
	}
	if r.numSegs != 0 {
		t.Fatalf("numSegs=%d want 0", r.numSegs)
	}
}

func TestExactChunkSizeHasFinalMarker(t *testing.T) {
	key := makeKey(t)
	const chunkSize = 32
	plain := makePlain(2 * chunkSize) // exactly two full segments
	store := encryptObject(t, key, "exact", chunkSize, plain)

	r, err := NewReader(store, key, "exact")
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	if r.numSegs != 2 {
		t.Fatalf("numSegs=%d want 2", r.numSegs)
	}

	// Swapping the last frame (sealed with final=true) into index 0 must
	// fail authentication because index and final flag are bound in AAD.
	tampered := store.clone()
	last := int64(r.frameSize)
	frame0 := append([]byte(nil), tampered.data[:last]...)
	frame1 := append([]byte(nil), tampered.data[last:2*last]...)
	copy(tampered.data[:last], frame1)
	copy(tampered.data[last:2*last], frame0)

	r2, err := NewReader(tampered, key, "exact")
	if err != nil {
		t.Fatalf("NewReader after reorder: %v", err)
	}
	buf := make([]byte, 2*chunkSize)
	marker := byte(0xAA)
	for i := range buf {
		buf[i] = marker
	}
	n, err := r2.ReadAt(buf, 0)
	if !errors.Is(err, ErrAuthentication) {
		t.Fatalf("reorder ReadAt err=%v want ErrAuthentication", err)
	}
	if n != 0 {
		t.Fatalf("reorder leaked %d bytes", n)
	}
	for i, b := range buf {
		if b != marker {
			t.Fatalf("plaintext leaked at byte %d after auth failure", i)
		}
	}
}

func TestRandomReadsOnlyTouchNeededSegments(t *testing.T) {
	key := makeKey(t)
	const chunkSize = 16
	plain := makePlain(10 * chunkSize) // 10 full segments
	store := encryptObject(t, key, "rand", chunkSize, plain)
	r, err := NewReader(store, key, "rand")
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	ranges := []struct {
		off int64
		n   int
	}{
		{0, 1}, {5, 10}, {15, 2}, {16, 16}, {31, 3}, {100, 50}, {159, 1},
	}
	for _, tc := range ranges {
		buf := make([]byte, tc.n)
		n, err := r.ReadAt(buf, tc.off)
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("ReadAt(%d,%d): %v", tc.off, tc.n, err)
		}
		want := plain[tc.off : tc.off+int64(n)]
		if !bytes.Equal(buf[:n], want) {
			t.Fatalf("ReadAt(%d,%d) mismatch", tc.off, tc.n)
		}
	}

	// Read past EOF: partial data plus io.EOF.
	buf := make([]byte, 10)
	n, err := r.ReadAt(buf, int64(len(plain))-3)
	if !errors.Is(err, io.EOF) || n != 3 {
		t.Fatalf("tail ReadAt: n=%d err=%v", n, err)
	}
	if !bytes.Equal(buf[:3], plain[len(plain)-3:]) {
		t.Fatal("tail plaintext mismatch")
	}
}

func TestTruncationAndExtension(t *testing.T) {
	key := makeKey(t)
	plain := makePlain(100)
	store := encryptObject(t, key, "trim", 16, plain)

	// Cut the tail (final segment and/or the authenticated footer).
	for _, cut := range []int{1, 16, 32, len(store.data) - 1} {
		t.Run(fmt.Sprintf("truncate%d", cut), func(t *testing.T) {
			tampered := store.clone()
			tampered.data = tampered.data[:len(tampered.data)-cut]
			_, err := NewReader(tampered, key, "trim")
			if !errors.Is(err, ErrTruncated) && !errors.Is(err, ErrFormat) {
				t.Fatalf("truncate %d: err=%v want ErrTruncated/ErrFormat", cut, err)
			}
		})
	}

	// Append garbage: authenticated length no longer matches storage size.
	t.Run("extend", func(t *testing.T) {
		tampered := store.clone()
		tampered.data = append(tampered.data, 0xDE, 0xAD, 0xBE, 0xEF)
		if _, err := NewReader(tampered, key, "trim"); !errors.Is(err, ErrTruncated) && !errors.Is(err, ErrFormat) {
			t.Fatalf("extend: err=%v want ErrTruncated/ErrFormat", err)
		}
	})
}

func TestSegmentReorder(t *testing.T) {
	key := makeKey(t)
	const chunkSize = 16
	plain := makePlain(5 * chunkSize)
	store := encryptObject(t, key, "reorder", chunkSize, plain)

	tampered := store.clone()
	frame := int64(chunkSize + tagSize)
	first := append([]byte(nil), tampered.data[:frame]...)
	third := append([]byte(nil), tampered.data[2*frame:3*frame]...)
	copy(tampered.data[:frame], third)
	copy(tampered.data[2*frame:3*frame], first)

	// Footer is intact, so opening succeeds; swapped frames must be
	// rejected when their segments are read.
	r, err := NewReader(tampered, key, "reorder")
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	buf := make([]byte, chunkSize)
	if _, err := r.ReadAt(buf, 0); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("segment 0 err=%v want ErrAuthentication", err)
	}
	// Untouched segments remain independently readable.
	if _, err := r.ReadAt(buf, int64(4*chunkSize)); err != nil {
		t.Fatalf("untouched segment err=%v", err)
	}
}

func TestCrossObjectSplice(t *testing.T) {
	key := makeKey(t)
	const chunkSize = 16
	const size = 4 * chunkSize

	storeA := encryptObject(t, key, "object-A", chunkSize, makePlain(size))
	storeB := encryptObject(t, key, "object-B", chunkSize, makePlain(size))

	// Whole-object substitution: header names a different object ID.
	t.Run("wholeObject", func(t *testing.T) {
		if _, err := NewReader(storeB, key, "object-A"); !errors.Is(err, ErrObjectID) {
			t.Fatalf("whole splice: err=%v want ErrObjectID", err)
		}
	})

	// Frame-level splice: B's frames inside A, keeping A's footer. Sizes
	// match so opening works, but segment AAD (object ID + index) rejects B.
	t.Run("framesOnly", func(t *testing.T) {
		rA, err := NewReader(storeA, key, "object-A")
		if err != nil {
			t.Fatalf("NewReader A: %v", err)
		}
		framesEnd := int64(rA.numSegs) * int64(rA.frameSize)
		tampered := storeA.clone()
		copy(tampered.data[:framesEnd], storeB.data[:framesEnd])

		r, err := NewReader(tampered, key, "object-A")
		if err != nil {
			t.Fatalf("NewReader spliced: %v", err)
		}
		buf := make([]byte, size)
		if _, err := r.ReadAt(buf, 0); !errors.Is(err, ErrAuthentication) {
			t.Fatalf("splice ReadAt: err=%v want ErrAuthentication", err)
		}
	})
}

func TestWrongKeyAndTamperedFrame(t *testing.T) {
	key := makeKey(t)
	plain := makePlain(64)
	store := encryptObject(t, key, "sealed", 16, plain)

	other := makeKey(t)
	if _, err := NewReader(store, other, "sealed"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("wrong key: err=%v want ErrAuthentication", err)
	}

	// Flip one byte in the first frame; no plaintext may reach the caller.
	tampered := store.clone()
	tampered.data[0] ^= 0x01
	r, err := NewReader(tampered, key, "sealed")
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	buf := make([]byte, 16)
	marker := byte(0x77)
	for i := range buf {
		buf[i] = marker
	}
	n, err := r.ReadAt(buf, 0)
	if !errors.Is(err, ErrAuthentication) || n != 0 {
		t.Fatalf("tampered frame: n=%d err=%v", n, err)
	}
	for i, b := range buf {
		if b != marker {
			t.Fatalf("plaintext leaked at byte %d", i)
		}
	}
}

func TestSplitWritesAcrossChunkBoundaries(t *testing.T) {
	key := makeKey(t)
	const chunkSize = 16
	plain := makePlain(100)

	store := &memFile{}
	w, err := NewWriter(store, key, "split", chunkSize)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	// Deliberately awkward write sizes crossing every chunk boundary.
	for pos := 0; pos < len(plain); {
		step := pos%7 + 1
		if pos+step > len(plain) {
			step = len(plain) - pos
		}
		if _, err := w.Write(plain[pos : pos+step]); err != nil {
			t.Fatalf("Write: %v", err)
		}
		pos += step
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("second Close: err=%v want ErrClosed", err)
	}
	if _, err := w.Write([]byte{1}); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after close: err=%v want ErrClosed", err)
	}

	r, err := NewReader(store, key, "split")
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	if got := readFullAt(t, r); !bytes.Equal(got, plain) {
		t.Fatal("split-write plaintext mismatch")
	}
}

func TestCiphertextHidesPlaintext(t *testing.T) {
	key := makeKey(t)
	plain := bytes.Repeat([]byte("TOP-SECRET-PATTERN-123456"), 8)
	store := encryptObject(t, key, "confidential", 16, plain)

	if bytes.Contains(store.data, plain) {
		t.Fatal("full plaintext pattern found in ciphertext")
	}
	if bytes.Contains(store.data, []byte("TOP-SECRET-PATTERN-123456")) {
		t.Fatal("plaintext fragment found in ciphertext")
	}
	if bytes.Contains(store.data, []byte("confidential")) {
		t.Fatal("object ID leaked into stored bytes")
	}
}

func TestSizedAdapterAndNegativeOffset(t *testing.T) {
	key := makeKey(t)
	plain := makePlain(50)
	store := encryptObject(t, key, "adapted", 16, plain)

	// Hide Size behind a bare io.ReaderAt and re-expose it via NewSized.
	var bare io.ReaderAt = readerAtFunc(store.ReadAt)
	r, err := NewReader(NewSized(bare, store.Size()), key, "adapted")
	if err != nil {
		t.Fatalf("NewReader via Sized: %v", err)
	}
	if got := readFullAt(t, r); !bytes.Equal(got, plain) {
		t.Fatal("Sized adapter plaintext mismatch")
	}

	if _, err := r.ReadAt(make([]byte, 1), -1); !errors.Is(err, ErrConfig) {
		t.Fatalf("negative offset: err=%v want ErrConfig", err)
	}
}

type readerAtFunc func(p []byte, off int64) (int, error)

func (f readerAtFunc) ReadAt(p []byte, off int64) (int, error) { return f(p, off) }

func TestConcurrentReadAt(t *testing.T) {
	key := makeKey(t)
	const chunkSize = 16
	plain := makePlain(64 * chunkSize)
	store := encryptObject(t, key, "concurrent", chunkSize, plain)
	r, err := NewReader(store, key, "concurrent")
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	const workers = 16
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				off := int64((seed*7 + i*13) % len(plain))
				n := int((i%5)+1) * 3
				if int(off)+n > len(plain) {
					n = len(plain) - int(off)
				}
				buf := make([]byte, n)
				got, err := r.ReadAt(buf, off)
				if err != nil && !errors.Is(err, io.EOF) {
					errCh <- err
					return
				}
				if !bytes.Equal(buf[:got], plain[off:off+int64(got)]) {
					errCh <- errors.New("concurrent plaintext mismatch")
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}
