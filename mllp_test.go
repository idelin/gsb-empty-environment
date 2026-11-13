package netsec

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

type chunkReader struct {
	data      []byte
	sizes     []int
	offset    int
	remaining int
	hasChunk  bool
}

func newChunkReader(data []byte, sizes ...int) *chunkReader {
	return &chunkReader{data: data, sizes: append([]int(nil), sizes...)}
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	for !r.hasChunk && len(r.sizes) > 0 {
		r.remaining = r.sizes[0]
		r.sizes = r.sizes[1:]
		r.hasChunk = r.remaining > 0
	}
	n := len(r.data) - r.offset
	if r.hasChunk && r.remaining < n {
		n = r.remaining
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, r.data[r.offset:r.offset+n])
	r.offset += n
	if r.hasChunk {
		r.remaining -= n
		if r.remaining == 0 {
			r.hasChunk = false
		}
	}
	return n, nil
}

func frame(body []byte) []byte {
	out := make([]byte, 0, len(body)+3)
	out = append(out, StartBlock)
	out = append(out, body...)
	out = append(out, EndBlock, CarriageReturn)
	return out
}

func readAll(t *testing.T, r *Reader) ([][]byte, error) {
	t.Helper()
	var bodies [][]byte
	for {
		body, err := r.ReadFrame()
		if errors.Is(err, io.EOF) {
			return bodies, nil
		}
		if err != nil {
			return bodies, err
		}
		bodies = append(bodies, body)
	}
}

