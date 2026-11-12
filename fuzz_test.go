package netsec

import (
	"encoding/base64"
	"errors"
	"math/rand"
	"reflect"
	"strconv"
	"testing"
)

// TestParserAgreement drives both independent paths over shuffled valid
// envelopes, byte-mutated samples and random junk. They must never disagree on
// accept/reject or on derived semantics, and Receive must never report
// divergence on grammar-accepted input.
func TestParserAgreement(t *testing.T) {
	rng := rand.New(rand.NewSource(0x3110311))

	for i := 0; i < 200; i++ {
		var input []byte
		switch rng.Intn(3) {
		case 0:
			input = randomEnvelope(rng)
		case 1:
			input = mutate(rng, randomEnvelope(rng))
		case 2:
			input = randomJunk(rng)
		}

		handFields, handErr := parseHand(input)
		stdFields, stdErr := parseStd(input)
		if (handErr == nil) != (stdErr == nil) {
			t.Fatalf("iter %d parser accept mismatch on %q: hand=%v std=%v",
				i, input, handErr, stdErr)
		}
		if handErr == nil && !reflect.DeepEqual(handFields, stdFields) {
			t.Fatalf("iter %d parser field mismatch on %q:\nhand=%#v\nstd =%#v",
				i, input, handFields, stdFields)
		}

		if handErr == nil {
			if _, err := Receive(input); err != nil && errors.Is(err, ErrParserDivergence) {
				t.Fatalf("iter %d divergence on %q: %v", i, input, err)
			}
		} else if _, err := Receive(input); err == nil {
			t.Fatalf("iter %d Receive accepted but parsers rejected %q", i, input)
		}
	}
}

// TestCanonicalUintShape pins the exact integer grammar shared by both paths.
func TestCanonicalUintShape(t *testing.T) {
	valid := map[string]uint64{"0": 0, "1": 1, "10": 10, "4294967295": 4294967295}
	for text, want := range valid {
		got, err := parseCanonicalUint(text, 32)
		if err != nil {
			t.Fatalf("canonical %q rejected: %v", text, err)
		}
		if got != want {
			t.Fatalf("canonical %q = %d, want %d", text, got, want)
		}
	}

	invalid := []string{"", "00", "01", "-0", "+1", "1.0", "1e3", "0x1", " 1", "1 "}
	for _, text := range invalid {
		if _, err := parseCanonicalUint(text, 64); !errors.Is(err, ErrMalformed) {
			t.Fatalf("non-canonical %q: err=%v, want ErrMalformed", text, err)
		}
	}

	if _, err := parseCanonicalUint("4294967296", 32); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("uint32 overflow: err=%v, want ErrOutOfRange", err)
	}
	if _, err := parseCanonicalUint("18446744073709551616", 64); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("uint64 overflow: err=%v, want ErrOutOfRange", err)
	}
	if got, err := parseCanonicalUint("18446744073709551615", 64); err != nil || got != ^uint64(0) {
		t.Fatalf("max uint64: got %d err=%v", got, err)
	}
}

// randomEnvelope builds a schema-valid envelope with shuffled field order.
func randomEnvelope(rng *rand.Rand) []byte {
	size := rng.Intn(32)
	payload := make([]byte, size)
	if _, err := rng.Read(payload); err != nil {
		panic(err)
	}
	msg := &Message{
		Version: rng.Uint32(),
		Length:  uint64(size),
		Payload: payload,
	}
	msg.Digest = Digest(msg)

	values := map[string]string{
		FieldVersion: strconv.FormatUint(uint64(msg.Version), 10),
		FieldLength:  strconv.FormatUint(msg.Length, 10),
		FieldPayload: jsonString(base64.StdEncoding.EncodeToString(msg.Payload)),
		FieldDigest:  jsonString(msg.Digest),
	}
	order := rng.Perm(len(fieldOrder))
	parts := make([]string, 0, len(fieldOrder))
	for _, idx := range order {
		name := fieldOrder[idx]
		parts = append(parts, jsonString(name)+":"+values[name])
	}
	return []byte("{" + joinComma(parts) + "}")
}

// mutate applies one grammar- or schema-breaking change to a valid sample.
func mutate(rng *rand.Rand, in []byte) []byte {
	if len(in) == 0 {
		return in
	}
	out := make([]byte, len(in))
	copy(out, in)
	switch rng.Intn(6) {
	case 0:
		pos := rng.Intn(len(out))
		out[pos] = byte(" \t{},:\"[]"[rng.Intn(9)])
	case 1:
		pos := rng.Intn(len(out))
		return append(out[:pos], out[pos+1:]...)
	case 2:
		pos := rng.Intn(len(out) + 1)
		return append(out[:pos], append([]byte{byte('0' + rng.Intn(10))}, out[pos:]...)...)
	case 3:
		return append(out, ' ', 'x')
	case 4:
		return append([]byte(`{"extra":1},`), out...)
	default:
		pos := rng.Intn(len(out))
		out[pos] = 0
	}
	return out
}

func randomJunk(rng *rand.Rand) []byte {
	alphabet := `{}[]:,."0123456789truefalsnull -+eE\u`
	n := rng.Intn(48)
	out := make([]byte, n)
	for i := range out {
		out[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return out
}

func joinComma(parts []string) string {
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += ","
		}
		out += part
	}
	return out
}
