package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/rand"
	"testing"
)

const (
	typeSpec  = 0x01 // 4 字节载荷
	typeBoot  = 0x02 // 8 字节载荷
	typeEmpty = 0x03 // 0 字节载荷
)

func testTable() *TypeTable {
	tbl := NewTypeTable()
	tbl.Declare(typeSpec, 4)
	tbl.Declare(typeBoot, 8)
	tbl.Declare(typeEmpty, 0)
	return tbl
}

// sampleStream 构造三条分属不同寄存器的合法事件。
func sampleStream() ([]byte, []Event) {
	raw := [][]byte{
		MarshalEvent(1, typeSpec, []byte{0xde, 0xad, 0xbe, 0xef}),
		MarshalEvent(2, typeBoot, []byte("boot0001")),
		MarshalEvent(1, typeEmpty, nil),
	}
	var stream []byte
	var evs []Event
	for _, r := range raw {
		stream = append(stream, r...)
		var d [DigestLen]byte
		copy(d[:], r[len(r)-DigestLen:])
		evs = append(evs, Event{
			Register: r[0],
			Type:     r[1],
			Payload:  append([]byte(nil), r[HeaderLen:len(r)-DigestLen]...),
			Digest:   d,
		})
	}
	return stream, evs
}

func feedAll(t *testing.T, d *Decoder, chunks ...[]byte) []Event {
	t.Helper()
	var got []Event
	for _, c := range chunks {
		evs, err := d.Feed(c)
		if err != nil {
			t.Fatalf("Feed: %v", err)
		}
		got = append(got, evs...)
	}
	return got
}

func assertEvents(t *testing.T, got, want []Event) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d", len(got), len(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Register != w.Register || g.Type != w.Type ||
			!bytes.Equal(g.Payload, w.Payload) || g.Digest != w.Digest {
			t.Fatalf("event %d mismatch: got %+v, want %+v", i, g, w)
		}
	}
}

func TestWholeStream(t *testing.T) {
	stream, want := sampleStream()
	d := NewDecoder(testTable())
	got := feedAll(t, d, stream)
	assertEvents(t, got, want)
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// 任意切块：所有二切点、逐字节、随机块大小，交付事件必须一致。
func TestArbitraryChunking(t *testing.T) {
	stream, want := sampleStream()

	for cut := 0; cut <= len(stream); cut++ {
		d := NewDecoder(testTable())
		got := feedAll(t, d, stream[:cut], stream[cut:])
		assertEvents(t, got, want)
		if err := d.Close(); err != nil {
			t.Fatalf("cut=%d Close: %v", cut, err)
		}
	}

	d := NewDecoder(testTable())
	var gotEvents []Event
	for _, b := range stream {
		evs, err := d.Feed([]byte{b})
		if err != nil {
			t.Fatalf("byte-wise Feed: %v", err)
		}
		gotEvents = append(gotEvents, evs...)
	}
	assertEvents(t, gotEvents, want)

	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		d := NewDecoder(testTable())
		var got []Event
		for rest := stream; len(rest) > 0; {
			n := 1 + rng.Intn(7)
			if n > len(rest) {
				n = len(rest)
			}
			evs, err := d.Feed(rest[:n])
			if err != nil {
				t.Fatalf("trial %d Feed: %v", trial, err)
			}
			got = append(got, evs...)
			rest = rest[n:]
		}
		assertEvents(t, got, want)
		if err := d.Close(); err != nil {
			t.Fatalf("trial %d Close: %v", trial, err)
		}
	}
}

// 截断：任何真前缀都不得交付半个事件，Close 必须报告截断。
func TestTruncation(t *testing.T) {
	stream, _ := sampleStream()

	// 各事件的结束偏移：切点落在边界上即为完整流。
	var bounds []int
	for off := 0; off < len(stream); {
		n := int(binary.BigEndian.Uint32(stream[off+2 : off+6]))
		off += HeaderLen + n + DigestLen
		bounds = append(bounds, off)
	}
	atBoundary := func(cut int) bool {
		for _, b := range bounds {
			if b == cut {
				return true
			}
		}
		return false
	}

	for cut := 0; cut < len(stream); cut++ {
		d := NewDecoder(testTable())
		got := feedAll(t, d, stream[:cut])
		// 只能交付完整事件，绝不交付半条。
		wantN := 0
		for _, b := range bounds {
			if cut >= b {
				wantN++
			}
		}
		if len(got) != wantN {
			t.Fatalf("cut=%d: delivered %d events, want %d", cut, len(got), wantN)
		}
		if cut == 0 || atBoundary(cut) {
			// 空流或恰好完整的事件序列都是合法日志，不算截断。
			if err := d.Close(); err != nil {
				t.Fatalf("cut=%d: Close = %v, want nil", cut, err)
			}
			continue
		}
		if err := d.Close(); !errors.Is(err, ErrTruncated) {
			t.Fatalf("cut=%d: Close = %v, want ErrTruncated", cut, err)
		}
	}
}

