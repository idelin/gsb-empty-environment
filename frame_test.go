package correlator

import (
	"bytes"
	"errors"
	"testing"
)

const (
	testMaxPayload  = 64
	testMaxBuffered = 4 * (HeaderSize + testMaxPayload)
)

func newTestParser() *Parser { return NewParser(testMaxPayload, testMaxBuffered) }

func feedAll(t *testing.T, p *Parser, stream []byte, chunkSizes ...int) {
	t.Helper()
	i := 0
	for _, n := range chunkSizes {
		if i >= len(stream) {
			break
		}
		if n > len(stream)-i {
			n = len(stream) - i
		}
		if err := p.Feed(stream[i : i+n]); err != nil {
			t.Fatalf("Feed: %v", err)
		}
		i += n
	}
	if i < len(stream) {
		if err := p.Feed(stream[i:]); err != nil {
			t.Fatalf("Feed tail: %v", err)
		}
	}
}

func TestParserArbitraryChunking(t *testing.T) {
	frames := []Frame{
		{Type: FrameRequest, Epoch: 1, Seq: 1, Payload: []byte("hello")},
		{Type: FrameResponse, Epoch: 1, Seq: 1, Payload: []byte("world!")},
		{Type: FrameRequest, Epoch: 2, Seq: 7},
	}
	var stream []byte
	for _, f := range frames {
		stream = append(stream, EncodeFrame(f)...)
	}

	// 逐字节喂入，模拟最极端的分块。
	p := newTestParser()
	var got []Frame
	for i := 0; i < len(stream); i++ {
		if err := p.Feed(stream[i : i+1]); err != nil {
			t.Fatalf("Feed byte %d: %v", i, err)
		}
		for {
			f, err := p.Next()
			if errors.Is(err, ErrNeedMoreData) {
				break
			}
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			got = append(got, f)
		}
	}
	if len(got) != len(frames) {
		t.Fatalf("got %d frames, want %d", len(got), len(frames))
	}
	for i, f := range frames {
		if got[i].Type != f.Type || got[i].Epoch != f.Epoch || got[i].Seq != f.Seq ||
			!bytes.Equal(got[i].Payload, f.Payload) {
			t.Fatalf("frame %d mismatch: got %+v want %+v", i, got[i], f)
		}
	}
}

func TestParserNeedMoreDataVsBadData(t *testing.T) {
	full := EncodeFrame(Frame{Type: FrameRequest, Epoch: 9, Seq: 3, Payload: []byte("abc")})

	// 截断的每个前缀都必须是 ErrNeedMoreData，而不是 ErrBadData。
	for n := 0; n < len(full); n++ {
		p := newTestParser()
		if err := p.Feed(full[:n]); err != nil {
			t.Fatalf("Feed prefix %d: %v", n, err)
		}
		if _, err := p.Next(); !errors.Is(err, ErrNeedMoreData) {
			t.Fatalf("prefix %d: got %v, want ErrNeedMoreData", n, err)
		}
	}

	// 坏魔数 / 坏版本 / 坏类型必须是 ErrBadData。
	corrupt := func(mutate func(b []byte)) []byte {
		b := append([]byte(nil), full...)
		mutate(b)
		return b
	}
	cases := map[string][]byte{
		"bad magic":   corrupt(func(b []byte) { b[0] = 0x00 }),
		"bad version": corrupt(func(b []byte) { b[2] = 99 }),
		"bad type":    corrupt(func(b []byte) { b[3] = 77 }),
	}
	for name, data := range cases {
		p := newTestParser()
		if err := p.Feed(data); err != nil {
			t.Fatalf("%s: Feed: %v", name, err)
		}
		if _, err := p.Next(); !errors.Is(err, ErrBadData) {
			t.Fatalf("%s: got %v, want ErrBadData", name, err)
		}
	}
}

func TestParserLengthQuotaCheckedBeforeAlloc(t *testing.T) {
	// 头部声明超长 payload，即使负载一个字节都还没到，也必须立即拒绝，
	// 且不得等待更多数据（证明检查发生在分配/等待之前）。
	hdr := EncodeFrame(Frame{Type: FrameRequest, Epoch: 1, Seq: 1,
		Payload: make([]byte, testMaxPayload+1)})[:HeaderSize]
	p := newTestParser()
	if err := p.Feed(hdr); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if _, err := p.Next(); !errors.Is(err, ErrBadData) {
		t.Fatalf("got %v, want ErrBadData", err)
	}

	// 巨型长度（4GB）同样应在额度检查处被拦下。
	giant := EncodeFrame(Frame{Type: FrameRequest, Epoch: 1, Seq: 1})
	giant[20], giant[21], giant[22], giant[23] = 0xFF, 0xFF, 0xFF, 0xFF
	p2 := newTestParser()
	if err := p2.Feed(giant[:HeaderSize]); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if _, err := p2.Next(); !errors.Is(err, ErrBadData) {
		t.Fatalf("giant length: got %v, want ErrBadData", err)
	}

	// 合法上限内的长度：数据未到齐时是 ErrNeedMoreData。
	ok := EncodeFrame(Frame{Type: FrameRequest, Epoch: 1, Seq: 1,
		Payload: make([]byte, testMaxPayload)})[:HeaderSize]
	p3 := newTestParser()
	if err := p3.Feed(ok); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if _, err := p3.Next(); !errors.Is(err, ErrNeedMoreData) {
		t.Fatalf("in-quota partial: got %v, want ErrNeedMoreData", err)
	}
}

func TestParserBufferQuota(t *testing.T) {
	p := newTestParser()
	if err := p.Feed(make([]byte, testMaxBuffered+1)); !errors.Is(err, ErrBadData) {
		t.Fatalf("got %v, want ErrBadData on buffer quota", err)
	}
}
