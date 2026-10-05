package acx

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

func encodeAll(t *testing.T, numCtx int, events []Event) []byte {
	t.Helper()
	enc, err := NewEncoder(numCtx)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	for _, ev := range events {
		if err := enc.Put(ev.Label, ev.Bit); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	return enc.Frame()
}

func roundTrip(t *testing.T, numCtx int, events []Event) []byte {
	t.Helper()
	frame := encodeAll(t, numCtx, events)
	got, err := Decode(frame, MaxContexts)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got) != len(events) {
		t.Fatalf("decoded %d events, want %d", len(got), len(events))
	}
	for i := range events {
		if got[i] != events[i] {
			t.Fatalf("event %d: got %+v, want %+v", i, got[i], events[i])
		}
	}
	return frame
}

func TestModelRescaleBoundary(t *testing.T) {
	// 计数和到达 4096 时各自向上取半且至少为 1。
	c := context{zero: 2047, one: 2047}
	c.update(0) // zero=2048, sum=4095，不重标定
	if c.zero != 2048 || c.one != 2047 {
		t.Fatalf("before boundary: %+v", c)
	}
	c.update(0) // zero=2049, sum=4096 -> ceil(2049/2)=1025, ceil(2047/2)=1024
	if c.zero != 1025 || c.one != 1024 {
		t.Fatalf("after rescale: got %+v, want {1025 1024}", c)
	}
	// 极端偏斜：one=1 时向上取半仍为 1。
	c = context{zero: maxTotal - 1, one: 0}
	c.update(1) // one=1, sum=4096 -> zero=ceil(4095/2)=2048, one=1
	if c.zero != 2048 || c.one != 1 {
		t.Fatalf("skewed rescale: got %+v, want {2048 1}", c)
	}
	// 不变式：任意更新后计数和不超过 4096，且各自 >= 1。
	c = newContext()
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 100000; i++ {
		c.update(uint8(rng.Intn(2)))
		if c.total() > maxTotal || c.zero < 1 || c.one < 1 {
			t.Fatalf("invariant violated at %d: %+v", i, c)
		}
	}
}

func TestRoundTripBasic(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	var events []Event
	for i := 0; i < 5000; i++ {
		events = append(events, Event{Label: uint8(rng.Intn(4)), Bit: uint8(rng.Intn(2))})
	}
	roundTrip(t, 4, events)
}

func TestAlternatingContexts(t *testing.T) {
	// 交替上下文：ctx0 几乎全 0，ctx1 几乎全 1。
	var events []Event
	for i := 0; i < 20000; i++ {
		lb := uint8(i % 2)
		bit := lb
		if i%97 == 0 {
			bit ^= 1
		}
		events = append(events, Event{Label: lb, Bit: bit})
	}
	frame := roundTrip(t, 2, events)
	payload := framePayload(t, frame)
	t.Logf("alternating contexts: %d events -> 帧 %d 字节 (标签 %d + 载荷 %d), 载荷 %.4f 比特/事件",
		len(events), len(frame), len(events), len(payload), float64(len(payload))*8/float64(len(events)))
}

func TestSkewedDistribution(t *testing.T) {
	// 极偏分布：1/64 的 1。
	rng := rand.New(rand.NewSource(2))
	var events []Event
	for i := 0; i < 65536; i++ {
		bit := uint8(0)
		if rng.Intn(64) == 0 {
			bit = 1
		}
		events = append(events, Event{Label: 0, Bit: bit})
	}
	frame := roundTrip(t, 1, events)
	payload := framePayload(t, frame)
	t.Logf("skewed p(1)=1/64: %d events -> 载荷 %d 字节, %.4f 比特/事件 (理论熵约 0.116)",
		len(events), len(payload), float64(len(payload))*8/float64(len(events)))
}

func TestRescaleBoundaryRoundTrip(t *testing.T) {
	// 单上下文持续编码，多次跨越 4096 重标定点。
	rng := rand.New(rand.NewSource(3))
	var events []Event
	for i := 0; i < 30000; i++ {
		bit := uint8(0)
		if rng.Intn(5) == 0 {
			bit = 1
		}
		events = append(events, Event{Label: 0, Bit: bit})
	}
	roundTrip(t, 1, events)
}

