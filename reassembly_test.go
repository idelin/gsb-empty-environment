package fragmentreassembly_test

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"testing"
	"time"

	fragmentreassembly "fragmentreassembly"
)

type chunkReader struct {
	data []byte
	rng  *rand.Rand
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := r.rng.Intn(len(p)) + 1
	if n > len(r.data) {
		n = len(r.data)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, io.EOF
	}
	return n, nil
}

func makeFragment(t *testing.T, id uint64, total string, offset uint32, payload []byte) fragmentreassembly.Fragment {
	t.Helper()
	return fragmentreassembly.Fragment{
		Header: fragmentreassembly.Header{
			MessageID: id,
			TotalSize: uint32(len(total)),
			Offset:    offset,
		},
		Payload: payload,
	}
}

func marshalFrame(t *testing.T, frag fragmentreassembly.Fragment) []byte {
	t.Helper()
	frame, err := frag.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return frame
}

func TestOutOfOrderReassemblyAllOrders(t *testing.T) {
	message := []byte("0123456789ABCDEF")
	frags := []fragmentreassembly.Fragment{
		makeFragment(t, 42, string(message), 0, message[0:5]),
		makeFragment(t, 42, string(message), 5, message[5:10]),
		makeFragment(t, 42, string(message), 10, message[10:16]),
	}

	orders := [][]int{
		{0, 1, 2},
		{2, 1, 0},
		{1, 2, 0},
	}
	for _, order := range orders {
		t.Run("", func(t *testing.T) {
			r, err := fragmentreassembly.NewReassembler(fragmentreassembly.Config{})
			if err != nil {
				t.Fatalf("NewReassembler() error = %v", err)
			}

			var result fragmentreassembly.Result
			for _, idx := range order {
				result, err = r.Add(frags[idx])
				if err != nil {
					t.Fatalf("Add(%d) error = %v", idx, err)
				}
			}
			if !result.Complete {
				t.Fatal("expected complete message")
			}
			if !bytes.Equal(result.Data, message) {
				t.Fatalf("data = %q, want %q", result.Data, message)
			}

			repeat, err := r.Add(frags[1])
			if err != nil {
				t.Fatalf("duplicate Add error = %v", err)
			}
			if !repeat.Complete || !repeat.Duplicate || !bytes.Equal(repeat.Data, message) {
				t.Fatalf("duplicate result = %+v", repeat)
			}
		})
	}
}

func TestOverlappingFragmentsMustAgree(t *testing.T) {
	message := []byte("abcdefgh")
	first := makeFragment(t, 7, string(message), 0, message[0:5])
	second := makeFragment(t, 7, string(message), 4, message[4:8])

	r, err := fragmentreassembly.NewReassembler(fragmentreassembly.Config{})
	if err != nil {
		t.Fatalf("NewReassembler() error = %v", err)
	}
	if _, err := r.Add(first); err != nil {
		t.Fatalf("first Add error = %v", err)
	}

	bad := second
	bad.Payload = append([]byte(nil), second.Payload...)
	bad.Payload[0] = 'X'
	_, err = r.Add(bad)
	if !errors.Is(err, fragmentreassembly.ErrConflict) {
		t.Fatalf("bad overlap error = %v, want conflict", err)
	}

	good, err := r.Add(second)
	if err != nil {
		t.Fatalf("matching overlap error = %v", err)
	}
	if !good.Complete || !bytes.Equal(good.Data, message) {
		t.Fatalf("result = %+v", good)
	}
}

func TestLengthQuotaCheckedBeforeAllocation(t *testing.T) {
	t.Run("per-message", func(t *testing.T) {
		r, err := fragmentreassembly.NewReassembler(fragmentreassembly.Config{MaxMessageSize: 4})
		if err != nil {
			t.Fatalf("NewReassembler() error = %v", err)
		}
		header := fragmentreassembly.Header{MessageID: 1, TotalSize: 5, Length: 1}
		if err := r.Allow(header); !errors.Is(err, fragmentreassembly.ErrMessageTooLarge) {
			t.Fatalf("Allow error = %v, want too large", err)
		}
		_, err = r.Add(fragmentreassembly.Fragment{Header: header, Payload: []byte("a")})
		if !errors.Is(err, fragmentreassembly.ErrMessageTooLarge) {
			t.Fatalf("Add error = %v, want too large", err)
		}
	})

	t.Run("global", func(t *testing.T) {
		r, err := fragmentreassembly.NewReassembler(fragmentreassembly.Config{GlobalBytes: 5})
		if err != nil {
			t.Fatalf("NewReassembler() error = %v", err)
		}
		first := fragmentreassembly.Fragment{
			Header:  fragmentreassembly.Header{MessageID: 1, TotalSize: 3, Length: 1},
			Payload: []byte("a"),
		}
		second := fragmentreassembly.Header{MessageID: 2, TotalSize: 3, Length: 1}
		if _, err := r.Add(first); err != nil {
			t.Fatalf("first Add error = %v", err)
		}
		if err := r.Allow(second); !errors.Is(err, fragmentreassembly.ErrQuotaExceeded) {
			t.Fatalf("second Allow error = %v, want quota", err)
		}
	})
}

