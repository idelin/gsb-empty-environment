package framecomp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math/rand"
	"testing"
)

func buildArchive(blocks ...[]byte) []byte {
	var buf []byte
	for _, b := range blocks {
		buf = AppendFrame(buf, b)
	}
	return buf
}

func blockOf(seed int64, size int) []byte {
	rng := rand.New(rand.NewSource(seed))
	words := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"}
	var b bytes.Buffer
	for b.Len() < size {
		b.WriteString(words[rng.Intn(len(words))])
		b.WriteByte(' ')
	}
	return b.Bytes()[:size]
}

func concat(blocks ...[]byte) []byte {
	return bytes.Join(blocks, nil)
}

// frameOffsets returns the offset of each frame header in a clean archive.
func frameOffsets(t *testing.T, archive []byte, n int) []int {
	t.Helper()
	var offs []int
	pos := 0
	for len(offs) < n {
		idx := bytes.Index(archive[pos:], magic[:])
		if idx < 0 {
			t.Fatalf("only %d frames found, want %d", len(offs), n)
		}
		offs = append(offs, pos+idx)
		compLen := binary.LittleEndian.Uint32(archive[pos+idx+6 : pos+idx+10])
		pos += idx + HeaderSize + int(compLen)
	}
	return offs
}

func TestRoundTripMultiFrame(t *testing.T) {
	blocks := [][]byte{blockOf(1, 3000), blockOf(2, 100), blockOf(3, 9000), []byte("x")}
	archive := buildArchive(blocks...)
	out, st, err := DecodeAll(archive, DefaultLimits())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(out, concat(blocks...)) {
		t.Fatal("round trip mismatch")
	}
	if st.Frames != len(blocks) || st.BadCandidates != 0 || st.SkippedBytes != 0 {
		t.Fatalf("unexpected stats: %+v", st)
	}
	if st.OutputBytes != len(concat(blocks...)) {
		t.Fatalf("output bytes = %d, want %d", st.OutputBytes, len(concat(blocks...)))
	}
}

func TestCorruptPayloadSkippedAndRecovered(t *testing.T) {
	blocks := [][]byte{blockOf(1, 4000), blockOf(2, 4000), blockOf(3, 4000)}
	archive := buildArchive(blocks...)
	offs := frameOffsets(t, archive, 3)
	// Flip bytes across the middle frame's payload.
	for i := offs[1] + HeaderSize; i < offs[1]+HeaderSize+200; i++ {
		archive[i] ^= 0xA5
	}
	out, st, err := DecodeAll(archive, DefaultLimits())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Bad frame must emit nothing; frames 1 and 3 fully recovered.
	want := concat(blocks[0], blocks[2])
	if !bytes.Equal(out, want) {
		t.Fatalf("got %d bytes, want %d (frames 1+3 only)", len(out), len(want))
	}
	if bytes.Contains(out, []byte("beta")) && !bytes.Contains(want, []byte("beta")) {
		t.Fatal("output contains bytes from corrupted frame")
	}
	if st.Frames != 2 || st.BadCandidates == 0 {
		t.Fatalf("unexpected stats: %+v", st)
	}
}

func TestCorruptHeaderFakeLengthRecovered(t *testing.T) {
	blocks := [][]byte{blockOf(1, 2000), blockOf(2, 2000), blockOf(3, 2000)}
	archive := buildArchive(blocks...)
	offs := frameOffsets(t, archive, 3)
	// Rewrite middle frame's compLen to a huge (but limit-passing) value.
	binary.LittleEndian.PutUint32(archive[offs[1]+6:offs[1]+10], 4<<20)
	out, st, err := DecodeAll(archive, DefaultLimits())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(out, concat(blocks[0], blocks[2])) {
		t.Fatal("frames 1 and 3 not recovered after fake length header")
	}
	if st.Frames != 2 || st.BadCandidates == 0 {
		t.Fatalf("unexpected stats: %+v", st)
	}
}

func TestFakeLengthRejectedByCompLenCap(t *testing.T) {
	blocks := [][]byte{blockOf(1, 1000), blockOf(2, 1000)}
	archive := buildArchive(blocks...)
	// Plant a fake header claiming compLen just above the cap.
	lim := DefaultLimits()
	lim.MaxFrameCompLen = 1 << 20
	fake := make([]byte, HeaderSize)
	copy(fake, magic[:])
	fake[4] = Version
	fake[5] = MethodLZ
	binary.LittleEndian.PutUint32(fake[6:10], uint32(lim.MaxFrameCompLen+1))
	binary.LittleEndian.PutUint32(fake[10:14], 100)
	archive = append(fake, archive...)
	out, st, err := DecodeAll(archive, lim)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(out, concat(blocks...)) {
		t.Fatal("real frames not recovered after fake header")
	}
	if st.BadCandidates == 0 {
		t.Fatalf("fake header not counted: %+v", st)
	}
}

func TestFakeLengthBoundedByRemainingData(t *testing.T) {
	// Fake header claims a compLen within the cap but far beyond the bytes
	// actually present; decoder must reject it without waiting for data and
	// still recover the real frame that follows.
	blk := blockOf(7, 1500)
	real := buildArchive(blk)
	fake := make([]byte, HeaderSize)
	copy(fake, magic[:])
	fake[4] = Version
	fake[5] = MethodLZ
	binary.LittleEndian.PutUint32(fake[6:10], 1<<20) // 1 MiB claimed
	binary.LittleEndian.PutUint32(fake[10:14], 1<<20)
	stream := append(fake, real...)
	out, st, err := DecodeAll(stream, DefaultLimits())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(out, blk) {
		t.Fatalf("got %d bytes, want the 1500-byte real frame", len(out))
	}
	if st.BadCandidates == 0 {
		t.Fatalf("fake header not rejected: %+v", st)
	}
}