func TestEmptyInput(t *testing.T) {
	frame := roundTrip(t, 3, nil)
	got, err := Decode(frame, MaxContexts)
	if err != nil {
		t.Fatalf("Decode empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 events, got %d", len(got))
	}
	t.Logf("empty frame: %d bytes", len(frame))
}

func TestLongRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	var events []Event
	for i := 0; i < 200000; i++ {
		lb := uint8(rng.Intn(8))
		// 每个上下文偏斜不同
		p := float64(lb+1) / 16
		bit := uint8(0)
		if rng.Float64() < p {
			bit = 1
		}
		events = append(events, Event{Label: lb, Bit: bit})
	}
	frame := roundTrip(t, 8, events)
	payload := framePayload(t, frame)
	t.Logf("long mixed: %d events -> 帧 %d 字节 (标签 %d + 载荷 %d), 载荷 %.4f 比特/事件",
		len(events), len(frame), len(events), len(payload), float64(len(payload))*8/float64(len(events)))
}

func TestResumeByteIdentical(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	var events []Event
	for i := 0; i < 50000; i++ {
		events = append(events, Event{Label: uint8(rng.Intn(6)), Bit: uint8(rng.Intn(2))})
	}
	oneShot := encodeAll(t, 6, events)

	// 分段编码，中间序列化快照并恢复，要求与一次编码逐字节一致。
	for _, cut := range []int{0, 1, 7, 12345, 49999, 50000} {
		enc, _ := NewEncoder(6)
		for _, ev := range events[:cut] {
			if err := enc.Put(ev.Label, ev.Bit); err != nil {
				t.Fatal(err)
			}
		}
		snap, err := enc.Snapshot()
		if err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
		enc2, err := Resume(snap)
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		for _, ev := range events[cut:] {
			if err := enc2.Put(ev.Label, ev.Bit); err != nil {
				t.Fatal(err)
			}
		}
		if got := enc2.Frame(); !bytes.Equal(got, oneShot) {
			t.Fatalf("cut=%d: resumed frame differs from one-shot", cut)
		}
	}

	// 多次连续快照/恢复。
	enc, _ := NewEncoder(6)
	for i, ev := range events {
		if err := enc.Put(ev.Label, ev.Bit); err != nil {
			t.Fatal(err)
		}
		if i%4096 == 4095 {
			snap, err := enc.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if enc, err = Resume(snap); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := enc.Frame(); !bytes.Equal(got, oneShot) {
		t.Fatal("multi-snapshot frame differs from one-shot")
	}
}

func TestTruncationRejected(t *testing.T) {
	rng := rand.New(rand.NewSource(6))
	var events []Event
	for i := 0; i < 1000; i++ {
		events = append(events, Event{Label: uint8(rng.Intn(3)), Bit: uint8(rng.Intn(2))})
	}
	frame := encodeAll(t, 3, events)
	for _, n := range []int{0, 2, 4, 5, 8, len(frame) / 2, len(frame) - 1} {
		if _, err := Decode(frame[:n], MaxContexts); err == nil {
			t.Fatalf("truncated to %d bytes: want error, got nil", n)
		}
	}
	// 尾部垃圾也必须拒绝。
	if _, err := Decode(append(append([]byte(nil), frame...), 0x00), MaxContexts); err == nil {
		t.Fatal("trailing garbage: want error, got nil")
	}
}

func TestIllegalContextRejected(t *testing.T) {
	// 编码侧：标签越界直接报错。
	enc, _ := NewEncoder(2)
	if err := enc.Put(2, 0); err != ErrBadLabel {
		t.Fatalf("Put bad label: got %v", err)
	}
	if err := enc.Put(0, 2); err != ErrBadBit {
		t.Fatalf("Put bad bit: got %v", err)
	}
	// 解码侧：篡改标签字节为越界值。
	events := []Event{{0, 1}, {1, 0}, {1, 1}, {0, 0}}
	frame := encodeAll(t, 2, events)
	// 定位 labels 起点：magic(4) + uvarint(count) + uvarint(numCtx) + uvarint(payloadLen)
	pos := 4
	_, n := binary.Uvarint(frame[pos:])
	pos += n
	_, n = binary.Uvarint(frame[pos:])
	pos += n
	_, n = binary.Uvarint(frame[pos:])
	pos += n
	frame[pos+1] = 5 // numCtx=2，标签 5 非法
	if _, err := Decode(frame, MaxContexts); err != ErrBadLabel {
		t.Fatalf("Decode bad label: got %v, want ErrBadLabel", err)
	}
	// 解码侧：调用方上下文上限小于帧声明。
	frame2 := encodeAll(t, 4, events)
	if _, err := Decode(frame2, 2); err != ErrContextLimit {
		t.Fatalf("Decode context limit: got %v, want ErrContextLimit", err)
	}
}

func TestLengthOverflowRejected(t *testing.T) {
	// 构造 count 超过 MaxEvents 的帧头。
	var frame []byte
	frame = append(frame, magic...)
	frame = append(frame, uvarint(MaxEvents+1)...)
	frame = append(frame, uvarint(1)...)
	frame = append(frame, uvarint(0)...)
	if _, err := Decode(frame, MaxContexts); err != ErrTooMany {
		t.Fatalf("oversized count: got %v, want ErrTooMany", err)
	}
	// count 合法但 labels 不足（截断）。
	frame = frame[:0]
	frame = append(frame, magic...)
	frame = append(frame, uvarint(100)...)
	frame = append(frame, uvarint(1)...)
	frame = append(frame, uvarint(0)...)
	frame = append(frame, make([]byte, 10)...)
	if _, err := Decode(frame, MaxContexts); err != ErrTruncated {
		t.Fatalf("short labels: got %v, want ErrTruncated", err)
	}
}

func TestChecksumMismatchRejected(t *testing.T) {
	rng := rand.New(rand.NewSource(8))
	var events []Event
	for i := 0; i < 2000; i++ {
		events = append(events, Event{Label: 0, Bit: uint8(rng.Intn(2))})
	}
	frame := encodeAll(t, 1, events)
	// 翻转载荷中间一个字节。
	frame[len(frame)/2+10] ^= 0x40
	if _, err := Decode(frame, MaxContexts); err == nil {
		t.Fatal("corrupted payload: want error, got nil")
	}
}

func TestBadMagicRejected(t *testing.T) {
	frame := encodeAll(t, 1, []Event{{0, 1}})
	frame[0] = 'X'
	if _, err := Decode(frame, MaxContexts); err != ErrBadMagic {
		t.Fatalf("got %v, want ErrBadMagic", err)
	}
}

// TestCompressionReport 报告真实压缩大小，不设无法保证的固定门槛。
func TestCompressionReport(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	const n = 100000

	mk := func(gen func(i int) Event) []Event {
		ev := make([]Event, n)
		for i := range ev {
			ev[i] = gen(i)
		}
		return ev
	}
	cases := []struct {
		name   string
		numCtx int
		events []Event
	}{
		{"iid p(1)=1/2 单上下文", 1, mk(func(i int) Event {
			return Event{0, uint8(rng.Intn(2))}
		})},
		{"iid p(1)=1/16 单上下文", 1, mk(func(i int) Event {
			b := uint8(0)
			if rng.Intn(16) == 0 {
				b = 1
			}
			return Event{0, b}
		})},
		{"iid p(1)=1/128 单上下文", 1, mk(func(i int) Event {
			b := uint8(0)
			if rng.Intn(128) == 0 {
				b = 1
			}
			return Event{0, b}
		})},
		{"双上下文交替且偏斜相反", 2, mk(func(i int) Event {
			lb := uint8(i & 1)
			b := lb
			if rng.Intn(32) == 0 {
				b ^= 1
			}
			return Event{lb, b}
		})},
	}
	for _, tc := range cases {
		frame := roundTrip(t, tc.numCtx, tc.events)
		payload := framePayload(t, frame)
		t.Logf("%-20s 事件=%d  载荷=%d 字节 (%.4f 比特/事件)  标签=%d 字节  帧总计=%d 字节",
			tc.name, n, len(payload), float64(len(payload))*8/n, n, len(frame))
	}
}