func TestExpiryRemovesOldBufferAndCompletionDedup(t *testing.T) {
	base := time.Unix(100, 0)
	message := []byte("abcdefgh")
	r, err := fragmentreassembly.NewReassembler(fragmentreassembly.Config{
		GlobalBytes: 8,
		TTL:         time.Minute,
	})
	if err != nil {
		t.Fatalf("NewReassembler() error = %v", err)
	}

	first := makeFragment(t, 9, string(message), 0, message[0:4])
	if _, err := r.AddAt(first, base); err != nil {
		t.Fatalf("first AddAt error = %v", err)
	}
	late := makeFragment(t, 9, string(message), 4, []byte("ZZZZ"))
	_, err = r.AddAt(late, base.Add(time.Minute))
	if err != nil {
		t.Fatalf("late fragment after expiry should start a fresh buffer, got %v", err)
	}
	oldTail := makeFragment(t, 9, string(message), 4, message[4:8])
	if _, err := r.AddAt(oldTail, base.Add(time.Minute)); !errors.Is(err, fragmentreassembly.ErrConflict) {
		t.Fatalf("old tail error = %v, want conflict", err)
	}

	completed := makeFragment(t, 10, "x", 0, []byte("x"))
	result, err := r.AddAt(completed, base.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("completion after expired reservation error = %v", err)
	}
	if !result.Complete || string(result.Data) != "x" {
		t.Fatalf("result = %+v", result)
	}
	repeat, err := r.AddAt(completed, base.Add(2*time.Minute+time.Second))
	if err != nil {
		t.Fatalf("completed duplicate error = %v", err)
	}
	if !repeat.Duplicate {
		t.Fatal("expected completed duplicate")
	}

	_, err = r.AddAt(completed, base.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("fragment after completion expiry should be accepted as new state, got %v", err)
	}
}

func TestStreamFramesAcceptArbitraryChunks(t *testing.T) {
	message := []byte("stream-data")
	frags := []fragmentreassembly.Fragment{
		makeFragment(t, 3, string(message), 0, message[0:4]),
		makeFragment(t, 3, string(message), 4, message[4:8]),
		makeFragment(t, 3, string(message), 8, message[8:11]),
	}
	var wire bytes.Buffer
	for _, frag := range frags {
		wire.Write(marshalFrame(t, frag))
	}

	reader := &chunkReader{data: wire.Bytes(), rng: rand.New(rand.NewSource(1))}
	r, err := fragmentreassembly.NewReassembler(fragmentreassembly.Config{})
	if err != nil {
		t.Fatalf("NewReassembler() error = %v", err)
	}

	var result fragmentreassembly.Result
	for i := 0; i < len(frags); i++ {
		frag, err := fragmentreassembly.ReadFrame(reader, nil)
		if err != nil {
			t.Fatalf("ReadFrame(%d) error = %v", i, err)
		}
		result, err = r.Add(frag)
		if err != nil {
			t.Fatalf("Add(%d) error = %v", i, err)
		}
	}
	if !result.Complete || !bytes.Equal(result.Data, message) {
		t.Fatalf("result = %+v", result)
	}
}

func TestFrameParserDistinguishesMissingAndBadData(t *testing.T) {
	good := makeFragment(t, 1, "abc", 0, []byte("abc"))
	frame := marshalFrame(t, good)

	for _, n := range []int{0, 1, fragmentreassembly.HeaderSize - 1, len(frame) - 1} {
		_, _, err := fragmentreassembly.UnmarshalFrame(frame[:n])
		if !errors.Is(err, fragmentreassembly.ErrNeedMoreData) {
			t.Fatalf("len %d error = %v, want need more data", n, err)
		}
	}

	badMagic := append([]byte(nil), frame...)
	badMagic[0] ^= 0xFF
	if _, _, err := fragmentreassembly.UnmarshalFrame(badMagic); !errors.Is(err, fragmentreassembly.ErrBadData) {
		t.Fatalf("bad magic error = %v", err)
	}

	badPayload := append([]byte(nil), frame...)
	badPayload[fragmentreassembly.HeaderSize] ^= 0xFF
	if _, _, err := fragmentreassembly.UnmarshalFrame(badPayload); !errors.Is(err, fragmentreassembly.ErrBadData) {
		t.Fatalf("bad payload error = %v", err)
	}

	badLength := append([]byte(nil), frame...)
	badLength[24] ^= 0xFF
	if _, _, err := fragmentreassembly.UnmarshalFrame(badLength); !errors.Is(err, fragmentreassembly.ErrBadData) {
		t.Fatalf("bad length error = %v", err)
	}

	tooFar := makeFragment(t, 1, "abc", 2, []byte("abc"))
	if _, err := tooFar.Marshal(); !errors.Is(err, fragmentreassembly.ErrBadData) {
		t.Fatalf("marshal out-of-range error = %v", err)
	}
}

func TestGateDrainAllowsNextFrameAfterRejection(t *testing.T) {
	allowed := makeFragment(t, 1, "ok", 0, []byte("ok"))
	rejected := makeFragment(t, 2, "too large", 0, []byte("too large"))
	var wire bytes.Buffer
	wire.Write(marshalFrame(t, rejected))
	wire.Write(marshalFrame(t, allowed))

	r, err := fragmentreassembly.NewReassembler(fragmentreassembly.Config{MaxMessageSize: 2})
	if err != nil {
		t.Fatalf("NewReassembler() error = %v", err)
	}
	reader := bytes.NewReader(wire.Bytes())

	_, err = fragmentreassembly.ReadFrame(reader, r.Allow)
	if !errors.Is(err, fragmentreassembly.ErrMessageTooLarge) {
		t.Fatalf("rejected frame error = %v", err)
	}
	frag, err := fragmentreassembly.ReadFrame(reader, r.Allow)
	if err != nil {
		t.Fatalf("next frame error = %v", err)
	}
	result, err := r.Add(frag)
	if err != nil {
		t.Fatalf("Add error = %v", err)
	}
	if !result.Complete || string(result.Data) != "ok" {
		t.Fatalf("result = %+v", result)
	}
}
