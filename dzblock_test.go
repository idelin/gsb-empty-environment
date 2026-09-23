package dzblock

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"testing"
)

var testDict = []byte(`{"level":"info","msg":"request completed","status":200,"path":"/api/v1/users"}
{"level":"warn","msg":"request slow","status":200,"path":"/api/v1/orders"}
{"level":"error","msg":"request failed","status":500,"path":"/api/v1/users"}
`)

// makeBlock builds a deterministic block that shares plenty of
// substrings with the dictionary.
func makeBlock(seed int64, lines int) []byte {
	r := rand.New(rand.NewSource(seed))
	var b bytes.Buffer
	for i := 0; i < lines; i++ {
		switch r.Intn(3) {
		case 0:
			b.WriteString(`{"level":"info","msg":"request completed","status":200,"path":"/api/v1/users"}`)
		case 1:
			b.WriteString(`{"level":"warn","msg":"request slow","status":200,"path":"/api/v1/orders"}`)
		default:
			b.WriteString(`{"level":"error","msg":"request failed","status":500,"path":"/api/v1/users"}`)
		}
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func buildStream(t *testing.T, dict []byte, blocks [][]byte) []byte {
	t.Helper()
	stream := AppendStreamHeader(nil, dict)
	var err error
	for _, b := range blocks {
		stream, err = AppendBlock(stream, dict, b)
		if err != nil {
			t.Fatalf("AppendBlock: %v", err)
		}
	}
	return stream
}

func readAll(t *testing.T, stream, dict []byte, maxOut int64) [][]byte {
	t.Helper()
	rd, err := NewReader(bytes.NewReader(stream), dict, maxOut)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	var out [][]byte
	for {
		b, err := rd.ReadBlock()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("ReadBlock: %v", err)
		}
		out = append(out, b)
	}
}

func TestRoundTrip(t *testing.T) {
	blocks := [][]byte{
		makeBlock(1, 100),
		makeBlock(2, 500),
		makeBlock(3, 1),                 // tiny block
		bytes.Repeat([]byte("a"), 4096), // highly compressible
		{},                              // empty block
	}
	// Incompressible tail block.
	rnd := make([]byte, 3000)
	rand.New(rand.NewSource(9)).Read(rnd)
	blocks = append(blocks, rnd)

	stream := buildStream(t, testDict, blocks)
	got := readAll(t, stream, testDict, 1<<30)
	if len(got) != len(blocks) {
		t.Fatalf("got %d blocks, want %d", len(got), len(blocks))
	}
	for i := range blocks {
		if !bytes.Equal(got[i], blocks[i]) {
			t.Fatalf("block %d mismatch: got %d bytes, want %d", i, len(got[i]), len(blocks[i]))
		}
	}
}

func TestEmptyDictionary(t *testing.T) {
	blocks := [][]byte{makeBlock(4, 200), make([]byte, 0)}
	stream := buildStream(t, nil, blocks)
	got := readAll(t, stream, nil, 1<<30)
	for i := range blocks {
		if !bytes.Equal(got[i], blocks[i]) {
			t.Fatalf("block %d mismatch with empty dictionary", i)
		}
	}
}

func TestDictionarySharedAcrossBlocks(t *testing.T) {
	// A block identical to dictionary content should compress far
	// better with the dictionary than without it.
	block := bytes.Repeat(testDict, 4)
	withDict, err := AppendBlock(nil, testDict, block)
	if err != nil {
		t.Fatal(err)
	}
	withoutDict, err := AppendBlock(nil, nil, block)
	if err != nil {
		t.Fatal(err)
	}
	if len(withDict) >= len(withoutDict) {
		t.Fatalf("dictionary did not help: with=%d without=%d", len(withDict), len(withoutDict))
	}
}

func TestWrongDictionaryRejected(t *testing.T) {
	blocks := [][]byte{makeBlock(5, 50)}
	stream := buildStream(t, testDict, blocks)

	// Stream-level rejection.
	if _, err := NewReader(bytes.NewReader(stream), []byte("wrong dict"), 1<<30); !errors.Is(err, ErrDictMismatch) {
		t.Fatalf("NewReader with wrong dict: got %v, want ErrDictMismatch", err)
	}

	// Block-level rejection: decode a single block with the wrong dict.
	hdr, err := ParseBlockHeader(stream[StreamHeaderSize:])
	if err != nil {
		t.Fatal(err)
	}
	payload := stream[StreamHeaderSize+BlockHeaderSize : StreamHeaderSize+BlockHeaderSize+int(hdr.EncLen)]
	if _, err := DecodeBlock([]byte("wrong dict"), hdr, payload, 1<<20); !errors.Is(err, ErrDictMismatch) {
		t.Fatalf("DecodeBlock with wrong dict: got %v, want ErrDictMismatch", err)
	}
}

func TestBlockSizeLimit(t *testing.T) {
	if _, err := AppendBlock(nil, nil, make([]byte, MaxBlockRaw+1)); !errors.Is(err, ErrBlockTooLarge) {
		t.Fatalf("oversize block: got %v, want ErrBlockTooLarge", err)
	}
	if _, err := AppendBlock(nil, nil, make([]byte, MaxBlockRaw)); err != nil {
		t.Fatalf("max-size block rejected: %v", err)
	}
}

func TestOutputLimit(t *testing.T) {
	blocks := [][]byte{makeBlock(6, 100), makeBlock(7, 100)}
	stream := buildStream(t, testDict, blocks)
	total := int64(len(blocks[0]) + len(blocks[1]))

	rd, err := NewReader(bytes.NewReader(stream), testDict, total-1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rd.ReadBlock(); err != nil { // first block fits
		t.Fatalf("first block: %v", err)
	}
	if _, err := rd.ReadBlock(); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("second block: got %v, want ErrOutputLimit", err)
	}

	// DecodeBlock honours its per-call limit too.
	hdr, _ := ParseBlockHeader(stream[StreamHeaderSize:])
	payload := stream[StreamHeaderSize+BlockHeaderSize : StreamHeaderSize+BlockHeaderSize+int(hdr.EncLen)]
	if _, err := DecodeBlock(testDict, hdr, payload, len(blocks[0])-1); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("DecodeBlock limit: got %v, want ErrOutputLimit", err)
	}
}

