package blockdict

import (
	"bytes"
	"errors"
	"hash/crc32"
	"math/rand"
	"testing"
)

var testDict = NewDict([]byte("the quick brown fox jumps over the lazy dog. " +
	"pack my box with five dozen liquor jugs. "))

func roundTrip(t *testing.T, d *Dict, src []byte) {
	t.Helper()
	buf := AppendBlock(nil, d, src)
	out, next, err := DecodeBlockAt(buf, 0, d, 1<<20)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if next != len(buf) {
		t.Fatalf("next=%d, want %d", next, len(buf))
	}
	if !bytes.Equal(out, src) {
		t.Fatalf("round trip mismatch: got %d bytes, want %d", len(out), len(src))
	}
}

func TestRoundTripWithDict(t *testing.T) {
	// 内容大量复用字典，压缩应显著生效。
	src := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 50)
	buf := AppendBlock(nil, testDict, src)
	if len(buf)-HeaderSize >= len(src) {
		t.Fatalf("expected compression, payload=%d src=%d", len(buf)-HeaderSize, len(src))
	}
	roundTrip(t, testDict, src)
}

func TestRoundTripEmptyDict(t *testing.T) {
	d := NewDict(nil)
	src := bytes.Repeat([]byte("hello hello hello, no dictionary needed! "), 100)
	roundTrip(t, d, src)
}

func TestRoundTripEmptyInput(t *testing.T) {
	roundTrip(t, testDict, nil)
	roundTrip(t, NewDict(nil), nil)
}

func TestRoundTripRandomData(t *testing.T) {
	// 不可压缩数据应走原样存储路径，仍能往返。
	src := make([]byte, 4096)
	rand.New(rand.NewSource(1)).Read(src)
	roundTrip(t, testDict, src)
}

func TestWrongDictRejected(t *testing.T) {
	src := []byte("the quick brown fox jumps over the lazy dog. the quick brown fox.")
	buf := AppendBlock(nil, testDict, src)
	other := NewDict([]byte("a completely different dictionary"))
	if _, _, err := DecodeBlockAt(buf, 0, other, 1<<20); !errors.Is(err, ErrDictMismatch) {
		t.Fatalf("want ErrDictMismatch, got %v", err)
	}
	// 空字典 vs 非空字典同样必须拒绝。
	if _, _, err := DecodeBlockAt(buf, 0, NewDict(nil), 1<<20); !errors.Is(err, ErrDictMismatch) {
		t.Fatalf("want ErrDictMismatch for empty dict, got %v", err)
	}
}

func TestOutputLimitEnforced(t *testing.T) {
	src := bytes.Repeat([]byte("the quick brown fox "), 200) // 4000 字节
	buf := AppendBlock(nil, testDict, src)
	// 头中声明的解压长度超限。
	if _, _, err := DecodeBlockAt(buf, 0, testDict, 1000); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("want ErrOutputLimit, got %v", err)
	}
	// 恰好等于输出长度则放行。
	if _, _, err := DecodeBlockAt(buf, 0, testDict, len(src)); err != nil {
		t.Fatalf("exact limit should pass: %v", err)
	}
}

func TestBlockTooLarge(t *testing.T) {
	buf := AppendBlock(nil, testDict, []byte("data"))
	// 篡改载荷长度为超限值并修回头 CRC，模拟对端伪造。
	buf[42] = byte(MaxPayload>>24 + 1)
	fixHeaderCRC(buf, 0)
	if _, _, err := DecodeBlockAt(buf, 0, testDict, 1<<20); !errors.Is(err, ErrBlockTooLarge) {
		t.Fatalf("want ErrBlockTooLarge, got %v", err)
	}
}