func assertFrames(t *testing.T, got, want [][]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d frames, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("frame %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestReaderAllChunkBoundaries(t *testing.T) {
	first := []byte("MSH|^~\\&|SENDER|A|RECV|B|20261113||ORU^R01|1|P|2.5\rPID|1||12345\rOBX|1|ST|X||hello")
	second := []byte("MSH|^~\\&|OTHER|C|RECV|D|20261113||ADT^A01|2|P|2.5\rPID|1||67890")
	stream := append(frame(first), frame(second)...)
	want := [][]byte{first, second}

	got, err := readAll(t, NewReader(newChunkReader(stream)))
	if err != nil {
		t.Fatalf("whole stream: %v", err)
	}
	assertFrames(t, got, want)

	// Every single cut point, including cuts at the start, between 0x1C and
	// 0x0D, and at the end.
	for cut := 0; cut <= len(stream); cut++ {
		sizes := []int{cut, len(stream) - cut}
		got, err := readAll(t, NewReader(newChunkReader(stream, sizes...)))
		if err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		assertFrames(t, got, want)
	}

	// Enumerate all two-cut partitions as well, so every combination of two
	// chunk boundaries is exercised.
	for firstCut := 0; firstCut <= len(stream); firstCut++ {
		for secondCut := firstCut; secondCut <= len(stream); secondCut++ {
			sizes := []int{firstCut, secondCut - firstCut, len(stream) - secondCut}
			got, err := readAll(t, NewReader(newChunkReader(stream, sizes...)))
			if err != nil {
				t.Fatalf("cuts %d,%d: %v", firstCut, secondCut, err)
			}
			assertFrames(t, got, want)
		}
	}

	// One-byte chunks put every cut directly on the parser.
	got, err = readAll(t, NewReader(newChunkReader(stream, repeatOne(len(stream))...)))
	if err != nil {
		t.Fatalf("one-byte chunks: %v", err)
	}
	assertFrames(t, got, want)
}

func repeatOne(n int) []int {
	sizes := make([]int, n)
	for i := range sizes {
		sizes[i] = 1
	}
	return sizes
}

func TestReaderTerminatorAcrossChunkBoundary(t *testing.T) {
	body := []byte("MSH|^~\\&|A|B|C|D|20261113||ORU^R01|1|P|2.5")
	stream := frame(body)

	// len-1 ends the first chunk on 0x1C; len-2 ends it just before 0x1C.
	for _, cut := range []int{len(stream) - 2, len(stream) - 1} {
		got, err := ReadFrame(newChunkReader(stream, cut, len(stream)-cut))
		if err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		if !bytes.Equal(got, body) {
			t.Fatalf("cut %d: got %q, want %q", cut, got, body)
		}
	}
}

func TestReaderEmbeddedFieldTerminatorSplitsStream(t *testing.T) {
	// An attacker places a complete MLLP terminator inside a field value and
	// appends another segment after it. The reader can only end the first frame
	// at that terminator; the remaining bytes are outside a frame and must be
	// rejected instead of treated as a new message.
	prefix := []byte("MSH|^~\\&|SENDER|A|RECV|B|20261113||ORU^R01|1|P|2.5|evil")
	after := []byte("PID|1||12345")
	stream := make([]byte, 0, len(prefix)+len(after)+8)
	stream = append(stream, StartBlock)
	stream = append(stream, prefix...)
	stream = append(stream, EndBlock, CarriageReturn)
	stream = append(stream, after...)
	stream = append(stream, EndBlock, CarriageReturn)

	r := NewReader(newChunkReader(stream))
	got, err := r.ReadFrame()
	if err != nil {
		t.Fatalf("first frame: %v", err)
	}
	if !bytes.Equal(got, prefix) {
		t.Fatalf("first frame = %q, want %q", got, prefix)
	}
	if _, err := r.ReadFrame(); !errors.Is(err, ErrProtocol) || !errors.Is(err, ErrOutsideFrame) {
		t.Fatalf("second read = %v, want ErrProtocol/ErrOutsideFrame", err)
	}

	// The protocol error is sticky: callers must close the connection.
	if _, err := r.ReadFrame(); !errors.Is(err, ErrProtocol) {
		t.Fatalf("read after fatal error = %v, want ErrProtocol", err)
	}
}

func TestReaderRejectsNestedStartBlock(t *testing.T) {
	stream := []byte{StartBlock}
	stream = append(stream, []byte("MSH|^~\\&|A|B")...)
	stream = append(stream, StartBlock)
	stream = append(stream, []byte("PID|1||12345")...)
	stream = append(stream, EndBlock, CarriageReturn)

	r := NewReader(newChunkReader(stream))
	if _, err := r.ReadFrame(); !errors.Is(err, ErrNestedStart) || !errors.Is(err, ErrProtocol) {
		t.Fatalf("read = %v, want ErrNestedStart/ErrProtocol", err)
	}
	if _, err := r.ReadFrame(); !errors.Is(err, ErrNestedStart) {
		t.Fatalf("read after nested start = %v, want sticky ErrNestedStart", err)
	}
}

func TestReaderInvalidTerminatorDisconnects(t *testing.T) {
	stream := []byte{StartBlock}
	stream = append(stream, []byte("MSH|bad")...)
	stream = append(stream, EndBlock, 'X')
	stream = append(stream, []byte("discarded")...)
	stream = append(stream, StartBlock)
	stream = append(stream, []byte("MSH|good")...)
	stream = append(stream, EndBlock, CarriageReturn)

	r := NewReader(newChunkReader(stream, 3))
	if _, err := r.ReadFrame(); !errors.Is(err, ErrInvalidTerminator) || !errors.Is(err, ErrProtocol) {
		t.Fatalf("first read = %v, want ErrInvalidTerminator/ErrProtocol", err)
	}
	if _, err := r.ReadFrame(); !errors.Is(err, ErrInvalidTerminator) {
		t.Fatalf("read after invalid terminator = %v, want sticky ErrInvalidTerminator", err)
	}
}

func TestReaderInvalidTerminatorFollowedByStartDisconnects(t *testing.T) {
	// 0x1C immediately followed by 0x0B is still an invalid terminator; the
	// stream has lost frame alignment and must not be reused.
	stream := []byte{StartBlock}
	stream = append(stream, []byte("MSH|bad")...)
	stream = append(stream, EndBlock, StartBlock)
	stream = append(stream, []byte("MSH|good")...)
	stream = append(stream, EndBlock, CarriageReturn)

	r := NewReader(newChunkReader(stream, 1))
	if _, err := r.ReadFrame(); !errors.Is(err, ErrInvalidTerminator) || !errors.Is(err, ErrProtocol) {
		t.Fatalf("first read = %v, want ErrInvalidTerminator/ErrProtocol", err)
	}
	if _, err := r.ReadFrame(); !errors.Is(err, ErrInvalidTerminator) {
		t.Fatalf("read after invalid terminator = %v, want sticky ErrInvalidTerminator", err)
	}
}

func TestReaderDiscardsOversizedFrameAndResynchronizes(t *testing.T) {
	tooLong := []byte("MSH|" + "1234567890")
	good := []byte("MSH|1")
	stream := append(frame(tooLong), frame(good)...)

	r := NewReader(newChunkReader(stream, 2), 5)
	if _, err := r.ReadFrame(); !errors.Is(err, ErrFrameTooLong) {
		t.Fatalf("oversized read = %v, want ErrFrameTooLong", err)
	}
	got, err := r.ReadFrame()
	if err != nil {
		t.Fatalf("read after oversized frame: %v", err)
	}
	if !bytes.Equal(got, good) {
		t.Fatalf("recovered body = %q, want %q", got, good)
	}
}

func TestReaderSizeLimitIsExact(t *testing.T) {
	body := []byte("MSH|12345")
	stream := frame(body)
	got, err := NewReader(newChunkReader(stream, 1), len(body)).ReadFrame()
	if err != nil {
		t.Fatalf("exact-limit frame: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("body = %q, want %q", got, body)
	}
}

func TestReaderTruncatedFrame(t *testing.T) {
	for _, stream := range [][]byte{
		{StartBlock, 'A', 'B'},
		{StartBlock, 'A', EndBlock},
	} {
		_, err := NewReader(newChunkReader(stream, 1)).ReadFrame()
		if !errors.Is(err, ErrTruncatedFrame) {
			t.Fatalf("stream %q: read = %v, want ErrTruncatedFrame", stream, err)
		}
	}
}

func TestWriterRejectsForbiddenBytesWithoutWriting(t *testing.T) {
	var tests [][]byte
	for _, forbidden := range []byte{StartBlock, EndBlock} {
		body := append([]byte("MSH|abc"), forbidden)
		tests = append(tests, body)
	}
	// An embedded terminator in a field must be rejected as a control byte even
	// though it would form a syntactically complete frame terminator.
	tests = append(tests,
		[]byte("MSH|abc"+string([]byte{EndBlock, CarriageReturn})+"PID|1"))
	for _, body := range tests {
		var dst bytes.Buffer
		if err := NewWriter(&dst).WriteFrame(body); !errors.Is(err, ErrForbiddenByte) {
			t.Fatalf("WriteFrame(%q) = %v, want ErrForbiddenByte", body, err)
		}
		if dst.Len() != 0 {
			t.Fatalf("rejected frame wrote %d bytes", dst.Len())
		}
	}
}

func TestWriterRejectsMisplacedCarriageReturn(t *testing.T) {
	tests := [][]byte{
		[]byte("MSH|abc\r\rPID|1"),
		[]byte("\rMSH|abc"),
		[]byte("MSH|abc\r\r"),
		[]byte("MSH|abc\rnot-a-segment"),
		[]byte("MSH|abc\rP|1"),
		[]byte("MSH|abc\rpid|1"),
		[]byte("MSH|\rPID|1\r\r"),
	}
	for _, body := range tests {
		if err := ValidateOutboundMessage(body); !errors.Is(err, ErrInvalidSegment) {
			t.Fatalf("ValidateOutboundMessage(%q) = %v, want ErrInvalidSegment", body, err)
		}
	}
}

func TestWriterWritesValidFrameExactly(t *testing.T) {
	body := []byte("MSH|^~\\&|A|B\rPID|1||12345\r")
	if err := ValidateOutboundMessage(body); err != nil {
		t.Fatalf("valid body rejected: %v", err)
	}
	var dst bytes.Buffer
	if err := NewWriter(&dst).WriteFrame(body); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	if !bytes.Equal(dst.Bytes(), frame(body)) {
		t.Fatalf("frame = %q, want %q", dst.Bytes(), frame(body))
	}

	got, err := ReadFrame(bytes.NewReader(dst.Bytes()))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("round-trip body = %q, want %q", got, body)
	}
}

func TestReaderRejectsLeadingBytes(t *testing.T) {
	stream := []byte("garbage")
	stream = append(stream, frame([]byte("MSH|ok"))...)
	r := NewReader(newChunkReader(stream))
	if _, err := r.ReadFrame(); !errors.Is(err, ErrProtocol) || !errors.Is(err, ErrOutsideFrame) {
		t.Fatalf("read = %v, want ErrProtocol/ErrOutsideFrame", err)
	}
	if _, err := r.ReadFrame(); !errors.Is(err, ErrProtocol) {
		t.Fatalf("sticky read = %v, want ErrProtocol", err)
	}
}
