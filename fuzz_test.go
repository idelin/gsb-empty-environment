package chain

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"testing"
)

// TestRandomChunking feeds the same stream through many random chunk plans;
// the accepted frames must be identical regardless of byte boundaries.
func TestRandomChunking(t *testing.T) {
	b := NewBuilder()
	want := make([][]byte, 0, 50)
	for i := 0; i < 50; i++ {
		pl := bytes.Repeat([]byte{byte(i)}, i%7)
		f := b.Add(pl)
		want = append(want, f.Encode())
	}
	stream := b.Bytes()

	base := parseAll(t, stream, []int{len(stream)})

	rng := rand.New(rand.NewPCG(1, 2))
	for iter := 0; iter < 200; iter++ {
		var plan []int
		covered := 0
		for covered < len(stream) {
			n := 1 + rng.IntN(37)
			plan = append(plan, n)
			covered += n
		}
		got := parseAll(t, stream, plan)
		if len(got) != len(base) {
			t.Fatalf("iter=%d frames=%d want %d", iter, len(got), len(base))
		}
		for i := range got {
			if !bytes.Equal(got[i], base[i]) {
				t.Fatalf("iter=%d frame %d differs", iter, i)
			}
		}
	}
}

func parseAll(t *testing.T, stream []byte, plan []int) [][]byte {
	t.Helper()
	p := NewParser()
	pos := 0
	var out [][]byte
	for _, n := range plan {
		end := pos + n
		if end > len(stream) {
			end = len(stream)
		}
		if end > pos {
			p.Append(stream[pos:end])
			pos = end
		}
		for {
			ev, err := p.Next()
			if errors.Is(err, ErrNeedMoreData) {
				break
			}
			if err != nil {
				t.Fatalf("plan chunk: %v", err)
			}
			if ev.Kind == EventFrame {
				out = append(out, ev.Frame.Encode())
			}
		}
		if pos == len(stream) {
			break
		}
	}
	return out
}

// FuzzRoundTrip checks that any well-formed frame decoded by DecodeFrame
// round-trips, and that short prefixes always mean "need more data".
func FuzzRoundTrip(f *testing.F) {
	f.Add([]byte("hello"), uint8(3))
	f.Fuzz(func(t *testing.T, payload []byte, n uint8) {
		if len(payload) > 512 {
			payload = payload[:512]
		}
		fr := Frame{Seq: uint64(n), PrevDigest: ZeroDigest(), Payload: payload}
		raw := fr.Encode()

		if cut := int(n) % (len(raw) + 1); cut < len(raw) {
			if _, _, err := DecodeFrame(raw[:cut], MaxPayload); !errors.Is(err, ErrNeedMoreData) {
				t.Fatalf("prefix %d/%d: %v", cut, len(raw), err)
			}
		}

		got, m, err := DecodeFrame(raw, MaxPayload)
		if err != nil || m != len(raw) {
			t.Fatalf("decode: err=%v consumed=%d/%d", err, m, len(raw))
		}
		if got.Seq != uint64(n) || !bytes.Equal(got.Payload, payload) {
			t.Fatal("round-trip mismatch")
		}
		if !bytes.Equal(got.Encode(), raw) {
			t.Fatal("re-encode mismatch")
		}
	})
}
