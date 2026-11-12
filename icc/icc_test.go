package icc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"runtime"
	"testing"
)

var testBudget = Budget{
	MaxTags:        64,
	MaxCurvePoints: 4096,
	MaxCLUTBytes:   1 << 20,
}

// maxAllocBytes bounds the heap bytes a single Parse call may
// allocate in these tests; anything larger means attacker-controlled
// sizes drove an allocation.
const maxAllocBytes = 1 << 20

func makeHeader(size uint32) []byte {
	h := make([]byte, HeaderSize)
	binary.BigEndian.PutUint32(h[0:4], size)
	copy(h[36:40], "acsp")
	return h
}

func makeCurv(points uint32) []byte {
	b := make([]byte, curvHeaderSize+int(points)*2)
	copy(b[0:4], "curv")
	binary.BigEndian.PutUint32(b[8:12], points)
	return b
}

func makeMFT2(inCh, outCh, grid uint8, inEntries, outEntries uint16) []byte {
	clut := int64(grid)
	for i := uint8(1); i < inCh; i++ {
		clut *= int64(grid)
	}
	clut *= int64(outCh) * 2
	total := mft2HeaderSize + int(inCh)*int(inEntries)*2 + int(clut) + int(outCh)*int(outEntries)*2
	b := make([]byte, total)
	copy(b[0:4], "mft2")
	b[8], b[9], b[10] = inCh, outCh, grid
	binary.BigEndian.PutUint16(b[48:50], inEntries)
	binary.BigEndian.PutUint16(b[50:52], outEntries)
	return b
}

type tagSpec struct {
	sig  string
	data []byte
}

// buildProfile assembles a profile with tags laid out sequentially.
func buildProfile(tags []tagSpec) []byte {
	size := HeaderSize + 4 + len(tags)*tagEntrySize
	for _, tg := range tags {
		size += len(tg.data)
	}
	buf := makeHeader(uint32(size))
	var table bytes.Buffer
	binary.Write(&table, binary.BigEndian, uint32(len(tags)))
	dataOff := HeaderSize + 4 + len(tags)*tagEntrySize
	for _, tg := range tags {
		table.WriteString(tg.sig)
		binary.Write(&table, binary.BigEndian, uint32(dataOff))
		binary.Write(&table, binary.BigEndian, uint32(len(tg.data)))
		dataOff += len(tg.data)
	}
	buf = append(buf, table.Bytes()...)
	for _, tg := range tags {
		buf = append(buf, tg.data...)
	}
	return buf
}

func parseBytes(data []byte, b Budget) (*Profile, error) {
	return Parse(bytes.NewReader(data), int64(len(data)), b)
}

// measureAlloc returns the heap bytes allocated while running f.
func measureAlloc(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func TestHugeTagCountRejected(t *testing.T) {
	buf := append(makeHeader(132), 0xFF, 0xFF, 0xFF, 0xFF)
	var err error
	alloc := measureAlloc(func() {
		_, err = parseBytes(buf, testBudget)
	})
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("got %v, want ErrBudget", err)
	}
	if alloc > maxAllocBytes {
		t.Fatalf("allocated %d bytes for tag count 0xFFFFFFFF", alloc)
	}
}

func TestOverlappingTagRanges(t *testing.T) {
	// Two tags pointing at the same curv payload: legal per spec and
	// must not cause duplicated or oversized allocations.
	curv := makeCurv(14)
	buf := buildProfile([]tagSpec{{"rTRC", curv}, {"gTRC", curv}})
	// Rewrite the second entry to overlap the first payload.
	firstOff := binary.BigEndian.Uint32(buf[HeaderSize+4+4 : HeaderSize+4+8])
	binary.BigEndian.PutUint32(buf[HeaderSize+4+tagEntrySize+4:], firstOff)

	var p *Profile
	var err error
	alloc := measureAlloc(func() {
		p, err = parseBytes(buf, testBudget)
	})
	if err != nil {
		t.Fatalf("overlapping tags should parse: %v", err)
	}
	if len(p.Tags) != 2 || p.Tags[0].Offset != p.Tags[1].Offset {
		t.Fatalf("unexpected tags: %+v", p.Tags)
	}
	if p.Tags[0].Curve == nil || p.Tags[0].Curve.Points != 14 {
		t.Fatalf("unexpected curve info: %+v", p.Tags[0].Curve)
	}
	if alloc > maxAllocBytes {
		t.Fatalf("allocated %d bytes for overlapping tags", alloc)
	}
}