func TestCorruptPayloadDoesNotHideNextBlock(t *testing.T) {
	a := bytes.Repeat([]byte("the quick brown fox "), 30)
	b := []byte("second independent block, decodable without the first. second independent block.")
	var stream []byte
	stream = AppendBlock(stream, testDict, a)
	offB := len(stream)
	stream = AppendBlock(stream, testDict, b)

	// 损坏第一块载荷的一个字节。
	stream[HeaderSize+3] ^= 0x5A

	_, next, err := DecodeBlockAt(stream, 0, testDict, 1<<20)
	if !errors.Is(err, ErrPayloadCRC) {
		t.Fatalf("want ErrPayloadCRC, got %v", err)
	}
	if next != offB {
		t.Fatalf("next=%d, want block2 offset %d", next, offB)
	}
	// 下一块仍可定位并解码。
	out, next, err := DecodeBlockAt(stream, next, testDict, 1<<20)
	if err != nil {
		t.Fatalf("block after corrupt one undecodable: %v", err)
	}
	if next != len(stream) || !bytes.Equal(out, b) {
		t.Fatalf("bad recovery decode")
	}
}

func TestCorruptHeaderResync(t *testing.T) {
	a := []byte("first block payload first block payload")
	b := []byte("second block stays locatable. second block stays locatable.")
	var stream []byte
	stream = AppendBlock(stream, testDict, a)
	stream = AppendBlock(stream, testDict, b)

	// 损坏第一块头部（长度字段），长度不再可信。
	stream[40] ^= 0xFF
	if _, _, err := DecodeBlockAt(stream, 0, testDict, 1<<20); !errors.Is(err, ErrHeaderCRC) {
		t.Fatalf("want ErrHeaderCRC, got %v", err)
	}
	// 扫描重新同步到第二块。
	off := FindNext(stream, 1)
	if off < 0 {
		t.Fatal("FindNext failed to resync")
	}
	out, _, err := DecodeBlockAt(stream, off, testDict, 1<<20)
	if err != nil || !bytes.Equal(out, b) {
		t.Fatalf("resync decode failed: %v", err)
	}
}

func TestRandomAccessIndependence(t *testing.T) {
	blocks := [][]byte{
		bytes.Repeat([]byte("alpha alpha alpha "), 20),
		bytes.Repeat([]byte("the quick brown fox "), 20),
		bytes.Repeat([]byte("gamma gamma gamma "), 20),
	}
	var stream []byte
	var offs []int
	for _, blk := range blocks {
		offs = append(offs, len(stream))
		stream = AppendBlock(stream, testDict, blk)
	}
	// 乱序、跳过式解码：不依赖前块状态。
	for _, i := range []int{2, 0, 1} {
		out, _, err := DecodeBlockAt(stream, offs[i], testDict, 1<<20)
		if err != nil || !bytes.Equal(out, blocks[i]) {
			t.Fatalf("random access block %d failed: %v", i, err)
		}
	}
}

func TestTruncated(t *testing.T) {
	buf := AppendBlock(nil, testDict, []byte("some payload to truncate"))
	if _, _, err := DecodeBlockAt(buf[:HeaderSize-1], 0, testDict, 1<<20); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated for short header, got %v", err)
	}
	if _, _, err := DecodeBlockAt(buf[:len(buf)-2], 0, testDict, 1<<20); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated for short payload, got %v", err)
	}
}

func TestBadMagicAndVersion(t *testing.T) {
	buf := AppendBlock(nil, testDict, []byte("x"))
	buf[0] ^= 0xFF
	if _, _, err := DecodeBlockAt(buf, 0, testDict, 1<<20); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("want ErrBadMagic, got %v", err)
	}
	buf = AppendBlock(nil, testDict, []byte("x"))
	buf[4] = 99
	fixHeaderCRC(buf, 0)
	if _, _, err := DecodeBlockAt(buf, 0, testDict, 1<<20); !errors.Is(err, ErrBadVersion) {
		t.Fatalf("want ErrBadVersion, got %v", err)
	}
}

// fixHeaderCRC 在篡改头部字段后重算头 CRC，用于构造“头合法但内容非法”的用例。
func fixHeaderCRC(buf []byte, off int) {
	sum := crc32.ChecksumIEEE(buf[off : off+50])
	buf[off+50] = byte(sum >> 24)
	buf[off+51] = byte(sum >> 16)
	buf[off+52] = byte(sum >> 8)
	buf[off+53] = byte(sum)
}