func TestCorruptPayloadDetected(t *testing.T) {
	stream := buildStream(t, testDict, [][]byte{makeBlock(8, 100)})
	stream[StreamHeaderSize+BlockHeaderSize+3] ^= 0xFF // flip a payload byte
	rd, err := NewReader(bytes.NewReader(stream), testDict, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rd.ReadBlock(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("got %v, want ErrCorrupt", err)
	}
}

func TestCorruptBlockDoesNotHideNext(t *testing.T) {
	blocks := [][]byte{makeBlock(10, 100), makeBlock(11, 100), makeBlock(12, 100)}
	stream := buildStream(t, testDict, blocks)

	// Corrupt the middle block's payload beyond recognition.
	hdr1, err := ParseBlockHeader(stream[StreamHeaderSize:])
	if err != nil {
		t.Fatal(err)
	}
	second := StreamHeaderSize + BlockHeaderSize + int(hdr1.EncLen)
	hdr2, err := ParseBlockHeader(stream[second:])
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < int(hdr2.EncLen); i++ {
		stream[second+BlockHeaderSize+i] ^= 0xA5
	}

	rd, err := NewReader(bytes.NewReader(stream), testDict, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	got, err := rd.ReadBlock() // block 1 fine
	if err != nil || !bytes.Equal(got, blocks[0]) {
		t.Fatalf("block 1: err=%v match=%v", err, bytes.Equal(got, blocks[0]))
	}
	if _, err := rd.ReadBlock(); err == nil { // block 2 corrupt
		t.Fatal("corrupt block 2 decoded without error")
	}
	if err := rd.Resync(); err != nil { // locate block 3
		t.Fatalf("Resync: %v", err)
	}
	got, err = rd.ReadBlock()
	if err != nil || !bytes.Equal(got, blocks[2]) {
		t.Fatalf("block 3 after resync: err=%v match=%v", err, bytes.Equal(got, blocks[2]))
	}
	if _, err := rd.ReadBlock(); err != io.EOF {
		t.Fatalf("end of stream: got %v, want io.EOF", err)
	}
}

func TestResyncWithGarbageBetweenBlocks(t *testing.T) {
	blocks := [][]byte{makeBlock(20, 50), makeBlock(21, 50)}
	garbage := []byte("this is not a block at all, \xdb\x1bBK even a fake magic")
	stream := AppendStreamHeader(nil, testDict)
	var err error
	stream, err = AppendBlock(stream, testDict, blocks[0])
	if err != nil {
		t.Fatal(err)
	}
	stream = append(stream, garbage...)
	stream, err = AppendBlock(stream, testDict, blocks[1])
	if err != nil {
		t.Fatal(err)
	}

	rd, err := NewReader(bytes.NewReader(stream), testDict, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rd.ReadBlock(); err != nil {
		t.Fatalf("block 1: %v", err)
	}
	if _, err := rd.ReadBlock(); err == nil {
		t.Fatal("garbage decoded as a block")
	}
	if err := rd.Resync(); err != nil {
		t.Fatalf("Resync: %v", err)
	}
	got, err := rd.ReadBlock()
	if err != nil || !bytes.Equal(got, blocks[1]) {
		t.Fatalf("block 2 after garbage: err=%v match=%v", err, bytes.Equal(got, blocks[1]))
	}
}

func TestRandomAccessDecode(t *testing.T) {
	blocks := [][]byte{makeBlock(30, 80), makeBlock(31, 120), makeBlock(32, 60)}
	stream := buildStream(t, testDict, blocks)

	// Walk headers without decoding, then decode only block 3.
	off := StreamHeaderSize
	var hdr *BlockHeader
	for i := 0; i < 3; i++ {
		h, err := ParseBlockHeader(stream[off:])
		if err != nil {
			t.Fatal(err)
		}
		hdr = h
		if i < 2 {
			off += BlockHeaderSize + int(h.EncLen)
		}
	}
	payload := stream[off+BlockHeaderSize : off+BlockHeaderSize+int(hdr.EncLen)]
	got, err := DecodeBlock(testDict, hdr, payload, MaxBlockRaw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, blocks[2]) {
		t.Fatal("random-access decode of block 3 mismatch")
	}
}

func TestBadVersionAndMagic(t *testing.T) {
	stream := buildStream(t, testDict, [][]byte{makeBlock(40, 10)})

	bad := bytes.Clone(stream)
	bad[0] ^= 0xFF
	if _, err := NewReader(bytes.NewReader(bad), testDict, 1<<30); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("bad stream magic: got %v", err)
	}

	bad = bytes.Clone(stream)
	bad[4] = 99
	if _, err := NewReader(bytes.NewReader(bad), testDict, 1<<30); !errors.Is(err, ErrBadVersion) {
		t.Fatalf("bad stream version: got %v", err)
	}

	bad = bytes.Clone(stream)
	bad[StreamHeaderSize+4] = 99 // block version
	if _, err := ParseBlockHeader(bad[StreamHeaderSize:]); !errors.Is(err, ErrBadVersion) {
		t.Fatalf("bad block version: got %v", err)
	}

	bad = bytes.Clone(stream)
	bad[StreamHeaderSize+10] ^= 0x01 // corrupt header field -> header CRC fails
	if _, err := ParseBlockHeader(bad[StreamHeaderSize:]); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corrupt block header: got %v", err)
	}
}

func TestTruncatedStream(t *testing.T) {
	stream := buildStream(t, testDict, [][]byte{makeBlock(50, 100)})
	rd, err := NewReader(bytes.NewReader(stream[:len(stream)-5]), testDict, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rd.ReadBlock(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("truncated payload: got %v, want ErrCorrupt", err)
	}
}
