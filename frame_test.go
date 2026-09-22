package fsm

import (
	"bytes"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := []Frame{
		{Type: TypeHello},
		{Type: TypePing, Payload: []byte("x")},
		{Type: TypeData, Seq: 42, Payload: []byte("payload")},
		{Type: TypeBye},
	}
	var stream []byte
	for _, f := range in {
		stream = append(stream, Encode(f)...)
	}
	p := NewParser(0)
	p.Feed(stream)
	for i, want := range in {
		got, err := p.Next()
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if got.Type != want.Type || got.Seq != want.Seq || !bytes.Equal(got.Payload, want.Payload) {
			t.Fatalf("frame %d: got %+v want %+v", i, got, want)
		}
	}
	if _, err := p.Next(); err != ErrNeedMoreData {
		t.Fatalf("drained parser: %v, want ErrNeedMoreData", err)
	}
}

func TestParserArbitraryChunking(t *testing.T) {
	stream := append(Encode(Frame{Type: TypeHello}),
		Encode(Frame{Type: TypeData, Seq: 7, Payload: []byte("chunked")})...)
	for _, size := range []int{1, 2, 3, 5, len(stream) - 1, len(stream)} {
		p := NewParser(0)
		var got []*Frame
		for i := 0; i < len(stream); i += size {
			end := i + size
			if end > len(stream) {
				end = len(stream)
			}
			p.Feed(stream[i:end])
			for {
				f, err := p.Next()
				if err == ErrNeedMoreData {
					break
				}
				if err != nil {
					t.Fatalf("chunk=%d: %v", size, err)
				}
				got = append(got, f)
			}
		}
		if len(got) != 2 || got[0].Type != TypeHello || got[1].Seq != 7 ||
			string(got[1].Payload) != "chunked" {
			t.Fatalf("chunk=%d: got %+v", size, got)
		}
	}
}

func TestParserDistinguishesNeedMoreFromBad(t *testing.T) {
	p := NewParser(0)
	if _, err := p.Next(); err != ErrNeedMoreData {
		t.Fatalf("empty: %v", err)
	}
	p.Feed([]byte{0x00}) // half a header
	if _, err := p.Next(); err != ErrNeedMoreData {
		t.Fatalf("half header: %v", err)
	}
	p.Feed([]byte{0x05, TypePing, 'a'}) // declares 5, only 2 present
	if _, err := p.Next(); err != ErrNeedMoreData {
		t.Fatalf("short body: %v", err)
	}
	if p.Buffered() != 4 {
		t.Fatalf("incomplete frame not retained: buffered=%d", p.Buffered())
	}
}

func TestParserLengthQuotaCheckedBeforeAllocation(t *testing.T) {
	p := NewParser(8)
	// Only the 2 header bytes arrive, declaring 0xffff bytes. The quota
	// error must fire immediately, before the body exists to allocate.
	p.Feed([]byte{0xff, 0xff})
	_, err := p.Next()
	if !IsBadFrame(err) {
		t.Fatalf("oversized: %v, want BadFrameError", err)
	}
	if err == ErrNeedMoreData {
		t.Fatal("oversized length misreported as insufficient data")
	}
}

func TestParserRejectsZeroLengthAndShortData(t *testing.T) {
	p := NewParser(0)
	p.Feed([]byte{0x00, 0x00}) // length 0: no room for the type byte
	if _, err := p.Next(); !IsBadFrame(err) {
		t.Fatalf("zero length: %v", err)
	}
	p = NewParser(0)
	p.Feed([]byte{0x00, 0x03, TypeData, 0x01, 0x02}) // DATA with 2-byte seq
	if _, err := p.Next(); !IsBadFrame(err) {
		t.Fatalf("short DATA: %v", err)
	}
}