func TestTagRangeOverflow(t *testing.T) {
	// offset + size wraps uint32; 64-bit checked arithmetic must
	// reject it instead of wrapping to a small in-bounds range.
	buf := buildProfile([]tagSpec{{"rTRC", makeCurv(2)}})
	binary.BigEndian.PutUint32(buf[HeaderSize+4+4:], 0xFFFFFFF0)
	binary.BigEndian.PutUint32(buf[HeaderSize+4+8:], 0x40)
	var err error
	alloc := measureAlloc(func() {
		_, err = parseBytes(buf, testBudget)
	})
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("got %v, want ErrTruncated", err)
	}
	if alloc > maxAllocBytes {
		t.Fatalf("allocated %d bytes for overflowing tag range", alloc)
	}
}

func TestCLUTGridOverflow(t *testing.T) {
	// 255^255 grid cells overflows uint64; must be caught by checked
	// multiplication, not by allocating.
	mft2 := make([]byte, mft2HeaderSize)
	copy(mft2[0:4], "mft2")
	mft2[8], mft2[9], mft2[10] = 255, 255, 255
	buf := buildProfile([]tagSpec{{"A2B0", mft2}})
	var err error
	alloc := measureAlloc(func() {
		_, err = parseBytes(buf, testBudget)
	})
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("got %v, want ErrOverflow", err)
	}
	if alloc > maxAllocBytes {
		t.Fatalf("allocated %d bytes for CLUT grid overflow", alloc)
	}
}

func TestCLUTBudgetEnforced(t *testing.T) {
	// 255^3 * 3 * 2 = ~99 MB CLUT: no overflow, but over budget.
	mft2 := make([]byte, mft2HeaderSize)
	copy(mft2[0:4], "mft2")
	mft2[8], mft2[9], mft2[10] = 3, 3, 255
	buf := buildProfile([]tagSpec{{"A2B0", mft2}})
	var err error
	alloc := measureAlloc(func() {
		_, err = parseBytes(buf, testBudget)
	})
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("got %v, want ErrBudget", err)
	}
	if alloc > maxAllocBytes {
		t.Fatalf("allocated %d bytes for over-budget CLUT", alloc)
	}
}

func TestHugeCurveRejected(t *testing.T) {
	curv := make([]byte, curvHeaderSize)
	copy(curv[0:4], "curv")
	binary.BigEndian.PutUint32(curv[8:12], 0xFFFFFFFF)
	buf := buildProfile([]tagSpec{{"rTRC", curv}})
	var err error
	alloc := measureAlloc(func() {
		_, err = parseBytes(buf, testBudget)
	})
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("got %v, want ErrBudget", err)
	}
	if alloc > maxAllocBytes {
		t.Fatalf("allocated %d bytes for 0xFFFFFFFF curve points", alloc)
	}
}

func TestValidSmallProfile(t *testing.T) {
	// Minimal sRGB-like profile: a curv TRC and a small mft2 LUT.
	mft2 := makeMFT2(3, 3, 2, 256, 256)
	buf := buildProfile([]tagSpec{
		{"rTRC", makeCurv(26)},
		{"A2B0", mft2},
		{"desc", append([]byte("desc"), make([]byte, 20)...)},
	})
	var p *Profile
	var err error
	alloc := measureAlloc(func() {
		p, err = parseBytes(buf, testBudget)
	})
	if err != nil {
		t.Fatalf("valid profile rejected: %v", err)
	}
	if len(p.Tags) != 3 {
		t.Fatalf("got %d tags, want 3", len(p.Tags))
	}
	trc := p.Tags[0]
	if trc.Type != "curv" || trc.Curve == nil || trc.Curve.Points != 26 {
		t.Fatalf("unexpected rTRC: %+v", trc)
	}
	lut := p.Tags[1]
	wantCLUT := uint64(2 * 2 * 2 * 3 * 2) // grid^inCh * outCh * 2 bytes
	if lut.Type != "mft2" || lut.MFT2 == nil || lut.MFT2.CLUTBytes != wantCLUT {
		t.Fatalf("unexpected A2B0: %+v", lut)
	}
	if p.Tags[2].Type != "desc" || p.Tags[2].Curve != nil || p.Tags[2].MFT2 != nil {
		t.Fatalf("unexpected desc: %+v", p.Tags[2])
	}
	if alloc > maxAllocBytes {
		t.Fatalf("allocated %d bytes for small valid profile", alloc)
	}
}

func TestTruncatedInputs(t *testing.T) {
	if _, err := parseBytes(nil, testBudget); !errors.Is(err, ErrTruncated) {
		t.Fatalf("empty input: got %v, want ErrTruncated", err)
	}
	short := makeHeader(HeaderSize + 4) // no tag count present
	if _, err := parseBytes(short[:HeaderSize], testBudget); !errors.Is(err, ErrTruncated) {
		t.Fatalf("missing tag count: got %v, want ErrTruncated", err)
	}
}
