package framechain

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand"
	"testing"
)

// buildChain makes n validly chained frames starting at genesis.
func buildChain(n int) []*Frame {
	var frames []*Frame
	prev := GenesisPrev
	for i := 0; i < n; i++ {
		f := &Frame{Seq: uint64(i), Prev: prev, Payload: []byte{byte(i), byte(i * 7), 'x'}}
		frames = append(frames, f)
		prev = f.DigestOf()
	}
	return frames
}

func encodeAll(fs []*Frame) []byte {
	var b []byte
	for _, f := range fs {
		b = append(b, f.Encode()...)
	}
	return b
}

func TestRoundTrip(t *testing.T) {
	fs := buildChain(5)
	p := NewParser()
	got, err := p.Feed(encodeAll(fs))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || p.Finish() != 0 {
		t.Fatalf("got %d frames, pending %d", len(got), p.Finish())
	}
	for i := range fs {
		if got[i].Seq != fs[i].Seq || got[i].Prev != fs[i].Prev || !bytes.Equal(got[i].Payload, fs[i].Payload) {
			t.Fatalf("frame %d mismatch", i)
		}
	}
}

func TestArbitraryChunking(t *testing.T) {
	fs := buildChain(20)
	stream := encodeAll(fs)
	for _, size := range []int{1, 3, 7, 64, 1024} {
		p := NewParser()
		var got []*Frame
		for i := 0; i < len(stream); i += size {
			end := i + size
			if end > len(stream) {
				end = len(stream)
			}
			out, err := p.Feed(stream[i:end])
			if err != nil {
				t.Fatalf("chunk %d: %v", size, err)
			}
			got = append(got, out...)
		}
		if len(got) != 20 || p.Finish() != 0 {
			t.Fatalf("chunk %d: got %d frames pending %d", size, len(got), p.Finish())
		}
	}
	// 随机分块
	r := rand.New(rand.NewSource(42))
	p := NewParser()
	var got []*Frame
	for i := 0; i < len(stream); {
		n := 1 + r.Intn(50)
		if i+n > len(stream) {
			n = len(stream) - i
		}
		out, err := p.Feed(stream[i : i+n])
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, out...)
		i += n
	}
	if len(got) != 20 {
		t.Fatalf("random chunking: got %d frames", len(got))
	}
}

func TestTruncationIsNotBadData(t *testing.T) {
	fs := buildChain(3)
	stream := encodeAll(fs)
	full := frameSizeOf(t, fs[2])
	trunc := stream[:len(stream)-full+full/2] // 末帧只收一半
	p := NewParser()
	got, err := p.Feed(trunc)
	if err != nil {
		t.Fatalf("truncated tail must not be bad data: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d frames, want 2", len(got))
	}
	if p.Finish() == 0 {
		t.Fatal("expected pending bytes for truncated last frame")
	}
	// 流在此结束：仅末帧截断 => 视为未完成，而不是损坏
	c := NewChain()
	for _, f := range got {
		if _, err := c.Add(f); err != nil {
			t.Fatal(err)
		}
	}
}

func frameSizeOf(t *testing.T, f *Frame) int {
	t.Helper()
	return len(f.Encode())
}

func TestMiddleFrameMissingMustError(t *testing.T) {
	fs := buildChain(5)
	// 丢掉中间第 2 帧（seq=2）
	broken := append(append([]*Frame{}, fs[:2]...), fs[3:]...)
	c := NewChain()
	var err error
	for _, f := range broken {
		if _, err = c.Add(f); err != nil {
			break
		}
	}
	if !errors.Is(err, ErrSeqGap) {
		t.Fatalf("middle missing frame must error with ErrSeqGap, got %v", err)
	}
	if idx := FindBreak(broken); idx != 2 {
		t.Fatalf("FindBreak = %d, want 2", idx)
	}
}

func TestBadData(t *testing.T) {
	fs := buildChain(2)
	stream := encodeAll(fs)

	// 坏 magic
	bad := append([]byte(nil), stream...)
	bad[0] = 'X'
	if _, err := NewParser().Feed(bad); !errors.Is(err, ErrBadData) {
		t.Fatalf("bad magic: %v", err)
	}
	// 坏 CRC（翻转 payload 一字节）
	bad = append([]byte(nil), stream...)
	bad[headerSize] ^= 0xff
	if _, err := NewParser().Feed(bad); !errors.Is(err, ErrBadData) {
		t.Fatalf("bad crc: %v", err)
	}
}

