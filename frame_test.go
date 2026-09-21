package proto

import (
	"errors"
	"testing"
)

func encodeMust(t *testing.T, typ MsgType, seq uint32, payload []byte) []byte {
	t.Helper()
	b, err := Encode(typ, seq, payload)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return b
}

func TestParserArbitraryChunking(t *testing.T) {
	f1 := encodeMust(t, MsgHello, 0, nil)
	f2 := encodeMust(t, MsgData, 7, []byte("payload"))
	f3 := encodeMust(t, MsgPing, 0, nil)
	wire := append(append(append([]byte{}, f1...), f2...), f3...)

	// Feed one byte at a time: the parser must reassemble all three frames.
	var p Parser
	var got []Frame
	for _, b := range wire {
		p.Feed([]byte{b})
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
	if len(got) != 3 {
		t.Fatalf("got %d frames, want 3", len(got))
	}
	if got[0].Type != MsgHello || got[2].Type != MsgPing {
		t.Fatalf("unexpected types: %v %v", got[0].Type, got[2].Type)
	}
	if got[1].Seq != 7 || string(got[1].Payload) != "payload" {
		t.Fatalf("data frame mismatch: seq=%d payload=%q", got[1].Seq, got[1].Payload)
	}
}

func TestParserNeedMoreVsBadData(t *testing.T) {
	// Truncated header and truncated body are "need more", never "bad".
	for _, prefix := range [][]byte{
		{},
		{0x00},
		{0x00, 0x05},
		{0x00, 0x05, byte(MsgHello)},
		{0x00, 0x05, byte(MsgHello), 0x01, 0x02},
	} {
		var p Parser
		p.Feed(prefix)
		if _, err := p.Next(); !errors.Is(err, ErrNeedMoreData) {
			t.Fatalf("prefix %x: got %v, want ErrNeedMoreData", prefix, err)
		}
	}
	// Zero length and over-quota length are bad data, not "need more".
	for _, bad := range [][]byte{
		{0x00, 0x00, byte(MsgHello)},      // length 0: smaller than the type byte
		{0x04, 0x01, byte(MsgHello)},      // length 1025 > quota 1024
		{0xff, 0xff, byte(MsgHello)},      // length 65535 > quota
		{0x00, 0x02, byte(MsgData), 0x01}, // data frame without full seq
	} {
		var p Parser
		p.Feed(bad)
		_, err := p.Next()
		var bde *BadDataError
		if !errors.As(err, &bde) {
			t.Fatalf("input %x: got %v, want *BadDataError", bad, err)
		}
	}
}

func TestParserQuotaCheckedBeforeAllocation(t *testing.T) {
	// A header declaring the maximum u16 length must be rejected
	// immediately, with zero payload bytes present.
	var p Parser
	p.Feed([]byte{0xff, 0xfe, byte(MsgData)})
	_, err := p.Next()
	var bde *BadDataError
	if !errors.As(err, &bde) {
		t.Fatalf("got %v, want *BadDataError", err)
	}
}

func TestEncodeRejectsOversized(t *testing.T) {
	if _, err := Encode(MsgData, 1, make([]byte, MaxFrameSize)); err == nil {
		t.Fatal("Encode of oversized payload should fail")
	}
}