// 伪长字段：长度与类型声明不符时立即报错，不得缓冲等待，
// 更不得把后续寄存器的事件字节吞作载荷。
func TestForgedLengthField(t *testing.T) {
	good := MarshalEvent(1, typeSpec, []byte{0xde, 0xad, 0xbe, 0xef})
	next := MarshalEvent(2, typeBoot, []byte("boot0001"))

	for _, fake := range []uint32{0, 3, 5, 0xFFFFFFFF, uint32(len(good) + len(next))} {
		ev := append([]byte(nil), good...)
		binary.BigEndian.PutUint32(ev[2:6], fake)
		stream := append(ev, next...)

		d := NewDecoder(testTable())
		if _, err := d.Feed(stream); !errors.Is(err, ErrLengthMismatch) {
			t.Fatalf("fake=%d: err = %v, want ErrLengthMismatch", fake, err)
		}
		// 头部一到即报错：只喂 6 字节头部也应失败，而不是傻等 fake 长度。
		d2 := NewDecoder(testTable())
		if _, err := d2.Feed(ev[:HeaderLen]); !errors.Is(err, ErrLengthMismatch) {
			t.Fatalf("fake=%d header-only: err = %v, want ErrLengthMismatch", fake, err)
		}
	}
}

// 出错后进入失败态：拒绝后续输入，不搜索后续魔数重新同步。
func TestNoResyncAfterError(t *testing.T) {
	good := MarshalEvent(1, typeSpec, []byte{1, 2, 3, 4})
	bad := append([]byte(nil), good...)
	bad[HeaderLen] ^= 0xFF // 破坏载荷，摘要不匹配
	valid := MarshalEvent(2, typeBoot, []byte("boot0001"))

	d := NewDecoder(testTable())
	if _, err := d.Feed(bad); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("err = %v, want ErrDigestMismatch", err)
	}
	// 后续即使喂入完整合法事件，也绝不恢复解析。
	for i := 0; i < 3; i++ {
		evs, err := d.Feed(valid)
		if !errors.Is(err, ErrDigestMismatch) {
			t.Fatalf("retry %d: err = %v, want sticky ErrDigestMismatch", i, err)
		}
		if len(evs) != 0 {
			t.Fatalf("retry %d: delivered %d events after failure", i, len(evs))
		}
	}
	if err := d.Close(); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Close = %v, want sticky error", err)
	}
}

func TestDigestMismatch(t *testing.T) {
	ev := MarshalEvent(1, typeSpec, []byte{1, 2, 3, 4})
	ev[len(ev)-1] ^= 0x01 // 破坏摘要最后一字节
	d := NewDecoder(testTable())
	if _, err := d.Feed(ev); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("err = %v, want ErrDigestMismatch", err)
	}
}

func TestUnknownType(t *testing.T) {
	ev := MarshalEvent(0, 0x7f, []byte{1, 2, 3, 4})
	d := NewDecoder(testTable())
	if _, err := d.Feed(ev); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("err = %v, want ErrUnknownType", err)
	}
}

func TestFeedAfterClose(t *testing.T) {
	d := NewDecoder(testTable())
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := d.Feed([]byte{0}); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
}

// 宿主：只有完整且摘要匹配的事件才扩展寄存器；任意切块结果一致。
func TestHostExtendsOnlyCompleteEvents(t *testing.T) {
	stream, evs := sampleStream()

	// 参考值：手工按序扩展。
	wantReg := func(idx uint8) [DigestLen]byte {
		var reg [DigestLen]byte
		for _, ev := range evs {
			if ev.Register == idx {
				reg = sha256.Sum256(append(reg[:], ev.Digest[:]...))
			}
		}
		return reg
	}

	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 50; trial++ {
		h := NewHost(testTable())
		total := 0
		for rest := stream; len(rest) > 0; {
			n := 1 + rng.Intn(11)
			if n > len(rest) {
				n = len(rest)
			}
			cnt, err := h.Feed(rest[:n])
			if err != nil {
				t.Fatalf("trial %d Feed: %v", trial, err)
			}
			total += cnt
			rest = rest[n:]
		}
		if total != len(evs) || h.Events() != len(evs) {
			t.Fatalf("trial %d: extended %d events, want %d", trial, total, len(evs))
		}
		if h.Register(1) != wantReg(1) || h.Register(2) != wantReg(2) {
			t.Fatalf("trial %d: register values mismatch", trial)
		}
		if h.Register(9) != ([DigestLen]byte{}) {
			t.Fatalf("trial %d: untouched register not zero", trial)
		}
		if err := h.Close(); err != nil {
			t.Fatalf("trial %d Close: %v", trial, err)
		}
	}
}

// 宿主：伪长字段攻击下寄存器保持原状，且不会把后续事件误解析出来。
func TestHostRejectsForgedLength(t *testing.T) {
	good := MarshalEvent(1, typeSpec, []byte{0xde, 0xad, 0xbe, 0xef})
	forged := append([]byte(nil), good...)
	binary.BigEndian.PutUint32(forged[2:6], uint32(len(good)+64)) // 吞掉后续字节
	stream := append(forged, MarshalEvent(2, typeBoot, []byte("boot0001"))...)

	h := NewHost(testTable())
	if _, err := h.Feed(stream); !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("err = %v, want ErrLengthMismatch", err)
	}
	if h.Events() != 0 {
		t.Fatalf("extended %d events under forged length", h.Events())
	}
	if h.Register(1) != ([DigestLen]byte{}) || h.Register(2) != ([DigestLen]byte{}) {
		t.Fatal("registers must stay zero after rejected stream")
	}
}

// 宿主：截断日志不得扩展半截事件的寄存器。
func TestHostTruncatedLog(t *testing.T) {
	stream, _ := sampleStream()
	h := NewHost(testTable())
	cut := len(stream) - 3 // 最后一条事件缺 3 字节
	cnt, err := h.Feed(stream[:cut])
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if cnt != 2 {
		t.Fatalf("extended %d events, want 2", cnt)
	}
	if err := h.Close(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("Close = %v, want ErrTruncated", err)
	}
}