func TestLengthQuotaCheckedBeforeAlloc(t *testing.T) {
	var hdr []byte
	hdr = append(hdr, Magic...)
	hdr = append(hdr, make([]byte, 8)...)          // seq
	hdr = append(hdr, make([]byte, DigestSize)...) // prev
	hdr = append(hdr, 0xff, 0xff, 0xff, 0xff)      // paylen = 4GiB-1，远超额度
	if _, err := NewParser().Feed(hdr); !errors.Is(err, ErrBadData) {
		t.Fatalf("oversize length must be rejected before allocation, got %v", err)
	}
	// 恰好等于额度：不是坏数据，只是缺数据
	binary.BigEndian.PutUint32(hdr[len(hdr)-4:], MaxPayload)
	got, err := NewParser().Feed(hdr)
	if err != nil || got != nil {
		t.Fatalf("in-quota length should wait for more data, got %v %v", got, err)
	}
}

func TestSwapAdjacentBreaksChain(t *testing.T) {
	fs := buildChain(4)
	fs[1], fs[2] = fs[2], fs[1] // 各自 CRC 都正确，但顺序被交换
	c := NewChain()
	var err error
	for _, f := range fs {
		if _, err = c.Add(f); err != nil {
			break
		}
	}
	if !errors.Is(err, ErrChainBreak) && !errors.Is(err, ErrSeqGap) {
		t.Fatalf("swapped frames must break chain, got %v", err)
	}
	if idx := FindBreak(fs); idx != 1 {
		t.Fatalf("FindBreak = %d, want 1", idx)
	}
}

func TestDuplicateFrame(t *testing.T) {
	fs := buildChain(3)
	c := NewChain()
	for _, f := range fs {
		if _, err := c.Add(f); err != nil {
			t.Fatal(err)
		}
	}
	// 字节级重复：忽略，不当新帧
	dup, err := c.Add(fs[1])
	if err != nil || !dup {
		t.Fatalf("identical resend: dup=%v err=%v", dup, err)
	}
	if len(c.Frames()) != 3 {
		t.Fatalf("duplicate must not be appended, have %d frames", len(c.Frames()))
	}
	// 同 seq 不同内容：冲突
	evil := &Frame{Seq: 1, Prev: fs[1].Prev, Payload: []byte("forged")}
	if _, err := c.Add(evil); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("conflicting duplicate: %v", err)
	}
}

func TestCheckpointSuffix(t *testing.T) {
	fs := buildChain(6)
	cp := CheckpointOf(fs[2]) // 前 3 帧已被验证
	if err := VerifySuffix(cp, fs[3:]); err != nil {
		t.Fatalf("valid suffix rejected: %v", err)
	}
	// 从检查点续传
	c := NewChainFrom(cp)
	for _, f := range fs[3:] {
		if _, err := c.Add(f); err != nil {
			t.Fatal(err)
		}
	}
	if c.Head().Seq != 5 {
		t.Fatalf("head seq = %d", c.Head().Seq)
	}

	// 错误检查点：摘要不对
	badDigest := cp
	badDigest.Digest[0] ^= 0xff
	if err := VerifySuffix(badDigest, fs[3:]); !errors.Is(err, ErrCheckpoint) {
		t.Fatalf("wrong digest checkpoint accepted suffix: %v", err)
	}
	// 错误检查点：序号与摘要不属于同一帧
	badSeq := Checkpoint{Seq: cp.Seq, Digest: fs[3].DigestOf()}
	if err := VerifySuffix(badSeq, fs[3:]); !errors.Is(err, ErrCheckpoint) {
		t.Fatalf("mismatched checkpoint accepted suffix: %v", err)
	}
	// 检查点之后的后缀中间断链
	broken := append(append([]*Frame{}, fs[3:5]...), fs[5])
	broken[1] = &Frame{Seq: 4, Prev: fs[3].Prev, Payload: []byte("x")} // prev 指错
	if err := VerifySuffix(cp, broken); !errors.Is(err, ErrChainBreak) {
		t.Fatalf("broken suffix: %v", err)
	}
}

func TestFindBreakClean(t *testing.T) {
	if idx := FindBreak(buildChain(10)); idx != -1 {
		t.Fatalf("clean chain: FindBreak = %d", idx)
	}
}
