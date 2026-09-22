package framemux

import (
	"bytes"
	"errors"
	"testing"
)

// All 256 byte values must round-trip, including the delimiter and escape
// bytes themselves.
func TestRoundTripAllByteValues(t *testing.T) {
	payload := make([]byte, 256)
	for i := range payload {
		payload[i] = byte(i)
	}
	f := Frame{Type: TypeData, Stream: 3, Seq: 42, Epoch: 0, Payload: payload}
	enc := AppendFrame(nil, f)

	got, n, err := DecodeFrame(enc[2:], 0)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if n != len(enc)-2 {
		t.Fatalf("consumed %d, want %d", n, len(enc)-2)
	}
	if got.Stream != 3 || got.Seq != 42 || got.Epoch != 0 {
		t.Fatalf("header mismatch: %+v", got)
	}
	if !bytes.Equal(got.Payload, payload) {
		t.Fatal("payload mismatch after round trip")
	}

	// Same through a Conn pair.
	a, b := NewConn(Config{}), NewConn(Config{})
	if err := a.Send(3, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write(a.TakeOutput()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rf, ok := b.Recv()
	if !ok {
		t.Fatal("no frame delivered")
	}
	if !bytes.Equal(rf.Payload, payload) {
		t.Fatal("conn round-trip payload mismatch")
	}
	if b.Stats().BadFrames != 0 {
		t.Fatalf("unexpected bad frames: %+v", b.Stats())
	}
}

// Every epoch's escape rules must keep the escaped alphabet disjoint from
// the delimiter and the escape byte.
func TestEscapeConfigSafety(t *testing.T) {
	for e := 0; e < 256; e++ {
		cfg := configForEpoch(uint8(e))
		if cfg.Esc == Magic0 || cfg.Esc == Magic1 {
			t.Fatalf("epoch %d: escape byte collides with delimiter", e)
		}
		for _, b := range []byte{Magic0, Magic1, cfg.Esc} {
			if enc := b ^ cfg.Xor; enc == Magic0 || enc == Magic1 || enc == cfg.Esc {
				t.Fatalf("epoch %d: escaped %#02x -> %#02x collides", e, b, enc)
			}
		}
	}
}

// A truncated frame is "need more data"; a corrupted frame is "bad data".
// The two must never be confused.
func TestNeedMoreDataVsBadData(t *testing.T) {
	f := Frame{Type: TypeData, Stream: 9, Seq: 7, Epoch: 0,
		Payload: []byte{0x7D, 0x7E, 0x5A, 0x00, 0xFF}}
	enc := AppendFrame(nil, f)
	body := enc[2:]

	cuts := []int{0, 1, headerLen - 1, headerLen, headerLen + 2, len(body) - 1}
	// Also cut right after an escape byte (dangling escape).
	for i, b := range body {
		if b == configForEpoch(0).Esc {
			cuts = append(cuts, i+1)
			break
		}
	}
	for _, cut := range cuts {
		if cut >= len(body) {
			continue
		}
		if _, _, err := DecodeFrame(body[:cut], 0); !errors.Is(err, ErrNeedMoreData) {
			t.Fatalf("cut=%d: got %v, want ErrNeedMoreData", cut, err)
		}
	}

	bad := append([]byte(nil), body...)
	bad[len(bad)-1] ^= 0xFF // corrupt the CRC
	if _, _, err := DecodeFrame(bad, 0); !errors.Is(err, ErrBadData) {
		t.Fatalf("corrupt CRC: got %v, want ErrBadData", err)
	}
	if errors.Is(DecodeFrameErr(bad), ErrNeedMoreData) {
		t.Fatal("bad data misreported as need-more-data")
	}
}

func DecodeFrameErr(body []byte) error {
	_, _, err := DecodeFrame(body, 0)
	return err
}
