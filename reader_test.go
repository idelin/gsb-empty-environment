package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"testing"
)

const (
	testHeaderSize = 12
	testBlockSize  = 8
	testHeaderKey  = uint32(0x9E3779B9)
	testBodyXor    = byte(0x5A)
)

var (
	errBadHeader = errors.New("bad header tag")
	errBadBody   = errors.New("bad body tag")
)

// fakeTransform is a deterministic stand-in for an authenticated cipher:
// headers carry a length plus a keyed checksum, bodies are XOR protected in
// place. It records nothing across frames, mirroring a stateless AEAD.
type fakeTransform struct {
	headerSize int
	blockSize  int
	headerFail bool
	bodyFail   bool
	frameSeq   int
	failBodyOn map[int]bool
}

func (f *fakeTransform) HeaderSize() int { return f.headerSize }

func (f *fakeTransform) BlockSize() int { return f.blockSize }

func (f *fakeTransform) OpenHeader(protected []byte) (uint32, error) {
	if f.headerFail {
		return 0, errBadHeader
	}
	length := binary.BigEndian.Uint32(protected[:4])
	checksum := binary.BigEndian.Uint32(protected[4:8])
	if checksum != length^testHeaderKey {
		return 0, errBadHeader
	}
	return length, nil
}

func (f *fakeTransform) OpenBody(dst, header, protected []byte) error {
	seq := f.frameSeq
	f.frameSeq++
	if f.bodyFail || f.failBodyOn[seq] {
		return errBadBody
	}
	for i := range protected {
		dst[i] = protected[i] ^ testBodyXor
	}
	return nil
}

func sealHeader(length uint32) []byte {
	buf := make([]byte, testHeaderSize)
	binary.BigEndian.PutUint32(buf[:4], length)
	binary.BigEndian.PutUint32(buf[4:8], length^testHeaderKey)
	return buf
}

func sealBody(plain []byte) []byte {
	protected := make([]byte, len(plain))
	for i, b := range plain {
		protected[i] = b ^ testBodyXor
	}
	return protected
}

func frame(plain []byte) []byte {
	var buf bytes.Buffer
	buf.Write(sealHeader(uint32(len(plain))))
	buf.Write(sealBody(plain))
	return buf.Bytes()
}

// alignedFrame pads plaintext with zero bytes so that header plus body length
// is a multiple of testBlockSize, returning the padded plaintext for asserts.
func alignedFrame(plain []byte) []byte {
	n := len(plain)
	if rem := (testHeaderSize + n) % testBlockSize; rem != 0 {
		n += testBlockSize - rem
	}
	padded := make([]byte, n)
	copy(padded, plain)
	var buf bytes.Buffer
	buf.Write(sealHeader(uint32(n)))
	buf.Write(sealBody(padded))
	return buf.Bytes()
}

// alignedPlain returns the block-aligned form of plaintext used by
// alignedFrame.
func alignedPlain(plain []byte) []byte {
	n := len(plain)
	if rem := (testHeaderSize + n) % testBlockSize; rem != 0 {
		n += testBlockSize - rem
	}
	padded := make([]byte, n)
	copy(padded, plain)
	return padded
}

// cyclicReader replays the same bytes forever, so a rejection path can be
// exercised repeatedly without ever supplying a body.
type cyclicReader struct {
	data []byte
	pos  int
}

func (c *cyclicReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = c.data[c.pos%len(c.data)]
		c.pos++
	}
	return len(p), nil
}

type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	m, err := c.r.Read(p)
	c.n += m
	return m, err
}

func newFake() *fakeTransform {
	return &fakeTransform{headerSize: testHeaderSize, blockSize: testBlockSize}
}

func TestReadPacketDecryptsBoundedBody(t *testing.T) {
	plain := []byte("hello authenticated world")
	want := alignedPlain(plain)
	rd := NewReader(bytes.NewReader(alignedFrame(plain)), newFake())

	got, err := rd.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if cap(got) != len(want) {
		t.Fatalf("body cap = %d, want exactly %d (no over-allocation)", cap(got), len(want))
	}
}