func TestScanBudgetEnforced(t *testing.T) {
	blocks := [][]byte{blockOf(1, 500)}
	archive := buildArchive(blocks...)
	// Prepend garbage larger than the scan budget: sync must fail.
	garbage := bytes.Repeat([]byte{0xEE}, 4096)
	stream := append(garbage, archive...)
	lim := DefaultLimits()
	lim.MaxScanBytes = 1024
	_, _, err := DecodeAll(stream, lim)
	if !errors.Is(err, ErrSyncLost) {
		t.Fatalf("want ErrSyncLost, got %v", err)
	}
	// With a sufficient budget the frame is found again.
	lim.MaxScanBytes = 1 << 20
	out, _, err := DecodeAll(stream, lim)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(out, blocks[0]) {
		t.Fatal("frame not recovered after widening scan budget")
	}
}

func TestTruncatedFrameNeverSucceeds(t *testing.T) {
	blocks := [][]byte{blockOf(1, 3000), blockOf(2, 3000)}
	archive := buildArchive(blocks...)
	offs := frameOffsets(t, archive, 2)
	// Cut the stream at several points inside the second frame.
	for _, cut := range []int{offs[1] + 5, offs[1] + HeaderSize, offs[1] + HeaderSize + 10, len(archive) - 1} {
		out, st, err := DecodeAll(archive[:cut], DefaultLimits())
		if err != nil {
			t.Fatalf("cut=%d: %v", cut, err)
		}
		if !bytes.Equal(out, blocks[0]) {
			t.Fatalf("cut=%d: truncated frame produced output (%d bytes)", cut, len(out)-len(blocks[0]))
		}
		if st.Frames != 1 {
			t.Fatalf("cut=%d: frames=%d, want 1", cut, st.Frames)
		}
	}
}

func TestCRCOnlyOnCompleteOriginal(t *testing.T) {
	blk := blockOf(9, 2048)
	archive := buildArchive(blk)
	// Forge a frame whose payload is a truncated prefix of the real
	// compressed stream but whose CRC matches the full original: the
	// decoder must reject it because decompression cannot complete.
	compLen := binary.LittleEndian.Uint32(archive[6:10])
	trunc := archive[:HeaderSize+int(compLen)/2]
	out, st, err := DecodeAll(trunc, DefaultLimits())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 0 || st.Frames != 0 {
		t.Fatalf("truncated payload decoded: %+v", st)
	}
	// And a single-bit payload corruption must fail the CRC of the
	// complete original.
	archive[HeaderSize+3] ^= 0x01
	out, _, err = DecodeAll(archive, DefaultLimits())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 0 {
		t.Fatal("corrupted frame emitted bytes")
	}
}

func TestCRCFieldIsOfOriginal(t *testing.T) {
	blk := blockOf(5, 1500)
	archive := buildArchive(blk)
	got := binary.LittleEndian.Uint32(archive[14:18])
	if want := crc32.ChecksumIEEE(blk); got != want {
		t.Fatalf("header crc = %08x, want crc32(original) = %08x", got, want)
	}
}

func TestOutputQuotaBounded(t *testing.T) {
	blocks := [][]byte{blockOf(1, 2000), blockOf(2, 2000), blockOf(3, 2000)}
	archive := buildArchive(blocks...)
	lim := DefaultLimits()
	lim.MaxTotalOutput = 2500 // fits frame 1 only
	out, _, err := DecodeAll(archive, lim)
	if !errors.Is(err, ErrOutputQuota) {
		t.Fatalf("want ErrOutputQuota, got %v", err)
	}
	if len(out) > lim.MaxTotalOutput {
		t.Fatalf("emitted %d bytes beyond quota %d", len(out), lim.MaxTotalOutput)
	}
}

func TestGarbageBetweenFrames(t *testing.T) {
	blocks := [][]byte{blockOf(1, 1200), blockOf(2, 1200)}
	f1 := buildArchive(blocks[0])
	f2 := buildArchive(blocks[1])
	garbage := []byte("this is not a frame at all, just noise")
	stream := concat(f1, garbage, f2)
	out, st, err := DecodeAll(stream, DefaultLimits())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(out, concat(blocks...)) {
		t.Fatal("frames around garbage not recovered")
	}
	if st.SkippedBytes != len(garbage) {
		t.Fatalf("skipped = %d, want %d", st.SkippedBytes, len(garbage))
	}
}

func TestUnknownVersionRejected(t *testing.T) {
	blocks := [][]byte{blockOf(1, 800), blockOf(2, 800)}
	archive := buildArchive(blocks...)
	offs := frameOffsets(t, archive, 2)
	archive[offs[0]+4] = Version + 1 // bump first frame's version
	out, st, err := DecodeAll(archive, DefaultLimits())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(out, blocks[1]) {
		t.Fatal("only the valid-version frame should decode")
	}
	if st.BadCandidates == 0 {
		t.Fatalf("bad version not counted: %+v", st)
	}
}

func TestHeaderFieldsPlanned(t *testing.T) {
	blk := blockOf(3, 777)
	archive := buildArchive(blk)
	if !bytes.Equal(archive[:4], magic[:]) {
		t.Fatal("missing magic")
	}
	if archive[4] != Version {
		t.Fatalf("version = %d, want %d", archive[4], Version)
	}
	compLen := binary.LittleEndian.Uint32(archive[6:10])
	origLen := binary.LittleEndian.Uint32(archive[10:14])
	if int(origLen) != len(blk) {
		t.Fatalf("origLen = %d, want %d", origLen, len(blk))
	}
	if int(compLen) != len(archive)-HeaderSize {
		t.Fatalf("compLen = %d, payload is %d", compLen, len(archive)-HeaderSize)
	}
}