func TestHugeDeclaredLengthAllocatesNothingAndRejects(t *testing.T) {
	tr := newFake()
	rd := NewReader(&cyclicReader{data: sealHeader(math.MaxUint32)}, tr)

	allocs := testing.AllocsPerRun(16, func() {
		pkt, err := rd.ReadPacket()
		if !errors.Is(err, ErrDeclaredLength) {
			t.Fatalf("err = %v, want ErrDeclaredLength", err)
		}
		if pkt != nil {
			t.Fatalf("packet = %v, want nil on rejection", pkt)
		}
	})
	if allocs != 0 {
		t.Fatalf("rejection path made %v allocations, want 0", allocs)
	}
}

func TestCustomMaxBodyIsEnforced(t *testing.T) {
	// 36 is block aligned (12+36 = 48, a multiple of 8) but above the cap.
	tr := newFake()
	rd := NewReader(&cyclicReader{data: sealHeader(36)}, tr, WithMaxBody(32))

	_, err := rd.ReadPacket()
	if !errors.Is(err, ErrDeclaredLength) {
		t.Fatalf("err = %v, want ErrDeclaredLength", err)
	}
	if rd.maxBody != 32 {
		t.Fatalf("maxBody = %d, want 32", rd.maxBody)
	}
}

func TestMisalignedLengthRejectsWithoutReadingBody(t *testing.T) {
	tr := newFake()
	src := &countingReader{r: bytes.NewReader(sealHeader(64))} // 12+64 not divisible by 8
	rd := NewReader(src, tr)

	pkt, err := rd.ReadPacket()
	if !errors.Is(err, ErrAlignment) {
		t.Fatalf("err = %v, want ErrAlignment", err)
	}
	if pkt != nil {
		t.Fatalf("packet = %v, want nil", pkt)
	}
	if src.n != testHeaderSize {
		t.Fatalf("read %d bytes, want only the %d-byte header", src.n, testHeaderSize)
	}
}

func TestTruncatedHeaderAndBodyDeliverNothing(t *testing.T) {
	tr := newFake()

	shortHeader := make([]byte, testHeaderSize-3)
	if _, err := NewReader(bytes.NewReader(shortHeader), tr).ReadPacket(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated header err = %v, want ErrUnexpectedEOF", err)
	}

	var partial bytes.Buffer
	partial.Write(sealHeader(20))   // 12+20 = 32, block aligned
	partial.Write(make([]byte, 10)) // 10 bytes short of the declared body
	if _, err := NewReader(&partial, newFake()).ReadPacket(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated body err = %v, want ErrUnexpectedEOF", err)
	}
}

func TestCleanEOF(t *testing.T) {
	if _, err := NewReader(bytes.NewReader(nil), newFake()).ReadPacket(); !errors.Is(err, io.EOF) {
		t.Fatalf("empty stream err = %v, want io.EOF", err)
	}
}

func TestHeaderAuthFailureDeliversNothing(t *testing.T) {
	tr := newFake()
	tr.headerFail = true
	pkt, err := NewReader(bytes.NewReader(sealHeader(8)), tr).ReadPacket()
	if !errors.Is(err, ErrHeaderAuth) {
		t.Fatalf("err = %v, want ErrHeaderAuth", err)
	}
	if pkt != nil {
		t.Fatalf("packet = %v, want nil", pkt)
	}
}

func TestBodyAuthFailureDiscardsBufferAndNextFrameStillDelivered(t *testing.T) {
	first := []byte("first packet payload")
	third := []byte("third packet payload")

	var stream bytes.Buffer
	stream.Write(alignedFrame(first))
	wantFirst := alignedPlain(first)
	wantThird := alignedPlain(third)

	stream.Write(alignedFrame([]byte("doomed packet payload")))
	stream.Write(alignedFrame(third))

	tr := newFake()
	tr.failBodyOn = map[int]bool{1: true} // frame 0 ok, frame 1 fails auth, frame 2 ok
	rd := NewReader(&stream, tr)

	gotFirst, err := rd.ReadPacket()
	if err != nil || !bytes.Equal(gotFirst, wantFirst) {
		t.Fatalf("first packet = %q, %v", gotFirst, err)
	}

	// The bad frame's full body is read but must fail authentication and be
	// dropped rather than cached for later redelivery.
	if pkt, err := rd.ReadPacket(); !errors.Is(err, ErrBodyAuth) || pkt != nil {
		t.Fatalf("bad body = %v, %v; want nil, ErrBodyAuth", pkt, err)
	}

	gotThird, err := rd.ReadPacket()
	if err != nil {
		t.Fatalf("third ReadPacket: %v", err)
	}
	if !bytes.Equal(gotThird, wantThird) {
		t.Fatalf("third packet = %q, want %q", gotThird, wantThird)
	}
	if bytes.Equal(gotThird, gotFirst) {
		t.Fatal("stale first packet redelivered after body auth failure")
	}
}

func TestTruncatedBodyDoesNotRedeliverOldBuffer(t *testing.T) {
	first := []byte("established packet")
	next := []byte("recovered packet")
	wantFirst := alignedPlain(first)
	wantNext := alignedPlain(next)

	var stream bytes.Buffer
	stream.Write(alignedFrame(first))
	stream.Write(sealHeader(20))  // 12+20 = 32, block aligned
	stream.Write(make([]byte, 7)) // truncated middle frame

	rd := NewReader(&stream, newFake())

	gotFirst, err := rd.ReadPacket()
	if err != nil || !bytes.Equal(gotFirst, wantFirst) {
		t.Fatalf("first packet = %q, %v", gotFirst, err)
	}
	if _, err := rd.ReadPacket(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated frame err = %v, want ErrUnexpectedEOF", err)
	}

	// Stream framing is broken by the truncation, so simulate a fresh Reader on
	// the remaining well-formed frame and confirm no stale buffer is handed out.
	recovered := NewReader(bytes.NewReader(alignedFrame(next)), newFake())
	gotNext, err := recovered.ReadPacket()
	if err != nil || !bytes.Equal(gotNext, wantNext) {
		t.Fatalf("recovered packet = %q, %v", gotNext, err)
	}
	if bytes.Equal(gotNext, gotFirst) {
		t.Fatal("stale buffer redelivered after truncation")
	}
}

func TestHeaderBufferIsFixedAndReused(t *testing.T) {
	tr := newFake()
	rd := NewReader(bytes.NewReader(append(alignedFrame([]byte("one")), alignedFrame([]byte("two"))...)), tr)

	if len(rd.header) != testHeaderSize {
		t.Fatalf("header buffer len = %d, want %d", len(rd.header), testHeaderSize)
	}
	addr := &rd.header[0]

	if _, err := rd.ReadPacket(); err != nil {
		t.Fatalf("first ReadPacket: %v", err)
	}
	if _, err := rd.ReadPacket(); err != nil {
		t.Fatalf("second ReadPacket: %v", err)
	}
	if &rd.header[0] != addr {
		t.Fatal("header buffer was reallocated instead of reused")
	}
}

func TestZeroLengthBodyWithStreamTransform(t *testing.T) {
	tr := &fakeTransform{headerSize: testHeaderSize, blockSize: 1}
	rd := NewReader(bytes.NewReader(frame(nil)), tr)

	got, err := rd.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("body len = %d, want 0", len(got))
	}
}

func TestNewReaderValidation(t *testing.T) {
	tr := newFake()

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic on nil reader")
			}
		}()
		NewReader(nil, tr)
	}()

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic on nil transform")
			}
		}()
		NewReader(bytes.NewReader(nil), nil)
	}()

	rd := NewReader(bytes.NewReader(nil), tr, WithMaxBody(-5))
	if rd.maxBody != DefaultMaxBodyBytes {
		t.Fatalf("negative max override changed maxBody to %d", rd.maxBody)
	}
}
